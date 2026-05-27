# STATE-13 — Batch builder facade

## Mục tiêu

STATE-13 là **ranh giới Go interface** giữa P3 primitives (STATE-03..09) và P4 wiring (`internal/service/batch_service.go`). Một call duy nhất `Builder.Build(ctx, BuildInput)` phải nhận từ P4 danh sách deposits + withdraw requests đã resolve, rồi trả ra `SettlementUpdate + BatchCommitments + Witness` ở dạng canonical, không partial.

Theo Agreements (mục P3 task table):

> **STATE-13** — Expose `batch.Builder` interface + `LocalBuilder` orchestrating STATE-03..09 + commitments in one call. Map `state.ErrInsufficientBalance` → `batch.ErrInsufficientOffchainBalance`. Owns Go interface boundary between P3 primitives and P4 wiring.
>
> **Output**: `batch.Builder`, `BuildInput`, `BuildOutput`, `ErrInsufficientOffchainBalance`, `NewLocalBuilder()`

## Vị trí trong pipeline

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                          P4 BatchService.BuildBatch                          │
│  depositIds[], withdrawIds[]  ──►  repo lookup  ──►  Builder.Build(ctx, in)  │
└────────────────────────────────────────┬─────────────────────────────────────┘
                                         │ BuildInput
                                         ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                       batch.LocalBuilder.Build (STATE-13)                    │
│                                                                              │
│   ctx.Err() ─► validate OldStateRoot ─► indexSecrets ─► collectParticipants  │
│                                                                              │
│   ┌────────┐  STATE-03      ┌──────────┐  STATE-06    ┌──────────┐           │
│   │ fresh  │ ApplyDeposit   │ STATE-06 │ Nullifier    │ STATE-07 │           │
│   │ Local  │ ───────────►   │ STATE-07 │ DestHash     │          │           │
│   │ State  │                └──────────┘              └──────────┘           │
│   └────────┘                      │                         │                │
│                                   ▼                         ▼                │
│                            STATE-05 ApplyWithdrawal (idempotent on nullifier)│
│                                   │                                          │
│                                   ▼                                          │
│   STATE-08 SettlementUpdateBuilder.Build  ──► validate roots/single-denom    │
│                                   │                                          │
│                                   ▼                                          │
│   STATE-09 WitnessBuilder.Build           ──► validate ZK-04/ZK-05           │
│                                   │                                          │
│                                   ▼                                          │
│   BuildCommitments(upd)                    ──► 4 root buộc public inputs     │
└────────────────────────────────────────┬─────────────────────────────────────┘
                                         │ BuildOutput
                                         ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│  P4 BatchService → BatchRepository.SaveBatchBuild → HTTP 200 to frontend     │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Public surface

### `batch.Builder` interface

```go
type Builder interface {
    Build(ctx context.Context, in BuildInput) (BuildOutput, error)
}
```

P4 phụ thuộc vào interface này, KHÔNG phụ thuộc `*LocalBuilder` cụ thể — chuẩn bị cho việc swap sang remote/RPC builder trong tương lai.

### `batch.BuildInput`

```go
type BuildInput struct {
    OldStateRoot     string                  // echo-only, P1 verifier kiểm thật
    Deposits         []types.DepositRecord   // P4 resolve từ depositIds[]
    WithdrawRequests []types.WithdrawRequest // P4 resolve từ withdrawIds[]
    AccountSecrets   []AccountSecret         // OPTIONAL (fallback mock)
}

type AccountSecret struct {
    Owner      string
    UserSecret string
}
```

**Quan trọng**:
- `OldStateRoot` là metadata echo — facade KHÔNG so sánh với fresh `LocalState.Root()`. P4 hôm nay hardcode `"0xrootA"`; P1 verifier mới là nơi reconcile cuối cùng với on-chain `currentStateRoot`.
- `AccountSecrets` không bắt buộc. Owner không có entry sẽ fallback về literal `"mock-user-secret"` (Agreements canonical Witness JSON). Khi P5/keystore sẵn sàng, P4 sẽ inject secret thật.

