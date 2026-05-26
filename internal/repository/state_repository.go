package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// StateRepository provides the dashboard read model.
//
// Local mode:
// - GET /api/state is backed by MemoryStore.
//
// REST mode:
// - GET /api/state merges MemoryStore dashboard fields with real chain query data.
// - currentStateRoot comes from x/zkdex REST.
// - moduleAccountBalance comes from x/zkdex REST.
//
// This matches INT-11: query root, balances, deposit/withdraw statuses for P5.
type StateRepository struct {
	store            *store.MemoryStore
	chainQueryClient *chain.RestQueryClient
	queryMode        string
}

func NewStateRepository(store *store.MemoryStore) *StateRepository {
	return &StateRepository{
		store:     store,
		queryMode: "local",
	}
}

func NewStateRepositoryWithChainQuery(
	store *store.MemoryStore,
	chainQueryClient *chain.RestQueryClient,
	queryMode string,
) *StateRepository {
	if queryMode == "" {
		queryMode = "local"
	}

	return &StateRepository{
		store:            store,
		chainQueryClient: chainQueryClient,
		queryMode:        queryMode,
	}
}

func (r *StateRepository) GetState(ctx context.Context) types.AppState {
	state := r.store.GetAppState()

	if r.queryMode != "rest" || r.chainQueryClient == nil {
		return state
	}

	root, err := r.chainQueryClient.GetCurrentStateRoot(ctx)
	if err == nil && root != "" {
		state.CurrentStateRoot = root
	}

	moduleBalance, err := r.chainQueryClient.GetModuleAccountBalance(ctx)
	if err == nil {
		state.ModuleAccountBalance = normalizeModuleBalance(moduleBalance)
	}

	return state
}

func normalizeModuleBalance(balance map[string]string) map[string]string {
	if len(balance) > 0 {
		return balance
	}

	return map[string]string{
		"uusdc": "0",
	}
}
