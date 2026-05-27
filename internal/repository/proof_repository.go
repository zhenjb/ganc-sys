package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const ProofBundleStoreMemory = "memory"
const ProofBundleStorePostgres = "postgres"

// ProofRepository owns proof read-model persistence.
//
// DB-04 status:
// - proofBundle can be persisted in Postgres.
// - default mode remains MemoryStore for local tests.
// - latestProof in MemoryStore is still updated for /api/state local dashboard.
type ProofRepository struct {
	store *store.MemoryStore

	dbPool           *pgxpool.Pool
	proofBundleStore string
}

func NewProofRepository(store *store.MemoryStore) *ProofRepository {
	return &ProofRepository{
		store:            store,
		proofBundleStore: ProofBundleStoreMemory,
	}
}

func NewProofRepositoryWithDB(
	store *store.MemoryStore,
	dbPool *pgxpool.Pool,
	proofBundleStore string,
) *ProofRepository {
	if proofBundleStore == "" {
		proofBundleStore = ProofBundleStoreMemory
	}

	return &ProofRepository{
		store:            store,
		dbPool:           dbPool,
		proofBundleStore: proofBundleStore,
	}
}

func (r *ProofRepository) SaveProofBundle(
	ctx context.Context,
	batchID string,
	proofBundle types.ProofBundle,
) {
	r.store.SaveProofBundle(proofBundle)

	if r.proofBundleStore == ProofBundleStorePostgres {
		if err := r.saveProofBundlePostgres(ctx, batchID, proofBundle); err != nil {
			panic(fmt.Sprintf("save proof bundle in postgres: %v", err))
		}
	}
}

func (r *ProofRepository) saveProofBundlePostgres(
	ctx context.Context,
	batchID string,
	proofBundle types.ProofBundle,
) error {
	if r.dbPool == nil {
		return errors.New("postgres proof bundle store selected but db pool is nil")
	}

	if batchID == "" {
		return errors.New("batch id is required to persist proof bundle")
	}

	publicInputsRaw, err := json.Marshal(proofBundle.PublicInputs)
	if err != nil {
		return fmt.Errorf("marshal public inputs: %w", err)
	}

	_, err = r.dbPool.Exec(
		ctx,
		`
		INSERT INTO proof_bundles (
			batch_id,
			proof,
			public_inputs,
			verification_key_id,
			status,
			updated_at
		)
		VALUES ($1, $2, $3, $4, 'ready', NOW())
		ON CONFLICT (batch_id) DO UPDATE SET
			proof = EXCLUDED.proof,
			public_inputs = EXCLUDED.public_inputs,
			verification_key_id = EXCLUDED.verification_key_id,
			status = EXCLUDED.status,
			updated_at = NOW()
		`,
		batchID,
		proofBundle.Proof,
		publicInputsRaw,
		proofBundle.VerificationKeyID,
	)
	if err != nil {
		return err
	}

	return nil
}

func (r *ProofRepository) GetLocalProofBundle(ctx context.Context) types.ProofBundle {
	return types.ProofBundle{
		Proof: "0xmockproof",
		PublicInputs: []string{
			"0xrootA",
			"0xrootB",
			"0xdepositsRoot",
			"0xwithdrawalsRoot",
			"0xnullifiersRoot",
			"0xwithdrawOutputsRoot",
		},
		VerificationKeyID: "v1",
	}
}
