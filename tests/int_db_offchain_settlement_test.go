package tests

import (
	"context"
	"errors"
	"os"
	"testing"

	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/repository"
)

func TestDBOffchainSettlementRepository(t *testing.T) {
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("set RUN_DB_TESTS=1 to run postgres tests")
	}

	ctx := context.Background()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, "DELETE FROM offchain_pending_withdrawals"); err != nil {
		t.Fatalf("clean offchain_pending_withdrawals: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM offchain_pending_deposits"); err != nil {
		t.Fatalf("clean offchain_pending_deposits: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM offchain_state_cursors WHERE name = 'test'"); err != nil {
		t.Fatalf("clean offchain_state_cursors: %v", err)
	}

	repo := repository.NewOffchainSettlementRepository(pool)

	if err := repo.SavePendingDeposit(ctx, repository.PendingDepositTransition{
		DepositID:     "dep-settlement-1",
		OwnerAddress:  "cosmos1alice",
		Denom:         "uusdc",
		Amount:        "100",
		RootBefore:    "0xrootA",
		RootAfter:     "0xrootB",
		BalanceBefore: "0",
		BalanceAfter:  "100",
	}); err != nil {
		t.Fatalf("save pending deposit: %v", err)
	}

	if err := repo.SavePendingWithdrawal(ctx, repository.PendingWithdrawalTransition{
		WithdrawID:         "wd-settlement-1",
		OwnerAddress:       "cosmos1alice",
		Denom:              "uusdc",
		Amount:             "40",
		DestinationAddress: "cosmos1alice",
		Nonce:              "1",
		Signature:          "0xmocksignature",
		Nullifier:          "0xmocknullifier",
		DestinationHash:    "0xmockdestinationhash",
		RootBefore:         "0xrootB",
		RootAfter:          "0xrootC",
		BalanceBefore:      "100",
		BalanceAfter:       "60",
	}); err != nil {
		t.Fatalf("save pending withdrawal: %v", err)
	}

	deposits, err := repo.ListPendingDeposits(ctx)
	if err != nil {
		t.Fatalf("list pending deposits: %v", err)
	}
	if len(deposits) != 1 {
		t.Fatalf("expected one pending deposit, got %d", len(deposits))
	}
	if deposits[0].DepositID != "dep-settlement-1" {
		t.Fatalf("expected dep-settlement-1, got %q", deposits[0].DepositID)
	}

	withdrawals, err := repo.ListPendingWithdrawals(ctx)
	if err != nil {
		t.Fatalf("list pending withdrawals: %v", err)
	}
	if len(withdrawals) != 1 {
		t.Fatalf("expected one pending withdrawal, got %d", len(withdrawals))
	}
	if withdrawals[0].WithdrawID != "wd-settlement-1" {
		t.Fatalf("expected wd-settlement-1, got %q", withdrawals[0].WithdrawID)
	}

	if err := repo.MarkIncluded(
		ctx,
		"batch-settlement-1",
		[]string{"dep-settlement-1"},
		[]string{"wd-settlement-1"},
	); err != nil {
		t.Fatalf("mark included: %v", err)
	}

	deposits, err = repo.ListPendingDeposits(ctx)
	if err != nil {
		t.Fatalf("list pending deposits after included: %v", err)
	}
	if len(deposits) != 0 {
		t.Fatalf("expected no pending deposits, got %d", len(deposits))
	}

	withdrawals, err = repo.ListPendingWithdrawals(ctx)
	if err != nil {
		t.Fatalf("list pending withdrawals after included: %v", err)
	}
	if len(withdrawals) != 0 {
		t.Fatalf("expected no pending withdrawals, got %d", len(withdrawals))
	}

	if err := repo.MarkCommitted(ctx, "batch-settlement-1", "0xtxhash"); err != nil {
		t.Fatalf("mark committed: %v", err)
	}

	if err := repo.UpsertCursor(ctx, repository.OffchainStateCursor{
		Name:                 "test",
		CommittedRoot:        "0xrootC",
		PendingRoot:          "0xrootC",
		LastCommittedBatchID: "batch-settlement-1",
	}); err != nil {
		t.Fatalf("upsert cursor: %v", err)
	}

	cursor, err := repo.GetCursor(ctx, "test")
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}

	if cursor.CommittedRoot != "0xrootC" {
		t.Fatalf("expected committedRoot=0xrootC, got %q", cursor.CommittedRoot)
	}

	if cursor.PendingRoot != "0xrootC" {
		t.Fatalf("expected pendingRoot=0xrootC, got %q", cursor.PendingRoot)
	}

	if cursor.LastCommittedBatchID != "batch-settlement-1" {
		t.Fatalf("expected lastCommittedBatchId=batch-settlement-1, got %q", cursor.LastCommittedBatchID)
	}

	err = repo.MarkIncluded(ctx, "batch-missing", []string{"missing-deposit"}, nil)
	if err == nil {
		t.Fatalf("expected missing deposit error")
	}

	if !errors.Is(err, repository.ErrOffchainSettlementRecordNotFound) {
		t.Fatalf("expected ErrOffchainSettlementRecordNotFound, got %v", err)
	}
}
