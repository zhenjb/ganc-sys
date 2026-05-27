package indexer

import (
	"context"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/event"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

type fakeIndexedDepositApplier struct {
	calls   int
	records []types.DepositRecord
}

func (f *fakeIndexedDepositApplier) ApplyIndexedDeposit(
	ctx context.Context,
	deposit types.DepositRecord,
) (repository.PendingDepositTransition, error) {
	f.calls++
	f.records = append(f.records, deposit)

	return repository.PendingDepositTransition{
		DepositID:     deposit.DepositID,
		OwnerAddress:  deposit.Owner,
		Denom:         deposit.Denom,
		Amount:        deposit.Amount,
		RootBefore:    "0xrootA",
		RootAfter:     "0xrootB",
		BalanceBefore: "0",
		BalanceAfter:  deposit.Amount,
		Status:        repository.OffchainSettlementStatusPending,
	}, nil
}

func TestDepositIndexerAppliesIndexedDepositToOffchainSettlement(t *testing.T) {
	ctx := context.Background()

	depositRepository := repository.NewDepositRepository(store.NewMemoryStore())
	applier := &fakeIndexedDepositApplier{}

	depositIndexer := NewDepositIndexerWithOffchainSettlement(depositRepository, applier)

	record, err := depositIndexer.IndexDepositFromTx(ctx, chain.TxResult{
		TxHash: "0xtx",
		Height: 123,
		Events: []event.Event{
			{
				Type: event.TypeDeposit,
				Attributes: map[string]string{
					"depositId": "dep-indexer-1",
					"creator":   "cosmos1alice",
					"denom":     "uusdc",
					"amount":    "100",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("index deposit: %v", err)
	}

	if record.DepositID != "dep-indexer-1" {
		t.Fatalf("expected depositId=dep-indexer-1, got %q", record.DepositID)
	}

	if applier.calls != 1 {
		t.Fatalf("expected applier calls=1, got %d", applier.calls)
	}

	if len(applier.records) != 1 {
		t.Fatalf("expected one applied record, got %d", len(applier.records))
	}

	applied := applier.records[0]

	if applied.DepositID != record.DepositID {
		t.Fatalf("expected applied depositId=%q, got %q", record.DepositID, applied.DepositID)
	}

	if applied.Owner != "cosmos1alice" {
		t.Fatalf("expected owner=cosmos1alice, got %q", applied.Owner)
	}

	stored, err := depositRepository.GetDeposit(ctx, "dep-indexer-1")
	if err != nil {
		t.Fatalf("get stored deposit: %v", err)
	}

	if stored.DepositID != "dep-indexer-1" {
		t.Fatalf("expected stored depositId=dep-indexer-1, got %q", stored.DepositID)
	}
}
