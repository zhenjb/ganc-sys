package service

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// INT-T06 — Trade batch pipeline.
//
// Drains the fills produced by matching (INT-T05) and settles them through the
// same build → prove → submit shape as the core deposit/withdraw path, but for
// trades: apply fills to off-chain state (STATE-T06 — the ONLY step that moves
// balances), assemble a SettlementUpdate(+trades[]) with tradesRoot/ordersRoot
// (STATE-T07/T08), append the 8 public inputs [6]=tradesRoot/[7]=ordersRoot,
// build the trade witness (STATE-T09), prove (Wave-1 stub) and submit
// (MsgSubmitBatchProof shape). On any prove/submit failure the off-chain state
// is rolled back to its pre-apply snapshot and the fills are re-enqueued, so the
// sequencer retries the identical (deterministic) batch — self-healing, no
// double-spend, no x/bank movement (the chain only commits the new root).

// CommittedRootSink is notified after a TRADE batch is accepted on-chain, so the
// CORE settlement cursor (owned by OffchainSettlementService) can advance to the
// trade's new root and stay in lockstep with the chain. Without it the cursor
// lags after every trade and the next core deposit/withdraw batch fails to build
// (DB-1). Implemented by *OffchainSettlementService; nil when off-chain
// settlement is disabled.
type CommittedRootSink interface {
	AdvanceCommittedRoot(ctx context.Context, newCommittedRoot, batchID string) error
}

// ownerDenom identifies one (owner, denom) account for balance snapshots.
type ownerDenom struct {
	owner string
	denom string
}

// availReserved holds an account's available + reserved amounts (integer strings).
type availReserved struct {
	avail    string
	reserved string
}

// SettleTradesOnce drains all pending fills and settles them, grouped per market
// (each market is one batch — TradeApplier is single-market). Returns whether at
// least one market batch settled, plus the first error encountered. Idle (no
// fills) returns (false, nil).
func (s *RealOrderService) SettleTradesOnce(ctx context.Context) (bool, error) {
	drained := s.queue.Drain()
	if len(drained) == 0 {
		return false, nil
	}

	byMarket := map[string][]types.Fill{}
	marketIDs := make([]string, 0)
	for _, f := range drained {
		if _, ok := byMarket[f.Market]; !ok {
			marketIDs = append(marketIDs, f.Market)
		}
		byMarket[f.Market] = append(byMarket[f.Market], f)
	}
	sort.Strings(marketIDs) // deterministic settle order

	settledAny := false
	var firstErr error
	for _, id := range marketIDs {
		fills := byMarket[id]
		market, ok := s.markets.Get(id)
		if !ok {
			// Unknown market — requeue and surface the error (should not happen).
			s.queue.Enqueue(fills)
			if firstErr == nil {
				firstErr = fmt.Errorf("trade settlement: unknown market %q", id)
			}
			continue
		}
		if err := s.settleMarket(ctx, market, fills); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		settledAny = true
	}
	return settledAny, firstErr
}

