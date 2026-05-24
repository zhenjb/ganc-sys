# P3 — STATE-09: Build witness

## 1. Nhiệm vụ

STATE-09 đóng gói các **private inputs** mà circuit ZK của P2 sẽ tiêu thụ vào file `witness_batch_1.json`. Đây là input PRIVATE — không bao giờ đẩy lên chain, chỉ tồn tại trên prover host:

1. **P2 prover** (ZK-09) đọc cả `settlement_update_batch_1.json` (public) và `witness_batch_1.json` (private) → generate `proofBundle.json`.
2. Witness chứa các giá trị mà circuit phải prove "tôi biết" mà không tiết lộ:
   - `userSecret` — đầu vào của `Hash(secret, nonce) == nullifier`.
   - `nonce` — counter chống replay; cùng giá trị chain sẽ thấy ở `Account.Nonce` sau khi P1 verify proof.
   - `oldBalance` / `newBalance` — chứng minh balance transition `newBalance + withdrawAmount == oldBalance + depositAmount` (ZK-04).
   - `statePath` — optional merkle-style path, MVP để nil/empty.

Schema chốt trong Tab Agreements (đã có sẵn `pkg/types.Witness`):

```json
{
  "userSecret": "alice_secret",
  "nonce": "1",
  "oldBalance": "0",
  "newBalance": "60"
}
```

`statePath` có `omitempty` — không xuất hiện trong JSON khi nil/empty.

ZK I/O contract (locked trong Agreements):

```
Public inputs (từ SettlementUpdate, STATE-08):
- oldStateRoot
- newStateRoot
- depositAmount
- withdrawAmount
- withdrawAddressHash
- nullifier

Private witness (STATE-09):
- userSecret
- nonce
- oldBalance
- newBalance
- statePath (optional)
```

## 2. Luồng hoạt động

```
STATE-03 ApplyDeposit ────────────► (oldBalance snapshot tại đầu vào batch)
STATE-04 WithdrawRequest ─────────► nonce
STATE-05 ApplyWithdrawal ─────────► (newBalance snapshot sau debit)
STATE-06 NullifierFor ────────────► Settlement.Nullifier (public)
STATE-07 WithdrawAddressHash ─────► Settlement.WithdrawAddressHash (public)
STATE-08 SettlementUpdateBuilder ──► SettlementInputs (đã validate)

                          │
                          ▼
         ┌────────────────────────────────────────┐
         │  STATE-09 WitnessBuilder               │
         │  .Build(WitnessInputs{                 │
         │      UserSecret,                       │
         │      Settlement = SettlementInputs,    │
         │      OldBalance,                       │
         │      NewBalance,                       │
         │      StatePath (nil cho MVP),          │
         │  })                                    │
         └────────────────────────────────────────┘
                          │
                          ▼
              types.Witness  →  witness_batch_1.json
                          │
                          ▼
                  P2 prover (ZK-09)
                          │
                          ▼
               proofBundle.json (public)
```

## 3. API

### 3.1 `WitnessInputs`

```go
type WitnessInputs struct {
    UserSecret string             // private — không trim, chỉ TrimSpace bao quanh
    Settlement SettlementInputs   // reuse output STATE-08; nguồn của nonce, nullifier, amounts, denom
    OldBalance string             // balance TRƯỚC batch — "0" cho Alice
    NewBalance string             // balance SAU batch — "60" cho Alice
    StatePath  []string           // optional, nil cho MVP
}
```

- `Settlement` chứa `Withdraw.Nonce`, `Nullifier`, `Deposit.Amount`, `Withdraw.Amount`, `Deposit.Denom`, `Withdraw.Denom`. Witness builder đọc từng field từ đây.
- `UserSecret` ĐƯỢC pass thẳng từ wallet/P3 layer, KHÔNG đi qua `SettlementInputs` (security invariant: secret KHÔNG bao giờ chạm `batch.SettlementUpdateBuilder`).
- `OldBalance` / `NewBalance` là string (base-10 integer); builder canonical hoá qua `big.Int`.

### 3.2 `WitnessBuilder`

```go
type WitnessBuilder struct{}  // stateless

func NewWitnessBuilder() *WitnessBuilder
func (b *WitnessBuilder) Build(in WitnessInputs) (types.Witness, error)
```

- Stateless: không có counter, không có mutex, không hold reference đến `LocalState`.
- Concurrency-safe by construction.
- Pure: cùng input → cùng output.

### 3.3 Sentinel error

```go
var ErrInvalidWitnessInputs = errors.New("batch: invalid witness inputs")
```

Mọi failure bọc sentinel này. P4 map sang HTTP 400 với message gốc trong body (cùng pattern STATE-08).

## 4. Pipeline validation chi tiết

