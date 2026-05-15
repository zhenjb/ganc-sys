# Tài liệu triển khai P3 — STATE-01, STATE-02, STATE-03

**Vai trò:** P3 — Off-chain state / batch builder
**Phạm vi tài liệu:** 3 task đầu tiên trong nhánh P3
**Ngày cập nhật:** 2026-05-15
**Phiên bản Go đã verify:** 1.26.3 (Windows/amd64, cài tại `D:\Setup\Go\Setting`)

---

## 0. Mục tiêu & ngữ cảnh

Theo bản plan đã chốt (`zkdex_final_parallel_plan_fixed.html` — tab "P3 State/Batch"), P3 đảm nhận **bridge giữa on-chain `DepositRecord` và ZK proof generation**. Bộ ba task đầu tiên đặt nền tảng cho mọi bước tiếp theo:

| Task | Mô tả ngắn | Output cần có |
|---|---|---|
| STATE-01 | Định nghĩa các struct dùng chung | Type definitions cho deposit, withdraw, update, witness, batch |
| STATE-02 | Khởi tạo local state cho test vector Alice | `rootA` + balance 0 |
| STATE-03 | Áp dụng deposit | Balance +100, `rootB` mới |

3 task này phục vụ trực tiếp:
- **P1 ONCHAIN-03 (genesis root)** — dùng `NewLocalState()` để seed `currentStateRoot`.
- **P2 ZK-09 (generate proof)** — dùng `oldBalance/newBalance` và `oldStateRoot/newStateRoot` từ vector canonical.
- **P4 INT-05 (deposit indexer)** — gọi `ApplyDeposit` mỗi khi thấy event `DepositQueued`.

---

## 1. Tầng schema — `pkg/types/` (STATE-01)

Đây là "ngôn ngữ chung" giữa 5 vai trò P1..P5. Không có logic, chỉ là **contract**.

### 1.1. `pkg/types/account.go` — `Account`

```go
type Account struct {
    Owner   string  `json:"owner"`   // "cosmos1alice"
    Denom   string  `json:"denom"`   // "uusdc"
    Balance string  `json:"balance"` // "100"
    Nonce   string  `json:"nonce"`   // "0"
}
```

Đại diện một dòng trong sổ cái off-chain. Theo agreement *"Amounts are strings in API/JSON"*, **Balance & Nonce đều là `string`** — để:
- Tránh tràn `int64` khi balance lớn.
- JSON tunnel sang frontend không bị mất chính xác như `number` của JavaScript.
- Số học bên trong dùng `math/big.Int`.

### 1.2. `pkg/types/batch.go` — `Batch` + `BatchStatus`

```go
type BatchStatus string

const (
    BatchStatusPending   BatchStatus = "pending"
    BatchStatusProved    BatchStatus = "proved"
    BatchStatusSubmitted BatchStatus = "submitted"
    BatchStatusAccepted  BatchStatus = "accepted"
    BatchStatusRejected  BatchStatus = "rejected"
)

type Batch struct {
    BatchID      string
    OldStateRoot string
    NewStateRoot string
    DepositIDs   []string
    WithdrawIDs  []string
    Status       BatchStatus
}
```

Khung sườn cho **STATE-08** (build settlement update) và **P4 relayer state machine**. 5 trạng thái biểu diễn vòng đời batch:
```
pending → proved → submitted → accepted
                            └→ rejected
```

### 1.3. `pkg/types/witness.go` — mở rộng `Witness`

```go
type Witness struct {
    UserSecret string   `json:"userSecret"`
    Nonce      string   `json:"nonce"`
    OldBalance string   `json:"oldBalance"`
    NewBalance string   `json:"newBalance"`
    StatePath  []string `json:"statePath,omitempty"`  // ← MỚI
}
```

Thêm `StatePath` (tuỳ chọn) khớp với ZK I/O contract trong plan: *"optional MVP simplification"*. STATE-03 chưa dùng nhưng P2 sẽ điền khi mạch cần Merkle path.

### 1.4. Các type sẵn có (không đổi)

- `DepositRecord` — đã có sẵn, sau đó P4 bổ sung `TxHash` (xem mục **§9**).
- `WithdrawRequest`, `WithdrawRecord`, `SettlementUpdate`, `ProofBundle` — đã khớp agreements, giữ nguyên.

---

## 2. Hash helper — `pkg/hash/hash.go`

