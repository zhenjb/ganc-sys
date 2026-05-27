package testvectors_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/testvectors"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// TestFailureManifest_LoadAndScope assert subfolder + scenario name.
func TestFailureManifest_LoadAndScope(t *testing.T) {
	m, err := testvectors.LoadFailureManifest()
	if err != nil {
		t.Fatalf("LoadFailureManifest: %v", err)
	}
	if m.Scenario != testvectors.ScenarioName {
		t.Errorf("Scenario=%q, want %q", m.Scenario, testvectors.ScenarioName)
	}
	if m.Subfolder != testvectors.FailureVectorsSubdir {
		t.Errorf("Subfolder=%q, want %q", m.Subfolder, testvectors.FailureVectorsSubdir)
	}
	if len(m.Files) != 4 {
		t.Errorf("Files=%d, want 4 (over_withdraw/wrong_root/duplicate_nullifier/tampered_destination)", len(m.Files))
	}
}

// TestFailureManifest_VersionPinned chốt vectorVersion failure khớp const
// pkg-level. Tách khỏi happy-path version để 2 set vector có thể bump
// độc lập nếu schema failure thay đổi nhưng happy path không.
func TestFailureManifest_VersionPinned(t *testing.T) {
	m, err := testvectors.LoadFailureManifest()
	if err != nil {
		t.Fatalf("LoadFailureManifest: %v", err)
	}
	if m.VectorVersion != testvectors.FailureExpectedVectorVersion {
		t.Errorf("VectorVersion=%q, want %q", m.VectorVersion, testvectors.FailureExpectedVectorVersion)
	}
}

// TestFailureManifest_VerifyIntegrity re-hash từng failure JSON.
func TestFailureManifest_VerifyIntegrity(t *testing.T) {
	m, err := testvectors.LoadFailureManifest()
	if err != nil {
		t.Fatalf("LoadFailureManifest: %v", err)
	}
	if err := testvectors.VerifyFailureManifest(m); err != nil {
		t.Fatalf("VerifyFailureManifest: %v", err)
	}
}

// TestFailureManifest_AllFilesCovered đảm bảo 4 const File* có entry.
func TestFailureManifest_AllFilesCovered(t *testing.T) {
	m, err := testvectors.LoadFailureManifest()
	if err != nil {
		t.Fatalf("LoadFailureManifest: %v", err)
	}
	wanted := []string{
		testvectors.FileOverWithdraw,
		testvectors.FileWrongRoot,
		testvectors.FileDuplicateNullifier,
		testvectors.FileTamperedDestination,
	}
	for _, name := range wanted {
		if entry := m.FindByName(name); entry == nil {
			t.Errorf("failure manifest thiếu entry cho %s", name)
		}
	}
	if len(m.Files) != len(wanted) {
		t.Errorf("manifest có %d files, const liệt kê %d", len(m.Files), len(wanted))
	}
}

// TestFailureManifest_HappyPathRootsMatch đảm bảo HappyPathRoots khớp
// happy-path manifest — nếu drift, ai đó đã regenerate 1 trong 2 mà
// quên đồng bộ.
func TestFailureManifest_HappyPathRootsMatch(t *testing.T) {
	hp, err := testvectors.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest happy: %v", err)
	}
	fm, err := testvectors.LoadFailureManifest()
	if err != nil {
		t.Fatalf("LoadFailureManifest: %v", err)
	}
	if fm.HappyPathRoots != hp.Roots {
		t.Errorf("happyPathRoots drift: failure=%+v, happy=%+v", fm.HappyPathRoots, hp.Roots)
	}
}

// TestFailureManifest_VerifyErrorWrap kiểm sentinel chain — đột biến SHA.
func TestFailureManifest_VerifyErrorWrap(t *testing.T) {
	m, err := testvectors.LoadFailureManifest()
	if err != nil {
		t.Fatalf("LoadFailureManifest: %v", err)
	}
	fake := *m
	fake.Files = append([]testvectors.FailureManifestEntry(nil), m.Files...)
	fake.Files[0].SHA256 = "0xdeadbeef"
	gotErr := testvectors.VerifyFailureManifest(&fake)
	if gotErr == nil {
		t.Fatalf("expected mismatch error, got nil")
	}
	if !errors.Is(gotErr, testvectors.ErrFailureManifestMismatch) {
		t.Errorf("error %v doesn't wrap ErrFailureManifestMismatch", gotErr)
	}
}

// TestFailureBundle_LoadAndSanity load toàn bundle + chạy SanityCheck.
func TestFailureBundle_LoadAndSanity(t *testing.T) {
	b, err := testvectors.LoadFailureBundle()
	if err != nil {
		t.Fatalf("LoadFailureBundle: %v", err)
	}
	if err := b.SanityCheck(); err != nil {
		t.Fatalf("SanityCheck: %v", err)
	}
}

