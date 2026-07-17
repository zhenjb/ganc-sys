package batch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// SnapshotBuilder is the first P3INT bridge between P4 and P3's
// OffchainStateManager.
//
// It differs from LocalBuilder in one important way:
//
// LocalBuilder:
// - starts every Build from an empty LocalState.
//
// SnapshotBuilder:
// - starts every Build from OffchainStateManager.Snapshot().
// - applies this batch on top of that snapshot in a working copy.
// - does not mutate the manager during Build.
//
// This keeps witness oldBalance/newBalance correct:
//
// oldBalance = snapshot account balance before this batch
// newBalance = working state account balance after this batch
//
// Production Option B will later move to request-time apply +
// pending operation tracking + rollback. This builder is the safe
// bridge before that.
type SnapshotBuilder struct {
	manager    *state.OffchainStateManager
	settlement *SettlementUpdateBuilder
	witness    *WitnessBuilder
}

func NewSnapshotBuilder(manager *state.OffchainStateManager) *SnapshotBuilder {
	if manager == nil {
		manager = state.NewOffchainStateManager()
	}

	return &SnapshotBuilder{
		manager: manager,
		// INT-2SEQ: đường core mint batchId dưới namespace "core-" để không đụng
		// namespace "trade-" của đường trade (RealOrderService) trên cùng chain.
		settlement: NewSettlementUpdateBuilderWithPrefix("core-"),
		witness:    NewWitnessBuilder(),
	}
}

func (b *SnapshotBuilder) Build(ctx context.Context, in BuildInput) (BuildOutput, error) {
	if err := ctx.Err(); err != nil {
		return BuildOutput{}, err
	}

	secrets, err := indexSecrets(in.AccountSecrets)
	if err != nil {
		return BuildOutput{}, err
	}

	parts, err := collectParticipants(in.Deposits, in.WithdrawRequests)
	if err != nil {
		return BuildOutput{}, err
	}

	snap := b.manager.Snapshot()
	oldRoot := snap.Root()
	if err := validateRoot(oldRoot, "snapshot.oldStateRoot"); err != nil {
		return BuildOutput{}, err
	}

	workingState := state.NewLocalStateFromSnapshot(snap)

	for i, d := range in.Deposits {
		if _, err := workingState.ApplyDeposit(d); err != nil {
			return BuildOutput{}, fmt.Errorf("%w: deposits[%d] depositId=%q: %v",
				ErrInvalidBuildInput, i, d.DepositID, err)
		}
	}

	withdrawals := make([]WithdrawalInput, 0, len(in.WithdrawRequests))
	for i, req := range in.WithdrawRequests {
		owner := strings.TrimSpace(req.Owner)
		secret := resolveSecret(secrets, owner)

		nullifier, err := state.NullifierFor(secret, req.Nonce)
		if err != nil {
			return BuildOutput{}, fmt.Errorf("%w: withdrawRequests[%d] withdrawId=%q nullifier derivation: %v",
				ErrInvalidBuildInput, i, req.WithdrawID, err)
		}

		destinationHash, err := state.WithdrawAddressHash(req.Destination)
		if err != nil {
			return BuildOutput{}, fmt.Errorf("%w: withdrawRequests[%d] withdrawId=%q destinationHash derivation: %v",
				ErrInvalidBuildInput, i, req.WithdrawID, err)
		}

		if _, err := workingState.ApplyWithdrawal(req, nullifier); err != nil {
			if errors.Is(err, state.ErrInsufficientBalance) {
				return BuildOutput{}, fmt.Errorf(
					"%w: withdrawRequests[%d] withdrawId=%q owner=%q amount=%s: %v",
					ErrInsufficientOffchainBalance, i, req.WithdrawID, req.Owner, req.Amount, err,
				)
			}

			return BuildOutput{}, fmt.Errorf("%w: withdrawRequests[%d] withdrawId=%q apply: %v",
				ErrInvalidBuildInput, i, req.WithdrawID, err)
		}

		withdrawals = append(withdrawals, WithdrawalInput{
			Request:         req,
			Nullifier:       nullifier,
			DestinationHash: destinationHash,
		})
	}

	newRoot := workingState.Root()

	settlementInput := SettlementInputs{
		OldStateRoot: oldRoot,
		NewStateRoot: newRoot,
		Deposits:     append([]types.DepositRecord(nil), in.Deposits...),
		Withdrawals:  withdrawals,
	}

	settlementUpdate, err := b.settlement.Build(settlementInput)
	if err != nil {
		return BuildOutput{}, err
	}

	accounts := make([]AccountWitnessSecret, 0, len(parts))
	for _, p := range parts {
		oldAccount := snap.Account(p.owner, p.denom)
		newAccount := workingState.Account(p.owner, p.denom)

		accounts = append(accounts, AccountWitnessSecret{
			Owner:      p.owner,
			UserSecret: resolveSecret(secrets, p.owner),
			OldBalance: oldAccount.Balance,
			NewBalance: newAccount.Balance,
			Denom:      p.denom,
		})
	}

	witnessInput := WitnessInputs{
		Settlement: settlementInput,
		Accounts:   accounts,
	}

	witness, err := b.witness.Build(witnessInput)
	if err != nil {
		return BuildOutput{}, err
	}

	commitments := BuildCommitments(settlementUpdate)

	return BuildOutput{
		SettlementUpdate: settlementUpdate,
		BatchCommitments: commitments,
		Witness:          witness,
	}, nil
}

func (b *SnapshotBuilder) Seq() uint64 {
	return b.settlement.Seq()
}

var _ Builder = (*SnapshotBuilder)(nil)
