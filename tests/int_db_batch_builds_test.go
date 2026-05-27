package tests

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestDBBatchBuildRepository(t *testing.T) {
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("set RUN_DB_TESTS=1 to run postgres tests")
	}

	ctx := context.Background()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, "DELETE FROM batch_builds"); err != nil {
		t.Fatalf("clean batch_builds: %v", err)
	}

	repo := repository.NewBatchRepositoryWithDB(
		store.NewMemoryStore(),
		pool,
		repository.BatchBuildStorePostgres,
	)

	settlementUpdate := types.SettlementUpdate{
		BatchID:      "batch-db-1",
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
				WithdrawID:      "wd-1",
				Owner:           "cosmos1alice",
				Denom:           "uusdc",
				Amount:          "40",
				Destination:     "cosmos1alice",
				DestinationHash: "0xdestinationhash",
				Nullifier:       "0xnullifier",
			},
		},
	}

	batchCommitments := types.BatchCommitments{
		DepositsRoot:        "0xdepositsRoot",
		WithdrawalsRoot:     "0xwithdrawalsRoot",
		NullifiersRoot:      "0xnullifiersRoot",
		WithdrawOutputsRoot: "0xwithdrawOutputsRoot",
	}

	witness := types.Witness{
		Accounts: []types.WitnessAccount{
			{
				Owner:      "cosmos1alice",
				UserSecret: "mock-user-secret",
				Nonce:      "1",
				OldBalance: "0",
				NewBalance: "60",
			},
		},
	}

	repo.SaveBatchBuild(ctx, settlementUpdate, batchCommitments, witness)

	var (
		batchID        string
		oldStateRoot   string
		newStateRoot   string
		settlementRaw  []byte
		commitmentsRaw []byte
		witnessRaw     []byte
		status         string
	)

	err = pool.QueryRow(
		ctx,
		`
		SELECT
			batch_id,
			old_state_root,
			new_state_root,
			settlement_update,
			batch_commitments,
			witness,
			status
		FROM batch_builds
		WHERE batch_id = $1
		`,
		settlementUpdate.BatchID,
	).Scan(
		&batchID,
		&oldStateRoot,
		&newStateRoot,
		&settlementRaw,
		&commitmentsRaw,
		&witnessRaw,
		&status,
	)
	if err != nil {
		t.Fatalf("query batch build: %v", err)
	}

	if batchID != "batch-db-1" {
		t.Fatalf("expected batchId=batch-db-1, got %q", batchID)
	}

	if oldStateRoot != "0xrootA" {
		t.Fatalf("expected oldStateRoot=0xrootA, got %q", oldStateRoot)
	}

	if newStateRoot != "0xrootB" {
		t.Fatalf("expected newStateRoot=0xrootB, got %q", newStateRoot)
	}

	if status != "built" {
		t.Fatalf("expected status=built, got %q", status)
	}

	var storedSettlement types.SettlementUpdate
	if err := json.Unmarshal(settlementRaw, &storedSettlement); err != nil {
		t.Fatalf("unmarshal settlement: %v", err)
	}

	if storedSettlement.BatchID != settlementUpdate.BatchID {
		t.Fatalf("expected settlement batchId=%q, got %q", settlementUpdate.BatchID, storedSettlement.BatchID)
	}

	var storedCommitments types.BatchCommitments
	if err := json.Unmarshal(commitmentsRaw, &storedCommitments); err != nil {
		t.Fatalf("unmarshal commitments: %v", err)
	}

	if storedCommitments.DepositsRoot != batchCommitments.DepositsRoot {
		t.Fatalf("expected depositsRoot=%q, got %q", batchCommitments.DepositsRoot, storedCommitments.DepositsRoot)
	}

	var storedWitness types.Witness
	if err := json.Unmarshal(witnessRaw, &storedWitness); err != nil {
		t.Fatalf("unmarshal witness: %v", err)
	}

	if len(storedWitness.Accounts) != 1 {
		t.Fatalf("expected one witness account, got %d", len(storedWitness.Accounts))
	}

	if storedWitness.Accounts[0].Owner != "cosmos1alice" {
		t.Fatalf("expected witness owner=cosmos1alice, got %q", storedWitness.Accounts[0].Owner)
	}
}
