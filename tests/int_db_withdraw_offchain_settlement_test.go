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

func TestDBWithdrawServiceAppliesRequestToOffchainSettlement(t *testing.T) {
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

	if _, err := pool.Exec(ctx, "DELETE FROM withdraw_requests"); err != nil {
		t.Fatalf("clean withdraw_requests: %v", err)
	}
	if _, err := pool.Exec(ctx, "ALTER SEQUENCE withdraw_request_seq RESTART WITH 1"); err != nil {
		t.Fatalf("reset withdraw_request_seq: %v", err)
	}

	manager := appstate.NewOffchainStateManager()
	offchainRepo := repository.NewOffchainSettlementRepository(pool)
	offchainSvc := service.NewOffchainSettlementService(manager, offchainRepo)

	_, err = offchainSvc.ApplyIndexedDeposit(ctx, types.DepositRecord{
		DepositID: "dep-withdraw-service-1",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "100",
		Processed: false,
	})
	if err != nil {
		t.Fatalf("seed deposit into offchain settlement: %v", err)
	}

	withdrawRepo := repository.NewWithdrawRepositoryWithDB(
		store.NewMemoryStore(),
		pool,
		repository.WithdrawRequestStorePostgres,
		repository.WithdrawRecordStoreMemory,
	)

	withdrawSvc := service.NewWithdrawServiceWithOffchainSettlement(
		withdrawRepo,
		relayer.NewLocalClient(),
		true,
		offchainSvc,
	)

	response, err := withdrawSvc.CreateWithdrawRequest(ctx, types.WithdrawRequestBody{
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
	})
	if err != nil {
		t.Fatalf("create withdraw request: %v", err)
	}

	if response.WithdrawRequest.WithdrawID != "wd-1" {
		t.Fatalf("expected withdrawId=wd-1, got %q", response.WithdrawRequest.WithdrawID)
	}

	account := offchainSvc.ManagerAccount("cosmos1alice", "uusdc")
	if account.Balance != "60" {
		t.Fatalf("expected offchain manager balance=60, got %q", account.Balance)
	}

	deposits, withdrawals, err := offchainSvc.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement: %v", err)
	}

	if len(deposits) != 1 {
		t.Fatalf("expected one pending deposit, got %d", len(deposits))
	}

	if len(withdrawals) != 1 {
		t.Fatalf("expected one pending withdrawal, got %d", len(withdrawals))
	}

	withdrawal := withdrawals[0]

	if withdrawal.WithdrawID != "wd-1" {
		t.Fatalf("expected pending withdrawal wd-1, got %q", withdrawal.WithdrawID)
	}

	if withdrawal.BalanceBefore != "100" {
		t.Fatalf("expected withdrawal balanceBefore=100, got %q", withdrawal.BalanceBefore)
	}

	if withdrawal.BalanceAfter != "60" {
		t.Fatalf("expected withdrawal balanceAfter=60, got %q", withdrawal.BalanceAfter)
	}

	if withdrawal.Nullifier == "" {
		t.Fatalf("expected nullifier")
	}

	if withdrawal.DestinationHash == "" {
		t.Fatalf("expected destinationHash")
	}
}

func TestDBWithdrawServiceRejectsInsufficientOffchainBalance(t *testing.T) {
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

	if _, err := pool.Exec(ctx, "DELETE FROM withdraw_requests"); err != nil {
		t.Fatalf("clean withdraw_requests: %v", err)
	}
	if _, err := pool.Exec(ctx, "ALTER SEQUENCE withdraw_request_seq RESTART WITH 1"); err != nil {
		t.Fatalf("reset withdraw_request_seq: %v", err)
	}

	manager := appstate.NewOffchainStateManager()
	offchainRepo := repository.NewOffchainSettlementRepository(pool)
	offchainSvc := service.NewOffchainSettlementService(manager, offchainRepo)

	withdrawRepo := repository.NewWithdrawRepositoryWithDB(
		store.NewMemoryStore(),
		pool,
		repository.WithdrawRequestStorePostgres,
		repository.WithdrawRecordStoreMemory,
	)

	withdrawSvc := service.NewWithdrawServiceWithOffchainSettlement(
		withdrawRepo,
		relayer.NewLocalClient(),
		true,
		offchainSvc,
	)

	_, err = withdrawSvc.CreateWithdrawRequest(ctx, types.WithdrawRequestBody{
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
	})
	if err == nil {
		t.Fatalf("expected insufficient balance error")
	}

	if !service.IsWithdrawInsufficientBalanceError(err) {
		t.Fatalf("expected insufficient balance error, got %v", err)
	}

	_, withdrawals, err := offchainSvc.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement: %v", err)
	}

	if len(withdrawals) != 0 {
		t.Fatalf("expected no pending withdrawals, got %d", len(withdrawals))
	}
}
