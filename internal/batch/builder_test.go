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

// canonicalAlice chạy đầy đủ STATE-02..07 pipeline trên một LocalState
// mới, trả về mọi thứ STATE-08 cần để build Alice 100/40 batch-shaped
// SettlementUpdate (1 deposit, 1 withdrawal).
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
	destinationHash, err := state.WithdrawAddressHash(req.Destination)
	if err != nil {
		t.Fatalf("WithdrawAddressHash: %v", err)
	}

	newRoot, err := ls.ApplyWithdrawal(req, nullifier)
	if err != nil {
		t.Fatalf("ApplyWithdrawal: %v", err)
	}

	return batch.SettlementInputs{
		OldStateRoot: oldRoot,
		NewStateRoot: newRoot,
		Deposits:     []types.DepositRecord{dep},
		Withdrawals: []batch.WithdrawalInput{
			{Request: req, Nullifier: nullifier, DestinationHash: destinationHash},
		},
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
	if len(upd.Deposits) != 1 || upd.Deposits[0].DepositID != "dep-1" || upd.Deposits[0].Amount != "100" {
		t.Fatalf("Deposits[0]: %+v", upd.Deposits)
	}
	if upd.Deposits[0].Owner != aliceAddr || upd.Deposits[0].Denom != denom {
		t.Fatalf("Deposits[0] owner/denom: %+v", upd.Deposits[0])
	}
	if len(upd.Withdrawals) != 1 {
		t.Fatalf("Withdrawals length: want 1, got %d", len(upd.Withdrawals))
	}
	w := upd.Withdrawals[0]
	if w.WithdrawID != "wd-1" || w.Amount != "40" || w.Destination != aliceAddr {
		t.Fatalf("Withdrawals[0]: %+v", w)
	}
	if w.DestinationHash != in.Withdrawals[0].DestinationHash {
		t.Fatalf("DestinationHash mismatch")
	}
	if w.Nullifier != in.Withdrawals[0].Nullifier {
		t.Fatalf("Nullifier mismatch")
	}
}

// INT-2SEQ: the core and trade settle paths use independent builders but submit
// into ONE chain-side batchId namespace; a per-builder prefix keeps their id
// streams disjoint so they never collide ("batchId already exists"). The default
// constructor stays "batch-" (backward-compat: LocalBuilder and every existing
// caller/test are unchanged).
func TestBuild_BatchIDNamespacePrefix(t *testing.T) {
	cases := []struct {
		name    string
		builder *batch.SettlementUpdateBuilder
		want    string
	}{
		{"default", batch.NewSettlementUpdateBuilder(), "batch-1"},
		{"core", batch.NewSettlementUpdateBuilderWithPrefix("core-"), "core-1"},
		{"trade", batch.NewSettlementUpdateBuilderWithPrefix("trade-"), "trade-1"},
		{"empty-falls-back", batch.NewSettlementUpdateBuilderWithPrefix("  "), "batch-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upd, err := tc.builder.Build(canonicalAlice(t))
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if upd.BatchID != tc.want {
				t.Fatalf("BatchID: want %s, got %s", tc.want, upd.BatchID)
			}
		})
	}
}

// Two independent builders (as the live core vs trade paths are) must not share a
// batchId even though each starts its own counter at 1 — the exact live collision.
func TestBuild_CoreAndTradeNamespacesDoNotCollide(t *testing.T) {
	core := batch.NewSettlementUpdateBuilderWithPrefix("core-")
	trade := batch.NewSettlementUpdateBuilderWithPrefix("trade-")

	coreUpd, err := core.Build(canonicalAlice(t))
	if err != nil {
		t.Fatalf("core Build: %v", err)
	}
	tradeUpd, err := trade.Build(canonicalAlice(t))
	if err != nil {
		t.Fatalf("trade Build: %v", err)
	}
	if coreUpd.BatchID == tradeUpd.BatchID {
		t.Fatalf("core and trade minted the same batchId %q — namespaces collide", coreUpd.BatchID)
	}
	if coreUpd.BatchID != "core-1" || tradeUpd.BatchID != "trade-1" {
		t.Fatalf("unexpected ids: core=%s trade=%s", coreUpd.BatchID, tradeUpd.BatchID)
	}
}

