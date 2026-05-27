# STATE-12 — Failure vectors (negative test vectors)

> Tài liệu triển khai chi tiết cho task **STATE-12** thuộc P3 — Off-chain state / batch builder.
>
> Pre-req: STATE-02..11 + STATE-13 đã ổn định. Xem changenote `p3/changenotes/2026-05-26-state-12.md` cho lịch sử quyết định.

## 1. Mục tiêu

STATE-12 emit **4 negative test vector canonical** cho luồng Alice 100/40 — mỗi vector là một input mà toàn bộ pipeline (P3 builder → P2 prover → P1 chain verifier) PHẢI reject ở stage chỉ định. Mục đích:

1. **P1 ONCHAIN-12** (failure tests) có vector dùng được ngay — không phải tự bịa số.
2. **P2 ZK-12** (negative tests) có vector cho ZK-05 (nullifier binding) và ZK-07 (destination binding) — đo lường circuit có "bắt" tampering không.
3. **P4 INT-13** (failure demo endpoints/scripts) có payload chạy thử trên backend stub trước khi chain ready.
4. **P5 FE-10** (failure demo panel) có dữ liệu literal để hiển thị message reject cho người dùng.

4 case bám theo Edge cases panel của tài liệu `zkdex_cosmos_deposit_withdraw_flows.html`:

| # | Case | Stage reject (canonical) | Invariant bị vi phạm |
|---|---|---|---|
| 1 | `over_withdraw` | STATE-04 `WithdrawRequestBuilder` | balance ≥ amount |
| 2 | `wrong_root` | P1 chain `MsgSubmitBatchProof` (ONCHAIN-08) | `OldStateRoot == currentStateRoot` |
| 3 | `duplicate_nullifier` | P1 chain dup check (hoặc STATE-05) | nullifier chỉ dùng 1 lần |
| 4 | `tampered_destination` | P2 ZK-07 (hoặc P1 recompute) | `destinationHash == H(destination)` |

## 2. Folder layout

```
testvectors/alice_100_40/
├── MANIFEST.json                    ← STATE-11 happy path manifest (11 files)
├── README.md
├── initial_state.json               ← STATE-02
├── deposit_dep_1.json               ← STATE-03 (input)
├── ... (8 file happy path khác)
└── failure_vectors/                 ← STATE-12 (subfolder mới)
    ├── MANIFEST.json                ← failure manifest riêng (4 files + happyPathRoots)
    ├── README.md
    ├── over_withdraw.json
    ├── wrong_root.json
    ├── duplicate_nullifier.json
    └── tampered_destination.json
```

**Quyết định:** subfolder + manifest **RIÊNG** (không nhét vào manifest cha). Ưu điểm:

- `vectorVersion` có thể bump độc lập (vd. format failure thay đổi nhưng happy path không).
- `VerifyManifest` cha tự skip subdirectory → 0 thay đổi code happy path.
- `TestManifest_AllFilesCovered` của happy path vẫn assert đúng 11 file (failure không leak vào count).
- Cross-link `HappyPathRoots` trong failure manifest đảm bảo 2 set không drift độc lập (test `TestFailureManifest_HappyPathRootsMatch` bắt drift).

## 3. Schema

### 3.1. Common header — `FailureMeta`

```go
type FailureMeta struct {
    Case               string           `json:"case"`
    Title              string           `json:"title"`
    Description        string           `json:"description"`
    ViolatedInvariant  string           `json:"violatedInvariant"`
    HappyPathReference string           `json:"happyPathReference"`
    Mutation           string           `json:"mutation"`
    Rejection          FailureRejection `json:"rejection"`
    Consumers          []string         `json:"consumers"`
}

type FailureRejection struct {
    Stage           string `json:"stage"`           // FailureStage* label
    Sentinel        string `json:"sentinel"`        // Go error sentinel name
    MessageContains string `json:"messageContains"` // substring expected trong err.Error()
    Note            string `json:"note,omitempty"`
}
```

Mọi vector struct **embed** `FailureMeta` — không duplicate field declaration.

### 3.2. Per-case payload

| Case | Payload (ngoài FailureMeta) |
|---|---|
| `OverWithdrawVector` | `AccountSnapshot types.Account` + `Intent WithdrawIntentVector` |
| `WrongRootVector` | `CorrectOldStateRoot string` + `TamperedSettlement types.SettlementUpdate` + `BatchCommitments` + `PublicInputs []string` |
| `DuplicateNullifierVector` | `Nullifier string` + `SettlementUpdate` (chứa ≥2 withdrawals cùng nullifier) + `BatchCommitments` + `PublicInputs` |
| `TamperedDestinationVector` | `OriginalDestination/TamperedDestination/OriginalDestinationHash string` + `TamperedSettlement` + `BatchCommitments` + `PublicInputs` |

