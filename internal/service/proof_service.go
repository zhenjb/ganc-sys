package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/prover"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ProofService owns proof generation orchestration.
//
// P4 owns this service as integration glue.
// P2 owns the actual prover implementation behind prover.Client.
type ProofService struct {
	proverClient    prover.Client
	proofRepository *repository.ProofRepository
}

func NewProofService(
	proverClient prover.Client,
	proofRepository *repository.ProofRepository,
) *ProofService {
	return &ProofService{
		proverClient:    proverClient,
		proofRepository: proofRepository,
	}
}

func (s *ProofService) GenerateProof(ctx context.Context, req types.GenerateProofRequestBody) (types.GenerateProofResponse, error) {
	proofBundle, err := s.proverClient.GenerateProof(ctx, prover.GenerateProofInput{
		SettlementUpdate: req.SettlementUpdate,
		BatchCommitments: req.BatchCommitments,
		Witness:          req.Witness,
	})
	if err != nil {
		return types.GenerateProofResponse{}, err
	}

	s.proofRepository.SaveProofBundle(ctx, req.SettlementUpdate.BatchID, proofBundle)

	return types.GenerateProofResponse{
		ProofBundle: proofBundle,
		State: types.PartialState{
			ProofStatus: "ready",
		},
	}, nil
}
