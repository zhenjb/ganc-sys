package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// WithdrawService owns withdrawal request and claim use cases.
//
// INT-10 status:
// - CreateWithdrawRequest creates and persists local withdraw requests.
// - ClaimWithdraw now reads submitted withdrawRecords from store.
// - ClaimWithdraw calls relayer.Client boundary.
// - Claimed records are persisted back to store.
//
// Still local/stubbed:
// - relayer.LocalClient does not transfer real funds.
// - balances are local deterministic snapshots.
type WithdrawService struct {
	withdrawRepository *repository.WithdrawRepository
	relayerClient      relayer.Client
}

func NewWithdrawService(
	withdrawRepository *repository.WithdrawRepository,
	relayerClient relayer.Client,
) *WithdrawService {
	return &WithdrawService{
		withdrawRepository: withdrawRepository,
		relayerClient:      relayerClient,
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

func (s *WithdrawService) ClaimWithdraw(ctx context.Context, req types.ClaimWithdrawRequestBody) (types.ClaimWithdrawResponse, error) {
	record, err := s.withdrawRepository.GetWithdrawRecord(ctx, req.WithdrawID)
	if err != nil {
		return types.ClaimWithdrawResponse{}, err
	}

	claimResult, err := s.relayerClient.ClaimWithdraw(ctx, relayer.ClaimWithdrawInput{
		WithdrawRecord: record,
	})
	if err != nil {
		return types.ClaimWithdrawResponse{}, err
	}

	claimedRecord, err := s.withdrawRepository.ClaimWithdrawRecord(ctx, claimResult.WithdrawRecord.WithdrawID)
	if err != nil {
		return types.ClaimWithdrawResponse{}, err
	}

	balances := s.withdrawRepository.GetLocalClaimBalanceSnapshot(ctx, claimedRecord)

	return types.ClaimWithdrawResponse{
		TxHash:         claimResult.TxHash,
		WithdrawRecord: claimedRecord,
		Balances:       balances,
		State: types.PartialState{
			WithdrawStatus: "claimed",
		},
	}, nil
}
