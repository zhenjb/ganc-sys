package state

import (
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T06 — Apply trade to state.
//
// Turns a matching engine's Fill[] (STATE-T05) into off-chain balance
// transitions and a new pending root — the step that makes matching results into
// state a proof can commit. The core invariant is VALUE CONSERVATION per denom:
// everything consumed from reserved is credited back to available somewhere
// (counterparty or fee account), so in = out + fee holds exactly.
//
// Per Fill (price = maker price, qty = fill qty; notional = price*qty in quote):
//
//	buyer:  consume (notional + buyerFee) from its RESERVED quote  (locked at T02)
//	        receive  qty base into available
//	seller: consume  qty from its RESERVED base
//	        receive (notional - sellerFee) quote into available
//	fee:    receive (buyerFee + sellerFee) quote into available (feeAccount)
//
// buyerFee/sellerFee are the parties' ROLE fees: the maker pays makerFee, the
// taker pays takerFee. We consume RESERVED (never available) because the funds
// were already moved available->reserved when the order was placed (STATE-T02);
// debiting available would double-spend. Fees land in the fee account inside the
// SAME root, or a value-conservation proof would fail (plan pitfall).
//
// After all fills, any order that is fully filled (no longer in the book) has
// its leftover reserved released back to available (unused fee buffer / rounding
// slack), so a filled order ends with reserved = 0.
//
// Atomicity: Apply snapshots the manager first and rolls back on any error, so a
// mid-batch failure leaves state byte-identical to before (no partial trade,
// never a negative balance). Book rollback coordination is STATE-T08/T10.

// FeeAccountOwner is the off-chain account that collects trading fees. It lives
// in the same account map (same root) as user balances.
const FeeAccountOwner = "zkdex/fee-account"

// ErrTradeApply is the sentinel wrapping any failure while applying fills.
var ErrTradeApply = errors.New("state: trade apply failed")

// TradeApplier applies matched fills to an OffchainStateManager.
type TradeApplier struct {
	feeOwner string
}

// NewTradeApplier builds an applier crediting fees to feeOwner (defaults to
// FeeAccountOwner when empty).
func NewTradeApplier(feeOwner string) *TradeApplier {
	if feeOwner == "" {
		feeOwner = FeeAccountOwner
	}
	return &TradeApplier{feeOwner: feeOwner}
}

// TradeApplyResult summarizes a successful apply.
type TradeApplyResult struct {
	NewRoot      string `json:"newRoot"`
	FillsApplied int    `json:"fillsApplied"`
	FeeCredited  string `json:"feeCredited"` // total fee (quote units) moved to the fee account
	FeeDenom     string `json:"feeDenom"`
}

// Apply applies fills to the manager's pending state and returns the new root.
//
// `sides` maps every participating orderHash to its side (buy/sell) so the
// applier can tell buyer/seller apart from maker/taker roles — the caller builds
// it from the batch's orders. `book` is the post-match book, used to detect
// fully-filled orders (absent from the book) and release their leftover reserved.
//
// On any error the manager is rolled back to its pre-Apply snapshot and
// ErrTradeApply is returned; on success the state reflects every fill.
func (a *TradeApplier) Apply(
	m *OffchainStateManager,
	book *Orderbook,
	fills []types.Fill,
	market types.Market,
	sides map[string]types.OrderSide,
) (result TradeApplyResult, err error) {
	if len(fills) == 0 {
		return TradeApplyResult{NewRoot: m.Root(), FeeDenom: market.QuoteDenom}, nil
	}

	snap := m.Snapshot()
	defer func() {
		if err != nil {
			m.Rollback(snap)
		}
	}()

	totalFee := big.NewInt(0)
	touched := map[string]struct{}{}

	for i, f := range fills {
		if f.Market != market.Market {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d market %q != %q", ErrTradeApply, i, f.Market, market.Market)
		}
		buyHash, sellHash, ok := buySellHashes(f, sides)
		if !ok {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d missing side mapping for %s/%s", ErrTradeApply, i, f.MakerOrderHash, f.TakerOrderHash)
		}
		touched[buyHash] = struct{}{}
		touched[sellHash] = struct{}{}

		notional, derr := fillNotional(f)
		if derr != nil {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d: %v", ErrTradeApply, i, derr)
		}
		qtyDec, derr := parsePositiveDecimal(f.Qty)
		if derr != nil {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d qty %q: %v", ErrTradeApply, i, f.Qty, derr)
		}
		qtyInt, ok := qtyDec.toIntExact()
		if !ok {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d qty %q not whole units", ErrTradeApply, i, f.Qty)
		}

		makerFee, ok1 := new(big.Int).SetString(f.MakerFee, 10)
		takerFee, ok2 := new(big.Int).SetString(f.TakerFee, 10)
		if !ok1 || !ok2 || makerFee.Sign() < 0 || takerFee.Sign() < 0 {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d bad fee (%q/%q)", ErrTradeApply, i, f.MakerFee, f.TakerFee)
		}
		// Buyer pays its role fee; seller pays the other.
		buyerIsMaker := buyHash == f.MakerOrderHash
		buyerFee, sellerFee := takerFee, makerFee
		if buyerIsMaker {
			buyerFee, sellerFee = makerFee, takerFee
		}

		buyerQuoteOut := new(big.Int).Add(notional, buyerFee)
		sellerQuoteIn := new(big.Int).Sub(notional, sellerFee)
		if sellerQuoteIn.Sign() < 0 {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d seller fee %s exceeds notional %s", ErrTradeApply, i, sellerFee, notional)
		}

		// 1. Consume locked collateral (reserved, not available).
		if _, e := m.ConsumeOrder(buyHash, buyerQuoteOut.String()); e != nil {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d consume buyer quote: %v", ErrTradeApply, i, e)
		}
		if _, e := m.ConsumeOrder(sellHash, qtyInt.String()); e != nil {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d consume seller base: %v", ErrTradeApply, i, e)
		}
		// 2. Credit counterparties (available).
		if _, e := m.CreditAvailable(f.Buyer, market.BaseDenom, qtyInt.String()); e != nil {
			return TradeApplyResult{}, fmt.Errorf("%w: fill %d credit buyer base: %v", ErrTradeApply, i, e)
		}
		if sellerQuoteIn.Sign() > 0 {
			if _, e := m.CreditAvailable(f.Seller, market.QuoteDenom, sellerQuoteIn.String()); e != nil {
				return TradeApplyResult{}, fmt.Errorf("%w: fill %d credit seller quote: %v", ErrTradeApply, i, e)
			}
		}
		totalFee.Add(totalFee, buyerFee)
		totalFee.Add(totalFee, sellerFee)
	}

	// 3. Fees to the fee account (same root).
	if totalFee.Sign() > 0 {
		if _, e := m.CreditAvailable(a.feeOwner, market.QuoteDenom, totalFee.String()); e != nil {
			return TradeApplyResult{}, fmt.Errorf("%w: credit fee account: %v", ErrTradeApply, e)
		}
	}

	// 4. Release leftover reserved for fully-filled orders (not in the book).
	//    Sorted for deterministic root progression.
	hashes := make([]string, 0, len(touched))
	for h := range touched {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	for _, h := range hashes {
		if _, e := book.RemainingQty(h); errors.Is(e, ErrOrderNotFound) {
			// Fully filled → return any leftover reserved to available.
			if _, e := m.ReleaseOrder(h); e != nil && !errors.Is(e, ErrReservationNotFound) {
				return TradeApplyResult{}, fmt.Errorf("%w: release leftover %s: %v", ErrTradeApply, h, e)
			}
		}
	}

	return TradeApplyResult{
		NewRoot:      m.Root(),
		FillsApplied: len(fills),
		FeeCredited:  totalFee.String(),
		FeeDenom:     market.QuoteDenom,
	}, nil
}

// buySellHashes resolves which of a fill's maker/taker hashes is the buy order
// and which is the sell order, using the side map.
func buySellHashes(f types.Fill, sides map[string]types.OrderSide) (buyHash, sellHash string, ok bool) {
	ms, mok := sides[f.MakerOrderHash]
	ts, tok := sides[f.TakerOrderHash]
	if !mok || !tok || ms == ts {
		return "", "", false
	}
	if ms == types.SideBuy {
		return f.MakerOrderHash, f.TakerOrderHash, true
	}
	return f.TakerOrderHash, f.MakerOrderHash, true
}

// fillNotional returns price*qty as a whole integer of quote smallest-units.
func fillNotional(f types.Fill) (*big.Int, error) {
	price, err := parsePositiveDecimal(f.Price)
	if err != nil {
		return nil, fmt.Errorf("price %q: %w", f.Price, err)
	}
	qty, err := parsePositiveDecimal(f.Qty)
	if err != nil {
		return nil, fmt.Errorf("qty %q: %w", f.Qty, err)
	}
	notional, ok := mulDecimal(price, qty).toIntExact()
	if !ok {
		return nil, fmt.Errorf("notional price*qty (%s*%s) is not whole units", f.Price, f.Qty)
	}
	return notional, nil
}
