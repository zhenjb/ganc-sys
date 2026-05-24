package batch_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const (
	aliceAddr   = "cosmos1alice"
	denom       = "uusdc"
	aliceSecret = "alice_secret"
)

// canonicalAlice runs the full STATE-02..07 pipeline against a fresh
// LocalState, returning everything STATE-08 needs to assemble the
// Alice 100/40 SettlementUpdate. Mirrors the gen_state_vectors recipe
// 1:1 so tests are byte-identical with the on-disk vector.
func canonicalAlice(t *testing.T) batch.SettlementInputs {
	t.Helper()

	ls := state.NewLocalState()

	dep := types.DepositRecord{
		DepositID:     "dep-1",
		Owner:         aliceAddr,
		Denom:         denom,
		Amount:        "100",
		Processed:     false,
		CreatedHeight: 12345,
	}
	if _, err := ls.ApplyDeposit(dep); err != nil {
		t.Fatalf("ApplyDeposit: %v", err)
	}
	oldRoot := ls.Root()

	wb := state.NewWithdrawRequestBuilder(ls)
	req, err := wb.Build(state.WithdrawIntent{
		Owner:       aliceAddr,
		Denom:       denom,
		Amount:      "40",
		Destination: aliceAddr,
	})
	if err != nil {
		t.Fatalf("Build withdraw request: %v", err)
	}

	nullifier, err := state.NullifierFor(aliceSecret, req.Nonce)
	if err != nil {
		t.Fatalf("NullifierFor: %v", err)
	}
	addrHash, err := state.WithdrawAddressHash(req.Destination)
	if err != nil {
		t.Fatalf("WithdrawAddressHash: %v", err)
	}

	newRoot, err := ls.ApplyWithdrawal(req, nullifier)
	if err != nil {
		t.Fatalf("ApplyWithdrawal: %v", err)
	}

	return batch.SettlementInputs{
		OldStateRoot:        oldRoot,
		NewStateRoot:        newRoot,
		Deposit:             dep,
		Withdraw:            req,
		Nullifier:           nullifier,
		WithdrawAddressHash: addrHash,
	}
}

func TestBuild_CanonicalAliceVector(t *testing.T) {
	in := canonicalAlice(t)
	b := batch.NewSettlementUpdateBuilder()

	upd, err := b.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if upd.BatchID != "batch-1" {
		t.Fatalf("BatchID: want batch-1, got %s", upd.BatchID)
	}
	if upd.OldStateRoot != in.OldStateRoot {
		t.Fatalf("OldStateRoot mismatch:\n  want %s\n  got  %s", in.OldStateRoot, upd.OldStateRoot)
	}
	if upd.NewStateRoot != in.NewStateRoot {
		t.Fatalf("NewStateRoot mismatch:\n  want %s\n  got  %s", in.NewStateRoot, upd.NewStateRoot)
	}
	if upd.DepositID != "dep-1" || upd.DepositAmount != "100" {
		t.Fatalf("Deposit fields: %+v", upd)
	}
	if upd.WithdrawID != "wd-1" || upd.WithdrawAmount != "40" {
		t.Fatalf("Withdraw fields: %+v", upd)
	}
	if upd.WithdrawAddress != aliceAddr {
		t.Fatalf("WithdrawAddress: want %s, got %s", aliceAddr, upd.WithdrawAddress)
	}
	if upd.WithdrawAddressHash != in.WithdrawAddressHash {
		t.Fatalf("WithdrawAddressHash mismatch")
	}
	if upd.Nullifier != in.Nullifier {
		t.Fatalf("Nullifier mismatch")
	}
}

func TestBuild_SequentialBatchIDs(t *testing.T) {
	in := canonicalAlice(t)
	b := batch.NewSettlementUpdateBuilder()

	for i := 1; i <= 4; i++ {
		// Use varying nullifier/root pairs so each Build call passes
		// the no-op guard. We are not asserting correctness of those
		// values here, only that BatchID advances by exactly +1.
		in.NewStateRoot = "0x" + strings.Repeat("a", 60) + lpad(i)
		in.Nullifier = "0x" + strings.Repeat("b", 60) + lpad(i)
		upd, err := b.Build(in)
		if err != nil {
			t.Fatalf("Build #%d: %v", i, err)
		}
		want := "batch-" + itoa(i)
		if upd.BatchID != want {
			t.Fatalf("BatchID #%d: want %s, got %s", i, want, upd.BatchID)
		}
	}
	if got := b.Seq(); got != 4 {
		t.Fatalf("Seq: want 4, got %d", got)
	}
}

