package state

import (
	"errors"
	"fmt"
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

var (
	ErrDepositAlreadyApplied = errors.New("state: deposit already applied")
	ErrInvalidDepositRecord  = errors.New("state: invalid deposit record")
)

// LocalState is the off-chain mirror managed by P3.
//
// It holds the pending account balances and the corresponding pending root.
// The root advances every time a deposit/withdraw is applied locally; on-chain
// `currentStateRoot` only catches up once `MsgSubmitBatchProof` is accepted.
type LocalState struct {
	mu              sync.Mutex
	accounts        *AccountState
	root            string
	appliedDeposits map[string]struct{}
}

// NewLocalState initializes an empty off-chain state.
//
// The initial root is derived from the empty account set so the chain genesis
// (P1 ONCHAIN-03) and the off-chain mirror agree without a hard-coded seed.
func NewLocalState() *LocalState {
	accounts := NewAccountState()
	return &LocalState{
		accounts:        accounts,
		root:            ComputeRoot(accounts.Snapshot()),
		appliedDeposits: make(map[string]struct{}),
	}
}

func (s *LocalState) Root() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root
}

func (s *LocalState) Account(owner, denom string) types.Account {
	return s.accounts.GetOrZero(owner, denom)
}

func (s *LocalState) Snapshot() []types.Account {
	return s.accounts.Snapshot()
}

// ApplyDeposit credits the off-chain balance for the deposit and advances the
// pending root.
//
// The chain-side `DepositRecord` is the source of truth; this function is
// idempotency-guarded by depositId so an indexer replaying events cannot
// double-credit. Returns the new pending root.
func (s *LocalState) ApplyDeposit(d types.DepositRecord) (string, error) {
	if err := validateDeposit(d); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.appliedDeposits[d.DepositID]; ok {
		return "", fmt.Errorf("%w: depositId=%s", ErrDepositAlreadyApplied, d.DepositID)
	}

	if _, err := s.accounts.Credit(d.Owner, d.Denom, d.Amount); err != nil {
		return "", fmt.Errorf("credit %s/%s: %w", d.Owner, d.Denom, err)
	}

	s.appliedDeposits[d.DepositID] = struct{}{}
	s.root = ComputeRoot(s.accounts.Snapshot())
	return s.root, nil
}

func (s *LocalState) IsDepositApplied(depositID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.appliedDeposits[depositID]
	return ok
}

func validateDeposit(d types.DepositRecord) error {
	if d.DepositID == "" {
		return fmt.Errorf("%w: depositId is empty", ErrInvalidDepositRecord)
	}
	if d.Owner == "" {
		return fmt.Errorf("%w: owner is empty", ErrInvalidDepositRecord)
	}
	if d.Denom == "" {
		return fmt.Errorf("%w: denom is empty", ErrInvalidDepositRecord)
	}
	if _, err := parsePositiveAmount(d.Amount); err != nil {
		return fmt.Errorf("%w: amount %q invalid: %v", ErrInvalidDepositRecord, d.Amount, err)
	}
	return nil
}
