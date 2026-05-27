package batch_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
)

// canonicalAliceWitness mở rộng canonicalAlice (định nghĩa ở
// builder_test.go) với balance snapshot STATE-09 cần.
//
// Cho canonical Alice vector:
//   - oldBalance = 0 (Alice chưa có account entry trước ApplyDeposit).
//   - newBalance = 60 (sau khi credit 100, debit 40).
func canonicalAliceWitness(t *testing.T) batch.WitnessInputs {
	t.Helper()
	in := canonicalAlice(t)
	return batch.WitnessInputs{
		Settlement: in,
		Accounts: []batch.AccountWitnessSecret{
			{
				Owner:      aliceAddr,
				UserSecret: aliceSecret,
				OldBalance: "0",
				NewBalance: "60",
			},
		},
	}
}

func TestWitnessBuild_CanonicalAliceVector(t *testing.T) {
	in := canonicalAliceWitness(t)
	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(w.Accounts) != 1 {
		t.Fatalf("Accounts length: want 1, got %d", len(w.Accounts))
	}
	acc := w.Accounts[0]
	if acc.Owner != aliceAddr {
		t.Fatalf("Owner: want %s, got %s", aliceAddr, acc.Owner)
	}
	if acc.UserSecret != aliceSecret {
		t.Fatalf("UserSecret: want %s, got %s", aliceSecret, acc.UserSecret)
	}
	if acc.Nonce != in.Settlement.Withdrawals[0].Request.Nonce {
		t.Fatalf("Nonce: want %s, got %s", in.Settlement.Withdrawals[0].Request.Nonce, acc.Nonce)
	}
	if acc.OldBalance != "0" || acc.NewBalance != "60" {
		t.Fatalf("balances: oldBalance=%s newBalance=%s", acc.OldBalance, acc.NewBalance)
	}
	if w.StatePath != nil {
		t.Fatalf("StatePath: want nil for MVP, got %v", w.StatePath)
	}
}

func TestWitnessBuild_NullifierBindsWitnessToUpdate(t *testing.T) {
	// Re-derive nullifier ở đây và assert STATE-09 emit cùng nullifier
	// STATE-06 đã sinh. Nếu domain tag drift giữa state.NullifierFor và
	// circuit, test này gãy trước khi tốn prover round-trip.
	in := canonicalAliceWitness(t)
	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	rederived, err := state.NullifierFor(w.Accounts[0].UserSecret, w.Accounts[0].Nonce)
	if err != nil {
		t.Fatalf("NullifierFor: %v", err)
	}
	if rederived != in.Settlement.Withdrawals[0].Nullifier {
		t.Fatalf("nullifier drift: settlement=%s, witness-derived=%s",
			in.Settlement.Withdrawals[0].Nullifier, rederived)
	}
}

func TestWitnessBuild_RejectsBalanceTransitionViolation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*batch.WitnessInputs)
	}{
		{"newBalance too small", func(in *batch.WitnessInputs) { in.Accounts[0].NewBalance = "59" }},
		{"newBalance too large", func(in *batch.WitnessInputs) { in.Accounts[0].NewBalance = "61" }},
		{"oldBalance too small", func(in *batch.WitnessInputs) { in.Accounts[0].OldBalance = "1" }},
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
	// Attacker (hay caller bug) đưa secret KHÔNG match nullifier baked
	// trong SettlementUpdate.
	in.Accounts[0].UserSecret = "mallory_secret"

	_, err := batch.NewWitnessBuilder().Build(in)
	if !errors.Is(err, batch.ErrInvalidWitnessInputs) {
		t.Fatalf("want ErrInvalidWitnessInputs, got %v", err)
	}
	if !strings.Contains(err.Error(), "nullifier mismatch") {
		t.Fatalf("error should mention nullifier mismatch: %v", err)
	}
}

func TestWitnessBuild_RejectsOwnerNotInBatch(t *testing.T) {
	in := canonicalAliceWitness(t)
	in.Accounts[0].Owner = "cosmos1bob"

	_, err := batch.NewWitnessBuilder().Build(in)
	if !errors.Is(err, batch.ErrInvalidWitnessInputs) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "no deposit or withdrawal") {
		t.Fatalf("error should mention missing owner participation: %v", err)
	}
}

func TestWitnessBuild_InvalidScalars(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*batch.WitnessInputs)
		wantSubstr string
	}{
		{"empty accounts", func(in *batch.WitnessInputs) { in.Accounts = nil }, "accounts is empty"},
		{"owner empty", func(in *batch.WitnessInputs) { in.Accounts[0].Owner = "" }, "owner is empty"},
		{"userSecret empty", func(in *batch.WitnessInputs) { in.Accounts[0].UserSecret = "" }, "userSecret is empty"},
		{"userSecret whitespace", func(in *batch.WitnessInputs) { in.Accounts[0].UserSecret = "   " }, "userSecret is empty"},
		{"oldBalance junk", func(in *batch.WitnessInputs) { in.Accounts[0].OldBalance = "xx" }, "oldBalance"},
		{"oldBalance negative", func(in *batch.WitnessInputs) { in.Accounts[0].OldBalance = "-1" }, "oldBalance"},
		{"newBalance empty", func(in *batch.WitnessInputs) { in.Accounts[0].NewBalance = "" }, "newBalance"},
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

func TestWitnessBuild_NormalizesBalances(t *testing.T) {
	// "000" / "0060" / "  60 " phải canonicalize về "0" / "60" để
	// bytes prover serialize == bytes chain re-derive. Cùng kiểu
	// big.Int canonicalization như STATE-06/STATE-08.
	in := canonicalAliceWitness(t)
	in.Accounts[0].OldBalance = "000"
	in.Accounts[0].NewBalance = "0060"

	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if w.Accounts[0].OldBalance != "0" {
		t.Fatalf("OldBalance must canonicalize: want 0, got %s", w.Accounts[0].OldBalance)
	}
	if w.Accounts[0].NewBalance != "60" {
		t.Fatalf("NewBalance must canonicalize: want 60, got %s", w.Accounts[0].NewBalance)
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
	path[0] = "0xMUTATED"
	if w.StatePath[0] == "0xMUTATED" {
		t.Fatal("WitnessBuilder phải defensive copy StatePath")
	}
}

func TestWitnessBuild_OmitsStatePathWhenEmpty(t *testing.T) {
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
	in := canonicalAliceWitness(t)
	in.Accounts[0].UserSecret = "  " + aliceSecret + " \t"

	w, err := batch.NewWitnessBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if w.Accounts[0].UserSecret != aliceSecret {
		t.Fatalf("UserSecret: want %q, got %q", aliceSecret, w.Accounts[0].UserSecret)
	}
}

func TestWitnessBuild_Concurrent(t *testing.T) {
	// WitnessBuilder stateless. Chạy bầy goroutine assert no data race +
	// mọi kết quả identical.
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
			acc := w.Accounts[0]
			out <- acc.Owner + "|" + acc.UserSecret + "|" + acc.Nonce + "|" + acc.OldBalance + "|" + acc.NewBalance
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
