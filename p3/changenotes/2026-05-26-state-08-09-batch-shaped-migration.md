# 2026-05-26 — Migrate STATE-08/09 output sang batch-shaped (Agreements compliance)

## Context

STATE-01..09 đã hoàn thành ở phase trước với `pkg/types.SettlementUpdate`
+ `pkg/types.Witness` dạng **scalar** (một deposit, một withdraw mỗi
batch, field `withdrawAddress` / `withdrawAddressHash`). Sau đó
Agreements v2 chốt lại:

- SettlementUpdate phải là **batch-shaped**: `deposits[]`,
  `withdrawals[]` (kể cả khi MVP chỉ có 1 entry mỗi loại).
- Đặt tên: `destination` / `destinationHash`. **KHÔNG** dùng
  `withdrawAddress` / `withdrawAddressHash`.
- Witness: `accounts[]` (mỗi account có owner/userSecret/nonce/oldBalance/newBalance).
- Phải có **BatchCommitments**: 4 root `depositsRoot`,
  `withdrawalsRoot`, `nullifiersRoot`, `withdrawOutputsRoot` bind toàn
  bộ batch vào proof public inputs.

Vector cũ ở `testvectors/alice_100_40/` và builder ở `internal/batch/`
output không khớp Agreements → cần migrate.

## Quyết định: sửa trực tiếp pkg/types

Lần đầu prototype chọn hướng additive (`BatchSettlementUpdate` /
`BatchWitness` song song với legacy). Sau review, đổi sang **sửa trực
tiếp** vì:

- Hai struct mô tả cùng concept là tech debt thật, không phải tạm thời.
- Naming `BatchSettlementUpdate` chỉ tồn tại vì legacy đang giữ tên
  `SettlementUpdate` — vô nghĩa khi legacy bị xoá luôn.
- P4 mock chỉ có 3 file phụ thuộc legacy schema, sửa cùng lúc là rẻ
  hơn dài hạn so với việc gánh prefix `Batch*` trong toàn bộ codebase.

## Thay đổi

### `pkg/types/`

| File | Trạng thái | Ghi chú |
| --- | --- | --- |
| `settlement.go` | **rewrite** | `SettlementUpdate{Deposits[], Withdrawals[]}` + `SettlementDeposit`, `SettlementWithdrawal` |
| `witness.go` | **rewrite** | `Witness{Accounts[]}` + `WitnessAccount` |
| `commitments.go` | **mới** | `BatchCommitments{4 roots}` |

> Legacy scalar `SettlementUpdate`/`Witness` đã bị xoá hoàn toàn — không
> còn additive types.

### `internal/batch/`

| File | Trạng thái | Ghi chú |
| --- | --- | --- |
| `builder.go` | **rewrite** | `SettlementInputs{Deposits[], Withdrawals[]WithdrawalInput}` → `types.SettlementUpdate`. Validate per-entry, single-denom invariant cho cả batch, re-derive destinationHash chống tampering |
| `witness.go` | **rewrite** | `WitnessInputs{Settlement, Accounts[]AccountWitnessSecret}` → `types.Witness`. ZK-04 dùng tổng (deposit credit, withdraw debit) theo Owner; ZK-05 re-derive nullifier cho TỪNG withdrawal của Owner |
| `commitments.go` | **mới** | `BuildCommitments(types.SettlementUpdate) types.BatchCommitments` (SHA-256 placeholder với 4 domain tag riêng) |
| `builder_test.go` | rewrite | 11 test bao phủ canonical Alice, sequential batchID, empty batch, mixed denom, destinationHash tamper, invalid roots, identity, normalize, no-op rejection, concurrent, schema canary |
| `witness_test.go` | rewrite | 11 test bao phủ canonical Alice, nullifier binding, balance violation, mismatch, owner-not-in-batch, scalar invalid, normalize, defensive copy, trim, concurrent |
| `commitments_test.go` | **mới** | 5 test: hex prefix + length, deterministic, domain separation, sensitivity tới withdrawal mutation, domain tag exposed |

### P4 (đồng bộ tối thiểu để build/test xanh)