## 4. Cách hàm hoạt động

### 4.1. `pkg/testvectors/failure_vectors.go`

#### `LoadFailureBundle() (*FailureBundle, error)`

```
1. LoadFailureManifest()                  → đọc + parse MANIFEST.json subfolder
2. loadFailureJSON(FileOverWithdraw, ...)  → 4 lần, mỗi case 1 lần
3. discriminatorCheck()                    → đảm bảo file/content khớp `case` field
4. return bundle
```

Failure → return err với context filename. Caller test gọi `MustLoadFailureBundle()` để panic-on-fail.

#### `VerifyFailureManifest(m *FailureManifest) error`

Logic giống `VerifyManifest` cha:

1. Mỗi file trong manifest: re-hash SHA-256, đối chiếu với entry.
2. Mỗi file trên đĩa (trừ `MANIFEST.json` và `README.md`): có entry không, nếu không → orphan.
3. Mismatch → wrap `ErrFailureManifestMismatch`. Caller chain bằng `errors.Is`.

#### `FailureBundle.SanityCheck() error`

Tầng 2 invariant — bắt generator bug semantic mà SHA-256 (tầng 1) không thấy:

| Invariant | Check |
|---|---|
| Discriminator | mỗi vector mang đúng `case` field (FailureCase*) |
| over_withdraw | `AccountSnapshot.Balance < Intent.Amount`; owner/denom khớp |
| wrong_root | `TamperedSettlement.OldStateRoot != CorrectOldStateRoot` |
| duplicate_nullifier | ≥2 withdrawal entry trong `SettlementUpdate.Withdrawals[]` mang cùng `Nullifier` |
| tampered_destination | settlement `Destination = TamperedDestination` (khác `OriginalDestination`), `DestinationHash` giữ `OriginalDestinationHash` |
| publicInputs | length = 6 cho mọi vector có PublicInputs (wrong_root / duplicate_nullifier / tampered_destination) |

#### Loaders per-case

```go
LoadOverWithdraw() (OverWithdrawVector, error)
LoadWrongRoot() (WrongRootVector, error)
LoadDuplicateNullifier() (DuplicateNullifierVector, error)
LoadTamperedDestination() (TamperedDestinationVector, error)
```

Mỗi hàm wrap `loadFailureJSON(file, &v)`. Caller stateless dùng được mà không phải load cả bundle.

### 4.2. `p3/script-test/gen_state_vectors/failure_vectors.go` (package main)

#### `emitFailureVectors(scenarioDir string, hp happyPathArtifacts)`

Entry point được `main.go` gọi sau khi happy path đã ghi xong:

```
1. mkdir <scenarioDir>/failure_vectors
2. w := newFailureWriter(...)
3. emitOverWithdraw(w, hp)
4. emitWrongRoot(w, hp)
5. emitDuplicateNullifier(w, hp)
6. emitTamperedDestination(w, hp)
7. w.emitManifest(...)  → sort files alphabetical, ghi MANIFEST.json
```

`happyPathArtifacts` là struct gói data canonical mà generator chính đã dựng — tránh truyền 10 tham số. Bao gồm: 3 root, `dep1`, `wdReq`, `nullifier`, `destinationHash`, `upd`, `commitments`.

#### `emitOverWithdraw`

```go
ls := state.NewLocalState()
ls.ApplyDeposit(hp.Deposit)              // balance Alice = 100
acc := ls.Account(...)                   // snapshot tại moment intent submit

vec := OverWithdrawVector{
    FailureMeta: FailureMeta{...stage = STATE-04 builder, sentinel = ErrInsufficientBalance...},
    AccountSnapshot: acc,
    Intent: WithdrawIntentVector{Amount: "200", ...},
}
```

`AccountSnapshot` lấy từ LocalState **fresh** (re-build) chứ không phải LocalState đã xử lý happy path (đã advance qua rootC). Lý do: moment realistic là post-deposit/pre-withdraw — đúng moment mà P4 BatchService nhận intent.

#### `emitWrongRoot`

```go
tampered := cloneSettlement(hp.Settlement)
tampered.OldStateRoot = "0x" + sha256("STATE-12/wrong_root/oldStateRoot")  // deterministic

com := batch.BuildCommitments(tampered)
pi, _ := batch.BuildPublicInputs(tampered, com)

// vec.CorrectOldStateRoot = hp.RootB (để test driver init chain ở rootB)
// vec.TamperedSettlement, BatchCommitments, PublicInputs đầy đủ
```