Một **entry point duy nhất** cho mọi việc băm. Mục đích: khi P2 ZK-02 chốt hàm hash khác (Poseidon/Keccak/Mimc) thì chỉ phải sửa **một** file.

```go
const HexPrefix = "0x"

func SHA256Hex(data []byte) string        // SHA-256 → "0x" + hex (64 ký tự)
func SHA256HexString(s string) string     // wrapper nhận input string
func IsHexPrefixed(s string) bool         // check prefix "0x"
func StripHex(s string) string            // bỏ prefix "0x"
```

Format `"0x..."` theo agreement: *"Roots/nullifiers/proofs are hex strings"*.

---

## 3. AccountState — `internal/state/account_state.go`

Lưu trữ **thread-safe** các `Account`, đánh chỉ mục bằng `(owner, denom)`.

### 3.1. Khoá nội bộ

```go
type accountKey struct { Owner, Denom string }

func newAccountKey(owner, denom string) (accountKey, error) {
    // Trim space; reject nếu owner hoặc denom rỗng.
}
```

### 3.2. Cấu trúc

```go
type AccountState struct {
    mu       sync.RWMutex                    // RW vì hot path là đọc (queries)
    accounts map[accountKey]types.Account
}
```

### 3.3. API công khai

| Hàm | Vai trò | Caller chính |
|---|---|---|
| `NewAccountState()` | Khởi tạo map rỗng | `LocalState` constructor |
| `Get(owner, denom) (Account, bool)` | Tra balance, có flag tồn tại | API query nội bộ |
| `GetOrZero(owner, denom) Account` | Trả `Account{Balance:"0", Nonce:"0"}` nếu chưa có | `LocalState.Account()` cho UI/REST |
| `Credit(owner, denom, amount)` | Cộng số dư bằng `big.Int`, lock toàn hàm | `ApplyDeposit` |
| `Snapshot() []Account` | Mảng đã sort theo `(Owner, Denom)` | Đầu vào của `ComputeRoot` |

> **Tại sao `Snapshot` sort?** Đây là điều kiện **bắt buộc** để root deterministic. Hai LocalState áp dụng deposit theo thứ tự khác nhau vẫn ra cùng snapshot sau khi sort → cùng root.

### 3.4. Hai parser nội bộ

```go
func parsePositiveAmount(amount string) (*big.Int, error)
func parseNonNegativeAmount(amount string) (*big.Int, error)
```

| Parser | Cho phép | Dùng cho |
|---|---|---|
| `parsePositiveAmount` | `> 0` | Delta deposit/withdraw (không thể 0 hoặc âm) |
| `parseNonNegativeAmount` | `≥ 0` | Balance hiện tại (có thể là 0 cho account chưa giao dịch) |

Cả hai dùng `big.Int.SetString(s, 10)` → reject mọi input không phải số nguyên thập phân hợp lệ.

### 3.5. Liên kết nội bộ

`Credit(owner, denom, amount)` gọi:
1. `newAccountKey(owner, denom)` — validate key.
2. `parsePositiveAmount(amount)` — validate delta.
3. Đọc account hiện tại (hoặc tạo zero account).
4. `parseNonNegativeAmount(acc.Balance)` — đọc balance an toàn.
5. `bal.Add(bal, delta)` — cộng `big.Int`.
6. Ghi lại `acc.Balance = bal.String()`.

---

## 4. Tính root — `internal/state/root.go`

```go
const stateDomainTag = "zkdex/state/v1"

func ComputeRoot(accounts []types.Account) string {
    canonical, _ := json.Marshal(accounts)
    var buf strings.Builder
    buf.WriteString(stateDomainTag)
    buf.WriteByte('|')
    buf.Write(canonical)
    return hash.SHA256HexString(buf.String())
}
```

### 4.1. Ba quyết định thiết kế

1. **Snapshot-hash, không transition-hash**
   Root chỉ phụ thuộc trạng thái cuối, không phụ thuộc lịch sử giao dịch. Lý do:
   - Verifier P1 chỉ cần derive `newStateRoot` từ post-state, không cần biết chain transitions.
   - Deposit và withdraw đối xứng — STATE-05 sau này dùng cùng hàm `ComputeRoot`.

2. **Domain tag `"zkdex/state/v1"`**
   Phòng:
   - Version-collision khi đổi schema ở v2.
   - Cross-protocol replay nếu P2 reuse mạch cho project khác.

