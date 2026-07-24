package batch_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// localBuilderAliceInput tái dựng input canonical cho LocalBuilder:
// Alice deposit 100, withdraw 40 trong CÙNG một batch.
//
// OldStateRoot là placeholder "0xrootA" — facade KHÔNG so sánh với
// fresh LocalState root (echo-only metadata). Withdraw nonce phải =
// account.Nonce + 1 = 1 vì fresh state có nonce=0.
//
// AccountSecrets có truyền hoặc không đều phải chạy được; helper này
// truyền explicit để đảm bảo determinism của nullifier expected.
func localBuilderAliceInput(t *testing.T, withSecret bool) batch.BuildInput {
	t.Helper()
	in := batch.BuildInput{
		OldStateRoot: "0xrootA",
		Deposits: []types.DepositRecord{
			{
				DepositID:     "dep-1",
				Owner:         aliceAddr,
				Denom:         denom,
				Amount:        "100",
				Processed:     false,
				CreatedHeight: 12345,
			},
		},
		WithdrawRequests: []types.WithdrawRequest{
			{
				WithdrawID:  "wd-1",
				Owner:       aliceAddr,
				Denom:       denom,
				Amount:      "40",
				Destination: aliceAddr,
				Nonce:       "1",
			},
		},
	}
	if withSecret {
		in.AccountSecrets = []batch.AccountSecret{
			{Owner: aliceAddr, UserSecret: aliceSecret},
		}
	}
	return in
}

func TestLocalBuilder_Build_CanonicalAlice_WithExplicitSecret(t *testing.T) {
	b := batch.NewLocalBuilder()
	out, err := b.Build(context.Background(), localBuilderAliceInput(t, true))
	if err != nil {
		t.Fatalf("Build: unexpected error: %v", err)
	}

	if out.SettlementUpdate.BatchID != "batch-1" {
		t.Errorf("BatchID = %q, want batch-1", out.SettlementUpdate.BatchID)
	}
	if out.SettlementUpdate.OldStateRoot != "0xrootA" {
		t.Errorf("OldStateRoot = %q, want echo of input 0xrootA", out.SettlementUpdate.OldStateRoot)
	}
	if out.SettlementUpdate.NewStateRoot == "" || out.SettlementUpdate.NewStateRoot == "0xrootA" {
		t.Errorf("NewStateRoot must differ from OldStateRoot, got %q", out.SettlementUpdate.NewStateRoot)
	}

	if len(out.SettlementUpdate.Deposits) != 1 || out.SettlementUpdate.Deposits[0].DepositID != "dep-1" {
		t.Errorf("Deposits = %+v, want 1 dep-1", out.SettlementUpdate.Deposits)
	}
	if len(out.SettlementUpdate.Withdrawals) != 1 {
		t.Fatalf("Withdrawals len = %d, want 1", len(out.SettlementUpdate.Withdrawals))
	}
	w := out.SettlementUpdate.Withdrawals[0]
	if w.WithdrawID != "wd-1" || w.Amount != "40" {
		t.Errorf("Withdrawal entry mismatch: %+v", w)
	}

	expectedNullifier, err := state.NullifierFor(aliceSecret, "1")
	if err != nil {
		t.Fatalf("NullifierFor: %v", err)
	}
	if w.Nullifier != expectedNullifier {
		t.Errorf("Nullifier = %s, want %s", w.Nullifier, expectedNullifier)
	}
	expectedDestHash, err := state.WithdrawAddressHash(aliceAddr)
	if err != nil {
		t.Fatalf("WithdrawAddressHash: %v", err)
	}
	if w.DestinationHash != expectedDestHash {
		t.Errorf("DestinationHash = %s, want %s", w.DestinationHash, expectedDestHash)
	}

	if len(out.Witness.Accounts) != 1 {
		t.Fatalf("Witness.Accounts len = %d, want 1", len(out.Witness.Accounts))
	}
	wa := out.Witness.Accounts[0]
	if wa.Owner != aliceAddr || wa.UserSecret != aliceSecret {
		t.Errorf("WitnessAccount identity = %+v", wa)
	}
	if wa.OldBalance != "0" || wa.NewBalance != "60" {
		t.Errorf("WitnessAccount balances = (old=%s, new=%s), want (0, 60)", wa.OldBalance, wa.NewBalance)
	}
	if wa.Nonce != "1" {
		t.Errorf("WitnessAccount nonce = %s, want 1", wa.Nonce)
	}

	if out.BatchCommitments.DepositsRoot == "" || out.BatchCommitments.WithdrawalsRoot == "" ||
		out.BatchCommitments.NullifiersRoot == "" || out.BatchCommitments.WithdrawOutputsRoot == "" {
		t.Errorf("All BatchCommitments roots must be non-empty: %+v", out.BatchCommitments)
	}
}

