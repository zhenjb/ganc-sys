package batch

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ErrInvalidWitnessInputs is the sentinel returned whenever the fields
// of WitnessInputs are missing, malformed, or mutually inconsistent.
// Callers chain with errors.Is.
//
// As with ErrInvalidSettlementInputs, a single sentinel keeps the
// P4 surface area trivial (HTTP 400 with the underlying message).
var ErrInvalidWitnessInputs = errors.New("batch: invalid witness inputs")

// WitnessInputs is the read-only bundle the off-chain caller hands to
// WitnessBuilder. It deliberately reuses SettlementInputs (the bundle
// STATE-08 already validated) so the witness and the SettlementUpdate
// cannot drift in production — they are built from the exact same
// source of truth in one orchestration step.
//
// Extra fields beyond SettlementInputs:
//
//   - UserSecret: opaque bytes the wallet/P3 owns. The nullifier is
//     bound to (userSecret, withdraw.nonce); leaking the secret would
//     let an attacker forge nullifiers. This field MUST NOT escape
//     into any artifact P3 publishes (SettlementUpdate, public inputs,
//     batch metadata) — only into witness.json which stays on the
//     prover host.
//
//   - OldBalance: account balance for
//     (Settlement.Withdraw.Owner, Settlement.Withdraw.Denom) BEFORE
//     the batch's deposit-then-withdraw is applied to LocalState. For
//     the canonical Alice 100/40 vector, this is "0".
//
//   - NewBalance: same account balance AFTER the batch. For Alice
//     100/40, this is "60".
//
//   - StatePath: optional merkle-style witness path. ZK-06 uses a
//     simplified state model in the MVP, so this slot is nil/empty
//     until ZK-02 locks the final commitment scheme.
type WitnessInputs struct {
	UserSecret string
	Settlement SettlementInputs
	OldBalance string
	NewBalance string
	StatePath  []string
}

// WitnessBuilder produces canonical Witness records consumed by the
// P2 prover (ZK-09). It has no mutable state — concurrent calls are
// safe — because the witness is pure data and there is no monotonic
// counter to maintain (the BatchID lives on the SettlementUpdate
// side).
type WitnessBuilder struct{}

func NewWitnessBuilder() *WitnessBuilder { return &WitnessBuilder{} }

