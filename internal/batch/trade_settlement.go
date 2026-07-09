package batch

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T08 — Build trade SettlementUpdate.
//
// Trades settle through the SAME MsgSubmitBatchProof as deposits/withdrawals.
// This assembler packs trades[] + tradeBatchCommitment into a batch-shaped
// SettlementUpdate and appends tradesRoot/ordersRoot (STATE-T07) to the
// BatchCommitments — APPEND-ONLY, so the core deposit/withdraw path and its
// vectors are untouched. A single batch may carry deposits, withdrawals AND
// trades (the chain commits only the state-root transition — no bank msg per
// trade).

const tradeBatchCommitmentDomainTag = "zkdex/batch/tradeBatchCommitment/v0"

// TradeBatchCommitment binds the trade sub-batch's two roots (STATE-T07) into a
// single scalar the SettlementUpdate carries and the chain records:
//
//	tradeBatchCommitment = SHA256( domain | tradesRoot | ordersRoot )
//
// MVP placeholder hash (SHA-256); bumps in lockstep with the ZK-T01 circuit hash.
func TradeBatchCommitment(tradesRoot, ordersRoot string) string {
	var b strings.Builder
	b.WriteString(tradeBatchCommitmentDomainTag)
	b.WriteByte('|')
	b.WriteString(tradesRoot)
	b.WriteByte('|')
	b.WriteString(ordersRoot)
	return hash.SHA256Hex([]byte(b.String()))
}

// TradeBatchInputs is the batch-shaped input for BuildTradeBatch: the core
// deposit/withdraw portion plus the trade portion.
//
//   - Core:   deposits/withdrawals + old/new roots, exactly as SettlementInputs.
//   - Fills:  matched fills (STATE-T05) in matching order.
//   - Orders: the batch's orders with post-batch state (STATE-T07 input), used
//     to derive ordersRoot and the order-nullifier set.
type TradeBatchInputs struct {
	Core   SettlementInputs
	Fills  []types.Fill
	Orders []state.OrderCommitmentInput
}

// BuildTradeBatch assembles a SettlementUpdate (with trades[] +
// tradeBatchCommitment) and its BatchCommitments (with tradesRoot/ordersRoot
// appended), from a batch that may include deposits, withdrawals and/or trades.
//
// Validation (all failures wrap ErrInvalidSettlementInputs; no partial state):
//  1. Roots non-empty, hex-prefixed, strictly different (state changed).
//  2. Non-empty batch: at least one deposit, withdrawal OR fill.
//  3. Each fill: identity fields non-empty, price/qty present, fees non-negative.
//  4. ordersRoot/tradesRoot derived via STATE-T07 (duplicate order nullifier is
//     rejected there).
//
// The core (deposit/withdraw) portion reuses the exact core Build path when
// present, so its single-denom invariant, destination-hash re-derivation and
// BatchID minting are unchanged. Trade-only batches skip the core path and mint
// the BatchID directly.
func (b *SettlementUpdateBuilder) BuildTradeBatch(in TradeBatchInputs) (types.SettlementUpdate, types.BatchCommitments, error) {
	if err := validateRoot(in.Core.OldStateRoot, "oldStateRoot"); err != nil {
		return types.SettlementUpdate{}, types.BatchCommitments{}, err
	}
	if err := validateRoot(in.Core.NewStateRoot, "newStateRoot"); err != nil {
		return types.SettlementUpdate{}, types.BatchCommitments{}, err
	}
	if in.Core.OldStateRoot == in.Core.NewStateRoot {
		return types.SettlementUpdate{}, types.BatchCommitments{}, fmt.Errorf("%w: oldStateRoot == newStateRoot (no-op batch)", ErrInvalidSettlementInputs)
	}

	hasCore := len(in.Core.Deposits) > 0 || len(in.Core.Withdrawals) > 0
	if !hasCore && len(in.Fills) == 0 {
		return types.SettlementUpdate{}, types.BatchCommitments{}, fmt.Errorf("%w: empty batch (no deposits, withdrawals or trades)", ErrInvalidSettlementInputs)
	}

	trades, err := validateFills(in.Fills)
	if err != nil {
		return types.SettlementUpdate{}, types.BatchCommitments{}, err
	}

	// Trade commitments (STATE-T07): ordersRoot binds order set + nullifiers +
	// filled status; tradesRoot binds the fill sequence. Duplicate order
	// nullifier is caught here.
	tc, err := state.BuildTradeCommitments(in.Orders, in.Fills)
	if err != nil {
		return types.SettlementUpdate{}, types.BatchCommitments{}, fmt.Errorf("%w: %v", ErrInvalidSettlementInputs, err)
	}

	// Assemble the core update.
	var upd types.SettlementUpdate
	if hasCore {
		upd, err = b.Build(in.Core) // reuse the tested core path (mints BatchID + seq)
		if err != nil {
			return types.SettlementUpdate{}, types.BatchCommitments{}, err
		}
	} else {
		b.mu.Lock()
		b.seq++
		seq := b.seq
		b.mu.Unlock()
		upd = types.SettlementUpdate{
			BatchID:      "batch-" + strconv.FormatUint(seq, 10),
			OldStateRoot: in.Core.OldStateRoot,
			NewStateRoot: in.Core.NewStateRoot,
			Deposits:     []types.SettlementDeposit{},
			Withdrawals:  []types.SettlementWithdrawal{},
		}
	}

	// Append the trade portion.
	upd.Trades = trades
	upd.TradeBatchCommitment = TradeBatchCommitment(tc.TradesRoot, tc.OrdersRoot)

	// Commitments: core 4 roots (unchanged) + appended trade roots.
	com := BuildCommitments(upd)
	com.TradesRoot = tc.TradesRoot
	com.OrdersRoot = tc.OrdersRoot

	return upd, com, nil
}

// validateFills checks each fill's required fields and returns them verbatim (in
// matching order — the order is the proof). Fills come from STATE-T05 but are
// re-validated here as defense-in-depth against a forged SettlementUpdate.
func validateFills(fills []types.Fill) ([]types.Fill, error) {
	out := make([]types.Fill, 0, len(fills))
	for i, f := range fills {
		for _, fld := range []struct{ name, val string }{
			{"tradeId", f.TradeID},
			{"market", f.Market},
			{"makerOrderHash", f.MakerOrderHash},
			{"takerOrderHash", f.TakerOrderHash},
			{"price", f.Price},
			{"qty", f.Qty},
			{"buyer", f.Buyer},
			{"seller", f.Seller},
		} {
			if strings.TrimSpace(fld.val) == "" {
				return nil, fmt.Errorf("%w: trades[%d].%s is empty", ErrInvalidSettlementInputs, i, fld.name)
			}
		}
		if f.MakerOrderHash == f.TakerOrderHash {
			return nil, fmt.Errorf("%w: trades[%d] maker and taker are the same order", ErrInvalidSettlementInputs, i)
		}
		if _, err := parseNonNegative(f.MakerFee); err != nil {
			return nil, fmt.Errorf("%w: trades[%d].makerFee %q invalid: %v", ErrInvalidSettlementInputs, i, f.MakerFee, err)
		}
		if _, err := parseNonNegative(f.TakerFee); err != nil {
			return nil, fmt.Errorf("%w: trades[%d].takerFee %q invalid: %v", ErrInvalidSettlementInputs, i, f.TakerFee, err)
		}
		out = append(out, f)
	}
	return out, nil
}
