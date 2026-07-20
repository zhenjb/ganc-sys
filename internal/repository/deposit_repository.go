package repository

import (
	"context"
	"errors"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

var ErrDepositNotFound = errors.New("deposit not found")

// DepositRepository owns deposit record access.
//
// Source-of-truth rule:
//
// Local mode:
// - MemoryStore is the source of truth.
// - Used for local/dev flow and tests.
//
// REST mode:
// - Chain REST is the source of truth for on-chain deposit records.
// - MemoryStore is only a fallback for local transitional data.
// - This prevents stale local data from overriding real chain state.
//
// This matches INT-05:
// - Deposit is produced on-chain.
// - Backend queries/indexes DepositRecord for P3 batch builder and P5 UI.
type DepositRepository struct {
	store            *store.MemoryStore
	chainQueryClient *chain.RestQueryClient
	queryMode        string
}

func NewDepositRepository(store *store.MemoryStore) *DepositRepository {
	return &DepositRepository{
		store:     store,
		queryMode: "local",
	}
}

func NewDepositRepositoryWithChainQuery(
	store *store.MemoryStore,
	chainQueryClient *chain.RestQueryClient,
	queryMode string,
) *DepositRepository {
	if queryMode == "" {
		queryMode = "local"
	}

	return &DepositRepository{
		store:            store,
		chainQueryClient: chainQueryClient,
		queryMode:        queryMode,
	}
}

func (r *DepositRepository) SaveDeposit(ctx context.Context, record types.DepositRecord) {
	r.store.SaveDeposit(record)
}

// MarkProcessed flips the given deposits' Processed flag once their batch has
// settled on-chain (Nhóm 4 (d)). Best-effort against the in-memory read model that
// backs GET /api/deposits; unknown ids are ignored.
func (r *DepositRepository) MarkProcessed(ctx context.Context, depositIDs []string) {
	for _, id := range depositIDs {
		r.store.MarkDepositProcessed(id)
	}
}

func (r *DepositRepository) GetDeposit(ctx context.Context, depositID string) (types.DepositRecord, error) {
	if usesChainQuery(r.queryMode) && r.chainQueryClient != nil {
		return r.getDepositChainFirst(ctx, depositID)
	}

	return r.getDepositLocalOnly(depositID)
}

func (r *DepositRepository) ListDeposits(ctx context.Context) []types.DepositRecord {
	// Current x/zkdex REST API exposes get-by-id deposit query,
	// but not list-deposits.
	//
	// Therefore list remains local/indexed only for now.
	// Once P1 exposes a list endpoint or event indexer, REST mode should
	// return chain-indexed deposits here too.
	return r.store.ListDeposits()
}

func (r *DepositRepository) getDepositChainFirst(ctx context.Context, depositID string) (types.DepositRecord, error) {
	chainRecord, found, err := r.chainQueryClient.GetDepositRecord(ctx, depositID)
	if err != nil {
		return types.DepositRecord{}, err
	}

	if found {
		return chainRecord, nil
	}

	// Fallback only for transitional local data.
	// In REST mode, this must never override a chain record.
	record, ok := r.store.GetDeposit(depositID)
	if ok {
		return record, nil
	}

	return types.DepositRecord{}, ErrDepositNotFound
}

func (r *DepositRepository) getDepositLocalOnly(depositID string) (types.DepositRecord, error) {
	record, ok := r.store.GetDeposit(depositID)
	if !ok {
		return types.DepositRecord{}, ErrDepositNotFound
	}

	return record, nil
}
