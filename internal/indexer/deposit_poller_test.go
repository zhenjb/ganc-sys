package indexer

import (
	"context"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/event"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// scriptedSource replays a fixed set of txs above the requested height, mimicking
// a chain whose deposit events accumulate over blocks.
type scriptedSource struct {
	all   []chain.TxResult
	calls int
}

func (s *scriptedSource) FetchDepositsSince(ctx context.Context, fromHeight int64) ([]chain.TxResult, int64, error) {
	s.calls++
	out := make([]chain.TxResult, 0)
	next := fromHeight
	for _, tx := range s.all {
		if tx.Height > fromHeight {
			out = append(out, tx)
			if tx.Height > next {
				next = tx.Height
			}
		}
	}
	return out, next, nil
}

func depositTx(hash string, height int64, depositID, owner, amount string) chain.TxResult {
	return chain.TxResult{
		TxHash: hash,
		Height: height,
		Events: []event.Event{
			{
				Type: event.TypeDeposit,
				Attributes: map[string]string{
					"depositId": depositID,
					"creator":   owner,
					"denom":     "uusdc",
					"amount":    amount,
				},
			},
		},
	}
}

// managerApplier mirrors indexed deposits into a real OffchainStateManager so the
// test asserts the on-chain -> off-chain state reflection that SYS-03 requires.
type managerApplier struct {
	manager *state.OffchainStateManager
}

func (a *managerApplier) ApplyIndexedDeposit(ctx context.Context, deposit types.DepositRecord) (repository.PendingDepositTransition, error) {
	root, err := a.manager.ApplyDeposit(deposit)
	if err != nil {
		return repository.PendingDepositTransition{}, err
	}
	return repository.PendingDepositTransition{
		DepositID:    deposit.DepositID,
		OwnerAddress: deposit.Owner,
		Denom:        deposit.Denom,
		Amount:       deposit.Amount,
		RootAfter:    root,
	}, nil
}

func newPollerHarness() (*scriptedSource, *managerApplier, *repository.DepositRepository, *DepositIndexer) {
	depositRepo := repository.NewDepositRepository(store.NewMemoryStore())
	applier := &managerApplier{manager: state.NewOffchainStateManager()}
	indexer := NewDepositIndexerWithOffchainSettlement(depositRepo, applier)
	source := &scriptedSource{
		all: []chain.TxResult{
			depositTx("TX1", 10, "dep-10", "cosmos1alice", "100"),
			depositTx("TX2", 12, "dep-12", "cosmos1alice", "40"),
		},
	}
	return source, applier, depositRepo, indexer
}

func TestDepositPollerIndexesAndMirrorsToOffchainState(t *testing.T) {
	ctx := context.Background()
	source, applier, depositRepo, indexer := newPollerHarness()
	poller := NewDepositPoller(source, indexer, 0, 0)

	indexed, err := poller.PollOnce(ctx)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if indexed != 2 {
		t.Fatalf("expected 2 indexed deposits, got %d", indexed)
	}
	if poller.Cursor() != 12 {
		t.Fatalf("expected cursor advanced to 12, got %d", poller.Cursor())
	}

	// Deposits mirrored into the deposit store.
	if _, err := depositRepo.GetDeposit(ctx, "dep-10"); err != nil {
		t.Fatalf("dep-10 not stored: %v", err)
	}

	// Off-chain pending balance reflects both deposits: 100 + 40 = 140.
	acc := applier.manager.Account("cosmos1alice", "uusdc")
	if acc.Balance != "140" {
		t.Fatalf("expected off-chain balance 140, got %q", acc.Balance)
	}
}

func TestDepositPollerIsIdempotentAcrossPolls(t *testing.T) {
	ctx := context.Background()
	source, applier, _, indexer := newPollerHarness()
	poller := NewDepositPoller(source, indexer, 0, 0)

	if _, err := poller.PollOnce(ctx); err != nil {
		t.Fatalf("first poll: %v", err)
	}

	// Reset the cursor so the SAME deposits are re-fetched; the manager's
	// idempotency-by-depositId must prevent any double credit.
	poller.mu.Lock()
	poller.cursor = 0
	poller.mu.Unlock()

	indexed, err := poller.PollOnce(ctx)
	if err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if indexed != 0 {
		t.Fatalf("expected 0 newly indexed on replay, got %d", indexed)
	}

	acc := applier.manager.Account("cosmos1alice", "uusdc")
	if acc.Balance != "140" {
		t.Fatalf("expected balance to stay 140 after replay, got %q", acc.Balance)
	}
}

func TestDepositPollerSkipsMalformedTxWithoutAborting(t *testing.T) {
	ctx := context.Background()
	depositRepo := repository.NewDepositRepository(store.NewMemoryStore())
	applier := &managerApplier{manager: state.NewOffchainStateManager()}
	indexer := NewDepositIndexerWithOffchainSettlement(depositRepo, applier)

	source := &scriptedSource{
		all: []chain.TxResult{
			// Missing depositId -> indexer returns an error for this tx.
			{TxHash: "BAD", Height: 5, Events: []event.Event{{
				Type:       event.TypeDeposit,
				Attributes: map[string]string{"creator": "cosmos1alice", "denom": "uusdc", "amount": "10"},
			}}},
			depositTx("GOOD", 6, "dep-6", "cosmos1alice", "10"),
		},
	}
	poller := NewDepositPoller(source, indexer, 0, 0)

	indexed, err := poller.PollOnce(ctx)
	if err != nil {
		t.Fatalf("poll should not hard-fail on a single bad tx: %v", err)
	}
	if indexed != 1 {
		t.Fatalf("expected 1 good deposit indexed, got %d", indexed)
	}
	if poller.Cursor() != 6 {
		t.Fatalf("expected cursor to advance to 6 despite bad tx, got %d", poller.Cursor())
	}
}
