# P3 — STATE-08: Build SettlementUpdate

## 1. Nhiệm vụ

STATE-08 gom mọi artifact off-chain (`oldRoot`, `newRoot`, `DepositRecord`, `WithdrawRequest`, `nullifier`, `withdrawAddressHash`) thành một `SettlementUpdate` chuẩn — payload mà:

1. **P2 prover** (ZK-09) dùng để compute proof: extract public inputs từ update.
2. **P4 relayer** (INT-09) gửi nguyên xi lên chain qua `MsgSubmitBatchProof(settlementUpdate, proofBundle)`.
3. **P1 verifier** (ONCHAIN-08) re-derive public inputs từ update và verify proof.

Schema chốt trong Tab Agreements:

```json
{
  "batchId": "batch-1",
  "oldStateRoot": "0xrootA",
  "newStateRoot": "0xrootB",
  "depositId": "dep-1",
  "depositAmount": "100",
  "withdrawId": "wd-1",
  "withdrawAmount": "40",
  "withdrawAddress": "cosmos1alice...",
  "withdrawAddressHash": "0x...",
  "nullifier": "0x..."
}
```

## 2. Luồng hoạt động

```
STATE-03 ApplyDeposit ─► oldRoot (rootB), DepositRecord
                              │
STATE-04 WithdrawRequest ─────┤
                              ▼
STATE-06 NullifierFor      ┌─────────────────────────────────┐
STATE-07 WithdrawAddressHash│  STATE-08 SettlementUpdateBuilder │
STATE-05 ApplyWithdrawal ─►│  .Build(SettlementInputs)        │
                              └─────────────────────────────────┘
                                          │
                          ┌───────────────┼────────────────┐
                          ▼               ▼                ▼
                   P2 prover         P4 relayer        P1 verifier
                   ZK-09             INT-09            ONCHAIN-08
                   (proofBundle)     (MsgSubmitBatch)  (re-derive)
```

Trong backend (REST):

```
P4 POST /api/batch/build
   │
   ▼
indexer cung cấp DepositRecord  ─┐
STATE-04 build WithdrawRequest  ─┤
STATE-06 NullifierFor           ─┼─► STATE-08 Build ─► SettlementUpdate
STATE-07 WithdrawAddressHash    ─┤                          │
STATE-05 ApplyWithdrawal        ─┘                          ▼
                                                     POST /api/proof/generate
                                                     POST /api/batch/submit
```

## 3. API

### 3.1 `SettlementInputs`

```go
type SettlementInputs struct {
    OldStateRoot        string                  // rootB (pre-withdrawal)
    NewStateRoot        string                  // rootC (post-withdrawal)
    Deposit             types.DepositRecord     // from STATE-03 / chain indexer
    Withdraw            types.WithdrawRequest   // from STATE-04
    Nullifier           string                  // from STATE-06
    WithdrawAddressHash string                  // from STATE-07
}
```

Tất cả field bắt buộc. Builder không nhận `userSecret` — không cần secret để assemble update; giữ secret ra khỏi `batch` package là invariant bảo mật.

### 3.2 `SettlementUpdateBuilder`

```go
type SettlementUpdateBuilder struct { /* mu, seq */ }

func NewSettlementUpdateBuilder() *SettlementUpdateBuilder
func (b *SettlementUpdateBuilder) Build(in SettlementInputs) (types.SettlementUpdate, error)
func (b *SettlementUpdateBuilder) Seq() uint64
```

- `New...` cấp builder mới với `seq=0`.
- `Build` validate + assemble; trả `SettlementUpdate` với `BatchID="batch-N"` (N tăng dần).
- `Seq` trả số batch đã build thành công (debug/test).

### 3.3 Sentinel error

```go
var ErrInvalidSettlementInputs = errors.New("batch: invalid settlement inputs")
```

Mọi failure bọc sentinel này. P4 map sang HTTP 400 với message gốc trong body.

