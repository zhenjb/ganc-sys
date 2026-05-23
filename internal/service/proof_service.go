package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ProofService owns proof generation use cases.
//
// INT-04 status:
// - Returns local deterministic ProofBundle.
// - P2 prover is not connected yet.
// - No real ZK proof is generated yet.
//
// TODO(INT-08 / P2):
// Replace ProofRepository fixture with a real prover client call.
// The prover input must include:
// - SettlementUpdate,
// - BatchCommitments,
// - Witness.
//
// Public input order is locked:
// 0 oldStateRoot
// 1 newStateRoot
// 2 depositsRoot
// 3 withdrawalsRoot
// 4 nullifiersRoot
// 5 withdrawOutputsRoot
type ProofService struct {
	proofRepository *repository.ProofRepository
}

func NewProofService(proofRepository *repository.ProofRepository) *ProofService {
	return &ProofService{
		proofRepository: proofRepository,
	}
}

func (s *ProofService) GenerateProof(ctx context.Context, req types.GenerateProofRequestBody) types.GenerateProofResponse {
	// TODO(INT-08):
	// Validate req.SettlementUpdate + req.BatchCommitments + req.Witness,
	// then call the real prover.
	return types.GenerateProofResponse{
		ProofBundle: s.proofRepository.GetLocalProofBundle(ctx),
		State: types.PartialState{
			ProofStatus: "ready",
		},
	}
}