| # | Rule | Failure substring | Test cover |
|---|---|---|---|
| 1 | `UserSecret` non-empty sau trim | `userSecret is empty` | `TestWitnessBuild_InvalidScalars/userSecret_*` |
| 2 | `Settlement.Withdraw.Nonce` parse non-negative `big.Int` | `withdraw.nonce ... invalid` | `TestWitnessBuild_InvalidScalars/nonce_*` |
| 3 | `OldBalance` parse non-negative `big.Int` | `oldBalance ... invalid` | `TestWitnessBuild_InvalidScalars/oldBalance_*` |
| 4 | `NewBalance` parse non-negative `big.Int` | `newBalance ... invalid` | `TestWitnessBuild_InvalidScalars/newBalance_*` |
| 5 | `Settlement.Deposit.Amount` parse positive | `deposit.amount ... invalid` | `TestWitnessBuild_InvalidScalars/deposit_*` |
| 6 | `Settlement.Withdraw.Amount` parse positive | `withdraw.amount ... invalid` | `TestWitnessBuild_InvalidScalars/withdraw_*` |
| 7 | `Settlement.Deposit.Denom == Settlement.Withdraw.Denom` | `mixed-denom witness not supported` | `TestWitnessBuild_RejectsMixedDenom` |
| 8 | ZK-04: `newBalance + withdrawAmount == oldBalance + depositAmount` | `balance transition violated (ZK-04)` | `TestWitnessBuild_RejectsBalanceTransitionViolation` (3 sub) |
| 9 | ZK-05: `NullifierFor(secret, nonce) == Settlement.Nullifier` | `nullifier mismatch (ZK-05)` | `TestWitnessBuild_RejectsNullifierMismatch` |

Toàn bộ failure → sentinel `ErrInvalidWitnessInputs` (wrapper với cause). Không có output partial: hàm trả `types.Witness{}` zero-value khi error != nil.

## 5. Mỗi hàm cụ thể

### `NewWitnessBuilder() *WitnessBuilder`

Trả pointer đến struct rỗng. Không có khởi tạo state (stateless). Tách hàm constructor để API đối xứng với `NewSettlementUpdateBuilder()` và để tương lai có thể inject config (vd. domain tag version) mà không vỡ API.

### `Build(in WitnessInputs) (types.Witness, error)`

Pipeline thực thi tuần tự, fail fast:

1. **Secret check**: `secret := strings.TrimSpace(in.UserSecret)`; nếu empty → error `userSecret is empty`.
2. **Scalar parsing** (gọi helper `parseNonNegative` / `parsePositive` đã share với `builder.go` cùng package):
   - `nonce, _ := parseNonNegative(in.Settlement.Withdraw.Nonce)`
   - `oldBal, _ := parseNonNegative(in.OldBalance)`
   - `newBal, _ := parseNonNegative(in.NewBalance)`
   - `depAmt, _ := parsePositive(in.Settlement.Deposit.Amount)`
   - `wdAmt, _ := parsePositive(in.Settlement.Withdraw.Amount)`
3. **Denom check**: `Deposit.Denom == Withdraw.Denom`.
4. **ZK-04 balance constraint**:
   ```go
   lhs := new(big.Int).Add(newBal, wdAmt)
   rhs := new(big.Int).Add(oldBal, depAmt)
   if lhs.Cmp(rhs) != 0 { reject }
   ```
   Error message log đủ 4 con số + 2 tổng → dễ debug.
5. **ZK-05 nullifier re-derive**:
   ```go
   rederived, _ := state.NullifierFor(secret, nonce.String())
   if rederived != in.Settlement.Nullifier { reject }
   ```
   Gọi `nonce.String()` không phải `in.Settlement.Withdraw.Nonce` → đảm bảo dùng giá trị canonical (mọi format `"01"` / `"1"` cùng nullifier).
6. **StatePath defensive copy**:
   ```go
   var statePath []string
   if len(in.StatePath) > 0 {
       statePath = append([]string(nil), in.StatePath...)
   }
   ```
7. **Assemble**:
   ```go
   return types.Witness{
       UserSecret: secret,
       Nonce:      nonce.String(),
       OldBalance: oldBal.String(),
       NewBalance: newBal.String(),
       StatePath:  statePath,
   }, nil
   ```

### `parseNonNegative(amount string) (*big.Int, error)` / `parsePositive`

Shared helper từ `builder.go` (cùng package `batch`). `parseNonNegative`: trim → empty check → `big.Int.SetString(., 10)` → sign check `>= 0`. `parsePositive` thêm rule `Sign() != 0`.

## 6. ZK-04 balance constraint — vì sao là hard gate?

**Threat:** Witness drift khỏi LocalState's actual transition. Vd:

