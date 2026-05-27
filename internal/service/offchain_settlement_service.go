package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/zhenjb/ganc-sys/internal/repository"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

var ErrOffchainSettlementUnavailable = errors.New("offchain settlement service unavailable")

// OffchainSettlementService is the orchestration layer for P3's production
// off-chain settlement state.
//
// It connects:
// - internal/state.OffchainStateManager: in-memory pending state mirror
// - repository.OffchainSettlementRepository: durable pending settlement store
//
// P3INT-03 only introduces the service and tests it directly.
// API wiring happens later.
type OffchainSettlementService struct {
	manager    *appstate.OffchainStateManager
	repository *repository.OffchainSettlementRepository
	cursorName string
}

func NewOffchainSettlementService(
	manager *appstate.OffchainStateManager,
	repository *repository.OffchainSettlementRepository,
) *OffchainSettlementService {
	if manager == nil {
		manager = appstate.NewOffchainStateManager()
	}

	return &OffchainSettlementService{
		manager:    manager,
		repository: repository,
		cursorName: repositoryDefaultCursorName(),
	}
}

func (s *OffchainSettlementService) ApplyIndexedDeposit(
	ctx context.Context,
	deposit types.DepositRecord,
) (repository.PendingDepositTransition, error) {
	if s.repository == nil {
		return repository.PendingDepositTransition{}, ErrOffchainSettlementUnavailable
	}

	beforeSnapshot := s.manager.Snapshot()
	beforeAccount := beforeSnapshot.Account(deposit.Owner, deposit.Denom)

	rootAfter, err := s.manager.ApplyDeposit(deposit)
	if err != nil {
		return repository.PendingDepositTransition{}, err
	}

	afterSnapshot := s.manager.Snapshot()
	afterAccount := afterSnapshot.Account(deposit.Owner, deposit.Denom)

	transition := repository.PendingDepositTransition{
		DepositID: deposit.DepositID,

		OwnerAddress: deposit.Owner,
		Denom:        deposit.Denom,
		Amount:       deposit.Amount,

		RootBefore:    beforeSnapshot.Root(),
		RootAfter:     rootAfter,
		BalanceBefore: beforeAccount.Balance,
		BalanceAfter:  afterAccount.Balance,

		Status: repository.OffchainSettlementStatusPending,
	}

	if err := s.repository.SavePendingDeposit(ctx, transition); err != nil {
		return repository.PendingDepositTransition{}, err
	}

	if err := s.updatePendingRoot(ctx, afterSnapshot.Root()); err != nil {
		return repository.PendingDepositTransition{}, err
	}

	return transition, nil
}

func (s *OffchainSettlementService) ApplyWithdrawRequest(
	ctx context.Context,
	req types.WithdrawRequest,
	userSecret string,
) (repository.PendingWithdrawalTransition, error) {
	if s.repository == nil {
		return repository.PendingWithdrawalTransition{}, ErrOffchainSettlementUnavailable
	}

	nullifier, err := appstate.NullifierFor(userSecret, req.Nonce)
	if err != nil {
		return repository.PendingWithdrawalTransition{}, fmt.Errorf("derive nullifier: %w", err)
	}

	destinationHash, err := appstate.WithdrawAddressHash(req.Destination)
	if err != nil {
		return repository.PendingWithdrawalTransition{}, fmt.Errorf("derive destination hash: %w", err)
	}

	beforeSnapshot := s.manager.Snapshot()
	beforeAccount := beforeSnapshot.Account(req.Owner, req.Denom)

	rootAfter, err := s.manager.ApplyWithdrawRequest(req, nullifier)
	if err != nil {
		return repository.PendingWithdrawalTransition{}, err
	}

	afterSnapshot := s.manager.Snapshot()
	afterAccount := afterSnapshot.Account(req.Owner, req.Denom)

	transition := repository.PendingWithdrawalTransition{
		WithdrawID: req.WithdrawID,

		OwnerAddress:       req.Owner,
		Denom:              req.Denom,
		Amount:             req.Amount,
		DestinationAddress: req.Destination,
		Nonce:              req.Nonce,
		Signature:          req.Signature,
		Nullifier:          nullifier,
		DestinationHash:    destinationHash,

		RootBefore:    beforeSnapshot.Root(),
		RootAfter:     rootAfter,
		BalanceBefore: beforeAccount.Balance,
		BalanceAfter:  afterAccount.Balance,

		Status: repository.OffchainSettlementStatusPending,
	}

	if err := s.repository.SavePendingWithdrawal(ctx, transition); err != nil {
		return repository.PendingWithdrawalTransition{}, err
	}

	if err := s.updatePendingRoot(ctx, afterSnapshot.Root()); err != nil {
		return repository.PendingWithdrawalTransition{}, err
	}

	return transition, nil
}

