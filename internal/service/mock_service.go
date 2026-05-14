package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

type MockService struct {
	mockRepository *repository.MockRepository
	chainClient    chain.Client
}

func NewMockService(
	mockRepository *repository.MockRepository,
	chainClient chain.Client,
) *MockService {
	return &MockService{
		mockRepository: mockRepository,
		chainClient:    chainClient,
	}
}

func (s *MockService) GetState(ctx context.Context) types.AppState {
	return s.mockRepository.GetState(ctx)
}

func (s *MockService) MockDeposit(ctx context.Context, req types.DepositRequestBody) (types.DepositResponse, error) {
	result, err := s.chainClient.Deposit(ctx, chain.DepositRequest{
		Owner:  req.Owner,
		Denom:  req.Denom,
		Amount: req.Amount,
	})
	if err != nil {
		return types.DepositResponse{}, err
	}

	return types.DepositResponse{
		TxHash:        result.TxHash,
		DepositRecord: result.DepositRecord,
		State: types.PartialState{
			CurrentStateRoot: "0xrootA",
			DepositStatus:    "locked",
			ProofStatus:      "idle",
			WithdrawStatus:   "none",
		},
	}, nil
}

func (s *MockService) MockWithdrawRequest(ctx context.Context, req types.WithdrawRequestBody) types.WithdrawRequestResponse {
	withdrawReq := s.mockRepository.GetWithdrawRequest(ctx)

	return types.WithdrawRequestResponse{
		WithdrawRequest: withdrawReq,
		State: types.PartialState{
			WithdrawStatus: "requested",
		},
	}
}

func (s *MockService) MockBuildBatch(ctx context.Context, req types.BuildBatchRequestBody) types.BuildBatchResponse {
	return types.BuildBatchResponse{
		SettlementUpdate: s.mockRepository.GetSettlementUpdate(ctx),
		Witness:          s.mockRepository.GetWitness(ctx),
		State: types.PartialState{
			ProofStatus:    "idle",
			WithdrawStatus: "batchBuilt",
		},
	}
}

func (s *MockService) MockGenerateProof(ctx context.Context, req types.GenerateProofRequestBody) types.GenerateProofResponse {
	return types.GenerateProofResponse{
		ProofBundle: s.mockRepository.GetProofBundle(ctx),
		State: types.PartialState{
			ProofStatus: "ready",
		},
	}
}

func (s *MockService) MockSubmitBatch(ctx context.Context, req types.SubmitBatchRequestBody) types.SubmitBatchResponse {
	settlement := s.mockRepository.GetSettlementUpdate(ctx)
	withdrawRecord := s.mockRepository.GetWithdrawRecord(ctx, false)

	return types.SubmitBatchResponse{
		TxHash:           "0xmocksubmitbatch",
		Accepted:         true,
		ProofStatus:      "accepted",
		SettlementUpdate: settlement,
		WithdrawRecord:   withdrawRecord,
		State: types.PartialState{
			CurrentStateRoot: "0xrootB",
			DepositStatus:    "processed",
			ProofStatus:      "accepted",
			WithdrawStatus:   "readyToClaim",
		},
	}
}

func (s *MockService) MockClaimWithdraw(ctx context.Context, req types.ClaimWithdrawRequestBody) types.ClaimWithdrawResponse {
	withdrawRecord := s.mockRepository.GetWithdrawRecord(ctx, true)

	return types.ClaimWithdrawResponse{
		TxHash:         "0xmockclaimwithdraw",
		WithdrawRecord: withdrawRecord,
		Balances: map[string]string{
			"cosmos1alice/uusdc":  "940",
			"moduleAccount/uusdc": "60",
		},
		State: types.PartialState{
			WithdrawStatus: "claimed",
		},
	}
}
