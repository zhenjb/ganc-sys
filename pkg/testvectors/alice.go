package testvectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// StateSnapshot là echo của struct gen_state_vectors viết cho 3 file
// state JSON (initial / after_deposit / after_withdrawal). Đặt ở đây
// để consumer không phải redeclare.
type StateSnapshot struct {
	Root     string          `json:"root"`
	Accounts []types.Account `json:"accounts"`
	Note     string          `json:"note,omitempty"`
}

// NullifierVector echo schema nullifier_wd_1.json — gồm cả input
// (userSecret, nonce, domainTag) lẫn output (nullifier). P1 verifier /
// P2 circuit verify recompute với cùng input phải khớp.
type NullifierVector struct {
	WithdrawID    string `json:"withdrawId"`
	Owner         string `json:"owner"`
	Nonce         string `json:"nonce"`
	UserSecret    string `json:"userSecret"`
	DomainTag     string `json:"domainTag"`
	HashAlgorithm string `json:"hashAlgorithm"`
	Nullifier     string `json:"nullifier"`
	Note          string `json:"note,omitempty"`
}

// DestinationHashVector echo schema destination_hash_wd_1.json.
type DestinationHashVector struct {
	WithdrawID      string `json:"withdrawId"`
	Destination     string `json:"destination"`
	DomainTag       string `json:"domainTag"`
	HashAlgorithm   string `json:"hashAlgorithm"`
	DestinationHash string `json:"destinationHash"`
	Note            string `json:"note,omitempty"`
}

// BatchCommitmentsVector echo schema batch_commitments_batch_1.json —
// wrap types.BatchCommitments + domain tag metadata.
type BatchCommitmentsVector struct {
	BatchID         string                 `json:"batchId"`
	Commitments     types.BatchCommitments `json:"commitments"`
	DepositsTag     string                 `json:"depositsDomainTag"`
	WithdrawalsTag  string                 `json:"withdrawalsDomainTag"`
	NullifiersTag   string                 `json:"nullifiersDomainTag"`
	WithdrawOutsTag string                 `json:"withdrawOutputsDomainTag"`
	HashAlgorithm   string                 `json:"hashAlgorithm"`
	Note            string                 `json:"note,omitempty"`
}

// PublicInputsVector echo schema public_inputs_batch_1.json. Slice
// PublicInputs giữ đúng thứ tự batch.PublicInputIdx* — consumer không
// được re-order theo Labels.
type PublicInputsVector struct {
	BatchID      string   `json:"batchId"`
	Count        int      `json:"count"`
	Labels       []string `json:"labels"`
	PublicInputs []string `json:"publicInputs"`
	Note         string   `json:"note,omitempty"`
}

// AliceScenario là typed snapshot toàn bộ folder canonical. Mỗi field
// được populate từ một file vector. Consumer ưu tiên gọi
// LoadAliceScenario (one-shot) thay vì gọi từng Load* riêng — tránh
// phân tán logic load.
type AliceScenario struct {
	Manifest *Manifest

	InitialState         StateSnapshot
	Deposit              types.DepositRecord
	StateAfterDeposit    StateSnapshot
	WithdrawRequest      types.WithdrawRequest
	Nullifier            NullifierVector
	DestinationHash      DestinationHashVector
	StateAfterWithdrawal StateSnapshot
	SettlementUpdate     types.SettlementUpdate
	BatchCommitments     BatchCommitmentsVector
	Witness              types.Witness
	PublicInputs         PublicInputsVector
}

// LoadAliceScenario load tất cả file canonical và parse vào struct.
// Trả về (scenario, err); err non-nil nếu bất kỳ file nào missing,
// parse fail, hoặc manifest scenario name lệch.
//
// KHÔNG tự gọi VerifyManifest — caller test chủ động gọi để fail nhanh
// khi cần determinism check. Lý do: production caller (P4 backend,
// P5 demo) có thể chỉ cần load mà không cần verify mỗi request.
func LoadAliceScenario() (*AliceScenario, error) {
	m, err := LoadManifest()
	if err != nil {
		return nil, err
	}
	s := &AliceScenario{Manifest: m}
	if err := loadJSON(FileInitialState, &s.InitialState); err != nil {
		return nil, err
	}
	if err := loadJSON(FileDepositDep1, &s.Deposit); err != nil {
		return nil, err
	}
	if err := loadJSON(FileStateAfterDeposit, &s.StateAfterDeposit); err != nil {
		return nil, err
	}
	if err := loadJSON(FileWithdrawRequestWd1, &s.WithdrawRequest); err != nil {
		return nil, err
	}
	if err := loadJSON(FileNullifierWd1, &s.Nullifier); err != nil {
		return nil, err
	}
	if err := loadJSON(FileDestinationHashWd1, &s.DestinationHash); err != nil {
		return nil, err
	}
	if err := loadJSON(FileStateAfterWithdrawal, &s.StateAfterWithdrawal); err != nil {
		return nil, err
	}
	if err := loadJSON(FileSettlementUpdate, &s.SettlementUpdate); err != nil {
		return nil, err
	}
	if err := loadJSON(FileBatchCommitments, &s.BatchCommitments); err != nil {
		return nil, err
	}
	if err := loadJSON(FileWitness, &s.Witness); err != nil {
		return nil, err
	}
	if err := loadJSON(FilePublicInputs, &s.PublicInputs); err != nil {
		return nil, err
	}
	return s, nil
}

