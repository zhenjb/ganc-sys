package indexer

import (
	"context"
	"fmt"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/event"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

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
type DepositIndexer struct {
	depositRepository *repository.DepositRepository
}

func NewDepositIndexer(depositRepository *repository.DepositRepository) *DepositIndexer {
	return &DepositIndexer{
		depositRepository: depositRepository,
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
