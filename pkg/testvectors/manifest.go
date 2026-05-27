package testvectors

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	// ScenarioName chốt slug folder. KHÔNG dùng filepath ad-hoc ở
	// consumer — gọi PathFor(filename) hoặc các Load* function.
	ScenarioName = "alice_100_40"

	// ManifestFile là tên MANIFEST.json mà generator emit. STATE-11 chốt.
	ManifestFile = "MANIFEST.json"

	// ExpectedVectorVersion phải khớp `vectorVersion` const trong
	// gen_state_vectors. Bump cả hai cùng nhau khi ZK-02 chuyển hash
	// circuit hoặc bất kỳ schema breaking change nào. Test
	// TestManifest_VersionPinned catch drift.
	ExpectedVectorVersion = "v0"

	// ExpectedHashAlgorithm phải khớp giá trị manifest emit. STATE-11
	// dùng sha256 placeholder. ZK-02 đổi sang Poseidon thì bump.
	ExpectedHashAlgorithm = "sha256"

	// repoRootMarker dùng để FindRepoRoot xác định gốc module. Match
	// go.mod đảm bảo cùng module với generator.
	repoRootMarker = "go.mod"
)

// Canonical filenames (echo nguyên si từ generator). Consumer reference
// thông qua const để compiler bắt typo thay vì runtime "file not found".
const (
	FileInitialState         = "initial_state.json"
	FileDepositDep1          = "deposit_dep_1.json"
	FileStateAfterDeposit    = "state_after_deposit.json"
	FileWithdrawRequestWd1   = "withdraw_request_wd_1.json"
	FileNullifierWd1         = "nullifier_wd_1.json"
	FileDestinationHashWd1   = "destination_hash_wd_1.json"
	FileStateAfterWithdrawal = "state_after_withdrawal.json"
	FileSettlementUpdate     = "settlement_update_batch_1.json"
	FileBatchCommitments     = "batch_commitments_batch_1.json"
	FileWitness              = "witness_batch_1.json"
	FilePublicInputs         = "public_inputs_batch_1.json"
)

// ErrManifestMismatch là sentinel khi VerifyManifest phát hiện file
// thực tế khác với entry trong manifest (file bị edit tay, mất,
// orphan...). Caller chain bằng errors.Is.
var ErrManifestMismatch = errors.New("testvectors: manifest mismatch")

// ManifestEntry là metadata cho một file trong canonical folder. Khớp
// 1:1 với schema gen_state_vectors emit.
type ManifestEntry struct {
	Name      string   `json:"name"`
	StateTask string   `json:"stateTask"`
	Schema    string   `json:"schema"`
	Consumers []string `json:"consumers"`
	SHA256    string   `json:"sha256"`
	Note      string   `json:"note,omitempty"`
}

// ManifestRoots tóm tắt 3 root canonical của scenario.
type ManifestRoots struct {
	RootA string `json:"rootA"`
	RootB string `json:"rootB"`
	RootC string `json:"rootC"`
}

// ManifestAlice tóm tắt user-facing parameters của Alice scenario.
type ManifestAlice struct {
	Address                   string `json:"address"`
	Denom                     string `json:"denom"`
	DepositAmount             string `json:"depositAmount"`
	WithdrawAmount            string `json:"withdrawAmount"`
	StartingBalance           string `json:"startingBalance"`
	FinalBalance              string `json:"finalBalance"`
	ModuleAccountFinalBalance string `json:"moduleAccountFinalBalance"`
}

// ManifestDomainTags echo các domain tag đang được dùng cho hash
// placeholder. Consumer (đặc biệt P1 verifier on-chain) dùng để
// reconcile với hash function trên chain.
type ManifestDomainTags struct {
	Nullifier           string `json:"nullifier"`
	WithdrawAddress     string `json:"withdrawAddress"`
	DepositsRoot        string `json:"depositsRoot"`
	WithdrawalsRoot     string `json:"withdrawalsRoot"`
	NullifiersRoot      string `json:"nullifiersRoot"`
	WithdrawOutputsRoot string `json:"withdrawOutputsRoot"`
}

