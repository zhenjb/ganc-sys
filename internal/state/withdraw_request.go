package state

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

var (
	ErrInvalidWithdrawIntent = errors.New("state: invalid withdraw intent")
	ErrInsufficientBalance   = errors.New("state: insufficient balance for withdraw")
)

// WithdrawIntent is the user-supplied withdraw input.
//
// It is the strict subset of fields P4/P5 collect from the user before P3
// assigns the engine-side identifiers (withdrawId, nonce) and before the
// wallet attaches a signature. Keeping intent separate from
// types.WithdrawRequest prevents callers from forging a withdrawId/nonce.
type WithdrawIntent struct {
	Owner       string
	Denom       string
	Amount      string
	Destination string
}

// WithdrawRequestBuilder produces deterministic, sequentially-numbered
// WithdrawRequest records from user intents.
//
// Read-only with respect to LocalState: balance and nonce are snapshotted
// at build time but not mutated. STATE-05 ApplyWithdrawal is the only
// function that debits balance and increments Account.Nonce.
//
// Nonce in the produced request is the post-increment value
// (Account.Nonce + 1). This is the value STATE-06 will feed into
// nullifier = Hash(userSecret, nonce) and what STATE-05 will write back
// to Account.Nonce, so the proof and the on-chain account agree.
type WithdrawRequestBuilder struct {
	state *LocalState
	mu    sync.Mutex
	seq   uint64
}

func NewWithdrawRequestBuilder(state *LocalState) *WithdrawRequestBuilder {
	return &WithdrawRequestBuilder{state: state}
}

// Build validates intent and produces a WithdrawRequest with assigned
// withdrawId and nonce. Signature is left empty for MVP; the wallet
// layer (P4/P5) attaches it after the user signs.
//
// Calling Build twice for the same account without STATE-05 processing
// the first request will produce two requests with the same nonce
// (because Account.Nonce hasn't moved). The caller is responsible for
// sequencing requests through STATE-05.
func (b *WithdrawRequestBuilder) Build(intent WithdrawIntent) (types.WithdrawRequest, error) {
	intent.Owner = strings.TrimSpace(intent.Owner)
	intent.Denom = strings.TrimSpace(intent.Denom)
	intent.Destination = strings.TrimSpace(intent.Destination)
	intent.Amount = strings.TrimSpace(intent.Amount)

	if intent.Owner == "" {
		return types.WithdrawRequest{}, fmt.Errorf("%w: owner is empty", ErrInvalidWithdrawIntent)
	}
	if intent.Denom == "" {
		return types.WithdrawRequest{}, fmt.Errorf("%w: denom is empty", ErrInvalidWithdrawIntent)
	}
	if intent.Destination == "" {
		return types.WithdrawRequest{}, fmt.Errorf("%w: destination is empty", ErrInvalidWithdrawIntent)
	}
	amount, err := parsePositiveAmount(intent.Amount)
	if err != nil {
		return types.WithdrawRequest{}, fmt.Errorf("%w: amount %q invalid: %v", ErrInvalidWithdrawIntent, intent.Amount, err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	acc := b.state.Account(intent.Owner, intent.Denom)

	bal, err := parseNonNegativeAmount(acc.Balance)
	if err != nil {
		return types.WithdrawRequest{}, fmt.Errorf("corrupt balance for %s/%s: %w", intent.Owner, intent.Denom, err)
	}
	if bal.Cmp(amount) < 0 {
		return types.WithdrawRequest{}, fmt.Errorf("%w: have %s, want %s", ErrInsufficientBalance, bal.String(), amount.String())
	}

	currentNonce, err := parseNonNegativeAmount(acc.Nonce)
	if err != nil {
		return types.WithdrawRequest{}, fmt.Errorf("corrupt nonce for %s/%s: %w", intent.Owner, intent.Denom, err)
	}
	nextNonce := new(big.Int).Add(currentNonce, big.NewInt(1)).String()

	b.seq++
	return types.WithdrawRequest{
		WithdrawID:  "wd-" + strconv.FormatUint(b.seq, 10),
		Owner:       intent.Owner,
		Denom:       intent.Denom,
		Amount:      amount.String(),
		Destination: intent.Destination,
		Nonce:       nextNonce,
		Signature:   "",
	}, nil
}

// Seq returns the number of withdraw requests built by this builder
// (testing/debug; not part of the STATE-04 contract).
func (b *WithdrawRequestBuilder) Seq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}
