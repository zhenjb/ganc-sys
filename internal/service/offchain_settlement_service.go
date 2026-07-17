package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	appbatch "github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/repository"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

var ErrOffchainSettlementUnavailable = errors.New("offchain settlement service unavailable")
var ErrNoPendingSettlementOperations = errors.New("offchain settlement has no pending operations")
var ErrInvalidPendingSettlement = errors.New("offchain settlement pending operations invalid")

const offchainSettlementDefaultSecret = "mock-user-secret"

// OffchainSettlementService is the orchestration layer for P3's production
// off-chain settlement state.
//
// It connects:
// - internal/state.OffchainStateManager: in-memory pending state mirror
// - repository.OffchainSettlementRepository: durable pending settlement store
//
// P3INT-03 introduced apply/deposit/withdraw orchestration.
// P3INT-04 adds pending batch artifact construction:
//
// pending settlement transitions
// -> SettlementUpdate
// -> Witness
// -> BatchCommitments
// -> mark pending transitions as included
type OffchainSettlementService struct {
	manager    *appstate.OffchainStateManager
	repository *repository.OffchainSettlementRepository
	cursorName string

	settlementBuilder *appbatch.SettlementUpdateBuilder
	witnessBuilder    *appbatch.WitnessBuilder
}

func NewOffchainSettlementService(
	manager *appstate.OffchainStateManager,
	repo *repository.OffchainSettlementRepository,
) *OffchainSettlementService {
	if manager == nil {
		manager = appstate.NewOffchainStateManager()
	}

	return &OffchainSettlementService{
		manager:    manager,
		repository: repo,
		cursorName: repository.DefaultOffchainSettlementCursorName,
		// INT-2SEQ: đây là đường CORE (deposit/withdraw) settle trong pending mode —
		// nơi thực sự mint batchId khi BATCH_BUILD_SOURCE=pending (cấu hình live/DB).
		// Mang namespace "core-" để không đụng namespace "trade-" của đường trade
		// (RealOrderService) trên cùng chain.
		settlementBuilder: appbatch.NewSettlementUpdateBuilderWithPrefix("core-"),
		witnessBuilder:    appbatch.NewWitnessBuilder(),
	}
}

// BuildWithdrawRequest maps the API request body to a state.WithdrawIntent and
// delegates to the off-chain manager's STATE-04 builder, which derives the
// per-account nonce (account.Nonce + 1) from the same off-chain account state
// that ApplyWithdrawRequest validates against.
//
// withdrawID is supplied by the caller from the durable store (P4) so the id is
// unique across restarts; the manager only owns nonce, not identity.
func (s *OffchainSettlementService) BuildWithdrawRequest(
	req types.WithdrawRequestBody,
	withdrawID string,
) (types.WithdrawRequest, error) {
	return s.manager.BuildWithdrawRequest(appstate.WithdrawIntent{
		Owner:       req.Owner,
		Denom:       req.Denom,
		Amount:      req.Amount,
		Destination: req.Destination,
	}, withdrawID)
}

