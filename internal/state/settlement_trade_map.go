package state

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// TRD-D1 — Luồn field order→Fill→Trade.
//
// A Fill (STATE-T05) carries only the match-level fields (tradeId, market,
// maker/takerOrderHash, price, qty, fees, buyer, seller). The on-chain Trade
// (ganc-trade x/zkdex) needs six more: orderNullifier, owner, denom, side,
// baseQty, quoteQty. BuildSettlementTrades derives those from the Fill + the
// batch's orders + the market registry, producing the chain-facing
// types.SettlementTrade the relayer submits (TRD-D2 wires the send).
//
// Mapping (AGR-2, buyer perspective — mirrors the chain's canonical agreementTrade):
//
//	tradeId       ← Fill.tradeId
//	market        ← Fill.market
//	makerOrderId  ← Fill.makerOrderHash
//	takerOrderId  ← Fill.takerOrderHash
//	owner         ← buy-side order owner            (== Fill.buyer)
//	side          ← "buy"
//	denom         ← market.baseDenom                (the asset traded)
//	orderHash     ← buy-side order hash
//	orderNullifier← Hash(buyer, buyerOrderHash)     (STATE-T03)
//	amount,baseQty← Fill.qty                          (base units)
//	price         ← Fill.price
//	quoteQty      ← price × qty                       (exact decimal)
//	makerFee,takerFee ← Fill.*
//
// SECURITY NOTE (flagged for AGR-2 / member B): this emits ONE trade per fill
// carrying the BUYER's orderNullifier, matching the chain's shipped one-per-fill
// contract. The SELLER's order nullifier is bound in ordersRoot (public input
// [7], enforced by the circuit) but is NOT marked used per-Trade on-chain. If the
// team wants the chain's persistent nullifier store to cover both orders (belt +
// suspenders against cross-batch replay), switch this to emit two records per
// fill (buyer + seller) — the chain's validateSettlementTrades already accepts a
// list and marks each. Kept one-per-fill here to honor the current agreement.

// ErrBuildSettlementTrades wraps every mapping failure (missing order, bad cross,
// inconsistent buyer, invalid decimal). Sentinel — callers chain with errors.Is.
var ErrBuildSettlementTrades = errors.New("state: build settlement trades")

// BuildSettlementTrades maps each Fill to its chain-facing SettlementTrade. It
// returns an error (no partial output) if any fill references an order absent
// from `orders`, is not a buy/sell cross, has a buyer that disagrees with the
// buy-side order owner, references an unknown market, or carries a malformed
// decimal. Fills are mapped in the given order (the matching sequence is the
// proof — never reordered).
func BuildSettlementTrades(
	fills []types.Fill,
	orders []OrderCommitmentInput,
	markets *MarketRegistry,
) ([]types.SettlementTrade, error) {
	if markets == nil {
		return nil, fmt.Errorf("%w: nil market registry", ErrBuildSettlementTrades)
	}

	byHash := make(map[string]OrderCommitmentInput, len(orders))
	for _, o := range orders {
		byHash[strings.TrimSpace(o.OrderHash)] = o
	}

	out := make([]types.SettlementTrade, 0, len(fills))
	for i, f := range fills {
		maker, ok := byHash[strings.TrimSpace(f.MakerOrderHash)]
		if !ok {
			return nil, fmt.Errorf("%w: fill[%d] maker order %q not in orders", ErrBuildSettlementTrades, i, f.MakerOrderHash)
		}
		taker, ok := byHash[strings.TrimSpace(f.TakerOrderHash)]
		if !ok {
			return nil, fmt.Errorf("%w: fill[%d] taker order %q not in orders", ErrBuildSettlementTrades, i, f.TakerOrderHash)
		}

		// Exactly one side must be buy and the other sell.
		var buyOrder OrderCommitmentInput
		switch {
		case maker.Side == types.SideBuy && taker.Side == types.SideSell:
			buyOrder = maker
		case taker.Side == types.SideBuy && maker.Side == types.SideSell:
			buyOrder = taker
		default:
			return nil, fmt.Errorf("%w: fill[%d] is not a buy/sell cross (maker=%q taker=%q)", ErrBuildSettlementTrades, i, maker.Side, taker.Side)
		}

		// The buy-side order owner must equal the Fill's declared buyer, else the
		// fill is internally inconsistent (or the orders were mismatched).
		if strings.TrimSpace(buyOrder.Owner) != strings.TrimSpace(f.Buyer) {
			return nil, fmt.Errorf("%w: fill[%d] buyer %q != buy-order owner %q", ErrBuildSettlementTrades, i, f.Buyer, buyOrder.Owner)
		}

		market, ok := markets.Get(f.Market)
		if !ok {
			return nil, fmt.Errorf("%w: fill[%d] unknown market %q", ErrBuildSettlementTrades, i, f.Market)
		}

		nullifier, err := OrderNullifierFor(buyOrder.Owner, buyOrder.OrderHash)
		if err != nil {
			return nil, fmt.Errorf("%w: fill[%d] nullifier: %v", ErrBuildSettlementTrades, i, err)
		}

		quoteQty, err := mulDecimalStrings(f.Price, f.Qty)
		if err != nil {
			return nil, fmt.Errorf("%w: fill[%d] quoteQty(price=%q qty=%q): %v", ErrBuildSettlementTrades, i, f.Price, f.Qty, err)
		}

		out = append(out, types.SettlementTrade{
			TradeID:        f.TradeID,
			Market:         f.Market,
			MakerOrderID:   f.MakerOrderHash,
			TakerOrderID:   f.TakerOrderHash,
			OrderHash:      strings.TrimSpace(buyOrder.OrderHash),
			OrderNullifier: nullifier,
			Owner:          strings.TrimSpace(buyOrder.Owner),
			Denom:          strings.TrimSpace(market.BaseDenom),
			Side:           string(types.SideBuy),
			Amount:         f.Qty,
			Price:          f.Price,
			BaseQty:        f.Qty,
			QuoteQty:       quoteQty,
			MakerFee:       f.MakerFee,
			TakerFee:       f.TakerFee,
		})
	}

	return out, nil
}

// mulDecimalStrings returns the exact product a×b of two non-negative decimal
// strings in canonical form (no float). Used for quoteQty = price × qty.
func mulDecimalStrings(a, b string) (string, error) {
	da, err := parseDecimal(a)
	if err != nil {
		return "", fmt.Errorf("factor %q: %w", a, err)
	}
	db, err := parseDecimal(b)
	if err != nil {
		return "", fmt.Errorf("factor %q: %w", b, err)
	}
	return mulDecimal(da, db).String(), nil
}
