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
	if s.offchainSettlementEnabled {
		if s.offchainSettlementService == nil {
			return types.WithdrawRequestResponse{}, ErrOffchainSettlementUnavailable
		}

		// Reserve a durable, restart-safe withdrawId from the store (P4 owns
		// identity). Sourcing the id from the same persistent sequence that
		// backs the withdraw_requests primary key prevents duplicate-key
		// collisions with rows persisted by earlier process runs.
		withdrawID, err := s.withdrawRepository.NextWithdrawID(ctx)
		if err != nil {
			return types.WithdrawRequestResponse{}, err
		}

		// STATE-04 (P3): build the request with a per-account nonce
		// (account.Nonce + 1), validating balance early. The nonce is sourced
		// from the same off-chain account state that ApplyWithdrawRequest
		// validates against — never a global counter. Identity (withdrawID)
		// comes from the durable store above, not from P3.
		withdrawReq, err := s.offchainSettlementService.BuildWithdrawRequest(req, withdrawID)
		if err != nil {
			return types.WithdrawRequestResponse{}, err
		}

		// STATE-05/14 (P3): validate nonce == account.Nonce+1 and debit the
		// pending balance. On failure the off-chain state is left untouched.
		if _, err := s.offchainSettlementService.ApplyWithdrawRequest(
			ctx,
			withdrawReq,
			withdrawDefaultUserSecret,
		); err != nil {
			return types.WithdrawRequestResponse{}, err
		}

		// Persist ONLY after a successful apply, so a rejected request never
		// leaves a phantom row in the store.
		withdrawReq, err = s.withdrawRepository.SaveWithdrawRequest(ctx, withdrawReq)
		if err != nil {
			return types.WithdrawRequestResponse{}, err
		}

		return types.WithdrawRequestResponse{
			WithdrawRequest: withdrawReq,
			State: types.PartialState{
				WithdrawStatus: "requested",
			},
		}, nil
	}

	// Legacy non-off-chain path: P4 assigns a sequence-based nonce. There is no
	// per-account off-chain state to sequence against in this mode.
	withdrawReq := s.withdrawRepository.CreateWithdrawRequest(ctx, req)

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

// ListWithdrawRecords returns the settled-withdrawal history (analog of
// DepositService.ListDeposits). It reads from the same durable store that backs
// GetWithdrawRecord / ClaimWithdraw, so claimed status is reflected.
func (s *WithdrawService) ListWithdrawRecords(ctx context.Context) types.ListWithdrawRecordsResponse {
	return types.ListWithdrawRecordsResponse{
		WithdrawRecords: s.withdrawRepository.ListWithdrawRecords(ctx),
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
