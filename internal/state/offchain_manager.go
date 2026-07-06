package state

import (
	"sort"
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// OffchainStateManager là long-lived mirror của pending off-chain state
// mô tả trong flow diagram (deposit step 6, withdraw steps 2-3).
//
// Manager là source of truth duy nhất cho pending balance giữa hai lần
// SubmitBatchProof được accept. Vai trò:
//
//   - INT-05 (deposit indexer) gọi ApplyDeposit khi index event từ chain.
//     Pending balance được credit ngay, root advance.
//   - INT-06 (withdraw request API) gọi ApplyWithdrawRequest tại
//     request-time: validate balance >= amount, debit pending balance,
//     mark nullifier consumed. Nếu balance không đủ → reject ngay tại
//     API layer, KHÔNG để dồn xuống batch build.
//   - P3 batch builder gọi Snapshot() để chụp state làm input cho
//     SettlementUpdate/Witness — thay vì xây fresh state per batch
//     (STATE-13 shortcut).
//   - INT-09 (batch submit) gọi Rollback(snap) khi proof submit fail —
//     restore state về thời điểm trước batch để các withdrawal đã
//     "tạm trừ" được hoàn lại pending balance.
//
// Tính chất:
//
//   - Thread-safe: tất cả mutator hold m.mu xuyên suốt; reader (Snapshot,
//     Root, Account, IsDepositApplied, IsNullifierApplied) cũng đi qua
//     mu để bảo đảm consistent view.
//   - Idempotent ApplyDeposit theo depositId — indexer replay an toàn.
//   - Idempotent ApplyWithdrawRequest theo nullifier — request retry an toàn.
//   - Rollback dùng deep-copy → snapshot vẫn immutable sau khi restore.
//
// Manager KHÔNG biết về secret/wallet; nullifier do caller cung cấp
// (đã được STATE-06 derive). Đây là contract giữ STATE-14 không lệ
// thuộc keystore implementation của P4/P5.
type OffchainStateManager struct {
	mu  sync.Mutex
	ls  *LocalState
	gen uint64
}

// NewOffchainStateManager khởi tạo manager với state rỗng. Root ban đầu
// được derive từ empty account set qua ComputeRoot — cùng giá trị với
// genesis root P1 (ONCHAIN-03) để off-chain mirror khớp on-chain từ
// block 0.
func NewOffchainStateManager() *OffchainStateManager {
	return &OffchainStateManager{
		ls: NewLocalState(),
	}
}

// NewOffchainStateManagerWithGenesisRoot pins the manager's genesis root so the
// first pending batch's oldStateRoot equals the on-chain genesis
// currentStateRoot (e.g. the chain's placeholder "0xrootA"). Without this the
// off-chain genesis (ComputeRoot of the empty set) never matches a chain that
// seeds a placeholder genesis, and the very first MsgSubmitBatchProof is
// rejected with "oldStateRoot mismatch". Empty genesisRoot => default behavior.
func NewOffchainStateManagerWithGenesisRoot(genesisRoot string) *OffchainStateManager {
	return &OffchainStateManager{
		ls: NewLocalStateWithGenesisRoot(genesisRoot),
	}
}

// BuildWithdrawRequest dựng một WithdrawRequest từ user intent dựa trên
// state sống của manager (STATE-04). Đây là entry point đúng kiến trúc cho
// INT-06: nonce được derive per-account (`account.Nonce + 1`) qua
// WithdrawRequestBuilder, KHÔNG dùng global counter — nên không bao giờ lệch
// khỏi nonce mà ApplyWithdrawRequest validate, kể cả sau các request fail.
//
// Builder cũng validate balance >= amount tại thời điểm build, nên thiếu
// balance bị reject sớm với state.ErrInsufficientBalance (caller P4 map qua
// HTTP).
//
// withdrawID là identity bền vững do caller (P4) cấp từ một nguồn durable
// (vd. Postgres sequence) — KHÔNG do manager sinh. Tách bạch như vậy để
// withdrawId luôn unique xuyên suốt restart khi request được persist vào
// store bền vững, trong khi nonce vẫn thuần là state per-account của P3.
//
// Read-only với balance/nonce: việc debit + increment nonce vẫn thuộc về
// ApplyWithdrawRequest (STATE-05). Caller chịu trách nhiệm gọi
// ApplyWithdrawRequest sau khi build để thực sự áp dụng.
func (m *OffchainStateManager) BuildWithdrawRequest(intent WithdrawIntent, withdrawID string) (types.WithdrawRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	builder := NewWithdrawRequestBuilder(m.ls)
	req, err := builder.Build(intent)
	if err != nil {
		return types.WithdrawRequest{}, err
	}
	// Override the builder's in-memory id with the caller-supplied durable id.
	req.WithdrawID = withdrawID
	return req, nil
}

// ApplyDeposit credit pending balance cho deposit record và advance
// pending root. Idempotent: replaying cùng depositId trả về
// ErrDepositAlreadyApplied (cho phép indexer poll lại cùng tx mà không
// double-credit).
//
// Caller (INT-05) phải đã extract DepositRecord từ chain event và đảm
// bảo owner/denom/amount khớp với on-chain DepositRecord.
//
// Returns: pending root mới sau khi credit; lỗi nếu deposit invalid
// hoặc đã apply trước đó.
func (m *OffchainStateManager) ApplyDeposit(d types.DepositRecord) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	root, err := m.ls.ApplyDeposit(d)
	if err != nil {
		return "", err
	}
	m.gen++
	return root, nil
}

