# STATE-10 — Public input builder

## Mục tiêu

STATE-10 là bước **cuối cùng** của P3 trước khi proof được sinh: gom
`(SettlementUpdate, BatchCommitments)` thành slice public input đúng
thứ tự để P2 prover, P1 verifier on-chain và P4 relayer cùng đọc cùng
một schema.

Theo Agreements (mục `ZK I/O contract`):

```
publicInputs[0] = oldStateRoot
publicInputs[1] = newStateRoot
publicInputs[2] = depositsRoot
publicInputs[3] = withdrawalsRoot
publicInputs[4] = nullifiersRoot
publicInputs[5] = withdrawOutputsRoot
```

Và mục P3 task table:

> **STATE-10** — Convert update + BatchCommitments to ordered public
> input list → `public_inputs.json` → Output to: P2, P1

STATE-10 đóng vai trò **single source of truth** cho thứ tự public
input. Mọi consumer (P1/P2/P4) phải hoặc gọi
`batch.BuildPublicInputs` hoặc reference `batch.PublicInputIdx*`
constants — KHÔNG hard-code index số.

## Vị trí trong pipeline

```
┌─────────────────────────────────────────────────────────────────────────┐
│                       STATE-08 SettlementUpdate                         │
│      { oldStateRoot, newStateRoot, deposits[], withdrawals[] }          │
└────────────────────────────────────┬────────────────────────────────────┘
                                     │
                                     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│            STATE-08 (extension) BuildCommitments(upd)                   │
│   { depositsRoot, withdrawalsRoot, nullifiersRoot, withdrawOutputsRoot }│
└────────────────────────────────────┬────────────────────────────────────┘
                                     │
                                     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                STATE-10 BuildPublicInputs(upd, com)                     │
│                                                                         │
│   validateHex × 6 ─► no-op batch check ─► assemble slice 6 phần tử      │
│                                                                         │
│   [0] oldStateRoot                                                      │
│   [1] newStateRoot                                                      │
│   [2] depositsRoot                                                      │
│   [3] withdrawalsRoot                                                   │
│   [4] nullifiersRoot                                                    │
│   [5] withdrawOutputsRoot                                               │
└────────────────────────────────────┬────────────────────────────────────┘
                                     │
                          ┌──────────┴──────────┐
                          ▼                     ▼
                  ┌───────────────┐    ┌────────────────────┐
                  │ P2 prover     │    │ P1 verifier        │
                  │ ProofBundle.  │    │ MsgSubmitBatchProof│
                  │ PublicInputs  │    │ derive & assert    │
                  └───────────────┘    └────────────────────┘
```

## Public surface

### `batch.PublicInputBuilder`

```go
type PublicInputBuilder struct{}

func NewPublicInputBuilder() *PublicInputBuilder
func (b *PublicInputBuilder) Build(
    upd types.SettlementUpdate,
    com types.BatchCommitments,
) ([]string, error)
```

Stateless builder, tuân theo symmetry với
`SettlementUpdateBuilder` (STATE-08) và `WitnessBuilder` (STATE-09).
Không state mutable → an toàn cho concurrent calls.

### `batch.BuildPublicInputs`

```go
func BuildPublicInputs(
    upd types.SettlementUpdate,
    com types.BatchCommitments,
) ([]string, error)
```

Pure helper top-level dành cho caller stateless (script
`gen_state_vectors`, test). Tương đương
`NewPublicInputBuilder().Build(upd, com)`.

### Constants (LOCKED)

```go
const (
    PublicInputIdxOldStateRoot        = 0
    PublicInputIdxNewStateRoot        = 1
    PublicInputIdxDepositsRoot        = 2
    PublicInputIdxWithdrawalsRoot     = 3
    PublicInputIdxNullifiersRoot      = 4
    PublicInputIdxWithdrawOutputsRoot = 5
    PublicInputCount                  = 6
)
```

Mọi consumer dùng `PublicInputIdx*` constant thay vì hard-code số.
Khi ZK-02 thêm public input mới (vd `accountsRoot`), bump
`PublicInputCount` và thêm const mới — KHÔNG silent renumber.

