# Canonical test vector — `alice_100_40` (STATE-11)

Đây là **single source of truth** cho mọi role (P1 verifier, P2 prover/circuit, P4 backend, P5 frontend) khi cần dữ liệu test cố định cho luồng ZK settlement MVP.

## Kịch bản Alice 100/40

| Bước | Mô tả | Số dư Alice | Số dư module account | Root |
|---|---|---|---|---|
| 0 | Khởi tạo (chưa có account nào) | x/bank: 1000 / off-chain: 0 | 0 | `rootA` |
| 1 | Deposit 100 (Alice → module) → indexer credit pending | x/bank: 900 / off-chain: 100 | 100 | `rootB` |
| 2 | Withdraw request 40, off-chain debit, batch settled | x/bank: 900 / off-chain: 60 | 100 | `rootC` |
| 3 | Claim withdraw (module → Alice) | x/bank: 940 / off-chain: 60 | 60 | (unchanged) |

Lưu ý: folder này chỉ chốt vector cho **bước 0..2** (đến khi proof batch accepted). Step 3 là on-chain transfer thuần và không ghi vào ZK proof.

## Inventory file

| File | STATE task | Schema (Go) | Consumer | Mô tả |
|---|---|---|---|---|
| `initial_state.json` | STATE-02 | `StateSnapshot` | P1, P2, P3, P4 | Root khởi tạo, account empty. |
| `deposit_dep_1.json` | STATE-03 (input) | `types.DepositRecord` | P1, P3, P4 | Canonical dep-1 record. |
| `state_after_deposit.json` | STATE-03 | `StateSnapshot` | P2, P3, P4 | Pending off-chain state sau deposit. |
| `withdraw_request_wd_1.json` | STATE-04 | `types.WithdrawRequest` | P3, P4, P5 | Canonical wd-1 request. |
| `nullifier_wd_1.json` | STATE-06 | `NullifierVector` | P1, P2 | Nullifier = `H(domain \| userSecret \| nonce)`. |
| `destination_hash_wd_1.json` | STATE-07 | `DestinationHashVector` | P1, P2 | DestinationHash = `H(domain \| destination)`. |
| `state_after_withdrawal.json` | STATE-05 | `StateSnapshot` | P2, P3, P4 | Post-batch state. |
| `settlement_update_batch_1.json` | STATE-08 | `types.SettlementUpdate` | P1, P2, P4, P5 | Batch-shaped settlement update. |
| `batch_commitments_batch_1.json` | STATE-08-ext | `BatchCommitmentsVector` | P1, P2, P4 | 4 commitment root cho public inputs[2..5]. |
| `witness_batch_1.json` | STATE-09 | `types.Witness` | P2 | Private witness. **Demo-only secret.** |
| `public_inputs_batch_1.json` | STATE-10 | `PublicInputsVector` | P1, P2, P4 | Slice public input đúng thứ tự Agreements. |
| `MANIFEST.json` | STATE-11 | `Manifest` | Everyone | File index + SHA-256 + scenario roots. |

## Sử dụng từ Go

```go
import "github.com/zhenjb/ganc-sys/pkg/testvectors"

s, err := testvectors.LoadAliceScenario()
// hoặc Load* riêng từng file:
upd, _ := testvectors.LoadSettlementUpdate()
ws, _  := testvectors.LoadWitness()
pi, _  := testvectors.LoadPublicInputs()
```

Trong test, dùng `MustLoadAliceScenario()` (panic nếu fail) và gọi `SanityCheck()` để verify cross-file invariant.

**Tuyệt đối không** tự bash path string `testvectors/alice_100_40/...` — package `pkg/testvectors` resolve folder theo `go.mod`, không phụ thuộc cwd của test invocation.

## Regenerate

```bash
go run ./p3/script-test/gen_state_vectors
```

Lệnh này sẽ:

