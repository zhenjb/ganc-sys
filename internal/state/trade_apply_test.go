package state

import (
	"errors"
	"math/big"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func mustDeposit(t *testing.T, m *OffchainStateManager, id, owner, denom, amount string) {
	t.Helper()
	if _, err := m.ApplyDeposit(types.DepositRecord{DepositID: id, Owner: owner, Denom: denom, Amount: amount}); err != nil {
		t.Fatalf("deposit %s: %v", id, err)
	}
}

func bal(t *testing.T, m *OffchainStateManager, owner, denom string) *big.Int {
	t.Helper()
	v, ok := new(big.Int).SetString(m.Account(owner, denom).Balance, 10)
	if !ok {
		t.Fatalf("bad balance for %s/%s", owner, denom)
	}
	return v
}

func reservedOf(t *testing.T, m *OffchainStateManager, owner, denom string) string {
	t.Helper()
	return m.Account(owner, denom).Reserved
}

// placeReserved deposits nothing; it inserts an order into a book wired to the
// manager, which reserves the given collateral.
func placeReserved(t *testing.T, book *Orderbook, hash, owner string, side types.OrderSide, price, qty, denom, amount string) {
	t.Helper()
	o := mkOrder(owner, "ATOM/USDC", side, price, qty)
	v := OrderValidation{Accepted: true, OrderHash: hash, OrderNullifier: "null-" + hash, ReserveDenom: denom, ReserveAmount: amount}
	if _, err := book.Insert(o, v); err != nil {
		t.Fatalf("place %s: %v", hash, err)
	}
}

// Full pipeline: deposit -> reserve (insert) -> match -> apply. Asserts value
// conservation per denom, reserved=0 for filled orders, root change, and exact
// balances.
func TestTradeApplyCanonicalConservation(t *testing.T) {
	m := NewOffchainStateManager()
	market := matchMarket() // maker 50bps, taker 100bps, base uatom, quote uusdc

	// Fund: alice 5000 uusdc, bob 50 uatom.
	mustDeposit(t, m, "d1", "alice", "uusdc", "5000")
	mustDeposit(t, m, "d2", "bob", "uatom", "50")
	totalQuoteBefore := bal(t, m, "alice", "uusdc") // 5000, others 0
	totalBaseBefore := bal(t, m, "bob", "uatom")    // 50

	book := NewOrderbook("ATOM/USDC", m, nil)
	// alice buys 20@100 first (maker); reserve = ceil(2000)+ceil(2000*100/10000)=2020.
	placeReserved(t, book, "alice", "alice", types.SideBuy, "100", "20", "uusdc", "2020")
	// bob sells 20@100 (taker); reserve base = 20.
	placeReserved(t, book, "bob", "bob", types.SideSell, "100", "20", "uatom", "20")

	rootBefore := m.Root()

	fills, err := NewMatchingEngine().Match(book, market)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 1 {
		t.Fatalf("want 1 fill, got %d", len(fills))
	}

	sides := map[string]types.OrderSide{"alice": types.SideBuy, "bob": types.SideSell}
	res, err := NewTradeApplier("").Apply(m, book, fills, market, sides)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// --- Exact balances ---
	// notional 2000; buyerFee (alice=maker) 10; sellerFee (bob=taker) 20.
	// alice quote: 5000 -2020 reserve +10 leftover-release = 2990 available, 0 reserved.
	// alice base:  +20.
	// bob   base:  50 -20 reserve = 30 available, 0 reserved.
	// bob   quote: notional - sellerFee = 1980.
	// fee   quote: 10+20 = 30.
	checks := []struct {
		owner, denom, wantAvail, wantReserved string
	}{
		{"alice", "uusdc", "2990", ""},
		{"alice", "uatom", "20", ""},
		{"bob", "uatom", "30", ""},
		{"bob", "uusdc", "1980", ""},
		{FeeAccountOwner, "uusdc", "30", ""},
	}
	for _, c := range checks {
		acc := m.Account(c.owner, c.denom)
		if acc.Balance != c.wantAvail || acc.Reserved != c.wantReserved {
			t.Fatalf("%s/%s = avail %q reserved %q, want %q/%q",
				c.owner, c.denom, acc.Balance, acc.Reserved, c.wantAvail, c.wantReserved)
		}
	}

	// --- Value conservation per denom ---
	quoteAfter := new(big.Int)
	quoteAfter.Add(quoteAfter, bal(t, m, "alice", "uusdc"))
	quoteAfter.Add(quoteAfter, bal(t, m, "bob", "uusdc"))
	quoteAfter.Add(quoteAfter, bal(t, m, FeeAccountOwner, "uusdc"))
	if quoteAfter.Cmp(totalQuoteBefore) != 0 {
		t.Fatalf("quote not conserved: before %s after %s", totalQuoteBefore, quoteAfter)
	}
	baseAfter := new(big.Int)
	baseAfter.Add(baseAfter, bal(t, m, "alice", "uatom"))
	baseAfter.Add(baseAfter, bal(t, m, "bob", "uatom"))
	if baseAfter.Cmp(totalBaseBefore) != 0 {
		t.Fatalf("base not conserved: before %s after %s", totalBaseBefore, baseAfter)
	}

	// --- Root changed and result reports fee ---
	if res.NewRoot == rootBefore {
		t.Fatal("root must change after applying a trade")
	}
	if res.FeeCredited != "30" || res.FeeDenom != "uusdc" || res.FillsApplied != 1 {
		t.Fatalf("result summary wrong: %+v", res)
	}
}

// A mid-batch failure must roll the manager back to its pre-Apply state (no
// partial trade, no negative balance).
func TestTradeApplyRollsBackOnFailure(t *testing.T) {
	m := NewOffchainStateManager()
	market := matchMarket()
	mustDeposit(t, m, "d1", "alice", "uusdc", "5000")
	mustDeposit(t, m, "d2", "bob", "uatom", "50")

	book := NewOrderbook("ATOM/USDC", m, nil)
	placeReserved(t, book, "alice", "alice", types.SideBuy, "100", "20", "uusdc", "2020")
	// Seller under-reserves (10) but sells 20 → apply's seller consume will fail.
	placeReserved(t, book, "bob", "bob", types.SideSell, "100", "20", "uatom", "10")

	fills, err := NewMatchingEngine().Match(book, market)
	if err != nil {
		t.Fatalf("match: %v", err)
	}

	// Capture state right before Apply.
	aliceQuote := m.Account("alice", "uusdc")
	bobBase := m.Account("bob", "uatom")
	rootBefore := m.Root()

	sides := map[string]types.OrderSide{"alice": types.SideBuy, "bob": types.SideSell}
	_, err = NewTradeApplier("").Apply(m, book, fills, market, sides)
	if !errors.Is(err, ErrTradeApply) {
		t.Fatalf("expected ErrTradeApply, got %v", err)
	}

	// State must be byte-identical to before Apply (buyer consume was rolled back).
	if got := m.Account("alice", "uusdc"); got != aliceQuote {
		t.Fatalf("alice quote not restored: %+v vs %+v", got, aliceQuote)
	}
	if got := m.Account("bob", "uatom"); got != bobBase {
		t.Fatalf("bob base not restored: %+v vs %+v", got, bobBase)
	}
	if m.Root() != rootBefore {
		t.Fatalf("root not restored after rollback")
	}
	// No fee account created by a failed apply.
	if m.Account(FeeAccountOwner, "uusdc").Balance != "0" {
		t.Fatal("fee account should be untouched after rollback")
	}
}

// Empty fills is a no-op returning the current root.
func TestTradeApplyEmptyFillsNoop(t *testing.T) {
	m := NewOffchainStateManager()
	market := matchMarket()
	book := NewOrderbook("ATOM/USDC", m, nil)
	root := m.Root()
	res, err := NewTradeApplier("").Apply(m, book, nil, market, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.NewRoot != root {
		t.Fatal("empty apply must not change root")
	}
}

// Partial fill: a taker larger than the resting maker leaves the taker resting
// with reserved still backing its unfilled remainder (not released).
func TestTradeApplyPartialFillKeepsTakerReserved(t *testing.T) {
	m := NewOffchainStateManager()
	market := matchMarket()
	mustDeposit(t, m, "d1", "alice", "uusdc", "10000")
	mustDeposit(t, m, "d2", "bob", "uatom", "50")

	book := NewOrderbook("ATOM/USDC", m, nil)
	// bob sells 8 @100 first (maker). reserve base 8.
	placeReserved(t, book, "bob", "bob", types.SideSell, "100", "8", "uatom", "8")
	// alice buys 20 @100 (taker). reserve quote = ceil(2000)+ceil(2000*100/10000)=2020.
	placeReserved(t, book, "alice", "alice", types.SideBuy, "100", "20", "uusdc", "2020")

	fills, err := NewMatchingEngine().Match(book, market)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 1 || fills[0].Qty != "8" {
		t.Fatalf("want 1 fill of 8, got %+v", fills)
	}

	sides := map[string]types.OrderSide{"alice": types.SideBuy, "bob": types.SideSell}
	if _, err := NewTradeApplier("").Apply(m, book, fills, market, sides); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// bob fully filled: reserved base 0.
	if r := reservedOf(t, m, "bob", "uatom"); r != "" {
		t.Fatalf("bob reserved should be 0, got %q", r)
	}
	// alice partially filled (12 remaining in book): still reserved (NOT released).
	// notional 8*100=800, buyerFee(taker)=floor(800*100/10000)=8 → consumed 808 of 2020.
	if r := reservedOf(t, m, "alice", "uusdc"); r != "1212" {
		t.Fatalf("alice reserved should stay 1212 (2020-808), got %q", r)
	}
	if rem, _ := book.RemainingQty("alice"); rem != "12" {
		t.Fatalf("alice should have 12 remaining, got %s", rem)
	}
}
