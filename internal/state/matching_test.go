package state

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func matchMarket() types.Market {
	return types.Market{
		Market:      "ATOM/USDC",
		BaseDenom:   "uatom",
		QuoteDenom:  "uusdc",
		TickSize:    "0.1",
		LotSize:     "1",
		MakerFeeBps: 50,  // 0.50%
		TakerFeeBps: 100, // 1.00%
		Status:      types.MarketActive,
	}
}

// place rests an order with an explicit owner + orderHash (distinct from the
// orderbook_test helper which derives owner from the hash).
func place(t *testing.T, b *Orderbook, hash, owner string, side types.OrderSide, price, qty string) {
	t.Helper()
	o := mkOrder(owner, "ATOM/USDC", side, price, qty)
	denom := "uusdc"
	if side == types.SideSell {
		denom = "uatom"
	}
	if _, err := b.Insert(o, mkVerdict(hash, denom, "1")); err != nil {
		t.Fatalf("place %s: %v", hash, err)
	}
}

// Canonical single fill: Alice buys 20 @100, Bob sells 20 @100. Alice rests
// first → maker. One fill at maker price, correct fees and roles, book empties.
func TestMatchCanonicalAliceBuyBobSell(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "alice-buy", "alice", types.SideBuy, "100", "20")
	place(t, b, "bob-sell", "bob", types.SideSell, "100", "20")

	fills, _, err := NewMatchingEngine().Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("want 1 fill, got %d: %+v", len(fills), fills)
	}
	f := fills[0]
	// notional = 100*20 = 2000; makerFee=2000*50/10000=10; takerFee=2000*100/10000=20.
	if f.Price != "100" || f.Qty != "20" || f.MakerFee != "10" || f.TakerFee != "20" {
		t.Fatalf("fill economics wrong: %+v", f)
	}
	if f.Buyer != "alice" || f.Seller != "bob" {
		t.Fatalf("sides wrong: buyer=%s seller=%s", f.Buyer, f.Seller)
	}
	if f.MakerOrderHash != "alice-buy" || f.TakerOrderHash != "bob-sell" {
		t.Fatalf("maker/taker roles wrong: %+v", f)
	}
	if f.TradeID == "" || f.Market != "ATOM/USDC" {
		t.Fatalf("tradeId/market missing: %+v", f)
	}
	// Book fully consumed.
	if _, ok := b.BestBid(); ok {
		t.Fatal("bid side should be empty")
	}
	if _, ok := b.BestAsk(); ok {
		t.Fatal("ask side should be empty")
	}
}

// Trade must execute at the MAKER price, not the taker (crossing) price. A
// resting ask @100 (maker) meets an aggressive buy @105 (taker) → price 100.
func TestMatchExecutesAtMakerPrice(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "bob-sell", "bob", types.SideSell, "100", "10") // maker (rests first)
	place(t, b, "alice-buy", "alice", types.SideBuy, "105", "10") // taker, crosses up

	fills, _, err := NewMatchingEngine().Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("want 1 fill, got %d", len(fills))
	}
	if fills[0].Price != "100" {
		t.Fatalf("must trade at maker price 100, got %s", fills[0].Price)
	}
	if fills[0].MakerOrderHash != "bob-sell" || fills[0].TakerOrderHash != "alice-buy" {
		t.Fatalf("maker should be the resting ask: %+v", fills[0])
	}
}

// One aggressive taker sweeps two resting makers at different price levels; each
// fill uses its own maker's price. Also exercises partial fill + level removal.
func TestMatchPartialFillAcrossLevels(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "bob", "bob", types.SideSell, "100", "8")   // maker seq1
	place(t, b, "carol", "carol", types.SideSell, "101", "12") // maker seq2
	place(t, b, "alice", "alice", types.SideBuy, "105", "20")  // taker seq3, sweeps both

	fills, _, err := NewMatchingEngine().Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 2 {
		t.Fatalf("want 2 fills, got %d: %+v", len(fills), fills)
	}
	if fills[0].Price != "100" || fills[0].Qty != "8" || fills[0].Seller != "bob" {
		t.Fatalf("fill#1 wrong: %+v", fills[0])
	}
	if fills[1].Price != "101" || fills[1].Qty != "12" || fills[1].Seller != "carol" {
		t.Fatalf("fill#2 wrong: %+v", fills[1])
	}
	if _, ok := b.BestBid(); ok {
		t.Fatal("taker should be fully filled")
	}
	// Distinct trade ids.
	if fills[0].TradeID == fills[1].TradeID {
		t.Fatal("trade ids must be distinct")
	}
}

