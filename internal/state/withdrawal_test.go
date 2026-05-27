package state

import (
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// canonical placeholder nullifier used in the Alice 100/40 scenario.
// STATE-06 will replace the hash recipe with the circuit-locked one; the
// value only needs to be stable across the test file.
const aliceNullifier = "0xnull-alice-1"

// seededWithBuildState returns a LocalState with dep-1 applied (100) and the
// canonical wd-1 WithdrawRequest, ready for STATE-05.
func seededWithBuildState(t *testing.T) (*LocalState, types.WithdrawRequest, string) {
	t.Helper()
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)
	req, err := b.Build(WithdrawIntent{
		Owner: aliceAddr, Denom: "uusdc", Amount: "40", Destination: aliceDest,
	})
	if err != nil {
		t.Fatalf("seed: build wd-1: %v", err)
	}
	return ls, req, ls.Root()
}

func TestApplyWithdrawal_Canonical(t *testing.T) {
	ls, req, rootB := seededWithBuildState(t)

	rootC, err := ls.ApplyWithdrawal(req, aliceNullifier)
	if err != nil {
		t.Fatalf("ApplyWithdrawal: %v", err)
	}

	if rootC == rootB {
		t.Fatalf("root must advance after withdrawal apply, got same: %s", rootC)
	}
	if rootC != ls.Root() {
		t.Fatalf("returned root must equal LocalState.Root(): %s vs %s", rootC, ls.Root())
	}

	acc := ls.Account(aliceAddr, "uusdc")
	if acc.Balance != "60" {
		t.Fatalf("balance after withdraw: want 60, got %s", acc.Balance)
	}
	if acc.Nonce != "1" {
		t.Fatalf("account nonce after withdraw must equal request.Nonce: want 1, got %s", acc.Nonce)
	}
	if acc.Nonce != req.Nonce {
		t.Fatalf("post-condition violated: account.Nonce (%s) must equal request.Nonce (%s)", acc.Nonce, req.Nonce)
	}
	if !ls.IsNullifierApplied(aliceNullifier) {
		t.Fatal("nullifier must be marked applied after successful withdraw")
	}
}

// Idempotency by nullifier: replaying the exact same nullifier — even with
// the same request — must be rejected. This guards against the chain
// indexer / relayer queuing the same settlement twice.
func TestApplyWithdrawal_IdempotentByNullifier(t *testing.T) {
	ls, req, _ := seededWithBuildState(t)

	if _, err := ls.ApplyWithdrawal(req, aliceNullifier); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	_, err := ls.ApplyWithdrawal(req, aliceNullifier)
	if !errors.Is(err, ErrWithdrawAlreadyApplied) {
		t.Fatalf("replay must trip ErrWithdrawAlreadyApplied, got %v", err)
	}

	acc := ls.Account(aliceAddr, "uusdc")
	if acc.Balance != "60" {
		t.Fatalf("balance must remain 60 after rejected replay, got %s", acc.Balance)
	}
	if acc.Nonce != "1" {
		t.Fatalf("nonce must remain 1 after rejected replay, got %s", acc.Nonce)
	}
}

// A fresh nullifier on the *same* request must still be rejected because the
// account nonce has already advanced — proving nonce-sequencing is a real
// second line of defense, not redundant with the nullifier map.
func TestApplyWithdrawal_StaleRequestRejectedByNonce(t *testing.T) {
	ls, req, _ := seededWithBuildState(t)

	if _, err := ls.ApplyWithdrawal(req, aliceNullifier); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// Same request but pretend STATE-06 produced a different nullifier
	// (e.g. attacker forging it). Must still be rejected by the
	// nonce-sequencing check.
	_, err := ls.ApplyWithdrawal(req, "0xnull-other")
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("stale request with fresh nullifier must trip ErrNonceMismatch, got %v", err)
	}
}

// Out-of-order: request whose nonce skips over the next-expected value
// must be rejected (e.g. wd-2 cannot be applied before wd-1).
func TestApplyWithdrawal_NonceMismatch_FutureNonce(t *testing.T) {
	ls, req, _ := seededWithBuildState(t)
	req.Nonce = "2" // expected = 0+1 = 1, but request says 2

	_, err := ls.ApplyWithdrawal(req, aliceNullifier)
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("future nonce must trip ErrNonceMismatch, got %v", err)
	}
}