### `batch.PublicInputLabels()`

```go
func PublicInputLabels() []string
```

Trả về copy danh sách 6 label (`"oldStateRoot"`, ..., `"withdrawOutputsRoot"`).
Defensive copy → caller mutate slice không ảnh hưởng global state. Dùng
cho debug log và metadata trong `public_inputs_batch_1.json`.

### Sentinel

| Sentinel | Trigger |
|---|---|
| `ErrInvalidPublicInputs` | Bất kỳ root nào empty / không có 0x prefix / `oldStateRoot == newStateRoot` |

Tách khỏi `ErrInvalidSettlementInputs` và `ErrInvalidWitnessInputs`
để P4 phân biệt rõ "binding public input sai" vs "settlement sai" vs
"witness sai".

## Giải thích từng hàm trong `public_inputs.go`

### `(b *PublicInputBuilder) Build(upd, com) ([]string, error)`

Entry point chính. Trình tự xử lý:

1. **`validateHex(upd.OldStateRoot, ...)`** — reuse helper hex check
   từ STATE-08 (`internal/batch/builder.go:226`). Reject nếu empty /
   không có `0x` prefix / chỉ có `0x` mà không có hex body. Lỗi
   wrap qua `wrapPublicInputErr` → `ErrInvalidPublicInputs`.
2. **`validateHex(upd.NewStateRoot, ...)`** — tương tự.
3. **No-op batch check** — `upd.OldStateRoot != upd.NewStateRoot`.
   Đây là defense-in-depth: STATE-08 đã reject batch rỗng, nhưng nếu
   caller bypass builder và đẩy thẳng `SettlementUpdate` tự chế vào
   `BuildPublicInputs`, layer này vẫn fail.
4. **`validateHex` cho 4 commitment root** — `depositsRoot`,
   `withdrawalsRoot`, `nullifiersRoot`, `withdrawOutputsRoot`.
5. **Assemble slice** — `make([]string, PublicInputCount)` rồi gán
   theo từng `PublicInputIdx*` const. Trả slice mới mỗi call → không
   shared state.

**KHÔNG hash, KHÔNG normalize, KHÔNG đổi case.** Verifier P1 đọc raw
hex để hash → verify. Mọi transform bổ sung phải đi qua ZK-02 và bump
domain tag / count.

### `BuildPublicInputs(upd, com)`

Thin wrapper: `(&PublicInputBuilder{}).Build(upd, com)`. Tồn tại để
caller stateless không phải `NewPublicInputBuilder()` mỗi lần.

### `PublicInputLabels() []string`

Copy slice `publicInputLabels[]` (var private). Defensive copy bảo
đảm caller mutate output không ảnh hưởng giá trị global. Test
`TestPublicInputs_Labels` invariant này.

### `wrapPublicInputErr(cause error) error`

Re-wrap lỗi từ `validateHex` (vốn đính `ErrInvalidSettlementInputs`)
sang `ErrInvalidPublicInputs`. Giữ message gốc làm `cause` để debug
truy nguồn — nhưng `errors.Is(err, ErrInvalidPublicInputs)` ở caller
sẽ match đúng sentinel STATE-10.

## Mapping với Flow doc

| Bước trong Flow (ZK settlement batch) | STATE primitive | Vị trí |
|---|---|---|
| Step 1 — Offchain builds SettlementUpdate | STATE-08 | `SettlementUpdateBuilder.Build` |
| Step 1 — buộc deposits/withdrawals vào commitments | STATE-08 ext | `BuildCommitments(upd)` |
| Step 2 — Prover creates ProofBundle{proof, publicInputs, vk} | STATE-10 | `batch.BuildPublicInputs(upd, com)` |
| Step 4 — Chain verifies proof against publicInputs | STATE-10 | Same builder, P1 derive lại |

## Test vectors

`testvectors/alice_100_40/public_inputs_batch_1.json` (canonical Alice
100/40):