// A taker that only partially fills leaves its remainder resting.
func TestMatchTakerRemainderRests(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "bob", "bob", types.SideSell, "100", "5")
	place(t, b, "alice", "alice", types.SideBuy, "100", "12") // wants 12, only 5 available

	fills, _, err := NewMatchingEngine().Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 1 || fills[0].Qty != "5" {
		t.Fatalf("want 1 fill of 5, got %+v", fills)
	}
	// Alice's remaining 7 still rests.
	rem, err := b.RemainingQty("alice")
	if err != nil || rem != "7" {
		t.Fatalf("alice remaining should be 7, got %s err=%v", rem, err)
	}
	if _, ok := b.BestAsk(); ok {
		t.Fatal("ask fully consumed")
	}
}

// Non-crossing book (bid < ask) produces no fills and leaves the book intact.
func TestMatchNonCrossingNoFills(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "alice", "alice", types.SideBuy, "99", "10")
	place(t, b, "bob", "bob", types.SideSell, "100", "10")

	fills, _, err := NewMatchingEngine().Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 0 {
		t.Fatalf("non-crossing should yield 0 fills, got %d", len(fills))
	}
	if bid, ok := b.BestBid(); !ok || bid.Remaining != "10" {
		t.Fatal("bid should be untouched")
	}
	if ask, ok := b.BestAsk(); !ok || ask.Remaining != "10" {
		t.Fatal("ask should be untouched")
	}
}

// DoD: same input run twice → byte-identical fill sequence.
func TestMatchDeterministic(t *testing.T) {
	build := func() []types.Fill {
		b := NewOrderbook("ATOM/USDC", nil, nil)
		place(t, b, "bob", "bob", types.SideSell, "100", "8")
		place(t, b, "carol", "carol", types.SideSell, "101", "12")
		place(t, b, "dave", "dave", types.SideSell, "102", "5")
		place(t, b, "alice", "alice", types.SideBuy, "105", "20")
		place(t, b, "erin", "erin", types.SideBuy, "101", "10")
		f, _, err := NewMatchingEngine().Match(b, matchMarket())
		if err != nil {
			t.Fatalf("match: %v", err)
		}
		return f
	}
	a := build()
	c := build()
	if !reflect.DeepEqual(a, c) {
		t.Fatalf("fills not deterministic:\n a=%+v\n c=%+v", a, c)
	}
	ja, _ := json.Marshal(a)
	jc, _ := json.Marshal(c)
	if string(ja) != string(jc) {
		t.Fatal("fill JSON not byte-identical")
	}
}

func TestTradeIDForDeterministicAndSensitive(t *testing.T) {
	base := TradeIDFor("ATOM/USDC", "mk", "tk", 0)
	if base != TradeIDFor("ATOM/USDC", "mk", "tk", 0) {
		t.Fatal("tradeId must be deterministic")
	}
	if base == TradeIDFor("ATOM/USDC", "mk", "tk", 1) {
		t.Fatal("tradeId must change with fill index")
	}
	if base == TradeIDFor("OSMO/USDC", "mk", "tk", 0) {
		t.Fatal("tradeId must change with market")
	}
	if base == TradeIDFor("ATOM/USDC", "tk", "mk", 0) {
		t.Fatal("tradeId must change when maker/taker swap")
	}
}

// Market mismatch between engine config and book is an error.
// Self-Trade Prevention (cancel-newest): a resting order must never fill against
// another order from the SAME owner. Alice's bid rests first (maker); Alice's own
// crossing sell arrives → the NEWER order (the sell) is cancelled, no fill, and
// the resting bid stays. Returned in the STP-cancelled list.
func TestMatchStpCancelNewestOnSelfCross(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "alice-buy", "alice", types.SideBuy, "100", "20")   // rests first (seq1)
	place(t, b, "alice-sell", "alice", types.SideSell, "100", "20") // same owner, crosses (seq2)

	fills, cancelled, err := NewMatchingEngine().Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 0 {
		t.Fatalf("self-trade must produce 0 fills, got %d: %+v", len(fills), fills)
	}
	if len(cancelled) != 1 {
		t.Fatalf("want 1 STP-cancelled order, got %d", len(cancelled))
	}
	// cancel-newest → the newer order (alice-sell, seq2) is the victim.
	if cancelled[0].OrderHash != "alice-sell" {
		t.Fatalf("cancel-newest must cancel the newer order, got %s", cancelled[0].OrderHash)
	}
	if cancelled[0].Remaining != "20" {
		t.Fatalf("cancelled order remaining (un-filled qty) = %s, want 20", cancelled[0].Remaining)
	}
	// The older bid stays resting; the cancelled ask is gone.
	if _, ok := b.BestBid(); !ok {
		t.Fatal("older resting bid must remain")
	}
	if _, ok := b.BestAsk(); ok {
		t.Fatal("cancelled ask must be gone from the book")
	}
}

