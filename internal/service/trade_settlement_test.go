package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// settleSetup funds alice+bob, crosses a 20@100 buy/sell (match on insert),
// leaving exactly one fill queued and ready to settle.
func settleSetup(t *testing.T) (*service.RealOrderService, *state.OffchainStateManager) {
	t.Helper()
	svc, mgr := newRealService(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "5000"},
		{DepositID: "d2", Owner: "cosmos1bob", Denom: "uatom", Amount: "50"},
	})
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, aliceBuy())); err != nil {
		t.Fatalf("alice: %v", err)
	}
	if _, err := svc.CreateOrder(context.Background(), signedOrder(t, bobSell("100", "20", "1"))); err != nil {
		t.Fatalf("bob: %v", err)
	}
	if svc.PendingFillCount() != 1 {
		t.Fatalf("expected 1 queued fill, got %d", svc.PendingFillCount())
	}
	return svc, mgr
}

// DoD: a batch with trades[] builds → proves(stub) → submits, the new root is
// committed, and balances transition with exact value conservation.
func TestSettleTradesHappyPath(t *testing.T) {
	svc, mgr := settleSetup(t)
	oldRoot := mgr.Root()

	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil {
		t.Fatalf("SettleTradesOnce: %v", err)
	}
	if !settled {
		t.Fatal("expected a batch to settle")
	}

	// Root advanced (state committed).
	if mgr.Root() == oldRoot {
		t.Fatal("root did not advance after settle")
	}
	// Queue drained.
	if svc.PendingFillCount() != 0 {
		t.Fatalf("queue = %d after settle, want 0", svc.PendingFillCount())
	}

	// Balance transitions (see doc for the arithmetic):
	//   alice: paid 2000 notional + 10 maker fee -> uusdc 2990, received 20 uatom.
	//   bob:   delivered 20 uatom -> 30 left, received 2000 - 20 taker fee = 1980 uusdc.
	//   fee account: 10 + 20 = 30 uusdc.
	assertAcct(t, mgr, "cosmos1alice", "uusdc", "2990", "")
	assertAcct(t, mgr, "cosmos1alice", "uatom", "20", "")
	assertAcct(t, mgr, "cosmos1bob", "uusdc", "1980", "")
	assertAcct(t, mgr, "cosmos1bob", "uatom", "30", "")
	assertAcct(t, mgr, state.FeeAccountOwner, "uusdc", "30", "")

	// Conservation: uusdc total 5000, uatom total 50, unchanged.
	quote := amt(t, mgr, "cosmos1alice", "uusdc") + amt(t, mgr, "cosmos1bob", "uusdc") + amt(t, mgr, state.FeeAccountOwner, "uusdc")
	if quote != 5000 {
		t.Fatalf("uusdc conservation broken: total %d, want 5000", quote)
	}
	base := amt(t, mgr, "cosmos1alice", "uatom") + amt(t, mgr, "cosmos1bob", "uatom")
	if base != 50 {
		t.Fatalf("uatom conservation broken: total %d, want 50", base)
	}
}

// Idle: nothing queued → (false, nil), no state change.
func TestSettleTradesIdle(t *testing.T) {
	svc, _ := newRealService(t, nil)
	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil || settled {
		t.Fatalf("idle settle = (%v, %v), want (false, nil)", settled, err)
	}
}

type failingSubmitter struct{}

func (failingSubmitter) SubmitTrade(_ context.Context, _ types.SettlementUpdate, _ types.BatchCommitments, _ types.ProofBundle) (string, bool, error) {
	return "", false, errors.New("relayer down")
}

// On a submit failure the state rolls back to its pre-apply snapshot and the
// fills are re-enqueued for retry (self-healing) — no double-spend, no fee left
// credited.
func TestSettleTradesRollbackOnSubmitFailure(t *testing.T) {
	svc, mgr := settleSetup(t)
	svc.SetTradeSettlement(nil, failingSubmitter{})

	// Pre-apply (post-match) balances: reserved still locked, nothing applied.
	assertAcct(t, mgr, "cosmos1alice", "uusdc", "2980", "2020")
	assertAcct(t, mgr, "cosmos1bob", "uatom", "30", "20")

	settled, err := svc.SettleTradesOnce(context.Background())
	if err == nil {
		t.Fatal("expected settle error")
	}
	if settled {
		t.Fatal("no batch should have settled")
	}

	// State restored exactly (rollback), fee account NOT credited.
	assertAcct(t, mgr, "cosmos1alice", "uusdc", "2980", "2020")
	assertAcct(t, mgr, "cosmos1alice", "uatom", "0", "")
	assertAcct(t, mgr, "cosmos1bob", "uusdc", "0", "")
	assertAcct(t, mgr, "cosmos1bob", "uatom", "30", "20")
	assertAcct(t, mgr, state.FeeAccountOwner, "uusdc", "0", "")

	// Fills re-enqueued for the next tick.
	if svc.PendingFillCount() != 1 {
		t.Fatalf("fills not re-enqueued: pending %d, want 1", svc.PendingFillCount())
	}
}

type notAcceptedSubmitter struct{}

func (notAcceptedSubmitter) SubmitTrade(_ context.Context, _ types.SettlementUpdate, _ types.BatchCommitments, _ types.ProofBundle) (string, bool, error) {
	return "0xtx", false, nil // chain rejected the batch
}

// A chain rejection (accepted=false) also rolls back and re-enqueues.
func TestSettleTradesRollbackOnReject(t *testing.T) {
	svc, mgr := settleSetup(t)
	svc.SetTradeSettlement(nil, notAcceptedSubmitter{})

	settled, err := svc.SettleTradesOnce(context.Background())
	if err == nil || settled {
		t.Fatalf("reject settle = (%v, %v), want (false, err)", settled, err)
	}
	assertAcct(t, mgr, "cosmos1alice", "uusdc", "2980", "2020")
	if svc.PendingFillCount() != 1 {
		t.Fatalf("fills not re-enqueued after reject: %d", svc.PendingFillCount())
	}
}

// After a rollback, retrying with a working submitter settles cleanly (the same
// deterministic batch) — proving self-healing retry.
func TestSettleTradesRetryAfterRollback(t *testing.T) {
	svc, mgr := settleSetup(t)
	svc.SetTradeSettlement(nil, failingSubmitter{})
	if _, err := svc.SettleTradesOnce(context.Background()); err == nil {
		t.Fatal("expected first settle to fail")
	}
	// Restore a working submitter and retry.
	svc.SetTradeSettlement(nil, service.NewLocalTradeSubmitter())
	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil || !settled {
		t.Fatalf("retry settle = (%v, %v), want (true, nil)", settled, err)
	}
	assertAcct(t, mgr, "cosmos1alice", "uusdc", "2990", "")
	assertAcct(t, mgr, state.FeeAccountOwner, "uusdc", "30", "")
}

// --- helpers ---

func assertAcct(t *testing.T, mgr *state.OffchainStateManager, owner, denom, wantAvail, wantReserved string) {
	t.Helper()
	acc := mgr.Account(owner, denom)
	if acc.Balance != wantAvail {
		t.Fatalf("%s/%s available = %q, want %q", owner, denom, acc.Balance, wantAvail)
	}
	if acc.Reserved != wantReserved {
		t.Fatalf("%s/%s reserved = %q, want %q", owner, denom, acc.Reserved, wantReserved)
	}
}

func amt(t *testing.T, mgr *state.OffchainStateManager, owner, denom string) int {
	t.Helper()
	acc := mgr.Account(owner, denom)
	n := 0
	for _, c := range acc.Balance {
		n = n*10 + int(c-'0')
	}
	return n
}