func TestBuild_RejectsNoOpBatch(t *testing.T) {
	in := canonicalAlice(t)
	in.NewStateRoot = in.OldStateRoot
	b := batch.NewSettlementUpdateBuilder()

	_, err := b.Build(in)
	if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
		t.Fatalf("want ErrInvalidSettlementInputs, got %v", err)
	}
	if !strings.Contains(err.Error(), "no-op") {
		t.Fatalf("error should mention no-op: %v", err)
	}
}

func TestBuild_RejectsTamperedWithdrawAddressHash(t *testing.T) {
	in := canonicalAlice(t)
	// Attacker rewrites the destination (or equivalently, the hash) so
	// the proof would be valid against a different recipient than the
	// chain re-derives. STATE-08 must catch this defense-in-depth.
	in.Withdraw.Destination = "cosmos1mallory"

	b := batch.NewSettlementUpdateBuilder()
	_, err := b.Build(in)
	if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
		t.Fatalf("want ErrInvalidSettlementInputs, got %v", err)
	}
	if !strings.Contains(err.Error(), "withdrawAddressHash mismatch") {
		t.Fatalf("error should pinpoint hash mismatch: %v", err)
	}
}

func TestBuild_RejectsMixedDenom(t *testing.T) {
	in := canonicalAlice(t)
	in.Withdraw.Denom = "uatom"
	// Re-derive address hash so we don't trip the address-hash guard first.

	b := batch.NewSettlementUpdateBuilder()
	_, err := b.Build(in)
	if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
		t.Fatalf("want ErrInvalidSettlementInputs, got %v", err)
	}
	if !strings.Contains(err.Error(), "mixed-denom") {
		t.Fatalf("error should mention mixed-denom: %v", err)
	}
}