`OldStateRoot` placeholder là SHA-256 của một const string → mỗi lần regenerate ra cùng giá trị → SHA-256 file ổn định → manifest không drift mỗi run.

Note: BatchCommitments thực ra **không phụ thuộc** OldStateRoot (chỉ phụ thuộc Deposits[]/Withdrawals[]). Vector vẫn re-derive đầy đủ để caller test không phải tính lại.

#### `emitDuplicateNullifier`

```go
tampered := cloneSettlement(hp.Settlement)
replay := tampered.Withdrawals[0]   // shallow copy struct
replay.WithdrawID = "wd-2-replay"
replay.Amount = "20"
// nullifier + destinationHash giữ y hệt wd-1
tampered.Withdrawals = append(tampered.Withdrawals, replay)
```

Intra-batch replay: cùng SettlementUpdate có 2 entry chia sẻ `nullifier`. Test driver có 2 lựa chọn:

- **Chain dup check**: submit MsgSubmitBatchProof, expect ONCHAIN-08 reject ngay (chưa đụng VerifyProof).
- **LocalState.ApplyWithdrawal**: apply 2 lần với cùng nullifier → lần 2 reject với `state.ErrWithdrawAlreadyApplied` (test `TestDuplicateNullifier_StateApplyRejectsReplay` làm điều này).

#### `emitTamperedDestination`

```go
const attackerAddr = "cosmos1attacker0000000000000000000000xxxxx"

tampered := cloneSettlement(hp.Settlement)
tampered.Withdrawals[0].Destination = attackerAddr
// DestinationHash giữ nguyên = hash("cosmos1alice")
```

Verifier nào (P1 on-chain hoặc P2 circuit) phải recompute `H(destination)` và so với field `DestinationHash` → mismatch → reject. Test `TestTamperedDestination_HashDoesNotMatchRecompute` chứng minh: `state.WithdrawAddressHash(attackerAddr) != OriginalDestinationHash`.

#### `cloneSettlement(src) SettlementUpdate`

Deep-copy: copy struct + alloc lại `Deposits` và `Withdrawals` slice. Bắt buộc vì 4 emitter chia sẻ cùng `hp.Settlement` — nếu shared slice, các vector mutate nhau (bug: vector wrong_root append withdrawals[1] sẽ rò sang vector tampered_destination).

### 4.3. `main.go` wiring

```go
// Sau khi happy path đã emit + manifest ghi xong:
emitFailureVectors(outDir, happyPathArtifacts{
    RootA: rootA, RootB: rootB, RootC: rootC,
    Deposit: dep1, WithdrawRequest: wdReq,
    Nullifier: nullifier, DestinationHash: destinationHash,
    Settlement: upd, Commitments: commitments,
})
```

Generator giờ idempotent cho cả happy + failure: mỗi lần `go run` overwrite 11 + 5 file (4 vector + 1 failure manifest).

## 5. Test coverage

### 5.1. Manifest tests (6 test)

| Test | Mục đích |
|---|---|
| `TestFailureManifest_LoadAndScope` | Load + assert scenario name / subfolder slug. |
| `TestFailureManifest_VersionPinned` | `vectorVersion == FailureExpectedVectorVersion`. |
| `TestFailureManifest_VerifyIntegrity` | SHA-256 re-hash khớp manifest. |
| `TestFailureManifest_AllFilesCovered` | 4 file const đều có entry. |
| `TestFailureManifest_HappyPathRootsMatch` | `failure.HappyPathRoots == happy.Roots` (cross-manifest invariant). |
| `TestFailureManifest_VerifyErrorWrap` | Đột biến SHA → `errors.Is(err, ErrFailureManifestMismatch)`. |

### 5.2. Bundle + semantic invariant test (1 test)

| Test | Mục đích |
|---|---|
| `TestFailureBundle_LoadAndSanity` | `LoadFailureBundle()` + `SanityCheck()` xanh. |

### 5.3. Per-case behavioral test (4 test)

| Test | Pipeline chạy thử | Sentinel expected |
|---|---|---|
| `TestOverWithdraw_RejectedByState04` | `LocalState.ApplyDeposit` → `WithdrawRequestBuilder.Build(intent.Amount=200)` | `state.ErrInsufficientBalance` |
| `TestOverWithdraw_RejectedByBatchLocalBuilder` | `batch.LocalBuilder.Build` với 1 forged withdraw amount=200 | `batch.ErrInsufficientOffchainBalance` |
| `TestWrongRoot_PublicInputsConsistent` | Đối chiếu `vec.PublicInputs[0] == TamperedSettlement.OldStateRoot` + re-derive BatchCommitments | — (vector self-consistency) |
| `TestDuplicateNullifier_StateApplyRejectsReplay` | `ApplyDeposit` → `ApplyWithdrawal` (wd-1) → `ApplyWithdrawal` (wd-2-replay cùng nullifier) | `state.ErrWithdrawAlreadyApplied` |
| `TestTamperedDestination_HashDoesNotMatchRecompute` | `state.WithdrawAddressHash(TamperedDestination)` ≠ `OriginalDestinationHash` | — (semantic invariant) |

