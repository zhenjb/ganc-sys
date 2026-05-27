# STATE-11 — Canonical test vector folder

## 1. Tổng quan

STATE-11 hoàn thiện folder `testvectors/alice_100_40/` thành **single source of truth** machine-verifiable cho mọi role tiêu thụ vector test:

- **P1** verifier on-chain — đối chiếu `settlement_update` + `batch_commitments` + `public_inputs` với chain payload.
- **P2** prover/circuit — load `witness` + `settlement_update` để generate proof.
- **P4** backend — fixture cho integration test, init genesis state local chain.
- **P5** frontend — render scenario Alice cho demo + screenshot pack.

STATE-11 KHÔNG sinh thêm vector mới (STATE-02..10 đã đủ 11 file). Phần đóng góp của STATE-11:

1. `MANIFEST.json` machine-readable + SHA-256 mỗi file.
2. `README.md` human walkthrough scenario + distinction static vs runtime secret.
3. Go loader package `pkg/testvectors` để mọi role consume vector mà không bash path/parse JSON ad-hoc.
4. 10 unit test bắt 4 dạng drift: byte-level edit tay, missing file, orphan file, cross-file semantic mismatch.

## 2. Sơ đồ liên kết

```
+------------------------------------------------------------+
|        gen_state_vectors (p3/script-test/...)              |
|        |                                                   |
|        | run -> emit 11 JSON + MANIFEST.json               |
|        v                                                   |
+------------------------------------------------------------+
|     testvectors/alice_100_40/                              |
|        - initial_state.json           (STATE-02)           |
|        - deposit_dep_1.json           (STATE-03 input)     |
|        - state_after_deposit.json     (STATE-03)           |
|        - withdraw_request_wd_1.json   (STATE-04)           |
|        - nullifier_wd_1.json          (STATE-06)           |
|        - destination_hash_wd_1.json   (STATE-07)           |
|        - state_after_withdrawal.json  (STATE-05)           |
|        - settlement_update_batch_1.json (STATE-08)         |
|        - batch_commitments_batch_1.json (STATE-08-ext)     |
|        - witness_batch_1.json         (STATE-09)           |
|        - public_inputs_batch_1.json   (STATE-10)           |
|        - MANIFEST.json                (STATE-11)           |
|        - README.md                    (STATE-11)           |
+------------------------------------------------------------+
        ^                       ^
        |                       |
        | load                  | verify SHA-256
        |                       |
+--------------------+   +-----------------+
| pkg/testvectors/   |   | manifest_test   |
|   alice.go         |   |   alice_test    |
|   manifest.go      |---+                 |
|   doc.go           |   +-----------------+
+--------------------+
        ^
        |
        | consumer: P1 / P2 / P4 / P5
```

## 3. Schema MANIFEST.json

```json
{
  "scenario": "alice_100_40",
  "description": "Alice 100/40 canonical scenario: ...",
  "vectorVersion": "v0",
  "hashAlgorithm": "sha256",
  "generator": "p3/script-test/gen_state_vectors",
  "alice":       { "address": "cosmos1alice", "denom": "uusdc", ... },
  "roots":       { "rootA": "0x...", "rootB": "0x...", "rootC": "0x..." },
  "domainTags":  { "nullifier": "zkdex/nullifier/v0", ... },
  "files": [
    {
      "name": "settlement_update_batch_1.json",
      "stateTask": "STATE-08",
      "schema": "types.SettlementUpdate",
      "consumers": ["P1","P2","P4","P5"],
      "sha256": "0x2c473d957a08de63813fa7f4d8d9bcb6...",
      "note": "Batch-shaped: deposits[] + withdrawals[]."
    },
    ...
  ]
}
```

### Tại sao SHA-256 (không phải Poseidon)?

MVP placeholder. `internal/batch/commitments.go` cũng dùng SHA-256 cho commitment roots. ZK-02 chốt hash circuit (Poseidon/MiMC) sẽ:

1. Bump `gen_state_vectors::vectorVersion` v0 → v1.
2. Bump `pkg/testvectors::ExpectedVectorVersion` v0 → v1.
3. Regenerate folder.