// MustLoadAliceScenario panic nếu load fail. Dành cho test helper.
func MustLoadAliceScenario() *AliceScenario {
	s, err := LoadAliceScenario()
	if err != nil {
		panic(fmt.Sprintf("testvectors.MustLoadAliceScenario: %v", err))
	}
	return s
}

// SanityCheck assert các invariant chéo giữa các file. Nếu fail nghĩa
// là generator drift mà MANIFEST.json không catch (vd. file đúng SHA
// nhưng generator có bug semantic). Đây là cross-validation tầng 2.
//
// Invariants:
//
//  1. SettlementUpdate.OldStateRoot == StateAfterDeposit.Root      (rootB)
//  2. SettlementUpdate.NewStateRoot == StateAfterWithdrawal.Root   (rootC)
//  3. InitialState.Root non-empty, khác rootB/rootC                (rootA)
//  4. Public inputs slice length == 6 và publicInputs[0]=rootB,
//     publicInputs[1]=rootC, publicInputs[2..5]=4 commitment roots.
//  5. Witness Account[0] balance journey: 0 → 60, nonce=1.
//  6. WithdrawRequest.Nonce == StateAfterWithdrawal.Account[0].Nonce.
//  7. SettlementUpdate.Withdrawals[0].Nullifier == Nullifier.Nullifier.
//  8. SettlementUpdate.Withdrawals[0].DestinationHash == DestinationHash.DestinationHash.
//  9. Deposit.Amount == "100", WithdrawRequest.Amount == "40".
func (s *AliceScenario) SanityCheck() error {
	if s == nil {
		return errors.New("testvectors: nil scenario")
	}
	rootB := s.StateAfterDeposit.Root
	rootC := s.StateAfterWithdrawal.Root

	if s.SettlementUpdate.OldStateRoot != rootB {
		return fmt.Errorf("testvectors: SettlementUpdate.OldStateRoot=%s != state_after_deposit.Root=%s",
			s.SettlementUpdate.OldStateRoot, rootB)
	}
	if s.SettlementUpdate.NewStateRoot != rootC {
		return fmt.Errorf("testvectors: SettlementUpdate.NewStateRoot=%s != state_after_withdrawal.Root=%s",
			s.SettlementUpdate.NewStateRoot, rootC)
	}
	if s.InitialState.Root == "" || s.InitialState.Root == rootB || s.InitialState.Root == rootC {
		return fmt.Errorf("testvectors: rootA=%q is empty or collides với rootB/rootC", s.InitialState.Root)
	}

	if len(s.PublicInputs.PublicInputs) != 6 {
		return fmt.Errorf("testvectors: publicInputs len=%d, want 6", len(s.PublicInputs.PublicInputs))
	}
	want := []string{
		rootB,
		rootC,
		s.BatchCommitments.Commitments.DepositsRoot,
		s.BatchCommitments.Commitments.WithdrawalsRoot,
		s.BatchCommitments.Commitments.NullifiersRoot,
		s.BatchCommitments.Commitments.WithdrawOutputsRoot,
	}
	for i, w := range want {
		if s.PublicInputs.PublicInputs[i] != w {
			return fmt.Errorf("testvectors: publicInputs[%d]=%s, want %s",
				i, s.PublicInputs.PublicInputs[i], w)
		}
	}

	if len(s.Witness.Accounts) != 1 {
		return fmt.Errorf("testvectors: witness accounts len=%d, want 1", len(s.Witness.Accounts))
	}
	wa := s.Witness.Accounts[0]
	if wa.OldBalance != "0" || wa.NewBalance != "60" || wa.Nonce != "1" {
		return fmt.Errorf("testvectors: witness journey oldBalance=%s newBalance=%s nonce=%s, want 0/60/1",
			wa.OldBalance, wa.NewBalance, wa.Nonce)
	}

	if s.WithdrawRequest.Nonce != "1" {
		return fmt.Errorf("testvectors: WithdrawRequest.Nonce=%s, want 1", s.WithdrawRequest.Nonce)
	}
	if len(s.StateAfterWithdrawal.Accounts) != 1 ||
		s.StateAfterWithdrawal.Accounts[0].Nonce != s.WithdrawRequest.Nonce {
		return fmt.Errorf("testvectors: post-withdrawal account nonce mismatch with request nonce")
	}

	if len(s.SettlementUpdate.Withdrawals) != 1 {
		return fmt.Errorf("testvectors: SettlementUpdate.Withdrawals len=%d, want 1",
			len(s.SettlementUpdate.Withdrawals))
	}
	sw := s.SettlementUpdate.Withdrawals[0]
	if sw.Nullifier != s.Nullifier.Nullifier {
		return fmt.Errorf("testvectors: SettlementUpdate.Withdrawals[0].Nullifier=%s != nullifier vector=%s",
			sw.Nullifier, s.Nullifier.Nullifier)
	}
	if sw.DestinationHash != s.DestinationHash.DestinationHash {
		return fmt.Errorf("testvectors: SettlementUpdate.Withdrawals[0].DestinationHash=%s != destinationHash vector=%s",
			sw.DestinationHash, s.DestinationHash.DestinationHash)
	}

	if s.Deposit.Amount != "100" {
		return fmt.Errorf("testvectors: Deposit.Amount=%s, want 100", s.Deposit.Amount)
	}
	if s.WithdrawRequest.Amount != "40" {
		return fmt.Errorf("testvectors: WithdrawRequest.Amount=%s, want 40", s.WithdrawRequest.Amount)
	}
	return nil
}

