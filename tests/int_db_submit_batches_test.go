package tests

import (
	"context"
	"os"
	"testing"

	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestDBSubmitBatchRepository(t *testing.T) {
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("set RUN_DB_TESTS=1 to run postgres tests")
	}

	ctx := context.Background()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, "DELETE FROM indexed_withdraw_records"); err != nil {
		t.Fatalf("clean indexed_withdraw_records: %v", err)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM submit_batches"); err != nil {
		t.Fatalf("clean submit_batches: %v", err)
	}

	repo := repository.NewBatchRepositoryWithDB(
		store.NewMemoryStore(),
		pool,
		repository.BatchBuildStoreMemory,
		repository.SubmitBatchStorePostgres,
	)

	settlementUpdate := types.SettlementUpdate{
		BatchID:      "batch-submit-db-1",
		OldStateRoot: "0xrootA",
		NewStateRoot: "0xrootB",
		Deposits: []types.SettlementDeposit{
			{
				DepositID: "dep-1",
				Owner:     "cosmos1alice",
				Denom:     "uusdc",
				Amount:    "100",
			},
		},
		Withdrawals: []types.SettlementWithdrawal{
			{
				WithdrawID:      "wd-submit-db-1",
				Owner:           "cosmos1alice",
				Denom:           "uusdc",
				Amount:          "40",
				Destination:     "cosmos1alice",
				DestinationHash: "0xdestinationhash",
				Nullifier:       "0xsubmitdbnullifier",
			},
		},
	}

	batchCommitments := types.BatchCommitments{
		DepositsRoot:        "0xdepositsRoot",
		WithdrawalsRoot:     "0xwithdrawalsRoot",
		NullifiersRoot:      "0xnullifiersRoot",
		WithdrawOutputsRoot: "0xwithdrawOutputsRoot",
	}

	withdrawRecords := []types.WithdrawRecord{
		{
			WithdrawID:  "wd-submit-db-1",
			Owner:       "cosmos1alice",
			Denom:       "uusdc",
			Amount:      "40",
			Destination: "cosmos1alice",
			Nullifier:   "0xsubmitdbnullifier",
			Claimed:     false,
		},
	}

	repo.SaveBatchSubmitResult(
		ctx,
		settlementUpdate,
		batchCommitments,
		"0xsubmitdbtx",
		true,
		"accepted",
		withdrawRecords,
	)

	var (
		batchID     string
		txHash      string
		accepted    bool
		proofStatus string
	)

	err = pool.QueryRow(
		ctx,
		`
		SELECT
			batch_id,
			tx_hash,
			accepted,
			proof_status
		FROM submit_batches
		WHERE batch_id = $1
		`,
		settlementUpdate.BatchID,
	).Scan(
		&batchID,
		&txHash,
		&accepted,
		&proofStatus,
	)
	if err != nil {
		t.Fatalf("query submit batch: %v", err)
	}

	if batchID != settlementUpdate.BatchID {
		t.Fatalf("expected batchId=%q, got %q", settlementUpdate.BatchID, batchID)
	}

	if txHash != "0xsubmitdbtx" {
		t.Fatalf("expected txHash=0xsubmitdbtx, got %q", txHash)
	}

	if !accepted {
		t.Fatalf("expected accepted=true")
	}

	if proofStatus != "accepted" {
		t.Fatalf("expected proofStatus=accepted, got %q", proofStatus)
	}

	var (
		withdrawID  string
		owner       string
		denom       string
		amount      string
		destination string
		nullifier   string
		claimed     bool
		recordTx    string
	)

	err = pool.QueryRow(
		ctx,
		`
		SELECT
			withdraw_id,
			owner_address,
			denom,
			amount,
			destination_address,
			nullifier,
			claimed,
			tx_hash
		FROM indexed_withdraw_records
		WHERE withdraw_id = $1
		`,
		"wd-submit-db-1",
	).Scan(
		&withdrawID,
		&owner,
		&denom,
		&amount,
		&destination,
		&nullifier,
		&claimed,
		&recordTx,
	)
	if err != nil {
		t.Fatalf("query indexed withdraw record: %v", err)
	}

	if withdrawID != "wd-submit-db-1" {
		t.Fatalf("expected withdrawId=wd-submit-db-1, got %q", withdrawID)
	}

	if owner != "cosmos1alice" {
		t.Fatalf("expected owner=cosmos1alice, got %q", owner)
	}

	if denom != "uusdc" {
		t.Fatalf("expected denom=uusdc, got %q", denom)
	}

	if amount != "40" {
		t.Fatalf("expected amount=40, got %q", amount)
	}

	if destination != "cosmos1alice" {
		t.Fatalf("expected destination=cosmos1alice, got %q", destination)
	}

	if nullifier != "0xsubmitdbnullifier" {
		t.Fatalf("expected nullifier=0xsubmitdbnullifier, got %q", nullifier)
	}

	if claimed {
		t.Fatalf("expected claimed=false")
	}

	if recordTx != "0xsubmitdbtx" {
		t.Fatalf("expected withdraw record txHash=0xsubmitdbtx, got %q", recordTx)
	}
}
