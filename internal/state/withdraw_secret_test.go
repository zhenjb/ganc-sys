package state_test

import (
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/state"
)

// TestWithdrawSecretFor_Deterministic locks the property the whole fix relies
// on: the same (owner, denom) always derives the same secret, so the
// request-time nullifier, the batch-rebuilt nullifier and the gazk prover's
// re-derivation (which reads UserSecret from the witness account) all agree.
func TestWithdrawSecretFor_Deterministic(t *testing.T) {
	a := state.WithdrawSecretFor("cosmos1alice", "uusdc")
	for i := 0; i < 16; i++ {
		if b := state.WithdrawSecretFor("cosmos1alice", "uusdc"); b != a {
			t.Fatalf("non-deterministic: iter %d got %s, want %s", i, b, a)
		}
	}
	// Surrounding whitespace must not fork the secret (HTTP bodies carry padding).
	if state.WithdrawSecretFor("  cosmos1alice  ", " uusdc ") != a {
		t.Fatalf("whitespace changed the derived secret")
	}
	if !strings.HasPrefix(a, "0x") || len(a) != 66 {
		t.Fatalf("secret must be 0x + 64 hex, got %q (len %d)", a, len(a))
	}
}

// TestWithdrawSecretFor_PerOwnerDistinct — different owners must derive
// different secrets (cross-user collision regression).
func TestWithdrawSecretFor_PerOwnerDistinct(t *testing.T) {
	if state.WithdrawSecretFor("cosmos1alice", "uusdc") == state.WithdrawSecretFor("cosmos1bob", "uusdc") {
		t.Fatal("distinct owners collided on the same secret")
	}
}

// TestWithdrawSecretFor_PerDenomDistinct — the SAME owner withdrawing two
// different denoms must derive different secrets. This is the direct regression
// for the reported bug: nonce is per-account (owner,denom), so both denoms get
// nonce=1; only a denom-bound secret keeps their nullifiers apart.
func TestWithdrawSecretFor_PerDenomDistinct(t *testing.T) {
	if state.WithdrawSecretFor("cosmos1alice", "uatom") == state.WithdrawSecretFor("cosmos1alice", "uusdc") {
		t.Fatal("same owner, different denom collided on the same secret")
	}
}

// TestNullifier_SameOwnerCrossDenomNoCollision reproduces the exact failure
// (one user withdrawing uatom then uusdc, both at nonce=1) and asserts the
// nullifiers no longer collide. Before the denom binding both resolved to the
// per-owner secret and produced the identical nullifier.
func TestNullifier_SameOwnerCrossDenomNoCollision(t *testing.T) {
	const owner, nonce = "cosmos1alice", "1"

	atom, err := state.NullifierFor(state.WithdrawSecretFor(owner, "uatom"), nonce)
	if err != nil {
		t.Fatalf("uatom nullifier: %v", err)
	}
	usdc, err := state.NullifierFor(state.WithdrawSecretFor(owner, "uusdc"), nonce)
	if err != nil {
		t.Fatalf("uusdc nullifier: %v", err)
	}
	if atom == usdc {
		t.Fatalf("same owner cross-denom nullifier collision at nonce=%s: %s", nonce, atom)
	}
}

// TestNullifier_CrossUserSameNonceNoCollision — different owners, same denom &
// nonce must not collide either.
func TestNullifier_CrossUserSameNonceNoCollision(t *testing.T) {
	bob, err := state.NullifierFor(state.WithdrawSecretFor("cosmos1bob", "uosmo"), "1")
	if err != nil {
		t.Fatalf("bob nullifier: %v", err)
	}
	alice, err := state.NullifierFor(state.WithdrawSecretFor("cosmos1alice", "uosmo"), "1")
	if err != nil {
		t.Fatalf("alice nullifier: %v", err)
	}
	if bob == alice {
		t.Fatalf("cross-user nullifier collision: %s", bob)
	}
}

// TestNullifier_SameAccountSameNonceStillCollides confirms replay protection is
// preserved WITHIN an account: re-deriving for the same (owner, denom, nonce)
// must yield the same nullifier so ApplyWithdrawal still rejects a genuine
// replay.
func TestNullifier_SameAccountSameNonceStillCollides(t *testing.T) {
	secret := state.WithdrawSecretFor("cosmos1alice", "uusdc")
	first, err := state.NullifierFor(secret, "5")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := state.NullifierFor(secret, "5")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first != second {
		t.Fatalf("same account+nonce must reproduce the nullifier: %s vs %s", first, second)
	}
}
