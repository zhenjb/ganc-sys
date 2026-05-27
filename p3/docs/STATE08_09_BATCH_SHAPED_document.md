# STATE-08 & STATE-09 — Batch-shaped output (Agreements v2)

> Document này là phiên bản update của `STATE08_document.md` và
> `STATE09_document.md` sau khi P3 migrate output sang batch-shaped
> theo Agreements (deposits[], withdrawals[], destination/destinationHash,
> BatchCommitments). Hai file cũ vẫn còn trong `docs/` để tra cứu lịch
> sử shape scalar — KHÔNG xoá để dễ trace migration.

## 1. Schema chốt theo Agreements

### 1.1 `types.SettlementUpdate`

```json
{
  "batchId": "batch-1",
  "oldStateRoot": "0xrootB",
  "newStateRoot": "0xrootC",
  "deposits": [
    {
      "depositId": "dep-1",
      "owner": "cosmos1alice",
      "denom": "uusdc",
      "amount": "100"
    }
  ],
  "withdrawals": [
    {
      "withdrawId": "wd-1",
      "owner": "cosmos1alice",
      "denom": "uusdc",
      "amount": "40",
      "destination": "cosmos1alice",
      "destinationHash": "0x...",
      "nullifier": "0x..."
    }
  ]
}
```

Đặc tả:
- `deposits[]`, `withdrawals[]` là **mảng ngay từ ngày đầu** — Alice
  vector chỉ có 1 entry mỗi loại nhưng schema phải support nhiều.
- Trường tên đúng theo Agreements: `destination`, `destinationHash`
  (KHÔNG `withdrawAddress`/`withdrawAddressHash`).
- Mỗi withdrawal mang theo `nullifier` của chính nó (1:1).

### 1.2 `types.Witness`

```json
{
  "accounts": [
    {
      "owner": "cosmos1alice",
      "userSecret": "alice_secret",
      "nonce": "1",
      "oldBalance": "0",
      "newBalance": "60"
    }
  ]
}
```

Đặc tả:
- `accounts[]` cho phép nhiều account cùng batch.
- `userSecret` PRIVATE — không bao giờ rời file witness.
- `nonce` = nonce post-last-withdraw của account đó (canonical Alice = 1).
- `oldBalance` / `newBalance` là balance của
  `(owner, batch denom)` trước/sau toàn bộ batch.

### 1.3 `types.BatchCommitments`

```json
{
  "depositsRoot":        "0x...",
  "withdrawalsRoot":     "0x...",
  "nullifiersRoot":      "0x...",
  "withdrawOutputsRoot": "0x..."
}
```

Bốn root MAP 1:1 vào `publicInputs[2..5]` của proof.

## 2. API Go (P3)

### 2.1 `internal/batch/builder.go`

```go
type WithdrawalInput struct {
    Request         types.WithdrawRequest
    Nullifier       string
    DestinationHash string
}

type SettlementInputs struct {
    OldStateRoot string
    NewStateRoot string
    Deposits     []types.DepositRecord
    Withdrawals  []WithdrawalInput
}

type SettlementUpdateBuilder struct { /* ... */ }
func NewSettlementUpdateBuilder() *SettlementUpdateBuilder
func (b *SettlementUpdateBuilder) Build(in SettlementInputs) (types.SettlementUpdate, error)
func (b *SettlementUpdateBuilder) Seq() uint64
```

Validation pipeline:

1. Roots non-empty + hex-prefixed + KHÁC nhau.
2. Batch không được rỗng (≥ 1 deposit hoặc ≥ 1 withdrawal).
3. Mỗi deposit: identity non-empty, amount > 0.
4. Mỗi withdrawal: identity non-empty, amount > 0, nonce ≥ 0, nullifier
   + destinationHash đều hex-prefixed.
5. **Single-denom invariant**: mọi deposit + withdrawal cùng denom (MVP).
6. **Re-derive defense**: `state.WithdrawAddressHash(destination)` phải
   trả về `DestinationHash` được supply — chống tampering.

Post-conditions:
- `BatchID = "batch-N"` (post-increment seq, thread-safe).
- Mọi amount normalize qua big.Int.

### 2.2 `internal/batch/witness.go`

```go
type AccountWitnessSecret struct {
    Owner      string
    UserSecret string
    OldBalance string
    NewBalance string
}

type WitnessInputs struct {
    Settlement SettlementInputs
    Accounts   []AccountWitnessSecret
    StatePath  []string
}

type WitnessBuilder struct{}
func NewWitnessBuilder() *WitnessBuilder
func (b *WitnessBuilder) Build(in WitnessInputs) (types.Witness, error)
```

Validation pipeline cho mỗi account:

1. Owner non-empty và xuất hiện ít nhất 1 lần trong deposits hoặc
   withdrawals của batch (account không-liên-quan bị reject).
2. UserSecret non-empty (sau trim).
3. OldBalance / NewBalance parse non-negative int.
4. **ZK-04**: tính `sumDeposit = Σ deposits.amount where Owner==acc.Owner`
   và `sumWithdraw = Σ withdrawals.amount where Owner==acc.Owner`. Assert
   `newBalance + sumWithdraw == oldBalance + sumDeposit`.
5. **ZK-05**: với MỖI withdrawal của Owner, re-derive
   `state.NullifierFor(secret, request.Nonce)` và assert khớp.