func TestBuild_SequentialBatchIDs(t *testing.T) {
	in := canonicalAlice(t)
	b := batch.NewSettlementUpdateBuilder()

	for i := 1; i <= 4; i++ {
		in.NewStateRoot = "0x" + strings.Repeat("a", 60) + lpad(i)
		in.Withdrawals[0].Nullifier = "0x" + strings.Repeat("b", 60) + lpad(i)
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

func TestBuild_RejectsEmptyBatch(t *testing.T) {
	in := canonicalAlice(t)
	in.Deposits = nil
	in.Withdrawals = nil

	_, err := batch.NewSettlementUpdateBuilder().Build(in)
	if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
		t.Fatalf("want ErrInvalidSettlementInputs, got %v", err)
	}
	if !strings.Contains(err.Error(), "empty batch") {
		t.Fatalf("error should mention empty batch: %v", err)
	}
}

func TestBuild_RejectsTamperedDestinationHash(t *testing.T) {
	in := canonicalAlice(t)
	// Attacker đổi destination mà KHÔNG re-derive destinationHash —
	// STATE-08 phải catch (defense-in-depth).
	in.Withdrawals[0].Request.Destination = "cosmos1mallory"

	_, err := batch.NewSettlementUpdateBuilder().Build(in)
	if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
		t.Fatalf("want ErrInvalidSettlementInputs, got %v", err)
	}
	if !strings.Contains(err.Error(), "destinationHash mismatch") {
		t.Fatalf("error should pinpoint hash mismatch: %v", err)
	}
}

// INT-MULTIDENOM: a batch now gathers deposits/withdrawals across DIFFERENT
// denoms (the single-denom invariant is gone — the unified circuit binds a denom
// per cell). The canonical Alice vector with a uusdc deposit + a uatom withdrawal
// must build, preserving each op's own denom verbatim.
func TestBuild_AcceptsMixedDenom(t *testing.T) {
	in := canonicalAlice(t)
	in.Withdrawals[0].Request.Denom = "uatom"

	upd, err := batch.NewSettlementUpdateBuilder().Build(in)
	if err != nil {
		t.Fatalf("multi-denom batch should build, got %v", err)
	}
	if len(upd.Deposits) != 1 || upd.Deposits[0].Denom != "uusdc" {
		t.Fatalf("deposit denom not preserved: %+v", upd.Deposits)
	}
	if len(upd.Withdrawals) != 1 || upd.Withdrawals[0].Denom != "uatom" {
		t.Fatalf("withdrawal denom not preserved: %+v", upd.Withdrawals)
	}
}

