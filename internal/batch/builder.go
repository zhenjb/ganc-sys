package batch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

var ErrInsufficientOffchainBalance = errors.New("insufficient off-chain balance")

// Builder is the integration boundary for the P3 batch builder.
//
// P4 owns:
// - API endpoint integration,
// - loading indexed deposits,
// - loading persisted withdrawal requests,
// - calling this interface,
// - returning the output to FE/P2.
//
// P3 owns the real implementation:
// - off-chain state transition,
// - balance checks,
// - newStateRoot,
// - nullifier computation,
// - destinationHash,
// - batch commitments,
// - witness construction.
type Builder interface {
	Build(ctx context.Context, input BuildInput) (BuildOutput, error)
}

type BuildInput struct {
	OldStateRoot     string
	Deposits         []types.DepositRecord
	WithdrawRequests []types.WithdrawRequest
}

type BuildOutput struct {
	SettlementUpdate types.SettlementUpdate
	BatchCommitments types.BatchCommitments
	Witness          types.Witness
}

// LocalBuilder is a temporary deterministic placeholder for P3 integration.
//
// TODO(P3):
// Replace this with the real P3 batch builder implementation.
// Do not treat this as production batch logic.
// This exists only so P4/P5/P2 can continue integrating against stable contracts.
type LocalBuilder struct{}

func NewLocalBuilder() *LocalBuilder {
	return &LocalBuilder{}
}

func (b *LocalBuilder) Build(ctx context.Context, input BuildInput) (BuildOutput, error) {
	if input.OldStateRoot == "" {
		input.OldStateRoot = "0xrootA"
	}

	accountStates, err := buildLocalAccountStates(input.Deposits, input.WithdrawRequests)
	if err != nil {
		return BuildOutput{}, err
	}

	settlementDeposits := make([]types.SettlementDeposit, 0, len(input.Deposits))
	for _, deposit := range input.Deposits {
		settlementDeposits = append(settlementDeposits, types.SettlementDeposit{
			DepositID: deposit.DepositID,
			Owner:     deposit.Owner,
			Denom:     deposit.Denom,
			Amount:    deposit.Amount,
		})
	}

	settlementWithdrawals := make([]types.SettlementWithdrawal, 0, len(input.WithdrawRequests))
	for _, withdrawReq := range input.WithdrawRequests {
		settlementWithdrawals = append(settlementWithdrawals, types.SettlementWithdrawal{
			WithdrawID:      withdrawReq.WithdrawID,
			Owner:           withdrawReq.Owner,
			Denom:           withdrawReq.Denom,
			Amount:          withdrawReq.Amount,
			Destination:     withdrawReq.Destination,
			DestinationHash: localHash("destination", withdrawReq.Destination),
			Nullifier:       localHash("nullifier", withdrawReq.Owner, withdrawReq.Nonce),
		})
	}

	witnessAccounts := make([]types.WitnessAccount, 0, len(accountStates))
	for _, state := range accountStates {
		witnessAccounts = append(witnessAccounts, types.WitnessAccount{
			Owner:      state.Owner,
			UserSecret: "mock-user-secret",
			Nonce:      state.Nonce,
			OldBalance: strconv.FormatInt(state.OldBalance, 10),
			NewBalance: strconv.FormatInt(state.NewBalance, 10),
		})
	}

	commitments := types.BatchCommitments{
		DepositsRoot:        hashJSON("depositsRoot", settlementDeposits),
		WithdrawalsRoot:     hashJSON("withdrawalsRoot", settlementWithdrawals),
		NullifiersRoot:      hashJSON("nullifiersRoot", collectNullifiers(settlementWithdrawals)),
		WithdrawOutputsRoot: hashJSON("withdrawOutputsRoot", settlementWithdrawals),
	}

	newStateRoot := hashJSON("newStateRoot", map[string]any{
		"oldStateRoot": input.OldStateRoot,
		"accounts":     witnessAccounts,
		"deposits":     settlementDeposits,
		"withdrawals":  settlementWithdrawals,
	})

	settlementUpdate := types.SettlementUpdate{
		BatchID:      localHash("batch", input.OldStateRoot, commitments.DepositsRoot, commitments.WithdrawalsRoot)[:18],
		OldStateRoot: input.OldStateRoot,
		NewStateRoot: newStateRoot,
		Deposits:     settlementDeposits,
		Withdrawals:  settlementWithdrawals,
	}

	return BuildOutput{
		SettlementUpdate: settlementUpdate,
		BatchCommitments: commitments,
		Witness: types.Witness{
			Accounts: witnessAccounts,
		},
	}, nil
}

type localAccountState struct {
	Owner      string
	Nonce      string
	OldBalance int64
	NewBalance int64
}

// buildLocalAccountStates is placeholder state transition logic.
//
// TODO(P3):
// Replace with the real off-chain state manager and circuit-compatible
// accounting rules.
func buildLocalAccountStates(
	deposits []types.DepositRecord,
	withdrawRequests []types.WithdrawRequest,
) ([]localAccountState, error) {
	type totals struct {
		owner         string
		nonce         string
		totalDeposit  int64
		totalWithdraw int64
	}

	byOwner := make(map[string]*totals)
	order := make([]string, 0)

	ensure := func(owner string) *totals {
		existing, ok := byOwner[owner]
		if ok {
			return existing
		}

		item := &totals{
			owner: owner,
			nonce: "0",
		}

		byOwner[owner] = item
		order = append(order, owner)

		return item
	}

	for _, deposit := range deposits {
		amount, err := parsePositiveAmount(deposit.Amount)
		if err != nil {
			return nil, fmt.Errorf("invalid deposit amount for %s: %w", deposit.DepositID, err)
		}

		item := ensure(deposit.Owner)
		item.totalDeposit += amount
	}

	for _, withdrawReq := range withdrawRequests {
		amount, err := parsePositiveAmount(withdrawReq.Amount)
		if err != nil {
			return nil, fmt.Errorf("invalid withdraw amount for %s: %w", withdrawReq.WithdrawID, err)
		}

		item := ensure(withdrawReq.Owner)
		item.totalWithdraw += amount
		item.nonce = withdrawReq.Nonce
	}

	states := make([]localAccountState, 0, len(order))

	for _, owner := range order {
		item := byOwner[owner]

		newBalance := item.totalDeposit - item.totalWithdraw
		if newBalance < 0 {
			return nil, ErrInsufficientOffchainBalance
		}

		states = append(states, localAccountState{
			Owner:      item.owner,
			Nonce:      item.nonce,
			OldBalance: 0,
			NewBalance: newBalance,
		})
	}

	return states, nil
}

func parsePositiveAmount(value string) (int64, error) {
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}

	if amount <= 0 {
		return 0, fmt.Errorf("amount must be positive")
	}

	return amount, nil
}

func collectNullifiers(withdrawals []types.SettlementWithdrawal) []string {
	nullifiers := make([]string, 0, len(withdrawals))

	for _, withdrawal := range withdrawals {
		nullifiers = append(nullifiers, withdrawal.Nullifier)
	}

	return nullifiers
}

func hashJSON(label string, value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return localHash(label, "marshal-error")
	}

	return localHash(label, string(raw))
}

func localHash(parts ...string) string {
	h := sha256.New()

	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}

	return "0x" + hex.EncodeToString(h.Sum(nil))
}