// settleMarket runs the full pipeline for one market's fills under the
// single-writer lock. On failure after apply it rolls the manager back to the
// pre-apply snapshot; on a transient prove/submit failure it also re-enqueues
// the fills for retry. A deterministic build/data error is NOT re-enqueued (it
// would loop) — it is logged via the returned error and the fills are dropped
// from the queue (the matched book state is unaffected).
func (s *RealOrderService) settleMarket(ctx context.Context, market types.Market, fills []types.Fill) (err error) {
	s.matchMu.Lock()
	defer s.matchMu.Unlock()

	book := s.books.Book(market.Market)
	oldRoot := s.manager.Root()
	snap := s.manager.Snapshot() // apply-level rollback point

	commit, sides, witnessOrders, derr := s.orderDataForFillsLocked(book, fills)
	if derr != nil {
		return fmt.Errorf("trade settlement: order data: %w", derr) // data error — do not requeue
	}

	accounts := touchedAccounts(fills, market, state.FeeAccountOwner)
	oldBal := s.snapshotBalancesLocked(accounts)

	// Apply: the only step that moves balances (consume reserved, credit
	// counterparties + fee). On error it rolls the manager back itself; requeue.
	res, aerr := state.NewTradeApplier("").Apply(s.manager, book, fills, market, sides)
	if aerr != nil {
		s.queue.Enqueue(fills)
		return fmt.Errorf("trade settlement: apply: %w", aerr)
	}
	newBal := s.snapshotBalancesLocked(accounts)

	// rollbackRequeue restores balances and re-queues the fills for a retry.
	rollbackRequeue := func(cause error, stage string) error {
		s.manager.Rollback(snap)
		s.queue.Enqueue(fills)
		return fmt.Errorf("trade settlement: %s: %w", stage, cause)
	}

	upd, com, berr := s.builder.BuildTradeBatch(batch.TradeBatchInputs{
		Core:    batch.SettlementInputs{OldStateRoot: oldRoot, NewStateRoot: res.NewRoot},
		Fills:   fills,
		Orders:  commit,
		Markets: s.markets,
	})
	if berr != nil {
		return rollbackRequeue(berr, "build")
	}

	publicInputs, perr := batch.BuildPublicInputsWithTrades(upd, com)
	if perr != nil {
		return rollbackRequeue(perr, "public inputs")
	}

	witness, werr := buildTradeWitness(oldRoot, res.NewRoot, com, fills, witnessOrders, accounts, oldBal, newBal)
	if werr != nil {
		return rollbackRequeue(werr, "witness")
	}

	proof, prerr := s.tradeProver.ProveTrade(ctx, upd, com, witness, publicInputs)
	if prerr != nil {
		return rollbackRequeue(prerr, "prove")
	}

	txHash, accepted, serr := s.tradeSubmitter.SubmitTrade(ctx, upd, com, proof)
	if serr != nil {
		return rollbackRequeue(serr, "submit")
	}
	if !accepted {
		return rollbackRequeue(fmt.Errorf("batch %s not accepted", upd.BatchID), "submit")
	}

	// DB-1: the trade just advanced the SHARED manager root AND the on-chain root,
	// but the core pipeline has no pending rows to commit. Sync the core settlement
	// cursor to this new root so the NEXT core deposit/withdraw batch continues from
	// it instead of failing to build ("cannot continue transition chain from <stale
	// root>"). Best-effort: the trade is already on-chain, so a sink error is logged,
	// not rolled back (rolling back the off-chain state would desync it from chain).
	if s.committedRootSink != nil {
		if serr := s.committedRootSink.AdvanceCommittedRoot(ctx, res.NewRoot, upd.BatchID); serr != nil {
			log.Printf("[trade-settlement] WARNING batch=%s settled on-chain but core cursor advance failed: %v (next core batch may fail to build until reconciled)",
				upd.BatchID, serr)
		}
	}

	log.Printf("[trade-settlement] SETTLED batch=%s market=%s fills=%d newRoot=%s tx=%s",
		upd.BatchID, market.Market, len(fills), res.NewRoot, txHash)
	return nil
}

