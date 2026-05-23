package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// DepositRepository owns local access to indexed deposit records.
//
// INT-04 status:
// - POST /api/deposit currently gets DepositRecord directly from chain.LocalClient.
// - No event indexer/store is connected yet.
// - This repository exists now so INT-05 can add event-backed deposit indexing cleanly.
//
// TODO(INT-05):
// Implement event-backed deposit indexing:
// 1. consume zkdex.deposit_queued event emitted by x/zkdex,
// 2. convert event attributes into types.DepositRecord,
// 3. save DepositRecord into local store/database,
// 4. expose GetDeposit/ListDeposits for P3 batch builder and P5 UI.
//
// Important:
// The final source of truth for DepositRecord should be indexed on-chain events,
// not direct construction inside the HTTP handler/service.
type DepositRepository struct{}

func NewDepositRepository() *DepositRepository {
	return &DepositRepository{}
}

// GetLocalDepositFixture returns the canonical local deposit fixture.
//
// This is not used as the source of truth after INT-05.
// It is kept only as a local fixture while the event indexer is not connected.
func (r *DepositRepository) GetLocalDepositFixture(ctx context.Context) types.DepositRecord {
	return types.DepositRecord{
		DepositID:     "dep-1",
		Owner:         "cosmos1alice",
		Denom:         "uusdc",
		Amount:        "100",
		Processed:     false,
		CreatedHeight: 12345,
		TxHash:        "0xlocaldeposit",
	}
}