// loadJSON là tiện ích cho mọi Load* — open path canonical theo
// PathFor, unmarshal vào v. Trả về error có context filename để debug
// dễ.
func loadJSON(name string, v any) error {
	path, err := PathFor(name)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("testvectors: read %s: %w", name, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("testvectors: parse %s: %w", name, err)
	}
	return nil
}

// LoadInitialState đọc + parse initial_state.json.
func LoadInitialState() (StateSnapshot, error) {
	var s StateSnapshot
	return s, loadJSON(FileInitialState, &s)
}

// LoadDeposit đọc + parse deposit_dep_1.json.
func LoadDeposit() (types.DepositRecord, error) {
	var d types.DepositRecord
	return d, loadJSON(FileDepositDep1, &d)
}

// LoadStateAfterDeposit đọc + parse state_after_deposit.json.
func LoadStateAfterDeposit() (StateSnapshot, error) {
	var s StateSnapshot
	return s, loadJSON(FileStateAfterDeposit, &s)
}

// LoadWithdrawRequest đọc + parse withdraw_request_wd_1.json.
func LoadWithdrawRequest() (types.WithdrawRequest, error) {
	var r types.WithdrawRequest
	return r, loadJSON(FileWithdrawRequestWd1, &r)
}

// LoadNullifier đọc + parse nullifier_wd_1.json.
func LoadNullifier() (NullifierVector, error) {
	var n NullifierVector
	return n, loadJSON(FileNullifierWd1, &n)
}

// LoadDestinationHash đọc + parse destination_hash_wd_1.json.
func LoadDestinationHash() (DestinationHashVector, error) {
	var d DestinationHashVector
	return d, loadJSON(FileDestinationHashWd1, &d)
}

// LoadStateAfterWithdrawal đọc + parse state_after_withdrawal.json.
func LoadStateAfterWithdrawal() (StateSnapshot, error) {
	var s StateSnapshot
	return s, loadJSON(FileStateAfterWithdrawal, &s)
}

// LoadSettlementUpdate đọc + parse settlement_update_batch_1.json.
func LoadSettlementUpdate() (types.SettlementUpdate, error) {
	var u types.SettlementUpdate
	return u, loadJSON(FileSettlementUpdate, &u)
}

// LoadBatchCommitments đọc + parse batch_commitments_batch_1.json.
func LoadBatchCommitments() (BatchCommitmentsVector, error) {
	var b BatchCommitmentsVector
	return b, loadJSON(FileBatchCommitments, &b)
}

// LoadWitness đọc + parse witness_batch_1.json.
func LoadWitness() (types.Witness, error) {
	var w types.Witness
	return w, loadJSON(FileWitness, &w)
}

// LoadPublicInputs đọc + parse public_inputs_batch_1.json.
func LoadPublicInputs() (PublicInputsVector, error) {
	var p PublicInputsVector
	return p, loadJSON(FilePublicInputs, &p)
}
