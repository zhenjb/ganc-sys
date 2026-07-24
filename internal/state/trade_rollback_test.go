package state

import (
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// setupTradeScenario wires manager + funded accounts + book with two crossing
// orders placed (collateral reserved). Returns the pieces plus the batch's order
// nullifiers.
func setupTradeScenario(t *testing.T, sellQty string) (*OffchainStateManager, *Orderbook, *InMemoryOrderNullifiers, []string) {
	t.Helper()
	m := NewOffchainStateManager()
	mustDeposit(t, m, "d1", "alice", "uusdc", "5000")
	mustDeposit(t, m, "d2", "bob", "uatom", "50")

	nulls := NewInMemoryOrderNullifiers()
	book := NewOrderbook("ATOM/USDC", m, nulls)
	placeReserved(t, book, "alice", "alice", types.SideBuy, "100", "20", "uusdc", "2020")
	placeReserved(t, book, "bob", "bob", types.SideSell, "100", sellQty, "uatom", sellQty)

	return m, book, nulls, []string{"null-alice", "null-bob"}
}

var tradeSides = map[string]types.OrderSide{"alice": types.SideBuy, "bob": types.SideSell}

func TestTradeRollbackFullFillRestoresBaseline(t *testing.T) {
	m, book, nulls, nullifiers := setupTradeScenario(t, "20")

	// Baseline = state with orders resting + collateral reserved, pre-match.
	baseline := CaptureTradeBaseline(m, book, nullifiers)
	baseRoot := baseline.Root()

	// Match + apply (mutates manager balances/reserved + book + fee account).
	fills, _, err := NewMatchingEngine().Match(book, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if _, err := NewTradeApplier("").Apply(m, book, fills, matchMarket(), tradeSides); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Simulate settlement marking the filled orders' nullifiers used.
	nulls.MarkUsed("null-alice")
	nulls.MarkUsed("null-bob")

	// Sanity: state actually changed.
	if m.Root() == baseRoot {
		t.Fatal("precondition: apply should have changed the root")
	}
	if m.Account(FeeAccountOwner, "uusdc").Balance != "30" {
		t.Fatal("precondition: fee account should be credited")
	}

	// --- Rollback ---
	got := NewTradeRollback(nulls).Rollback(m, book, baseline)

	if got != baseRoot || m.Root() != baseRoot {
		t.Fatalf("root not restored to baseline: got %s want %s", got, baseRoot)
	}
	// Balances + reserved back to pre-batch.
	checks := []struct{ owner, denom, avail, reserved string }{
		{"alice", "uusdc", "2980", "2020"},
		{"alice", "uatom", "0", ""},
		{"bob", "uatom", "30", "20"},
		{"bob", "uusdc", "0", ""},
		{FeeAccountOwner, "uusdc", "0", ""}, // fee refunded
	}
	for _, c := range checks {
		acc := m.Account(c.owner, c.denom)
		if acc.Balance != c.avail || acc.Reserved != c.reserved {
			t.Fatalf("%s/%s = %q/%q, want %q/%q", c.owner, c.denom, acc.Balance, acc.Reserved, c.avail, c.reserved)
		}
	}
	// Reservations restored.
	if r, ok := m.Reservation("alice"); !ok || r.Amount != "2020" {
		t.Fatalf("alice reservation not restored: %+v ok=%v", r, ok)
	}
	// Orders reopened with full remaining.
	if rem, err := book.RemainingQty("alice"); err != nil || rem != "20" {
		t.Fatalf("alice order not reopened: rem=%s err=%v", rem, err)
	}
	if rem, err := book.RemainingQty("bob"); err != nil || rem != "20" {
		t.Fatalf("bob order not reopened: rem=%s err=%v", rem, err)
	}
	if bid, ok := book.BestBid(); !ok || bid.OrderHash != "alice" {
		t.Fatal("best bid not restored")
	}
	// Nullifiers released.
	if nulls.IsOrderNullifierUsed("null-alice") || nulls.IsOrderNullifierUsed("null-bob") {
		t.Fatal("order nullifiers should be released after rollback")
	}
}

func TestTradeRollbackIsIdempotent(t *testing.T) {
	m, book, nulls, nullifiers := setupTradeScenario(t, "20")
	baseline := CaptureTradeBaseline(m, book, nullifiers)
	baseRoot := baseline.Root()

	fills, _, _ := NewMatchingEngine().Match(book, matchMarket())
	_, _ = NewTradeApplier("").Apply(m, book, fills, matchMarket(), tradeSides)

	tr := NewTradeRollback(nulls)
	tr.Rollback(m, book, baseline)
	// Second rollback must not corrupt anything.
	got := tr.Rollback(m, book, baseline)

	if got != baseRoot {
		t.Fatalf("idempotency broken: root %s != baseline %s", got, baseRoot)
	}
	if acc := m.Account("alice", "uusdc"); acc.Balance != "2980" || acc.Reserved != "2020" {
		t.Fatalf("idempotency broken: alice %q/%q", acc.Balance, acc.Reserved)
	}
	if rem, err := book.RemainingQty("alice"); err != nil || rem != "20" {
		t.Fatalf("idempotency broken: alice rem=%s err=%v", rem, err)
	}
}

// Rolling back a partial fill restores the taker's full remaining and reserved.
func TestTradeRollbackPartialFill(t *testing.T) {
	m, book, nulls, nullifiers := setupTradeScenario(t, "8") // bob only sells 8
	baseline := CaptureTradeBaseline(m, book, nullifiers)

	fills, _, err := NewMatchingEngine().Match(book, matchMarket())
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(fills) != 1 || fills[0].Qty != "8" {
		t.Fatalf("want 1 fill of 8, got %+v", fills)
	}
	if _, err := NewTradeApplier("").Apply(m, book, fills, matchMarket(), tradeSides); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// alice partially filled (remaining 12), bob fully filled (gone).
	if rem, _ := book.RemainingQty("alice"); rem != "12" {
		t.Fatalf("precondition: alice remaining should be 12, got %s", rem)
	}

	NewTradeRollback(nulls).Rollback(m, book, baseline)

	// alice restored to full remaining 20 + reserved 2020; bob reopened, reserved 8.
	if rem, err := book.RemainingQty("alice"); err != nil || rem != "20" {
		t.Fatalf("alice not restored: rem=%s err=%v", rem, err)
	}
	if acc := m.Account("alice", "uusdc"); acc.Balance != "2980" || acc.Reserved != "2020" {
		t.Fatalf("alice balance not restored: %q/%q", acc.Balance, acc.Reserved)
	}
	if rem, err := book.RemainingQty("bob"); err != nil || rem != "8" {
		t.Fatalf("bob not reopened: rem=%s err=%v", rem, err)
	}
	if acc := m.Account("bob", "uatom"); acc.Balance != "42" || acc.Reserved != "8" {
		t.Fatalf("bob balance not restored: %q/%q (want 42/8)", acc.Balance, acc.Reserved)
	}
}

// Orderbook.Capture/Restore is a self-contained deep snapshot.
func TestOrderbookCaptureRestore(t *testing.T) {
	book := NewOrderbook("ATOM/USDC", nil, nil)
	insert(t, book, types.SideBuy, "b1", "10.5", "5")
	insert(t, book, types.SideBuy, "b2", "10.0", "5")
	insert(t, book, types.SideSell, "a1", "11.0", "5")

	snap := book.Capture()
	if snap.Len() != 3 {
		t.Fatalf("snapshot should hold 3 orders, got %d", snap.Len())
	}

	// Mutate: fill part of b1, cancel a1.
	if _, _, err := book.Reduce("b1", "3"); err != nil {
		t.Fatalf("reduce: %v", err)
	}
	if err := book.Cancel("a1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	book.Restore(snap)

	if rem, err := book.RemainingQty("b1"); err != nil || rem != "5" {
		t.Fatalf("b1 not restored: rem=%s err=%v", rem, err)
	}
	if _, err := book.RemainingQty("a1"); err != nil {
		t.Fatalf("a1 should be restored: %v", err)
	}
	// Priority preserved: best bid is b1 (10.5) then b2 (10.0).
	if bid, ok := book.BestBid(); !ok || bid.OrderHash != "b1" {
		t.Fatal("priority not restored")
	}
}