func (s *OffchainSettlementService) ListPendingSettlement(
	ctx context.Context,
) ([]repository.PendingDepositTransition, []repository.PendingWithdrawalTransition, error) {
	if s.repository == nil {
		return nil, nil, ErrOffchainSettlementUnavailable
	}

	deposits, err := s.repository.ListPendingDeposits(ctx)
	if err != nil {
		return nil, nil, err
	}

	withdrawals, err := s.repository.ListPendingWithdrawals(ctx)
	if err != nil {
		return nil, nil, err
	}

	return deposits, withdrawals, nil
}

func (s *OffchainSettlementService) MarkIncluded(
	ctx context.Context,
	batchID string,
	depositIDs []string,
	withdrawIDs []string,
) error {
	if s.repository == nil {
		return ErrOffchainSettlementUnavailable
	}

	return s.repository.MarkIncluded(ctx, batchID, depositIDs, withdrawIDs)
}

func (s *OffchainSettlementService) CommitBatch(
	ctx context.Context,
	batchID string,
	txHash string,
	newCommittedRoot string,
) error {
	if s.repository == nil {
		return ErrOffchainSettlementUnavailable
	}

	if err := s.repository.MarkCommitted(ctx, batchID, txHash); err != nil {
		return err
	}

	cursor, err := s.getOrInitCursor(ctx)
	if err != nil {
		return err
	}

	cursor.CommittedRoot = newCommittedRoot
	cursor.PendingRoot = newCommittedRoot
	cursor.LastCommittedBatchID = batchID

	return s.repository.UpsertCursor(ctx, cursor)
}

func (s *OffchainSettlementService) FailBatch(
	ctx context.Context,
	batchID string,
	reason string,
) error {
	if s.repository == nil {
		return ErrOffchainSettlementUnavailable
	}

	return s.repository.MarkFailed(ctx, batchID, reason)
}

func (s *OffchainSettlementService) Cursor(ctx context.Context) (repository.OffchainStateCursor, error) {
	if s.repository == nil {
		return repository.OffchainStateCursor{}, ErrOffchainSettlementUnavailable
	}

	return s.getOrInitCursor(ctx)
}

func (s *OffchainSettlementService) ManagerRoot() string {
	return s.manager.Root()
}

func (s *OffchainSettlementService) ManagerAccount(owner string, denom string) types.Account {
	return s.manager.Account(owner, denom)
}

func (s *OffchainSettlementService) updatePendingRoot(ctx context.Context, pendingRoot string) error {
	cursor, err := s.getOrInitCursor(ctx)
	if err != nil {
		return err
	}

	cursor.PendingRoot = pendingRoot

	return s.repository.UpsertCursor(ctx, cursor)
}

func (s *OffchainSettlementService) getOrInitCursor(ctx context.Context) (repository.OffchainStateCursor, error) {
	cursor, err := s.repository.GetCursor(ctx, s.cursorName)
	if err == nil {
		return cursor, nil
	}

	if !errors.Is(err, repository.ErrOffchainSettlementRecordNotFound) {
		return repository.OffchainStateCursor{}, err
	}

	root := s.manager.Root()

	cursor = repository.OffchainStateCursor{
		Name:          s.cursorName,
		CommittedRoot: root,
		PendingRoot:   root,
	}

	if err := s.repository.UpsertCursor(ctx, cursor); err != nil {
		return repository.OffchainStateCursor{}, err
	}

	return cursor, nil
}

func repositoryDefaultCursorName() string {
	return repository.DefaultOffchainSettlementCursorName
}
