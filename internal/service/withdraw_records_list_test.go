package service_test

import (
	"context"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// GET /api/withdraws history: ListWithdrawRecords returns settled records from
// the store (memory mode), preserving each record's claimed status — the
// withdraw analog of DepositService.ListDeposits.
func TestListWithdrawRecords(t *testing.T) {
	st := store.NewMemoryStore()
	repo := repository.NewWithdrawRepository(st)
	svc := service.NewWithdrawService(repo, relayer.NewLocalClient())

	// Empty history is a non-nil empty slice (stable JSON []).
	if got := svc.ListWithdrawRecords(context.Background()); len(got.WithdrawRecords) != 0 {
		t.Fatalf("empty history = %d, want 0", len(got.WithdrawRecords))
	}

	repo.SaveWithdrawRecord(context.Background(), types.WithdrawRecord{
		WithdrawID: "wd-1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "40",
		Destination: "cosmos1alice", Nullifier: "0xnull1", Claimed: false,
	})
	repo.SaveWithdrawRecord(context.Background(), types.WithdrawRecord{
		WithdrawID: "wd-2", Owner: "cosmos1bob", Denom: "uatom", Amount: "5",
		Destination: "cosmos1bob", Nullifier: "0xnull2", Claimed: true,
	})

	got := svc.ListWithdrawRecords(context.Background())
	if len(got.WithdrawRecords) != 2 {
		t.Fatalf("history = %d, want 2", len(got.WithdrawRecords))
	}
	byID := map[string]types.WithdrawRecord{}
	for _, r := range got.WithdrawRecords {
		byID[r.WithdrawID] = r
	}
	if byID["wd-1"].Amount != "40" || byID["wd-1"].Claimed {
		t.Fatalf("wd-1 = %+v, want amount 40 unclaimed", byID["wd-1"])
	}
	if !byID["wd-2"].Claimed || byID["wd-2"].Denom != "uatom" {
		t.Fatalf("wd-2 = %+v, want claimed uatom", byID["wd-2"])
	}
}