- Caller pass `oldBalance` lấy từ snapshot của account khác.
- Caller forget cộng deposit vào oldBalance (treat oldBalance như post-deposit thay vì pre-batch).
- Refactor `AccountState.Debit` break invariant nonce++ → balance đúng nhưng witness lệch.

**Hậu quả nếu không catch:**

- Prover build witness ok (witness hợp lệ với chính nó).
- Circuit reject (constraint `newBalance + withdrawAmount == oldBalance + depositAmount` không thoả) → prover lỗi.
- Hoặc tệ hơn: nếu circuit không enforce constraint này (bug ZK-04), proof generate được, nhưng public input chứa `withdrawAmount` không reflect balance thật → P1 reject vì root mismatch.

→ Mất 1 round-trip prover (giây — đắt) hoặc tệ hơn là proof valid với constraint giả → vấn đề security.

**Catch ở STATE-09:** Re-compute LHS/RHS ngay, fail trong microsecond, error log đầy đủ 4 con số.

**Khi nào KHÔNG vi phạm:** Nếu cả pipeline STATE-03..05 đi đúng và caller capture balance đúng thời điểm, LHS == RHS. Validation thừa cho happy path nhưng cần cho misuse path.

## 7. ZK-05 nullifier re-derive — vì sao là defense-in-depth?

**Threat:** Caller có secret đúng nhưng nullifier sai trong `SettlementInputs`. Vd:

- Cache stale: pipeline cũ dùng nonce=0, mới dùng nonce=1, nullifier vẫn map sang nonce cũ.
- Bug: `NullifierFor` được fork qua component khác (P2 circuit có hash function riêng) — domain tag drift.

**Hậu quả nếu không catch:**

- Witness và update có nullifier khác nhau → circuit hash secret+nonce thành nullifier A, update nói nullifier B → public input mismatch → proof reject ở verify time.

**Catch ở STATE-09:** STATE-09 là điểm DUY NHẤT có cả secret và nullifier. STATE-08 không có secret (giữ ra khỏi package). STATE-06 không có update (chỉ hash). → STATE-09 chốt invariant `update.Nullifier == NullifierFor(secret, nonce)`.

**Cost:** 1 SHA-256 (~microsecond). Domain tag được gọi qua `state.NullifierFor` (single source of truth) → không thể drift trong cùng module.

**Test:** `TestWitnessBuild_NullifierBindsWitnessToUpdate` — re-derive bằng API public `state.NullifierFor` và assert == settlement. Bắt drift domain tag với P2/P1 nếu họ implement riêng (sẽ cần CI integration test cross-language sau).

## 8. Đồng nhất với các STATE trước

### STATE-04: nonce semantics

`Settlement.Withdraw.Nonce` là giá trị `nextNonce = Account.Nonce + 1` mà STATE-04 đã gán cho request. STATE-09 dùng nguyên nonce này (canonical hoá) để derive nullifier — giống y hệt STATE-06 đã làm. Hệ quả: witness.Nonce == settlement.Withdraw.Nonce == account.Nonce sau ApplyWithdrawal.

### STATE-05: balance order

Generator capture `oldBalance := ls.Account(...).Balance` **trước** `ApplyDeposit` và `newBalance := ls.Account(...).Balance` **sau** `ApplyWithdrawal`. Witness builder không sờ vào LocalState — caller chịu trách nhiệm capture đúng thời điểm.

```go
ls := state.NewLocalState()
oldBalance := ls.Account(aliceAddr, denom).Balance  // "0"
ls.ApplyDeposit(dep1)
oldRoot := ls.Root()                                  // rootB
// ... build req, nullifier, addrHash ...
rootC, _ := ls.ApplyWithdrawal(req, nullifier)
newBalance := ls.Account(aliceAddr, denom).Balance  // "60"
```

### STATE-06: nullifier value

STATE-09 re-derive `NullifierFor(secret, nonce)` và assert bằng `Settlement.Nullifier` — chính là giá trị STATE-06 đã produce và STATE-05 đã consume. Single source of truth qua `state.NullifierFor`.

### STATE-08: SettlementInputs reuse

`WitnessInputs.Settlement` LÀ `SettlementInputs` — không copy field bằng tay. Reuse đảm bảo:

- Caller chỉ build `SettlementInputs` 1 lần, pass vào cả `SettlementUpdateBuilder.Build` và `WitnessBuilder.Build`.
- Không có chỗ nào caller "edit" field giữa hai call → witness và update guaranteed consistent.

## 9. Kết quả cuối (canonical Alice 100/40 vector)

`testvectors/alice_100_40/witness_batch_1.json`:

```json
{
  "userSecret": "alice_secret",
  "nonce": "1",
  "oldBalance": "0",
  "newBalance": "60"
}
```

