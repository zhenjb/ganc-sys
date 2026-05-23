package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// WithdrawService owns withdrawal request and claim use cases.
//
// INT-04 status:
// - CreateWithdrawRequest returns a local deterministic request.
// - ClaimWithdraw returns a local deterministic claimed record.
// - No persisted withdrawal request store exists yet.
// - No MsgClaimWithdraw chain integration exists yet.
//
// TODO(INT-06):
// Create and persist real withdrawal requests from user input.
//
// TODO(INT-10 / P1):
// Claim withdrawal by submitting MsgClaimWithdraw to the chain,
// then query/index final withdrawal record and balances.
type WithdrawService struct {
	withdrawRepository *repository.WithdrawRepository
}

func NewWithdrawService(withdrawRepository *repository.WithdrawRepository) *WithdrawService {
	return &WithdrawService{
		withdrawRepository: withdrawRepository,
	}
}

func (s *WithdrawService) CreateWithdrawRequest(ctx context.Context, req types.WithdrawRequestBody) types.WithdrawRequestResponse {
	// TODO(INT-06):
	// Use req.Owner, req.Denom, req.Amount, req.Destination to create a real
	// withdraw request with nonce/signature and persist it.
	withdrawReq := s.withdrawRepository.GetLocalWithdrawRequest(ctx)

	return types.WithdrawRequestResponse{
		WithdrawRequest: withdrawReq,
		State: types.PartialState{
			WithdrawStatus: "requested",
		},
	}
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
