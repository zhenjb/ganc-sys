package batch

import (
	"context"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestSnapshotBuilderBuildsFromManagerSnapshot(t *testing.T) {
	manager := state.NewOffchainStateManager()

	seedDeposit := types.DepositRecord{
		DepositID: "dep-seed",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "50",
		Processed: false,
	}

	oldRoot, err := manager.ApplyDeposit(seedDeposit)
	if err != nil {
		t.Fatalf("seed manager deposit: %v", err)
	}

	builder := NewSnapshotBuilder(manager)

	output, err := builder.Build(context.Background(), BuildInput{
		OldStateRoot: oldRoot,
		Deposits: []types.DepositRecord{
			{
				DepositID: "dep-1",
				Owner:     "cosmos1alice",
				Denom:     "uusdc",
				Amount:    "100",
				Processed: false,
			},
		},
		WithdrawRequests: []types.WithdrawRequest{
			{
				WithdrawID:  "wd-1",
				Owner:       "cosmos1alice",
				Denom:       "uusdc",
				Amount:      "40",
				Destination: "cosmos1alice",
				Nonce:       "1",
				Signature:   "0xmocksignature",
			},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if output.SettlementUpdate.OldStateRoot != oldRoot {
		t.Fatalf("expected oldStateRoot=%q, got %q", oldRoot, output.SettlementUpdate.OldStateRoot)
	}

	if output.SettlementUpdate.NewStateRoot == "" {
		t.Fatalf("expected newStateRoot")
	}

	if output.SettlementUpdate.NewStateRoot == output.SettlementUpdate.OldStateRoot {
		t.Fatalf("expected state root to change")
	}

	if len(output.SettlementUpdate.Deposits) != 1 {
		t.Fatalf("expected one settlement deposit, got %d", len(output.SettlementUpdate.Deposits))
	}

	if len(output.SettlementUpdate.Withdrawals) != 1 {
		t.Fatalf("expected one settlement withdrawal, got %d", len(output.SettlementUpdate.Withdrawals))
	}

	if len(output.Witness.Accounts) != 1 {
		t.Fatalf("expected one witness account, got %d", len(output.Witness.Accounts))
	}

	account := output.Witness.Accounts[0]

	if account.Owner != "cosmos1alice" {
		t.Fatalf("expected owner=cosmos1alice, got %q", account.Owner)
	}

	if account.OldBalance != "50" {
		t.Fatalf("expected oldBalance=50, got %q", account.OldBalance)
	}

	if account.NewBalance != "110" {
		t.Fatalf("expected newBalance=110, got %q", account.NewBalance)
	}

	if output.BatchCommitments.DepositsRoot == "" {
		t.Fatalf("expected depositsRoot")
	}

	if output.BatchCommitments.WithdrawalsRoot == "" {
		t.Fatalf("expected withdrawalsRoot")
	}

	if output.BatchCommitments.NullifiersRoot == "" {
		t.Fatalf("expected nullifiersRoot")
	}

	if output.BatchCommitments.WithdrawOutputsRoot == "" {
		t.Fatalf("expected withdrawOutputsRoot")
	}
}

func TestSnapshotBuilderDoesNotMutateManager(t *testing.T) {
	manager := state.NewOffchainStateManager()

	seedDeposit := types.DepositRecord{
		DepositID: "dep-seed",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "50",
		Processed: false,
	}

	rootBefore, err := manager.ApplyDeposit(seedDeposit)
	if err != nil {
		t.Fatalf("seed manager deposit: %v", err)
	}

	builder := NewSnapshotBuilder(manager)

	_, err = builder.Build(context.Background(), BuildInput{
		Deposits: []types.DepositRecord{
			{
				DepositID: "dep-1",
				Owner:     "cosmos1alice",
				Denom:     "uusdc",
				Amount:    "100",
				Processed: false,
			},
		},
		WithdrawRequests: []types.WithdrawRequest{
			{
				WithdrawID:  "wd-1",
				Owner:       "cosmos1alice",
				Denom:       "uusdc",
				Amount:      "40",
				Destination: "cosmos1alice",
				Nonce:       "1",
				Signature:   "0xmocksignature",
			},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	rootAfter := manager.Root()
	if rootAfter != rootBefore {
		t.Fatalf("expected manager root unchanged, before=%q after=%q", rootBefore, rootAfter)
	}

	account := manager.Account("cosmos1alice", "uusdc")
	if account.Balance != "50" {
		t.Fatalf("expected manager balance unchanged at 50, got %q", account.Balance)
	}
}

func TestSnapshotBuilderDiffersFromLocalBuilderWhenManagerHasExistingState(t *testing.T) {
	manager := state.NewOffchainStateManager()

	_, err := manager.ApplyDeposit(types.DepositRecord{
		DepositID: "dep-seed",
		Owner:     "cosmos1alice",
		Denom:     "uusdc",
		Amount:    "50",
		Processed: false,
	})
	if err != nil {
		t.Fatalf("seed manager deposit: %v", err)
	}

	input := BuildInput{
		OldStateRoot: "0xrootA",
		Deposits: []types.DepositRecord{
			{
				DepositID: "dep-1",
				Owner:     "cosmos1alice",
				Denom:     "uusdc",
				Amount:    "100",
				Processed: false,
			},
		},
		WithdrawRequests: []types.WithdrawRequest{
			{
				WithdrawID:  "wd-1",
				Owner:       "cosmos1alice",
				Denom:       "uusdc",
				Amount:      "40",
				Destination: "cosmos1alice",
				Nonce:       "1",
				Signature:   "0xmocksignature",
			},
		},
	}

	localOutput, err := NewLocalBuilder().Build(context.Background(), input)
	if err != nil {
		t.Fatalf("local build: %v", err)
	}

	snapshotOutput, err := NewSnapshotBuilder(manager).Build(context.Background(), input)
	if err != nil {
		t.Fatalf("snapshot build: %v", err)
	}

	localAccount := localOutput.Witness.Accounts[0]
	snapshotAccount := snapshotOutput.Witness.Accounts[0]

	if localAccount.OldBalance != "0" {
		t.Fatalf("expected local oldBalance=0, got %q", localAccount.OldBalance)
	}

	if localAccount.NewBalance != "60" {
		t.Fatalf("expected local newBalance=60, got %q", localAccount.NewBalance)
	}

	if snapshotAccount.OldBalance != "50" {
		t.Fatalf("expected snapshot oldBalance=50, got %q", snapshotAccount.OldBalance)
	}

	if snapshotAccount.NewBalance != "110" {
		t.Fatalf("expected snapshot newBalance=110, got %q", snapshotAccount.NewBalance)
	}

	if localOutput.SettlementUpdate.NewStateRoot == snapshotOutput.SettlementUpdate.NewStateRoot {
		t.Fatalf("expected different newStateRoot between local and snapshot builder")
	}
}