// RehydrateFromStore rebuilds the in-memory off-chain manager state from the
// durable pending-transition tables on startup.
//
// Why this exists: OffchainStateManager is in-memory and resets to genesis on
// every process start, but the offchain_pending_* tables (and their unique
// constraints on withdraw_id / nullifier) persist across restarts. Without
// rehydration the manager forgets prior balances/nonces/nullifiers, so the
// deterministic nullifier = Hash(secret, nonce) regenerates after a restart and
// collides with the persisted unique index (SQLSTATE 23505). Replaying the
// persisted pending deposits and withdrawals restores per-account balances,
// nonces, and consumed nullifiers so subsequent requests advance correctly and
// never reuse a value.
//
// Replay uses the manager's in-memory mutators directly (ApplyDeposit /
// ApplyWithdrawRequest) — NOT the persisting service wrappers — so it never
// re-writes the DB. Deposits are applied before withdrawals so balances exist
// before debits; withdrawals are replayed in persisted (created_at = nonce)
// order with their stored nullifier. Individual replay failures are logged and
// skipped so one corrupt legacy row cannot abort startup.
func (s *OffchainSettlementService) RehydrateFromStore(ctx context.Context) error {
	if s.repository == nil {
		return ErrOffchainSettlementUnavailable
	}

	deposits, err := s.repository.ListPendingDeposits(ctx)
	if err != nil {
		return fmt.Errorf("list pending deposits: %w", err)
	}
	withdrawals, err := s.repository.ListPendingWithdrawals(ctx)
	if err != nil {
		return fmt.Errorf("list pending withdrawals: %w", err)
	}

	var depApplied, depSkipped, wdApplied, wdSkipped int

	for _, d := range deposits {
		record := types.DepositRecord{
			DepositID: d.DepositID,
			Owner:     d.OwnerAddress,
			Denom:     d.Denom,
			Amount:    d.Amount,
		}
		if _, err := s.manager.ApplyDeposit(record); err != nil {
			depSkipped++
			log.Printf("[offchain-rehydrate] skip deposit %s: %v", d.DepositID, err)
			continue
		}
		depApplied++
	}

	for _, w := range withdrawals {
		req := types.WithdrawRequest{
			WithdrawID:  w.WithdrawID,
			Owner:       w.OwnerAddress,
			Denom:       w.Denom,
			Amount:      w.Amount,
			Destination: w.DestinationAddress,
			Nonce:       w.Nonce,
			Signature:   w.Signature,
		}
		if _, err := s.manager.ApplyWithdrawRequest(req, w.Nullifier); err != nil {
			wdSkipped++
			log.Printf("[offchain-rehydrate] skip withdrawal %s (nullifier=%s): %v", w.WithdrawID, w.Nullifier, err)
			continue
		}
		wdApplied++
	}

	log.Printf("[offchain-rehydrate] deposits applied=%d skipped=%d, withdrawals applied=%d skipped=%d",
		depApplied, depSkipped, wdApplied, wdSkipped)
	return nil
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

	if _, err := s.getOrInitCursorWithRoot(ctx, beforeSnapshot.Root()); err != nil {
		return repository.PendingDepositTransition{}, err
	}

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

	if strings.TrimSpace(userSecret) == "" {
		userSecret = offchainSettlementDefaultSecret
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

	if _, err := s.getOrInitCursorWithRoot(ctx, beforeSnapshot.Root()); err != nil {
		return repository.PendingWithdrawalTransition{}, err
	}

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

func (s *OffchainSettlementService) BuildPendingBatch(
	ctx context.Context,
	accountSecrets []appbatch.AccountSecret,
) (appbatch.BuildOutput, error) {
	if s.repository == nil {
		return appbatch.BuildOutput{}, ErrOffchainSettlementUnavailable
	}

	cursor, err := s.getOrInitCursor(ctx)
	if err != nil {
		return appbatch.BuildOutput{}, err
	}

	pendingDeposits, pendingWithdrawals, err := s.ListPendingSettlement(ctx)
	if err != nil {
		return appbatch.BuildOutput{}, err
	}

	if len(pendingDeposits) == 0 && len(pendingWithdrawals) == 0 {
		return appbatch.BuildOutput{}, ErrNoPendingSettlementOperations
	}

	orderedTransitions, err := orderPendingSettlementTransitions(
		cursor.CommittedRoot,
		pendingDeposits,
		pendingWithdrawals,
	)
	if err != nil {
		return appbatch.BuildOutput{}, err
	}

	// The gazk settlement circuit v1 proves exactly ONE withdrawal per batch:
	// a single per-account nonce binds the withdrawal nullifier
	// (NullifierFor(secret, account.Nonce)). If a batch carried two
	// withdrawals for the same account, the witness could only carry the last
	// nonce, so the prover would reject every earlier withdrawal with a
	// "nullifier mismatch". Bounding the batch to the ordered-chain prefix that
	// ends at the FIRST withdrawal keeps every emitted batch within that limit;
	// any remaining pending operations drain in subsequent sequencer passes.
	// Deposit-only chains take the whole prefix. NewStateRoot must therefore be
	// the prefix's final rootAfter — NOT cursor.PendingRoot, which reflects the
	// full (possibly larger) pending set.
	boundedTransitions := boundSingleWithdrawalPrefix(orderedTransitions)
	newStateRoot := boundedTransitions[len(boundedTransitions)-1].rootAfter

	selectedDeposits := make(map[string]bool)
	selectedWithdrawals := make(map[string]bool)
	for _, transition := range boundedTransitions {
		switch transition.kind {
		case "deposit":
			selectedDeposits[transition.id] = true
		case "withdrawal":
			selectedWithdrawals[transition.id] = true
		}
	}

	deposits := make([]types.DepositRecord, 0, len(selectedDeposits))
	depositIDs := make([]string, 0, len(selectedDeposits))
	for _, transition := range pendingDeposits {
		if !selectedDeposits[transition.DepositID] {
			continue
		}
		deposits = append(deposits, types.DepositRecord{
			DepositID: transition.DepositID,
			Owner:     transition.OwnerAddress,
			Denom:     transition.Denom,
			Amount:    transition.Amount,
			Processed: false,
		})
		depositIDs = append(depositIDs, transition.DepositID)
	}

	withdrawals := make([]appbatch.WithdrawalInput, 0, len(selectedWithdrawals))
	withdrawIDs := make([]string, 0, len(selectedWithdrawals))
	for _, transition := range pendingWithdrawals {
		if !selectedWithdrawals[transition.WithdrawID] {
			continue
		}
		withdrawals = append(withdrawals, appbatch.WithdrawalInput{
			Request: types.WithdrawRequest{
				WithdrawID:  transition.WithdrawID,
				Owner:       transition.OwnerAddress,
				Denom:       transition.Denom,
				Amount:      transition.Amount,
				Destination: transition.DestinationAddress,
				Nonce:       transition.Nonce,
				Signature:   transition.Signature,
			},
			Nullifier:       transition.Nullifier,
			DestinationHash: transition.DestinationHash,
		})
		withdrawIDs = append(withdrawIDs, transition.WithdrawID)
	}

	settlementInput := appbatch.SettlementInputs{
		OldStateRoot: cursor.CommittedRoot,
		NewStateRoot: newStateRoot,
		Deposits:     deposits,
		Withdrawals:  withdrawals,
	}

	settlementUpdate, err := s.settlementBuilder.Build(settlementInput)
	if err != nil {
		return appbatch.BuildOutput{}, err
	}

	witnessAccounts, err := buildWitnessAccountsFromPendingTransitions(
		boundedTransitions,
		accountSecrets,
	)
	if err != nil {
		return appbatch.BuildOutput{}, err
	}

	witness, err := s.witnessBuilder.Build(appbatch.WitnessInputs{
		Settlement: settlementInput,
		Accounts:   witnessAccounts,
	})
	if err != nil {
		return appbatch.BuildOutput{}, err
	}

	commitments := appbatch.BuildCommitments(settlementUpdate)

	if err := s.repository.MarkIncluded(
		ctx,
		settlementUpdate.BatchID,
		depositIDs,
		withdrawIDs,
	); err != nil {
		return appbatch.BuildOutput{}, err
	}

	return appbatch.BuildOutput{
		SettlementUpdate: settlementUpdate,
		BatchCommitments: commitments,
		Witness:          witness,
	}, nil
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

func (s *OffchainSettlementService) CancelIncludedBatch(
	ctx context.Context,
	batchID string,
	reason string,
) error {
	if s.repository == nil {
		return ErrOffchainSettlementUnavailable
	}

	return s.repository.ReopenIncluded(ctx, batchID, reason)
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
	return s.getOrInitCursorWithRoot(ctx, s.manager.Root())
}

func (s *OffchainSettlementService) getOrInitCursorWithRoot(
	ctx context.Context,
	root string,
) (repository.OffchainStateCursor, error) {
	cursor, err := s.repository.GetCursor(ctx, s.cursorName)
	if err == nil {
		return cursor, nil
	}

	if !errors.Is(err, repository.ErrOffchainSettlementRecordNotFound) {
		return repository.OffchainStateCursor{}, err
	}

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

type pendingSettlementTransition struct {
	kind string

	id           string
	ownerAddress string
	denom        string

	rootBefore    string
	rootAfter     string
	balanceBefore string
	balanceAfter  string
}

// boundSingleWithdrawalPrefix returns the longest prefix of an ordered
// transition chain that contains at most ONE withdrawal — the prefix up to and
// including the first withdrawal. A deposit-only chain returns unchanged. This
// enforces the gazk settlement circuit v1 limit of one withdrawal per batch;
// the caller settles the prefix now and drains the remainder in later passes.
//
// The input MUST be non-empty (BuildPendingBatch guarantees this by rejecting
// empty pending sets before ordering).
func boundSingleWithdrawalPrefix(ordered []pendingSettlementTransition) []pendingSettlementTransition {
	for i, transition := range ordered {
		if transition.kind == "withdrawal" {
			return ordered[:i+1]
		}
	}
	return ordered
}

func orderPendingSettlementTransitions(
	committedRoot string,
	deposits []repository.PendingDepositTransition,
	withdrawals []repository.PendingWithdrawalTransition,
) ([]pendingSettlementTransition, error) {
	byRootBefore := make(map[string]pendingSettlementTransition, len(deposits)+len(withdrawals))

	for _, deposit := range deposits {
		transition := pendingSettlementTransition{
			kind: "deposit",

			id:           deposit.DepositID,
			ownerAddress: deposit.OwnerAddress,
			denom:        deposit.Denom,

			rootBefore:    deposit.RootBefore,
			rootAfter:     deposit.RootAfter,
			balanceBefore: deposit.BalanceBefore,
			balanceAfter:  deposit.BalanceAfter,
		}

		if err := addPendingTransition(byRootBefore, transition); err != nil {
			return nil, err
		}
	}

	for _, withdrawal := range withdrawals {
		transition := pendingSettlementTransition{
			kind: "withdrawal",

			id:           withdrawal.WithdrawID,
			ownerAddress: withdrawal.OwnerAddress,
			denom:        withdrawal.Denom,

			rootBefore:    withdrawal.RootBefore,
			rootAfter:     withdrawal.RootAfter,
			balanceBefore: withdrawal.BalanceBefore,
			balanceAfter:  withdrawal.BalanceAfter,
		}

		if err := addPendingTransition(byRootBefore, transition); err != nil {
			return nil, err
		}
	}

	ordered := make([]pendingSettlementTransition, 0, len(byRootBefore))
	currentRoot := committedRoot

	for len(ordered) < len(byRootBefore) {
		next, ok := byRootBefore[currentRoot]
		if !ok {
			return nil, fmt.Errorf(
				"%w: cannot continue transition chain from root %s",
				ErrInvalidPendingSettlement,
				currentRoot,
			)
		}

		ordered = append(ordered, next)
		currentRoot = next.rootAfter
	}

	return ordered, nil
}

func addPendingTransition(
	byRootBefore map[string]pendingSettlementTransition,
	transition pendingSettlementTransition,
) error {
	if strings.TrimSpace(transition.rootBefore) == "" {
		return fmt.Errorf(
			"%w: %s %s rootBefore is empty",
			ErrInvalidPendingSettlement,
			transition.kind,
			transition.id,
		)
	}

	if strings.TrimSpace(transition.rootAfter) == "" {
		return fmt.Errorf(
			"%w: %s %s rootAfter is empty",
			ErrInvalidPendingSettlement,
			transition.kind,
			transition.id,
		)
	}

	if _, exists := byRootBefore[transition.rootBefore]; exists {
		return fmt.Errorf(
			"%w: duplicate transition rootBefore %s",
			ErrInvalidPendingSettlement,
			transition.rootBefore,
		)
	}

	byRootBefore[transition.rootBefore] = transition

	return nil
}

type pendingOwnerWitnessBalance struct {
	owner string
	denom string

	oldBalance string
	newBalance string
}

func buildWitnessAccountsFromPendingTransitions(
	orderedTransitions []pendingSettlementTransition,
	accountSecrets []appbatch.AccountSecret,
) ([]appbatch.AccountWitnessSecret, error) {
	secrets, err := indexAccountSecrets(accountSecrets)
	if err != nil {
		return nil, err
	}

	ownerOrder := make([]string, 0)
	byOwner := make(map[string]pendingOwnerWitnessBalance)

	for _, transition := range orderedTransitions {
		owner := strings.TrimSpace(transition.ownerAddress)
		denom := strings.TrimSpace(transition.denom)

		if owner == "" {
			return nil, fmt.Errorf("%w: transition owner is empty", ErrInvalidPendingSettlement)
		}

		if denom == "" {
			return nil, fmt.Errorf("%w: transition denom is empty", ErrInvalidPendingSettlement)
		}

		current, exists := byOwner[owner]
		if !exists {
			ownerOrder = append(ownerOrder, owner)
			byOwner[owner] = pendingOwnerWitnessBalance{
				owner: owner,
				denom: denom,

				oldBalance: transition.balanceBefore,
				newBalance: transition.balanceAfter,
			}
			continue
		}

		if current.denom != denom {
			return nil, fmt.Errorf(
				"%w: owner %s appears with multiple denoms: %s and %s",
				ErrInvalidPendingSettlement,
				owner,
				current.denom,
				denom,
			)
		}

		current.newBalance = transition.balanceAfter
		byOwner[owner] = current
	}

	accounts := make([]appbatch.AccountWitnessSecret, 0, len(ownerOrder))
	for _, owner := range ownerOrder {
		balance := byOwner[owner]

		accounts = append(accounts, appbatch.AccountWitnessSecret{
			Owner:      owner,
			UserSecret: resolveAccountSecret(secrets, owner),
			OldBalance: balance.oldBalance,
			NewBalance: balance.newBalance,
		})
	}

	return accounts, nil
}

func indexAccountSecrets(accountSecrets []appbatch.AccountSecret) (map[string]string, error) {
	out := make(map[string]string, len(accountSecrets))

	for i, secret := range accountSecrets {
		owner := strings.TrimSpace(secret.Owner)
		userSecret := strings.TrimSpace(secret.UserSecret)

		if owner == "" {
			return nil, fmt.Errorf("%w: accountSecrets[%d].owner is empty", ErrInvalidPendingSettlement, i)
		}

		if userSecret == "" {
			return nil, fmt.Errorf("%w: accountSecrets[%d].userSecret is empty", ErrInvalidPendingSettlement, i)
		}

		if _, exists := out[owner]; exists {
			return nil, fmt.Errorf("%w: accountSecrets[%d] duplicate owner %s", ErrInvalidPendingSettlement, i, owner)
		}

		out[owner] = userSecret
	}

	return out, nil
}

func resolveAccountSecret(secrets map[string]string, owner string) string {
	if secret, ok := secrets[owner]; ok {
		return secret
	}

	return offchainSettlementDefaultSecret
}