// TestOverWithdraw_RejectedByState04 chạy đúng pipeline mà vector mô tả
// (re-build LocalState theo AccountSnapshot rồi gọi STATE-04 builder
// với intent vượt balance) và assert sentinel khớp rejection.
func TestOverWithdraw_RejectedByState04(t *testing.T) {
	v, err := testvectors.LoadOverWithdraw()
	if err != nil {
		t.Fatalf("LoadOverWithdraw: %v", err)
	}

	// Setup LocalState ở snapshot moment: deposit 100 đã credit Alice.
	ls := state.NewLocalState()
	dep, err := testvectors.LoadDeposit()
	if err != nil {
		t.Fatalf("LoadDeposit: %v", err)
	}
	if _, err := ls.ApplyDeposit(dep); err != nil {
		t.Fatalf("ApplyDeposit: %v", err)
	}
	acc := ls.Account(v.AccountSnapshot.Owner, v.AccountSnapshot.Denom)
	if acc.Balance != v.AccountSnapshot.Balance {
		t.Fatalf("snapshot balance drift: ls=%s, vector=%s", acc.Balance, v.AccountSnapshot.Balance)
	}

	wb := state.NewWithdrawRequestBuilder(ls)
	_, err = wb.Build(state.WithdrawIntent{
		Owner:       v.Intent.Owner,
		Denom:       v.Intent.Denom,
		Amount:      v.Intent.Amount,
		Destination: v.Intent.Destination,
	})
	if err == nil {
		t.Fatalf("expected reject; STATE-04 builder accepted intent amount=%s vs balance=%s",
			v.Intent.Amount, acc.Balance)
	}
	if !errors.Is(err, state.ErrInsufficientBalance) {
		t.Errorf("error %v không wrap state.ErrInsufficientBalance (vector.sentinel=%s)",
			err, v.Rejection.Sentinel)
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(v.Rejection.MessageContains)) {
		t.Errorf("error %q không chứa substring %q", err.Error(), v.Rejection.MessageContains)
	}
}

// TestOverWithdraw_RejectedByBatchLocalBuilder đảm bảo P3 batch facade
// (P4 BatchService đi qua đây) cũng map sang ErrInsufficientOffchainBalance.
//
// Đường đi: thay vì stop ở STATE-04, ta forge một WithdrawRequest amount=200
// (giả sử upstream skip STATE-04) và đẩy thẳng vào LocalBuilder.Build —
// STATE-05 sẽ reject với state.ErrInsufficientBalance, facade map sang
// batch.ErrInsufficientOffchainBalance để P4 trả HTTP 400 sạch.
func TestOverWithdraw_RejectedByBatchLocalBuilder(t *testing.T) {
	v, err := testvectors.LoadOverWithdraw()
	if err != nil {
		t.Fatalf("LoadOverWithdraw: %v", err)
	}
	dep, err := testvectors.LoadDeposit()
	if err != nil {
		t.Fatalf("LoadDeposit: %v", err)
	}
	happy, err := testvectors.LoadAliceScenario()
	if err != nil {
		t.Fatalf("LoadAliceScenario: %v", err)
	}

	forged := happy.WithdrawRequest
	forged.Amount = v.Intent.Amount // "200"

	lb := batch.NewLocalBuilder()
	_, err = lb.Build(context.Background(), batch.BuildInput{
		OldStateRoot:     happy.SettlementUpdate.OldStateRoot,
		Deposits:         []types.DepositRecord{dep},
		WithdrawRequests: []types.WithdrawRequest{forged},
	})
	if err == nil {
		t.Fatalf("expected reject from LocalBuilder.Build")
	}
	if !errors.Is(err, batch.ErrInsufficientOffchainBalance) {
		t.Errorf("error %v không wrap batch.ErrInsufficientOffchainBalance", err)
	}
}

