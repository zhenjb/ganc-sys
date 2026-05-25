package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// StateRepository provides the dashboard read model.
//
// INT-11 status:
//   - GET /api/state is now backed by MemoryStore.
//   - It reflects latest deposit, withdraw request, batch, proof, submit,
//     claim, balances, and statuses.
type StateRepository struct {
	store *store.MemoryStore
}

func NewStateRepository(store *store.MemoryStore) *StateRepository {
	return &StateRepository{
		store: store,
	}
}

func (r *StateRepository) GetState(ctx context.Context) types.AppState {
	return r.store.GetAppState()
}
