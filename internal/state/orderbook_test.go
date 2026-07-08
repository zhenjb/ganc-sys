package state

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func mkOrder(owner, market string, side types.OrderSide, price, qty string) types.SignedOrder {
	return types.SignedOrder{
		Owner: owner, Market: market, Side: side,
		Price: price, Qty: qty, Expiry: "2000000", Nonce: "1", Signature: "0xsig",
	}
}

func mkVerdict(hash, denom, amount string) OrderValidation {
	return OrderValidation{
		Accepted:       true,
		OrderHash:      hash,
		OrderNullifier: "null-" + hash,
		ReserveDenom:   denom,
		ReserveAmount:  amount,
	}
}

type fakeReservation struct {
	reserves    []string
	releases    []string
	failReserve bool
}

func (f *fakeReservation) Reserve(owner, denom, amount, orderHash string) (string, error) {
	if f.failReserve {
		return "", errors.New("insufficient")
	}
	f.reserves = append(f.reserves, orderHash)
	return "root", nil
}
func (f *fakeReservation) ReleaseOrder(orderHash string) (string, error) {
	f.releases = append(f.releases, orderHash)
	return "root", nil
}

type fakeMarker struct{ used []string }

func (f *fakeMarker) MarkUsed(n string) { f.used = append(f.used, n) }

// insertBid/insertAsk are helpers that rest an order and fail the test on error.
func insert(t *testing.T, b *Orderbook, side types.OrderSide, hash, price, qty string) {
	t.Helper()
	o := mkOrder("owner-"+hash, "ATOM/USDC", side, price, qty)
	denom := "uusdc"
	if side == types.SideSell {
		denom = "uatom"
	}
	if _, err := b.Insert(o, mkVerdict(hash, denom, "1")); err != nil {
		t.Fatalf("insert %s: %v", hash, err)
	}
}