3. **Input `accounts` đã sort sẵn**
   Do `Snapshot()` sort theo `(Owner, Denom)`, hai LocalState áp dụng deposit theo thứ tự khác nhau vẫn ra cùng root.

### 4.2. Bằng chứng

`TestComputeRoot_OrderIndependent` chứng minh tính chất này:
```
LocalState A: apply d1 (alice, 100) → apply d2 (bob, 50)
LocalState B: apply d2 (bob, 50)    → apply d1 (alice, 100)
A.Root() == B.Root()  ✓
```

---

## 5. LocalState — `internal/state/local_state.go` (STATE-02 + STATE-03)

Đây là **mặt tiền P3** mà P4 backend sẽ gọi vào. Bọc `AccountState` + root + tập deposit đã apply.

### 5.1. Cấu trúc

```go
type LocalState struct {
    mu              sync.Mutex
    accounts        *AccountState
    root            string
    appliedDeposits map[string]struct{}   // idempotency theo depositId
}
```

`appliedDeposits` là **bản ghi local** của các deposit đã áp dụng. Khác với `depositProcessed[]` on-chain, nó tồn tại để indexer P4 replay event mà không gây double-credit.

### 5.2. `NewLocalState()` — STATE-02

```go
func NewLocalState() *LocalState {
    accounts := NewAccountState()
    return &LocalState{
        accounts:        accounts,
        root:            ComputeRoot(accounts.Snapshot()),   // ← rootA derive động
        appliedDeposits: make(map[string]struct{}),
    }
}
```

**Điểm cốt lõi:** `rootA` được **derive** từ empty snapshot, **không** hard-code. Nhờ vậy:
- P1 ONCHAIN-03 (genesis) gọi cùng đường code này → root on-chain & off-chain đồng bộ tự nhiên.
- Nếu sau này đổi `stateDomainTag` hoặc encoding, mọi lần seed sẽ tự cập nhật.

### 5.3. `ApplyDeposit(d DepositRecord) (string, error)` — STATE-03

```go
func (s *LocalState) ApplyDeposit(d types.DepositRecord) (string, error) {
    if err := validateDeposit(d); err != nil {              // 1. validate input
        return "", err
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    if _, ok := s.appliedDeposits[d.DepositID]; ok {        // 2. idempotency
        return "", fmt.Errorf("%w: depositId=%s",
                              ErrDepositAlreadyApplied, d.DepositID)
    }

    if _, err := s.accounts.Credit(d.Owner, d.Denom, d.Amount); err != nil {  // 3. credit
        return "", fmt.Errorf("credit %s/%s: %w", d.Owner, d.Denom, err)
    }

    s.appliedDeposits[d.DepositID] = struct{}{}              // 4. mark applied
    s.root = ComputeRoot(s.accounts.Snapshot())              // 5. advance root
    return s.root, nil
}
```

**4 lớp bảo vệ:**

| # | Tên | Bảo vệ điều gì |
|---|---|---|
| 1 | `validateDeposit` | Reject record sai shape (empty id/owner/denom, amount không hợp lệ) |
| 2 | Idempotency theo `depositId` | Indexer replay event `DepositQueued` không gây double-credit |
| 3 | `Credit` qua `big.Int` | Chống tràn số khi balance lớn |
| 4 | `ComputeRoot(Snapshot())` | Root mới deterministic, trả về cho relayer & P2 |

**Lưu ý quan trọng:** Hàm **không thay đổi `Nonce`** trên deposit — `Nonce` chỉ tăng khi withdraw được áp dụng (STATE-05). Đây là invariant được test `TestApplyDeposit_CreditsBalanceAndAdvancesRoot` xác nhận.

### 5.4. Các getter

```go
func (s *LocalState) Root() string                        // root pending hiện tại
func (s *LocalState) Account(owner, denom) types.Account  // proxy GetOrZero
func (s *LocalState) Snapshot() []types.Account           // mảng đã sort
func (s *LocalState) IsDepositApplied(id) bool            // query trạng thái
```

`IsDepositApplied` cho phép P4 backend kiểm tra trước khi gọi lại `ApplyDeposit` (tránh chạm error path nếu không cần).

### 5.5. `validateDeposit`