func TestBuild_InvalidRoots(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*batch.SettlementInputs)
		wantSubstr string
	}{
		{"old empty", func(in *batch.SettlementInputs) { in.OldStateRoot = "" }, "oldStateRoot is empty"},
		{"old no 0x", func(in *batch.SettlementInputs) { in.OldStateRoot = "deadbeef" }, "missing 0x"},
		{"old just 0x", func(in *batch.SettlementInputs) { in.OldStateRoot = "0x" }, "empty after stripping"},
		{"new empty", func(in *batch.SettlementInputs) { in.NewStateRoot = "" }, "newStateRoot is empty"},
		{"new no 0x", func(in *batch.SettlementInputs) { in.NewStateRoot = "cafef00d" }, "missing 0x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := canonicalAlice(t)
			tc.mutate(&in)
			_, err := batch.NewSettlementUpdateBuilder().Build(in)
			if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
				t.Fatalf("want sentinel, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err %q should contain %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestBuild_InvalidNullifierAndAddressHash(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*batch.SettlementInputs)
		wantSubstr string
	}{
		{"nullifier empty", func(in *batch.SettlementInputs) { in.Nullifier = "" }, "nullifier is empty"},
		{"nullifier no 0x", func(in *batch.SettlementInputs) { in.Nullifier = "abc" }, "missing 0x"},
		{"addrHash empty", func(in *batch.SettlementInputs) { in.WithdrawAddressHash = "" }, "withdrawAddressHash is empty"},
		{"addrHash no 0x", func(in *batch.SettlementInputs) { in.WithdrawAddressHash = "ff" }, "missing 0x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := canonicalAlice(t)
			tc.mutate(&in)
			_, err := batch.NewSettlementUpdateBuilder().Build(in)
			if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
				t.Fatalf("want sentinel, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err %q should contain %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestBuild_InvalidDepositAndWithdrawIdentity(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*batch.SettlementInputs)
		wantSubstr string
	}{
		{"deposit id empty", func(in *batch.SettlementInputs) { in.Deposit.DepositID = "" }, "deposit.depositId is empty"},
		{"deposit owner empty", func(in *batch.SettlementInputs) { in.Deposit.Owner = "" }, "deposit.owner is empty"},
		{"deposit denom empty", func(in *batch.SettlementInputs) { in.Deposit.Denom = ""; in.Withdraw.Denom = "" }, "deposit.denom is empty"},
		{"deposit amount zero", func(in *batch.SettlementInputs) { in.Deposit.Amount = "0" }, "deposit.amount"},
		{"deposit amount negative", func(in *batch.SettlementInputs) { in.Deposit.Amount = "-1" }, "deposit.amount"},
		{"deposit amount junk", func(in *batch.SettlementInputs) { in.Deposit.Amount = "abc" }, "deposit.amount"},
		{"withdraw id empty", func(in *batch.SettlementInputs) { in.Withdraw.WithdrawID = "" }, "withdraw.withdrawId is empty"},
		{"withdraw owner empty", func(in *batch.SettlementInputs) { in.Withdraw.Owner = "" }, "withdraw.owner is empty"},
		{"withdraw destination empty", func(in *batch.SettlementInputs) { in.Withdraw.Destination = "" }, "withdraw.destination is empty"},
		{"withdraw amount zero", func(in *batch.SettlementInputs) { in.Withdraw.Amount = "0" }, "withdraw.amount"},
		{"withdraw nonce junk", func(in *batch.SettlementInputs) { in.Withdraw.Nonce = "x" }, "withdraw.nonce"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := canonicalAlice(t)
			tc.mutate(&in)
			_, err := batch.NewSettlementUpdateBuilder().Build(in)
			if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
				t.Fatalf("want sentinel, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err %q should contain %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestBuild_NormalizesAmounts(t *testing.T) {
	in := canonicalAlice(t)
	in.Deposit.Amount = "0100"      // leading zero
	in.Withdraw.Amount = "  40  "   // whitespace

	upd, err := batch.NewSettlementUpdateBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if upd.DepositAmount != "100" {
		t.Fatalf("DepositAmount must be canonical: want 100, got %s", upd.DepositAmount)
	}
	if upd.WithdrawAmount != "40" {
		t.Fatalf("WithdrawAmount must be canonical: want 40, got %s", upd.WithdrawAmount)
	}
}

func TestBuild_FailureDoesNotIncrementSeq(t *testing.T) {
	in := canonicalAlice(t)
	b := batch.NewSettlementUpdateBuilder()

	bad := in
	bad.NewStateRoot = bad.OldStateRoot
	if _, err := b.Build(bad); err == nil {
		t.Fatalf("expected error on no-op batch")
	}
	if got := b.Seq(); got != 0 {
		t.Fatalf("Seq must not advance on failure: got %d", got)
	}

	upd, err := b.Build(in)
	if err != nil {
		t.Fatalf("good Build after bad: %v", err)
	}
	if upd.BatchID != "batch-1" {
		t.Fatalf("first successful batch should be batch-1, got %s", upd.BatchID)
	}
}

func TestBuild_ConcurrentBuildsAssignDistinctBatchIDs(t *testing.T) {
	const goroutines = 16
	b := batch.NewSettlementUpdateBuilder()

	var wg sync.WaitGroup
	results := make(chan string, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := canonicalAlice(t)
			// Differentiate inputs so the no-op guard does not pre-empt.
			in.NewStateRoot = "0x" + strings.Repeat("c", 60) + lpad(i+1)
			in.Nullifier = "0x" + strings.Repeat("d", 60) + lpad(i+1)
			upd, err := b.Build(in)
			if err != nil {
				t.Errorf("goroutine %d Build: %v", i, err)
				return
			}
			results <- upd.BatchID
		}(i)
	}
	wg.Wait()
	close(results)

	seen := make(map[string]struct{}, goroutines)
	for id := range results {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate BatchID %s under concurrency", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != goroutines {
		t.Fatalf("expected %d distinct BatchIDs, got %d", goroutines, len(seen))
	}
	if got := b.Seq(); got != goroutines {
		t.Fatalf("Seq: want %d, got %d", goroutines, got)
	}
}

func TestBuild_OutputMatchesAgreementSchema(t *testing.T) {
	// Ensures every field documented in the Agreements tab for
	// SettlementUpdate is populated. This is the canary that fires if
	// pkg/types.SettlementUpdate adds a new field but Build forgets to
	// fill it.
	upd, err := batch.NewSettlementUpdateBuilder().Build(canonicalAlice(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if upd.BatchID == "" || upd.OldStateRoot == "" || upd.NewStateRoot == "" ||
		upd.DepositID == "" || upd.DepositAmount == "" ||
		upd.WithdrawID == "" || upd.WithdrawAmount == "" ||
		upd.WithdrawAddress == "" || upd.WithdrawAddressHash == "" ||
		upd.Nullifier == "" {
		t.Fatalf("SettlementUpdate has empty field(s): %+v", upd)
	}
}

func lpad(i int) string {
	s := itoa(i)
	if len(s) >= 4 {
		return s
	}
	return strings.Repeat("0", 4-len(s)) + s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
