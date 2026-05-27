# Failure vectors — `alice_100_40/failure_vectors/` (STATE-12)

Thư mục này chứa **4 negative test vector canonical** ăn theo happy path `alice_100_40`. Mỗi file là một input mà toàn bộ pipeline (P3 builder → P2 prover → P1 chain verifier) **PHẢI** reject ở stage chỉ định. Tách khỏi happy-path folder cha để consumer KHÔNG vô tình mix một failure case vào pipeline xanh.

## Bốn case canonical (theo Edge cases panel của `zkdex_cosmos_deposit_withdraw_flows.html`)

| File | Case | Stage reject | Sentinel / Tín hiệu | Consumer |
|---|---|---|---|---|
| `over_withdraw.json` | `over_withdraw` | STATE-04 `WithdrawRequestBuilder` (hoặc STATE-05 `ApplyWithdrawal`) | `state.ErrInsufficientBalance` | P3, P4 |
| `wrong_root.json` | `wrong_root` | P1 `x/zkdex MsgSubmitBatchProof` (ONCHAIN-08) | `ErrStaleStateRoot` (chain) | P1, P4 |
| `duplicate_nullifier.json` | `duplicate_nullifier` | P1 chain dup check (hoặc STATE-05 `ApplyWithdrawal`) | `ErrNullifierReplay` / `state.ErrWithdrawAlreadyApplied` | P1, P3, P4 |
| `tampered_destination.json` | `tampered_destination` | P2 ZK circuit binding (ZK-07) hoặc P1 verifier recompute `H(destination)` | mismatch `destinationHash != H(destination)` | P1, P2, P4 |

## Schema chung

Mỗi vector mở đầu bằng `FailureMeta`:

```json
{
  "case": "over_withdraw",
  "title": "...",
  "description": "...",
  "violatedInvariant": "...",
  "happyPathReference": "alice_100_40",
  "mutation": "...",
  "rejection": {
    "stage": "STATE-04 WithdrawRequestBuilder",
    "sentinel": "state.ErrInsufficientBalance",
    "messageContains": "insufficient balance",
    "note": "..."
  },
  "consumers": ["P3", "P4"]
}
```

Sau header là payload tuỳ case — xem `pkg/testvectors/failure_vectors.go` cho struct chi tiết.

## Sử dụng từ Go

```go
import "github.com/zhenjb/ganc-sys/pkg/testvectors"

// One-shot: load cả bundle
b, err := testvectors.LoadFailureBundle()
if err := b.SanityCheck(); err != nil {
    t.Fatal(err)
}

// Hoặc load từng case
ov, _ := testvectors.LoadOverWithdraw()
wr, _ := testvectors.LoadWrongRoot()
du, _ := testvectors.LoadDuplicateNullifier()
td, _ := testvectors.LoadTamperedDestination()
```

Mỗi struct có đầy đủ payload mà pipeline cần (Intent / SettlementUpdate / BatchCommitments / PublicInputs / hash) — caller chỉ cần feed vào entry point của role mình test.

## Pattern test mẫu (xem `failure_vectors_test.go`)

```go
// over_withdraw — STATE-04 reject
v, _ := testvectors.LoadOverWithdraw()
ls := state.NewLocalState()
_, _ = ls.ApplyDeposit(canonicalDeposit)
wb := state.NewWithdrawRequestBuilder(ls)
_, err := wb.Build(state.WithdrawIntent{...v.Intent...})
require.ErrorIs(t, err, state.ErrInsufficientBalance)

// tampered_destination — H(tampered) != OriginalDestinationHash
v, _ := testvectors.LoadTamperedDestination()
rehash, _ := state.WithdrawAddressHash(v.TamperedDestination)
require.NotEqual(t, v.OriginalDestinationHash, rehash)
```

## Regenerate

```bash
go run ./p3/script-test/gen_state_vectors
```

Generator emit cả happy path + subfolder `failure_vectors/` cùng lúc. Sau khi regenerate:

```bash
go test ./pkg/testvectors/...
```

PHẢI xanh — bao gồm cả `TestFailureManifest_HappyPathRootsMatch` (3 root canonical khớp giữa 2 manifest).

## Tại sao 4 case này, không phải 5+?

Plan v2 (`zkdex_final_parallel_plan_batch_contracts_v2.html` — bảng P3) liệt kê đúng 4 case: over-withdraw, wrong root, duplicate nullifier, tampered destination. Case "invalid proof" và "claim twice" thuộc về P1/P2 (chain reject ở proof verify hoặc claim handler) — không cần off-chain vector để demo.

Nếu sau này thêm case (vd. nonce skip, denom mismatch), bump `vectorVersion` v0 → v1 và:

1. Thêm `FailureCase*` const + filename const + struct + `Load*` function trong `pkg/testvectors/failure_vectors.go`.
2. Thêm emitter trong `p3/script-test/gen_state_vectors/failure_vectors.go`.
3. Mở rộng `SanityCheck` để cover invariant của case mới.
4. Cập nhật `TestFailureManifest_AllFilesCovered` với filename mới.

## Failure invariants vs happy path determinism

- Happy path manifest (`alice_100_40/MANIFEST.json`) chốt SHA-256 từng vector → bất kỳ hand-edit nào break test xanh.
- Failure manifest (`alice_100_40/failure_vectors/MANIFEST.json`) chốt SHA-256 từng failure vector — đồng thời `HappyPathRoots` ghim cả 3 root rootA/rootB/rootC để 2 set không drift độc lập.

Khi ZK-02 bump hash circuit từ SHA-256 sang Poseidon/MiMC, cả 2 manifest đều phải bump `vectorVersion` v0 → v1 và regenerate.
