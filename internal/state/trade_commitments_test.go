package state

import (
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func sampleOrders() []OrderCommitmentInput {
	return []OrderCommitmentInput{
		{OrderHash: "alice", Owner: "alice", Side: types.SideBuy, Price: "100", Qty: "20", Remaining: "0", Filled: true, Sequence: 1},
		{OrderHash: "bob", Owner: "bob", Side: types.SideSell, Price: "100", Qty: "20", Remaining: "0", Filled: true, Sequence: 2},
	}
}

func sampleFills() []types.Fill {
	return []types.Fill{
		{TradeID: "t1", Market: "ATOM/USDC", MakerOrderHash: "alice", TakerOrderHash: "bob",
			Price: "100", Qty: "20", MakerFee: "10", TakerFee: "20", Buyer: "alice", Seller: "bob"},
	}
}

func TestBuildTradeCommitmentsDeterministic(t *testing.T) {
	a, err := BuildTradeCommitments(sampleOrders(), sampleFills())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	b, err := BuildTradeCommitments(sampleOrders(), sampleFills())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if a.OrdersRoot != b.OrdersRoot || a.TradesRoot != b.TradesRoot {
		t.Fatalf("roots not deterministic:\n a=%+v\n b=%+v", a, b)
	}
	if a.OrdersRoot == "" || a.TradesRoot == "" {
		t.Fatal("roots must be non-empty hex")
	}
	// Nullifiers list = sorted unique, one per order, matching OrderNullifierFor.
	if len(a.OrderNullifiers) != 2 {
		t.Fatalf("want 2 nullifiers, got %d", len(a.OrderNullifiers))
	}
	wantAlice, _ := OrderNullifierFor("alice", "alice")
	found := false
	for _, n := range a.OrderNullifiers {
		if n == wantAlice {
			found = true
		}
	}
	if !found {
		t.Fatal("nullifier list missing alice's order nullifier")
	}
}

// ordersRoot must be independent of the caller's slice order (leaves sorted by
// sequence). tradesRoot must depend on fill order (matching order is the proof).
func TestOrdersRootOrderIndependentTradesRootOrderSensitive(t *testing.T) {
	orders := sampleOrders()
	reversedOrders := []OrderCommitmentInput{orders[1], orders[0]}
	r1, _ := OrdersRoot(orders)
	r2, _ := OrdersRoot(reversedOrders)
	if r1 != r2 {
		t.Fatal("ordersRoot must be independent of input slice order")
	}

	fills := []types.Fill{
		{TradeID: "t1", Market: "M", Price: "100", Qty: "1"},
		{TradeID: "t2", Market: "M", Price: "101", Qty: "1"},
	}
	reversedFills := []types.Fill{fills[1], fills[0]}
	if TradesRoot(fills) == TradesRoot(reversedFills) {
		t.Fatal("tradesRoot must change when fill (matching) order changes")
	}
}

func TestDuplicateOrderNullifierDetected(t *testing.T) {
	orders := []OrderCommitmentInput{
		{OrderHash: "h", Owner: "alice", Side: types.SideBuy, Price: "1", Qty: "1", Sequence: 1},
		{OrderHash: "h", Owner: "alice", Side: types.SideBuy, Price: "1", Qty: "1", Sequence: 2}, // same owner+hash
	}
	if _, err := OrdersRoot(orders); !errors.Is(err, ErrDuplicateOrderNullifier) {
		t.Fatalf("expected ErrDuplicateOrderNullifier, got %v", err)
	}
}

func TestEmptyRootsAreStableSentinels(t *testing.T) {
	if EmptyOrdersRoot() == "" || EmptyTradesRoot() == "" {
		t.Fatal("empty roots must be non-empty sentinels")
	}
	// Non-empty batch differs from the empty sentinel.
	nonEmpty, _ := OrdersRoot(sampleOrders())
	if nonEmpty == EmptyOrdersRoot() {
		t.Fatal("non-empty ordersRoot must differ from empty sentinel")
	}
	if TradesRoot(sampleFills()) == EmptyTradesRoot() {
		t.Fatal("non-empty tradesRoot must differ from empty sentinel")
	}
	// Empty is stable.
	if EmptyOrdersRoot() != EmptyOrdersRoot() || EmptyTradesRoot() != EmptyTradesRoot() {
		t.Fatal("empty roots must be stable")
	}
}

// The filled status is bound into ordersRoot: same order open vs filled → root
// differs (a proof cannot lie about whether an order was consumed).
func TestOrdersRootBindsFilledStatus(t *testing.T) {
	open := []OrderCommitmentInput{{OrderHash: "h", Owner: "a", Side: types.SideBuy, Price: "1", Qty: "5", Remaining: "5", Filled: false, Sequence: 1}}
	filled := []OrderCommitmentInput{{OrderHash: "h", Owner: "a", Side: types.SideBuy, Price: "1", Qty: "5", Remaining: "0", Filled: true, Sequence: 1}}
	ro, _ := OrdersRoot(open)
	rf, _ := OrdersRoot(filled)
	if ro == rf {
		t.Fatal("ordersRoot must change with filled status / remaining")
	}
}

// Every trade field is bound into tradesRoot.
func TestTradesRootFieldSensitivity(t *testing.T) {
	base := sampleFills()
	baseRoot := TradesRoot(base)

	mutators := []func(f *types.Fill){
		func(f *types.Fill) { f.Price = "101" },
		func(f *types.Fill) { f.Qty = "21" },
		func(f *types.Fill) { f.MakerFee = "11" },
		func(f *types.Fill) { f.TakerFee = "21" },
		func(f *types.Fill) { f.Buyer = "carol" },
		func(f *types.Fill) { f.Seller = "dave" },
		func(f *types.Fill) { f.MakerOrderHash = "x" },
		func(f *types.Fill) { f.TradeID = "t2" },
	}
	for i, mut := range mutators {
		f := sampleFills()
		mut(&f[0])
		if TradesRoot(f) == baseRoot {
			t.Fatalf("mutator %d did not change tradesRoot", i)
		}
	}
}

func TestTradeCommitmentDomainTagsDistinct(t *testing.T) {
	o, tr := TradeCommitmentDomainTags()
	if o == tr || o == "" || tr == "" {
		t.Fatalf("domain tags must be distinct non-empty: %q %q", o, tr)
	}
}