| File | Thay đổi |
| --- | --- |
| `internal/repository/mock_repository.go` | `GetSettlementUpdate` chuyển sang batch-shaped (Deposits[]/Withdrawals[]); `GetWitness` dùng Accounts[]; `GetProofBundle.PublicInputs` đổi sang 6-root order Agreements |
| `tests/int02_mock_endpoints_test.go` | thêm helper `mockSettlementUpdate()` / `mockWitness()`; assertion update sang `Witness.Accounts[0].NewBalance` và `PublicInputs[4]=nullifiersRoot`, `[5]=withdrawOutputsRoot` |

> `internal/service/mock_service.go` không cần sửa (chỉ pass-through type).

### `p3/script-test/gen_state_vectors/main.go`

- Dùng `batch.SettlementInputs{Deposits[], Withdrawals[]}` để build vector.
- Sinh thêm `batch_commitments_batch_1.json`.
- Đổi tên file `withdraw_address_hash_wd_1.json` → `destination_hash_wd_1.json`
  + tự xoá file legacy nếu còn.
- In log batch commitments để dễ debug.

### `testvectors/alice_100_40/`

| File | Trạng thái |
| --- | --- |
| `settlement_update_batch_1.json` | rewrite → batch-shaped với `deposits[]`, `withdrawals[]`, dùng `destination`/`destinationHash` |
| `witness_batch_1.json` | rewrite → `{accounts:[{owner,userSecret,nonce,oldBalance,newBalance}]}` |
| `batch_commitments_batch_1.json` | **mới** |
| `destination_hash_wd_1.json` | **mới** (thay `withdraw_address_hash_wd_1.json`) |
| `withdraw_address_hash_wd_1.json` | **xoá** (legacy) |
| Các file khác | không đổi nội dung |

## Verification

```text
go build ./...                         → OK
go test ./internal/batch/... -count=1  → PASS (27 cases)
go test ./... -count=1                 → PASS (toàn repo, P4 mock đã đồng bộ)
go run ./p3/script-test/gen_state_vectors → regenerate sạch
```

Roots Alice 100/40 sau migration:
- `rootA = 0xe4029e12...0805d`
- `rootB = 0x9b325b41...8f8620`
- `rootC = 0x44d60f77...76250c`
- `nullifier = 0x1a1fdf4c...22f7`
- `destinationHash = 0xa75ac956...b417`
- 4 batch root SHA-256(domain | canonical bytes) — xem
  `batch_commitments_batch_1.json`.

## Tác động cho roles khác

- **P2 (ZK):** input vào prover giờ là `types.SettlementUpdate` +
  `types.BatchCommitments` + `types.Witness`. Public input order theo Agreements:
  `[oldRoot, newRoot, depositsRoot, withdrawalsRoot, nullifiersRoot, withdrawOutputsRoot]`.
  Domain tag MVP `zkdex/batch/<root>/v0` — bump khi chốt Poseidon/MiMC.
- **P1 (chain verifier):** sẽ verify proof bound vào 4 commitment root.
  Re-derive 4 root từ `types.SettlementUpdate` bằng cùng recipe ở
  `internal/batch/commitments.go`.
- **P4 (backend/relayer):** mock data đã đồng bộ; còn lại các phần API
  contract (BatchCommitments ở `/api/batch/build`, `/api/proof/generate`,
  `/api/batch/submit`; array hoá `withdrawIds`/`depositIds`/`withdrawRecords[]`;
  nested balances ở claim response; `LatestBatchCommitments` /
  `LatestWithdrawRecords[]` / `BatchStatus` ở AppState) sẽ do P4 owner
  triển khai khi cần.
- **P5 (frontend):** đợi P4 mở rộng API contract.

## Công nợ kỹ thuật

1. `internal/batch/commitments.go` đang dùng SHA-256 over concatenated
   bytes (placeholder). Khi ZK-02 chốt circuit hash (Poseidon/MiMC) cần
   thay implementation nhưng giữ interface.
2. Khi chốt circuit, bump 4 commitment domain tags từ `v0` → `v1` và
   regenerate vectors.
3. P4 mock đang dùng test data tự bịa cho 4 commitment root
   (`0xmockdepositsroot`, v.v.) — khi tích hợp prover/verifier thật, P4
   sẽ chạy `batch.BuildCommitments` để có giá trị đúng.