## 4. Pipeline validation chi tiết

| # | Rule | Failure substring | Test cover |
|---|---|---|---|
| 1 | `OldStateRoot` non-empty + `0x` prefix + non-empty sau strip | `oldStateRoot is empty` / `missing 0x` / `empty after stripping` | `TestBuild_InvalidRoots` |
| 2 | `NewStateRoot` non-empty + `0x` prefix + non-empty sau strip | `newStateRoot is empty` / `missing 0x` | `TestBuild_InvalidRoots` |
| 3 | `OldStateRoot != NewStateRoot` | `no-op batch` | `TestBuild_RejectsNoOpBatch` |
| 4 | `Deposit.DepositID/Owner/Denom` non-empty | `deposit.depositId is empty` v.v. | `TestBuild_InvalidDepositAndWithdrawIdentity` |
| 5 | `Deposit.Amount` parse positive `big.Int` | `deposit.amount` | sub-tests `deposit_amount_*` |
| 6 | `Withdraw.WithdrawID/Owner/Denom/Destination` non-empty | `withdraw.xxx is empty` | `TestBuild_InvalidDepositAndWithdrawIdentity` |
| 7 | `Withdraw.Amount` positive, `Nonce` non-negative | `withdraw.amount` / `withdraw.nonce` | sub-tests `withdraw_*` |
| 8 | `Nullifier` non-empty + `0x` prefix | `nullifier is empty` / `missing 0x` | `TestBuild_InvalidNullifierAndAddressHash` |
| 9 | `WithdrawAddressHash` non-empty + `0x` prefix | `withdrawAddressHash is empty` / `missing 0x` | `TestBuild_InvalidNullifierAndAddressHash` |
| 10 | `Deposit.Denom == Withdraw.Denom` | `mixed-denom batches not supported` | `TestBuild_RejectsMixedDenom` |
| 11 | `state.WithdrawAddressHash(Withdraw.Destination) == WithdrawAddressHash` | `withdrawAddressHash mismatch` | `TestBuild_RejectsTamperedWithdrawAddressHash` |

Toàn bộ failure → sentinel `ErrInvalidSettlementInputs` (wrapper với cause).

## 5. Mỗi hàm cụ thể

### `NewSettlementUpdateBuilder() *SettlementUpdateBuilder`

Trả builder mới. `mu` zero-value (sẵn sàng dùng), `seq=0`.

### `Build(in SettlementInputs) (types.SettlementUpdate, error)`

1. Chạy `validateRoot(in.OldStateRoot, "oldStateRoot")` và `validateRoot(in.NewStateRoot, "newStateRoot")`.
2. Check `OldStateRoot != NewStateRoot`.
3. `depAmt := validateDeposit(in.Deposit)` — trả `*big.Int` đã parse, dùng cho canonical hoá.
4. `wdAmt := validateWithdraw(in.Withdraw)` — tương tự.
5. `validateHex(in.Nullifier, ...)` và `validateHex(in.WithdrawAddressHash, ...)`.
6. Check `Deposit.Denom == Withdraw.Denom`.
7. `rederived := state.WithdrawAddressHash(in.Withdraw.Destination)`; nếu `!= in.WithdrawAddressHash` → reject.
8. Lock `mu`, `seq++`, return `SettlementUpdate{BatchID: "batch-" + Itoa(seq), ...}`.

**Quan trọng:** `seq++` chỉ xảy ra **sau** mọi validation. Nếu Build lỗi, lần thành công kế tiếp vẫn nhận `batch-1` (lock trong `TestBuild_FailureDoesNotIncrementSeq`).

**Amount normalization:** `DepositAmount`/`WithdrawAmount` output là `depAmt.String()` / `wdAmt.String()` — đã canonical hoá qua `big.Int`. `"0100"` input → `"100"` output. Lock trong `TestBuild_NormalizesAmounts`.

### `Seq() uint64`

Đọc `seq` dưới mutex. Idempotent, không mutate.

