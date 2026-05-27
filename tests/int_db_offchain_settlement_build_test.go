package tests

import (
	"context"
	"errors"
	"os"
	"testing"

	appbatch "github.com/zhenjb/ganc-sys/internal/batch"
	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestDBOffchainSettlementServiceBuildsPendingBatch(t *testing.T) {
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
		DepositID: "dep-build-1",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "100",
		Processed: false,
	})
	if err != nil {
		t.Fatalf("apply indexed deposit: %v", err)
	}

	withdrawTransition, err := svc.ApplyWithdrawRequest(ctx, types.WithdrawRequest{
		WithdrawID:  "wd-build-1",
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

	cursorBeforeBuild, err := svc.Cursor(ctx)
	if err != nil {
		t.Fatalf("cursor before build: %v", err)
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

	if output.SettlementUpdate.BatchID == "" {
		t.Fatalf("expected batchId")
	}

	if output.SettlementUpdate.OldStateRoot != cursorBeforeBuild.CommittedRoot {
		t.Fatalf(
			"expected oldStateRoot=%q, got %q",
			cursorBeforeBuild.CommittedRoot,
			output.SettlementUpdate.OldStateRoot,
		)
	}

	if output.SettlementUpdate.NewStateRoot != cursorBeforeBuild.PendingRoot {
		t.Fatalf(
			"expected newStateRoot=%q, got %q",
			cursorBeforeBuild.PendingRoot,
			output.SettlementUpdate.NewStateRoot,
		)
	}

	if output.SettlementUpdate.NewStateRoot != withdrawTransition.RootAfter {
		t.Fatalf(
			"expected newStateRoot=%q, got %q",
			withdrawTransition.RootAfter,
			output.SettlementUpdate.NewStateRoot,
		)
	}

	if len(output.SettlementUpdate.Deposits) != 1 {
		t.Fatalf("expected one settlement deposit, got %d", len(output.SettlementUpdate.Deposits))
	}

	if output.SettlementUpdate.Deposits[0].DepositID != "dep-build-1" {
		t.Fatalf("expected deposit dep-build-1, got %q", output.SettlementUpdate.Deposits[0].DepositID)
	}

	if len(output.SettlementUpdate.Withdrawals) != 1 {
		t.Fatalf("expected one settlement withdrawal, got %d", len(output.SettlementUpdate.Withdrawals))
	}

	if output.SettlementUpdate.Withdrawals[0].WithdrawID != "wd-build-1" {
		t.Fatalf("expected withdrawal wd-build-1, got %q", output.SettlementUpdate.Withdrawals[0].WithdrawID)
	}

	if output.SettlementUpdate.Withdrawals[0].Nullifier != withdrawTransition.Nullifier {
		t.Fatalf(
			"expected nullifier=%q, got %q",
			withdrawTransition.Nullifier,
			output.SettlementUpdate.Withdrawals[0].Nullifier,
		)
	}

	if len(output.Witness.Accounts) != 1 {
		t.Fatalf("expected one witness account, got %d", len(output.Witness.Accounts))
	}

	witnessAccount := output.Witness.Accounts[0]

	if witnessAccount.Owner != "cosmos1alice" {
		t.Fatalf("expected witness owner=cosmos1alice, got %q", witnessAccount.Owner)
	}

	if witnessAccount.OldBalance != depositTransition.BalanceBefore {
		t.Fatalf(
			"expected witness oldBalance=%q, got %q",
			depositTransition.BalanceBefore,
			witnessAccount.OldBalance,
		)
	}

	if witnessAccount.NewBalance != withdrawTransition.BalanceAfter {
		t.Fatalf(
			"expected witness newBalance=%q, got %q",
			withdrawTransition.BalanceAfter,
			witnessAccount.NewBalance,
		)
	}

	if witnessAccount.OldBalance != "0" {
		t.Fatalf("expected witness oldBalance=0, got %q", witnessAccount.OldBalance)
	}

	if witnessAccount.NewBalance != "60" {
		t.Fatalf("expected witness newBalance=60, got %q", witnessAccount.NewBalance)
	}

	if output.BatchCommitments.DepositsRoot == "" {
		t.Fatalf("expected depositsRoot")
	}

	if output.BatchCommitments.WithdrawalsRoot == "" {
		t.Fatalf("expected withdrawalsRoot")
	}

	if output.BatchCommitments.NullifiersRoot == "" {
		t.Fatalf("expected nullifiersRoot")
	}

	if output.BatchCommitments.WithdrawOutputsRoot == "" {
		t.Fatalf("expected withdrawOutputsRoot")
	}

	deposits, withdrawals, err := svc.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement after build: %v", err)
	}

	if len(deposits) != 0 {
		t.Fatalf("expected no pending deposits after build inclusion, got %d", len(deposits))
	}

	if len(withdrawals) != 0 {
		t.Fatalf("expected no pending withdrawals after build inclusion, got %d", len(withdrawals))
	}

	var depositStatus string
	var depositBatchID string

	err = pool.QueryRow(
		ctx,
		`
		SELECT status, COALESCE(batch_id, '')
		FROM offchain_pending_deposits
		WHERE deposit_id = $1
		`,
		"dep-build-1",
	).Scan(&depositStatus, &depositBatchID)
	if err != nil {
		t.Fatalf("query deposit status: %v", err)
	}

	if depositStatus != repository.OffchainSettlementStatusIncluded {
		t.Fatalf("expected deposit status=included, got %q", depositStatus)
	}

	if depositBatchID != output.SettlementUpdate.BatchID {
		t.Fatalf("expected deposit batchId=%q, got %q", output.SettlementUpdate.BatchID, depositBatchID)
	}

	var withdrawalStatus string
	var withdrawalBatchID string

	err = pool.QueryRow(
		ctx,
		`
		SELECT status, COALESCE(batch_id, '')
		FROM offchain_pending_withdrawals
		WHERE withdraw_id = $1
		`,
		"wd-build-1",
	).Scan(&withdrawalStatus, &withdrawalBatchID)
	if err != nil {
		t.Fatalf("query withdrawal status: %v", err)
	}

	if withdrawalStatus != repository.OffchainSettlementStatusIncluded {
		t.Fatalf("expected withdrawal status=included, got %q", withdrawalStatus)
	}

	if withdrawalBatchID != output.SettlementUpdate.BatchID {
		t.Fatalf("expected withdrawal batchId=%q, got %q", output.SettlementUpdate.BatchID, withdrawalBatchID)
	}
}

func TestDBOffchainSettlementServiceBuildPendingBatchRejectsEmptyPendingSet(t *testing.T) {
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

	_, err = svc.BuildPendingBatch(ctx, nil)
	if err == nil {
		t.Fatalf("expected no pending settlement operations error")
	}

	if !errors.Is(err, service.ErrNoPendingSettlementOperations) {
		t.Fatalf("expected ErrNoPendingSettlementOperations, got %v", err)
	}
}
