package state

import (
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T03 — Order validation.
//
// ValidateOrder is the security gate every order crosses before entering the
// orderbook (STATE-T04). It is deterministic (pitfall: P2 must reproduce the
// verdict) — the only time input is passed in explicitly (now), never read from
// a clock inside. It never mutates state: it computes the orderHash /
// orderNullifier and the collateral that WOULD be reserved, and reports whether
// the order is acceptable. The actual Reserve happens at insert time (T04).

// OrderRejectionReason is a stable machine-readable rejection code returned to
// P4 (and shown to P5). Empty string means accepted.
type OrderRejectionReason string

const (
	ReasonBadFormat             OrderRejectionReason = "bad_format"
	ReasonBadSignature          OrderRejectionReason = "bad_signature"
	ReasonUnknownMarket         OrderRejectionReason = "unknown_market"
	ReasonMarketInactive        OrderRejectionReason = "market_inactive"
	ReasonTickViolation         OrderRejectionReason = "tick_violation"
	ReasonLotViolation          OrderRejectionReason = "lot_violation"
	ReasonExpired               OrderRejectionReason = "expired"
	ReasonNullifierUsed         OrderRejectionReason = "order_nullifier_used"
	ReasonInsufficientAvailable OrderRejectionReason = "insufficient_available"
)

// OrderValidation is the verdict ValidateOrder returns. On accept, the derived
// identifiers and the collateral to lock are populated so the caller (T04) can
// Reserve without recomputing. On reject, Reason/Detail explain why.
type OrderValidation struct {
	Accepted       bool                 `json:"accepted"`
	Reason         OrderRejectionReason `json:"reason,omitempty"`
	Detail         string               `json:"detail,omitempty"`
	OrderHash      string               `json:"orderHash,omitempty"`
	OrderNullifier string               `json:"orderNullifier,omitempty"`
	ReserveDenom   string               `json:"reserveDenom,omitempty"`
	ReserveAmount  string               `json:"reserveAmount,omitempty"`
}

func rejected(reason OrderRejectionReason, format string, args ...any) OrderValidation {
	return OrderValidation{Accepted: false, Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// AvailableBalanceSource supplies the current available balance for collateral
// sufficiency checks. *OffchainStateManager satisfies it (Account.Balance is the
// available amount, STATE-T02).
type AvailableBalanceSource interface {
	Account(owner, denom string) types.Account
}

// OrderNullifierRegistry reports whether an order nullifier has already been
// consumed (the order was filled or cancelled). *InMemoryOrderNullifiers is the
// MVP off-chain implementation; ONCHAIN-T02 adds the authoritative on-chain KV.
type OrderNullifierRegistry interface {
	IsOrderNullifierUsed(nullifier string) bool
}

// InMemoryOrderNullifiers is the MVP off-chain used-order-nullifier set. An
// order's nullifier is marked used when it fills or is cancelled, blocking
// replay. Thread-safe.
type InMemoryOrderNullifiers struct {
	mu   sync.RWMutex
	used map[string]struct{}
}

// NewInMemoryOrderNullifiers returns an empty registry.
func NewInMemoryOrderNullifiers() *InMemoryOrderNullifiers {
	return &InMemoryOrderNullifiers{used: make(map[string]struct{})}
}

// IsOrderNullifierUsed reports whether nullifier has been consumed.
func (r *InMemoryOrderNullifiers) IsOrderNullifierUsed(nullifier string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.used[strings.TrimSpace(nullifier)]
	return ok
}

// MarkUsed records nullifier as consumed (call on fill or cancel).
func (r *InMemoryOrderNullifiers) MarkUsed(nullifier string) {
	nullifier = strings.TrimSpace(nullifier)
	if nullifier == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.used[nullifier] = struct{}{}
}

// alwaysFreshNullifiers is the default when no registry is injected: nothing is
// ever "used". Fine for tests / single-shot validation.
type alwaysFreshNullifiers struct{}

func (alwaysFreshNullifiers) IsOrderNullifierUsed(string) bool { return false }

// OrderValidator bundles the dependencies ValidateOrder needs. Construct via
// NewOrderValidator so nil optionals get safe defaults.
type OrderValidator struct {
	markets    *MarketRegistry
	balances   AvailableBalanceSource
	nullifiers OrderNullifierRegistry
	sig        OrderSignatureVerifier
}

// NewOrderValidator wires the validator. markets and balances are required. A
// nil nullifier registry defaults to "never used"; a nil signature verifier
// defaults to the MVP MockOrderSignatureVerifier.
func NewOrderValidator(markets *MarketRegistry, balances AvailableBalanceSource, nullifiers OrderNullifierRegistry, sig OrderSignatureVerifier) *OrderValidator {
	if nullifiers == nil {
		nullifiers = alwaysFreshNullifiers{}
	}
	if sig == nil {
		sig = MockOrderSignatureVerifier{}
	}
	return &OrderValidator{markets: markets, balances: balances, nullifiers: nullifiers, sig: sig}
}

// Validate runs the full order-validation pipeline and returns a verdict. `now`
// is the reference time in unix seconds (passed in for determinism — P2 replays
// with the same reference). It returns a non-nil error only on an internal
// inconsistency (e.g. misconfigured validator); ordinary rejections come back as
// an OrderValidation with Accepted=false and a Reason.
func (v *OrderValidator) Validate(order types.SignedOrder, now int64) (OrderValidation, error) {
	// 1. Structural (canonical fields present + newline-free, side enum, sig
	//    present). This also guarantees CanonicalBytes below cannot fail.
	if err := order.Validate(); err != nil {
		return rejected(ReasonBadFormat, "%v", err), nil
	}
	canonical, err := order.CanonicalBytes()
	if err != nil {
		return rejected(ReasonBadFormat, "%v", err), nil
	}

	// 2. orderHash (bound to canonical bytes).
	orderHash, err := OrderHash(order)
	if err != nil {
		return rejected(ReasonBadFormat, "orderHash: %v", err), nil
	}

	// 3. Market must exist and be active.
	market, ok := v.markets.Get(order.Market)
	if !ok {
		return rejected(ReasonUnknownMarket, "market %q not registered", order.Market), nil
	}
	if market.Status != types.MarketActive {
		return rejected(ReasonMarketInactive, "market %q status is %q", order.Market, market.Status), nil
	}

	// 4. Tick / lot alignment (exact decimal, no float).
	price, err := parsePositiveDecimal(order.Price)
	if err != nil {
		return rejected(ReasonBadFormat, "price %q: %v", order.Price, err), nil
	}
	qty, err := parsePositiveDecimal(order.Qty)
	if err != nil {
		return rejected(ReasonBadFormat, "qty %q: %v", order.Qty, err), nil
	}
	tick, err := parsePositiveDecimal(market.TickSize)
	if err != nil {
		return rejected(ReasonBadFormat, "market tickSize %q: %v", market.TickSize, err), nil
	}
	lot, err := parsePositiveDecimal(market.LotSize)
	if err != nil {
		return rejected(ReasonBadFormat, "market lotSize %q: %v", market.LotSize, err), nil
	}
	if !isMultipleOf(price, tick) {
		return rejected(ReasonTickViolation, "price %s not a multiple of tickSize %s", order.Price, market.TickSize), nil
	}
	if !isMultipleOf(qty, lot) {
		return rejected(ReasonLotViolation, "qty %s not a multiple of lotSize %s", order.Qty, market.LotSize), nil
	}

	// 5. Expiry not passed. now and expiry are unix seconds.
	expiry, ok := new(big.Int).SetString(strings.TrimSpace(order.Expiry), 10)
	if !ok {
		return rejected(ReasonBadFormat, "expiry %q not an integer", order.Expiry), nil
	}
	if expiry.Cmp(big.NewInt(now)) < 0 {
		return rejected(ReasonExpired, "order expired at %s, now %d", order.Expiry, now), nil
	}

	// 6. Signature over CANONICAL bytes (never the raw JSON).
	if err := v.sig.Verify(order, canonical); err != nil {
		return rejected(ReasonBadSignature, "%v", err), nil
	}

	// 7. Replay: orderNullifier = Hash(owner, orderHash) must be fresh.
	orderNullifier, err := OrderNullifierFor(order.Owner, orderHash)
	if err != nil {
		return rejected(ReasonBadFormat, "orderNullifier: %v", err), nil
	}
	if v.nullifiers.IsOrderNullifierUsed(orderNullifier) {
		return rejected(ReasonNullifierUsed, "order nullifier already used (filled/cancelled)"), nil
	}

	// 8. Collateral sufficiency: buy locks quote (price*qty + taker-fee buffer),
	//    sell locks base (qty). Amounts are whole smallest-units (ceil).
	reserveDenom, reserveAmount, err := requiredCollateral(order.Side, market, price, qty)
	if err != nil {
		return rejected(ReasonBadFormat, "collateral: %v", err), nil
	}
	available, ok := new(big.Int).SetString(v.balances.Account(order.Owner, reserveDenom).Balance, 10)
	if !ok {
		return OrderValidation{}, fmt.Errorf("corrupt available balance for %s/%s", order.Owner, reserveDenom)
	}
	if available.Cmp(reserveAmount) < 0 {
		return rejected(ReasonInsufficientAvailable, "need %s %s, available %s", reserveAmount, reserveDenom, available), nil
	}

	// Accepted.
	return OrderValidation{
		Accepted:       true,
		OrderHash:      orderHash,
		OrderNullifier: orderNullifier,
		ReserveDenom:   reserveDenom,
		ReserveAmount:  reserveAmount.String(),
	}, nil
}

// requiredCollateral computes which denom and how much an order must lock:
//
//	buy  -> quote denom, ceil(price*qty) + ceil(notional * takerFeeBps / 10000)
//	sell -> base  denom, ceil(qty)
//
// Amounts are whole smallest-units; ceil over-reserves conservatively (the exact
// amount is consumed on fill, the remainder released). The buy fee buffer uses
// the TAKER fee (the worst case a crossing order pays). NOTE (MVP): the decimal
// price/qty are treated as already expressed in the denom's smallest unit —
// full fixed-point denom-decimal scaling is deferred to the fee/scaling
// agreement with P2 (ZK-T04) and does not change this function's shape.
func requiredCollateral(side types.OrderSide, market types.Market, price, qty decimal) (string, *big.Int, error) {
	switch side {
	case types.SideBuy:
		notional := mulDecimal(price, qty).ceilToInt()
		fee := ceilDivBig(new(big.Int).Mul(notional, big.NewInt(market.TakerFeeBps)), big.NewInt(10000))
		return market.QuoteDenom, new(big.Int).Add(notional, fee), nil
	case types.SideSell:
		return market.BaseDenom, qty.ceilToInt(), nil
	default:
		return "", nil, fmt.Errorf("%w: unknown side %q", types.ErrInvalidOrder, side)
	}
}
