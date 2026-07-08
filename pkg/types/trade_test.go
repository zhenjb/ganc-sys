package types

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func validOrder() SignedOrder {
	return SignedOrder{
		Owner:     "cosmos1alice",
		Market:    "ATOM/USDC",
		Side:      SideBuy,
		Price:     "10.5",
		Qty:       "2",
		Expiry:    "1893456000",
		Nonce:     "1",
		Signature: "0xdeadbeef",
	}
}

// The golden canonical string pins the exact byte layout. If this test breaks,
// the canonical format changed — that is a breaking change and MUST come with a
// bump of OrderCanonicalVersion and a regenerated sample vector, NOT a silent
// edit of this constant.
const goldenCanonical = "zkdex/order/v0\n" +
	"owner:cosmos1alice\n" +
	"market:ATOM/USDC\n" +
	"side:buy\n" +
	"price:10.5\n" +
	"qty:2\n" +
	"expiry:1893456000\n" +
	"nonce:1"

func TestCanonicalStringGolden(t *testing.T) {
	got, err := validOrder().CanonicalString()
	if err != nil {
		t.Fatalf("CanonicalString: %v", err)
	}
	if got != goldenCanonical {
		t.Fatalf("canonical mismatch:\n got: %q\nwant: %q", got, goldenCanonical)
	}
}

func TestCanonicalBytesDeterministic(t *testing.T) {
	o := validOrder()
	b1, err := o.CanonicalBytes()
	if err != nil {
		t.Fatalf("bytes #1: %v", err)
	}
	b2, err := o.CanonicalBytes()
	if err != nil {
		t.Fatalf("bytes #2: %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("CanonicalBytes not deterministic: %q vs %q", b1, b2)
	}
}

// The signature must NOT be part of the canonical preimage (a signature cannot
// cover itself). Two orders differing only in Signature must canonicalize
// identically.
func TestCanonicalExcludesSignature(t *testing.T) {
	a := validOrder()
	b := validOrder()
	b.Signature = "0xffffffffffffffff"

	sa, err := a.CanonicalString()
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	sb, err := b.CanonicalString()
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if sa != sb {
		t.Fatalf("signature leaked into canonical bytes:\n a=%q\n b=%q", sa, sb)
	}
}

// Every canonical field must influence the bytes: changing any one of them
// yields a different canonical string.
func TestCanonicalSensitiveToEveryField(t *testing.T) {
	base, err := validOrder().CanonicalString()
	if err != nil {
		t.Fatalf("base: %v", err)
	}

	mutators := map[string]func(*SignedOrder){
		"owner":  func(o *SignedOrder) { o.Owner = "cosmos1bob" },
		"market": func(o *SignedOrder) { o.Market = "OSMO/USDC" },
		"side":   func(o *SignedOrder) { o.Side = SideSell },
		"price":  func(o *SignedOrder) { o.Price = "10.6" },
		"qty":    func(o *SignedOrder) { o.Qty = "3" },
		"expiry": func(o *SignedOrder) { o.Expiry = "1893456001" },
		"nonce":  func(o *SignedOrder) { o.Nonce = "2" },
	}
	for name, mut := range mutators {
		o := validOrder()
		mut(&o)
		s, err := o.CanonicalString()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s == base {
			t.Fatalf("mutating %q did not change canonical bytes", name)
		}
	}
}

// Whitespace around a field is trimmed, so the canonical bytes are stable
// regardless of incidental padding.
func TestCanonicalTrimsWhitespace(t *testing.T) {
	o := validOrder()
	o.Owner = "  cosmos1alice  "
	o.Price = " 10.5 "
	got, err := o.CanonicalString()
	if err != nil {
		t.Fatalf("CanonicalString: %v", err)
	}
	if got != goldenCanonical {
		t.Fatalf("trim not applied:\n got: %q\nwant: %q", got, goldenCanonical)
	}
}

func TestCanonicalRejectsEmptyField(t *testing.T) {
	o := validOrder()
	o.Price = "   "
	if _, err := o.CanonicalString(); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("expected ErrInvalidOrder for empty price, got %v", err)
	}
}

func TestCanonicalRejectsNewlineInjection(t *testing.T) {
	o := validOrder()
	o.Market = "ATOM/USDC\nnonce:999"
	if _, err := o.CanonicalString(); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("expected ErrInvalidOrder for newline injection, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	if err := validOrder().Validate(); err != nil {
		t.Fatalf("valid order rejected: %v", err)
	}

	badSide := validOrder()
	badSide.Side = "long"
	if err := badSide.Validate(); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("expected ErrInvalidOrder for bad side, got %v", err)
	}

	noSig := validOrder()
	noSig.Signature = ""
	if err := noSig.Validate(); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("expected ErrInvalidOrder for empty signature, got %v", err)
	}

	empty := validOrder()
	empty.Owner = ""
	if err := empty.Validate(); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("expected ErrInvalidOrder for empty owner, got %v", err)
	}
}

func TestOrderSideAndMarketStatusValidity(t *testing.T) {
	if !SideBuy.IsValid() || !SideSell.IsValid() {
		t.Fatal("buy/sell should be valid sides")
	}
	if OrderSide("hold").IsValid() {
		t.Fatal("hold should be invalid side")
	}
	for _, s := range []MarketStatus{MarketActive, MarketHalted, MarketInactive} {
		if !s.IsValid() {
			t.Fatalf("%q should be valid status", s)
		}
	}
	if MarketStatus("frozen").IsValid() {
		t.Fatal("frozen should be invalid status")
	}
}

// JSON round-trip must be lossless for every trade struct (DoD).
func TestJSONRoundTrip(t *testing.T) {
	t.Run("SignedOrder", func(t *testing.T) {
		assertRoundTrip(t, validOrder())
	})
	t.Run("Fill", func(t *testing.T) {
		assertRoundTrip(t, Fill{
			TradeID:        "trade-1",
			Market:         "ATOM/USDC",
			MakerOrderHash: "0xaaaa",
			TakerOrderHash: "0xbbbb",
			Price:          "10.5",
			Qty:            "2",
			MakerFee:       "0",
			TakerFee:       "21",
			Buyer:          "cosmos1alice",
			Seller:         "cosmos1bob",
		})
	})
	t.Run("Market", func(t *testing.T) {
		assertRoundTrip(t, Market{
			Market:      "ATOM/USDC",
			BaseDenom:   "uatom",
			QuoteDenom:  "uusdc",
			TickSize:    "0.01",
			LotSize:     "0.1",
			MakerFeeBps: 0,
			TakerFeeBps: 10,
			Status:      MarketActive,
		})
	})
	t.Run("Reservation", func(t *testing.T) {
		assertRoundTrip(t, Reservation{
			Owner:     "cosmos1alice",
			Denom:     "uusdc",
			Amount:    "21",
			OrderHash: "0xaaaa",
		})
	})
}

func assertRoundTrip[T any](t *testing.T, in T) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip lost data:\n in:  %+v\n out: %+v\n raw: %s", in, out, raw)
	}
}

// Field JSON tags are part of the cross-role contract (P2/P4/P5). Pin them so an
// accidental rename is caught here.
func TestJSONTagContract(t *testing.T) {
	raw, err := json.Marshal(validOrder())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"owner", "market", "side", "price", "qty", "expiry", "nonce", "signature"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("SignedOrder JSON missing expected key %q; got %s", key, raw)
		}
	}
}