`TestManifest_VersionPinned` fail nếu chỉ bump 1 trong 2 → catch quên đồng bộ.

## 4. Solver path: làm sao caller tìm folder?

```go
// pkg/testvectors/manifest.go
func FindRepoRoot() (string, error) {
    _, file, _, _ := runtime.Caller(0)            // -> .../pkg/testvectors/manifest.go
    dir := filepath.Dir(file)
    for {
        if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
            return dir, nil                       // -> repo root
        }
        parent := filepath.Dir(dir)
        if parent == dir { return "", err }
        dir = parent
    }
}
```

Lý do không dùng `os.Getwd()`: test invocation đổi cwd. `go test ./tests/...` cwd = `tests/`, `go test ./...` cwd = repo root. Caller(0) bám vào file source → đi lên `go.mod` → ổn định.

## 5. VerifyManifest — 3 dạng drift detection

```go
func VerifyManifest(m *Manifest) error {
    // 1. Mỗi entry trong manifest → file phải tồn tại + SHA khớp.
    for _, f := range m.Files {
        got, _ := hashFile(filepath.Join(dir, f.Name))
        if got != f.SHA256 { return ErrManifestMismatch }
    }
    // 2. Mỗi file trên đĩa → phải có entry manifest (orphan check).
    //    Trừ MANIFEST.json + README.md + file ẩn.
    for _, e := range os.ReadDir(dir) {
        if !expected[e.Name()] && !skipList[e.Name()] {
            orphans = append(orphans, e.Name())
        }
    }
    if len(orphans) > 0 { return ErrManifestMismatch }
}
```

Tại sao orphan check quan trọng: ai đó copy `settlement_update_batch_2.json` vào folder mà không update MANIFEST → consumer load sẽ không biết, nhưng nếu họ tự liệt kê file thì sẽ thấy file lạ → confusion. Orphan check kéo tất cả về MANIFEST.

## 6. Hai cấp invariant check

### Cấp 1 — `VerifyManifest`

Byte-level. SHA-256 của nội dung file (gồm trailing newline) phải khớp manifest entry. Bắt: edit tay JSON, file missing, file orphan.

### Cấp 2 — `AliceScenario.SanityCheck`

Semantic. Cross-file relationship:

| # | Invariant | Lý do |
|---|---|---|
| 1 | `SettlementUpdate.OldStateRoot == StateAfterDeposit.Root` (rootB) | Settlement bắt đầu từ post-deposit state |
| 2 | `SettlementUpdate.NewStateRoot == StateAfterWithdrawal.Root` (rootC) | Settlement kết thúc tại post-withdraw state |
| 3 | `InitialState.Root != rootB && != rootC` | rootA phải distinct |
| 4 | `len(PublicInputs)==6 && publicInputs[0..5]` khớp `(rootB, rootC, 4 commitment roots)` | STATE-10 layout |
| 5 | `Witness.Accounts[0]: oldBalance=0, newBalance=60, nonce=1` | Alice journey |
| 6 | `WithdrawRequest.Nonce == StateAfterWithdrawal.Accounts[0].Nonce` | Nonce consistency |
| 7 | `SettlementUpdate.Withdrawals[0].Nullifier == Nullifier.Nullifier` | Đúng nullifier được bind vào settlement |
| 8 | `SettlementUpdate.Withdrawals[0].DestinationHash == DestinationHash.DestinationHash` | Đúng dest hash |
| 9 | `Deposit.Amount=="100" && WithdrawRequest.Amount=="40"` | Lock scenario name |

Cấp 2 bắt trường hợp generator có bug semantic mà cấp 1 không thấy (vd. generator output file đúng SHA nhưng output Withdrawals[0].Nullifier sai do bug logic).

## 7. AliceScenario — typed snapshot

```go
type AliceScenario struct {
    Manifest             *Manifest
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
```

Consumer one-shot:

```go
s := testvectors.MustLoadAliceScenario()
proof := prover.Prove(s.SettlementUpdate, s.BatchCommitments, s.Witness)
verifier.Verify(s.PublicInputs.PublicInputs, proof)
```