### `batch.BuildOutput`

```go
type BuildOutput struct {
    SettlementUpdate types.SettlementUpdate
    BatchCommitments types.BatchCommitments
    Witness          types.Witness
}
```

Field `BatchCommitments` đặt tên khớp với `types.BuildBatchResponse.BatchCommitments` để P4 relay 1:1 xuống FE.

### `batch.NewLocalBuilder()`

```go
func NewLocalBuilder() *LocalBuilder
```

Tạo instance LocalBuilder. **Một process nên giữ một instance xuyên suốt** để `BatchID` seq tiếp tục đếm (batch-1, batch-2, ...). Tạo nhiều instance sẽ reset counter và clash batch id.

### Sentinels

| Sentinel | Trigger | P4 mapping |
|---|---|---|
| `ErrInsufficientOffchainBalance` | Withdraw amount > balance off-chain | HTTP 400 `"insufficient off-chain balance"` (đã wire ở `batch_handler.go:48`) |
| `ErrInvalidBuildInput` | Owner trống, duplicate secret, lỗi structural khác | HTTP 500 (default branch) |
| `ErrInvalidSettlementInputs` (re-exported từ STATE-08) | Root rỗng, batch rỗng, denom mismatch, destHash mismatch | HTTP 500 (default branch) |
| `ErrInvalidWitnessInputs` (re-exported từ STATE-09) | ZK-04 balance invariant, ZK-05 nullifier mismatch | HTTP 500 (default branch) |
| `context.Canceled` / `context.DeadlineExceeded` | Caller huỷ request trước khi Build chạy | P4 propagate lên client |

## Giải thích từng hàm trong `local_builder.go`

### `Build(ctx context.Context, in BuildInput) (BuildOutput, error)`

Entry point duy nhất của facade. Trình tự xử lý:

1. **`ctx.Err()` check** — nếu caller cancel ngay từ đầu, bail out không mutate gì.
2. **`validateRoot(in.OldStateRoot, "oldStateRoot")`** — dùng lại helper từ STATE-08 để đảm bảo root non-empty + 0x-prefixed. Root rỗng → `ErrInvalidSettlementInputs`.
3. **`indexSecrets(in.AccountSecrets)`** — build map owner→secret, validate duplicate-free.
4. **`collectParticipants(in.Deposits, in.WithdrawRequests)`** — gom danh sách (owner, denom) tham gia, theo first-seen order: deposits trước → withdrawals sau. Đây cũng là thứ tự `Witness.Accounts[]` xuất hiện ở output.
5. **`state.NewLocalState()`** — tạo state FRESH cho batch này. Đây là "fresh state per batch" shortcut được STATE-14 mô tả; mỗi Build call có một LocalState throwaway.
6. **Vòng `ApplyDeposit`** — STATE-03 cho mỗi deposit. Lỗi (deposit id trùng, owner/denom trống) wrap thành `ErrInvalidBuildInput`.
7. **Vòng withdraw** cho mỗi `WithdrawRequest`:
   - `resolveSecret(secrets, owner)` — secret thật nếu caller có truyền, ngược lại literal `"mock-user-secret"`.
   - `state.NullifierFor(secret, req.Nonce)` — STATE-06.
   - `state.WithdrawAddressHash(req.Destination)` — STATE-07.
   - `ls.ApplyWithdrawal(req, nullifier)` — STATE-05. Khi err là `state.ErrInsufficientBalance`, wrap thành `ErrInsufficientOffchainBalance`; mọi err khác (nonce mismatch, nullifier replay) → `ErrInvalidBuildInput`.
   - Append một `WithdrawalInput{Request, Nullifier, DestinationHash}` vào local slice.