```go
func validateDeposit(d types.DepositRecord) error {
    if d.DepositID == "" {
        return fmt.Errorf("%w: depositId is empty", ErrInvalidDepositRecord)
    }
    if d.Owner == "" {
        return fmt.Errorf("%w: owner is empty", ErrInvalidDepositRecord)
    }
    if d.Denom == "" {
        return fmt.Errorf("%w: denom is empty", ErrInvalidDepositRecord)
    }
    if _, err := parsePositiveAmount(d.Amount); err != nil {
        return fmt.Errorf("%w: amount %q invalid: %v", ErrInvalidDepositRecord, d.Amount, err)
    }
    return nil
}
```

**Pattern `%w`:** wrap `ErrInvalidDepositRecord` cho phép caller làm `errors.Is(err, ErrInvalidDepositRecord)` để phân loại lỗi, đồng thời `%v` đính kèm thông điệp chi tiết.

**Trường `TxHash` cố tình KHÔNG validate** — là metadata trace từ on-chain tx, idempotency đã có `depositId` lo. Chi tiết tại **§9**.

### 5.6. Các sentinel error

```go
var (
    ErrDepositAlreadyApplied = errors.New("state: deposit already applied")
    ErrInvalidDepositRecord  = errors.New("state: invalid deposit record")
    ErrInvalidOwner          = errors.New("state: owner is empty")
    ErrInvalidDenom          = errors.New("state: denom is empty")
    ErrInvalidAmount         = errors.New("state: amount is not a valid non-negative integer string")
    ErrAmountNegative        = errors.New("state: amount must be > 0")
)
```

Tất cả đều exported → caller dùng `errors.Is` để phân nhánh thay vì so sánh chuỗi.

---

## 6. Test — `internal/state/local_state_test.go`

**7 test (gồm 6 sub-test invalid):**

| Test | Bảo vệ invariant |
|---|---|
| `TestNewLocalState_EmptyAccountsDeterministicRoot` | `rootA` ổn định giữa các lần khởi tạo, và là hex `0x...` |
| `TestApplyDeposit_CreditsBalanceAndAdvancesRoot` | Sau 1 deposit: balance=100, nonce không đổi, root tiến A → B |
| `TestApplyDeposit_Idempotent` | Apply lại cùng `depositId` → `ErrDepositAlreadyApplied`, state giữ nguyên |
| `TestApplyDeposit_AccumulatesAcrossDepositIDs` | `dep-1=100` + `dep-2=40` → balance 140 |
| `TestApplyDeposit_Invalid/empty_id` | Reject empty `depositId` |
| `TestApplyDeposit_Invalid/empty_owner` | Reject empty `owner` |
| `TestApplyDeposit_Invalid/empty_denom` | Reject empty `denom` |
| `TestApplyDeposit_Invalid/zero_amount` | Reject amount `"0"` |
| `TestApplyDeposit_Invalid/negative_amount` | Reject amount `"-5"` |
| `TestApplyDeposit_Invalid/non-numeric_amount` | Reject amount `"abc"` |
| `TestComputeRoot_OrderIndependent` | `{d1,d2}` ↔ `{d2,d1}` → cùng root |

**Kết quả chạy thực tế** (Go 1.26.3):

```
=== RUN   TestNewLocalState_EmptyAccountsDeterministicRoot
--- PASS: TestNewLocalState_EmptyAccountsDeterministicRoot
=== RUN   TestApplyDeposit_CreditsBalanceAndAdvancesRoot
--- PASS: TestApplyDeposit_CreditsBalanceAndAdvancesRoot
... (toàn bộ PASS) ...
PASS
ok  github.com/zhenjb/ganc-sys/internal/state  1.057s
```

---

## 7. Vector canonical — `testvectors/alice_100_40/`

Bộ vector dùng chung cho toàn team (STATE-11), được sinh bởi `p3/script-test/gen_state_vectors`:

| File | Nguồn | Consumer |
|---|---|---|
| `initial_state.json` | STATE-02 output | P1 genesis seed, P2 sanity check |
| `deposit_dep_1.json` | DepositRecord canonical | P1 `MsgDeposit` integration test, P4 mock indexer |
| `state_after_deposit.json` | STATE-03 output | P2 witness builder, P3 STATE-05 starting point |

**Generator `p3/script-test/gen_state_vectors/main.go`:**

