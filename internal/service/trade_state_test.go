package service_test

import (
	"context"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// After placing an order, TradeState(owner) shows the reserved collateral and the
// open order; marketStatus comes from the registry (INT-T07 DoD, pre-match).
func TestTradeStateAfterOrder(t *testing.T) {
	svc, _ := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "5000"},
	})
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("order: %v", err)
	}

	ts := svc.TradeState(context.Background(), "cosmos1alice")

	// reservedBalances: alice uusdc available 2980 / reserved 2020.
	var found bool
	for _, rb := range ts.ReservedBalances {
		if rb.Owner == "cosmos1alice" && rb.Denom == "uusdc" {
			found = true
			if rb.Available != "2980" || rb.Reserved != "2020" {
				t.Fatalf("alice uusdc avail/reserved = %q/%q, want 2980/2020", rb.Available, rb.Reserved)
			}
		}
	}
	if !found {
		t.Fatalf("alice uusdc not in reservedBalances: %+v", ts.ReservedBalances)
	}

	if len(ts.OpenOrders) != 1 || ts.OpenOrders[0].Market != "ATOM/USDC" {
		t.Fatalf("openOrders = %+v, want one ATOM/USDC order", ts.OpenOrders)
	}
	if ts.MarketStatus["ATOM/USDC"] != types.MarketActive {
		t.Fatalf("marketStatus[ATOM/USDC] = %q, want active", ts.MarketStatus["ATOM/USDC"])
	}
	if len(ts.LatestTrades) != 0 {
		t.Fatalf("latestTrades = %d, want 0 (no match yet)", len(ts.LatestTrades))
	}
}

// After a crossing match, latestTrades reflects the fill (INT-T07 DoD).
func TestTradeStateLatestTradesAfterMatch(t *testing.T) {
	svc := matchingService(t) // funds alice+bob
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("alice: %v", err)
	}
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, bobSell("100", "20", "1"))); err != nil {
		t.Fatalf("bob: %v", err)
	}

	ts := svc.TradeState(context.Background(), "")
	if len(ts.LatestTrades) != 1 {
		t.Fatalf("latestTrades = %d, want 1", len(ts.LatestTrades))
	}
	if ts.LatestTrades[0].Price != "100" || ts.LatestTrades[0].Qty != "20" {
		t.Fatalf("latest trade = %+v, want 100/20", ts.LatestTrades[0])
	}
	// No owner → openOrders empty; reservedBalances lists only locked accounts.
	if len(ts.OpenOrders) != 0 {
		t.Fatalf("openOrders (no owner) = %d, want 0", len(ts.OpenOrders))
	}
}

// Without an owner filter, reservedBalances lists only accounts with locked
// collateral; a fully-settled/empty state lists none.
func TestTradeStateReservedFilter(t *testing.T) {
	svc, _ := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "5000"},
	})
	// No orders yet → no locked reserved.
	if ts := svc.TradeState(context.Background(), ""); len(ts.ReservedBalances) != 0 {
		t.Fatalf("reservedBalances (no orders) = %d, want 0", len(ts.ReservedBalances))
	}
	// With an owner filter, the funded account shows even with 0 reserved.
	ts := svc.TradeState(context.Background(), "cosmos1alice")
	if len(ts.ReservedBalances) != 1 || ts.ReservedBalances[0].Reserved != "0" {
		t.Fatalf("owner-filtered reservedBalances = %+v, want alice reserved 0", ts.ReservedBalances)
	}
}