// orderDataForFillsLocked rebuilds, for every order referenced by the fills, its
// OrderCommitmentInput (STATE-T07), side map (STATE-T06) and witness order input
// (STATE-T09). Post-batch remaining/filled comes from the current book (an order
// absent from the book is fully filled). Called with matchMu held.
func (s *RealOrderService) orderDataForFillsLocked(book *state.Orderbook, fills []types.Fill) ([]state.OrderCommitmentInput, map[string]types.OrderSide, []batch.TradeWitnessOrderInput, error) {
	seen := map[string]bool{}
	hashes := make([]string, 0, len(fills)*2)
	for _, f := range fills {
		for _, h := range []string{f.MakerOrderHash, f.TakerOrderHash} {
			if !seen[h] {
				seen[h] = true
				hashes = append(hashes, h)
			}
		}
	}
	sort.Strings(hashes) // deterministic

	commit := make([]state.OrderCommitmentInput, 0, len(hashes))
	sides := make(map[string]types.OrderSide, len(hashes))
	witnessOrders := make([]batch.TradeWitnessOrderInput, 0, len(hashes))
	for _, h := range hashes {
		rec, ok := s.orderRecords[h]
		if !ok {
			return nil, nil, nil, fmt.Errorf("no order record for %s", h)
		}
		remaining, filled := "0", true
		if rem, e := book.RemainingQty(h); e == nil {
			remaining, filled = rem, false
		}
		sides[h] = rec.order.Side
		commit = append(commit, state.OrderCommitmentInput{
			OrderHash: h,
			Owner:     rec.order.Owner,
			Side:      rec.order.Side,
			Price:     rec.order.Price,
			Qty:       rec.order.Qty,
			Remaining: remaining,
			Filled:    filled,
			Sequence:  rec.sequence,
		})
		witnessOrders = append(witnessOrders, batch.TradeWitnessOrderInput{
			Order:     rec.order,
			Filled:    filled,
			Remaining: remaining,
		})
	}
	return commit, sides, witnessOrders, nil
}

// snapshotBalancesLocked reads available+reserved for each account (matchMu held).
func (s *RealOrderService) snapshotBalancesLocked(accounts []ownerDenom) map[ownerDenom]availReserved {
	out := make(map[ownerDenom]availReserved, len(accounts))
	for _, a := range accounts {
		acc := s.manager.Account(a.owner, a.denom)
		reserved := acc.Reserved
		if reserved == "" {
			reserved = "0"
		}
		out[a] = availReserved{avail: acc.Balance, reserved: reserved}
	}
	return out
}

// touchedAccounts is the deterministic set of (owner, denom) accounts a batch's
// fills move: each party's base+quote, plus the fee account's quote. This is
// exactly the set the trade witness needs for its per-denom value-conservation
// check (buyer + seller + feeAccount all present).
func touchedAccounts(fills []types.Fill, market types.Market, feeOwner string) []ownerDenom {
	seen := map[ownerDenom]bool{}
	out := make([]ownerDenom, 0)
	add := func(owner, denom string) {
		k := ownerDenom{owner: owner, denom: denom}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, f := range fills {
		add(f.Buyer, market.QuoteDenom)
		add(f.Buyer, market.BaseDenom)
		add(f.Seller, market.QuoteDenom)
		add(f.Seller, market.BaseDenom)
	}
	add(feeOwner, market.QuoteDenom)
	sort.Slice(out, func(i, j int) bool {
		if out[i].owner != out[j].owner {
			return out[i].owner < out[j].owner
		}
		return out[i].denom < out[j].denom
	})
	return out
}

// buildTradeWitness assembles the STATE-T09 trade witness from the pre/post
// balance snapshots.
func buildTradeWitness(
	oldRoot, newRoot string,
	com types.BatchCommitments,
	fills []types.Fill,
	witnessOrders []batch.TradeWitnessOrderInput,
	accounts []ownerDenom,
	oldBal, newBal map[ownerDenom]availReserved,
) (types.Witness, error) {
	balInputs := make([]batch.TradeWitnessBalanceInput, 0, len(accounts))
	for _, a := range accounts {
		o, n := oldBal[a], newBal[a]
		balInputs = append(balInputs, batch.TradeWitnessBalanceInput{
			Owner:        a.owner,
			Denom:        a.denom,
			OldAvailable: o.avail,
			OldReserved:  o.reserved,
			NewAvailable: n.avail,
			NewReserved:  n.reserved,
		})
	}
	tw, err := batch.BuildTradeWitness(batch.TradeWitnessInputs{
		OldStateRoot: oldRoot,
		NewStateRoot: newRoot,
		OrdersRoot:   com.OrdersRoot,
		TradesRoot:   com.TradesRoot,
		Fills:        fills,
		Orders:       witnessOrders,
		Balances:     balInputs,
	})
	if err != nil {
		return types.Witness{}, err
	}
	return types.Witness{Trade: &tw}, nil
}