// Manifest là toàn bộ schema MANIFEST.json. Tuyệt đối không hand-edit
// file này — chạy generator để regenerate.
type Manifest struct {
	Scenario      string             `json:"scenario"`
	Description   string             `json:"description"`
	VectorVersion string             `json:"vectorVersion"`
	HashAlgorithm string             `json:"hashAlgorithm"`
	Generator     string             `json:"generator"`
	Alice         ManifestAlice      `json:"alice"`
	Roots         ManifestRoots      `json:"roots"`
	DomainTags    ManifestDomainTags `json:"domainTags"`
	Files         []ManifestEntry    `json:"files"`
	Note          string             `json:"note,omitempty"`
}

// FindByName trả về entry trùng tên file, hoặc nil. Dùng cho test +
// debug. Consumer thông thường gọi Load* thẳng.
func (m *Manifest) FindByName(name string) *ManifestEntry {
	for i := range m.Files {
		if m.Files[i].Name == name {
			return &m.Files[i]
		}
	}
	return nil
}

// FindRepoRoot walk up từ pkg/testvectors source location để tìm go.mod
// — đảm bảo locate folder testvectors/alice_100_40 đúng module dù
// caller chạy từ bất kỳ subdirectory nào (test, cmd, script).
//
// Lý do dùng runtime.Caller(0) thay vì os.Getwd(): test invocation đôi
// khi đổi cwd (vd. `go test ./...` chạy từ repo root nhưng `go test
// ./tests/...` đổi cwd vào tests/). Caller(0) trỏ về file này → walk
// up rồi bám vào go.mod là chỉ dấu ổn định.
func FindRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("testvectors: runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, repoRootMarker)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("testvectors: %s not found walking up from %s", repoRootMarker, filepath.Dir(file))
		}
		dir = parent
	}
}

// ScenarioDir trả về absolute path tới testvectors/<ScenarioName>.
func ScenarioDir() (string, error) {
	root, err := FindRepoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "testvectors", ScenarioName), nil
}

// PathFor trả về absolute path cho file tên `name` trong canonical
// folder. Caller TRUYỀN const File* — KHÔNG tự bash chuỗi.
func PathFor(name string) (string, error) {
	dir, err := ScenarioDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// LoadManifest đọc + parse MANIFEST.json. KHÔNG tự verify SHA-256 —
// caller chủ động gọi VerifyManifest để bắt drift.
func LoadManifest() (*Manifest, error) {
	path, err := PathFor(ManifestFile)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("testvectors: read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("testvectors: parse manifest: %w", err)
	}
	if m.Scenario != ScenarioName {
		return nil, fmt.Errorf("%w: manifest.scenario=%q, want %q", ErrManifestMismatch, m.Scenario, ScenarioName)
	}
	return &m, nil
}

// VerifyManifest re-hash từng file trên đĩa và đối chiếu với entry
// trong manifest. Phát hiện 3 dạng drift:
//
//  1. Manifest entry trỏ tới file không tồn tại trên đĩa.
//  2. SHA-256 thực tế khác giá trị trong manifest (file bị edit tay).
//  3. Orphan file trong folder (không có entry manifest) — trừ chính
//     MANIFEST.json và file ẩn (đầu dấu '.').
//
// Mọi mismatch trả về error wrap ErrManifestMismatch — caller chain
// bằng errors.Is.
func VerifyManifest(m *Manifest) error {
	dir, err := ScenarioDir()
	if err != nil {
		return err
	}
	expected := make(map[string]string, len(m.Files))
	for _, f := range m.Files {
		expected[f.Name] = f.SHA256
	}

	for name, want := range expected {
		path := filepath.Join(dir, name)
		got, err := hashFile(path)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrManifestMismatch, name, err)
		}
		if got != want {
			return fmt.Errorf("%w: %s sha256 drift: got %s, want %s", ErrManifestMismatch, name, got, want)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("testvectors: list %s: %w", dir, err)
	}
	var orphans []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == ManifestFile {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}
		// README.md không phải vector, không track trong manifest.
		if strings.EqualFold(name, "README.md") {
			continue
		}
		if _, ok := expected[name]; !ok {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		return fmt.Errorf("%w: orphan file(s) in folder: %s", ErrManifestMismatch, strings.Join(orphans, ", "))
	}
	return nil
}

// hashFile compute SHA-256 của nguyên file, format "0x..." khớp với
// generator output.
func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return "0x" + hex.EncodeToString(h[:]), nil
}
