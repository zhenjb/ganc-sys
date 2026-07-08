package state

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T02 — Reserved balance model.
//
// Trading must stop one balance from backing several orders, so each account's
// spendable funds are split into two buckets per denom:
//
//   - available  = types.Account.Balance  (spendable / withdrawable)
//   - reserved   = types.Account.Reserved (locked behind a resting order)
//
// The three primitives below move funds between the buckets. They are the
// literal Reserve/Release/Consume(owner, denom, amount) surface from the plan
// and operate on a single account atomically:
//
//	Reserve  available -= amount ; reserved += amount   (lock collateral)
//	Release  reserved  -= amount ; available += amount   (cancel / expire)
//	Consume  reserved  -= amount                         (fill: funds leave)
//
// Reserve/Release conserve available+reserved; Consume is the only primitive
// that reduces the total, because on a fill the locked collateral is paid to
// the counterparty (the credit side is applied by STATE-T05/T06 matching).
//
// The order-keyed registry that binds each reservation to its orderHash (so the
// exact amount is released for the exact order — see the pitfall in the plan)
// lives on LocalState / OffchainStateManager further below.

var (
	// ErrInsufficientAvailable is returned when a Reserve asks to lock more
	// than the account's available (Balance) amount. Sentinel — errors.Is.
	ErrInsufficientAvailable = errors.New("state: insufficient available balance to reserve")
	// ErrInsufficientReserved is returned when a Release/Consume asks to move
	// more than the account's reserved amount. Sentinel — errors.Is.
	ErrInsufficientReserved = errors.New("state: insufficient reserved balance")
)

// reservedBig parses acc.Reserved, treating "" as zero. A malformed reserved
// string is a corruption bug (we always write canonical big.Int strings), so it
// is surfaced as an error rather than silently zeroed.
func reservedBig(acc types.Account) (*big.Int, error) {
	if strings.TrimSpace(acc.Reserved) == "" {
		return big.NewInt(0), nil
	}
	return parseNonNegativeAmount(acc.Reserved)
}

// setReserved writes v into acc.Reserved, normalizing zero to "" so a
// fully-released account serializes byte-identically to a legacy (non-trading)
// account and its state root is unchanged.
func setReserved(acc *types.Account, v *big.Int) {
	if v.Sign() == 0 {
		acc.Reserved = ""
		return
	}
	acc.Reserved = v.String()
}

// Reserve moves `amount` from available (Balance) to reserved on the (owner,
// denom) account, atomically. Rejects with ErrInsufficientAvailable when
// available < amount. amount must be a positive integer string.
func (s *AccountState) Reserve(owner, denom, amount string) (types.Account, error) {
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
	avail, err := parseNonNegativeAmount(acc.Balance)
	if err != nil {
		return types.Account{}, fmt.Errorf("corrupt balance for %s/%s: %w", key.Owner, key.Denom, err)
	}
	if avail.Cmp(delta) < 0 {
		return types.Account{}, fmt.Errorf("%w: have %s, want %s", ErrInsufficientAvailable, avail.String(), delta.String())
	}
	reserved, err := reservedBig(acc)
	if err != nil {
		return types.Account{}, fmt.Errorf("corrupt reserved for %s/%s: %w", key.Owner, key.Denom, err)
	}

	avail.Sub(avail, delta)
	reserved.Add(reserved, delta)
	acc.Balance = avail.String()
	setReserved(&acc, reserved)

	s.accounts[key] = acc
	return acc, nil
}

// Release moves `amount` from reserved back to available (Balance), atomically —
// the cancel / expire path. Rejects with ErrInsufficientReserved when reserved <
// amount. amount must be a positive integer string.
func (s *AccountState) Release(owner, denom, amount string) (types.Account, error) {
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
		return types.Account{}, fmt.Errorf("%w: no reserved for %s/%s", ErrInsufficientReserved, key.Owner, key.Denom)
	}
	reserved, err := reservedBig(acc)
	if err != nil {
		return types.Account{}, fmt.Errorf("corrupt reserved for %s/%s: %w", key.Owner, key.Denom, err)
	}
	if reserved.Cmp(delta) < 0 {
		return types.Account{}, fmt.Errorf("%w: have %s, want %s", ErrInsufficientReserved, reserved.String(), delta.String())
	}
	avail, err := parseNonNegativeAmount(acc.Balance)
	if err != nil {
		return types.Account{}, fmt.Errorf("corrupt balance for %s/%s: %w", key.Owner, key.Denom, err)
	}

	reserved.Sub(reserved, delta)
	avail.Add(avail, delta)
	acc.Balance = avail.String()
	setReserved(&acc, reserved)

	s.accounts[key] = acc
	return acc, nil
}

