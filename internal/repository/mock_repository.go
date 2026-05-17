package repository

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

type MockRepository struct{}

func NewMockRepository() *MockRepository {
	return &MockRepository{}
}

func (r *MockRepository) GetState(ctx context.Context) types.AppState {
	return types.AppState{
		Mode:             "mock",
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

func (r *MockRepository) GetDepositRecord(ctx context.Context) types.DepositRecord {
	return types.DepositRecord{
		DepositID:     "dep-1",
		Owner:         "cosmos1alice",
		Denom:         "uusdc",
		Amount:        "100",
		Processed:     false,
		CreatedHeight: 12345,
		TxHash:        "0xmockdeposit",
	}
}

func (r *MockRepository) GetWithdrawRequest(ctx context.Context) types.WithdrawRequest {
	return types.WithdrawRequest{
		WithdrawID:  "wd-1",
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
		Nonce:       "1",
		Signature:   "0xmocksignature",
	}
}

func (r *MockRepository) GetSettlementUpdate(ctx context.Context) types.SettlementUpdate {
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

func (r *MockRepository) GetBatchCommitments(ctx context.Context) types.BatchCommitments {
	return types.BatchCommitments{
		DepositsRoot:        "0xdepositsRoot",
		WithdrawalsRoot:     "0xwithdrawalsRoot",
		NullifiersRoot:      "0xnullifiersRoot",
		WithdrawOutputsRoot: "0xwithdrawOutputsRoot",
	}
}

func (r *MockRepository) GetWitness(ctx context.Context) types.Witness {
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

func (r *MockRepository) GetProofBundle(ctx context.Context) types.ProofBundle {
	return types.ProofBundle{
		Proof: "0xmockproof",
		PublicInputs: []string{
			"0xrootA",
			"0xrootB",
			"0xdepositsRoot",
			"0xwithdrawalsRoot",
			"0xnullifiersRoot",
			"0xwithdrawOutputsRoot",
		},
		VerificationKeyID: "v1",
	}
}

func (r *MockRepository) GetWithdrawRecord(ctx context.Context, claimed bool) types.WithdrawRecord {
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
