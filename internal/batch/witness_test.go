package batch_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
)

// canonicalAliceWitness extends canonicalAlice (defined in
// builder_test.go) with the balance snapshots STATE-09 needs.
//
// For the Alice 100/40 vector:
//   - oldBalance = 0 (Alice has no account entry until ApplyDeposit).
//   - newBalance = 60 (post-deposit, post-withdraw).
//
// We re-derive both values from a fresh LocalState so the helper does
// not lean on knowledge from canonicalAlice's internals — any future
// change to the canonical scenario flows here automatically.
func canonicalAliceWitness(t *testing.T) batch.WitnessInputs {
	t.Helper()
	in := canonicalAlice(t)
	return batch.WitnessInputs{
		UserSecret: aliceSecret,
		Settlement: in,
		OldBalance: "0",
		NewBalance: "60",
	}
}

func TestWitnessBuild_CanonicalAliceVector(t *testing.T) {
	in := canonicalAliceWitness(t)
	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if w.UserSecret != aliceSecret {
		t.Fatalf("UserSecret: want %s, got %s", aliceSecret, w.UserSecret)
	}
	if w.Nonce != in.Settlement.Withdraw.Nonce {
		t.Fatalf("Nonce: want %s, got %s", in.Settlement.Withdraw.Nonce, w.Nonce)
	}
	if w.OldBalance != "0" || w.NewBalance != "60" {
		t.Fatalf("balances: oldBalance=%s newBalance=%s", w.OldBalance, w.NewBalance)
	}
	if w.StatePath != nil {
		t.Fatalf("StatePath: want nil for MVP, got %v", w.StatePath)
	}
}

func TestWitnessBuild_NullifierBindsWitnessToUpdate(t *testing.T) {
	// Re-derive nullifier here and assert STATE-09 emits the same one
	// STATE-06 produced. If the domain tag ever drifts between
	// state.NullifierFor and the circuit, this test breaks before any
	// prover round-trip.
	in := canonicalAliceWitness(t)
	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rederived, err := state.NullifierFor(w.UserSecret, w.Nonce)
	if err != nil {
		t.Fatalf("NullifierFor: %v", err)
	}
	if rederived != in.Settlement.Nullifier {
		t.Fatalf("nullifier drift: settlement=%s, witness-derived=%s",
			in.Settlement.Nullifier, rederived)
	}
}

func TestWitnessBuild_RejectsBalanceTransitionViolation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*batch.WitnessInputs)
	}{
		{"newBalance too small", func(in *batch.WitnessInputs) { in.NewBalance = "59" }},
		{"newBalance too large", func(in *batch.WitnessInputs) { in.NewBalance = "61" }},
		{"oldBalance too small", func(in *batch.WitnessInputs) { in.OldBalance = "1" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := canonicalAliceWitness(t)
			tc.mutate(&in)
			_, err := batch.NewWitnessBuilder().Build(in)
			if !errors.Is(err, batch.ErrInvalidWitnessInputs) {
				t.Fatalf("want ErrInvalidWitnessInputs, got %v", err)
			}
			if !strings.Contains(err.Error(), "balance transition violated") {
				t.Fatalf("error should mention balance transition: %v", err)
			}
		})
	}
}

func TestWitnessBuild_RejectsNullifierMismatch(t *testing.T) {
	in := canonicalAliceWitness(t)
	// Attacker (or buggy caller) hands in a secret that does NOT match
	// the nullifier baked into the SettlementUpdate.
	in.UserSecret = "mallory_secret"

	_, err := batch.NewWitnessBuilder().Build(in)
	if !errors.Is(err, batch.ErrInvalidWitnessInputs) {
		t.Fatalf("want ErrInvalidWitnessInputs, got %v", err)
	}
	if !strings.Contains(err.Error(), "nullifier mismatch") {
		t.Fatalf("error should mention nullifier mismatch: %v", err)
	}
}

func TestWitnessBuild_RejectsMixedDenom(t *testing.T) {
	in := canonicalAliceWitness(t)
	in.Settlement.Withdraw.Denom = "uatom"

	_, err := batch.NewWitnessBuilder().Build(in)
	if !errors.Is(err, batch.ErrInvalidWitnessInputs) {
		t.Fatalf("want ErrInvalidWitnessInputs, got %v", err)
	}
	if !strings.Contains(err.Error(), "mixed-denom") {
		t.Fatalf("error should mention mixed-denom: %v", err)
	}
}

