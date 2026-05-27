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

func TestDBProofBundleRepository(t *testing.T) {
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("set RUN_DB_TESTS=1 to run postgres tests")
	}

	ctx := context.Background()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, "DELETE FROM proof_bundles"); err != nil {
		t.Fatalf("clean proof_bundles: %v", err)
	}

	repo := repository.NewProofRepositoryWithDB(
		store.NewMemoryStore(),
		pool,
		repository.ProofBundleStorePostgres,
	)

	proofBundle := types.ProofBundle{
		Proof: "0xproofdb",
		PublicInputs: []string{
			"0xrootA",
			"0xrootB",
			"0xdepositsRoot",
			"0xwithdrawalsRoot",
			"0xnullifiersRoot",
			"0xwithdrawOutputsRoot",
		},
		VerificationKeyID: "local-v1",
	}

	repo.SaveProofBundle(ctx, "batch-db-1", proofBundle)

	var (
		batchID           string
		proof             string
		publicInputsRaw   []byte
		verificationKeyID string
		status            string
	)

	err = pool.QueryRow(
		ctx,
		`
		SELECT
			batch_id,
			proof,
			public_inputs,
			verification_key_id,
			status
		FROM proof_bundles
		WHERE batch_id = $1
		`,
		"batch-db-1",
	).Scan(
		&batchID,
		&proof,
		&publicInputsRaw,
		&verificationKeyID,
		&status,
	)
	if err != nil {
		t.Fatalf("query proof bundle: %v", err)
	}

	if batchID != "batch-db-1" {
		t.Fatalf("expected batchId=batch-db-1, got %q", batchID)
	}

	if proof != proofBundle.Proof {
		t.Fatalf("expected proof=%q, got %q", proofBundle.Proof, proof)
	}

	if verificationKeyID != proofBundle.VerificationKeyID {
		t.Fatalf("expected verificationKeyId=%q, got %q", proofBundle.VerificationKeyID, verificationKeyID)
	}

	if status != "ready" {
		t.Fatalf("expected status=ready, got %q", status)
	}

	var storedPublicInputs []string
	if err := json.Unmarshal(publicInputsRaw, &storedPublicInputs); err != nil {
		t.Fatalf("unmarshal public inputs: %v", err)
	}

	if len(storedPublicInputs) != len(proofBundle.PublicInputs) {
		t.Fatalf("expected %d public inputs, got %d", len(proofBundle.PublicInputs), len(storedPublicInputs))
	}

	for i := range proofBundle.PublicInputs {
		if storedPublicInputs[i] != proofBundle.PublicInputs[i] {
			t.Fatalf("expected publicInputs[%d]=%q, got %q", i, proofBundle.PublicInputs[i], storedPublicInputs[i])
		}
	}
}
