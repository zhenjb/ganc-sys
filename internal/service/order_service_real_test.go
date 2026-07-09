package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// fixedNow pins the validation reference time so expiry checks are deterministic.
const fixedNow int64 = 1_000_000

// newRealService builds a RealOrderService over a fresh manager funded via
// deposits, with the clock pinned to fixedNow.
func newRealService(t *testing.T, deposits []types.DepositRecord) (*service.RealOrderService, *state.OffchainStateManager) {
	t.Helper()
	mgr := state.NewOffchainStateManager()
	for _, d := range deposits {
		if _, err := mgr.ApplyDeposit(d); err != nil {
			t.Fatalf("deposit %s: %v", d.DepositID, err)
		}
	}
	svc, err := service.NewRealOrderService(mgr, service.DefaultMarkets(), func() int64 { return fixedNow })
	if err != nil {
		t.Fatalf("new real service: %v", err)
	}
	return svc, mgr
}

// signedOrder builds an order and signs it with the MVP mock signature so it
// passes STATE-T03 signature verification.
func signedOrder(t *testing.T, o types.SignedOrder) types.SignedOrder {
	t.Helper()
	sig, err := state.MockOrderSignature(o)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	o.Signature = sig
	return o
}

func aliceBuy() types.SignedOrder {
	return types.SignedOrder{
		Owner: "cosmos1alice", Market: "ATOM/USDC", Side: types.SideBuy,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1",
	}
}

// A funded, well-signed buy passes validate → reserve → insert, rests as "open",
// moves collateral available→reserved, and shows up in the orderbook depth.
func TestRealCreateOrderHappyPath(t *testing.T) {
	svc, mgr := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
	})
	order := signedOrder(t, aliceBuy())

	resp, err := svc.CreateOrder(context.Background(), order)
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if resp.Status != types.OrderStatusOpen {
		t.Fatalf("status = %q, want open", resp.Status)
	}
	if resp.State.Remaining != "20" || resp.State.Filled != "0" {
		t.Fatalf("remaining/filled = %q/%q, want 20/0", resp.State.Remaining, resp.State.Filled)
	}

	// Collateral: notional 100*20=2000 + taker buffer ceil(2000*100/10000)=20 => 2020.
	acc := mgr.Account("cosmos1alice", "uusdc")
	if acc.Balance != "980" { // 3000 - 2020
		t.Fatalf("available = %q, want 980", acc.Balance)
	}
	if acc.Reserved != "2020" {
		t.Fatalf("reserved = %q, want 2020", acc.Reserved)
	}

	// The order is visible in the book depth as a bid at its price.
	book, err := svc.GetOrderbook(context.Background(), "ATOM/USDC")
	if err != nil {
		t.Fatalf("GetOrderbook: %v", err)
	}
	if len(book.Bids) != 1 || book.Bids[0].Price != "100" || book.Bids[0].Qty != "20" {
		t.Fatalf("bids = %+v, want one level 100/20", book.Bids)
	}
	if book.BestBid != "100" || book.BestAsk != "" {
		t.Fatalf("bestBid/bestAsk = %q/%q, want 100/''", book.BestBid, book.BestAsk)
	}
}

// Insufficient available balance is rejected as insufficient_balance, and NO
// collateral is left reserved (atomicity: reject before/at reserve).
func TestRealCreateOrderInsufficientBalance(t *testing.T) {
	svc, mgr := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "100"}, // needs 2020
	})
	order := signedOrder(t, aliceBuy())

	_, err := svc.CreateOrder(context.Background(), order)
	var rej *service.OrderRejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want *OrderRejectedError", err)
	}
	if rej.Reason != service.ReasonInsufficientBalance {
		t.Fatalf("reason = %q, want %q", rej.Reason, service.ReasonInsufficientBalance)
	}
	if acc := mgr.Account("cosmos1alice", "uusdc"); acc.Reserved != "" {
		t.Fatalf("reserved = %q, want empty (no orphan reservation)", acc.Reserved)
	}
}

// A tampered order (price changed after signing) fails signature verification.
func TestRealCreateOrderForgedSignature(t *testing.T) {
	svc, _ := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
	})
	order := signedOrder(t, aliceBuy())
	order.Price = "99.9" // tamper AFTER signing -> canonical bytes no longer match sig

	_, err := svc.CreateOrder(context.Background(), order)
	var rej *service.OrderRejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want *OrderRejectedError", err)
	}
	if rej.Reason != string(state.ReasonBadSignature) {
		t.Fatalf("reason = %q, want %q", rej.Reason, state.ReasonBadSignature)
	}
}

// Tick/lot violations pass through their STATE-T03 reason code verbatim.
func TestRealCreateOrderTickViolation(t *testing.T) {
	svc, _ := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
	})
	o := aliceBuy()
	o.Price = "100.05" // tickSize 0.1 -> not a multiple
	order := signedOrder(t, o)

	_, err := svc.CreateOrder(context.Background(), order)
	var rej *service.OrderRejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want *OrderRejectedError", err)
	}
	if rej.Reason != string(state.ReasonTickViolation) {
		t.Fatalf("reason = %q, want tick_violation", rej.Reason)
	}
}

// Re-submitting the identical resting order is rejected as a duplicate.
func TestRealCreateOrderDuplicate(t *testing.T) {
	svc, _ := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "6000"},
	})
	order := signedOrder(t, aliceBuy())

	if _, err := svc.CreateOrder(context.Background(), order); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err := svc.CreateOrder(context.Background(), order)
	var rej *service.OrderRejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want *OrderRejectedError", err)
	}
	if rej.Reason != service.ReasonDuplicateOrder {
		t.Fatalf("reason = %q, want duplicate_order", rej.Reason)
	}
}

// A sell locks base denom (qty), and both sides aggregate into the depth.
func TestRealOrderbookBothSides(t *testing.T) {
	svc, _ := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
		{DepositID: "d2", Owner: "cosmos1bob", Denom: "uatom", Amount: "50"},
	})
	// alice buys 20@100 (bid), bob sells 20@101 (ask) — non-crossing, both rest.
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("alice: %v", err)
	}
	bob := signedOrder(t, types.SignedOrder{
		Owner: "cosmos1bob", Market: "ATOM/USDC", Side: types.SideSell,
		Price: "101", Qty: "20", Expiry: "2000000", Nonce: "1",
	})
	if _, err := svc.CreateOrder(context.Background(), bob); err != nil {
		t.Fatalf("bob: %v", err)
	}

	book, err := svc.GetOrderbook(context.Background(), "ATOM/USDC")
	if err != nil {
		t.Fatalf("GetOrderbook: %v", err)
	}
	if book.BestBid != "100" || book.BestAsk != "101" {
		t.Fatalf("bestBid/bestAsk = %q/%q, want 100/101", book.BestBid, book.BestAsk)
	}
}

func TestRealGetOrderbookUnknownMarket(t *testing.T) {
	svc, _ := newRealService(t, nil)
	_, err := svc.GetOrderbook(context.Background(), "NOPE/USDC")
	if !errors.Is(err, service.ErrMarketNotFound) {
		t.Fatalf("err = %v, want ErrMarketNotFound", err)
	}
}

func TestRealListMarkets(t *testing.T) {
	svc, _ := newRealService(t, nil)
	resp := svc.ListMarkets(context.Background())
	if len(resp.Markets) != len(service.DefaultMarkets()) {
		t.Fatalf("markets = %d, want %d", len(resp.Markets), len(service.DefaultMarkets()))
	}
}
