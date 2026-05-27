package tests

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestDBOffchainSettlementServiceAppliesDepositAndWithdrawal(t *testing.T) {
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

	depositTransition, err := svc.ApplyIndexedDeposit(ctx, types.DepositRecord{
		DepositID: "dep-service-1",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "100",
		Processed: false,
	})
	if err != nil {
		t.Fatalf("apply indexed deposit: %v", err)
	}

	if depositTransition.BalanceBefore != "0" {
		t.Fatalf("expected deposit balanceBefore=0, got %q", depositTransition.BalanceBefore)
	}

	if depositTransition.BalanceAfter != "100" {
		t.Fatalf("expected deposit balanceAfter=100, got %q", depositTransition.BalanceAfter)
	}

	if depositTransition.RootBefore == "" {
		t.Fatalf("expected deposit rootBefore")
	}

	if depositTransition.RootAfter == "" {
		t.Fatalf("expected deposit rootAfter")
	}

	if depositTransition.RootBefore == depositTransition.RootAfter {
		t.Fatalf("expected deposit root to change")
	}

	account := svc.ManagerAccount("cosmos1alice", "uusdc")
	if account.Balance != "100" {
		t.Fatalf("expected manager balance=100 after deposit, got %q", account.Balance)
	}

	withdrawTransition, err := svc.ApplyWithdrawRequest(ctx, types.WithdrawRequest{
		WithdrawID:  "wd-service-1",
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

	if withdrawTransition.BalanceBefore != "100" {
		t.Fatalf("expected withdraw balanceBefore=100, got %q", withdrawTransition.BalanceBefore)
	}

	if withdrawTransition.BalanceAfter != "60" {
		t.Fatalf("expected withdraw balanceAfter=60, got %q", withdrawTransition.BalanceAfter)
	}

	if withdrawTransition.Nullifier == "" {
		t.Fatalf("expected nullifier")
	}

	if withdrawTransition.DestinationHash == "" {
		t.Fatalf("expected destinationHash")
	}

	if withdrawTransition.RootBefore == "" {
		t.Fatalf("expected withdraw rootBefore")
	}

	if withdrawTransition.RootAfter == "" {
		t.Fatalf("expected withdraw rootAfter")
	}

	if withdrawTransition.RootBefore == withdrawTransition.RootAfter {
		t.Fatalf("expected withdraw root to change")
	}

	account = svc.ManagerAccount("cosmos1alice", "uusdc")
	if account.Balance != "60" {
		t.Fatalf("expected manager balance=60 after withdrawal, got %q", account.Balance)
	}

	deposits, withdrawals, err := svc.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement: %v", err)
	}

	if len(deposits) != 1 {
		t.Fatalf("expected one pending deposit, got %d", len(deposits))
	}

	if len(withdrawals) != 1 {
		t.Fatalf("expected one pending withdrawal, got %d", len(withdrawals))
	}

	if deposits[0].DepositID != "dep-service-1" {
		t.Fatalf("expected dep-service-1, got %q", deposits[0].DepositID)
	}

	if deposits[0].BalanceBefore != "0" {
		t.Fatalf("expected persisted deposit balanceBefore=0, got %q", deposits[0].BalanceBefore)
	}

	if deposits[0].BalanceAfter != "100" {
		t.Fatalf("expected persisted deposit balanceAfter=100, got %q", deposits[0].BalanceAfter)
	}

	if withdrawals[0].WithdrawID != "wd-service-1" {
		t.Fatalf("expected wd-service-1, got %q", withdrawals[0].WithdrawID)
	}

	if withdrawals[0].BalanceBefore != "100" {
		t.Fatalf("expected persisted withdrawal balanceBefore=100, got %q", withdrawals[0].BalanceBefore)
	}

	if withdrawals[0].BalanceAfter != "60" {
		t.Fatalf("expected persisted withdrawal balanceAfter=60, got %q", withdrawals[0].BalanceAfter)
	}

	cursor, err := svc.Cursor(ctx)
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}

	if cursor.PendingRoot != withdrawTransition.RootAfter {
		t.Fatalf("expected cursor pendingRoot=%q, got %q", withdrawTransition.RootAfter, cursor.PendingRoot)
	}

	if cursor.CommittedRoot == "" {
		t.Fatalf("expected cursor committedRoot")
	}

	if err := svc.MarkIncluded(
		ctx,
		"batch-service-1",
		[]string{"dep-service-1"},
		[]string{"wd-service-1"},
	); err != nil {
		t.Fatalf("mark included: %v", err)
	}

	deposits, withdrawals, err = svc.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement after included: %v", err)
	}

	if len(deposits) != 0 {
		t.Fatalf("expected no pending deposits after included, got %d", len(deposits))
	}

	if len(withdrawals) != 0 {
		t.Fatalf("expected no pending withdrawals after included, got %d", len(withdrawals))
	}

	if err := svc.CommitBatch(ctx, "batch-service-1", "0xtxhash", withdrawTransition.RootAfter); err != nil {
		t.Fatalf("commit batch: %v", err)
	}

	cursor, err = svc.Cursor(ctx)
	if err != nil {
		t.Fatalf("get cursor after commit: %v", err)
	}

	if cursor.CommittedRoot != withdrawTransition.RootAfter {
		t.Fatalf("expected committedRoot=%q, got %q", withdrawTransition.RootAfter, cursor.CommittedRoot)
	}

	if cursor.PendingRoot != withdrawTransition.RootAfter {
		t.Fatalf("expected pendingRoot=%q, got %q", withdrawTransition.RootAfter, cursor.PendingRoot)
	}

	if cursor.LastCommittedBatchID != "batch-service-1" {
		t.Fatalf("expected lastCommittedBatchId=batch-service-1, got %q", cursor.LastCommittedBatchID)
	}
}