8. **`newRoot := ls.Root()`** — chốt root sau khi áp dụng toàn bộ deposit + withdraw.
9. **`b.settlement.Build(sin)`** — STATE-08, re-validate (roots khác nhau, single-denom invariant, destinationHash binding khớp). Builder mint `BatchID = "batch-<seq>"`.
10. **Vòng `accounts`** — với mỗi participant, đọc balance hiện tại từ LocalState. Vì state fresh, `OldBalance = "0"`; `NewBalance = ls.Account(owner, denom).Balance` (kết quả của deposit + withdraw).
11. **`b.witness.Build(win)`** — STATE-09, re-validate `oldBal + sumDeposit == newBal + sumWithdraw` (ZK-04) và re-derive nullifier theo (secret, nonce) (ZK-05).
12. **`BuildCommitments(upd)`** — derive 4 root: `depositsRoot, withdrawalsRoot, nullifiersRoot, withdrawOutputsRoot`.
13. **Trả `BuildOutput`** — cả 3 artifact đầy đủ, atomic.

### `Seq() uint64`

Trả về settlement seq counter — phục vụ debug/test. KHÔNG dùng để mint batch id thủ công.

### `indexSecrets(in []AccountSecret) (map[string]string, error)`

Convert slice secrets thành map nhanh, validate:
- Owner non-empty (sau trim).
- UserSecret non-empty (sau trim).
- Owner duplicate → reject. Duplicate có thể là dấu hiệu copy-paste sai key wallet — fail fast.

Empty input → map rỗng (không lỗi). `resolveSecret` sẽ fallback.

### `resolveSecret(provided map[string]string, owner string) string`

Lookup map. Hit → trả nguyên giá trị. Miss → trả literal `mvpMockSecret = "mock-user-secret"`.

**Lý do dùng literal thay vì per-owner derivation**: Agreements chốt canonical Witness JSON với `userSecret = "mock-user-secret"`. INT-02 test của P4 lock literal này. Đổi sang derivation khác sẽ break test.

### `collectParticipants(deps, reqs) ([]participant, error)`

Duyệt deposits trước, withdrawals sau. Mỗi (owner, denom) chỉ thêm một lần (first-seen). Trim trước khi compare để khớp với `state.AccountState.newAccountKey` (cũng trim).

Trả về slice `[]participant{owner, denom}` — ổn định, deterministic giữa các lần Build.

### `var _ Builder = (*LocalBuilder)(nil)`

Compile-time assertion: bảo đảm `*LocalBuilder` thoả `Builder` interface. Bị break ngay tại `go build` nếu signature drift.

## Mapping với pipeline state-of-the-art

| Bước trong Flow doc (deposit + withdraw) | STATE primitive | Vị trí trong `LocalBuilder.Build` |
|---|---|---|
| Deposit flow step 5-6 (indexer apply deposit) | STATE-03 | Vòng `ApplyDeposit` (bước 6) |
| Withdraw flow step 2 (check balance) | STATE-05 internal check | Trong `ApplyWithdrawal`, map sang `ErrInsufficientOffchainBalance` |
| Withdraw flow step 3 (debit, nonce++, newRoot) | STATE-05 | Vòng `ApplyWithdrawal` (bước 7c) |
| Withdraw flow step 4 (nullifier compute) | STATE-06 | `state.NullifierFor` (bước 7a) |
| Withdraw flow step 4 (destination hash) | STATE-07 | `state.WithdrawAddressHash` (bước 7b) |
| Deposit/withdraw flow step 7-8 (build batch) | STATE-08 + STATE-09 | `settlement.Build` + `witness.Build` (bước 9, 11) |
| ZK settlement batch — public inputs commitment | Commitments | `BuildCommitments(upd)` (bước 12) |

## Test vectors

Canonical Alice 100/40:

