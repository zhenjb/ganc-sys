package state

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/hash"
)

// withdrawAddressDomainTag is the hash-domain separator for the off-chain
// withdrawal-address derivation. It MUST match the tag baked into the
// circuit (ZK-07) — when ZK-02 locks the final hash scheme (likely
// Poseidon), this tag bumps to "v1" and the canonical test vector is
// regenerated alongside the nullifier vector.
//
// The version is independent of nullifierDomainTag so the two derivations
// can evolve separately; in practice they will bump together when ZK-02
// finalizes the circuit hash, but a separate constant avoids accidentally
// coupling them (and accidentally making (secret, nonce) and destination
// inputs collide if the field separator is ever dropped).
const withdrawAddressDomainTag = "zkdex/withdrawAddr/v0"

// ErrInvalidWithdrawAddress is returned when destination is missing after
// trim. Sentinel — callers chain with errors.Is.
var ErrInvalidWithdrawAddress = errors.New("state: invalid withdraw address")

// WithdrawAddressHash derives the deterministic hash of a withdrawal
// destination address. This is STATE-07 of the P3 pipeline.
//
// Contract (canonical for the MVP):
//
//	withdrawAddressHash = H( domain | canonical(destination) )
//
// where:
//   - H is SHA-256 as a placeholder. ZK-02 will swap H to whatever
//     circuit-friendly hash the final stack picks (Poseidon/MiMC). At
//     that point, bump withdrawAddressDomainTag from "v0" to "v1" and
//     regenerate testvectors/alice_100_40/withdraw_address_hash_wd_1.json.
//   - domain = "zkdex/withdrawAddr/v0" is a fixed string. It
//     domain-separates this hash from other SHA-256 callsites
//     (nullifier, deposit_id hash, tx-hash mock) so the same input bytes
//     cannot collide across uses.
//   - canonical(destination) = strings.TrimSpace(destination). This
//     matches the normalization WithdrawRequestBuilder (STATE-04) applies
//     before storing Destination in the request; the two MUST agree so
//     that the on-chain verifier and the prover see identical bytes.
//
// Output format: "0x"-prefixed lowercase hex (the encoding agreed for
// proof-bound fields in `zkdex_final_parallel_plan_fixed.html` —
// roots/nullifiers/proofs as hex strings).
//
// Pre-conditions:
//   - destination non-empty after trim. The address is opaque bytes from
//     P4/wallet; we do not interpret bech32 or validate prefix — that is
//     the chain's job at claim time (`x/bank.SendCoinsFromModuleToAccount`
//     rejects malformed addresses). All this function guarantees is that
//     the bytes the circuit hashes equal the bytes the chain verifier
//     will hash.
//
// Determinism: same destination input always returns the same hash bytes.
// The function is pure — no LocalState, no mutex, no clock, no randomness.
//
// Tampering role: the returned hash is bound into the SettlementUpdate
// (STATE-08, field `withdrawAddressHash`). If a relayer rewrites
// `WithdrawAddress` between the prover and the chain, the chain
// re-derives the hash from the rewritten address, the public input
// changes, and proof verification fails (ZK-07).
//
// Why a separate function from the nullifier helper: destination is a
// public, attacker-controlled field; userSecret is private. Keeping the
// derivations in separate files with separate domain tags makes it
// impossible to accidentally feed one into the other's role even if a
// future refactor merges parameter lists.
func WithdrawAddressHash(destination string) (string, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return "", fmt.Errorf("%w: destination is empty", ErrInvalidWithdrawAddress)
	}

	var b strings.Builder
	b.Grow(len(withdrawAddressDomainTag) + 1 + len(destination))
	b.WriteString(withdrawAddressDomainTag)
	b.WriteByte('|')
	b.WriteString(destination)
	return hash.SHA256Hex([]byte(b.String())), nil
}

// WithdrawAddressDomainTag exposes the domain tag for cross-role checks
// (P2 circuit, P4 backend) that need to assert the off-chain derivation
// has not silently bumped versions.
func WithdrawAddressDomainTag() string {
	return withdrawAddressDomainTag
}
