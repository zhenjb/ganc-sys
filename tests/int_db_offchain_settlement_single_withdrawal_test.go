package tests

import (
	"context"
	"os"
	"testing"

	appbatch "github.com/zhenjb/ganc-sys/internal/batch"
	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// TestDBOffchainSettlementBoundsBatchToSingleWithdrawal is the regression test
// for the multi-withdrawal nullifier-mismatch bug.
//
// The gazk settlement circuit v1 proves exactly ONE withdrawal per batch (a
// single per-account nonce binds the nullifier). Before the fix, BuildPendingBatch
// emitted every pending withdrawal in one batch, so a batch with two withdrawals
// for the same account carried only the last nonce in its witness — the prover
// then rejected the earlier withdrawal with:
//
//	settlementUpdate.withdrawals[0].nullifier mismatch: got <nonce N>, expected <nonce N+1>
//
// This test creates two pending withdrawals for the same account and asserts each
// build produces a batch with exactly one withdrawal, in chain order, with the
// NewStateRoot pinned to that withdrawal's own rootAfter.
func TestDBOffchainSettlementBoundsBatchToSingleWithdrawal(t *testing.T) {
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("set RUN_DB_TESTS=1 to run postgres tests")
	}

	ctx := context.Background()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer pool.Close()

	cleanOffchainSettlementTables(t, ctx, pool)

	manager := appstate.NewOffchainStateManager()
	repo := repository.NewOffchainSettlementRepository(pool)
	svc := service.NewOffchainSettlementService(manager, repo)

	secret := "mock-user-secret"
	secrets := []appbatch.AccountSecret{{Owner: "cosmos1alice", UserSecret: secret}}

	if _, err := svc.ApplyIndexedDeposit(ctx, types.DepositRecord{
		DepositID: "dep-1",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "100",
	}); err != nil {
		t.Fatalf("apply deposit: %v", err)
	}

	wd1, err := svc.ApplyWithdrawRequest(ctx, types.WithdrawRequest{
		WithdrawID:  "wd-1",
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
		Nonce:       "1",
		Signature:   "0xmocksignature",
	}, secret)
	if err != nil {
		t.Fatalf("apply withdraw 1: %v", err)
	}

	wd2, err := svc.ApplyWithdrawRequest(ctx, types.WithdrawRequest{
		WithdrawID:  "wd-2",
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "10",
		Destination: "cosmos1alice",
		Nonce:       "2",
		Signature:   "0xmocksignature",
	}, secret)
	if err != nil {
		t.Fatalf("apply withdraw 2: %v", err)
	}

	// --- Build #1: must contain ONLY wd-1 (plus the deposit that chains before it).
	out1, err := svc.BuildPendingBatch(ctx, secrets)
	if err != nil {
		t.Fatalf("build #1: %v", err)
	}
	if got := len(out1.SettlementUpdate.Withdrawals); got != 1 {
		t.Fatalf("build #1: expected exactly 1 withdrawal, got %d", got)
	}
	if id := out1.SettlementUpdate.Withdrawals[0].WithdrawID; id != "wd-1" {
		t.Fatalf("build #1: expected wd-1, got %q", id)
	}
	if out1.SettlementUpdate.NewStateRoot != wd1.RootAfter {
		t.Fatalf("build #1: expected newStateRoot=%q (wd-1 rootAfter), got %q",
			wd1.RootAfter, out1.SettlementUpdate.NewStateRoot)
	}
	// The single witness account nonce must equal wd-1's nonce so the prover's
	// NullifierFor(secret, nonce) reproduces this withdrawal's nullifier.
	if len(out1.Witness.Accounts) != 1 || out1.Witness.Accounts[0].Nonce != "1" {
		t.Fatalf("build #1: expected witness account nonce=1, got %+v", out1.Witness.Accounts)
	}

	// Commit batch #1 (as submit would), advancing the committed root to wd-1's
	// rootAfter so wd-2 can chain from it.
	if err := svc.CommitBatch(ctx, out1.SettlementUpdate.BatchID, "0xtx1", out1.SettlementUpdate.NewStateRoot); err != nil {
		t.Fatalf("commit #1: %v", err)
	}

	// --- Build #2: must now contain ONLY wd-2, no deposits.
	out2, err := svc.BuildPendingBatch(ctx, secrets)
	if err != nil {
		t.Fatalf("build #2: %v", err)
	}
	if got := len(out2.SettlementUpdate.Withdrawals); got != 1 {
		t.Fatalf("build #2: expected exactly 1 withdrawal, got %d", got)
	}
	if id := out2.SettlementUpdate.Withdrawals[0].WithdrawID; id != "wd-2" {
		t.Fatalf("build #2: expected wd-2, got %q", id)
	}
	if got := len(out2.SettlementUpdate.Deposits); got != 0 {
		t.Fatalf("build #2: expected 0 deposits, got %d", got)
	}
	if out2.SettlementUpdate.OldStateRoot != wd1.RootAfter {
		t.Fatalf("build #2: expected oldStateRoot=%q (wd-1 rootAfter), got %q",
			wd1.RootAfter, out2.SettlementUpdate.OldStateRoot)
	}
	if out2.SettlementUpdate.NewStateRoot != wd2.RootAfter {
		t.Fatalf("build #2: expected newStateRoot=%q (wd-2 rootAfter), got %q",
			wd2.RootAfter, out2.SettlementUpdate.NewStateRoot)
	}
	if len(out2.Witness.Accounts) != 1 || out2.Witness.Accounts[0].Nonce != "2" {
		t.Fatalf("build #2: expected witness account nonce=2, got %+v", out2.Witness.Accounts)
	}
}
