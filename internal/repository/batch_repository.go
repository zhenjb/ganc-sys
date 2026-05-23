package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// BatchRepository owns local batch-building fixtures.
//
// INT-04 status:
// - Batch build and batch submit still return local deterministic data.
// - P3 batch builder is not connected yet.
// - No real state transition or commitment calculation happens here yet.
//
// TODO(INT-07 / P3):
// Replace local fixtures with P3 batch builder output:
// - SettlementUpdate with deposits[] and withdrawals[],
// - BatchCommitments,
// - Witness with accounts[].
//
// Important:
// Mock data may contain one deposit and one withdrawal,
// but schema must stay batch-shaped.
type BatchRepository struct{}

func NewBatchRepository() *BatchRepository {
	return &BatchRepository{}
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
