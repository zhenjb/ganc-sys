package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// BatchRepository owns local batch read-model persistence.
//
// INT-11 status:
// - Batch build updates latestSettlement/latestBatchCommitments.
// - Batch submit updates currentStateRoot/statuses/latestWithdrawRecords.
//
// P3 still owns the real batch builder implementation.
type BatchRepository struct {
	store *store.MemoryStore
}

func NewBatchRepository(store *store.MemoryStore) *BatchRepository {
	return &BatchRepository{
		store: store,
	}
}

func (r *BatchRepository) SaveBatchBuild(
	ctx context.Context,
	settlementUpdate types.SettlementUpdate,
	batchCommitments types.BatchCommitments,
) {
	r.store.SaveBatchBuild(settlementUpdate, batchCommitments)
}

func (r *BatchRepository) SaveBatchSubmitted(
	ctx context.Context,
	settlementUpdate types.SettlementUpdate,
	batchCommitments types.BatchCommitments,
	withdrawRecords []types.WithdrawRecord,
) {
	r.store.SaveBatchSubmitted(settlementUpdate, batchCommitments, withdrawRecords)
}

func (r *BatchRepository) GetLocalSettlementUpdate(ctx context.Context) types.SettlementUpdate {
	return types.SettlementUpdate{
		BatchID:      "batch-1",
		OldStateRoot: "0xrootA",
		NewStateRoot: "0xrootB",
		Deposits: []types.SettlementDeposit{
			{
				DepositID: "dep-1",
				Owner:     "cosmos1alice",
				Denom:     "uusdc",
				Amount:    "100",
			},
		},
		Withdrawals: []types.SettlementWithdrawal{
			{
				WithdrawID:      "wd-1",
				Owner:           "cosmos1alice",
				Denom:           "uusdc",
				Amount:          "40",
				Destination:     "cosmos1alice",
				DestinationHash: "0xmockdestinationhash",
				Nullifier:       "0xmocknullifier",
			},
		},
	}
}

func (r *BatchRepository) GetLocalBatchCommitments(ctx context.Context) types.BatchCommitments {
	return types.BatchCommitments{
		DepositsRoot:        "0xdepositsRoot",
		WithdrawalsRoot:     "0xwithdrawalsRoot",
		NullifiersRoot:      "0xnullifiersRoot",
		WithdrawOutputsRoot: "0xwithdrawOutputsRoot",
	}
}

func (r *BatchRepository) GetLocalWitness(ctx context.Context) types.Witness {
	return types.Witness{
		Accounts: []types.WitnessAccount{
			{
				Owner:      "cosmos1alice",
				UserSecret: "mock-user-secret",
				Nonce:      "1",
				OldBalance: "0",
				NewBalance: "60",
			},
		},
	}
}
