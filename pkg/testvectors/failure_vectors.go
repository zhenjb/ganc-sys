package testvectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-12 — Failure vectors.
//
// Folder layout: testvectors/<ScenarioName>/<FailureVectorsSubdir>/.
// Tách hẳn khỏi happy path để consumer (P1 verifier, P2 circuit, P4 backend)
// KHÔNG vô tình mix một failure case vào pipeline xanh. Mỗi file ở đây là
// một "negative" — chạy đúng pipeline với input này PHẢI bị reject ở stage
// đã ghi trong rejection.stage.
//
// Bốn case canonical (Edge cases panel — `zkdex_cosmos_deposit_withdraw_flows.html`):
//
//  1. over_withdraw         — STATE-04 / STATE-05 reject balance không đủ.
//  2. wrong_root            — P1 chain MsgSubmitBatchProof reject oldStateRoot lệch.
//  3. duplicate_nullifier   — P1 chain (hoặc STATE-05) reject replay nullifier.
//  4. tampered_destination  — P1 verifier / P2 circuit reject destinationHash không khớp destination.
//
// Hai tầng integrity hoàn toàn tương tự happy path:
//
//   - Cấp 1 (`VerifyFailureManifest`): SHA-256 từng JSON file → bắt hand-edit.
//   - Cấp 2 (`FailureBundle.SanityCheck`): semantic invariants chéo file → bắt generator bug.

const (
	// FailureVectorsSubdir chốt slug subfolder. KHÔNG dùng filepath ad-hoc
	// ở consumer — gọi FailurePathFor(filename) hoặc các LoadFailure* function.
	FailureVectorsSubdir = "failure_vectors"

	// FailureManifestFile là tên MANIFEST.json riêng cho subfolder. Tách
	// khỏi happy-path manifest để bump vectorVersion/SHA-256 độc lập khi
	// một case thay đổi (vd. ZK-02 chốt Poseidon → recompute commitments
	// trong wrong_root + tampered_destination + duplicate_nullifier).
	FailureManifestFile = "MANIFEST.json"

	// FailureExpectedVectorVersion phải khớp generator. Bump cùng nhau
	// khi schema/format đổi.
	FailureExpectedVectorVersion = "v0"
)

// Canonical filenames cho failure vectors. Consumer reference qua const
// để compiler bắt typo.
const (
	FileOverWithdraw        = "over_withdraw.json"
	FileWrongRoot           = "wrong_root.json"
	FileDuplicateNullifier  = "duplicate_nullifier.json"
	FileTamperedDestination = "tampered_destination.json"
)

// FailureCase là discriminator string (cũng là `case` field trong JSON).
// Const dùng cho switch trong consumer test.
const (
	FailureCaseOverWithdraw        = "over_withdraw"
	FailureCaseWrongRoot           = "wrong_root"
	FailureCaseDuplicateNullifier  = "duplicate_nullifier"
	FailureCaseTamperedDestination = "tampered_destination"
)

// FailureStage* là label định danh điểm mà failure phải được phát hiện.
// Tách thành const để test ngang-role có thể assert "case X reject ở
// stage Y" mà không hard-code string.
const (
	FailureStageState04Builder    = "STATE-04 WithdrawRequestBuilder"
	FailureStageState05Apply      = "STATE-05 LocalState.ApplyWithdrawal"
	FailureStageBatchLocalBuilder = "batch.LocalBuilder.Build (P3 facade)"
	FailureStageState08Settlement = "STATE-08 SettlementUpdateBuilder"
	FailureStageState10PublicIn   = "STATE-10 PublicInputBuilder"
	FailureStageChainVerifier     = "P1 x/zkdex MsgSubmitBatchProof verifier"
	FailureStageCircuitBinding    = "P2 ZK circuit binding (ZK-05/ZK-07)"
)

// ErrFailureManifestMismatch là sentinel khi VerifyFailureManifest phát
// hiện drift trong subfolder failure_vectors. Tách khỏi
// ErrManifestMismatch để consumer chain riêng và biết drift ở đâu.
var ErrFailureManifestMismatch = errors.New("testvectors: failure manifest mismatch")

