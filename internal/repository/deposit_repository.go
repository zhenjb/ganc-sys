package repository

import (
	"context"
	"errors"

	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

var ErrDepositNotFound = errors.New("deposit not found")

// DepositRepository owns local access to indexed deposit records.
//
// INT-05 status:
// - Deposit records are now saved from indexed tx events.
// - The local store is in-memory for development.
// - Later this can be replaced by Postgres or another persistent index store.
//
// Source of truth:
//   - DepositRecord should come from the on-chain EventDeposit event,
//     enriched with tx hash and tx height from TxResult.
type DepositRepository struct {
	store *store.MemoryStore
}

func NewDepositRepository(store *store.MemoryStore) *DepositRepository {
	return &DepositRepository{
		store: store,
	}
}

func (r *DepositRepository) SaveDeposit(ctx context.Context, record types.DepositRecord) {
	r.store.SaveDeposit(record)
}

func (r *DepositRepository) GetDeposit(ctx context.Context, depositID string) (types.DepositRecord, error) {
	record, ok := r.store.GetDeposit(depositID)
	if !ok {
		return types.DepositRecord{}, ErrDepositNotFound
	}

	return record, nil
}

func (r *DepositRepository) ListDeposits(ctx context.Context) []types.DepositRecord {
	return r.store.ListDeposits()
}
