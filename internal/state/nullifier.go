package state

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/hash"
)

// nullifierDomainTag is the hash-domain separator for the off-chain
// nullifier derivation. It MUST match the tag baked into the circuit
// (ZK-05) — when ZK-02 locks the final hash scheme (likely Poseidon),
// this tag bumps to "v1" and the canonical test vector is regenerated.
const nullifierDomainTag = "zkdex/nullifier/v0"

// ErrInvalidNullifierInput is returned when either userSecret or nonce
// is missing or malformed. Sentinel — callers chain with errors.Is.
var ErrInvalidNullifierInput = errors.New("state: invalid nullifier input")

// NullifierFor derives the deterministic withdrawal nullifier from a
// user secret and the withdrawal nonce. This is STATE-06 of the P3
// pipeline.
//
// Contract (canonical for the MVP):
//
//	nullifier = H( domain | userSecret | canonical(nonce) )
//
// where:
//   - H is SHA-256 as a placeholder. ZK-02 will swap H to whatever
//     circuit-friendly hash the final stack picks (Poseidon/MiMC). At
//     that point, bump nullifierDomainTag from "v0" to "v1" and
//     regenerate testvectors/alice_100_40/*.
//   - domain = "zkdex/nullifier/v0" is a fixed string. It
//     domain-separates this hash from other SHA-256 callsites
//     (deposit_id hash, tx-hash mock, future commitments) so the same
//     input bytes cannot collide across uses.
//   - The separator '|' between fields prevents
//     (secret="ab", nonce="1") from collapsing onto
//     (secret="a", nonce="b1") — a classic length-extension /
//     concatenation pitfall.
//   - canonical(nonce) is nonce reparsed through parseNonNegativeAmount
//     and stringified. This normalizes "01" → "1" so two semantically
//     identical nonces always produce the same nullifier.
//
// Output format: "0x"-prefixed lowercase hex (the encoding agreed for
// proof-bound fields in `zkdex_final_parallel_plan_fixed.html` —
// roots/nullifiers/proofs as hex strings).
//
// Pre-conditions:
//   - userSecret non-empty after trim. The secret is opaque bytes from
//     P3 / wallet; we do not interpret it, only require presence.
//   - nonce is a non-negative integer string. Negative or non-numeric
//     values return ErrInvalidNullifierInput. Empty rejected.
//
// Determinism: same (userSecret, nonce) inputs always return the same
// nullifier bytes. The function is pure — no LocalState, no mutex, no
// clock, no randomness.
//
// Idempotency role: the returned nullifier is the value
// LocalState.ApplyWithdrawal (STATE-05) uses as its idempotency key.
// Re-deriving with the same (secret, nonce) and calling
// ApplyWithdrawal twice returns ErrWithdrawAlreadyApplied on the
// second call.
func NullifierFor(userSecret, nonce string) (string, error) {
	userSecret = strings.TrimSpace(userSecret)
	if userSecret == "" {
		return "", fmt.Errorf("%w: userSecret is empty", ErrInvalidNullifierInput)
	}
	parsed, err := parseNonNegativeAmount(nonce)
	if err != nil {
		return "", fmt.Errorf("%w: nonce %q invalid: %v", ErrInvalidNullifierInput, nonce, err)
	}
	canonicalNonce := parsed.String()

	var b strings.Builder
	b.Grow(len(nullifierDomainTag) + 1 + len(userSecret) + 1 + len(canonicalNonce))
	b.WriteString(nullifierDomainTag)
	b.WriteByte('|')
	b.WriteString(userSecret)
	b.WriteByte('|')
	b.WriteString(canonicalNonce)
	return hash.SHA256Hex([]byte(b.String())), nil
}

// NullifierDomainTag exposes the domain tag for cross-role checks
// (P2 circuit, P4 backend) that need to assert the off-chain
// derivation has not silently bumped versions.
func NullifierDomainTag() string {
	return nullifierDomainTag
}

// withdrawSecretDomainTag domain-separates the per-owner MOCK withdrawal
// secret from every other SHA-256 callsite (nullifier, deposit-id, etc.).
const withdrawSecretDomainTag = "zkdex/withdraw-secret/v1"

// WithdrawSecretForOwner derives the per-owner MOCK secret that seeds a
// withdrawal nullifier (see NullifierFor). It replaces the single shared
// "mock-user-secret" literal whose GLOBAL reuse let two DIFFERENT owners
// collide on the same nullifier once a lockstep reset handed them the same
// withdrawal nonce (INT-WD-NULLIFIER-peruser).
//
// Properties:
//   - Deterministic: same owner → same secret, so the request-time nullifier,
//     the batch-rebuilt nullifier and the gazk prover's re-derivation all
//     agree (the prover reads UserSecret straight from the witness).
//   - Injective per owner: distinct owners → distinct secrets → each owner
//     gets an ISOLATED replay-protection namespace. Same owner+nonce still
//     yields the same nullifier, so genuine replay is still rejected.
//
// DE-MOCK SEAM: the nullifier FORMULA (NullifierFor) and the prover's binding
// check are UNCHANGED — only the SOURCE of the secret lives here. A later step
// swaps this owner-derived mock for a wallet-derived secret (ADR-036, parallel
// to the order-signature de-mock) WITHOUT touching NullifierFor or gazk.
//
// The output is opaque "0x"-prefixed hex (same shape a wallet-derived secret
// would take), so the witness UserSecret field does not change shape on
// de-mock. Owner is public, so embedding it in the preimage leaks nothing.
func WithdrawSecretForOwner(owner string) string {
	return hash.SHA256Hex([]byte(withdrawSecretDomainTag + "|" + strings.TrimSpace(owner)))
}