func TestApplyWithdrawal_NonceMismatch_PastNonce(t *testing.T) {
	ls, req, _ := seededWithBuildState(t)
	req.Nonce = "0" // expected = 1

	_, err := ls.ApplyWithdrawal(req, aliceNullifier)
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("past nonce must trip ErrNonceMismatch, got %v", err)
	}
}

func TestApplyWithdrawal_InsufficientBalance(t *testing.T) {
	ls, req, _ := seededWithBuildState(t)
	req.Amount = "999" // exceeds balance

	_, err := ls.ApplyWithdrawal(req, aliceNullifier)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("over-balance withdraw must trip ErrInsufficientBalance, got %v", err)
	}
}

// Boundary: amount == balance must succeed and leave balance=0.
func TestApplyWithdrawal_ExactBalance(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)
	req, err := b.Build(WithdrawIntent{
		Owner: aliceAddr, Denom: "uusdc", Amount: "100", Destination: aliceDest,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := ls.ApplyWithdrawal(req, aliceNullifier); err != nil {
		t.Fatalf("full-balance withdraw must succeed: %v", err)
	}
	acc := ls.Account(aliceAddr, "uusdc")
	if acc.Balance != "0" {
		t.Fatalf("balance after full withdraw must be 0, got %s", acc.Balance)
	}
}

func TestApplyWithdrawal_InvalidRequest(t *testing.T) {
	ls := seededState(t, "100")
	base := types.WithdrawRequest{
		WithdrawID: "wd-1", Owner: aliceAddr, Denom: "uusdc",
		Amount: "40", Destination: aliceDest, Nonce: "1",
	}

	cases := []struct {
		name string
		mut  func(r *types.WithdrawRequest)
	}{
		{"empty withdrawId", func(r *types.WithdrawRequest) { r.WithdrawID = "" }},
		{"whitespace withdrawId", func(r *types.WithdrawRequest) { r.WithdrawID = "   " }},
		{"empty owner", func(r *types.WithdrawRequest) { r.Owner = "" }},
		{"empty denom", func(r *types.WithdrawRequest) { r.Denom = "" }},
		{"empty destination", func(r *types.WithdrawRequest) { r.Destination = "" }},
		{"empty amount", func(r *types.WithdrawRequest) { r.Amount = "" }},
		{"zero amount", func(r *types.WithdrawRequest) { r.Amount = "0" }},
		{"negative amount", func(r *types.WithdrawRequest) { r.Amount = "-5" }},
		{"non-numeric amount", func(r *types.WithdrawRequest) { r.Amount = "abc" }},
		{"empty nonce", func(r *types.WithdrawRequest) { r.Nonce = "" }},
		{"negative nonce", func(r *types.WithdrawRequest) { r.Nonce = "-1" }},
		{"non-numeric nonce", func(r *types.WithdrawRequest) { r.Nonce = "abc" }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := base
			c.mut(&req)
			_, err := ls.ApplyWithdrawal(req, aliceNullifier)
			if !errors.Is(err, ErrInvalidWithdrawRequest) {
				t.Fatalf("want ErrInvalidWithdrawRequest, got %v", err)
			}
		})
	}
}

func TestApplyWithdrawal_EmptyNullifierRejected(t *testing.T) {
	ls, req, _ := seededWithBuildState(t)

	for _, n := range []string{"", "   "} {
		_, err := ls.ApplyWithdrawal(req, n)
		if !errors.Is(err, ErrInvalidWithdrawRequest) {
			t.Fatalf("empty nullifier %q must be rejected as invalid, got %v", n, err)
		}
	}
}

