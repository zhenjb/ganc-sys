package state

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

var (
	ErrInvalidWithdrawRequest = errors.New("state: invalid withdraw request")
	ErrWithdrawAlreadyApplied = errors.New("state: withdraw already applied (nullifier replay)")
	ErrNonceMismatch          = errors.New("state: withdraw request nonce does not match account next-nonce")
)

// ApplyWithdrawal debits the off-chain balance, advances the account nonce,
// and recomputes the pending state root for a previously-built
// WithdrawRequest. This is STATE-05 of the P3 pipeline.
//
// `nullifier` is the deterministic identifier produced by STATE-06
// (`Hash(userSecret, request.Nonce)`). It is the idempotency key:
// re-applying the same nullifier returns ErrWithdrawAlreadyApplied.
// We accept it as an explicit argument so STATE-05 stays decoupled from
// the hash scheme chosen by STATE-06/ZK-02.
//
// Pre-conditions (enforced):
//   - WithdrawRequest fields well-formed (owner/denom/destination non-empty,
//     amount > 0 numeric, nonce >= 0 numeric, withdrawId non-empty).
//   - nullifier non-empty (after trim).
//   - nullifier not previously applied on this LocalState.
//   - account balance >= request.Amount (re-checked even if STATE-04 already
//     vetted it — the request may sit in a queue and state may have moved).
//   - request.Nonce == account.Nonce + 1 (strict sequencing; guards against
//     stale requests and replay with a fresh nullifier).
//
// Post-conditions on success:
//   - account.Balance = old.Balance - request.Amount
//   - account.Nonce   = request.Nonce
//   - LocalState.root advances to the new snapshot hash (rootC).
//   - nullifier marked applied (idempotency).
//
// On any failure, LocalState is byte-identical to its pre-call state — no
// partial mutations because ComputeRoot only runs after the Debit succeeds
// and the nullifier is only recorded last.
func (s *LocalState) ApplyWithdrawal(req types.WithdrawRequest, nullifier string) (string, error) {
	if err := validateWithdrawRequest(req); err != nil {
		return "", err
	}
	nullifier = strings.TrimSpace(nullifier)
	if nullifier == "" {
		return "", fmt.Errorf("%w: nullifier is empty", ErrInvalidWithdrawRequest)
	}

	amount, err := parsePositiveAmount(req.Amount)
	if err != nil {
		return "", fmt.Errorf("%w: amount %q invalid: %v", ErrInvalidWithdrawRequest, req.Amount, err)
	}
	reqNonce, err := parseNonNegativeAmount(req.Nonce)
	if err != nil {
		return "", fmt.Errorf("%w: nonce %q invalid: %v", ErrInvalidWithdrawRequest, req.Nonce, err)
	}

	owner := strings.TrimSpace(req.Owner)
	denom := strings.TrimSpace(req.Denom)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.appliedNullifiers[nullifier]; ok {
		return "", fmt.Errorf("%w: nullifier=%s", ErrWithdrawAlreadyApplied, nullifier)
	}

	acc := s.accounts.GetOrZero(owner, denom)
	bal, err := parseNonNegativeAmount(acc.Balance)
	if err != nil {
		return "", fmt.Errorf("corrupt balance for %s/%s: %w", owner, denom, err)
	}
	if bal.Cmp(amount) < 0 {
		return "", fmt.Errorf("%w: have %s, want %s", ErrInsufficientBalance, bal.String(), amount.String())
	}

	accNonce, err := parseNonNegativeAmount(acc.Nonce)
	if err != nil {
		return "", fmt.Errorf("corrupt nonce for %s/%s: %w", owner, denom, err)
	}
	expected := new(big.Int).Add(accNonce, big.NewInt(1))
	if reqNonce.Cmp(expected) != 0 {
		return "", fmt.Errorf("%w: account.Nonce=%s, request.Nonce=%s, expected=%s",
			ErrNonceMismatch, accNonce.String(), reqNonce.String(), expected.String())
	}

	if _, err := s.accounts.Debit(owner, denom, req.Amount); err != nil {
		return "", fmt.Errorf("debit %s/%s: %w", owner, denom, err)
	}

	s.appliedNullifiers[nullifier] = struct{}{}
	s.root = ComputeRoot(s.accounts.Snapshot())
	return s.root, nil
}

// IsNullifierApplied reports whether the given nullifier has been
// consumed by a prior ApplyWithdrawal. Mirrors IsDepositApplied so
// callers (P4 query layer, replay logic) can probe without attempting
// a state mutation.
func (s *LocalState) IsNullifierApplied(nullifier string) bool {
	nullifier = strings.TrimSpace(nullifier)
	if nullifier == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.appliedNullifiers[nullifier]
	return ok
}

func validateWithdrawRequest(req types.WithdrawRequest) error {
	if strings.TrimSpace(req.WithdrawID) == "" {
		return fmt.Errorf("%w: withdrawId is empty", ErrInvalidWithdrawRequest)
	}
	if strings.TrimSpace(req.Owner) == "" {
		return fmt.Errorf("%w: owner is empty", ErrInvalidWithdrawRequest)
	}
	if strings.TrimSpace(req.Denom) == "" {
		return fmt.Errorf("%w: denom is empty", ErrInvalidWithdrawRequest)
	}
	if strings.TrimSpace(req.Destination) == "" {
		return fmt.Errorf("%w: destination is empty", ErrInvalidWithdrawRequest)
	}
	if _, err := parsePositiveAmount(req.Amount); err != nil {
		return fmt.Errorf("%w: amount %q invalid: %v", ErrInvalidWithdrawRequest, req.Amount, err)
	}
	if _, err := parseNonNegativeAmount(req.Nonce); err != nil {
		return fmt.Errorf("%w: nonce %q invalid: %v", ErrInvalidWithdrawRequest, req.Nonce, err)
	}
	return nil
}
