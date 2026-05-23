package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// StateRepository provides the current dashboard state.
//
// INT-04 status:
// - This still returns a local initial state fixture.
// - It does not query chain state yet.
// - It does not read indexed events yet.
//
// TODO(INT-05+):
// Replace local fixture data with data assembled from:
// - indexed deposits,
// - indexed batch submissions,
// - indexed withdrawal records,
// - chain/module account balance queries.
type StateRepository struct{}

func NewStateRepository() *StateRepository {
	return &StateRepository{}
}

func (r *StateRepository) GetState(ctx context.Context) types.AppState {
	return types.AppState{
		Mode:             "local",
		CurrentStateRoot: "0xrootA",
		UserBalances: map[string]string{
			"cosmos1alice/uusdc": "1000",
		},
		ModuleAccountBalance: map[string]string{
			"uusdc": "0",
		},

		LatestDeposit:          nil,
		LatestWithdrawRequest:  nil,
		LatestSettlement:       nil,
		LatestBatchCommitments: nil,
		LatestProof:            nil,
		LatestWithdrawRecords:  nil,

		ProofStatus:    "idle",
		DepositStatus:  "none",
		WithdrawStatus: "none",
		BatchStatus:    "none",
	}
}