Tổng cộng 11 test mới — full suite vẫn xanh.

## 6. Pipeline khi consumer dùng

### 6.1. P1 ONCHAIN-12 (failure tests)

```go
// Test "stale state root reject"
v, _ := testvectors.LoadWrongRoot()
chain := NewLocalChain(WithGenesisStateRoot(v.CorrectOldStateRoot))
proofBundle := mockProofBundle(v.PublicInputs)
res, err := chain.SubmitTx(MsgSubmitBatchProof{
    SettlementUpdate: v.TamperedSettlement,
    ProofBundle:      proofBundle,
})
require.Error(t, err)
require.Contains(t, err.Error(), v.Rejection.MessageContains) // "oldStateRoot"
```

### 6.2. P2 ZK-12 (negative tests)

```go
// Test "tampered destination — proof fail"
v, _ := testvectors.LoadTamperedDestination()
// Witness vẫn dùng original destination (vì circuit recompute hash từ witness)
// → public DestinationHash != H(witness destination) → constraint fail
proof := prover.Prove(v.TamperedSettlement, witness)
require.False(t, verifier.Verify(proof, v.PublicInputs))
```

### 6.3. P4 INT-13 (failure demo endpoints)

```go
// POST /api/withdraw-request với amount > balance → HTTP 400 sạch
v, _ := testvectors.LoadOverWithdraw()
resp := httptest.PostJSON("/api/withdraw-request", v.Intent)
require.Equal(t, 400, resp.StatusCode)
require.Contains(t, resp.Body.Error, "insufficient")
```

### 6.4. P5 FE-10 (failure demo panel)

Load `failure_vectors/over_withdraw.json` để hiển thị:

- Title chip: "Over-withdraw — Alice cố rút vượt balance"
- Tooltip: vector.Description
- Expected outcome chip: vector.Rejection.Stage + vector.Rejection.Sentinel

## 7. Versioning & maintenance

Khi ZK-02 chốt Poseidon/MiMC (thay SHA-256 placeholder):

1. Bump `vectorVersion` v0 → v1 trong `gen_state_vectors/main.go`.
2. Bump `ExpectedVectorVersion` + `FailureExpectedVectorVersion` trong `pkg/testvectors/manifest.go` và `pkg/testvectors/failure_vectors.go`.
3. Chạy `go run ./p3/script-test/gen_state_vectors` → regenerate cả 2 manifest.
4. `go test ./pkg/testvectors/...` PHẢI xanh — bao gồm `HappyPathRootsMatch`.

Khi thêm case mới (vd. `claim_twice.json`, `invalid_proof.json`):

1. Thêm `FailureCase*` const + filename const + struct + `Load*` function.
2. Thêm emitter + register trong `emitFailureVectors`.
3. Mở rộng `SanityCheck` với invariant của case mới.
4. Cập nhật `TestFailureManifest_AllFilesCovered` filename list.
5. Thêm 1-2 behavioral test cho case mới (pattern: setup pipeline → assert sentinel).

## 8. Tóm tắt thay đổi file

| File | Loại | Mô tả |
|---|---|---|
| `pkg/testvectors/failure_vectors.go` | mới | Schema + loaders + verify + SanityCheck. |
| `pkg/testvectors/failure_vectors_test.go` | mới | 11 test. |
| `p3/script-test/gen_state_vectors/failure_vectors.go` | mới | Generator extension (package main). |
| `p3/script-test/gen_state_vectors/main.go` | sửa | Gọi `emitFailureVectors(...)` sau happy path. |
| `testvectors/alice_100_40/failure_vectors/MANIFEST.json` | mới | Manifest subfolder + happyPathRoots. |
| `testvectors/alice_100_40/failure_vectors/*.json` (4 file) | mới | Vectors. |
| `testvectors/alice_100_40/failure_vectors/README.md` | mới | Vietnamese walkthrough. |
| `testvectors/alice_100_40/README.md` | sửa | Cập nhật block STATE-12 (chưa có → đã có). |
| `p3/changenotes/2026-05-26-state-12.md` | mới | Changenote. |
| `p3/docs/STATE12_document.md` | mới | This document. |
