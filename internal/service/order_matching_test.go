package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// matchingService funds alice (quote) and bob (base) so a buy and a sell can
// cross, with the clock pinned.
func matchingService(t *testing.T) *service.RealOrderService {
	t.Helper()
	svc, _ := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "5000"},
		{DepositID: "d2", Owner: "cosmos1bob", Denom: "uatom", Amount: "50"},
	})
	return svc
}

func bobSell(price, qty, nonce string) types.SignedOrder {
	return types.SignedOrder{
		Owner: "cosmos1bob", Market: "ATOM/USDC", Side: types.SideSell,
		Price: price, Qty: qty, Expiry: "2000000", Nonce: nonce,
	}
}

// Self-Trade Prevention over the service: Alice places a BUY then a crossing
// SELL. The SELL would fill against her OWN resting bid → STP (cancel-newest)
// rejects it with reason self_trade, releases its uatom collateral, and records
// no trade. Her original bid stays resting.
func TestCreateOrderRejectsSelfTrade(t *testing.T) {
	ctx := context.Background()
	svc, mgr := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "5000"},
		{DepositID: "d2", Owner: "cosmos1alice", Denom: "uatom", Amount: "50"},
	})

	// Alice buys 20 @ 100 → rests (reserves uusdc).
	if _, err := svc.CreateOrder(ctx, signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("alice buy: %v", err)
	}

	// Alice sells 20 @ 100 → would cross her own bid → STP self_trade rejection.
	aliceSell := types.SignedOrder{
		Owner: "cosmos1alice", Market: "ATOM/USDC", Side: types.SideSell,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "2",
	}
	_, err := svc.CreateOrder(ctx, signedOrder(t, aliceSell))
	var rej *service.OrderRejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("want *OrderRejectedError, got %v", err)
	}
	if rej.Reason != service.ReasonSelfTrade {
		t.Fatalf("reject reason = %q, want %q", rej.Reason, service.ReasonSelfTrade)
	}

	// The self-crossing sell did NOT rest — only the original bid remains open.
	open := svc.ListOpenOrders(ctx, "cosmos1alice").OpenOrders
	if len(open) != 1 {
		t.Fatalf("only the resting bid should remain, got %d open orders", len(open))
	}
	if open[0].Side != types.SideBuy {
		t.Fatalf("remaining open order must be the buy, got side %q", open[0].Side)
	}

	// The sell's uatom collateral was released by book.Cancel: none locked, full available.
	uatom := mgr.Account("cosmos1alice", "uatom")
	if uatom.Reserved != "" && uatom.Reserved != "0" {
		t.Fatalf("uatom reserved after STP = %q, want none (released)", uatom.Reserved)
	}
	if uatom.Balance != "50" {
		t.Fatalf("uatom available after STP = %q, want 50 (untouched)", uatom.Balance)
	}

	// No self-trade recorded, nothing queued for settlement.
	if n := len(svc.ListTrades(ctx, "ATOM/USDC").Fills); n != 0 {
		t.Fatalf("self-trade must not produce a trade, got %d", n)
	}
	if n := svc.PendingFillCount(); n != 0 {
		t.Fatalf("no fill should be queued, got %d", n)
	}
}

// DoD: two crossing orders produce a fill at the maker price and min qty; the
// fill appears in GET /api/trades and is loaded into the settlement queue.
func TestMatchOnInsertProducesFill(t *testing.T) {
	svc := matchingService(t)

	// Alice buys 20 @ 100 first (rests → maker).
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("alice: %v", err)
	}
	// Bob sells 20 @ 100 (crosses → taker). Match fires on insert.
	resp, err := svc.CreateOrder(context.Background(), signedOrder(t, bobSell("100", "20", "1")))
	if err != nil {
		t.Fatalf("bob: %v", err)
	}
	// Bob's order fully filled on entry.
	if resp.Status != types.OrderStatusFilled {
		t.Fatalf("bob status = %q, want filled", resp.Status)
	}
	if resp.State.Remaining != "0" || resp.State.Filled != "20" {
		t.Fatalf("bob remaining/filled = %q/%q, want 0/20", resp.State.Remaining, resp.State.Filled)
	}

	// Fill visible in GET /api/trades with maker price (100) and min qty (20).
	trades := svc.ListTrades(context.Background(), "ATOM/USDC")
	if len(trades.Fills) != 1 {
		t.Fatalf("trades = %d, want 1", len(trades.Fills))
	}
	f := trades.Fills[0]
	if f.Price != "100" || f.Qty != "20" {
		t.Fatalf("fill price/qty = %q/%q, want 100/20", f.Price, f.Qty)
	}
	if f.Buyer != "cosmos1alice" || f.Seller != "cosmos1bob" {
		t.Fatalf("fill buyer/seller = %q/%q", f.Buyer, f.Seller)
	}
	// makerFee = floor(2000 * 50/10000)=10 ; takerFee = floor(2000*100/10000)=20.
	if f.MakerFee != "10" || f.TakerFee != "20" {
		t.Fatalf("fill maker/taker fee = %q/%q, want 10/20", f.MakerFee, f.TakerFee)
	}

	// Loaded into the settlement queue exactly once.
	if n := svc.PendingFillCount(); n != 1 {
		t.Fatalf("pending fills = %d, want 1", n)
	}
	// Both orders left the book (fully filled).
	if len(svc.ListOpenOrders(context.Background(), "cosmos1alice").OpenOrders) != 0 {
		t.Fatal("alice order should be gone (filled)")
	}
	if len(svc.ListOpenOrders(context.Background(), "cosmos1bob").OpenOrders) != 0 {
		t.Fatal("bob order should be gone (filled)")
	}
}