// FailureRejection mô tả điểm + tín hiệu mà input PHẢI bị reject. Field
// `sentinel` ưu tiên dùng tên Go error sentinel (vd. "state.ErrInsufficientBalance").
// Field `messageContains` là substring expected — caller có thể assert
// strings.Contains(err.Error(), v.Rejection.MessageContains).
type FailureRejection struct {
	Stage           string `json:"stage"`
	Sentinel        string `json:"sentinel"`
	MessageContains string `json:"messageContains"`
	Note            string `json:"note,omitempty"`
}

// FailureMeta là metadata chung cho mọi failure vector. Embed vào từng
// case struct để tránh duplicate field declaration.
type FailureMeta struct {
	Case               string           `json:"case"`
	Title              string           `json:"title"`
	Description        string           `json:"description"`
	ViolatedInvariant  string           `json:"violatedInvariant"`
	HappyPathReference string           `json:"happyPathReference"`
	Mutation           string           `json:"mutation"`
	Rejection          FailureRejection `json:"rejection"`
	Consumers          []string         `json:"consumers"`
}

// WithdrawIntentVector là user-level intent (chưa được STATE-04 builder
// gán withdrawId/nonce). P4 BatchService nhận đúng shape này từ
// POST /api/withdraw-request body.
type WithdrawIntentVector struct {
	Owner       string `json:"owner"`
	Denom       string `json:"denom"`
	Amount      string `json:"amount"`
	Destination string `json:"destination"`
}

// OverWithdrawVector — STATE-04/05 reject vì Account.Balance < Intent.Amount.
//
// Payload:
//   - AccountSnapshot: trạng thái account tại thời điểm intent submit
//     (sau khi deposit-100 đã credit nhưng trước khi withdraw apply).
//   - Intent: WithdrawIntent với amount vượt balance.
//
// Cách dùng (P4 / P3 test):
//
//	ls := state.NewLocalState(); _, _ = ls.ApplyDeposit(...)
//	wb := state.NewWithdrawRequestBuilder(ls)
//	_, err := wb.Build(state.WithdrawIntent(vector.Intent))
//	errors.Is(err, state.ErrInsufficientBalance) // PHẢI true
type OverWithdrawVector struct {
	FailureMeta
	AccountSnapshot types.Account        `json:"accountSnapshot"`
	Intent          WithdrawIntentVector `json:"intent"`
}

// WrongRootVector — P1 chain verifier reject vì
// SettlementUpdate.OldStateRoot != currentStateRoot.
//
// Payload:
//   - CorrectOldStateRoot: rootB happy path (giá trị mà chain expect).
//   - TamperedSettlement: clone happy path SettlementUpdate nhưng
//     OldStateRoot bị thay bằng giá trị placeholder deterministic.
//   - BatchCommitments / PublicInputs được tái tính từ tampered settlement
//     để consumer có một payload complete cho MsgSubmitBatchProof.
//
// Cách dùng (P1 verifier test):
//
//	// Init chain at currentStateRoot = vector.CorrectOldStateRoot.
//	// Submit MsgSubmitBatchProof(vector.TamperedSettlement, proof) →
//	// reject với "oldStateRoot mismatch".
type WrongRootVector struct {
	FailureMeta
	CorrectOldStateRoot string                 `json:"correctOldStateRoot"`
	TamperedSettlement  types.SettlementUpdate `json:"tamperedSettlement"`
	BatchCommitments    types.BatchCommitments `json:"batchCommitments"`
	PublicInputs        []string               `json:"publicInputs"`
}