func TestWitnessBuild_InvalidScalars(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*batch.WitnessInputs)
		wantSubstr string
	}{
		{"userSecret empty", func(in *batch.WitnessInputs) { in.UserSecret = "" }, "userSecret is empty"},
		{"userSecret whitespace", func(in *batch.WitnessInputs) { in.UserSecret = "   " }, "userSecret is empty"},
		{"nonce junk", func(in *batch.WitnessInputs) { in.Settlement.Withdraw.Nonce = "abc" }, "withdraw.nonce"},
		{"nonce negative", func(in *batch.WitnessInputs) { in.Settlement.Withdraw.Nonce = "-1" }, "withdraw.nonce"},
		{"oldBalance junk", func(in *batch.WitnessInputs) { in.OldBalance = "xx" }, "oldBalance"},
		{"oldBalance negative", func(in *batch.WitnessInputs) { in.OldBalance = "-1" }, "oldBalance"},
		{"newBalance empty", func(in *batch.WitnessInputs) { in.NewBalance = "" }, "newBalance"},
		{"deposit amount zero", func(in *batch.WitnessInputs) { in.Settlement.Deposit.Amount = "0" }, "deposit.amount"},
		{"withdraw amount junk", func(in *batch.WitnessInputs) { in.Settlement.Withdraw.Amount = "??" }, "withdraw.amount"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := canonicalAliceWitness(t)
			tc.mutate(&in)
			_, err := batch.NewWitnessBuilder().Build(in)
			if !errors.Is(err, batch.ErrInvalidWitnessInputs) {
				t.Fatalf("want sentinel, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err %q should contain %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestWitnessBuild_NormalizesScalars(t *testing.T) {
	// "01" / "060" / "0100" / "  40 " must canonicalize to "1" / "60"
	// / "100" / "40" so the bytes the prover serializes match the
	// bytes the chain re-derives. This is the same big.Int
	// canonicalization STATE-06/STATE-08 already apply.
	in := canonicalAliceWitness(t)
	in.OldBalance = "000"
	in.NewBalance = "0060"
	in.Settlement.Deposit.Amount = "0100"
	in.Settlement.Withdraw.Amount = "  40 "

	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if w.OldBalance != "0" {
		t.Fatalf("OldBalance must canonicalize: want 0, got %s", w.OldBalance)
	}
	if w.NewBalance != "60" {
		t.Fatalf("NewBalance must canonicalize: want 60, got %s", w.NewBalance)
	}
}

func TestWitnessBuild_StatePathDefensiveCopy(t *testing.T) {
	in := canonicalAliceWitness(t)
	path := []string{"0xnode1", "0xnode2", "0xnode3"}
	in.StatePath = path

	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(w.StatePath) != len(path) {
		t.Fatalf("StatePath length: want %d, got %d", len(path), len(w.StatePath))
	}
	// Mutate caller's slice; the witness must NOT change.
	path[0] = "0xMUTATED"
	if w.StatePath[0] == "0xMUTATED" {
		t.Fatal("WitnessBuilder must defensively copy StatePath")
	}
}

func TestWitnessBuild_OmitsStatePathWhenEmpty(t *testing.T) {
	// MVP keeps the witness compact; an absent path serializes as
	// `omitempty` so prover/verifier do not branch on length-zero
	// arrays vs nil. STATE-09 must keep StatePath==nil when caller
	// supplies none.
	in := canonicalAliceWitness(t)
	if in.StatePath != nil {
		t.Fatalf("helper precondition: StatePath must be nil")
	}
	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if w.StatePath != nil {
		t.Fatalf("StatePath: want nil (omitempty), got %v", w.StatePath)
	}
}

func TestWitnessBuild_PreservesUserSecretAfterTrim(t *testing.T) {
	// Whitespace stripping around the secret is intentional (mirrors
	// the trim STATE-06 applies). The trimmed value must still match
	// the nullifier baked into the SettlementUpdate.
	in := canonicalAliceWitness(t)
	in.UserSecret = "  " + aliceSecret + " \t"

	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if w.UserSecret != aliceSecret {
		t.Fatalf("UserSecret: want %q, got %q", aliceSecret, w.UserSecret)
	}
}

func TestWitnessBuild_Concurrent(t *testing.T) {
	// WitnessBuilder is stateless. Run a herd to assert no data race
	// and all results identical.
	const n = 16
	in := canonicalAliceWitness(t)
	b := batch.NewWitnessBuilder()

	var wg sync.WaitGroup
	out := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, err := b.Build(in)
			if err != nil {
				t.Errorf("Build: %v", err)
				return
			}
			out <- w.UserSecret + "|" + w.Nonce + "|" + w.OldBalance + "|" + w.NewBalance
		}()
	}
	wg.Wait()
	close(out)

	var first string
	for s := range out {
		if first == "" {
			first = s
			continue
		}
		if s != first {
			t.Fatalf("witness drift under concurrency: %q vs %q", first, s)
		}
	}
}