### `validateRoot(root, label string) error`

Trim → check empty → check `0x` prefix → check `len(strip(root)) > 0`. Trả error đầu tiên gặp với label rõ ràng để debug.

### `validateHex(value, label string) error`

Giống `validateRoot` nhưng dùng cho `nullifier` và `withdrawAddressHash` (label khác). Tách hàm để tránh duplicate body, nhưng giữ tên rõ ràng cho stack trace.

### `validateDeposit(d types.DepositRecord) (*big.Int, error)`

Check `DepositID/Owner/Denom` non-empty (trim). Parse `Amount` qua `parsePositive`. Trả `*big.Int` cho caller canonical hoá.

### `validateWithdraw(w types.WithdrawRequest) (*big.Int, error)`

Check `WithdrawID/Owner/Denom/Destination` non-empty (trim). Parse `Amount` qua `parsePositive`, parse `Nonce` qua `parseNonNegative`. Trả `*big.Int` của `Amount`.

### `parsePositive(amount string) (*big.Int, error)` / `parseNonNegative`

Helper local. Trim → empty check → `big.Int.SetString(., 10)` → sign check. `parsePositive` thêm rule `Sign() != 0`. Tách riêng khỏi `state.parsePositiveAmount` để giữ `batch` package self-contained (không bị coupling vào unexported của `state`).

## 6. Re-derive `withdrawAddressHash` — vì sao là defense-in-depth?

**Threat:** Một component xấu (P4 backend bug, indexer race, malicious middleware) trong off-chain pipeline rewrite `Withdraw.Destination` *sau khi* STATE-07 đã hash. Caller pass cặp `(destination, hash)` không tương ứng.

**Hậu quả nếu không catch:**
- Builder produce `SettlementUpdate` với `withdrawAddress` mới + `withdrawAddressHash` cũ.
- Prover hash `oldDest` (từ witness) → public input chứa `oldHash`.
- Proof valid với `oldHash`.
- Chain re-derive: `Hash(newDest)` → khác `oldHash` → public input mismatch → P1 reject.

→ Bug tự lộ ở chain, nhưng đã tốn 1 round-trip prover (chậm và đắt).

**Catch ở STATE-08:** Re-derive ngay, fail fast, error message chỉ rõ "supplied vs. re-derived(destination=...)". Cost: 1 SHA-256.

**Tại sao không re-derive `nullifier`?** Sẽ phải pass `userSecret` xuống `batch` package. Vi phạm invariant "secret không ra khỏi `state` package" và làm tăng surface area của secret. Trade-off: nullifier sai → P1 verifier sẽ reject (cũng tốn 1 round-trip prover). Chấp nhận trade-off.

## 7. Đồng nhất với các STATE trước

### STATE-04: trim Destination

`WithdrawRequestBuilder.Build` trim `Destination` trước khi gắn vào request. STATE-08 dùng nguyên `Destination` đã trim (không trim lại) — re-deriving hash từ đúng bytes mà STATE-07 đã thấy.

### STATE-05: oldRoot capture order

Generator phải capture `oldRoot := ls.Root()` **trước** `ls.ApplyWithdrawal(...)`. STATE-08 chỉ nhận snapshot; không truy ngược lại `LocalState`.

```go
oldRoot := ls.Root()                        // rootB
rootC, _ := ls.ApplyWithdrawal(req, nullifier)
upd, _ := sub.Build(batch.SettlementInputs{
    OldStateRoot: oldRoot,
    NewStateRoot: rootC,
    ...
})
```

### STATE-06: nullifier value

Caller phải pass **đúng** `nullifier` đã dùng cho `ApplyWithdrawal`. Nếu drift, P1 verifier reject (`!nullifierUsed[nullifier]` check không thoả + public input mismatch). STATE-08 không re-derive nullifier (lý do trong §6).

### STATE-07: address hash matching

