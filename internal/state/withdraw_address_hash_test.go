package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// canonicalWithdrawAddressHash is the value the STATE-07 generator
// produces for the canonical Alice 100/40 vector
// (destination = "cosmos1alice"). It is also the hash embedded in
// testvectors/alice_100_40/withdraw_address_hash_wd_1.json. If this
// constant changes, the canonical vector must be regenerated and
// downstream consumers (P2 prover, P1 verifier, P4 backend) re-read it.
const canonicalWithdrawAddressHash = "0xa75ac956249df4c45b83281c5af6187c59df9709fd1c25b5e61b12d71a8eb417"

func TestWithdrawAddressHash_CanonicalAliceVector(t *testing.T) {
	got, err := state.WithdrawAddressHash("cosmos1alice")
	if err != nil {
		t.Fatalf("WithdrawAddressHash: unexpected error: %v", err)
	}
	if got != canonicalWithdrawAddressHash {
		t.Fatalf("hash mismatch:\n  want %s\n  got  %s", canonicalWithdrawAddressHash, got)
	}
}

func TestWithdrawAddressHash_Deterministic(t *testing.T) {
	a, err := state.WithdrawAddressHash("cosmos1alice")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	for i := 0; i < 32; i++ {
		b, err := state.WithdrawAddressHash("cosmos1alice")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if a != b {
			t.Fatalf("non-deterministic: iter %d got %s, want %s", i, b, a)
		}
	}
}

func TestWithdrawAddressHash_HexFormat(t *testing.T) {
	h, err := state.WithdrawAddressHash("cosmos1alice")
	if err != nil {
		t.Fatalf("WithdrawAddressHash: %v", err)
	}
	if !strings.HasPrefix(h, "0x") {
		t.Fatalf("missing 0x prefix: %s", h)
	}
	// SHA-256 → 32 bytes → 64 hex chars + "0x".
	if len(h) != 66 {
		t.Fatalf("expected length 66, got %d (%s)", len(h), h)
	}
	if strings.ToLower(h) != h {
		t.Fatalf("not lowercase: %s", h)
	}
	for _, c := range h[2:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("non-hex char %q in %s", c, h)
		}
	}
}

func TestWithdrawAddressHash_TrimsWhitespace(t *testing.T) {
	// HTTP bodies and CLI input can carry stray surrounding whitespace.
	// The off-chain hash MUST match what WithdrawRequestBuilder trims
	// before storing Destination — otherwise the prover would bind to
	// the trimmed bytes while a naive on-chain re-derivation could see
	// the padded bytes (or vice versa), causing verification to fail
	// for a legitimate user.
	base, err := state.WithdrawAddressHash("cosmos1alice")
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	spaced, err := state.WithdrawAddressHash("  cosmos1alice  ")
	if err != nil {
		t.Fatalf("spaced: %v", err)
	}
	if base != spaced {
		t.Fatalf("whitespace canonicalization failed: plain=%s, spaced=%s", base, spaced)
	}
}

func TestWithdrawAddressHash_DifferentDestinationsDifferentHashes(t *testing.T) {
	cases := []struct {
		name string
		a, b string
	}{
		{"alice vs bob", "cosmos1alice", "cosmos1bob"},
		{"case-sensitive", "cosmos1alice", "Cosmos1Alice"},
		// Without the '|' separator, the bytes ("cosmos1ali", "ce") and
		// ("cosmos1", "alice") could collapse if destination were ever
		// composed of multiple fields. We only hash one field today but
		// the separator is part of the contract for forward-compat with
		// witness builder (STATE-09) that may concatenate.
		{"single-byte difference", "cosmos1alice", "cosmos1alicE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := state.WithdrawAddressHash(tc.a)
			if err != nil {
				t.Fatalf("a: %v", err)
			}
			b, err := state.WithdrawAddressHash(tc.b)
			if err != nil {
				t.Fatalf("b: %v", err)
			}
			if a == b {
				t.Fatalf("expected different hashes, got %s for both", a)
			}
		})
	}
}

func TestWithdrawAddressHash_InvalidInput(t *testing.T) {
	cases := []struct {
		name        string
		destination string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"tabs and newlines", "\t\n  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := state.WithdrawAddressHash(tc.destination)
			if err == nil {
				t.Fatalf("expected error, got hash=%s", got)
			}
			if !errors.Is(err, state.ErrInvalidWithdrawAddress) {
				t.Fatalf("expected ErrInvalidWithdrawAddress, got %v", err)
			}
			if got != "" {
				t.Fatalf("expected empty hash on failure, got %s", got)
			}
		})
	}
}

func TestWithdrawAddressHash_DomainSeparatedFromNullifier(t *testing.T) {
	// If both helpers shared the same domain tag, then a destination
	// chosen to equal "secret|nonce" could yield a nullifier-shaped
	// hash, opening a cross-domain forgery. The version constants are
	// kept distinct precisely to prevent this.
	if state.WithdrawAddressDomainTag() == state.NullifierDomainTag() {
		t.Fatalf("withdraw-address and nullifier share domain tag %q",
			state.WithdrawAddressDomainTag())
	}

	// Sanity: even when the "payload" overlaps with what NullifierFor
	// would see, the derived hashes must not collide because the
	// domain tags differ.
	addr, err := state.WithdrawAddressHash("alice_secret|1")
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	null, err := state.NullifierFor("alice_secret", "1")
	if err != nil {
		t.Fatalf("null: %v", err)
	}
	if addr == null {
		t.Fatalf("cross-domain collision: %s", addr)
	}
}

func TestWithdrawAddressHash_DomainTagExposed(t *testing.T) {
	got := state.WithdrawAddressDomainTag()
	if got != "zkdex/withdrawAddr/v0" {
		t.Fatalf("domain tag drift: want zkdex/withdrawAddr/v0, got %s", got)
	}
}

// TestWithdrawAddressHash_MatchesWithdrawRequestDestination closes the
// loop with STATE-04: the destination stored in the request after the
// builder's trim must hash to the same value the user-facing call sees.
// If WithdrawRequestBuilder ever changes its canonicalization (e.g.
// adds lowercasing), STATE-07 must follow in lockstep — this test will
// fail first.
func TestWithdrawAddressHash_MatchesWithdrawRequestDestination(t *testing.T) {
	ls := state.NewLocalState()
	if _, err := ls.ApplyDeposit(types.DepositRecord{
		DepositID: "dep-1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "100",
	}); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}

	wb := state.NewWithdrawRequestBuilder(ls)
	req, err := wb.Build(state.WithdrawIntent{
		Owner: "cosmos1alice", Denom: "uusdc", Amount: "40",
		// Surrounding whitespace exercises the trim contract.
		Destination: "  cosmos1alice  ",
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	fromRequest, err := state.WithdrawAddressHash(req.Destination)
	if err != nil {
		t.Fatalf("from req.Destination: %v", err)
	}
	fromIntent, err := state.WithdrawAddressHash("cosmos1alice")
	if err != nil {
		t.Fatalf("from intent: %v", err)
	}
	if fromRequest != fromIntent {
		t.Fatalf("STATE-04 / STATE-07 disagree on canonical bytes:\n"+
			"  req.Destination=%q → %s\n  raw → %s",
			req.Destination, fromRequest, fromIntent)
	}
	if fromRequest != canonicalWithdrawAddressHash {
		t.Fatalf("hash drift from canonical vector:\n  want %s\n  got  %s",
			canonicalWithdrawAddressHash, fromRequest)
	}
}