func TestOrderbookPriceTimePriorityBids(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	// Insert order matters for time priority.
	insert(t, b, types.SideBuy, "b1", "10.0", "1")
	insert(t, b, types.SideBuy, "b2", "10.5", "1") // higher price -> best
	insert(t, b, types.SideBuy, "b3", "10.5", "1") // same price, later -> behind b2
	insert(t, b, types.SideBuy, "b4", "9.5", "1")

	best, ok := b.BestBid()
	if !ok || best.OrderHash != "b2" {
		t.Fatalf("best bid should be b2 (highest price, earliest), got %+v", best)
	}
	// Cancel b2 -> next best is b3 (same price, next in FIFO).
	if err := b.Cancel("b2"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	best, _ = b.BestBid()
	if best.OrderHash != "b3" {
		t.Fatalf("after cancel best bid should be b3, got %s", best.OrderHash)
	}
}

func TestOrderbookPriceTimePriorityAsks(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	insert(t, b, types.SideSell, "a1", "11.0", "1")
	insert(t, b, types.SideSell, "a2", "10.5", "1") // lowest price -> best
	insert(t, b, types.SideSell, "a3", "10.5", "1") // same price, later
	best, ok := b.BestAsk()
	if !ok || best.OrderHash != "a2" {
		t.Fatalf("best ask should be a2 (lowest price, earliest), got %+v", best)
	}
	if err := b.Cancel("a2"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if best, _ = b.BestAsk(); best.OrderHash != "a3" {
		t.Fatalf("after cancel best ask should be a3, got %s", best.OrderHash)
	}
}

// Equal prices with different scales must share one level and keep FIFO order.
func TestOrderbookEqualPriceDifferentScaleShareLevel(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	insert(t, b, types.SideBuy, "x1", "10.5", "1")
	insert(t, b, types.SideBuy, "x2", "10.50", "1") // same economic price
	snap := b.Snapshot()
	if len(snap.Bids) != 2 {
		t.Fatalf("want 2 bids, got %d", len(snap.Bids))
	}
	if snap.Bids[0].OrderHash != "x1" || snap.Bids[1].OrderHash != "x2" {
		t.Fatalf("FIFO within shared level broken: %+v", snap.Bids)
	}
}

func TestOrderbookPartialFillKeepsPriority(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	insert(t, b, types.SideBuy, "b1", "10.5", "5")
	insert(t, b, types.SideBuy, "b2", "10.5", "5")

	rem, removed, err := b.Reduce("b1", "2")
	if err != nil {
		t.Fatalf("reduce: %v", err)
	}
	if removed || rem != "3" {
		t.Fatalf("partial fill: got remaining=%s removed=%v, want 3/false", rem, removed)
	}
	// b1 still first (priority preserved despite partial fill).
	if best, _ := b.BestBid(); best.OrderHash != "b1" || best.Remaining != "3" {
		t.Fatalf("priority not preserved after partial fill: %+v", best)
	}
	// Fill the rest -> removed, b2 becomes best.
	_, removed, err = b.Reduce("b1", "3")
	if err != nil || !removed {
		t.Fatalf("full fill: removed=%v err=%v", removed, err)
	}
	if best, _ := b.BestBid(); best.OrderHash != "b2" {
		t.Fatalf("after full fill best should be b2, got %s", best.OrderHash)
	}
	if _, err := b.RemainingQty("b1"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("filled order should be gone, got err=%v", err)
	}
}

func TestOrderbookReduceRejectsOverfill(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	insert(t, b, types.SideBuy, "b1", "10.5", "5")
	if _, _, err := b.Reduce("b1", "6"); !errors.Is(err, ErrInvalidFill) {
		t.Fatalf("overfill should be ErrInvalidFill, got %v", err)
	}
	if _, _, err := b.Reduce("b1", "0"); !errors.Is(err, ErrInvalidFill) {
		t.Fatalf("zero fill should be ErrInvalidFill, got %v", err)
	}
}

func TestOrderbookInsertReservesAndCancelReleases(t *testing.T) {
	res := &fakeReservation{}
	mark := &fakeMarker{}
	b := NewOrderbook("ATOM/USDC", res, mark)

	o := mkOrder("alice", "ATOM/USDC", types.SideBuy, "10.5", "2")
	v := mkVerdict("h1", "uusdc", "22")
	if _, err := b.Insert(o, v); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if len(res.reserves) != 1 || res.reserves[0] != "h1" {
		t.Fatalf("insert should reserve h1, got %v", res.reserves)
	}

	if err := b.Cancel("h1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if len(res.releases) != 1 || res.releases[0] != "h1" {
		t.Fatalf("cancel should release h1, got %v", res.releases)
	}
	if len(mark.used) != 1 || mark.used[0] != "null-h1" {
		t.Fatalf("cancel should mark nullifier used, got %v", mark.used)
	}
}

// A failed reservation must leave the book untouched (order not added).
func TestOrderbookInsertReserveFailureNotAdded(t *testing.T) {
	res := &fakeReservation{failReserve: true}
	b := NewOrderbook("ATOM/USDC", res, nil)
	o := mkOrder("alice", "ATOM/USDC", types.SideBuy, "10.5", "2")
	if _, err := b.Insert(o, mkVerdict("h1", "uusdc", "22")); err == nil {
		t.Fatal("expected insert error when reserve fails")
	}
	if _, ok := b.BestBid(); ok {
		t.Fatal("book should be empty after failed reserve")
	}
}

func TestOrderbookRejectsBadInserts(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)

	// non-accepted verdict
	o := mkOrder("alice", "ATOM/USDC", types.SideBuy, "10.5", "2")
	if _, err := b.Insert(o, OrderValidation{Accepted: false, Reason: ReasonExpired}); !errors.Is(err, ErrOrderNotAccepted) {
		t.Fatalf("want ErrOrderNotAccepted, got %v", err)
	}
	// wrong market
	wrong := mkOrder("alice", "BTC/USDC", types.SideBuy, "10.5", "2")
	if _, err := b.Insert(wrong, mkVerdict("h1", "uusdc", "1")); !errors.Is(err, ErrWrongMarket) {
		t.Fatalf("want ErrWrongMarket, got %v", err)
	}
	// duplicate orderHash
	if _, err := b.Insert(o, mkVerdict("dup", "uusdc", "1")); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := b.Insert(o, mkVerdict("dup", "uusdc", "1")); !errors.Is(err, ErrOrderExists) {
		t.Fatalf("want ErrOrderExists, got %v", err)
	}
}

func TestOrderbookEmptyBestReturnsFalse(t *testing.T) {
	b := NewOrderbook("ATOM/USDC", nil, nil)
	if _, ok := b.BestBid(); ok {
		t.Fatal("empty book bestBid should be false")
	}
	if _, ok := b.BestAsk(); ok {
		t.Fatal("empty book bestAsk should be false")
	}
}

// DoD: same insert sequence -> byte-identical snapshot (reproducibility).
func TestOrderbookSnapshotDeterministic(t *testing.T) {
	build := func() BookSnapshot {
		b := NewOrderbook("ATOM/USDC", nil, nil)
		insert(t, b, types.SideBuy, "b1", "10.0", "1")
		insert(t, b, types.SideBuy, "b2", "10.5", "2")
		insert(t, b, types.SideBuy, "b3", "10.5", "3")
		insert(t, b, types.SideSell, "a1", "11.0", "1")
		insert(t, b, types.SideSell, "a2", "10.9", "1")
		return b.Snapshot()
	}
	s1, s2 := build(), build()
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("snapshot not deterministic:\n s1=%+v\n s2=%+v", s1, s2)
	}
	// Bids best-first: 10.5(b2),10.5(b3),10.0(b1). Asks best-first: 10.9(a2),11.0(a1).
	wantBids := []string{"b2", "b3", "b1"}
	for i, h := range wantBids {
		if s1.Bids[i].OrderHash != h {
			t.Fatalf("bid order[%d]=%s want %s", i, s1.Bids[i].OrderHash, h)
		}
	}
	wantAsks := []string{"a2", "a1"}
	for i, h := range wantAsks {
		if s1.Asks[i].OrderHash != h {
			t.Fatalf("ask order[%d]=%s want %s", i, s1.Asks[i].OrderHash, h)
		}
	}
}

func TestBookSetGetOrCreate(t *testing.T) {
	set := NewBookSet(nil, nil)
	b1 := set.Book("ATOM/USDC")
	b2 := set.Book("ATOM/USDC")
	if b1 != b2 {
		t.Fatal("Book should return the same instance per market")
	}
	set.Book("OSMO/USDC")
	got := set.Markets()
	if len(got) != 2 || got[0] != "ATOM/USDC" || got[1] != "OSMO/USDC" {
		t.Fatalf("markets: %v", got)
	}
}
