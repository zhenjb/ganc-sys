package tests

import (
	"context"
	"os"
	"testing"

	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestDBBatchServiceBuildsFromPendingOffchainSettlement(t *testing.T) {
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

	if _, err := pool.Exec(ctx, "DELETE FROM batch_builds"); err != nil {
		t.Fatalf("clean batch_builds: %v", err)
	}

	manager := appstate.NewOffchainStateManager()
	offchainRepo := repository.NewOffchainSettlementRepository(pool)
	offchainSvc := service.NewOffchainSettlementService(manager, offchainRepo)

	_, err = offchainSvc.ApplyIndexedDeposit(ctx, types.DepositRecord{
		DepositID: "dep-pending-source-1",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "100",
		Processed: false,
	})
	if err != nil {
		t.Fatalf("apply indexed deposit: %v", err)
	}

	_, err = offchainSvc.ApplyWithdrawRequest(ctx, types.WithdrawRequest{
		WithdrawID:  "wd-pending-source-1",
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

	batchRepo := repository.NewBatchRepositoryWithDB(
		store.NewMemoryStore(),
		pool,
		repository.BatchBuildStorePostgres,
		repository.SubmitBatchStoreMemory,
	)

	batchSvc := service.NewBatchServiceWithOffchainSettlement(
		batchRepo,
		nil,
		nil,
		nil,
		relayer.NewLocalClient(),
		service.BatchBuildSourcePending,
		offchainSvc,
	)

	response, err := batchSvc.BuildBatch(ctx, types.BuildBatchRequestBody{})
	if err != nil {
		t.Fatalf("build pending batch: %v", err)
	}

	if response.State.BatchStatus != "built" {
		t.Fatalf("expected batchStatus=built, got %q", response.State.BatchStatus)
	}

	if response.SettlementUpdate.BatchID == "" {
		t.Fatalf("expected batchId")
	}

	if len(response.SettlementUpdate.Deposits) != 1 {
		t.Fatalf("expected one settlement deposit, got %d", len(response.SettlementUpdate.Deposits))
	}

	if response.SettlementUpdate.Deposits[0].DepositID != "dep-pending-source-1" {
		t.Fatalf("expected dep-pending-source-1, got %q", response.SettlementUpdate.Deposits[0].DepositID)
	}

	if len(response.SettlementUpdate.Withdrawals) != 1 {
		t.Fatalf("expected one settlement withdrawal, got %d", len(response.SettlementUpdate.Withdrawals))
	}

	if response.SettlementUpdate.Withdrawals[0].WithdrawID != "wd-pending-source-1" {
		t.Fatalf("expected wd-pending-source-1, got %q", response.SettlementUpdate.Withdrawals[0].WithdrawID)
	}

	if len(response.Witness.Accounts) != 1 {
		t.Fatalf("expected one witness account, got %d", len(response.Witness.Accounts))
	}

	if response.Witness.Accounts[0].OldBalance != "0" {
		t.Fatalf("expected oldBalance=0, got %q", response.Witness.Accounts[0].OldBalance)
	}

	if response.Witness.Accounts[0].NewBalance != "60" {
		t.Fatalf("expected newBalance=60, got %q", response.Witness.Accounts[0].NewBalance)
	}

	var batchID string
	var status string

	err = pool.QueryRow(
		ctx,
		`
		SELECT batch_id, status
		FROM batch_builds
		WHERE batch_id = $1
		`,
		response.SettlementUpdate.BatchID,
	).Scan(&batchID, &status)
	if err != nil {
		t.Fatalf("query batch_builds: %v", err)
	}

	if batchID != response.SettlementUpdate.BatchID {
		t.Fatalf("expected batchId=%q, got %q", response.SettlementUpdate.BatchID, batchID)
	}

	if status != "built" {
		t.Fatalf("expected batch build status=built, got %q", status)
	}

	deposits, withdrawals, err := offchainSvc.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement: %v", err)
	}

	if len(deposits) != 0 {
		t.Fatalf("expected no pending deposits after build, got %d", len(deposits))
	}

	if len(withdrawals) != 0 {
		t.Fatalf("expected no pending withdrawals after build, got %d", len(withdrawals))
	}
}
