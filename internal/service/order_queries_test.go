package service_test

import (
	"context"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// After creating an order it shows up in ListOpenOrders for its owner (tagged
// with market + fill progress); a different owner sees nothing (DoD: queries
// reflect current state, filtered by owner).
func TestListOpenOrdersReflectsCreate(t *testing.T) {
	svc, _, resp := createAliceBuy(t)

	got := svc.ListOpenOrders(context.Background(), "cosmos1alice")
	if len(got.OpenOrders) != 1 {
		t.Fatalf("open orders = %d, want 1", len(got.OpenOrders))
	}
	o := got.OpenOrders[0]
	if o.OrderHash != resp.State.OrderHash {
		t.Fatalf("orderHash = %q, want %q", o.OrderHash, resp.State.OrderHash)
	}
	if o.Market != "ATOM/USDC" || o.Side != types.SideBuy || o.Price != "100" {
		t.Fatalf("unexpected order: %+v", o)
	}
	if o.Remaining != "20" || o.Filled != "0" || o.Status != types.OrderStatusOpen {
		t.Fatalf("remaining/filled/status = %q/%q/%q, want 20/0/open", o.Remaining, o.Filled, o.Status)
	}

	if other := svc.ListOpenOrders(context.Background(), "cosmos1bob"); len(other.OpenOrders) != 0 {
		t.Fatalf("bob should have no open orders, got %d", len(other.OpenOrders))
	}
	if empty := svc.ListOpenOrders(context.Background(), ""); len(empty.OpenOrders) != 0 {
		t.Fatalf("empty owner should yield 0, got %d", len(empty.OpenOrders))
	}
}

// After cancel, the order disappears from ListOpenOrders (DoD).
func TestListOpenOrdersEmptyAfterCancel(t *testing.T) {
	svc, _, resp := createAliceBuy(t)
	if _, err := svc.CancelOrder(context.Background(), resp.State.OrderHash, "cosmos1alice"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got := svc.ListOpenOrders(context.Background(), "cosmos1alice")
	if len(got.OpenOrders) != 0 {
		t.Fatalf("open orders after cancel = %d, want 0", len(got.OpenOrders))
	}
}

// ListTrades starts empty and returns fills recorded via the INT-T05 seam,
// filtered by market.
func TestListTradesReflectsRecordedFills(t *testing.T) {
	svc, _ := newRealService(t, nil)

	if got := svc.ListTrades(context.Background(), "ATOM/USDC"); len(got.Fills) != 0 {
		t.Fatalf("fresh trades = %d, want 0", len(got.Fills))
	}

	svc.RecordFills([]types.Fill{
		{TradeID: "0xt1", Market: "ATOM/USDC", Price: "100", Qty: "5", Buyer: "cosmos1alice", Seller: "cosmos1bob"},
		{TradeID: "0xt2", Market: "OSMO/USDC", Price: "1.24", Qty: "10", Buyer: "cosmos1x", Seller: "cosmos1y"},
	})

	atom := svc.ListTrades(context.Background(), "ATOM/USDC")
	if len(atom.Fills) != 1 || atom.Fills[0].TradeID != "0xt1" {
		t.Fatalf("ATOM/USDC fills = %+v, want [0xt1]", atom.Fills)
	}
	osmo := svc.ListTrades(context.Background(), "OSMO/USDC")
	if len(osmo.Fills) != 1 || osmo.Fills[0].TradeID != "0xt2" {
		t.Fatalf("OSMO/USDC fills = %+v, want [0xt2]", osmo.Fills)
	}
	if none := svc.ListTrades(context.Background(), "NOPE/USDC"); len(none.Fills) != 0 {
		t.Fatalf("unknown market fills = %d, want 0", len(none.Fills))
	}
}

// A returned trades slice is a copy — mutating it must not corrupt the store.
func TestListTradesReturnsCopy(t *testing.T) {
	store := service.NewInMemoryTradeStore()
	store.Record([]types.Fill{{TradeID: "0xa", Market: "ATOM/USDC"}})
	got := store.ByMarket("ATOM/USDC")
	got[0].TradeID = "0xtampered"
	if again := store.ByMarket("ATOM/USDC"); again[0].TradeID != "0xa" {
		t.Fatalf("store mutated through returned slice: %q", again[0].TradeID)
	}
}