// ApplyWithdrawRequest validate, debit pending balance và mark nullifier
// consumed cho một WithdrawRequest. Đây là entry point của
// "validate at request-time" — INT-06 gọi NGAY khi user submit withdraw,
// không đợi tới batch.
//
// Caller phải truyền nullifier đã derive bằng state.NullifierFor
// (STATE-06). Việc tách secret khỏi manager giữ surface area gọn và
// cho phép swap hash scheme (ZK-02) mà không đụng manager.
//
// Idempotent theo nullifier: replay cùng request → ErrWithdrawAlreadyApplied.
// Lỗi balance < amount → ErrInsufficientBalance (caller P4 map qua HTTP).
// Lỗi nonce mismatch → ErrNonceMismatch (giữ nguyên semantics STATE-05).
//
// Returns: pending root mới; lỗi nếu validation fail. Trên lỗi, state
// KHÔNG bị mutate (cùng semantic với LocalState.ApplyWithdrawal).
func (m *OffchainStateManager) ApplyWithdrawRequest(req types.WithdrawRequest, nullifier string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	root, err := m.ls.ApplyWithdrawal(req, nullifier)
	if err != nil {
		return "", err
	}
	m.gen++
	return root, nil
}

// Snapshot trả về một bản chụp immutable của state hiện tại.
//
// Snapshot là deep-copy: mutate manager sau đó KHÔNG ảnh hưởng snapshot
// đã trả ra. Đây là tính chất bắt buộc để:
//
//   - Batch builder (P3) có thể đọc snapshot tự do ngay cả khi indexer
//     vẫn đang ApplyDeposit ở goroutine khác.
//   - Rollback(snap) restore lại đúng trạng thái snapshot dù manager
//     đã chạy thêm operations.
//
// Snapshot gán generation hiện tại của manager — không phải khoá replay
// (Rollback bất kỳ snapshot nào cũng OK) mà là metadata cho debug/log.
func (m *OffchainStateManager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *OffchainStateManager) snapshotLocked() Snapshot {
	m.ls.mu.Lock()
	defer m.ls.mu.Unlock()

	m.ls.accounts.mu.RLock()
	defer m.ls.accounts.mu.RUnlock()

	accCopy := make(map[accountKey]types.Account, len(m.ls.accounts.accounts))
	for k, v := range m.ls.accounts.accounts {
		accCopy[k] = v
	}
	return Snapshot{
		root:              m.ls.root,
		accounts:          accCopy,
		appliedDeposits:   copyStrSet(m.ls.appliedDeposits),
		appliedNullifiers: copyStrSet(m.ls.appliedNullifiers),
		gen:               m.gen,
	}
}

// Rollback restore state về snapshot trước đó.
//
// Use case chính: INT-09 SubmitBatchProof fail (proof invalid hoặc
// chain reject) → các withdrawal đã ApplyWithdrawRequest cần được hoàn
// lại pending balance. Caller chụp snapshot TRƯỚC khi build batch,
// truyền vào đây khi submit fail.
//
// Rollback dùng deep-copy từ snapshot vào internal LocalState mới —
// snapshot vẫn giữ nguyên (có thể tái sử dụng).
//
// Sau Rollback, generation tăng → mọi snapshot in-flight cũ vẫn hợp
// lệ (vì là deep-copy độc lập), nhưng debug log thấy state đã được
// reset.
//
// Lưu ý: Rollback KHÔNG verify rằng snapshot là tổ tiên hợp lệ. Caller
// chịu trách nhiệm chỉ rollback về snapshot đã capture từ chính
// manager này — rollback từ snapshot của manager khác sẽ thay
// nguyên state nhưng không có cảnh báo.
func (m *OffchainStateManager) Rollback(snap Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ls = newLocalStateFromSnapshotData(snap)
	m.gen++
}

// Root trả về pending root hiện tại (đã advance qua mọi
// ApplyDeposit/ApplyWithdrawRequest đã thành công).
func (m *OffchainStateManager) Root() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ls.Root()
}

// Account trả về balance + nonce snapshot của (owner, denom) tại thời
// điểm gọi. Read-only; KHÔNG cấp quyền cho caller mutate.
//
// Trả về Account{Balance:"0", Nonce:"0"} nếu account chưa tồn tại — mô
// phỏng đúng GetOrZero của AccountState để FE/UI luôn nhận được dữ
// liệu deterministic.
func (m *OffchainStateManager) Account(owner, denom string) types.Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ls.Account(owner, denom)
}

// IsDepositApplied report whether deposit đã được apply vào manager.
// Phục vụ INT-05 idempotency check trước khi gọi ApplyDeposit (tránh
// log lỗi không cần thiết) và P5 UI hiển thị "đã indexed" badge.
func (m *OffchainStateManager) IsDepositApplied(depositID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ls.IsDepositApplied(depositID)
}