func TestLocalBuilder_Build_CanonicalAlice_FallbackMockSecret(t *testing.T) {
	// P4 hôm nay không truyền AccountSecrets — facade phải fallback về secret
	// MOCK PER-OWNER (INT-WD-NULLIFIER-peruser) và vẫn cho ra nullifier hợp lệ
	// (không phải placeholder string). Trước đây fallback là literal DÙNG CHUNG
	// "mock-user-secret" khiến mọi owner đụng cùng nullifier khi nonce trùng.
	out, err := batch.NewLocalBuilder().Build(context.Background(), localBuilderAliceInput(t, false))
	if err != nil {
		t.Fatalf("Build (no secret): unexpected error: %v", err)
	}
	w := out.SettlementUpdate.Withdrawals[0]
	if w.Nullifier == "" || w.Nullifier == "0xmocknullifier" {
		t.Errorf("Nullifier with mock secret must be a real hash, got %q", w.Nullifier)
	}
	// Determinism: secret per-(owner,denom) + nonce "1" luôn cho cùng nullifier,
	// và witness UserSecret PHẢI là chính secret đó để prover re-derive khớp.
	wantSecret := state.WithdrawSecretFor(w.Owner, w.Denom)
	expected, err := state.NullifierFor(wantSecret, "1")
	if err != nil {
		t.Fatalf("NullifierFor per-owner: %v", err)
	}
	if w.Nullifier != expected {
		t.Errorf("Nullifier = %s, want deterministic per-owner %s", w.Nullifier, expected)
	}
	if got := out.Witness.Accounts[0].UserSecret; got != wantSecret {
		t.Errorf("Witness UserSecret = %q, want per-owner fallback %q", got, wantSecret)
	}
}

func TestLocalBuilder_Build_InsufficientBalance(t *testing.T) {
	in := localBuilderAliceInput(t, true)
	in.WithdrawRequests[0].Amount = "500"

	_, err := batch.NewLocalBuilder().Build(context.Background(), in)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, batch.ErrInsufficientOffchainBalance) {
		t.Errorf("err = %v, want ErrInsufficientOffchainBalance", err)
	}
	if errors.Is(err, state.ErrInsufficientBalance) {
		t.Errorf("err should not chain state.ErrInsufficientBalance, got %v", err)
	}
}

func TestLocalBuilder_Build_DuplicateSecret(t *testing.T) {
	in := localBuilderAliceInput(t, true)
	in.AccountSecrets = append(in.AccountSecrets, batch.AccountSecret{
		Owner:      aliceAddr,
		UserSecret: "another_secret",
	})

	_, err := batch.NewLocalBuilder().Build(context.Background(), in)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, batch.ErrInvalidBuildInput) {
		t.Errorf("err = %v, want ErrInvalidBuildInput", err)
	}
	if !strings.Contains(err.Error(), "duplicate owner") {
		t.Errorf("err message should mention duplicate owner: %v", err)
	}
}

func TestLocalBuilder_Build_EmptyOldStateRootRejected(t *testing.T) {
	in := localBuilderAliceInput(t, true)
	in.OldStateRoot = ""
	_, err := batch.NewLocalBuilder().Build(context.Background(), in)
	if err == nil {
		t.Fatalf("expected error for empty oldStateRoot")
	}
	if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
		t.Errorf("err = %v, want ErrInvalidSettlementInputs", err)
	}
}

func TestLocalBuilder_Build_EmptyBatchRejected(t *testing.T) {
	_, err := batch.NewLocalBuilder().Build(context.Background(), batch.BuildInput{
		OldStateRoot: "0xrootA",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, batch.ErrInvalidSettlementInputs) {
		t.Errorf("err = %v, want ErrInvalidSettlementInputs", err)
	}
}

func TestLocalBuilder_Build_DepositOnlyBatch(t *testing.T) {
	in := batch.BuildInput{
		OldStateRoot: "0xrootA",
		Deposits: []types.DepositRecord{
			{DepositID: "dep-1", Owner: aliceAddr, Denom: denom, Amount: "100"},
		},
		AccountSecrets: []batch.AccountSecret{
			{Owner: aliceAddr, UserSecret: aliceSecret},
		},
	}
	out, err := batch.NewLocalBuilder().Build(context.Background(), in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(out.SettlementUpdate.Deposits) != 1 || len(out.SettlementUpdate.Withdrawals) != 0 {
		t.Errorf("deposit-only batch shape mismatch: %+v", out.SettlementUpdate)
	}
	if out.Witness.Accounts[0].NewBalance != "100" {
		t.Errorf("expected newBalance=100, got %s", out.Witness.Accounts[0].NewBalance)
	}
	if out.Witness.Accounts[0].Nonce != "0" {
		t.Errorf("deposit-only owner should keep nonce 0, got %s", out.Witness.Accounts[0].Nonce)
	}
}

func TestLocalBuilder_Build_CtxCancelledShortCircuit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := batch.NewLocalBuilder().Build(ctx, localBuilderAliceInput(t, true))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestLocalBuilder_Build_SeqIncrement(t *testing.T) {
	b := batch.NewLocalBuilder()
	out1, err := b.Build(context.Background(), localBuilderAliceInput(t, true))
	if err != nil {
		t.Fatalf("Build #1: %v", err)
	}
	if out1.SettlementUpdate.BatchID != "batch-1" {
		t.Errorf("first batchId = %s, want batch-1", out1.SettlementUpdate.BatchID)
	}
	out2, err := b.Build(context.Background(), localBuilderAliceInput(t, true))
	if err != nil {
		t.Fatalf("Build #2: %v", err)
	}
	if out2.SettlementUpdate.BatchID != "batch-2" {
		t.Errorf("second batchId = %s, want batch-2", out2.SettlementUpdate.BatchID)
	}
}

func TestLocalBuilder_Build_ConcurrentSafe(t *testing.T) {
	b := batch.NewLocalBuilder()
	const n = 16

	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := b.Build(context.Background(), localBuilderAliceInput(t, true)); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Build error: %v", err)
	}
	if got := b.Seq(); got != n {
		t.Errorf("Seq after %d builds = %d", n, got)
	}
}

func TestLocalBuilder_SatisfiesBuilderInterface(t *testing.T) {
	var _ batch.Builder = batch.NewLocalBuilder()
}
