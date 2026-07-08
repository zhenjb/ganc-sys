package state

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T03 — order hashing (canonical order commitment + replay nullifier).
//
// Both hashes are MVP placeholders using domain-separated SHA-256, mirroring
// nullifier.go / withdraw_address_hash.go. ZK-T01 locks the in-circuit hash
// (MiMC/Poseidon on BN254); when it does, bump the version tags here in lockstep
// and regenerate the trade test vectors so P1/P3/P4 stay bit-exact.

// orderNullifierDomainTag domain-separates the order replay nullifier from every
// other SHA-256 callsite (withdrawal nullifier, deposit-id hash, order hash).
const orderNullifierDomainTag = "zkdex/orderNullifier/v0"

// ErrInvalidOrderHashInput is returned when order-hash / nullifier inputs are
// malformed. Sentinel — callers chain with errors.Is.
var ErrInvalidOrderHashInput = errors.New("state: invalid order hash input")

// OrderHash derives the deterministic commitment to a SignedOrder:
//
//	orderHash = SHA256( order.CanonicalBytes() )
//
// The preimage is the canonical order bytes (STATE-T01), whose first line is the
// version domain tag "zkdex/order/v0" — that tag is what domain-separates this
// hash and pins the field layout. The signature is deliberately NOT in the
// preimage (a signature cannot cover itself), so orderHash is stable across
// (re)signings of the same logical order.
//
// Output: "0x"-prefixed lowercase hex. Deterministic and pure — same order
// always yields the same hash, so P2 can reproduce it in-circuit.
func OrderHash(order types.SignedOrder) (string, error) {
	canonical, err := order.CanonicalBytes()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidOrderHashInput, err)
	}
	return hash.SHA256Hex(canonical), nil
}

// OrderNullifierFor derives the replay-protection nullifier for an order:
//
//	orderNullifier = SHA256( domain | owner | orderHash )
//
// It binds the owner to the order commitment so a used order (filled or
// cancelled) can never be replayed: STATE-T03 rejects any order whose nullifier
// is already recorded. The '|' separators prevent (owner="ab", hash="c") from
// colliding with (owner="a", hash="bc").
//
// owner and orderHash must be non-empty after trim. Output is "0x"-prefixed
// lowercase hex. Deterministic and pure.
func OrderNullifierFor(owner, orderHash string) (string, error) {
	owner = strings.TrimSpace(owner)
	orderHash = strings.TrimSpace(orderHash)
	if owner == "" {
		return "", fmt.Errorf("%w: owner is empty", ErrInvalidOrderHashInput)
	}
	if orderHash == "" {
		return "", fmt.Errorf("%w: orderHash is empty", ErrInvalidOrderHashInput)
	}

	var b strings.Builder
	b.Grow(len(orderNullifierDomainTag) + 2 + len(owner) + len(orderHash))
	b.WriteString(orderNullifierDomainTag)
	b.WriteByte('|')
	b.WriteString(owner)
	b.WriteByte('|')
	b.WriteString(orderHash)
	return hash.SHA256Hex([]byte(b.String())), nil
}

// OrderNullifierDomainTag exposes the domain tag for cross-role checks (P1
// on-chain order-nullifier store, P2 circuit) that must assert the off-chain
// derivation has not silently bumped versions.
func OrderNullifierDomainTag() string {
	return orderNullifierDomainTag
}
