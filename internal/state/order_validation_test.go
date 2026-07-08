package state

import (
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

const testNow = int64(1_000_000)

func testMarket() types.Market {
	return types.Market{
		Market:      "ATOM/USDC",
		BaseDenom:   "uatom",
		QuoteDenom:  "uusdc",
		TickSize:    "0.1",
		LotSize:     "1",
		MakerFeeBps: 0,
		TakerFeeBps: 10, // 0.10%
		Status:      types.MarketActive,
	}
}

// signedOrder returns a well-formed order with a valid MVP signature over its
// canonical bytes. Mutate the returned order BEFORE re-signing with signOrder.
func signedOrder(t *testing.T) types.SignedOrder {
	t.Helper()
	o := types.SignedOrder{
		Owner:  "cosmos1buyer",
		Market: "ATOM/USDC",
		Side:   types.SideBuy,
		Price:  "10.5",
		Qty:    "2",
		Expiry: "2000000", // > testNow
		Nonce:  "1",
	}
	return signOrder(t, o)
}

func signOrder(t *testing.T, o types.SignedOrder) types.SignedOrder {
	t.Helper()
	sig, err := MockOrderSignature(o)
	if err != nil {
		t.Fatalf("mock sign: %v", err)
	}
	o.Signature = sig
	return o
}

// buildValidator wires a validator with a funded buyer (enough quote) unless
// overridden, an active ATOM/USDC market, and a fresh nullifier set.
func buildValidator(t *testing.T, quote string) (*OrderValidator, *OffchainStateManager, *InMemoryOrderNullifiers) {
	t.Helper()
	markets := NewMarketRegistry()
	if err := markets.Register(testMarket()); err != nil {
		t.Fatalf("register market: %v", err)
	}
	mgr := NewOffchainStateManager()
	if quote != "" {
		if _, err := mgr.ApplyDeposit(types.DepositRecord{
			DepositID: "d1", Owner: "cosmos1buyer", Denom: "uusdc", Amount: quote,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	nulls := NewInMemoryOrderNullifiers()
	return NewOrderValidator(markets, mgr, nulls, nil), mgr, nulls
}

func TestValidateOrderAccepted(t *testing.T) {
	v, _, _ := buildValidator(t, "1000")
	got, err := v.Validate(signedOrder(t), testNow)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !got.Accepted {
		t.Fatalf("expected accept, got reject: %s (%s)", got.Reason, got.Detail)
	}
	// buy: notional = 10.5*2 = 21 -> ceil 21; fee = ceil(21*10/10000)=1 -> 22 uusdc.
	if got.ReserveDenom != "uusdc" || got.ReserveAmount != "22" {
		t.Fatalf("collateral: got %s %s, want 22 uusdc", got.ReserveAmount, got.ReserveDenom)
	}
	if got.OrderHash == "" || got.OrderNullifier == "" {
		t.Fatalf("expected orderHash+nullifier populated: %+v", got)
	}
}

func TestValidateOrderForgedSignatureRejected(t *testing.T) {
	v, _, _ := buildValidator(t, "1000")
	o := signedOrder(t)
	// Tamper price AFTER signing: signature now covers the old canonical bytes.
	o.Price = "10.6"
	got, _ := v.Validate(o, testNow)
	if got.Accepted || got.Reason != ReasonBadSignature {
		t.Fatalf("expected bad_signature, got accepted=%v reason=%s", got.Accepted, got.Reason)
	}
}

func TestValidateOrderTickViolation(t *testing.T) {
	v, _, _ := buildValidator(t, "1000")
	o := signOrder(t, func() types.SignedOrder { o := signedOrder(t); o.Price = "10.55"; return o }())
	got, _ := v.Validate(o, testNow)
	if got.Accepted || got.Reason != ReasonTickViolation {
		t.Fatalf("expected tick_violation, got accepted=%v reason=%s", got.Accepted, got.Reason)
	}
}

func TestValidateOrderLotViolation(t *testing.T) {
	v, _, _ := buildValidator(t, "1000")
	o := signedOrder(t)
	o.Qty = "2.5" // lotSize is 1
	o = signOrder(t, o)
	got, _ := v.Validate(o, testNow)
	if got.Accepted || got.Reason != ReasonLotViolation {
		t.Fatalf("expected lot_violation, got accepted=%v reason=%s", got.Accepted, got.Reason)
	}
}

func TestValidateOrderExpired(t *testing.T) {
	v, _, _ := buildValidator(t, "1000")
	o := signedOrder(t)
	o.Expiry = "999999" // < testNow (1_000_000)
	o = signOrder(t, o)
	got, _ := v.Validate(o, testNow)
	if got.Accepted || got.Reason != ReasonExpired {
		t.Fatalf("expected expired, got accepted=%v reason=%s", got.Accepted, got.Reason)
	}
	// Exactly-at-expiry is still valid (not strictly past).
	o.Expiry = "1000000"
	o = signOrder(t, o)
	if got, _ := v.Validate(o, testNow); !got.Accepted {
		t.Fatalf("expiry==now should be valid, got reject %s", got.Reason)
	}
}

func TestValidateOrderUnknownAndInactiveMarket(t *testing.T) {
	v, _, _ := buildValidator(t, "1000")

	unknown := signedOrder(t)
	unknown.Market = "BTC/USDC"
	unknown = signOrder(t, unknown)
	if got, _ := v.Validate(unknown, testNow); got.Reason != ReasonUnknownMarket {
		t.Fatalf("expected unknown_market, got %s", got.Reason)
	}

	// Register an inactive market and target it.
	markets := NewMarketRegistry()
	m := testMarket()
	m.Status = types.MarketHalted
	_ = markets.Register(m)
	mgr := NewOffchainStateManager()
	_, _ = mgr.ApplyDeposit(types.DepositRecord{DepositID: "d", Owner: "cosmos1buyer", Denom: "uusdc", Amount: "1000"})
	v2 := NewOrderValidator(markets, mgr, nil, nil)
	if got, _ := v2.Validate(signedOrder(t), testNow); got.Reason != ReasonMarketInactive {
		t.Fatalf("expected market_inactive, got %s", got.Reason)
	}
}

func TestValidateOrderNullifierReused(t *testing.T) {
	v, _, nulls := buildValidator(t, "1000")
	o := signedOrder(t)

	first, _ := v.Validate(o, testNow)
	if !first.Accepted {
		t.Fatalf("first should accept: %s", first.Reason)
	}
	// Simulate the order having filled/cancelled → its nullifier is consumed.
	nulls.MarkUsed(first.OrderNullifier)

	second, _ := v.Validate(o, testNow)
	if second.Accepted || second.Reason != ReasonNullifierUsed {
		t.Fatalf("expected order_nullifier_used, got accepted=%v reason=%s", second.Accepted, second.Reason)
	}
}

func TestValidateOrderInsufficientAvailable(t *testing.T) {
	// Buyer funded with only 21 uusdc but needs 22 (notional 21 + fee 1).
	v, _, _ := buildValidator(t, "21")
	got, _ := v.Validate(signedOrder(t), testNow)
	if got.Accepted || got.Reason != ReasonInsufficientAvailable {
		t.Fatalf("expected insufficient_available, got accepted=%v reason=%s", got.Accepted, got.Reason)
	}
}

// A sell order locks BASE (qty), not quote. A seller with enough base passes.
func TestValidateSellOrderLocksBase(t *testing.T) {
	markets := NewMarketRegistry()
	_ = markets.Register(testMarket())
	mgr := NewOffchainStateManager()
	_, _ = mgr.ApplyDeposit(types.DepositRecord{DepositID: "d", Owner: "cosmos1seller", Denom: "uatom", Amount: "5"})
	v := NewOrderValidator(markets, mgr, nil, nil)

	sell := types.SignedOrder{
		Owner: "cosmos1seller", Market: "ATOM/USDC", Side: types.SideSell,
		Price: "10.5", Qty: "2", Expiry: "2000000", Nonce: "1",
	}
	sell = signOrder(t, sell)

	got, err := v.Validate(sell, testNow)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !got.Accepted {
		t.Fatalf("sell should accept: %s (%s)", got.Reason, got.Detail)
	}
	if got.ReserveDenom != "uatom" || got.ReserveAmount != "2" {
		t.Fatalf("sell collateral: got %s %s, want 2 uatom", got.ReserveAmount, got.ReserveDenom)
	}
}

// Determinism: same order + same now → byte-identical verdict (P2 must replay).
func TestValidateOrderDeterministic(t *testing.T) {
	v, _, _ := buildValidator(t, "1000")
	o := signedOrder(t)
	a, _ := v.Validate(o, testNow)
	b, _ := v.Validate(o, testNow)
	if a != b {
		t.Fatalf("verdict not deterministic:\n a=%+v\n b=%+v", a, b)
	}
}

// --- lower-level unit checks -------------------------------------------------

func TestOrderHashStableAndSignatureIndependent(t *testing.T) {
	o := signedOrder(t)
	h1, err := OrderHash(o)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	o2 := o
	o2.Signature = "0xsomethingelse"
	h2, _ := OrderHash(o2)
	if h1 != h2 {
		t.Fatal("orderHash must not depend on signature")
	}
	o3 := o
	o3.Price = "11.0"
	h3, _ := OrderHash(o3)
	if h1 == h3 {
		t.Fatal("orderHash must change when a canonical field changes")
	}
}

func TestDecimalIsMultipleOf(t *testing.T) {
	mustDec := func(s string) decimal {
		d, err := parseDecimal(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return d
	}
	cases := []struct {
		v, u string
		want bool
	}{
		{"10.5", "0.1", true},
		{"10.55", "0.1", false},
		{"2", "1", true},
		{"2.5", "1", false},
		{"100", "0.25", true},
		{"0.75", "0.25", true},
		{"0.8", "0.25", false},
	}
	for _, c := range cases {
		if got := isMultipleOf(mustDec(c.v), mustDec(c.u)); got != c.want {
			t.Fatalf("isMultipleOf(%s, %s)=%v want %v", c.v, c.u, got, c.want)
		}
	}
}

func TestParseDecimalRejectsBad(t *testing.T) {
	for _, s := range []string{"", "  ", "abc", "1.2.3", "-1", "+1", "1e5", "1,000"} {
		if _, err := parseDecimal(s); err == nil {
			t.Fatalf("expected error for %q", s)
		}
	}
}

func TestMarketRegistryValidation(t *testing.T) {
	r := NewMarketRegistry()
	bad := testMarket()
	bad.TickSize = "0"
	if err := r.Register(bad); err == nil {
		t.Fatal("tickSize 0 should be rejected")
	}
	bad = testMarket()
	bad.QuoteDenom = ""
	if err := r.Register(bad); err == nil {
		t.Fatal("empty quote denom should be rejected")
	}
	if err := r.Register(testMarket()); err != nil {
		t.Fatalf("valid market rejected: %v", err)
	}
	if _, ok := r.Get("ATOM/USDC"); !ok {
		t.Fatal("registered market not found")
	}
}