Post-conditions:
- `accounts[i].Nonce` = nonce của withdrawal cuối cùng (canonical Alice = 1).
- `StatePath` defensive copy hoặc `nil` (`omitempty`).

### 2.3 `internal/batch/commitments.go`

```go
const (
    depositsRootDomainTag        = "zkdex/batch/depositsRoot/v0"
    withdrawalsRootDomainTag     = "zkdex/batch/withdrawalsRoot/v0"
    nullifiersRootDomainTag      = "zkdex/batch/nullifiersRoot/v0"
    withdrawOutputsRootDomainTag = "zkdex/batch/withdrawOutputsRoot/v0"
)

func BuildCommitments(upd types.SettlementUpdate) types.BatchCommitments
func CommitmentDomainTags() (deposits, withdrawals, nullifiers, withdrawOutputs string)
```

Recipe placeholder MVP:

```
depositsRoot        = SHA256( "zkdex/batch/depositsRoot/v0|"
                              | (depositId|owner|denom|amount;)* )
withdrawalsRoot     = SHA256( "zkdex/batch/withdrawalsRoot/v0|"
                              | (withdrawId|owner|denom|amount|destination|destinationHash|nullifier;)* )
nullifiersRoot      = SHA256( "zkdex/batch/nullifiersRoot/v0|"
                              | (nullifier;)* )
withdrawOutputsRoot = SHA256( "zkdex/batch/withdrawOutputsRoot/v0|"
                              | (destinationHash|amount|denom;)* )
```

Khi ZK-02 chốt circuit hash (Poseidon/MiMC), bump 4 domain tag từ `v0`
→ `v1`, regenerate vectors. Interface giữ nguyên.

## 3. Test vectors

Folder `testvectors/alice_100_40/` (regenerate bằng
`go run ./p3/script-test/gen_state_vectors`):

| File | Mô tả |
| --- | --- |
| `initial_state.json` | rootA, accounts rỗng |
| `deposit_dep_1.json` | DepositRecord canonical (Alice 100 uusdc) |
| `state_after_deposit.json` | rootB, balance 100 |
| `withdraw_request_wd_1.json` | WithdrawRequest 40 uusdc, nonce=1 |
| `nullifier_wd_1.json` | nullifier + domain tag + algo |
| `destination_hash_wd_1.json` | destinationHash + domain tag + algo (mới, thay `withdraw_address_hash_*`) |
| `state_after_withdrawal.json` | rootC, balance 60, nonce 1 |
| `settlement_update_batch_1.json` | `SettlementUpdate` batch-shaped |
| `batch_commitments_batch_1.json` | 4 commitment root + meta (mới) |
| `witness_batch_1.json` | `Witness` accounts[] (mới shape) |

Roots Alice 100/40:
- rootA = `0xe4029e127d0d318624204f91c87aed84377819b97f1c80cc53edf9b35840805d`
- rootB = `0x9b325b4150d417adfd816930b6f291aaf9493995fe0f960864c616ff178f8620`
- rootC = `0x44d60f77db879799be6212c46d198243cbe2fe2969901ca3663dfb204276250c`

4 commitment root:
- depositsRoot        = `0x5ed1bb5a...bbc40`
- withdrawalsRoot     = `0xea5ccc9d...fdb1`
- nullifiersRoot      = `0x13b485d1...6f21`
- withdrawOutputsRoot = `0xce5e1274...8e14`

## 4. Tác động sang P4 (đã đồng bộ)

Khi migrate canonical types, ba file P4 bắt buộc phải cập nhật cùng
lúc để build/test còn xanh:

| File | Thay đổi |
| --- | --- |
| `internal/repository/mock_repository.go` | `GetSettlementUpdate` trả về batch-shaped với `Deposits[]` / `Withdrawals[]`; `GetWitness` dùng `Accounts[]`; `GetProofBundle.PublicInputs` đổi sang 6 root theo Agreements (`oldRoot, newRoot, depositsRoot, withdrawalsRoot, nullifiersRoot, withdrawOutputsRoot`) |
| `internal/service/mock_service.go` | không phải sửa — chỉ pass-through `types.SettlementUpdate` / `types.Witness` |
| `tests/int02_mock_endpoints_test.go` | dùng helper `mockSettlementUpdate()` / `mockWitness()` cho batch-shaped; assertion `body.Witness.Accounts[0].NewBalance`; public input check trỏ tới `nullifiersRoot` / `withdrawOutputsRoot` |

Các phần API contract còn pending ở P4 (KHÔNG nằm trong scope migration
này, sẽ do P4 owner tự triển khai khi cần):

- `BuildBatchRequestBody` đổi sang `{depositIds[], withdrawIds[]}` (hiện
  vẫn scalar).
- Thêm `BatchCommitments` ở
  `BuildBatchResponse` / `GenerateProofRequestBody` /
  `SubmitBatchRequestBody` / `SubmitBatchResponse`.
- `SubmitBatchResponse.withdrawRecords[]` (plural).
- `ClaimWithdrawResponse.balances` nested `{userBalances, moduleAccountBalance}`.
- `AppState.latestWithdrawRecords[]`, `AppState.latestBatchCommitments`,
  `AppState.batchStatus`.

Xem changenote
[`2026-05-26-state-08-09-batch-shaped-migration.md`](../changenotes/2026-05-26-state-08-09-batch-shaped-migration.md)
cho chi tiết migration trực tiếp (không qua additive types).