func TestDBOffchainSettlementServiceRejectsInsufficientBalance(t *testing.T) {
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

	_, err = svc.ApplyWithdrawRequest(ctx, types.WithdrawRequest{
		WithdrawID:  "wd-service-insufficient",
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
		Nonce:       "1",
		Signature:   "0xmocksignature",
	}, "mock-user-secret")
	if err == nil {
		t.Fatalf("expected insufficient balance error")
	}

	if !errors.Is(err, appstate.ErrInsufficientBalance) {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}

	deposits, withdrawals, err := svc.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement: %v", err)
	}

	if len(deposits) != 0 {
		t.Fatalf("expected no pending deposits, got %d", len(deposits))
	}

	if len(withdrawals) != 0 {
		t.Fatalf("expected no pending withdrawals, got %d", len(withdrawals))
	}

	account := svc.ManagerAccount("cosmos1alice", "uusdc")
	if account.Balance != "0" {
		t.Fatalf("expected manager balance still 0, got %q", account.Balance)
	}
}

func TestDBOffchainSettlementServiceFailBatchMarksIncludedOperationsFailed(t *testing.T) {
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
		DepositID: "dep-fail-1",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "100",
		Processed: false,
	})
	if err != nil {
		t.Fatalf("apply indexed deposit: %v", err)
	}

	if err := svc.MarkIncluded(ctx, "batch-fail-1", []string{"dep-fail-1"}, nil); err != nil {
		t.Fatalf("mark included: %v", err)
	}

	if err := svc.FailBatch(ctx, "batch-fail-1", "mock failure"); err != nil {
		t.Fatalf("fail batch: %v", err)
	}

	var status string
	var errorMessage string

	err = pool.QueryRow(
		ctx,
		`
		SELECT status, COALESCE(error_message, '')
		FROM offchain_pending_deposits
		WHERE deposit_id = $1
		`,
		"dep-fail-1",
	).Scan(&status, &errorMessage)
	if err != nil {
		t.Fatalf("query failed deposit operation: %v", err)
	}

	if status != repository.OffchainSettlementStatusFailed {
		t.Fatalf("expected status=failed, got %q", status)
	}

	if errorMessage != "mock failure" {
		t.Fatalf("expected errorMessage=mock failure, got %q", errorMessage)
	}
}

func cleanOffchainSettlementTables(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()

	if _, err := pool.Exec(ctx, "DELETE FROM offchain_pending_withdrawals"); err != nil {
		t.Fatalf("clean offchain_pending_withdrawals: %v", err)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM offchain_pending_deposits"); err != nil {
		t.Fatalf("clean offchain_pending_deposits: %v", err)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM offchain_state_cursors"); err != nil {
		t.Fatalf("clean offchain_state_cursors: %v", err)
	}
}