// A failed ApplyWithdrawal must leave LocalState byte-identical — no debit,
// no nonce advance, no root mutation, no nullifier consumed. This is what
// P4's retry path depends on.
func TestApplyWithdrawal_FailedApplyLeavesStateClean(t *testing.T) {
	ls, req, rootBefore := seededWithBuildState(t)
	accBefore := ls.Account(aliceAddr, "uusdc")
	req.Amount = "999" // force insufficient-balance failure

	_, err := ls.ApplyWithdrawal(req, aliceNullifier)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("setup: want ErrInsufficientBalance, got %v", err)
	}

	if ls.Root() != rootBefore {
		t.Fatalf("root changed after failed Apply: before=%s after=%s", rootBefore, ls.Root())
	}
	accAfter := ls.Account(aliceAddr, "uusdc")
	if accAfter.Balance != accBefore.Balance {
		t.Fatalf("balance changed after failed Apply: before=%s after=%s", accBefore.Balance, accAfter.Balance)
	}
	if accAfter.Nonce != accBefore.Nonce {
		t.Fatalf("nonce changed after failed Apply: before=%s after=%s", accBefore.Nonce, accAfter.Nonce)
	}
	if ls.IsNullifierApplied(aliceNullifier) {
		t.Fatal("nullifier must not be marked applied after failed Apply")
	}
}

// Re-deriving the root from the post-state must produce the same value
// returned by ApplyWithdrawal — guards against ComputeRoot drift.
func TestApplyWithdrawal_RootIsDeterministicSnapshotHash(t *testing.T) {
	ls, req, _ := seededWithBuildState(t)
	rootC, err := ls.ApplyWithdrawal(req, aliceNullifier)
	if err != nil {
		t.Fatal(err)
	}
	if got := ComputeRoot(ls.Snapshot()); got != rootC {
		t.Fatalf("root drift: returned=%s, recomputed=%s", rootC, got)
	}
}

// Trimmed equivalence: " 0xnull " and "0xnull" must be treated as the
// same nullifier — matches the trim behavior of IsNullifierApplied.
func TestApplyWithdrawal_NullifierTrimmed(t *testing.T) {
	ls, req, _ := seededWithBuildState(t)
	if _, err := ls.ApplyWithdrawal(req, " "+aliceNullifier+" "); err != nil {
		t.Fatalf("Apply with padded nullifier: %v", err)
	}
	if !ls.IsNullifierApplied(aliceNullifier) {
		t.Fatal("trimmed nullifier should be queryable without padding")
	}
}

// Cross-denom isolation: a withdraw request in a denom Alice never deposited
// must be rejected without touching her uusdc account.
func TestApplyWithdrawal_WrongDenom(t *testing.T) {
	ls := seededState(t, "100")
	req := types.WithdrawRequest{
		WithdrawID:  "wd-1",
		Owner:       aliceAddr,
		Denom:       "uatom",
		Amount:      "1",
		Destination: aliceDest,
		Nonce:       "1",
	}
	_, err := ls.ApplyWithdrawal(req, aliceNullifier)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("withdraw in unfunded denom must fail with ErrInsufficientBalance, got %v", err)
	}
	if ls.Account(aliceAddr, "uusdc").Balance != "100" {
		t.Fatalf("uusdc balance leaked to cross-denom failure path")
	}
}

// Two sequential withdrawals on the same account with the second
// request properly built after the first apply must both succeed.
// This is the realistic STATE-04 → STATE-05 → STATE-04 → STATE-05 loop.
func TestApplyWithdrawal_TwoSequentialWithdrawalsSucceed(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)

	req1, err := b.Build(WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Amount: "40", Destination: aliceDest})
	if err != nil {
		t.Fatalf("build wd-1: %v", err)
	}
	if _, err := ls.ApplyWithdrawal(req1, "0xnull-1"); err != nil {
		t.Fatalf("apply wd-1: %v", err)
	}

	req2, err := b.Build(WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Amount: "30", Destination: aliceDest})
	if err != nil {
		t.Fatalf("build wd-2: %v", err)
	}
	if req2.Nonce != "2" {
		t.Fatalf("wd-2 must pin nonce=2 after wd-1 applied, got %s", req2.Nonce)
	}
	if _, err := ls.ApplyWithdrawal(req2, "0xnull-2"); err != nil {
		t.Fatalf("apply wd-2: %v", err)
	}

	acc := ls.Account(aliceAddr, "uusdc")
	if acc.Balance != "30" {
		t.Fatalf("balance after two withdrawals: want 30, got %s", acc.Balance)
	}
	if acc.Nonce != "2" {
		t.Fatalf("nonce after two withdrawals: want 2, got %s", acc.Nonce)
	}
}
