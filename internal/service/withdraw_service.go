package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// WithdrawService owns withdrawal request and claim use cases.
//
// INT-06 status:
// - CreateWithdrawRequest creates a real local request from input.
// - The request is saved in MemoryStore.
// - P3/P5 can query saved requests.
//
// Still local/stubbed:
// - signature is local deterministic placeholder.
// - off-chain balance debit is not implemented here.
// - nullifier/withdraw output generation belongs to batch building.
// - claim withdraw is still local fixture until MsgClaimWithdraw integration.
type WithdrawService struct {
	withdrawRepository *repository.WithdrawRepository
}

func NewWithdrawService(withdrawRepository *repository.WithdrawRepository) *WithdrawService {
	return &WithdrawService{
		withdrawRepository: withdrawRepository,
	}
}

func (s *WithdrawService) CreateWithdrawRequest(ctx context.Context, req types.WithdrawRequestBody) types.WithdrawRequestResponse {
	withdrawReq := s.withdrawRepository.CreateWithdrawRequest(ctx, req)

	return types.WithdrawRequestResponse{
		WithdrawRequest: withdrawReq,
		State: types.PartialState{
			WithdrawStatus: "requested",
		},
	}
}

func (s *WithdrawService) ListWithdrawRequests(ctx context.Context) types.ListWithdrawRequestsResponse {
	return types.ListWithdrawRequestsResponse{
		WithdrawRequests: s.withdrawRepository.ListWithdrawRequests(ctx),
	}
}

func (s *WithdrawService) GetWithdrawRequest(ctx context.Context, withdrawID string) (types.GetWithdrawRequestResponse, error) {
	request, err := s.withdrawRepository.GetWithdrawRequest(ctx, withdrawID)
	if err != nil {
		return types.GetWithdrawRequestResponse{}, err
	}

	return types.GetWithdrawRequestResponse{
		WithdrawRequest: request,
	}, nil
}

func (s *WithdrawService) ClaimWithdraw(ctx context.Context, req types.ClaimWithdrawRequestBody) types.ClaimWithdrawResponse {
	// TODO(INT-10):
	// Submit MsgClaimWithdraw(req.WithdrawID), then return indexed chain result.
	withdrawRecord := s.withdrawRepository.GetLocalWithdrawRecord(ctx, true)
	balances := s.withdrawRepository.GetLocalClaimBalanceSnapshot(ctx)

	return types.ClaimWithdrawResponse{
		TxHash:         "0xmockclaimwithdraw",
		WithdrawRecord: withdrawRecord,
		Balances:       balances,
		State: types.PartialState{
			WithdrawStatus: "claimed",
		},
	}
}
