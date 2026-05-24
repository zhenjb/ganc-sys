package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/prover"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ProofService owns proof generation orchestration.
//
// P4 owns this service as integration glue.
// P2 owns the actual prover implementation behind prover.Client.
type ProofService struct {
	proverClient prover.Client
}

func NewProofService(proverClient prover.Client) *ProofService {
	return &ProofService{
		proverClient: proverClient,
	}
}

func (s *ProofService) GenerateProof(ctx context.Context, req types.GenerateProofRequestBody) (types.GenerateProofResponse, error) {
	// P4 integration point:
	// This calls the P2 prover interface.
	// Today this is wired to prover.LocalClient.
	// Later it should be replaced with P2's real prover implementation.
	proofBundle, err := s.proverClient.GenerateProof(ctx, prover.GenerateProofInput{
		SettlementUpdate: req.SettlementUpdate,
		BatchCommitments: req.BatchCommitments,
		Witness:          req.Witness,
	})
	if err != nil {
		return types.GenerateProofResponse{}, err
	}

	return types.GenerateProofResponse{
		ProofBundle: proofBundle,
		State: types.PartialState{
			ProofStatus: "ready",
		},
	}, nil
}