// STP must NOT affect orders from DIFFERENT owners — a normal cross still fills,
// and no order is STP-cancelled.
func TestMatchStpLeavesDistinctOwnersUntouched(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "alice-buy", "alice", types.SideBuy, "100", "20")
	place(t, b, "bob-sell", "bob", types.SideSell, "100", "20")

	fills, cancelled, err := NewMatchingEngine().Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("distinct owners must fill: want 1 fill, got %d", len(fills))
	}
	if len(cancelled) != 0 {
		t.Fatalf("distinct owners must NOT trigger STP, got %d cancelled", len(cancelled))
	}
}

// cancel-newest must fill against OTHER owners FIRST, then cancel only the
// self-crossing remainder. Alice's aggressive sell (40) fills Bob's better bid
// @100 (20), then its 20-remainder would cross Alice's own bid @99 → STP cancels
// just the remainder. Result: 1 fill (bob×alice), alice-sell cancelled w/ rem=20.
func TestMatchStpFillsOtherOwnerThenCancelsRemainder(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "alice-buy", "alice", types.SideBuy, "99", "20")   // seq1 (Alice resting bid, lower)
	place(t, b, "bob-buy", "bob", types.SideBuy, "100", "20")      // seq2 (Bob bid, better price)
	place(t, b, "alice-sell", "alice", types.SideSell, "98", "40") // seq3 (Alice aggressive sell)

	fills, cancelled, err := NewMatchingEngine().Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	// Bob's better bid fills first (maker price 100, qty 20).
	if len(fills) != 1 {
		t.Fatalf("want 1 fill (bob × alice), got %d: %+v", len(fills), fills)
	}
	if fills[0].Buyer != "bob" || fills[0].Seller != "alice" || fills[0].Qty != "20" {
		t.Fatalf("fill must be bob buys 20 from alice, got %+v", fills[0])
	}
	// The remaining 20 of alice-sell would cross Alice's own bid → STP-cancelled.
	if len(cancelled) != 1 || cancelled[0].OrderHash != "alice-sell" {
		t.Fatalf("expected alice-sell remainder STP-cancelled, got %+v", cancelled)
	}
	if cancelled[0].Remaining != "20" {
		t.Fatalf("cancelled remainder = %s, want 20", cancelled[0].Remaining)
	}
}

// cancel-oldest: on a same-owner self-cross the RESTING (older) order is
// cancelled and the NEW order survives (rests / matches others). Alice's bid
// rests first; her crossing sell arrives → the bid (older) is cancelled, the
// sell stays resting as an ask.
func TestMatchStpCancelOldest(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "alice-buy", "alice", types.SideBuy, "100", "20")   // seq1 (older)
	place(t, b, "alice-sell", "alice", types.SideSell, "100", "20") // seq2 (newer)

	fills, cancelled, err := NewMatchingEngineWithMode(StpCancelOldest).Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 0 {
		t.Fatalf("self-trade must produce 0 fills, got %d", len(fills))
	}
	if len(cancelled) != 1 || cancelled[0].OrderHash != "alice-buy" {
		t.Fatalf("cancel-oldest must cancel the resting bid, got %+v", cancelled)
	}
	// The newer sell survives and rests as the best ask; the bid is gone.
	if _, ok := b.BestBid(); ok {
		t.Fatal("older bid must be cancelled")
	}
	if ask, ok := b.BestAsk(); !ok || ask.OrderHash != "alice-sell" {
		t.Fatalf("newer sell must remain resting as ask, got %+v (ok=%v)", ask, ok)
	}
}

// cancel-both: both crossing same-owner orders are cancelled; the book empties.
func TestMatchStpCancelBoth(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	place(t, b, "alice-buy", "alice", types.SideBuy, "100", "20")
	place(t, b, "alice-sell", "alice", types.SideSell, "100", "20")

	fills, cancelled, err := NewMatchingEngineWithMode(StpCancelBoth).Match(b, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 0 {
		t.Fatalf("self-trade must produce 0 fills, got %d", len(fills))
	}
	if len(cancelled) != 2 {
		t.Fatalf("cancel-both must cancel 2 orders, got %d", len(cancelled))
	}
	if _, ok := b.BestBid(); ok {
		t.Fatal("bid must be cancelled")
	}
	if _, ok := b.BestAsk(); ok {
		t.Fatal("ask must be cancelled")
	}
}

func TestParseStpMode(t *testing.T) {
	cases := map[string]StpMode{
		"":               StpCancelNewest,
		"cancel-newest":  StpCancelNewest,
		"CANCEL-NEWEST":  StpCancelNewest,
		" cancel-oldest ": StpCancelOldest,
		"cancel-both":    StpCancelBoth,
		"garbage":        StpCancelNewest, // unknown → default
	}
	for in, want := range cases {
		if got := ParseStpMode(in); got != want {
			t.Errorf("ParseStpMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchMarketMismatch(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	m := matchMarket()
	m.Market = "OSMO/USDC"
	if _, _, err := NewMatchingEngine().Match(b, m); err == nil {
		t.Fatal("expected market-mismatch error")
	}
}
