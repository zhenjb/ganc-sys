package state

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T03 — order signature verification.
//
// Production order auth is Cosmos ADR-036 arbitrary-message signing
// (secp256k1): the wallet signs the CANONICAL order bytes, and the verifier
// recovers the pubkey, derives the bech32 address and asserts it equals Owner.
// That needs a secp256k1 + bech32 stack the MVP does not yet vendor, so
// verification is behind an interface: swap the implementation without touching
// ValidateOrder. This matches the repo's stub-then-real pattern (gazk
// StubProofVerifier) and the parallel-plan rule "code on mock/stub, graft the
// real proof last".
//
// The MVP verifier below is a DETERMINISTIC binding signature (same shape as the
// repo's localWithdrawSignature): the signature commits to owner + canonical
// bytes, so a forged or tampered order is still rejected and the check is
// reproducible by P2 — enough to satisfy the STATE-T03 DoD today.

// ErrOrderSignatureInvalid is returned when an order's signature does not verify
// against its owner + canonical bytes. Sentinel — errors.Is.
var ErrOrderSignatureInvalid = errors.New("state: order signature invalid")

// OrderSignatureVerifier verifies that `order` was authorized by order.Owner.
// Implementations MUST verify against the supplied canonical bytes, never the
// raw JSON (whitespace / field order would break the signature — STATE-T03
// pitfall). canonical is order.CanonicalBytes() computed once by the caller.
type OrderSignatureVerifier interface {
	Verify(order types.SignedOrder, canonical []byte) error
}

// mockOrderSigDomain domain-separates the MVP binding signature from every other
// SHA-256 callsite. The "/mock/" segment makes it unmistakable in logs/vectors
// that this is not a real ADR-036 signature.
const mockOrderSigDomain = "zkdex/orderSig/mock/v0"

// MockOrderSignature returns the deterministic MVP signature for an order:
//
//	sig = "0x" + SHA256Hex( domain | owner | canonicalBytes )   (0x-stripped)
//
// It binds the owner and the exact canonical bytes, so any change to a canonical
// field (or the owner) changes the signature. P5's mock wallet and tests use
// this to produce valid signatures. Returns an error if the order cannot be
// canonicalized.
func MockOrderSignature(order types.SignedOrder) (string, error) {
	canonical, err := order.CanonicalBytes()
	if err != nil {
		return "", err
	}
	return mockOrderSignatureFor(order.Owner, canonical), nil
}

func mockOrderSignatureFor(owner string, canonical []byte) string {
	var b strings.Builder
	b.Grow(len(mockOrderSigDomain) + 2 + len(owner) + len(canonical))
	b.WriteString(mockOrderSigDomain)
	b.WriteByte('|')
	b.WriteString(strings.TrimSpace(owner))
	b.WriteByte('|')
	b.Write(canonical)
	// hash.SHA256Hex already returns a 0x-prefixed lowercase-hex digest.
	return hash.SHA256Hex([]byte(b.String()))
}

// MockOrderSignatureVerifier is the MVP OrderSignatureVerifier. It recomputes
// the deterministic binding signature and compares it (constant-time) with the
// order's signature. It is the default used by NewOrderValidator when no
// verifier is injected.
type MockOrderSignatureVerifier struct{}

// Verify recomputes the expected binding signature over (owner, canonical) and
// constant-time compares it with order.Signature. Any tampering (different
// owner, price, qty, …) yields different canonical bytes and thus a mismatch.
func (MockOrderSignatureVerifier) Verify(order types.SignedOrder, canonical []byte) error {
	got := strings.ToLower(strings.TrimSpace(order.Signature))
	if got == "" {
		return fmt.Errorf("%w: signature is empty", ErrOrderSignatureInvalid)
	}
	want := mockOrderSignatureFor(order.Owner, canonical)
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return fmt.Errorf("%w: does not match owner %q over canonical bytes", ErrOrderSignatureInvalid, order.Owner)
	}
	return nil
}