```go
ls := state.NewLocalState()
// initial_state.json
write("initial_state.json", stateSnapshot{
    Root:     ls.Root(),                   // ← rootA
    Accounts: ls.Snapshot(),                // ← []
})

dep1 := types.DepositRecord{
    DepositID: "dep-1", Owner: "cosmos1alice", Denom: "uusdc",
    Amount: "100", CreatedHeight: 12345,
    TxHash: canonicalTxHash("deposit", "cosmos1alice", "uusdc", "100", "dep-1"),
}
write("deposit_dep_1.json", dep1)

newRoot, _ := ls.ApplyDeposit(dep1)
write("state_after_deposit.json", stateSnapshot{
    Root:     newRoot,                      // ← rootB
    Accounts: ls.Snapshot(),                // ← [{alice,uusdc,100,0}]
})
```

**Hàm `canonicalTxHash`** dùng **cùng công thức** với P4's `chain.MockClient.mockTxHash` (`"0x" + sha256(parts|).hex[:32]`) → vector tĩnh khớp output runtime của mock chain client, tránh phân kỳ giữa test data và mock.

**Convenience script `p3/script-test/run.ps1`** chạy gói gọn: `go test` rồi `go run gen_state_vectors`.

---

## 8. Bức tranh end-to-end

Luồng deposit từ user → on-chain → off-chain mirror, highlight phần P3 đã build:

```
┌─────────────────────────────────────────────────────────────────────┐
│ User                                                                │
│  │ MsgDeposit(uusdc, 100)                                           │
│  ▼                                                                  │
│ x/zkdex (P1, on-chain)                                              │
│  │ DepositRecord{dep-1, alice, uusdc, 100,                          │
│  │              processed:false, txHash:0x250e...}                  │
│  │ emit DepositQueued event                                         │
│  ▼                                                                  │
│ ─────────────── async boundary ───────────────                      │
│                                                                     │
│ Indexer (P4 INT-05)                                                 │
│  │ read DepositRecord                                               │
│  ▼                                                                  │
│ ┌─────────────────────────────────────────────┐                     │
│ │ LocalState.ApplyDeposit(dep)   ← P3 STATE-03 │                     │
│ │                                              │                     │
│ │  1. validateDeposit ✓                       │  ┌─────────────────┐│
│ │  2. check appliedDeposits[dep-1] absent ✓   │  │ STATE-02 init   ││
│ │  3. AccountState.Credit("alice","uusdc",     │  │   rootA derived ││
│ │                          "100")              │  │   accounts={}   ││
│ │       parsePositiveAmount("100") → 100      │  └─────────────────┘│
│ │       bal 0 + 100 = 100                     │                     │
│ │  4. appliedDeposits[dep-1] = struct{}{}     │                     │
│ │  5. root = ComputeRoot([{alice,uusdc,100,0}])│                    │
│ │         = sha256("zkdex/state/v1|[{...}]")  │                     │
│ │         = rootB                              │                     │
│ └─────────────────────────────────────────────┘                     │
│  │                                                                  │
│  ▼                                                                  │
│ LocalState.Root() → rootB                                           │
│  ├── feed STATE-08 → SettlementUpdate.newStateRoot                  │
│  ├── feed ZK-09 → witness {oldBalance:0, newBalance:100}            │
│  └── feed P4 INT-11 → GET /api/state response                       │
└─────────────────────────────────────────────────────────────────────┘
```

### Giá trị canonical đã đo

```
rootA = 0xe4029e127d0d318624204f91c87aed84377819b97f1c80cc53edf9b35840805d
rootB = 0x9b325b4150d417adfd816930b6f291aaf9493995fe0f960864c616ff178f8620
```

Hai giá trị này là **reference cố định** cho:
- **P1** dùng làm `oldStateRoot`/`newStateRoot` trong `MsgSubmitBatchProof` verify (ONCHAIN-08).
- **P2** dùng làm public inputs của ZK proof (ZK-09).
- **P4** dùng làm expected response của `GET /api/state` trước & sau dep-1.

**Quy tắc bất khả xâm phạm:** mọi thay đổi ở `pkg/hash` hoặc encoding root → bắt buộc re-run `gen_state_vectors` và commit lại 3 JSON.

---

## 9. Follow-up — Field `DepositRecord.TxHash` (cập nhật 2026-05-15)

P4 đã mở rộng `pkg/types/deposit.go` thêm `TxHash string` (hash của on-chain tx). Audit toàn bộ code P3:

