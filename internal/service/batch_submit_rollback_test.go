package service

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// configurableRelayer lets a test force an accepted / rejected / errored submit
// so the STATE-14 rollback wiring can be exercised without a chain.
type configurableRelayer struct {
	accepted bool
	err      error
}

func (r *configurableRelayer) SubmitBatch(ctx context.Context, in relayer.SubmitBatchInput) (relayer.SubmitBatchResult, error) {
	if r.err != nil {
		return relayer.SubmitBatchResult{}, r.err
	}
	status := "accepted"
	if !r.accepted {
		status = "rejected"
	}
	return relayer.SubmitBatchResult{TxHash: "0xtx", Accepted: r.accepted, ProofStatus: status}, nil
}

func (r *configurableRelayer) ClaimWithdraw(ctx context.Context, in relayer.ClaimWithdrawInput) (relayer.ClaimWithdrawResult, error) {
	return relayer.ClaimWithdrawResult{}, nil
}

func newRollbackBatchService(rel relayer.Client, manager *appstate.OffchainStateManager) *BatchService {
	memoryStore := store.NewMemoryStore()
	svc := NewBatchService(
		repository.NewBatchRepository(memoryStore),
		repository.NewDepositRepository(memoryStore),
		repository.NewWithdrawRepository(memoryStore),
		batch.NewSnapshotBuilder(manager),
		rel,
	)
	svc.SetStateRollback(manager)
	return svc
}

// applyPendingDeposit credits the manager and returns the resulting pending
// balance, mimicking the deposit indexer having mirrored an on-chain deposit.
func applyPendingDeposit(t *testing.T, m *appstate.OffchainStateManager, owner, denom, amount, id string) {
	t.Helper()
	if _, err := m.ApplyDeposit(types.DepositRecord{
		DepositID: id, Owner: owner, Denom: denom, Amount: amount,
	}); err != nil {
		t.Fatalf("apply deposit: %v", err)
	}
}

func TestSubmitBatchRollsBackPendingStateWhenRelayerErrors(t *testing.T) {
	ctx := context.Background()
	manager := appstate.NewOffchainStateManager()

	// Baseline captured at construction = empty (genesis).
	svc := newRollbackBatchService(&configurableRelayer{err: errors.New("chain unreachable")}, manager)

	// A deposit is mirrored into pending state AFTER the baseline.
	applyPendingDeposit(t, manager, "cosmos1alice", "uusdc", "100", "dep-1")
	if got := manager.Account("cosmos1alice", "uusdc").Balance; got != "100" {
		t.Fatalf("precondition: expected pending balance 100, got %q", got)
	}

	_, err := svc.SubmitBatch(ctx, sampleSubmitRequest())
	if err == nil {
		t.Fatalf("expected submit error")
	}

	// Submit failed => pending state restored to the baseline => not stuck.
	if got := manager.Account("cosmos1alice", "uusdc").Balance; got != "0" {
		t.Fatalf("expected pending balance rolled back to 0, got %q", got)
	}
}

func TestSubmitBatchRollsBackWhenProofVerifierFails(t *testing.T) {
	ctx := context.Background()
	manager := appstate.NewOffchainStateManager()
	svc := newRollbackBatchService(&configurableRelayer{accepted: true}, manager)
	svc.SetProofVerifier(&stubVerifier{err: errors.New("invalid proof")})

	applyPendingDeposit(t, manager, "cosmos1alice", "uusdc", "100", "dep-1")

	if _, err := svc.SubmitBatch(ctx, sampleSubmitRequest()); err == nil {
		t.Fatalf("expected verification error")
	}

	if got := manager.Account("cosmos1alice", "uusdc").Balance; got != "0" {
		t.Fatalf("expected rollback to 0 on verify failure, got %q", got)
	}
}

func TestSubmitBatchRollsBackWhenChainRejects(t *testing.T) {
	ctx := context.Background()
	manager := appstate.NewOffchainStateManager()
	svc := newRollbackBatchService(&configurableRelayer{accepted: false}, manager)

	applyPendingDeposit(t, manager, "cosmos1alice", "uusdc", "100", "dep-1")

	if _, err := svc.SubmitBatch(ctx, sampleSubmitRequest()); err != nil {
		t.Fatalf("rejected (not accepted) submit should not return a transport error: %v", err)
	}

	if got := manager.Account("cosmos1alice", "uusdc").Balance; got != "0" {
		t.Fatalf("expected rollback to 0 when chain rejects, got %q", got)
	}
}

func TestSubmitBatchAdvancesBaselineOnAcceptThenRollsBackToIt(t *testing.T) {
	ctx := context.Background()
	manager := appstate.NewOffchainStateManager()
	rel := &configurableRelayer{accepted: true}
	svc := newRollbackBatchService(rel, manager)

	// First batch: deposit committed via an accepted submit -> baseline advances.
	applyPendingDeposit(t, manager, "cosmos1alice", "uusdc", "100", "dep-1")
	if _, err := svc.SubmitBatch(ctx, sampleSubmitRequest()); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if got := manager.Account("cosmos1alice", "uusdc").Balance; got != "100" {
		t.Fatalf("expected committed balance 100 after accept, got %q", got)
	}

	// Second batch: another deposit, but this submit is rejected.
	rel.accepted = false
	applyPendingDeposit(t, manager, "cosmos1alice", "uusdc", "40", "dep-2")
	if got := manager.Account("cosmos1alice", "uusdc").Balance; got != "140" {
		t.Fatalf("precondition: expected pending 140, got %q", got)
	}

	if _, err := svc.SubmitBatch(ctx, sampleSubmitRequest()); err != nil {
		t.Fatalf("second submit: %v", err)
	}

	// Rolls back to the advanced baseline (100), NOT to genesis (0): the first
	// deposit stays committed, only the rejected batch's deposit is reverted.
	if got := manager.Account("cosmos1alice", "uusdc").Balance; got != "100" {
		t.Fatalf("expected rollback to advanced baseline 100, got %q", got)
	}
}

func TestSubmitBatchNoRollbackTargetLeavesStateUntouched(t *testing.T) {
	ctx := context.Background()
	manager := appstate.NewOffchainStateManager()

	// No SetStateRollback: the manager must not be touched by submit at all.
	memoryStore := store.NewMemoryStore()
	svc := NewBatchService(
		repository.NewBatchRepository(memoryStore),
		repository.NewDepositRepository(memoryStore),
		repository.NewWithdrawRepository(memoryStore),
		batch.NewSnapshotBuilder(manager),
		&configurableRelayer{err: errors.New("boom")},
	)

	applyPendingDeposit(t, manager, "cosmos1alice", "uusdc", "100", "dep-1")

	if _, err := svc.SubmitBatch(ctx, sampleSubmitRequest()); err == nil {
		t.Fatalf("expected submit error")
	}

	// Without a rollback target, pending state is left exactly as-is.
	if got := manager.Account("cosmos1alice", "uusdc").Balance; got != "100" {
		t.Fatalf("expected untouched balance 100, got %q", got)
	}
}
