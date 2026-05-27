package testvectors_test

import (
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/testvectors"
)

// TestAlice_LoadScenario assert load tất cả file thành công và parse
// đúng schema (không nil/empty key field).
func TestAlice_LoadScenario(t *testing.T) {
	s, err := testvectors.LoadAliceScenario()
	if err != nil {
		t.Fatalf("LoadAliceScenario: %v", err)
	}
	if s.Manifest == nil {
		t.Fatalf("Manifest nil")
	}
	if s.Deposit.DepositID != "dep-1" {
		t.Errorf("Deposit.DepositID=%q, want dep-1", s.Deposit.DepositID)
	}
	if s.WithdrawRequest.WithdrawID != "wd-1" {
		t.Errorf("WithdrawRequest.WithdrawID=%q, want wd-1", s.WithdrawRequest.WithdrawID)
	}
	if s.SettlementUpdate.BatchID != "batch-1" {
		t.Errorf("SettlementUpdate.BatchID=%q, want batch-1", s.SettlementUpdate.BatchID)
	}
	if len(s.SettlementUpdate.Deposits) != 1 {
		t.Errorf("SettlementUpdate.Deposits len=%d, want 1", len(s.SettlementUpdate.Deposits))
	}
	if len(s.SettlementUpdate.Withdrawals) != 1 {
		t.Errorf("SettlementUpdate.Withdrawals len=%d, want 1", len(s.SettlementUpdate.Withdrawals))
	}
	if s.PublicInputs.Count != 6 {
		t.Errorf("PublicInputs.Count=%d, want 6", s.PublicInputs.Count)
	}
}

// TestAlice_SanityCheck chạy cross-file invariant check — bảo vệ
// trường hợp generator có bug semantic mà manifest sha256 vẫn match
// (vd. tất cả file đúng sha nhưng quan hệ giữa các file sai).
func TestAlice_SanityCheck(t *testing.T) {
	s := testvectors.MustLoadAliceScenario()
	if err := s.SanityCheck(); err != nil {
		t.Fatalf("SanityCheck: %v", err)
	}
}

// TestAlice_RootsProgression lock 3 root từ scenario:
//   - rootA = root khởi tạo (no account)
//   - rootB = root sau khi deposit 100 → settlementUpdate.oldStateRoot
//   - rootC = root sau khi withdraw 40 → settlementUpdate.newStateRoot
func TestAlice_RootsProgression(t *testing.T) {
	s := testvectors.MustLoadAliceScenario()
	if s.Manifest.Roots.RootA != s.InitialState.Root {
		t.Errorf("manifest.RootA=%s != initial_state.Root=%s",
			s.Manifest.Roots.RootA, s.InitialState.Root)
	}
	if s.Manifest.Roots.RootB != s.StateAfterDeposit.Root {
		t.Errorf("manifest.RootB=%s != state_after_deposit.Root=%s",
			s.Manifest.Roots.RootB, s.StateAfterDeposit.Root)
	}
	if s.Manifest.Roots.RootC != s.StateAfterWithdrawal.Root {
		t.Errorf("manifest.RootC=%s != state_after_withdrawal.Root=%s",
			s.Manifest.Roots.RootC, s.StateAfterWithdrawal.Root)
	}
}

// TestAlice_StaticVsRuntimeSecret tài liệu hóa distinction giữa:
//
//   - Static folder: userSecret="alice_secret" (P3 baseline để verify
//     ZK-05 với nullifier const 0x1a1fdf…22f7).
//   - Runtime mock pipeline: P4 BatchService fallback "mock-user-secret"
//     khi AccountSecrets không truyền (chốt trong Agreements canonical
//     Witness JSON, lock bởi tests/int02).
//
// Hai literal CÙNG TỒN TẠI có chủ đích — test này lock invariant đó.
func TestAlice_StaticVsRuntimeSecret(t *testing.T) {
	s := testvectors.MustLoadAliceScenario()
	if len(s.Witness.Accounts) != 1 {
		t.Fatalf("witness accounts len=%d, want 1", len(s.Witness.Accounts))
	}
	if got := s.Witness.Accounts[0].UserSecret; got != "alice_secret" {
		t.Errorf("static vector userSecret=%q, want alice_secret (KHÁC runtime fallback 'mock-user-secret')", got)
	}
	if got := s.Nullifier.UserSecret; got != "alice_secret" {
		t.Errorf("nullifier vector userSecret=%q, want alice_secret", got)
	}
}