```
Input:
  OldStateRoot     = "0xrootA"   (echo-only)
  Deposits         = [ { depositId="dep-1", owner="cosmos1alice", denom="uusdc", amount="100" } ]
  WithdrawRequests = [ { withdrawId="wd-1", owner="cosmos1alice", denom="uusdc",
                         amount="40", destination="cosmos1alice", nonce="1" } ]
  AccountSecrets   = [ { owner="cosmos1alice", userSecret="alice_secret" } ]
                     -- HOẶC nil → fallback "mock-user-secret"

Expected Output:
  SettlementUpdate:
    batchId       = "batch-N"  (N = settlement.Seq + 1)
    oldStateRoot  = "0xrootA"
    newStateRoot  = SHA256(...)  -- determined by LocalState after ops
    deposits      = [ <echo dep-1, amount=100> ]
    withdrawals   = [ {
                        withdrawId="wd-1", amount="40",
                        destination="cosmos1alice",
                        destinationHash=WithdrawAddressHash("cosmos1alice"),
                        nullifier=NullifierFor(secret, "1"),
                      } ]
  BatchCommitments: { depositsRoot, withdrawalsRoot, nullifiersRoot, withdrawOutputsRoot } -- non-empty
  Witness:
    accounts[0] = { owner="cosmos1alice", userSecret=<resolved>, nonce="1",
                    oldBalance="0", newBalance="60" }
```

## Edge cases được cover

| Tình huống | Hành vi |
|---|---|
| `ctx` cancel trước Build | Return `context.Canceled` ngay, không tạo LocalState |
| `OldStateRoot=""` | `ErrInvalidSettlementInputs` từ `validateRoot` |
| `OldStateRoot` không có `0x` prefix | `ErrInvalidSettlementInputs` |
| `Deposits=[]` và `WithdrawRequests=[]` | `ErrInvalidSettlementInputs` từ STATE-08 (empty batch) |
| Duplicate depositId trong cùng batch | `ErrInvalidBuildInput` (STATE-03 idempotency reject) |
| Withdraw amount > balance | `ErrInsufficientOffchainBalance` |
| Nonce sai (≠ account.Nonce+1) | `ErrInvalidBuildInput` (STATE-05 nonce mismatch) |
| Owner trống ở deposit hoặc withdraw | `ErrInvalidBuildInput` |
| `AccountSecrets` duplicate owner | `ErrInvalidBuildInput` |
| `AccountSecrets` rỗng / nil | Fallback `"mock-user-secret"`, vẫn Build thành công |
| Mixed-denom (deposit usdc + withdraw atom) | `ErrInvalidSettlementInputs` từ STATE-08 (single-denom invariant) |
| Tampered `destination` (re-derive mismatch) | Không thể xảy ra vì facade tự derive — nhưng nếu caller bypass và truyền custom SettlementInputs vào builder STATE-08 trực tiếp, mismatch sẽ bị bắt |
| Concurrent Build calls trên cùng LocalBuilder | Safe — LocalState mỗi call riêng, settlement seq được mutex |

## Tích hợp với P4

P4 đã wire sẵn `internal/service/batch_service.go`:

```go
output, err := s.batchBuilder.Build(ctx, batchbuilder.BuildInput{
    OldStateRoot:     "0xrootA",
    Deposits:         deposits,         // resolve từ depositIds[]
    WithdrawRequests: withdrawRequests, // resolve từ withdrawIds[]
})
// dùng output.SettlementUpdate, output.BatchCommitments, output.Witness
```

Và `internal/handler/batch_handler.go`:

```go
case errors.Is(err, batchbuilder.ErrInsufficientOffchainBalance):
    response.Error(w, http.StatusBadRequest, "insufficient off-chain balance")
```

Integration test `tests/int07_batch_builder_test.go` chạy end-to-end qua HTTP. Sau STATE-13 align, **tất cả test pass**:

```
ok  github.com/zhenjb/ganc-sys/internal/batch  1.009s
ok  github.com/zhenjb/ganc-sys/internal/state  (cached)
ok  github.com/zhenjb/ganc-sys/tests           2.301s
```

## Việc còn lại (out-of-scope STATE-13)

| Task | Owner | Trigger |
|---|---|---|
| STATE-14 — `OffchainStateManager` persistent | P3 | Sau khi STATE-13 stable; thay "fresh state per batch" |
| P4 wire keystore/wallet để truyền AccountSecrets thật | P4 | Khi P5 wallet SDK sẵn sàng — xoá fallback `mvpMockSecret` |
| ZK-02 chốt hash circuit (Poseidon/MiMC) | P2 | Đồng bộ bump `nullifierDomainTag` v0→v1 + regenerate vectors |
