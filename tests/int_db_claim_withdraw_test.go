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

func TestDBClaimWithdrawRecord(t *testing.T) {
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

	_, err = pool.Exec(
		ctx,
		`
		INSERT INTO indexed_withdraw_records (
			withdraw_id,
			owner_address,
			denom,
			amount,
			destination_address,
			nullifier,
			claimed,
			tx_hash,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, FALSE, $7, NOW())
		`,
		"wd-claim-db-1",
		"cosmos1alice",
		"uusdc",
		"40",
		"cosmos1alice",
		"0xclaimdbnullifier",
		"0xsubmitdbtx",
	)
	if err != nil {
		t.Fatalf("seed indexed withdraw record: %v", err)
	}

	repo := repository.NewWithdrawRepositoryWithDB(
		store.NewMemoryStore(),
		pool,
		repository.WithdrawRequestStoreMemory,
		repository.WithdrawRecordStorePostgres,
	)

	record, err := repo.GetWithdrawRecord(ctx, "wd-claim-db-1")
	if err != nil {
		t.Fatalf("get withdraw record: %v", err)
	}

	if record.Claimed {
		t.Fatalf("expected claimed=false before claim")
	}

	claimedRecord, err := repo.ClaimWithdrawRecord(ctx, "wd-claim-db-1")
	if err != nil {
		t.Fatalf("claim withdraw record: %v", err)
	}

	if !claimedRecord.Claimed {
		t.Fatalf("expected claimed=true after claim")
	}

	var claimed bool
	err = pool.QueryRow(
		ctx,
		`
		SELECT claimed
		FROM indexed_withdraw_records
		WHERE withdraw_id = $1
		`,
		"wd-claim-db-1",
	).Scan(&claimed)
	if err != nil {
		t.Fatalf("query claimed flag: %v", err)
	}

	if !claimed {
		t.Fatalf("expected db claimed=true")
	}

	_, err = repo.ClaimWithdrawRecord(ctx, "wd-claim-db-1")
	if err == nil {
		t.Fatalf("expected already claimed error")
	}

	if err != repository.ErrWithdrawAlreadyClaimed {
		t.Fatalf("expected ErrWithdrawAlreadyClaimed, got %v", err)
	}

	_, err = repo.ClaimWithdrawRecord(ctx, "missing")
	if err == nil {
		t.Fatalf("expected missing withdraw record error")
	}

	if err != repository.ErrWithdrawRecordNotFound {
		t.Fatalf("expected ErrWithdrawRecordNotFound, got %v", err)
	}
}

func TestDBSaveWithdrawRecord(t *testing.T) {
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

	repo := repository.NewWithdrawRepositoryWithDB(
		store.NewMemoryStore(),
		pool,
		repository.WithdrawRequestStoreMemory,
		repository.WithdrawRecordStorePostgres,
	)

	repo.SaveWithdrawRecord(ctx, types.WithdrawRecord{
		WithdrawID:  "wd-save-db-1",
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
		Nullifier:   "0xsavedbnullifier",
		Claimed:     false,
	})

	record, err := repo.GetWithdrawRecord(ctx, "wd-save-db-1")
	if err != nil {
		t.Fatalf("get saved withdraw record: %v", err)
	}

	if record.WithdrawID != "wd-save-db-1" {
		t.Fatalf("expected withdrawId=wd-save-db-1, got %q", record.WithdrawID)
	}

	if record.Owner != "cosmos1alice" {
		t.Fatalf("expected owner=cosmos1alice, got %q", record.Owner)
	}

	if record.Nullifier != "0xsavedbnullifier" {
		t.Fatalf("expected nullifier=0xsavedbnullifier, got %q", record.Nullifier)
	}
}