// INT-MULTIDENOM: WitnessBuilder must aggregate sumDeposit/sumWithdraw PER
// (owner, denom). One owner holding two denoms yields two accounts, each
// satisfying ZK-04 with only its own denom — without the denom filter the uusdc
// account would wrongly absorb the uatom deposit (500000 != 505000) and break ZK-04.
func TestWitnessBuild_MultiDenomSameOwner(t *testing.T) {
	ls := state.NewLocalState()
	depUSDC := types.DepositRecord{DepositID: "dep-usdc", Owner: aliceAddr, Denom: "uusdc", Amount: "500000"}
	depATOM := types.DepositRecord{DepositID: "dep-atom", Owner: aliceAddr, Denom: "uatom", Amount: "5000"}
	oldRoot := ls.Root()
	if _, err := ls.ApplyDeposit(depUSDC); err != nil {
		t.Fatalf("apply usdc: %v", err)
	}
	if _, err := ls.ApplyDeposit(depATOM); err != nil {
		t.Fatalf("apply atom: %v", err)
	}
	newRoot := ls.Root()

	w, err := batch.NewWitnessBuilder().Build(batch.WitnessInputs{
		Settlement: batch.SettlementInputs{
			OldStateRoot: oldRoot,
			NewStateRoot: newRoot,
			Deposits:     []types.DepositRecord{depUSDC, depATOM},
		},
		Accounts: []batch.AccountWitnessSecret{
			{Owner: aliceAddr, UserSecret: aliceSecret, Denom: "uusdc", OldBalance: "0", NewBalance: "500000"},
			{Owner: aliceAddr, UserSecret: aliceSecret, Denom: "uatom", OldBalance: "0", NewBalance: "5000"},
		},
	})
	if err != nil {
		t.Fatalf("multi-denom same-owner witness should build, got %v", err)
	}
	if len(w.Accounts) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(w.Accounts))
	}
	got := map[string]string{}
	for _, a := range w.Accounts {
		got[a.Denom] = a.NewBalance
	}
	if got["uusdc"] != "500000" || got["uatom"] != "5000" {
		t.Fatalf("per-denom balances wrong: %+v", got)
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

func TestBuild_InvalidNullifierAndDestinationHash(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*batch.SettlementInputs)
		wantSubstr string
	}{
		{"nullifier empty", func(in *batch.SettlementInputs) { in.Withdrawals[0].Nullifier = "" }, "nullifier is empty"},
		{"nullifier no 0x", func(in *batch.SettlementInputs) { in.Withdrawals[0].Nullifier = "abc" }, "missing 0x"},
		{"destinationHash empty", func(in *batch.SettlementInputs) { in.Withdrawals[0].DestinationHash = "" }, "destinationHash is empty"},
		{"destinationHash no 0x", func(in *batch.SettlementInputs) { in.Withdrawals[0].DestinationHash = "ff" }, "missing 0x"},
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
		{"deposit id empty", func(in *batch.SettlementInputs) { in.Deposits[0].DepositID = "" }, "deposit.depositId is empty"},
		{"deposit owner empty", func(in *batch.SettlementInputs) { in.Deposits[0].Owner = "" }, "deposit.owner is empty"},
		{"deposit amount zero", func(in *batch.SettlementInputs) { in.Deposits[0].Amount = "0" }, "deposit.amount"},
		{"deposit amount negative", func(in *batch.SettlementInputs) { in.Deposits[0].Amount = "-1" }, "deposit.amount"},
		{"deposit amount junk", func(in *batch.SettlementInputs) { in.Deposits[0].Amount = "abc" }, "deposit.amount"},
		{"withdraw id empty", func(in *batch.SettlementInputs) { in.Withdrawals[0].Request.WithdrawID = "" }, "withdraw.withdrawId is empty"},
		{"withdraw owner empty", func(in *batch.SettlementInputs) { in.Withdrawals[0].Request.Owner = "" }, "withdraw.owner is empty"},
		{"withdraw destination empty", func(in *batch.SettlementInputs) { in.Withdrawals[0].Request.Destination = "" }, "withdraw.destination is empty"},
		{"withdraw amount zero", func(in *batch.SettlementInputs) { in.Withdrawals[0].Request.Amount = "0" }, "withdraw.amount"},
		{"withdraw nonce junk", func(in *batch.SettlementInputs) { in.Withdrawals[0].Request.Nonce = "x" }, "withdraw.nonce"},
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
	in.Deposits[0].Amount = "0100"              // leading zero
	in.Withdrawals[0].Request.Amount = "  40  " // whitespace

	upd, err := batch.NewSettlementUpdateBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if upd.Deposits[0].Amount != "100" {
		t.Fatalf("Deposits[0].Amount must be canonical: want 100, got %s", upd.Deposits[0].Amount)
	}
	if upd.Withdrawals[0].Amount != "40" {
		t.Fatalf("Withdrawals[0].Amount must be canonical: want 40, got %s", upd.Withdrawals[0].Amount)
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
			in.NewStateRoot = "0x" + strings.Repeat("c", 60) + lpad(i+1)
			in.Withdrawals[0].Nullifier = "0x" + strings.Repeat("d", 60) + lpad(i+1)
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
	// Canary kiểm tra mọi field trong Agreements schema cho
	// SettlementUpdate đều được populate. Nếu pkg/types thêm field
	// mới mà Build quên fill, test này phải đỏ.
	upd, err := batch.NewSettlementUpdateBuilder().Build(canonicalAlice(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if upd.BatchID == "" || upd.OldStateRoot == "" || upd.NewStateRoot == "" {
		t.Fatalf("SettlementUpdate có root/id rỗng: %+v", upd)
	}
	if len(upd.Deposits) == 0 || len(upd.Withdrawals) == 0 {
		t.Fatalf("SettlementUpdate phải có ít nhất 1 deposit + 1 withdrawal cho Alice vector")
	}
	d := upd.Deposits[0]
	if d.DepositID == "" || d.Owner == "" || d.Denom == "" || d.Amount == "" {
		t.Fatalf("Deposits[0] có field rỗng: %+v", d)
	}
	w := upd.Withdrawals[0]
	if w.WithdrawID == "" || w.Owner == "" || w.Denom == "" || w.Amount == "" ||
		w.Destination == "" || w.DestinationHash == "" || w.Nullifier == "" {
		t.Fatalf("Withdrawals[0] có field rỗng: %+v", w)
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
