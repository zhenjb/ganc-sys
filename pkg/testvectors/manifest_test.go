package testvectors_test

import (
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/testvectors"
)

// TestManifest_LoadAndScenario assert manifest tồn tại + scenario name
// đúng.
func TestManifest_LoadAndScenario(t *testing.T) {
	m, err := testvectors.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Scenario != testvectors.ScenarioName {
		t.Errorf("Scenario=%q, want %q", m.Scenario, testvectors.ScenarioName)
	}
	if len(m.Files) == 0 {
		t.Errorf("manifest.Files empty — generator chưa chạy?")
	}
}

// TestManifest_VersionPinned đảm bảo manifest version khớp const
// ExpectedVectorVersion. Khi ZK-02 bump version, dev phải sửa đồng thời
// 2 chỗ — test này catch trường hợp quên 1 bên.
func TestManifest_VersionPinned(t *testing.T) {
	m, err := testvectors.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.VectorVersion != testvectors.ExpectedVectorVersion {
		t.Errorf("VectorVersion=%q, want %q (bump cả manifest + const)",
			m.VectorVersion, testvectors.ExpectedVectorVersion)
	}
	if m.HashAlgorithm != testvectors.ExpectedHashAlgorithm {
		t.Errorf("HashAlgorithm=%q, want %q", m.HashAlgorithm, testvectors.ExpectedHashAlgorithm)
	}
}

// TestManifest_VerifyIntegrity re-hash từng file canonical và đối
// chiếu manifest. Đây là determinism test — nếu fail nghĩa là:
//
//   - Ai đó edit tay JSON file mà không chạy generator, HOẶC
//   - File bị xoá / thêm trộm, HOẶC
//   - Generator output non-deterministic (bug).
func TestManifest_VerifyIntegrity(t *testing.T) {
	m, err := testvectors.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if err := testvectors.VerifyManifest(m); err != nil {
		t.Fatalf("VerifyManifest: %v", err)
	}
}

// TestManifest_AllFilesCovered double-check rằng từng const File* có
// một entry trong manifest. Bắt regression khi thêm const mới mà quên
// generate, hoặc xoá file mà quên xoá const.
func TestManifest_AllFilesCovered(t *testing.T) {
	m, err := testvectors.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	wanted := []string{
		testvectors.FileInitialState,
		testvectors.FileDepositDep1,
		testvectors.FileStateAfterDeposit,
		testvectors.FileWithdrawRequestWd1,
		testvectors.FileNullifierWd1,
		testvectors.FileDestinationHashWd1,
		testvectors.FileStateAfterWithdrawal,
		testvectors.FileSettlementUpdate,
		testvectors.FileBatchCommitments,
		testvectors.FileWitness,
		testvectors.FilePublicInputs,
	}
	for _, name := range wanted {
		if entry := m.FindByName(name); entry == nil {
			t.Errorf("manifest thiếu entry cho %s", name)
		}
	}
	if len(m.Files) != len(wanted) {
		t.Errorf("manifest có %d files, const liệt kê %d — kiểm tra orphan/missing",
			len(m.Files), len(wanted))
	}
}

// TestManifest_FindRepoRoot smoke test resolver folder không phụ thuộc
// cwd.
func TestManifest_FindRepoRoot(t *testing.T) {
	root, err := testvectors.FindRepoRoot()
	if err != nil {
		t.Fatalf("FindRepoRoot: %v", err)
	}
	if root == "" {
		t.Errorf("repo root empty")
	}
}

// TestManifest_VerifyErrorWrap đảm bảo VerifyManifest mismatch sentinel
// chain đúng cho errors.Is — gọi với manifest giả đột biến SHA.
func TestManifest_VerifyErrorWrap(t *testing.T) {
	m, err := testvectors.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	// Đột biến một entry SHA → expect ErrManifestMismatch.
	fake := *m
	fake.Files = append([]testvectors.ManifestEntry(nil), m.Files...)
	fake.Files[0].SHA256 = "0xdeadbeef"
	gotErr := testvectors.VerifyManifest(&fake)
	if gotErr == nil {
		t.Fatalf("expected mismatch error, got nil")
	}
	if !errors.Is(gotErr, testvectors.ErrManifestMismatch) {
		t.Errorf("error %v doesn't wrap ErrManifestMismatch", gotErr)
	}
}
