package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ProofRepository owns local proof fixtures.
//
// INT-04 status:
// - Proof generation is still local deterministic data.
// - P2 prover is not connected yet.
// - No real ZK proof is generated here yet.
//
// TODO(INT-08 / P2):
// Replace this fixture with a real prover call that receives:
// - SettlementUpdate,
// - BatchCommitments,
// - Witness,
// and returns a real ProofBundle.
//
// Public input order is locked:
// 0 oldStateRoot
// 1 newStateRoot
// 2 depositsRoot
// 3 withdrawalsRoot
// 4 nullifiersRoot
// 5 withdrawOutputsRoot
type ProofRepository struct{}

func NewProofRepository() *ProofRepository {
	return &ProofRepository{}
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