// DuplicateNullifierVector — chain / STATE-05 reject vì nullifier xuất
// hiện hai lần trong cùng batch (intra-batch replay).
//
// Payload:
//   - Nullifier: giá trị nullifier bị replay (= Alice happy path nullifier).
//   - SettlementUpdate: clone happy path nhưng withdrawals[] có 2 entry,
//     entry thứ hai (wd-2-replay) reuse nullifier của entry đầu.
//   - BatchCommitments / PublicInputs tái tính.
//
// Cách dùng (P1 chain test):
//   - Trong cùng batch: chain phải kiểm `len(nullifiers) == len(unique(nullifiers))`.
//   - Cross-batch: chain phải kiểm `!nullifierUsed[n]` trong store on-chain.
//
// Vector này tập trung kịch bản intra-batch (rejected ngay tại invariant
// check trước proof verify). Kịch bản cross-batch dùng cùng nullifier
// nhưng submit 2 lần — test driver tự lo.
type DuplicateNullifierVector struct {
	FailureMeta
	Nullifier        string                 `json:"nullifier"`
	SettlementUpdate types.SettlementUpdate `json:"settlementUpdate"`
	BatchCommitments types.BatchCommitments `json:"batchCommitments"`
	PublicInputs     []string               `json:"publicInputs"`
}

// TamperedDestinationVector — P1 verifier / P2 circuit reject vì
// destinationHash KHÔNG khớp H(destination).
//
// Payload:
//   - OriginalDestination / OriginalDestinationHash: cặp đúng từ happy path.
//   - TamperedDestination: destination giả mạo (vd. cosmos1attacker...).
//   - TamperedSettlement: clone happy path nhưng withdrawals[0].Destination
//     bị đổi sang tampered destination, DestinationHash giữ nguyên (vẫn là
//     hash của Original).
//   - BatchCommitments / PublicInputs tái tính từ tampered settlement —
//     withdrawalsRoot và withdrawOutputsRoot sẽ khác happy path vì
//     Destination thay đổi nhưng DestinationHash không. Verifier nào
//     recompute H(destination) sẽ thấy nó != DestinationHash.
type TamperedDestinationVector struct {
	FailureMeta
	OriginalDestination     string                 `json:"originalDestination"`
	TamperedDestination     string                 `json:"tamperedDestination"`
	OriginalDestinationHash string                 `json:"originalDestinationHash"`
	TamperedSettlement      types.SettlementUpdate `json:"tamperedSettlement"`
	BatchCommitments        types.BatchCommitments `json:"batchCommitments"`
	PublicInputs            []string               `json:"publicInputs"`
}

// FailureManifestEntry mô tả 1 file trong failure_vectors. Khác
// ManifestEntry happy path ở field `case` thay vì `stateTask` (case
// luôn là discriminator string đầu file).
type FailureManifestEntry struct {
	Name      string   `json:"name"`
	Case      string   `json:"case"`
	Schema    string   `json:"schema"`
	Consumers []string `json:"consumers"`
	SHA256    string   `json:"sha256"`
	Note      string   `json:"note,omitempty"`
}

// FailureManifest tóm tắt subfolder failure_vectors + SHA-256 từng file.
type FailureManifest struct {
	Scenario       string                 `json:"scenario"`
	Subfolder      string                 `json:"subfolder"`
	Description    string                 `json:"description"`
	VectorVersion  string                 `json:"vectorVersion"`
	HashAlgorithm  string                 `json:"hashAlgorithm"`
	Generator      string                 `json:"generator"`
	HappyPathRoots ManifestRoots          `json:"happyPathRoots"`
	Files          []FailureManifestEntry `json:"files"`
	Note           string                 `json:"note,omitempty"`
}

// FindByName trả về entry trùng tên file, hoặc nil.
func (m *FailureManifest) FindByName(name string) *FailureManifestEntry {
	for i := range m.Files {
		if m.Files[i].Name == name {
			return &m.Files[i]
		}
	}
	return nil
}

// FailureBundle là typed snapshot toàn bộ subfolder failure_vectors.
type FailureBundle struct {
	Manifest *FailureManifest

	OverWithdraw        OverWithdrawVector
	WrongRoot           WrongRootVector
	DuplicateNullifier  DuplicateNullifierVector
	TamperedDestination TamperedDestinationVector
}

// FailureDir trả về absolute path tới testvectors/<Scenario>/failure_vectors.
func FailureDir() (string, error) {
	scenario, err := ScenarioDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(scenario, FailureVectorsSubdir), nil
}

