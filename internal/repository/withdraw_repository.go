package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// WithdrawRepository owns withdrawal request and withdrawal record access.
//
// INT-04 status:
// - Withdraw request and claim endpoints still return local deterministic data.
// - There is no persisted withdrawal request store yet.
// - There is no on-chain MsgClaimWithdraw integration yet.
//
// TODO(INT-06):
// Persist user-created withdrawal requests instead of returning fixtures.
//
// TODO(INT-09/INT-10):
// Replace local withdrawal records with records produced by successful
// MsgSubmitBatchProof and MsgClaimWithdraw chain events/queries.
type WithdrawRepository struct{}

func NewWithdrawRepository() *WithdrawRepository {
	return &WithdrawRepository{}
}

func (r *WithdrawRepository) GetLocalWithdrawRequest(ctx context.Context) types.WithdrawRequest {
	return types.WithdrawRequest{
		WithdrawID:  "wd-1",
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
		Nonce:       "1",
		Signature:   "0xlocalsignature",
	}
}

func (r *WithdrawRepository) GetLocalWithdrawRecord(ctx context.Context, claimed bool) types.WithdrawRecord {
	return types.WithdrawRecord{
		WithdrawID:  "wd-1",
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
		Nullifier:   "0xmocknullifier",
		Claimed:     claimed,
	}
}

func (r *WithdrawRepository) GetLocalClaimBalanceSnapshot(ctx context.Context) types.BalanceSnapshot {
	return types.BalanceSnapshot{
		UserBalances: map[string]string{
			"cosmos1alice/uusdc": "940",
		},
		ModuleAccountBalance: map[string]string{
			"uusdc": "60",
		},
	}
}
