package service

import (
	"context"
	"errors"

	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const withdrawDefaultUserSecret = "mock-user-secret"

// WithdrawService owns withdrawal request and claim use cases.
//
// INT-10 status:
// - CreateWithdrawRequest creates and persists local withdraw requests.
// - ClaimWithdraw reads submitted withdrawRecords from store.
// - ClaimWithdraw calls relayer.Client boundary.
// - Claimed records are persisted back to store.
//
// P3INT-07:
//   - When off-chain settlement is enabled, CreateWithdrawRequest also applies
//     the request to OffchainSettlementService.
//   - That path derives nullifier/destinationHash, checks pending balance,
//     debits pending balance, and persists offchain_pending_withdrawals.
//
// Still local/stubbed:
// - relayer.LocalClient does not transfer real funds.
// - userSecret is still the MVP mock secret until P5/wallet integration.
type WithdrawService struct {
	withdrawRepository *repository.WithdrawRepository
	relayerClient      relayer.Client

	offchainSettlementEnabled bool
	offchainSettlementService *OffchainSettlementService
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

func NewWithdrawServiceWithOffchainSettlement(
	withdrawRepository *repository.WithdrawRepository,
	relayerClient relayer.Client,
	offchainSettlementEnabled bool,
	offchainSettlementService *OffchainSettlementService,
) *WithdrawService {
	return &WithdrawService{
		withdrawRepository:        withdrawRepository,
		relayerClient:             relayerClient,
		offchainSettlementEnabled: offchainSettlementEnabled,
		offchainSettlementService: offchainSettlementService,
	}
}

func (s *WithdrawService) CreateWithdrawRequest(
	ctx context.Context,
	req types.WithdrawRequestBody,
) (types.WithdrawRequestResponse, error) {
	withdrawReq := s.withdrawRepository.CreateWithdrawRequest(ctx, req)

	if s.offchainSettlementEnabled {
		if s.offchainSettlementService == nil {
			return types.WithdrawRequestResponse{}, ErrOffchainSettlementUnavailable
		}

		_, err := s.offchainSettlementService.ApplyWithdrawRequest(
			ctx,
			withdrawReq,
			withdrawDefaultUserSecret,
		)
		if err != nil {
			return types.WithdrawRequestResponse{}, err
		}
	}

	return types.WithdrawRequestResponse{
		WithdrawRequest: withdrawReq,
		State: types.PartialState{
			WithdrawStatus: "requested",
		},
	}, nil
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

func IsWithdrawInsufficientBalanceError(err error) bool {
	return errors.Is(err, appstate.ErrInsufficientBalance)
}
