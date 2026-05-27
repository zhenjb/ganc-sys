package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// canonicalNullifier is the value the STATE-06 generator produces for the
// canonical Alice 100/40 test vector. It is also the nullifier embedded in
// testvectors/alice_100_40/nullifier_wd_1.json. If this constant changes,
// the canonical vector must be regenerated and downstream consumers
// (P2 prover, P1 verifier, P4 backend) re-read it.
const canonicalNullifier = "0x1a1fdf4ccecb7040b7cd7e125226d14ed7717618d996dab969d4cb12550b22f7"

func TestNullifierFor_CanonicalAliceVector(t *testing.T) {
	got, err := state.NullifierFor("alice_secret", "1")
	if err != nil {
		t.Fatalf("NullifierFor: unexpected error: %v", err)
	}
	if got != canonicalNullifier {
		t.Fatalf("nullifier mismatch:\n  want %s\n  got  %s", canonicalNullifier, got)
	}
}

func TestNullifierFor_Deterministic(t *testing.T) {
	a, err := state.NullifierFor("alice_secret", "1")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	for i := 0; i < 32; i++ {
		b, err := state.NullifierFor("alice_secret", "1")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if a != b {
			t.Fatalf("non-deterministic: iter %d got %s, want %s", i, b, a)
		}
	}
}

func TestNullifierFor_HexFormat(t *testing.T) {
	n, err := state.NullifierFor("alice_secret", "1")
	if err != nil {
		t.Fatalf("NullifierFor: %v", err)
	}
	if !strings.HasPrefix(n, "0x") {
		t.Fatalf("missing 0x prefix: %s", n)
	}
	// SHA-256 → 32 bytes → 64 hex chars + "0x".
	if len(n) != 66 {
		t.Fatalf("expected length 66, got %d (%s)", len(n), n)
	}
	if strings.ToLower(n) != n {
		t.Fatalf("not lowercase: %s", n)
	}
	for _, c := range n[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("non-hex char %q in %s", c, n)
		}
	}
}

func TestNullifierFor_NonceCanonicalization(t *testing.T) {
	// "1" and "01" represent the same non-negative integer; the
	// derivation must canonicalize before hashing so two semantically
	// identical inputs cannot fork the nullifier (and the idempotency
	// guard in ApplyWithdrawal would be bypassable otherwise).
	base, err := state.NullifierFor("alice_secret", "1")
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	padded, err := state.NullifierFor("alice_secret", "01")
	if err != nil {
		t.Fatalf("padded: %v", err)
	}
	if base != padded {
		t.Fatalf("nonce canonicalization failed: 1=%s, 01=%s", base, padded)
	}

	// Surrounding whitespace must also be ignored (HTTP bodies and CLI
	// inputs frequently carry stray padding).
	spaced, err := state.NullifierFor("alice_secret", "  1  ")
	if err != nil {
		t.Fatalf("spaced: %v", err)
	}
	if base != spaced {
		t.Fatalf("whitespace canonicalization failed: 1=%s, '  1  '=%s", base, spaced)
	}
}

func TestNullifierFor_DifferentInputsDifferentOutputs(t *testing.T) {
	cases := []struct {
		name           string
		a              [2]string
		b              [2]string
	}{
		{"different secret", [2]string{"alice_secret", "1"}, [2]string{"bob_secret", "1"}},
		{"different nonce", [2]string{"alice_secret", "1"}, [2]string{"alice_secret", "2"}},
		// Without a field separator the bytes ("a","12") and ("a1","2")
		// would collapse to the same hash input "a12". The '|' delimiter
		// MUST keep them distinct.
		{"separator prevents collision", [2]string{"a", "12"}, [2]string{"a1", "2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := state.NullifierFor(tc.a[0], tc.a[1])
			if err != nil {
				t.Fatalf("a: %v", err)
			}
			b, err := state.NullifierFor(tc.b[0], tc.b[1])
			if err != nil {
				t.Fatalf("b: %v", err)
			}
			if a == b {
				t.Fatalf("expected different nullifiers, got %s for both", a)
			}
		})
	}
}

func TestNullifierFor_InvalidInput(t *testing.T) {
	cases := []struct {
		name       string
		userSecret string
		nonce      string
	}{
		{"empty secret", "", "1"},
		{"whitespace secret", "   ", "1"},
		{"empty nonce", "alice_secret", ""},
		{"whitespace nonce", "alice_secret", "   "},
		{"negative nonce", "alice_secret", "-1"},
		{"non-numeric nonce", "alice_secret", "abc"},
		{"hex-prefixed nonce", "alice_secret", "0x1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := state.NullifierFor(tc.userSecret, tc.nonce)
			if err == nil {
				t.Fatalf("expected error, got nullifier=%s", got)
			}
			if !errors.Is(err, state.ErrInvalidNullifierInput) {
				t.Fatalf("expected ErrInvalidNullifierInput, got %v", err)
			}
			if got != "" {
				t.Fatalf("expected empty nullifier on failure, got %s", got)
			}
		})
	}
}

func TestNullifierFor_ZeroNonceAccepted(t *testing.T) {
	// Nonce=0 is a legal non-negative integer. Although STATE-05 will
	// reject a withdraw request with nonce=0 against the post-deposit
	// account (expected = account.Nonce+1 = 1), the nullifier
	// derivation itself must not pre-empt that — it is a pure hash.
	got, err := state.NullifierFor("alice_secret", "0")
	if err != nil {
		t.Fatalf("nonce=0 should be accepted: %v", err)
	}
	if got == "" {
		t.Fatal("empty nullifier for nonce=0")
	}
}

func TestNullifierFor_DomainTagExposed(t *testing.T) {
	got := state.NullifierDomainTag()
	if got != "zkdex/nullifier/v0" {
		t.Fatalf("domain tag drift: want zkdex/nullifier/v0, got %s", got)
	}
}

// TestNullifierFor_DrivesApplyWithdrawalIdempotency closes the loop with
// STATE-05: deriving the nullifier for the same (secret, nonce) pair and
// calling ApplyWithdrawal twice must trigger the idempotency guard on the
// second call. STATE-06 is the producer side of that guard's key.
func TestNullifierFor_DrivesApplyWithdrawalIdempotency(t *testing.T) {
	ls := state.NewLocalState()
	if _, err := ls.ApplyDeposit(types.DepositRecord{
		DepositID: "dep-1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "100",
	}); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}

	wb := state.NewWithdrawRequestBuilder(ls)
	req, err := wb.Build(state.WithdrawIntent{
		Owner: "cosmos1alice", Denom: "uusdc", Amount: "40", Destination: "cosmos1alice",
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	nullifier, err := state.NullifierFor("alice_secret", req.Nonce)
	if err != nil {
		t.Fatalf("nullifier: %v", err)
	}

	if _, err := ls.ApplyWithdrawal(req, nullifier); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if !ls.IsNullifierApplied(nullifier) {
		t.Fatal("nullifier not registered after first apply")
	}

	// Re-derive and replay. The nullifier must collide because the
	// derivation is deterministic — that is the whole point.
	again, err := state.NullifierFor("alice_secret", req.Nonce)
	if err != nil {
		t.Fatalf("re-derive: %v", err)
	}
	if again != nullifier {
		t.Fatalf("nullifier drift on re-derive: first=%s, second=%s", nullifier, again)
	}
	if _, err := ls.ApplyWithdrawal(req, again); !errors.Is(err, state.ErrWithdrawAlreadyApplied) {
		t.Fatalf("expected ErrWithdrawAlreadyApplied on replay, got %v", err)
	}
}
