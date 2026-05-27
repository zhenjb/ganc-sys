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

func TestDBBatchSubmitCommitsPendingOffchainSettlement(t *testing.T) {
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

	if _, err := pool.Exec(ctx, "DELETE FROM submit_batches"); err != nil {
		t.Fatalf("clean submit_batches: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM batch_builds"); err != nil {
		t.Fatalf("clean batch_builds: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM indexed_withdraw_records"); err != nil {
		t.Fatalf("clean indexed_withdraw_records: %v", err)
	}

	manager := appstate.NewOffchainStateManager()
	offchainRepo := repository.NewOffchainSettlementRepository(pool)
	offchainSvc := service.NewOffchainSettlementService(manager, offchainRepo)

	_, err = offchainSvc.ApplyIndexedDeposit(ctx, types.DepositRecord{
		DepositID: "dep-submit-commit-1",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "100",
		Processed: false,
	})
	if err != nil {
		t.Fatalf("apply indexed deposit: %v", err)
	}

	_, err = offchainSvc.ApplyWithdrawRequest(ctx, types.WithdrawRequest{
		WithdrawID:  "wd-submit-commit-1",
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
		repository.SubmitBatchStorePostgres,
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

	buildResp, err := batchSvc.BuildBatch(ctx, types.BuildBatchRequestBody{})
	if err != nil {
		t.Fatalf("build pending batch: %v", err)
	}

	submitResp, err := batchSvc.SubmitBatch(ctx, types.SubmitBatchRequestBody{
		SettlementUpdate: buildResp.SettlementUpdate,
		BatchCommitments: buildResp.BatchCommitments,
		ProofBundle: types.ProofBundle{
			Proof: "0xmockproof",
			PublicInputs: []string{
				buildResp.SettlementUpdate.OldStateRoot,
				buildResp.SettlementUpdate.NewStateRoot,
				buildResp.BatchCommitments.DepositsRoot,
				buildResp.BatchCommitments.WithdrawalsRoot,
				buildResp.BatchCommitments.NullifiersRoot,
				buildResp.BatchCommitments.WithdrawOutputsRoot,
			},
			VerificationKeyID: "local-v1",
		},
	})
	if err != nil {
		t.Fatalf("submit batch: %v", err)
	}

	if !submitResp.Accepted {
		t.Fatalf("expected submit accepted")
	}

	if submitResp.ProofStatus != "accepted" {
		t.Fatalf("expected proofStatus=accepted, got %q", submitResp.ProofStatus)
	}

	cursor, err := offchainSvc.Cursor(ctx)
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}

	if cursor.CommittedRoot != buildResp.SettlementUpdate.NewStateRoot {
		t.Fatalf(
			"expected committedRoot=%q, got %q",
			buildResp.SettlementUpdate.NewStateRoot,
			cursor.CommittedRoot,
		)
	}

	if cursor.PendingRoot != buildResp.SettlementUpdate.NewStateRoot {
		t.Fatalf(
			"expected pendingRoot=%q, got %q",
			buildResp.SettlementUpdate.NewStateRoot,
			cursor.PendingRoot,
		)
	}

	if cursor.LastCommittedBatchID != buildResp.SettlementUpdate.BatchID {
		t.Fatalf(
			"expected lastCommittedBatchId=%q, got %q",
			buildResp.SettlementUpdate.BatchID,
			cursor.LastCommittedBatchID,
		)
	}

	var depositStatus string
	var depositTxHash string

	err = pool.QueryRow(
		ctx,
		`
		SELECT status, COALESCE(tx_hash, '')
		FROM offchain_pending_deposits
		WHERE deposit_id = $1
		`,
		"dep-submit-commit-1",
	).Scan(&depositStatus, &depositTxHash)
	if err != nil {
		t.Fatalf("query deposit settlement status: %v", err)
	}

	if depositStatus != repository.OffchainSettlementStatusCommitted {
		t.Fatalf("expected deposit status=committed, got %q", depositStatus)
	}

	if depositTxHash != submitResp.TxHash {
		t.Fatalf("expected deposit txHash=%q, got %q", submitResp.TxHash, depositTxHash)
	}

	var withdrawalStatus string
	var withdrawalTxHash string

	err = pool.QueryRow(
		ctx,
		`
		SELECT status, COALESCE(tx_hash, '')
		FROM offchain_pending_withdrawals
		WHERE withdraw_id = $1
		`,
		"wd-submit-commit-1",
	).Scan(&withdrawalStatus, &withdrawalTxHash)
	if err != nil {
		t.Fatalf("query withdrawal settlement status: %v", err)
	}

	if withdrawalStatus != repository.OffchainSettlementStatusCommitted {
		t.Fatalf("expected withdrawal status=committed, got %q", withdrawalStatus)
	}

	if withdrawalTxHash != submitResp.TxHash {
		t.Fatalf("expected withdrawal txHash=%q, got %q", submitResp.TxHash, withdrawalTxHash)
	}

	var submitBatchID string
	var accepted bool

	err = pool.QueryRow(
		ctx,
		`
		SELECT batch_id, accepted
		FROM submit_batches
		WHERE batch_id = $1
		`,
		buildResp.SettlementUpdate.BatchID,
	).Scan(&submitBatchID, &accepted)
	if err != nil {
		t.Fatalf("query submit_batches: %v", err)
	}

	if submitBatchID != buildResp.SettlementUpdate.BatchID {
		t.Fatalf("expected submit batchId=%q, got %q", buildResp.SettlementUpdate.BatchID, submitBatchID)
	}

	if !accepted {
		t.Fatalf("expected submit_batches.accepted=true")
	}
}
