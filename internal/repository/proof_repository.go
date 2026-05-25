package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ProofRepository owns proof read-model persistence.
//
// INT-11 status:
// - Proof generation updates latestProof and proofStatus for GET /api/state.
type ProofRepository struct {
	store *store.MemoryStore
}

func NewProofRepository(store *store.MemoryStore) *ProofRepository {
	return &ProofRepository{
		store: store,
	}
}

func (r *ProofRepository) SaveProofBundle(ctx context.Context, proofBundle types.ProofBundle) {
	r.store.SaveProofBundle(proofBundle)
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
