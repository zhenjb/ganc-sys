package state

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

var (
	ErrInvalidOwner   = errors.New("state: owner is empty")
	ErrInvalidDenom   = errors.New("state: denom is empty")
	ErrInvalidAmount  = errors.New("state: amount is not a valid non-negative integer string")
	ErrAmountNegative = errors.New("state: amount must be > 0")
)

type accountKey struct {
	Owner string
	Denom string
}

func newAccountKey(owner, denom string) (accountKey, error) {
	owner = strings.TrimSpace(owner)
	denom = strings.TrimSpace(denom)
	if owner == "" {
		return accountKey{}, ErrInvalidOwner
	}
	if denom == "" {
		return accountKey{}, ErrInvalidDenom
	}
	return accountKey{Owner: owner, Denom: denom}, nil
}

type AccountState struct {
	mu       sync.RWMutex
	accounts map[accountKey]types.Account
}

func NewAccountState() *AccountState {
	return &AccountState{
		accounts: make(map[accountKey]types.Account),
	}
}

func (s *AccountState) Get(owner, denom string) (types.Account, bool) {
	key, err := newAccountKey(owner, denom)
	if err != nil {
		return types.Account{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	acc, ok := s.accounts[key]
	return acc, ok
}

func (s *AccountState) GetOrZero(owner, denom string) types.Account {
	if acc, ok := s.Get(owner, denom); ok {
		return acc
	}
	return types.Account{Owner: owner, Denom: denom, Balance: "0", Nonce: "0"}
}

func (s *AccountState) Credit(owner, denom, amount string) (types.Account, error) {
	key, err := newAccountKey(owner, denom)
	if err != nil {
		return types.Account{}, err
	}
	delta, err := parsePositiveAmount(amount)
	if err != nil {
		return types.Account{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	acc, ok := s.accounts[key]
	if !ok {
		acc = types.Account{Owner: key.Owner, Denom: key.Denom, Balance: "0", Nonce: "0"}
	}
	bal, err := parseNonNegativeAmount(acc.Balance)
	if err != nil {
		return types.Account{}, fmt.Errorf("corrupt balance for %s/%s: %w", key.Owner, key.Denom, err)
	}
	bal.Add(bal, delta)
	acc.Balance = bal.String()
	s.accounts[key] = acc
	return acc, nil
}

func (s *AccountState) Snapshot() []types.Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]types.Account, 0, len(s.accounts))
	for _, acc := range s.accounts {
		out = append(out, acc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner == out[j].Owner {
			return out[i].Denom < out[j].Denom
		}
		return out[i].Owner < out[j].Owner
	})
	return out
}

func parsePositiveAmount(amount string) (*big.Int, error) {
	v, err := parseNonNegativeAmount(amount)
	if err != nil {
		return nil, err
	}
	if v.Sign() == 0 {
		return nil, ErrAmountNegative
	}
	return v, nil
}

func parseNonNegativeAmount(amount string) (*big.Int, error) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return nil, ErrInvalidAmount
	}
	v, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		return nil, ErrInvalidAmount
	}
	if v.Sign() < 0 {
		return nil, ErrInvalidAmount
	}
	return v, nil
}
