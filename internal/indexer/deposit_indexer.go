package indexer

import (
	"context"
	"fmt"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/event"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// IndexedDepositApplier is the narrow boundary from the deposit indexer into
// P3's off-chain settlement state.
//
// The real implementation is service.OffchainSettlementService.
// This interface lives in indexer package to avoid an import cycle:
//
// service -> indexer
// indexer -> service would be illegal
type IndexedDepositApplier interface {
	ApplyIndexedDeposit(ctx context.Context, deposit types.DepositRecord) (repository.PendingDepositTransition, error)
}

// DepositIndexer consumes on-chain deposit events and creates a local
// DepositRecord mirror for P3 batch builder and P5 UI.
//
// The real chain emits:
//
//	event type: ob.zkdex.v1.EventDeposit
//	fields: DepositId, Creator, Denom, Amount
//
// TxHash and CreatedHeight do not come from EventDeposit itself.
// They are enriched from TxResult.
//
// P3INT-06:
// If an IndexedDepositApplier is configured, the indexed DepositRecord is also
// applied into the off-chain settlement state and persisted as a pending
// deposit transition.
type DepositIndexer struct {
	depositRepository *repository.DepositRepository
	indexedApplier    IndexedDepositApplier
}

func NewDepositIndexer(depositRepository *repository.DepositRepository) *DepositIndexer {
	return &DepositIndexer{
		depositRepository: depositRepository,
	}
}

func NewDepositIndexerWithOffchainSettlement(
	depositRepository *repository.DepositRepository,
	indexedApplier IndexedDepositApplier,
) *DepositIndexer {
	return &DepositIndexer{
		depositRepository: depositRepository,
		indexedApplier:    indexedApplier,
	}
}

func (i *DepositIndexer) IndexDepositFromTx(ctx context.Context, tx chain.TxResult) (types.DepositRecord, error) {
	for _, ev := range tx.Events {
		if ev.Type != event.TypeDeposit {
			continue
		}

		record, err := i.depositRecordFromEvent(tx, ev)
		if err != nil {
			return types.DepositRecord{}, err
		}

		i.depositRepository.SaveDeposit(ctx, record)

		if i.indexedApplier != nil {
			if _, err := i.indexedApplier.ApplyIndexedDeposit(ctx, record); err != nil {
				return types.DepositRecord{}, err
			}
		}

		return record, nil
	}

	return types.DepositRecord{}, fmt.Errorf("deposit event not found")
}

func (i *DepositIndexer) depositRecordFromEvent(tx chain.TxResult, ev event.Event) (types.DepositRecord, error) {
	depositID := attr(ev.Attributes, "depositId", "deposit_id")
	creator := attr(ev.Attributes, "creator", "owner")
	denom := attr(ev.Attributes, "denom")
	amount := attr(ev.Attributes, "amount")

	if depositID == "" {
		return types.DepositRecord{}, fmt.Errorf("deposit event missing depositId")
	}

	if creator == "" {
		return types.DepositRecord{}, fmt.Errorf("deposit event missing creator")
	}

	if denom == "" {
		return types.DepositRecord{}, fmt.Errorf("deposit event missing denom")
	}

	if amount == "" {
		return types.DepositRecord{}, fmt.Errorf("deposit event missing amount")
	}

	return types.DepositRecord{
		DepositID:     depositID,
		Owner:         creator,
		Denom:         denom,
		Amount:        amount,
		Processed:     false,
		CreatedHeight: tx.Height,
		TxHash:        tx.TxHash,
	}, nil
}

func attr(attrs map[string]string, keys ...string) string {
	for _, key := range keys {
		value, ok := attrs[key]
		if ok {
			return value
		}
	}

	return ""
}
