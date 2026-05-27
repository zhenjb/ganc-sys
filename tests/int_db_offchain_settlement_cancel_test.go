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

func TestDBOffchainSettlementServiceCancelsIncludedBatchForRetry(t *testing.T) {
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

	_, err = svc.ApplyIndexedDeposit(ctx, types.DepositRecord{
		DepositID: "dep-cancel-1",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "100",
		Processed: false,
	})
	if err != nil {
		t.Fatalf("apply indexed deposit: %v", err)
	}

	_, err = svc.ApplyWithdrawRequest(ctx, types.WithdrawRequest{
		WithdrawID:  "wd-cancel-1",
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
		Nonce:       "1",
		Signature:   "0xmocksignature",
	}, "mock-user-secret")
	if err != nil {
		t.Fatalf("apply withdraw request: %v", err)
	}

	output, err := svc.BuildPendingBatch(ctx, []appbatch.AccountSecret{
		{
			Owner:      "cosmos1alice",
			UserSecret: "mock-user-secret",
		},
	})
	if err != nil {
		t.Fatalf("build pending batch: %v", err)
	}

	batchID := output.SettlementUpdate.BatchID
	if batchID == "" {
		t.Fatalf("expected batchId")
	}

	deposits, withdrawals, err := svc.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement after build: %v", err)
	}
	if len(deposits) != 0 {
		t.Fatalf("expected no pending deposits after build, got %d", len(deposits))
	}
	if len(withdrawals) != 0 {
		t.Fatalf("expected no pending withdrawals after build, got %d", len(withdrawals))
	}

	if err := svc.CancelIncludedBatch(ctx, batchID, "proof generation failed"); err != nil {
		t.Fatalf("cancel included batch: %v", err)
	}

	deposits, withdrawals, err = svc.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement after cancel: %v", err)
	}
	if len(deposits) != 1 {
		t.Fatalf("expected one pending deposit after cancel, got %d", len(deposits))
	}
	if len(withdrawals) != 1 {
		t.Fatalf("expected one pending withdrawal after cancel, got %d", len(withdrawals))
	}

	if deposits[0].DepositID != "dep-cancel-1" {
		t.Fatalf("expected dep-cancel-1, got %q", deposits[0].DepositID)
	}
	if deposits[0].Status != repository.OffchainSettlementStatusPending {
		t.Fatalf("expected deposit status=pending, got %q", deposits[0].Status)
	}
	if deposits[0].BatchID != "" {
		t.Fatalf("expected deposit batchId cleared, got %q", deposits[0].BatchID)
	}
	if deposits[0].ErrorMessage != "proof generation failed" {
		t.Fatalf("expected deposit error message, got %q", deposits[0].ErrorMessage)
	}

	if withdrawals[0].WithdrawID != "wd-cancel-1" {
		t.Fatalf("expected wd-cancel-1, got %q", withdrawals[0].WithdrawID)
	}
	if withdrawals[0].Status != repository.OffchainSettlementStatusPending {
		t.Fatalf("expected withdrawal status=pending, got %q", withdrawals[0].Status)
	}
	if withdrawals[0].BatchID != "" {
		t.Fatalf("expected withdrawal batchId cleared, got %q", withdrawals[0].BatchID)
	}
	if withdrawals[0].ErrorMessage != "proof generation failed" {
		t.Fatalf("expected withdrawal error message, got %q", withdrawals[0].ErrorMessage)
	}

	retryOutput, err := svc.BuildPendingBatch(ctx, []appbatch.AccountSecret{
		{
			Owner:      "cosmos1alice",
			UserSecret: "mock-user-secret",
		},
	})
	if err != nil {
		t.Fatalf("retry build pending batch: %v", err)
	}

	if retryOutput.SettlementUpdate.BatchID == "" {
		t.Fatalf("expected retry batchId")
	}

	if retryOutput.SettlementUpdate.OldStateRoot != output.SettlementUpdate.OldStateRoot {
		t.Fatalf(
			"expected retry oldStateRoot=%q, got %q",
			output.SettlementUpdate.OldStateRoot,
			retryOutput.SettlementUpdate.OldStateRoot,
		)
	}

	if retryOutput.SettlementUpdate.NewStateRoot != output.SettlementUpdate.NewStateRoot {
		t.Fatalf(
			"expected retry newStateRoot=%q, got %q",
			output.SettlementUpdate.NewStateRoot,
			retryOutput.SettlementUpdate.NewStateRoot,
		)
	}

	if retryOutput.Witness.Accounts[0].OldBalance != "0" {
		t.Fatalf("expected retry witness oldBalance=0, got %q", retryOutput.Witness.Accounts[0].OldBalance)
	}

	if retryOutput.Witness.Accounts[0].NewBalance != "60" {
		t.Fatalf("expected retry witness newBalance=60, got %q", retryOutput.Witness.Accounts[0].NewBalance)
	}
}