Granular per-file (khi chỉ cần 1-2 file):

```go
upd, _ := testvectors.LoadSettlementUpdate()
pi,  _ := testvectors.LoadPublicInputs()
```

## 8. Distinction: static "alice_secret" vs runtime "mock-user-secret"

Đây là **invariant có chủ đích** — cả 2 literal cùng tồn tại trong codebase:

| Trường | Static folder (file này) | Runtime mock (P4 BatchService) |
|---|---|---|
| `userSecret` | `"alice_secret"` | `"mock-user-secret"` |
| Nullifier | `0x1a1fdf4c…22f7` (lock trong test) | Tính runtime từ `H(domain \| "mock-user-secret" \| "1")` |
| File source | `witness_batch_1.json` + `nullifier_wd_1.json` | `internal/batch/local_builder.go::mvpMockSecret` |
| Lock bởi test | `internal/state/nullifier_test.go::TestNullifierFor_CanonicalAliceVector` | `tests/int02_mock_endpoints_test.go` |
| Khi nào thống nhất | Khi P4 wire keystore/wallet thật và xoá fallback `mvpMockSecret`; hai luồng dùng cùng secret từ wallet | Cùng lúc |

`TestAlice_StaticVsRuntimeSecret` lock invariant này. Nếu ai đó đổi `aliceSecret` trong `gen_state_vectors` thành `"mock-user-secret"` để "thống nhất", test này fail + test nullifier_test cũng fail.

## 9. Vòng đời file

```
+---------+
| Dev     |
| muốn    |
| đổi vec |
+----+----+
     |
     v
+---------+
| Sửa     |
| primit. |
| state/  |
| batch   |
+----+----+
     |
     v
+----------+
| Chạy     |
| generator|
+----+-----+
     |
     v
+----------+
| File JSON|
| + MANI-  |
| FEST     |
| ghi đè   |
+----+-----+
     |
     v
+----------+
| go test  |
| ./pkg/   |
| testvec. |
+----+-----+
     |
     +----- PASS ---> commit
     |
     +----- FAIL ---> debug (SHA mismatch / semantic invariant / version pin)
```

**Quy ước**: KHÔNG bao giờ edit tay file trong `testvectors/alice_100_40/*.json`. Mọi thay đổi phải qua `gen_state_vectors`. Manifest SHA-256 enforce điều này tự động.

## 10. Tác động cross-role

### P1 (verifier on-chain)

```go
m, _ := testvectors.LoadManifest()
// m.DomainTags cho biết hash function on-chain phải dùng zkdex/nullifier/v0
expected, _ := testvectors.LoadPublicInputs()
// expected.PublicInputs là slice 6 hex string verifier reconstruct lại.
```

### P2 (prover/circuit)

```go
ws, _  := testvectors.LoadWitness()
upd, _ := testvectors.LoadSettlementUpdate()
// Generate proof. Public inputs sẽ khớp testvectors.LoadPublicInputs().
proof := prover.Prove(upd, ws, provingKey)
```

### P4 (backend) — optional refactor

Hiện `tests/int07_batch_builder_test.go` lock literal hex `"0xrootA"`. Có thể refactor sang `testvectors.MustLoadAliceScenario().StateAfterDeposit.Root` để self-document hơn. Non-breaking.

### P5 (frontend)

Endpoint `GET /api/state` có thể seed initial state từ `manifest.Roots.RootA`. UI Overview screen có thể đọc `manifest.Alice` cho "Alice address: cosmos1alice, balance: 1000 uusdc" text.

## 11. Việc còn lại

| Task | Owner | Trigger |
|---|---|---|
| STATE-12 negative vectors | P3 | Sau STATE-11 stable |
| Bump v0→v1 khi ZK-02 chốt hash | P2+P3 | Khi circuit ready |
| Xoá `mvpMockSecret` khi P4 wire wallet | P4 | Khi P5 wallet SDK |
| Refactor tests/int* dùng pkg/testvectors | P4 | Optional cleanup |
