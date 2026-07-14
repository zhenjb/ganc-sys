package state

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// TRD-D1 / TRD-D1b — Luồn field order→Fill→Trade (two-per-fill, AGR-2b).
//
// A Fill (STATE-T05) carries only the match-level fields (tradeId, market,
// maker/takerOrderHash, price, qty, fees, buyer, seller). The on-chain Trade
// (ganc-trade x/zkdex) needs six more: orderNullifier, owner, denom, side,
// baseQty, quoteQty. BuildSettlementTrades derives those from the Fill + the
// batch's orders + the market registry, producing the chain-facing
// types.SettlementTrade the relayer submits (TRD-D2 wires the send).
//
// AGR-2b (chốt 2026-07-14) — TWO records per fill: each fill consumes a BUY
// order and a SELL order, and the chain must mark BOTH order nullifiers used so
// neither side can be replayed in a later batch. So each Fill yields two
// SettlementTrade records:
//
//	buyer  record: side="buy",  owner/orderHash/orderNullifier = the BUY order
//	seller record: side="sell", owner/orderHash/orderNullifier = the SELL order
//
// The two records share the fill-level fields and are disambiguated by a tradeId
// suffix ("-buy" / "-sell") so the chain's duplicate-tradeId guard is satisfied.
// Both nullifiers are already bound in ordersRoot (public input [7], covering
// every maker+taker order), so emitting both does NOT change tradesRoot/ordersRoot
// or the proof — it only closes the seller-replay gap on-chain.
//
// Shared per record:
//
//	tradeId       ← Fill.tradeId + "-buy" | "-sell"
//	market        ← Fill.market
//	makerOrderId  ← Fill.makerOrderHash
//	takerOrderId  ← Fill.takerOrderHash
//	denom         ← market.baseDenom            (the asset traded)
//	amount,baseQty← Fill.qty
//	price         ← Fill.price
//	quoteQty      ← price × qty                 (exact decimal)
//	makerFee,takerFee ← Fill.*
//
// Per-side:
//
//	owner         ← that side's order owner     (buy == Fill.buyer, sell == Fill.seller)
//	side          ← "buy" | "sell"
//	orderHash     ← that side's order hash
//	orderNullifier← Hash(owner, orderHash)       (STATE-T03)

// tradeId suffixes distinguishing the buyer vs seller record of one fill.
const (
	tradeIDSuffixBuy  = "-buy"
	tradeIDSuffixSell = "-sell"
)

// ErrBuildSettlementTrades wraps every mapping failure (missing order, bad cross,
// inconsistent buyer/seller, invalid decimal). Sentinel — errors.Is.
var ErrBuildSettlementTrades = errors.New("state: build settlement trades")

// BuildSettlementTrades maps each Fill to its two chain-facing SettlementTrade
// records (buyer + seller). It returns an error (no partial output) if any fill
// references an order absent from `orders`, is not a buy/sell cross, has a
// buyer/seller that disagrees with the buy/sell-order owner, references an
// unknown market, or carries a malformed decimal. Fills are mapped in the given
// order (the matching sequence is the proof — never reordered); for each fill the
// buyer record precedes the seller record.
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

	out := make([]types.SettlementTrade, 0, len(fills)*2)
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
		var buyOrder, sellOrder OrderCommitmentInput
		switch {
		case maker.Side == types.SideBuy && taker.Side == types.SideSell:
			buyOrder, sellOrder = maker, taker
		case taker.Side == types.SideBuy && maker.Side == types.SideSell:
			buyOrder, sellOrder = taker, maker
		default:
			return nil, fmt.Errorf("%w: fill[%d] is not a buy/sell cross (maker=%q taker=%q)", ErrBuildSettlementTrades, i, maker.Side, taker.Side)
		}

		// The buy/sell-order owners must equal the Fill's declared buyer/seller,
		// else the fill is internally inconsistent (or the orders were mismatched).
		if strings.TrimSpace(buyOrder.Owner) != strings.TrimSpace(f.Buyer) {
			return nil, fmt.Errorf("%w: fill[%d] buyer %q != buy-order owner %q", ErrBuildSettlementTrades, i, f.Buyer, buyOrder.Owner)
		}
		if strings.TrimSpace(sellOrder.Owner) != strings.TrimSpace(f.Seller) {
			return nil, fmt.Errorf("%w: fill[%d] seller %q != sell-order owner %q", ErrBuildSettlementTrades, i, f.Seller, sellOrder.Owner)
		}

		market, ok := markets.Get(f.Market)
		if !ok {
			return nil, fmt.Errorf("%w: fill[%d] unknown market %q", ErrBuildSettlementTrades, i, f.Market)
		}
		denom := strings.TrimSpace(market.BaseDenom)

		quoteQty, err := mulDecimalStrings(f.Price, f.Qty)
		if err != nil {
			return nil, fmt.Errorf("%w: fill[%d] quoteQty(price=%q qty=%q): %v", ErrBuildSettlementTrades, i, f.Price, f.Qty, err)
		}

		buyRec, err := settlementTradeFor(f, buyOrder, types.SideBuy, tradeIDSuffixBuy, denom, quoteQty)
		if err != nil {
			return nil, fmt.Errorf("%w: fill[%d] buyer record: %v", ErrBuildSettlementTrades, i, err)
		}
		sellRec, err := settlementTradeFor(f, sellOrder, types.SideSell, tradeIDSuffixSell, denom, quoteQty)
		if err != nil {
			return nil, fmt.Errorf("%w: fill[%d] seller record: %v", ErrBuildSettlementTrades, i, err)
		}

		out = append(out, buyRec, sellRec)
	}

	return out, nil
}

// settlementTradeFor builds one side's SettlementTrade from the fill + that
// side's order. The amount/fee fields are identical on both records (they
// describe the same fill); side, owner, orderHash, orderNullifier and the tradeId
// suffix are per-side.
func settlementTradeFor(
	f types.Fill,
	o OrderCommitmentInput,
	side types.OrderSide,
	idSuffix, denom, quoteQty string,
) (types.SettlementTrade, error) {
	nullifier, err := OrderNullifierFor(o.Owner, o.OrderHash)
	if err != nil {
		return types.SettlementTrade{}, err
	}
	return types.SettlementTrade{
		TradeID:        f.TradeID + idSuffix,
		Market:         f.Market,
		MakerOrderID:   f.MakerOrderHash,
		TakerOrderID:   f.TakerOrderHash,
		OrderHash:      strings.TrimSpace(o.OrderHash),
		OrderNullifier: nullifier,
		Owner:          strings.TrimSpace(o.Owner),
		Denom:          denom,
		Side:           string(side),
		Amount:         f.Qty,
		Price:          f.Price,
		BaseQty:        f.Qty,
		QuoteQty:       quoteQty,
		MakerFee:       f.MakerFee,
		TakerFee:       f.TakerFee,
	}, nil
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