```json
{
  "batchId": "batch-1",
  "count": 6,
  "labels": [
    "oldStateRoot",
    "newStateRoot",
    "depositsRoot",
    "withdrawalsRoot",
    "nullifiersRoot",
    "withdrawOutputsRoot"
  ],
  "publicInputs": [
    "0x9b325b4150d417adfd816930b6f291aaf9493995fe0f960864c616ff178f8620",
    "0x44d60f77db879799be6212c46d198243cbe2fe2969901ca3663dfb204276250c",
    "0x5ed1bb5ad7bc2681e3ff91a63f2183052caa6479ba0e9d02d87392d24e1bbc40",
    "0xea5ccc9d188ad7d28786383089d136cc53d4bd9d9a43f755726038e9514efdb1",
    "0x13b485d1c819a2ddff2411fbacae9d5e7aa0c6efe8c103189b44554bcab66f21",
    "0xce5e12744106271ca8f191db22631016126b96e19653e3884dc8923e1dde8e14"
  ]
}
```

Cross-check:
- `publicInputs[0]` = `settlement_update_batch_1.json.oldStateRoot` (rootB)
- `publicInputs[1]` = `settlement_update_batch_1.json.newStateRoot` (rootC)
- `publicInputs[2..5]` = 4 root trong `batch_commitments_batch_1.json.commitments`

Regenerate:

```
go run ./p3/script-test/gen_state_vectors
```

## Edge cases được cover

| Tình huống | Hành vi |
|---|---|
| `OldStateRoot = ""` | `ErrInvalidPublicInputs` |
| `NewStateRoot` không có `0x` prefix | `ErrInvalidPublicInputs` |
| `OldStateRoot == NewStateRoot` | `ErrInvalidPublicInputs` ("no-op batch") |
| `DepositsRoot = ""` | `ErrInvalidPublicInputs` |
| `WithdrawalsRoot = ""` | `ErrInvalidPublicInputs` |
| `NullifiersRoot = ""` | `ErrInvalidPublicInputs` |
| `WithdrawOutputsRoot = ""` | `ErrInvalidPublicInputs` |
| Concurrent calls trên cùng builder | Safe — builder stateless |
| Caller mutate output slice | Build call tiếp theo trả slice mới |
| Caller mutate slice từ `PublicInputLabels()` | Defensive copy → global state intact |

## Tích hợp với consumer khác

### P2 prover (circuit)

Khi ZK-02 wire circuit thật, P2 reference các const:

```go
// Inside circuit witness binding:
oldRoot := publicInputs[batch.PublicInputIdxOldStateRoot]
newRoot := publicInputs[batch.PublicInputIdxNewStateRoot]
// ... etc
```

### P1 verifier (chain module `x/zkdex`)

Khi `MsgSubmitBatchProof` được handle, verifier có thể derive lại
slice public input từ payload chain nhận và assert khớp với
`proofBundle.PublicInputs`:

```go
derived, err := batch.BuildPublicInputs(msg.SettlementUpdate, msg.BatchCommitments)
if err != nil { return err }
if !slices.Equal(derived, msg.ProofBundle.PublicInputs) {
    return ErrPublicInputMismatch
}
```

### P4 prover client (hiện tại)

`internal/prover/client.go:69-76` đang INLINE thứ tự public input. Có
thể refactor sang `batch.BuildPublicInputs` ở cleanup pass — đó là
task tuỳ chọn của P4, **không thuộc scope STATE-10**.

Test cross-role `TestPublicInputs_MatchProverClientOrder` assert thứ
tự khớp, nên refactor sau này không lo silent drift.

## Việc còn lại (out-of-scope STATE-10)

| Task | Owner | Trigger |
|---|---|---|
| Refactor `internal/prover/client.go` để gọi `batch.BuildPublicInputs` | P4 | Tuỳ chọn cleanup |
| ZK-02 chốt hash circuit (Poseidon/MiMC) — có thể đổi representation của public input | P2 | Sau khi circuit skeleton xong |
| Thêm `accountsRoot` hoặc public input mới khi circuit cần | P2 + P3 | ZK-02 wire |