| Component | Có cần đổi? | Lý do |
|---|---|---|
| `internal/state/account_state.go` | **Không** | Chỉ thao tác trên `Account`, không chạm `DepositRecord` |
| `internal/state/root.go` | **Không** | Root là hàm của post-state, không phải metadata deposit. `rootA`/`rootB` không đổi — đã re-verify |
| `internal/state/local_state.go::validateDeposit` | **Không (chủ ý)** | TxHash là metadata audit, không phải state-affecting. Idempotency đã có `depositId`. Coupling validate với TxHash sẽ tạo tripwire vô ích cho unit test & offline replay |
| `internal/state/local_state_test.go` | **Không** | Tests assert trên balance/root/error shape. TxHash zero-value chấp nhận được |
| `p3/script-test/gen_state_vectors/main.go` | **Có** | Vector phải phản ánh schema mới. TxHash dùng đúng recipe của P4's `chain.MockClient` |
| `testvectors/alice_100_40/deposit_dep_1.json` | **Đã regenerate** | Nay chứa `"txHash": "0x250e45522560609a604b00b437734ecd"` |

**Re-verification:** `go test ./internal/state/... ./pkg/...` → **PASS**. `rootA`/`rootB` byte-identical với lần trước → xác nhận TxHash thực sự ngoài phạm vi root, đúng phân tách metadata vs state-affecting field.

---

## 10. Các file đã tạo/sửa

```
pkg/types/account.go                              (mới)
pkg/types/batch.go                                (mới)
pkg/types/witness.go                              (mở rộng — thêm StatePath)
pkg/hash/hash.go                                  (mới)
internal/state/account_state.go                   (mới)
internal/state/root.go                            (mới)
internal/state/local_state.go                     (mới)
internal/state/local_state_test.go                (mới)
internal/state/nullifier.go                       (placeholder package — STATE-06 sẽ fill)
p3/script-test/gen_state_vectors/main.go          (mới)
p3/script-test/run.ps1                            (mới)
testvectors/alice_100_40/initial_state.json       (sinh bởi generator)
testvectors/alice_100_40/deposit_dep_1.json       (sinh bởi generator)
testvectors/alice_100_40/state_after_deposit.json (sinh bởi generator)
p3/changenotes/2026-05-14-state-01-02-03.md       (ghi chú phát triển)
p3/docs/STATE01_02_03_document.md                 (tài liệu này)
```

---

## 11. Hướng dẫn chạy & verify

### 11.1. Chạy unit test
```powershell
& 'D:\Setup\Go\Setting\bin\go.exe' test -v ./internal/state/... ./pkg/...
```

### 11.2. Sinh lại vector canonical
```powershell
& 'D:\Setup\Go\Setting\bin\go.exe' run ./p3/script-test/gen_state_vectors
```

### 11.3. Cả hai bước trong một lệnh
```powershell
pwsh -File p3/script-test/run.ps1
```
*(yêu cầu `D:\Setup\Go\Setting\bin` có trong `PATH`, hoặc sửa script gọi đường dẫn tuyệt đối)*

### 11.4. Lưu ý về `go test ./...`

Hiện tại lệnh `go test ./...` từ repo root sẽ **fail** do 3 file stub rỗng (0 byte) thuộc scope khác:
```
internal\batch\builder.go:1:1: expected 'package', found 'EOF'
internal\prover\client.go:1:1: expected 'package', found 'EOF'
internal\relayer\client.go:1:1: expected 'package', found 'EOF'
```

Đây là pre-existing condition, sẽ được fill khi **STATE-08** (batch builder) và **P4 INT-08/09** (prover/relayer client) hoàn thiện. Cho tới khi đó, dùng pattern có scope như §11.1.

---

## 12. Hướng đi cho task P3 kế tiếp

- **STATE-04 WithdrawRequest** — nonce nguồn lấy từ `Account.Nonce`. `Account.Nonce` chỉ được tăng ở STATE-05 (apply withdraw).
- **STATE-05 Apply withdrawal** — phải check `balance ≥ amount` **trước khi** debit, tăng `Account.Nonce`, tính lại root. Idempotency key là `nullifier`, không phải `withdrawId`.
- **STATE-06/07 hashes** — phối hợp với P2 ZK-02 để chốt hàm hash. Khi chốt, **chỉ** `pkg/hash` cần đổi.
- **STATE-08 Build SettlementUpdate** — assemble từ `LocalState.Root()` (pre/post), `DepositRecord.DepositID`, `WithdrawRequest`, `nullifier`, `withdrawAddressHash`.