1. Re-build tất cả 11 vector JSON từ primitives `internal/state` + `internal/batch`.
2. Tính SHA-256 nội dung từng file.
3. Ghi `MANIFEST.json` chứa hash + metadata.

Sau khi regenerate, `go test ./pkg/testvectors/...` PHẢI xanh.

## Versioning

`vectorVersion` (manifest top-level) chốt format. Bump khi:

- ZK-02 chuyển hash circuit (SHA-256 placeholder → Poseidon/MiMC).
- Đổi domain tag (`zkdex/nullifier/v0` → `v1`, v.v.).
- Schema thay đổi (thêm field public input, đổi shape Witness, v.v.).

Dev cần đồng thời:

1. Bump `vectorVersion` trong `gen_state_vectors/main.go`.
2. Bump `ExpectedVectorVersion` trong `pkg/testvectors/manifest.go`.
3. Chạy generator → commit file mới.

Test `TestManifest_VersionPinned` sẽ fail nếu chỉ bump 1 bên.

## Tại sao có hai literal "secret" khác nhau?

Có chủ đích — đây là **distinction** giữa:

| | Static folder (file này) | Runtime mock pipeline (P4 BatchService) |
|---|---|---|
| `userSecret` | `"alice_secret"` | `"mock-user-secret"` |
| Nullifier | `0x1a1fdf4c…22f7` | recompute theo runtime |
| Mục đích | Lock baseline cho ZK-05 unit test | Đáp ứng `tests/int02` lock Agreements canonical Witness JSON |
| Khi nào sẽ thống nhất | Sau khi P4 wire keystore/wallet thật và xoá fallback `mvpMockSecret` | Cùng lúc — STATE-13 ghi rõ |

`internal/batch/local_builder.go::resolveSecret` fallback về `"mock-user-secret"` khi P4 không truyền `AccountSecrets` (đa số request HTTP). Folder này dùng `"alice_secret"` để kế thừa lịch sử STATE-06 (giá trị nullifier const đã lock test trong `internal/state/nullifier_test.go`).

**Không sửa folder này để khớp runtime fallback** — sẽ break STATE-06 nullifier const + STATE-13 changenote. Khi P4 wire wallet thật, hai luồng tự thống nhất (đều dùng secret từ wallet).

## Đường nhập cho từng role

- **P1 (verifier on-chain)**: dùng `settlement_update_batch_1.json` + `batch_commitments_batch_1.json` làm payload public; dùng `public_inputs_batch_1.json` để verify slice; dùng `MANIFEST.json::domainTags` để chốt hash function on-chain.
- **P2 (prover/circuit)**: dùng `witness_batch_1.json` (private) + `settlement_update_batch_1.json` (public hint) để generate proof; verify recompute `nullifier_wd_1.json` và `destination_hash_wd_1.json` theo cùng hash function.
- **P3 (state/batch)**: owner — không tiêu thụ tự bản thân, generate ra folder này.
- **P4 (backend)**: dùng `deposit_dep_1.json` + `withdraw_request_wd_1.json` làm fixture cho integration test; dùng `MANIFEST.json::roots.rootA` để init genesis local chain.
- **P5 (frontend)**: dùng `MANIFEST.json::alice` để hiển thị scenario demo; dùng `settlement_update_batch_1.json` + `public_inputs_batch_1.json` cho screenshot pack.

## Failure vectors (STATE-12 — đã có)

Negative test vectors nằm trong subfolder [`failure_vectors/`](./failure_vectors/) với MANIFEST.json riêng:

- `over_withdraw.json` — STATE-04 reject vì balance không đủ.
- `wrong_root.json` — P1 chain reject vì `oldStateRoot` lệch.
- `duplicate_nullifier.json` — chain/STATE-05 reject vì nullifier replay.
- `tampered_destination.json` — P2/P1 reject vì `destinationHash != H(destination)`.

Xem [`failure_vectors/README.md`](./failure_vectors/README.md) cho schema + cách dùng từ Go (`testvectors.LoadFailureBundle()`).
