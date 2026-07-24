package state_test

import (
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/state"
)

// TestWithdrawSecretForOwner_Deterministic locks the property the whole fix
// relies on: the same owner always derives the same secret, so the
// request-time nullifier, the batch-rebuilt nullifier and the gazk prover's
// re-derivation (which reads UserSecret from the witness) all agree.
func TestWithdrawSecretForOwner_Deterministic(t *testing.T) {
	a := state.WithdrawSecretForOwner("cosmos1alice")
	for i := 0; i < 16; i++ {
		if b := state.WithdrawSecretForOwner("cosmos1alice"); b != a {
			t.Fatalf("non-deterministic: iter %d got %s, want %s", i, b, a)
		}
	}
	// Surrounding whitespace must not fork the secret (HTTP bodies carry padding).
	if state.WithdrawSecretForOwner("  cosmos1alice  ") != a {
		t.Fatalf("whitespace changed the derived secret")
	}
	if !strings.HasPrefix(a, "0x") || len(a) != 66 {
		t.Fatalf("secret must be 0x + 64 hex, got %q (len %d)", a, len(a))
	}
}

// TestWithdrawSecretForOwner_PerOwnerDistinct is the direct regression for the
// reported bug: different owners must derive different secrets.
func TestWithdrawSecretForOwner_PerOwnerDistinct(t *testing.T) {
	alice := state.WithdrawSecretForOwner("cosmos1alice")
	bob := state.WithdrawSecretForOwner("cosmos1bob")
	if alice == bob {
		t.Fatalf("distinct owners collided on the same secret: %s", alice)
	}
}

// TestNullifier_CrossUserSameNonceNoCollision reproduces the exact failure
// (Bob then Alice both withdrawing at nonce=1) and asserts it no longer
// collides once each owner seeds its own secret. Before the fix both used the
// shared "mock-user-secret" and produced the identical 0xac44… nullifier.
func TestNullifier_CrossUserSameNonceNoCollision(t *testing.T) {
	const nonce = "1"

	bobNull, err := state.NullifierFor(state.WithdrawSecretForOwner("cosmos1bob"), nonce)
	if err != nil {
		t.Fatalf("bob nullifier: %v", err)
	}
	aliceNull, err := state.NullifierFor(state.WithdrawSecretForOwner("cosmos1alice"), nonce)
	if err != nil {
		t.Fatalf("alice nullifier: %v", err)
	}
	if bobNull == aliceNull {
		t.Fatalf("cross-user nullifier collision at nonce=%s: %s", nonce, bobNull)
	}

	// Sanity: the old shared-secret path is what used to collide — prove both
	// owners WOULD have collided under the pre-fix formula.
	legacyBob, _ := state.NullifierFor("mock-user-secret", nonce)
	legacyAlice, _ := state.NullifierFor("mock-user-secret", nonce)
	if legacyBob != legacyAlice {
		t.Fatalf("legacy shared secret unexpectedly differed: %s vs %s", legacyBob, legacyAlice)
	}
}

// TestNullifier_SameOwnerSameNonceStillCollides confirms replay protection is
// preserved WITHIN an owner: re-deriving for the same (owner, nonce) must yield
// the same nullifier so ApplyWithdrawal still rejects a genuine replay.
func TestNullifier_SameOwnerSameNonceStillCollides(t *testing.T) {
	secret := state.WithdrawSecretForOwner("cosmos1alice")
	first, err := state.NullifierFor(secret, "5")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := state.NullifierFor(secret, "5")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first != second {
		t.Fatalf("same owner+nonce must reproduce the nullifier: %s vs %s", first, second)
	}
}