// Consume removes `amount` from reserved WITHOUT crediting available — the fill
// path. The locked collateral leaves this account (it is paid to the
// counterparty by the matching apply step). Rejects with ErrInsufficientReserved
// when reserved < amount. amount must be a positive integer string.
func (s *AccountState) Consume(owner, denom, amount string) (types.Account, error) {
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
		return types.Account{}, fmt.Errorf("%w: no reserved for %s/%s", ErrInsufficientReserved, key.Owner, key.Denom)
	}
	reserved, err := reservedBig(acc)
	if err != nil {
		return types.Account{}, fmt.Errorf("corrupt reserved for %s/%s: %w", key.Owner, key.Denom, err)
	}
	if reserved.Cmp(delta) < 0 {
		return types.Account{}, fmt.Errorf("%w: have %s, want %s", ErrInsufficientReserved, reserved.String(), delta.String())
	}

	reserved.Sub(reserved, delta)
	setReserved(&acc, reserved)

	s.accounts[key] = acc
	return acc, nil
}

// ReserveDenomForSide returns which denom an order of the given side must lock:
// a buy locks the market's QUOTE denom (you pay quote to receive base), a sell
// locks the BASE denom (you deliver base). This is the buy/sell binding of the
// Reserved balance Agreement. The actual integer collateral amount (price*qty
// for buys, qty for sells, in smallest units) is computed by order validation
// (STATE-T03) and passed to Reserve — this helper only picks the denom.
func ReserveDenomForSide(market types.Market, side types.OrderSide) (string, error) {
	switch side {
	case types.SideBuy:
		if strings.TrimSpace(market.QuoteDenom) == "" {
			return "", fmt.Errorf("%w: market %q has empty quoteDenom", ErrInvalidDenom, market.Market)
		}
		return market.QuoteDenom, nil
	case types.SideSell:
		if strings.TrimSpace(market.BaseDenom) == "" {
			return "", fmt.Errorf("%w: market %q has empty baseDenom", ErrInvalidDenom, market.Market)
		}
		return market.BaseDenom, nil
	default:
		return "", fmt.Errorf("%w: unknown side %q", types.ErrInvalidOrder, side)
	}
}

// ---------------------------------------------------------------------------
// Order-keyed reservation registry (LocalState level).
//
// Each Reserve records a types.Reservation keyed by orderHash so the exact
// amount can be released for the exact order on cancel/expire (ReleaseOrder) or
// drawn down as the order fills (ConsumeOrder, partial-fill aware). This is the
// integrity layer the plan's pitfall calls for: "Reservation gắn với orderHash
// để release đúng phần của đúng order."
// ---------------------------------------------------------------------------

var (
	// ErrReservationExists is returned when Reserve is called with an
	// orderHash that already has a live reservation. Sentinel — errors.Is.
	ErrReservationExists = errors.New("state: reservation already exists for orderHash")
	// ErrReservationNotFound is returned when Release/Consume references an
	// orderHash with no live reservation. Sentinel — errors.Is.
	ErrReservationNotFound = errors.New("state: reservation not found for orderHash")
	// ErrInvalidReservation is returned when reservation inputs are malformed
	// (e.g. empty orderHash). Sentinel — errors.Is.
	ErrInvalidReservation = errors.New("state: invalid reservation input")
)