// A partial fill leaves the larger order resting with reduced remaining and
// status partial.
func TestMatchPartialFill(t *testing.T) {
	svc := matchingService(t)

	// Alice buys 20 @ 100 (maker).
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("alice: %v", err)
	}
	// Bob sells only 5 @ 100 → fills 5, alice has 15 left.
	resp, err := svc.CreateOrder(context.Background(), signedOrder(t, bobSell("100", "5", "1")))
	if err != nil {
		t.Fatalf("bob: %v", err)
	}
	if resp.Status != types.OrderStatusFilled { // bob's 5 fully filled
		t.Fatalf("bob status = %q, want filled", resp.Status)
	}

	// Alice partially filled: 15 remaining, status partial.
	open := svc.ListOpenOrders(context.Background(), "cosmos1alice").OpenOrders
	if len(open) != 1 {
		t.Fatalf("alice open orders = %d, want 1", len(open))
	}
	if open[0].Remaining != "15" || open[0].Filled != "5" || open[0].Status != types.OrderStatusPartial {
		t.Fatalf("alice remaining/filled/status = %q/%q/%q, want 15/5/partial",
			open[0].Remaining, open[0].Filled, open[0].Status)
	}
}

// Non-crossing orders produce no fill and both rest.
func TestMatchNonCrossing(t *testing.T) {
	svc := matchingService(t)
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("alice: %v", err)
	}
	// Bob asks 101 > bid 100 → no cross.
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, bobSell("101", "20", "1"))); err != nil {
		t.Fatalf("bob: %v", err)
	}
	if n := len(svc.ListTrades(context.Background(), "ATOM/USDC").Fills); n != 0 {
		t.Fatalf("fills = %d, want 0 (non-crossing)", n)
	}
	if n := svc.PendingFillCount(); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
}

// DrainFills hands the batch pipeline the queued fills once (no double-drain).
func TestDrainFillsOnce(t *testing.T) {
	svc := matchingService(t)
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("alice: %v", err)
	}
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, bobSell("100", "20", "1"))); err != nil {
		t.Fatalf("bob: %v", err)
	}
	drained := svc.DrainFills()
	if len(drained) != 1 {
		t.Fatalf("drained = %d, want 1", len(drained))
	}
	if again := svc.DrainFills(); len(again) != 0 {
		t.Fatalf("second drain = %d, want 0 (no double-processing)", len(again))
	}
	// History (GET /api/trades) is NOT drained — it persists.
	if n := len(svc.ListTrades(context.Background(), "ATOM/USDC").Fills); n != 1 {
		t.Fatalf("history = %d, want 1 (persists after drain)", n)
	}
}

// The interval sequencer's RunMatchingOnce matches a crossing left in the book.
// (Insert-time match already handles the common case; this proves the backstop.)
func TestRunMatchingOnceBackstop(t *testing.T) {
	svc := matchingService(t)
	// Two crossing orders inserted — insert-time match already fills them, so the
	// tick should find nothing new (idempotent, no double fill).
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("alice: %v", err)
	}
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, bobSell("100", "20", "1"))); err != nil {
		t.Fatalf("bob: %v", err)
	}
	n, err := svc.RunMatchingOnce()
	if err != nil {
		t.Fatalf("RunMatchingOnce: %v", err)
	}
	if n != 0 {
		t.Fatalf("tick produced %d fills, want 0 (already matched on insert)", n)
	}
	if total := len(svc.ListTrades(context.Background(), "ATOM/USDC").Fills); total != 1 {
		t.Fatalf("history = %d, want 1 (no duplicate fill)", total)
	}
}
