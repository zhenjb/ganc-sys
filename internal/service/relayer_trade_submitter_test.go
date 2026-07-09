package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// INT-T08 DoD: a trade batch settles through the relayer submit path (local
// relayer) — signed/submitted and accepted, committing the new root.
func TestSettleViaRelayerSubmitter(t *testing.T) {
	svc, mgr := settleSetup(t) // alice+bob crossed, one fill queued
	svc.SetTradeSettlement(nil, service.NewRelayerTradeSubmitter(relayer.NewLocalClient()))

	oldRoot := mgr.Root()
	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil {
		t.Fatalf("SettleTradesOnce via relayer: %v", err)
	}
	if !settled {
		t.Fatal("expected settle via relayer")
	}
	if mgr.Root() == oldRoot {
		t.Fatal("root did not advance")
	}
	// Balances transitioned (same conservation as the direct-stub path).
	assertAcct(t, mgr, "cosmos1alice", "uusdc", "2990", "")
	assertAcct(t, mgr, state.FeeAccountOwner, "uusdc", "30", "")
	if svc.PendingFillCount() != 0 {
		t.Fatalf("queue = %d after settle, want 0", svc.PendingFillCount())
	}
}

// rejectingTradeClient models a chain rejection (proof invalid / root mismatch).
type rejectingTradeClient struct{}

func (rejectingTradeClient) SubmitTradeBatch(_ context.Context, _ relayer.SubmitBatchInput) (relayer.SubmitBatchResult, error) {
	return relayer.SubmitBatchResult{Accepted: false, ProofStatus: "rejected"}, errors.New("submit-batch-proof rejected by chain (code=18)")
}

// A chain reject through the relayer rolls the batch back and re-enqueues it
// (INT-T06 self-healing driven by INT-T08's accepted=false/error signal).
func TestSettleViaRelayerRejectRollsBack(t *testing.T) {
	svc, mgr := settleSetup(t)
	svc.SetTradeSettlement(nil, service.NewRelayerTradeSubmitter(rejectingTradeClient{}))

	settled, err := svc.SettleTradesOnce(context.Background())
	if err == nil || settled {
		t.Fatalf("reject settle = (%v, %v), want (false, err)", settled, err)
	}
	// State restored to pre-apply, fee not credited, fills re-enqueued.
	assertAcct(t, mgr, "cosmos1alice", "uusdc", "2980", "2020")
	assertAcct(t, mgr, state.FeeAccountOwner, "uusdc", "0", "")
	if svc.PendingFillCount() != 1 {
		t.Fatalf("fills not re-enqueued after reject: %d", svc.PendingFillCount())
	}
}

// The submitter maps a relayer accept into (txHash, true, nil).
func TestRelayerTradeSubmitterMapsAccept(t *testing.T) {
	sub := service.NewRelayerTradeSubmitter(relayer.NewLocalClient())
	upd := types.SettlementUpdate{
		BatchID: "batch-1", OldStateRoot: "0xA", NewStateRoot: "0xB",
	}
	com := types.BatchCommitments{
		DepositsRoot: "0xd", WithdrawalsRoot: "0xw", NullifiersRoot: "0xn",
		WithdrawOutputsRoot: "0xo", TradesRoot: "0xt", OrdersRoot: "0xr",
	}
	proof := types.ProofBundle{
		Proof:        "0xp",
		PublicInputs: []string{"0xA", "0xB", "0xd", "0xw", "0xn", "0xo", "0xt", "0xr"},
	}
	txHash, accepted, err := sub.SubmitTrade(context.Background(), upd, com, proof)
	if err != nil || !accepted || txHash == "" {
		t.Fatalf("SubmitTrade = (%q, %v, %v), want accepted", txHash, accepted, err)
	}
}