// FailurePathFor trả về absolute path cho file `name` trong subfolder.
func FailurePathFor(name string) (string, error) {
	dir, err := FailureDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// LoadFailureManifest đọc + parse failure_vectors/MANIFEST.json. KHÔNG
// tự verify SHA-256; caller test chủ động gọi VerifyFailureManifest.
func LoadFailureManifest() (*FailureManifest, error) {
	path, err := FailurePathFor(FailureManifestFile)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("testvectors: read failure manifest %s: %w", path, err)
	}
	var m FailureManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("testvectors: parse failure manifest: %w", err)
	}
	if m.Scenario != ScenarioName {
		return nil, fmt.Errorf("%w: failure manifest.scenario=%q, want %q", ErrFailureManifestMismatch, m.Scenario, ScenarioName)
	}
	if m.Subfolder != FailureVectorsSubdir {
		return nil, fmt.Errorf("%w: failure manifest.subfolder=%q, want %q", ErrFailureManifestMismatch, m.Subfolder, FailureVectorsSubdir)
	}
	return &m, nil
}

// VerifyFailureManifest re-hash từng file trong failure_vectors và đối
// chiếu manifest. Phát hiện 3 dạng drift y hệt VerifyManifest:
//
//  1. Entry manifest trỏ tới file không tồn tại.
//  2. SHA-256 thực tế khác manifest (hand-edit).
//  3. Orphan file trong folder (không có entry).
//
// Bypass: chính MANIFEST.json, file ẩn (đầu '.'), và README.md.
func VerifyFailureManifest(m *FailureManifest) error {
	dir, err := FailureDir()
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
			return fmt.Errorf("%w: %s: %v", ErrFailureManifestMismatch, name, err)
		}
		if got != want {
			return fmt.Errorf("%w: %s sha256 drift: got %s, want %s", ErrFailureManifestMismatch, name, got, want)
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
		if name == FailureManifestFile {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.EqualFold(name, "README.md") {
			continue
		}
		if _, ok := expected[name]; !ok {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		return fmt.Errorf("%w: orphan file(s) in failure folder: %s", ErrFailureManifestMismatch, strings.Join(orphans, ", "))
	}
	return nil
}

// LoadFailureBundle load tất cả 4 file canonical + manifest. Phù hợp cho
// test top-level dùng một entry point thay vì gọi từng LoadFailure* riêng.
func LoadFailureBundle() (*FailureBundle, error) {
	m, err := LoadFailureManifest()
	if err != nil {
		return nil, err
	}
	b := &FailureBundle{Manifest: m}
	if err := loadFailureJSON(FileOverWithdraw, &b.OverWithdraw); err != nil {
		return nil, err
	}
	if err := loadFailureJSON(FileWrongRoot, &b.WrongRoot); err != nil {
		return nil, err
	}
	if err := loadFailureJSON(FileDuplicateNullifier, &b.DuplicateNullifier); err != nil {
		return nil, err
	}
	if err := loadFailureJSON(FileTamperedDestination, &b.TamperedDestination); err != nil {
		return nil, err
	}
	if err := b.discriminatorCheck(); err != nil {
		return nil, err
	}
	return b, nil
}

// MustLoadFailureBundle panic nếu load fail. Dành cho test helper.
func MustLoadFailureBundle() *FailureBundle {
	b, err := LoadFailureBundle()
	if err != nil {
		panic(fmt.Sprintf("testvectors.MustLoadFailureBundle: %v", err))
	}
	return b
}

// SanityCheck assert các invariant chéo giữa 4 file failure + đối chiếu
// với happy-path scenario. Bắt generator bug semantic mà SHA-256 không
// thấy.
//
// Invariants:
//
//  1. Mọi file đeo đúng `case` discriminator (FailureCase*).
//  2. OverWithdraw.AccountSnapshot.Balance < OverWithdraw.Intent.Amount.
//  3. OverWithdraw.AccountSnapshot.Owner == Intent.Owner; Denom khớp.
//  4. WrongRoot.TamperedSettlement.OldStateRoot != WrongRoot.CorrectOldStateRoot.
//  5. DuplicateNullifier.SettlementUpdate.Withdrawals[] có ≥ 2 entry với
//     cùng Nullifier == DuplicateNullifier.Nullifier.
//  6. TamperedDestination.TamperedSettlement.Withdrawals[0].Destination ==
//     TamperedDestination.TamperedDestination, KHÁC OriginalDestination,
//     và DestinationHash giữ OriginalDestinationHash.
//  7. PublicInputs length = 6 cho mọi vector có PublicInputs.
//
// Caller (test) gọi sau LoadFailureBundle để fail-fast khi generator drift.
func (b *FailureBundle) SanityCheck() error {
	if b == nil {
		return errors.New("testvectors: nil failure bundle")
	}

	if b.OverWithdraw.Case != FailureCaseOverWithdraw {
		return fmt.Errorf("testvectors: OverWithdraw.Case=%q want %q", b.OverWithdraw.Case, FailureCaseOverWithdraw)
	}
	if b.WrongRoot.Case != FailureCaseWrongRoot {
		return fmt.Errorf("testvectors: WrongRoot.Case=%q want %q", b.WrongRoot.Case, FailureCaseWrongRoot)
	}
	if b.DuplicateNullifier.Case != FailureCaseDuplicateNullifier {
		return fmt.Errorf("testvectors: DuplicateNullifier.Case=%q want %q", b.DuplicateNullifier.Case, FailureCaseDuplicateNullifier)
	}
	if b.TamperedDestination.Case != FailureCaseTamperedDestination {
		return fmt.Errorf("testvectors: TamperedDestination.Case=%q want %q", b.TamperedDestination.Case, FailureCaseTamperedDestination)
	}

	// over_withdraw: balance < intent amount, owner/denom khớp.
	if b.OverWithdraw.AccountSnapshot.Owner != b.OverWithdraw.Intent.Owner {
		return fmt.Errorf("testvectors: over_withdraw owner mismatch snapshot=%s intent=%s",
			b.OverWithdraw.AccountSnapshot.Owner, b.OverWithdraw.Intent.Owner)
	}
	if b.OverWithdraw.AccountSnapshot.Denom != b.OverWithdraw.Intent.Denom {
		return fmt.Errorf("testvectors: over_withdraw denom mismatch snapshot=%s intent=%s",
			b.OverWithdraw.AccountSnapshot.Denom, b.OverWithdraw.Intent.Denom)
	}
	bal, ok := parseDecimal(b.OverWithdraw.AccountSnapshot.Balance)
	if !ok {
		return fmt.Errorf("testvectors: over_withdraw balance %q not numeric", b.OverWithdraw.AccountSnapshot.Balance)
	}
	amt, ok := parseDecimal(b.OverWithdraw.Intent.Amount)
	if !ok {
		return fmt.Errorf("testvectors: over_withdraw amount %q not numeric", b.OverWithdraw.Intent.Amount)
	}
	if !(bal < amt) {
		return fmt.Errorf("testvectors: over_withdraw balance=%d not < amount=%d (should violate ErrInsufficientBalance)", bal, amt)
	}

	// wrong_root: tampered != correct.
	if b.WrongRoot.TamperedSettlement.OldStateRoot == b.WrongRoot.CorrectOldStateRoot {
		return fmt.Errorf("testvectors: wrong_root: tampered.OldStateRoot == CorrectOldStateRoot=%s", b.WrongRoot.CorrectOldStateRoot)
	}

	// duplicate_nullifier: ≥ 2 withdrawals share Nullifier.
	if len(b.DuplicateNullifier.SettlementUpdate.Withdrawals) < 2 {
		return fmt.Errorf("testvectors: duplicate_nullifier withdrawals len=%d, want ≥ 2", len(b.DuplicateNullifier.SettlementUpdate.Withdrawals))
	}
	count := 0
	for _, w := range b.DuplicateNullifier.SettlementUpdate.Withdrawals {
		if w.Nullifier == b.DuplicateNullifier.Nullifier {
			count++
		}
	}
	if count < 2 {
		return fmt.Errorf("testvectors: duplicate_nullifier: only %d withdrawals carry nullifier=%s, want ≥ 2", count, b.DuplicateNullifier.Nullifier)
	}

	// tampered_destination: destination tampered ≠ original, hash giữ nguyên.
	if b.TamperedDestination.TamperedDestination == b.TamperedDestination.OriginalDestination {
		return fmt.Errorf("testvectors: tampered_destination: tampered == original")
	}
	if len(b.TamperedDestination.TamperedSettlement.Withdrawals) == 0 {
		return errors.New("testvectors: tampered_destination: settlement withdrawals empty")
	}
	sw := b.TamperedDestination.TamperedSettlement.Withdrawals[0]
	if sw.Destination != b.TamperedDestination.TamperedDestination {
		return fmt.Errorf("testvectors: tampered_destination: settlement.Destination=%s != TamperedDestination=%s",
			sw.Destination, b.TamperedDestination.TamperedDestination)
	}
	if sw.DestinationHash != b.TamperedDestination.OriginalDestinationHash {
		return fmt.Errorf("testvectors: tampered_destination: settlement.DestinationHash=%s != OriginalDestinationHash=%s (mutation invalidated)",
			sw.DestinationHash, b.TamperedDestination.OriginalDestinationHash)
	}

	// publicInputs length check.
	for label, slice := range map[string][]string{
		"wrong_root":           b.WrongRoot.PublicInputs,
		"duplicate_nullifier":  b.DuplicateNullifier.PublicInputs,
		"tampered_destination": b.TamperedDestination.PublicInputs,
	} {
		if len(slice) != 6 {
			return fmt.Errorf("testvectors: %s publicInputs len=%d, want 6", label, len(slice))
		}
	}
	return nil
}

// discriminatorCheck là chỉ tiêu nhanh ngay sau Load — đảm bảo dữ liệu
// trong từng file phù hợp file (file over_withdraw.json không bị nhầm
// nội dung sang wrong_root). Gọi trước SanityCheck để fail nhanh hơn.
func (b *FailureBundle) discriminatorCheck() error {
	pairs := []struct {
		file string
		got  string
		want string
	}{
		{FileOverWithdraw, b.OverWithdraw.Case, FailureCaseOverWithdraw},
		{FileWrongRoot, b.WrongRoot.Case, FailureCaseWrongRoot},
		{FileDuplicateNullifier, b.DuplicateNullifier.Case, FailureCaseDuplicateNullifier},
		{FileTamperedDestination, b.TamperedDestination.Case, FailureCaseTamperedDestination},
	}
	for _, p := range pairs {
		if p.got != p.want {
			return fmt.Errorf("testvectors: %s carries case=%q, want %q (file/content mismatch)", p.file, p.got, p.want)
		}
	}
	return nil
}

// loadFailureJSON open + unmarshal file trong failure_vectors subfolder.
func loadFailureJSON(name string, v any) error {
	path, err := FailurePathFor(name)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("testvectors: read failure %s: %w", name, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("testvectors: parse failure %s: %w", name, err)
	}
	return nil
}

// LoadOverWithdraw đọc + parse over_withdraw.json.
func LoadOverWithdraw() (OverWithdrawVector, error) {
	var v OverWithdrawVector
	return v, loadFailureJSON(FileOverWithdraw, &v)
}

// LoadWrongRoot đọc + parse wrong_root.json.
func LoadWrongRoot() (WrongRootVector, error) {
	var v WrongRootVector
	return v, loadFailureJSON(FileWrongRoot, &v)
}

// LoadDuplicateNullifier đọc + parse duplicate_nullifier.json.
func LoadDuplicateNullifier() (DuplicateNullifierVector, error) {
	var v DuplicateNullifierVector
	return v, loadFailureJSON(FileDuplicateNullifier, &v)
}

// LoadTamperedDestination đọc + parse tampered_destination.json.
func LoadTamperedDestination() (TamperedDestinationVector, error) {
	var v TamperedDestinationVector
	return v, loadFailureJSON(FileTamperedDestination, &v)
}

// parseDecimal là helper rất hẹp cho SanityCheck — parse decimal string
// non-negative. Dùng int64 vì balance/amount trong scenario Alice < 2^53;
// nếu STATE-12 scale lên multi-account, refactor sang big.Int.
func parseDecimal(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