// TestWrongRoot_PublicInputsConsistent ghép wrong_root vector và assert
// publicInputs[0] = TamperedSettlement.OldStateRoot (KHÔNG phải rootB).
// Nếu generator quên re-derive public inputs, test này catch.
func TestWrongRoot_PublicInputsConsistent(t *testing.T) {
	v, err := testvectors.LoadWrongRoot()
	if err != nil {
		t.Fatalf("LoadWrongRoot: %v", err)
	}
	if len(v.PublicInputs) != 6 {
		t.Fatalf("PublicInputs len=%d, want 6", len(v.PublicInputs))
	}
	if v.PublicInputs[0] != v.TamperedSettlement.OldStateRoot {
		t.Errorf("publicInputs[0]=%s, want tamperedSettlement.OldStateRoot=%s",
			v.PublicInputs[0], v.TamperedSettlement.OldStateRoot)
	}
	if v.TamperedSettlement.OldStateRoot == v.CorrectOldStateRoot {
		t.Errorf("tampered.OldStateRoot == correctOldStateRoot=%s (mutation invalidated)",
			v.CorrectOldStateRoot)
	}

	// Re-derive batch commitments from tampered settlement and assert
	// match — vector stays self-consistent even when OldStateRoot drift.
	rederived := batch.BuildCommitments(v.TamperedSettlement)
	if rederived != v.BatchCommitments {
		t.Errorf("BatchCommitments drift; rederived=%+v, vector=%+v", rederived, v.BatchCommitments)
	}
}

// TestDuplicateNullifier_StateApplyRejectsReplay đẩy 2 withdrawal cùng
// nullifier qua LocalState.ApplyWithdrawal tuần tự — STATE-05 phải
// reject lần thứ hai với ErrWithdrawAlreadyApplied.
func TestDuplicateNullifier_StateApplyRejectsReplay(t *testing.T) {
	v, err := testvectors.LoadDuplicateNullifier()
	if err != nil {
		t.Fatalf("LoadDuplicateNullifier: %v", err)
	}
	if len(v.SettlementUpdate.Withdrawals) < 2 {
		t.Fatalf("vector needs ≥ 2 withdrawals, got %d", len(v.SettlementUpdate.Withdrawals))
	}

	// Build state với 1 deposit lớn để có balance đủ cho 2 lần withdraw
	// (40+20 = 60). Không quan trọng số dư ở đây — quan trọng là kiểm
	// tra nullifier replay, không phải kiểm tra balance.
	ls := state.NewLocalState()
	dep, err := testvectors.LoadDeposit()
	if err != nil {
		t.Fatalf("LoadDeposit: %v", err)
	}
	if _, err := ls.ApplyDeposit(dep); err != nil {
		t.Fatalf("ApplyDeposit: %v", err)
	}

	happy, err := testvectors.LoadAliceScenario()
	if err != nil {
		t.Fatalf("LoadAliceScenario: %v", err)
	}
	if _, err := ls.ApplyWithdrawal(happy.WithdrawRequest, v.Nullifier); err != nil {
		t.Fatalf("ApplyWithdrawal first: %v", err)
	}

	// Forge second request reusing same nullifier (and same nonce —
	// state will reject before nonce check fires; we want sentinel
	// ErrWithdrawAlreadyApplied specifically).
	replay := happy.WithdrawRequest
	replay.WithdrawID = "wd-2-replay"
	replay.Amount = "20"
	_, err = ls.ApplyWithdrawal(replay, v.Nullifier)
	if err == nil {
		t.Fatalf("expected ErrWithdrawAlreadyApplied; got nil")
	}
	if !errors.Is(err, state.ErrWithdrawAlreadyApplied) {
		t.Errorf("error %v không wrap state.ErrWithdrawAlreadyApplied", err)
	}
}

// TestTamperedDestination_HashDoesNotMatchRecompute là invariant chính:
// recompute H(tampered destination) phải KHÁC OriginalDestinationHash.
// Đây là điều P1 verifier / ZK-07 circuit dựa vào để reject.
func TestTamperedDestination_HashDoesNotMatchRecompute(t *testing.T) {
	v, err := testvectors.LoadTamperedDestination()
	if err != nil {
		t.Fatalf("LoadTamperedDestination: %v", err)
	}

	recomputed, err := state.WithdrawAddressHash(v.TamperedDestination)
	if err != nil {
		t.Fatalf("WithdrawAddressHash tampered: %v", err)
	}
	if recomputed == v.OriginalDestinationHash {
		t.Errorf("H(tampered)=%s == OriginalDestinationHash — mutation không hiệu lực",
			recomputed)
	}
	if v.TamperedSettlement.Withdrawals[0].DestinationHash != v.OriginalDestinationHash {
		t.Errorf("settlement.DestinationHash=%s != OriginalDestinationHash=%s (mutation invalidated)",
			v.TamperedSettlement.Withdrawals[0].DestinationHash, v.OriginalDestinationHash)
	}

	// Sanity: H(original) PHẢI match OriginalDestinationHash.
	origHash, err := state.WithdrawAddressHash(v.OriginalDestination)
	if err != nil {
		t.Fatalf("WithdrawAddressHash original: %v", err)
	}
	if origHash != v.OriginalDestinationHash {
		t.Errorf("H(original)=%s != OriginalDestinationHash=%s (vector self-inconsistent)",
			origHash, v.OriginalDestinationHash)
	}
}