// Build validates WitnessInputs and assembles a canonical Witness.
//
// Validation pipeline (any failure returns ErrInvalidWitnessInputs
// wrapped with a human-readable cause; no partial output on failure):
//
//  1. UserSecret non-empty after trim.
//  2. Settlement.Withdraw.Nonce parses as a non-negative integer.
//  3. OldBalance / NewBalance parse as non-negative integers.
//  4. Settlement.Deposit.Amount / Settlement.Withdraw.Amount parse as
//     positive integers (mirrors the SettlementUpdateBuilder gate; if
//     STATE-08 passed already, this is belt-and-braces).
//  5. Settlement.Deposit.Denom == Settlement.Withdraw.Denom. Mixed
//     denom would make the ZK-04 balance constraint meaningless.
//  6. ZK-04 balance constraint:
//     newBalance + withdrawAmount == oldBalance + depositAmount
//     A drift between LocalState's actual transition and the witness
//     surfaces here (e.g. caller debited withdrawAmount twice, or
//     forgot to credit the deposit) before the prover spends cycles.
//  7. ZK-05 nullifier constraint (defense-in-depth):
//     state.NullifierFor(userSecret, withdraw.Nonce) == Settlement.Nullifier
//     Re-derives using the same domain tag the circuit will enforce.
//     If the caller passed a stale or wrong (secret, nonce) pair, the
//     resulting proof would be invalid — fail fast here, save a
//     prover round-trip.
//
// Post-conditions on success:
//   - Returned Witness.Nonce / OldBalance / NewBalance are canonical
//     big.Int strings ("01" → "1"). Matches STATE-06 nonce
//     canonicalization.
//   - UserSecret is preserved verbatim (opaque bytes; trimming would
//     silently rewrite secret material).
//   - StatePath is a defensive copy when non-empty; nil when caller
//     supplied none — JSON `omitempty` matches the agreed schema.
//
// The function does NOT mutate Settlement or any field inside it.
func (b *WitnessBuilder) Build(in WitnessInputs) (types.Witness, error) {
	secret := strings.TrimSpace(in.UserSecret)
	if secret == "" {
		return types.Witness{}, fmt.Errorf("%w: userSecret is empty", ErrInvalidWitnessInputs)
	}

	nonce, err := parseNonNegative(in.Settlement.Withdraw.Nonce)
	if err != nil {
		return types.Witness{}, fmt.Errorf("%w: withdraw.nonce %q invalid: %v",
			ErrInvalidWitnessInputs, in.Settlement.Withdraw.Nonce, err)
	}
	oldBal, err := parseNonNegative(in.OldBalance)
	if err != nil {
		return types.Witness{}, fmt.Errorf("%w: oldBalance %q invalid: %v",
			ErrInvalidWitnessInputs, in.OldBalance, err)
	}
	newBal, err := parseNonNegative(in.NewBalance)
	if err != nil {
		return types.Witness{}, fmt.Errorf("%w: newBalance %q invalid: %v",
			ErrInvalidWitnessInputs, in.NewBalance, err)
	}
	depAmt, err := parsePositive(in.Settlement.Deposit.Amount)
	if err != nil {
		return types.Witness{}, fmt.Errorf("%w: deposit.amount %q invalid: %v",
			ErrInvalidWitnessInputs, in.Settlement.Deposit.Amount, err)
	}
	wdAmt, err := parsePositive(in.Settlement.Withdraw.Amount)
	if err != nil {
		return types.Witness{}, fmt.Errorf("%w: withdraw.amount %q invalid: %v",
			ErrInvalidWitnessInputs, in.Settlement.Withdraw.Amount, err)
	}

	if in.Settlement.Deposit.Denom != in.Settlement.Withdraw.Denom {
		return types.Witness{}, fmt.Errorf(
			"%w: deposit.denom=%q != withdraw.denom=%q (mixed-denom witness not supported in MVP)",
			ErrInvalidWitnessInputs, in.Settlement.Deposit.Denom, in.Settlement.Withdraw.Denom,
		)
	}

	// ZK-04: newBalance + withdrawAmount == oldBalance + depositAmount
	lhs := new(big.Int).Add(newBal, wdAmt)
	rhs := new(big.Int).Add(oldBal, depAmt)
	if lhs.Cmp(rhs) != 0 {
		return types.Witness{}, fmt.Errorf(
			"%w: balance transition violated (ZK-04): newBalance(%s)+withdrawAmount(%s)=%s != oldBalance(%s)+depositAmount(%s)=%s",
			ErrInvalidWitnessInputs,
			newBal.String(), wdAmt.String(), lhs.String(),
			oldBal.String(), depAmt.String(), rhs.String(),
		)
	}

	// ZK-05: nullifier == Hash(domain | userSecret | canonical(nonce))
	rederived, err := state.NullifierFor(secret, nonce.String())
	if err != nil {
		return types.Witness{}, fmt.Errorf(
			"%w: cannot re-derive nullifier: %v",
			ErrInvalidWitnessInputs, err,
		)
	}
	if rederived != in.Settlement.Nullifier {
		return types.Witness{}, fmt.Errorf(
			"%w: nullifier mismatch (ZK-05): supplied=%s, re-derived(userSecret, nonce=%s)=%s",
			ErrInvalidWitnessInputs, in.Settlement.Nullifier, nonce.String(), rederived,
		)
	}

	var statePath []string
	if len(in.StatePath) > 0 {
		statePath = append([]string(nil), in.StatePath...)
	}

	return types.Witness{
		UserSecret: secret,
		Nonce:      nonce.String(),
		OldBalance: oldBal.String(),
		NewBalance: newBal.String(),
		StatePath:  statePath,
	}, nil
}