Verify ZK-04: `60 + 40 == 0 + 100` → `100 == 100` ✓

Verify ZK-05: `NullifierFor("alice_secret", "1") == 0x1a1fdf4ccecb7040b7cd7e125226d14ed7717618d996dab969d4cb12550b22f7` (giá trị trong `settlement_update_batch_1.json` field `nullifier`) ✓

Consumer:

- **P2 (ZK-09)**: đọc cùng `settlement_update_batch_1.json` → derive proof, output `proofBundle.json`.
- **P3 (STATE-12)**: dùng làm baseline để tạo negative vector (vd. `wrong_secret`, `wrong_balance`).

KHÔNG bao giờ đẩy file này lên chain hoặc public artifact. Trong demo MVP, file checked-in chỉ vì secret là `"alice_secret"` plaintext.

## 10. Tests

| Test | Mục đích |
|---|---|
| `TestWitnessBuild_CanonicalAliceVector` | Happy path: tất cả field map đúng |
| `TestWitnessBuild_NullifierBindsWitnessToUpdate` | Re-derive khớp settlement — bắt drift domain tag |
| `TestWitnessBuild_RejectsBalanceTransitionViolation` (3 sub) | ZK-04 enforcement: off-by-one ± |
| `TestWitnessBuild_RejectsNullifierMismatch` | ZK-05 enforcement: secret sai |
| `TestWitnessBuild_RejectsMixedDenom` | Single-denom invariant |
| `TestWitnessBuild_InvalidScalars` (9 sub) | empty/junk/negative cho mọi scalar field |
| `TestWitnessBuild_NormalizesScalars` | Canonical hoá scalar qua `big.Int` |
| `TestWitnessBuild_StatePathDefensiveCopy` | Caller mutate path sau Build → witness unchanged |
| `TestWitnessBuild_OmitsStatePathWhenEmpty` | nil StatePath → omitempty |
| `TestWitnessBuild_PreservesUserSecretAfterTrim` | Whitespace trim, secret content giữ nguyên |
| `TestWitnessBuild_Concurrent` | Stateless: 16 goroutines build cùng input → output identical |

Tổng: 10 test functions, 21 sub-tests.

```bash
go test ./internal/batch/... -v -count=1
# PASS — 22/22 (12 STATE-08 + 10 STATE-09), 40 sub-tests, ~1s

go test ./...
# PASS — all packages

go build ./...
# OK

go run ./p3/script-test/gen_state_vectors
# emits witness_batch_1.json
# witness: oldBalance:0 newBalance:60 nonce:1
```

## 11. Tích hợp với STATE kế tiếp

- **STATE-10 Public input builder**: đọc `SettlementUpdate` (STATE-08 output) → output ordered slice `[oldStateRoot, newStateRoot, depositAmount, withdrawAmount, withdrawAddressHash, nullifier]`. STATE-09 đã ràng buộc `Settlement.Nullifier` consistent với witness; STATE-10 chỉ project, không re-derive.
- **STATE-11 Test vector folder**: 9/10 file đã có (`witness_batch_1.json` mới). STATE-11 sẽ thêm `public_inputs.json`.
- **STATE-12 Failure vectors** (gợi ý):
  - `witness_wrong_secret.json`: thay `userSecret` → `state.NullifierFor` mismatch — P2 prover constraint fail.
  - `witness_wrong_balance.json`: thay `newBalance="59"` → ZK-04 vi phạm.
  - `witness_mixed_denom_via_update.json`: tham nhũng `Settlement.Withdraw.Denom` — STATE-09 reject (cũng giống STATE-08 reject).
  Cả 3 đã có unit test trong `witness_test.go`; STATE-12 chỉ cần emit JSON tương ứng để P1/P2/P4 dùng làm negative fixture.

## 12. Bảo mật — checklist

- [x] `UserSecret` KHÔNG xuất hiện trong `SettlementInputs` hoặc `SettlementUpdate`.
- [x] `UserSecret` KHÔNG xuất hiện trong log của `SettlementUpdateBuilder.Build`.
- [x] `WitnessBuilder` không cache secret giữa các call (stateless).
- [x] Test vector commit-in (`alice_secret`) là demo-only; document rõ ràng trong changenote.
- [x] Domain tag của nullifier qua `state.NullifierFor` (single canonical entry point).
- [x] Defense-in-depth: re-derive nullifier ngay tại STATE-09 (điểm duy nhất có cả secret và nullifier).
- [ ] Production cần encrypt at-rest cho witness file (out of MVP scope; nhắc P4 khi triển khai prover service).
- [ ] Production cần audit log path từ wallet → witness builder để chắc secret không leak qua error/panic stack (out of MVP scope).