// IsNullifierApplied report whether nullifier đã được consume. Phục vụ
// INT-06 pre-check ("withdraw này đã được submit chưa") và P5 UI hiển
// thị "pending" / "consumed".
func (m *OffchainStateManager) IsNullifierApplied(nullifier string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ls.IsNullifierApplied(nullifier)
}

// Generation trả về số lượng mutation đã thực hiện thành công trên
// manager (ApplyDeposit/ApplyWithdrawRequest/Rollback). Phục vụ
// debug/log, KHÔNG dùng làm khoá optimistic concurrency.
func (m *OffchainStateManager) Generation() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gen
}

// Snapshot là bản chụp immutable của OffchainStateManager state. Chỉ
// được mint qua OffchainStateManager.Snapshot(). Các field private
// để bảo đảm caller không tự ý construct.
//
// Use cases:
//
//   - Read-only: P3 batch builder consume snapshot để biết
//     pre-batch balance/root.
//   - Rollback target: lưu lại trước khi submit batch, restore khi
//     proof fail.
//   - Deterministic UI: P5 chụp snapshot tại thời điểm hiển thị để
//     tránh race với pending Apply calls.
type Snapshot struct {
	root              string
	accounts          map[accountKey]types.Account
	appliedDeposits   map[string]struct{}
	appliedNullifiers map[string]struct{}
	gen               uint64
}

// Root trả về root tại thời điểm snapshot.
func (s Snapshot) Root() string {
	return s.root
}

// Generation trả về số gen của manager khi snapshot được tạo. Dùng
// cho log/debug — không phải khoá replay.
func (s Snapshot) Generation() uint64 {
	return s.gen
}

// Account trả về balance + nonce snapshot cho (owner, denom).
//
// Snapshot zero-value (chưa được Snapshot() trả ra) cũng dùng được —
// trả về Account default. Đây là điều kiện thuận tiện cho test/seed
// kịch bản empty pre-state.
func (s Snapshot) Account(owner, denom string) types.Account {
	key, err := newAccountKey(owner, denom)
	if err != nil {
		return types.Account{Owner: owner, Denom: denom, Balance: "0", Nonce: "0"}
	}
	if s.accounts == nil {
		return types.Account{Owner: owner, Denom: denom, Balance: "0", Nonce: "0"}
	}
	if acc, ok := s.accounts[key]; ok {
		return acc
	}
	return types.Account{Owner: owner, Denom: denom, Balance: "0", Nonce: "0"}
}

// Accounts trả về danh sách account đã sort theo (owner, denom). Bản
// copy mới mỗi lần gọi — caller mutate không ảnh hưởng snapshot.
func (s Snapshot) Accounts() []types.Account {
	out := make([]types.Account, 0, len(s.accounts))
	for _, acc := range s.accounts {
		out = append(out, acc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner == out[j].Owner {
			return out[i].Denom < out[j].Denom
		}
		return out[i].Owner < out[j].Owner
	})
	return out
}

// IsDepositApplied trả về true nếu depositID đã được apply trong
// snapshot này.
func (s Snapshot) IsDepositApplied(depositID string) bool {
	if s.appliedDeposits == nil {
		return false
	}
	_, ok := s.appliedDeposits[depositID]
	return ok
}

// IsNullifierApplied trả về true nếu nullifier đã được apply trong
// snapshot này.
func (s Snapshot) IsNullifierApplied(nullifier string) bool {
	if s.appliedNullifiers == nil {
		return false
	}
	_, ok := s.appliedNullifiers[nullifier]
	return ok
}

// NewLocalStateFromSnapshot construct LocalState seed từ một Snapshot.
//
// Phục vụ STATE-14 batch builder integration: thay vì
// state.NewLocalState() (empty), builder có thể start từ snapshot của
// manager. Mọi mutation trên LocalState mới KHÔNG ảnh hưởng snapshot
// (deep-copy).
//
// Đây là điểm tích hợp duy nhất giữa OffchainStateManager (mutable,
// long-lived) và LocalBuilder (per-batch, ephemeral). Manager giữ
// pending state; builder lấy snapshot để build settlement; sau khi
// submit succeed manager giữ nguyên (state đã đúng), submit fail thì
// caller Rollback(snap).
func NewLocalStateFromSnapshot(snap Snapshot) *LocalState {
	return newLocalStateFromSnapshotData(snap)
}

func newLocalStateFromSnapshotData(snap Snapshot) *LocalState {
	accounts := NewAccountState()
	if len(snap.accounts) > 0 {
		accounts.mu.Lock()
		for k, v := range snap.accounts {
			accounts.accounts[k] = v
		}
		accounts.mu.Unlock()
	}

	root := snap.root
	if root == "" {
		root = ComputeRoot(accounts.Snapshot())
	}

	return &LocalState{
		accounts:          accounts,
		root:              root,
		appliedDeposits:   copyStrSet(snap.appliedDeposits),
		appliedNullifiers: copyStrSet(snap.appliedNullifiers),
	}
}

func copyStrSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}