Re-derive trong `Build` (rule #11). Test `TestBuild_RejectsTamperedWithdrawAddressHash` lock invariant.

## 8. Kết quả cuối (canonical Alice 100/40 vector)

```json
{
  "batchId": "batch-1",
  "oldStateRoot": "0x9b325b4150d417adfd816930b6f291aaf9493995fe0f960864c616ff178f8620",
  "newStateRoot": "0x44d60f77db879799be6212c46d198243cbe2fe2969901ca3663dfb204276250c",
  "depositId": "dep-1",
  "depositAmount": "100",
  "withdrawId": "wd-1",
  "withdrawAmount": "40",
  "withdrawAddress": "cosmos1alice",
  "withdrawAddressHash": "0xa75ac956249df4c45b83281c5af6187c59df9709fd1c25b5e61b12d71a8eb417",
  "nullifier": "0x1a1fdf4ccecb7040b7cd7e125226d14ed7717618d996dab969d4cb12550b22f7"
}
```

File: `testvectors/alice_100_40/settlement_update_batch_1.json`. Consumer:

- P2 (ZK-09): đọc → derive witness → generate proof.
- P4 (INT-09): forward nguyên xi như payload của `MsgSubmitBatchProof`.
- P1 (ONCHAIN-08): re-derive public inputs `[oldStateRoot, newStateRoot, depositAmount, withdrawAmount, withdrawAddressHash, nullifier]`, verify proof.

## 9. Tests

| Test | Mục đích |
|---|---|
| `TestBuild_CanonicalAliceVector` | Happy path: tất cả field đúng record |
| `TestBuild_SequentialBatchIDs` | `batch-N` mono-increment |
| `TestBuild_RejectsNoOpBatch` | Chặn batch không có transition |
| `TestBuild_RejectsTamperedWithdrawAddressHash` | Defense-in-depth re-derive |
| `TestBuild_RejectsMixedDenom` | Single-denom constraint |
| `TestBuild_InvalidRoots` (5 sub) | Root format/non-empty |
| `TestBuild_InvalidNullifierAndAddressHash` (4 sub) | Hex field format/non-empty |
| `TestBuild_InvalidDepositAndWithdrawIdentity` (11 sub) | Identity + amount/nonce validation |
| `TestBuild_NormalizesAmounts` | Canonical hoá amount qua `big.Int` |
| `TestBuild_FailureDoesNotIncrementSeq` | Seq advance chỉ sau success |
| `TestBuild_ConcurrentBuildsAssignDistinctBatchIDs` | Mutex correctness |
| `TestBuild_OutputMatchesAgreementSchema` | Canary: mọi field non-empty |

Tổng: 12 test functions, 19 sub-tests, ~1s wall-clock.

```bash
go test ./internal/batch/... -v
# PASS — 12/12

go test ./...
# PASS — all packages

go build ./...
# OK

go run ./p3/script-test/gen_state_vectors
# emits settlement_update_batch_1.json with rootB→rootC
```

## 10. Tích hợp với STATE kế tiếp

- **STATE-09 Witness builder**: nhận `SettlementInputs` + `userSecret` + balance snapshots → emit `witness.json`. Witness phải chứa cùng `nullifier` / `withdrawAddressHash` mà STATE-08 đã encode.
- **STATE-10 Public input builder**: nhận `SettlementUpdate` (output STATE-08), trả slice ordered theo ZK I/O contract: `[oldStateRoot, newStateRoot, depositAmount, withdrawAmount, withdrawAddressHash, nullifier]`.
- **STATE-11 Test vector folder**: `settlement_update_batch_1.json` đã trong folder; STATE-11 sẽ bổ sung `witness.json` và `public_inputs.json`.
- **STATE-12 Failure vectors**: 3 scenario STATE-08 reject (`no_op_batch`, `mixed_denom`, `tampered_destination`) đã có unit test; STATE-12 chỉ cần emit JSON tương ứng để P1/P2/P4 dùng làm negative test fixture.
