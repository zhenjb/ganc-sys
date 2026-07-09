package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// createAliceBuy funds alice, submits her signed buy, and returns the resulting
// response (orderHash in State) plus the service+manager for further assertions.
func createAliceBuy(t *testing.T) (*service.RealOrderService, *state.OffchainStateManager, types.OrderResponse) {
	t.Helper()
	svc, mgr := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
	})
	resp, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy()))
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	return svc, mgr, resp
}

// Cancel by orderHash: order leaves the book and the full remaining collateral
// (2020 uusdc) returns available→ so reserved goes back to zero.
func TestCancelReleasesReservedAndRemovesFromBook(t *testing.T) {
	svc, mgr, resp := createAliceBuy(t)

	// Pre-cancel: reserved locked.
	if acc := mgr.Account("cosmos1alice", "uusdc"); acc.Reserved != "2020" || acc.Balance != "980" {
		t.Fatalf("pre-cancel balance/reserved = %q/%q, want 980/2020", acc.Balance, acc.Reserved)
	}

	out, err := svc.CancelOrder(context.Background(), resp.State.OrderHash, "cosmos1alice")
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if out.Status != types.OrderStatusCancelled {
		t.Fatalf("status = %q, want cancelled", out.Status)
	}
	if out.State.Remaining != "0" || out.State.Filled != "0" {
		t.Fatalf("remaining/filled = %q/%q, want 0/0", out.State.Remaining, out.State.Filled)
	}

	// Post-cancel: reserved fully released, available restored.
	if acc := mgr.Account("cosmos1alice", "uusdc"); acc.Reserved != "" || acc.Balance != "3000" {
		t.Fatalf("post-cancel balance/reserved = %q/%q, want 3000/empty", acc.Balance, acc.Reserved)
	}

	// Gone from the book depth (DoD).
	book, _ := svc.GetOrderbook(context.Background(), "ATOM/USDC")
	if len(book.Bids) != 0 || book.BestBid != "" {
		t.Fatalf("book still has the order: %+v", book)
	}
}

// A non-owner cannot cancel; the order stays put and reserved is untouched.
func TestCancelForbiddenForNonOwner(t *testing.T) {
	svc, mgr, resp := createAliceBuy(t)

	_, err := svc.CancelOrder(context.Background(), resp.State.OrderHash, "cosmos1mallory")
	if !errors.Is(err, service.ErrOrderForbidden) {
		t.Fatalf("err = %v, want ErrOrderForbidden", err)
	}
	// Untouched.
	if acc := mgr.Account("cosmos1alice", "uusdc"); acc.Reserved != "2020" {
		t.Fatalf("reserved = %q, want 2020 (unchanged)", acc.Reserved)
	}
	book, _ := svc.GetOrderbook(context.Background(), "ATOM/USDC")
	if len(book.Bids) != 1 {
		t.Fatalf("order should still rest: %+v", book)
	}
}

// Double-cancel: the second cancel is a 404 (already gone), no double release.
func TestCancelDoubleCancelNotFound(t *testing.T) {
	svc, mgr, resp := createAliceBuy(t)

	if _, err := svc.CancelOrder(context.Background(), resp.State.OrderHash, "cosmos1alice"); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	_, err := svc.CancelOrder(context.Background(), resp.State.OrderHash, "cosmos1alice")
	if !errors.Is(err, service.ErrOrderNotFound) {
		t.Fatalf("second cancel err = %v, want ErrOrderNotFound", err)
	}
	// Still exactly one release worth of funds (no double credit).
	if acc := mgr.Account("cosmos1alice", "uusdc"); acc.Balance != "3000" || acc.Reserved != "" {
		t.Fatalf("balance/reserved = %q/%q, want 3000/empty", acc.Balance, acc.Reserved)
	}
}

// A cancelled order's nullifier is consumed → the identical order cannot be
// re-submitted (replay protection, STATE-T03/T04).
func TestCancelBlocksReplay(t *testing.T) {
	svc, _, resp := createAliceBuy(t)
	if _, err := svc.CancelOrder(context.Background(), resp.State.OrderHash, "cosmos1alice"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	_, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy()))
	var rej *service.OrderRejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("re-submit err = %v, want *OrderRejectedError", err)
	}
	if rej.Reason != string(state.ReasonNullifierUsed) {
		t.Fatalf("reason = %q, want order_nullifier_used", rej.Reason)
	}
}

// Cancel also resolves the short "ord-…" display id, not just the raw hash.
func TestCancelByShortID(t *testing.T) {
	svc, _, resp := createAliceBuy(t)
	if _, err := svc.CancelOrder(context.Background(), resp.State.OrderID, "cosmos1alice"); err != nil {
		t.Fatalf("cancel by short id %q: %v", resp.State.OrderID, err)
	}
	book, _ := svc.GetOrderbook(context.Background(), "ATOM/USDC")
	if len(book.Bids) != 0 {
		t.Fatalf("order not cancelled via short id: %+v", book)
	}
}

func TestCancelUnknownID(t *testing.T) {
	svc, _ := newRealService(t, nil)
	_, err := svc.CancelOrder(context.Background(), "0xdeadbeef", "cosmos1alice")
	if !errors.Is(err, service.ErrOrderNotFound) {
		t.Fatalf("err = %v, want ErrOrderNotFound", err)
	}
}

func TestCancelMissingOwner(t *testing.T) {
	svc, _, resp := createAliceBuy(t)
	_, err := svc.CancelOrder(context.Background(), resp.State.OrderHash, "")
	var rej *service.OrderRejectedError
	if !errors.As(err, &rej) || rej.Reason != service.ReasonOwnerRequired {
		t.Fatalf("err = %v, want owner_required rejection", err)
	}
}
