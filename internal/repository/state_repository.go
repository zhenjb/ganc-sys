package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

type StateRepository struct{}

func NewStateRepository() *StateRepository {
	return &StateRepository{}
}

func (r *StateRepository) GetState(ctx context.Context) types.AppState {
	return types.AppState{
		Mode:             "mock",
		CurrentStateRoot: "0xrootA",
		UserBalances: map[string]string{
			"cosmos1alice/uusdc": "1000",
		},
		ModuleAccountBalance: map[string]string{
			"uusdc": "0",
		},
		ProofStatus:    "idle",
		DepositStatus:  "none",
		WithdrawStatus: "none",
	}
}