// Reserve locks `amount` of `denom` collateral for the order identified by
// `orderHash`, advancing the pending root. It records a Reservation so the lock
// can later be released or consumed by orderHash. Rejects a duplicate orderHash
// (ErrReservationExists) and insufficient available (ErrInsufficientAvailable).
func (s *LocalState) Reserve(owner, denom, amount, orderHash string) (string, error) {
	owner = strings.TrimSpace(owner)
	denom = strings.TrimSpace(denom)
	orderHash = strings.TrimSpace(orderHash)
	if orderHash == "" {
		return "", fmt.Errorf("%w: orderHash is empty", ErrInvalidReservation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.reservations[orderHash]; exists {
		return "", fmt.Errorf("%w: orderHash=%s", ErrReservationExists, orderHash)
	}
	if _, err := s.accounts.Reserve(owner, denom, amount); err != nil {
		return "", err
	}
	// Store the canonical (parsed) amount so the registry total always matches
	// what was moved into reserved.
	parsed, _ := parsePositiveAmount(amount)
	s.reservations[orderHash] = types.Reservation{
		Owner:     owner,
		Denom:     denom,
		Amount:    parsed.String(),
		OrderHash: orderHash,
	}
	s.root = ComputeRoot(s.accounts.Snapshot())
	return s.root, nil
}

// ReleaseOrder returns the FULL remaining reserved amount of `orderHash` to
// available and drops the reservation — the cancel / expire path. Advances the
// pending root. Rejects an unknown orderHash (ErrReservationNotFound).
func (s *LocalState) ReleaseOrder(orderHash string) (string, error) {
	orderHash = strings.TrimSpace(orderHash)

	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.reservations[orderHash]
	if !ok {
		return "", fmt.Errorf("%w: orderHash=%s", ErrReservationNotFound, orderHash)
	}
	if _, err := s.accounts.Release(r.Owner, r.Denom, r.Amount); err != nil {
		return "", err
	}
	delete(s.reservations, orderHash)
	s.root = ComputeRoot(s.accounts.Snapshot())
	return s.root, nil
}

// ConsumeOrder draws `amount` down from `orderHash`'s reservation (a fill),
// removing it from reserved. Supports partial fills: any remaining reserved
// amount stays locked and the reservation persists; a full consume drops it.
// Advances the pending root. Rejects an unknown orderHash
// (ErrReservationNotFound) or amount > remaining (ErrInsufficientReserved).
func (s *LocalState) ConsumeOrder(orderHash, amount string) (string, error) {
	orderHash = strings.TrimSpace(orderHash)
	delta, err := parsePositiveAmount(amount)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.reservations[orderHash]
	if !ok {
		return "", fmt.Errorf("%w: orderHash=%s", ErrReservationNotFound, orderHash)
	}
	remaining, err := parseNonNegativeAmount(r.Amount)
	if err != nil {
		return "", fmt.Errorf("corrupt reservation amount for orderHash=%s: %w", orderHash, err)
	}
	if remaining.Cmp(delta) < 0 {
		return "", fmt.Errorf("%w: reservation has %s, want %s", ErrInsufficientReserved, remaining.String(), delta.String())
	}
	if _, err := s.accounts.Consume(r.Owner, r.Denom, amount); err != nil {
		return "", err
	}
	remaining.Sub(remaining, delta)
	if remaining.Sign() == 0 {
		delete(s.reservations, orderHash)
	} else {
		r.Amount = remaining.String()
		s.reservations[orderHash] = r
	}
	s.root = ComputeRoot(s.accounts.Snapshot())
	return s.root, nil
}

// Reservation returns the live reservation for orderHash, if any.
func (s *LocalState) Reservation(orderHash string) (types.Reservation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reservations[strings.TrimSpace(orderHash)]
	return r, ok
}

// Reservations returns all live reservations sorted by orderHash (deterministic).
func (s *LocalState) Reservations() []types.Reservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedReservations(s.reservations)
}

func sortedReservations(in map[string]types.Reservation) []types.Reservation {
	out := make([]types.Reservation, 0, len(in))
	for _, r := range in {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrderHash < out[j].OrderHash })
	return out
}

func copyReservations(in map[string]types.Reservation) map[string]types.Reservation {
	out := make(map[string]types.Reservation, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
