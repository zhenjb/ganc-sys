# P3 — STATE-06: Compute Nullifier

## 1. Nhiệm vụ

STATE-06 nhận `(userSecret, nonce)` và trả về `nullifier` deterministic, dùng làm:

1. **Public input** của ZK proof (P2 prover + P1 verifier).
2. **Idempotency key** cho `LocalState.ApplyWithdrawal` (STATE-05).
3. **Replay-protection key** ghi vào on-chain map `nullifierUsed[nullifier]` (P1 ONCHAIN-09).

Output là `0x`-prefixed lowercase hex (theo Agreements: "Roots/nullifiers/proofs are hex strings").

## 2. Luồng hoạt động

```
STATE-04 (build WithdrawRequest) ──► nonce ──┐
                                              ├──► STATE-06 ──► nullifier ─┬─► STATE-05 ApplyWithdrawal
userSecret (wallet / test vector) ────────────┘                            ├─► STATE-09 witness builder (P2)
                                                                           └─► STATE-08 SettlementUpdate (publicInput)
```

Trong backend:

```
P4 nhận POST /api/withdraw-request
       │
       ▼
STATE-04 build → req
       │
       ▼
STATE-06 derive → nullifier
       │
       ├──► STATE-05 ApplyWithdrawal(req, nullifier)
       │            │
       │            ▼
       │       rootC + appliedNullifiers[nullifier]
       │
       └──► STATE-08 SettlementUpdate{nullifier, ...}
                    │
                    ▼
              P2 prover → proofBundle
                    │
                    ▼
              P1 MsgSubmitBatchProof
                    │
                    ▼
              nullifierUsed[nullifier] = true
```

## 3. Hàm chính

### 3.1. `NullifierFor(userSecret, nonce string) (string, error)`

Pure function. Không state, không mutex, không I/O. Thực thi:

1. **Trim + validate userSecret**: non-empty.
2. **Parse nonce**: phải là non-negative integer string. Reject `"-1"`, `"abc"`, `"0x1"`, `"1.0"`.
3. **Canonicalize nonce**: `parseNonNegativeAmount(nonce).String()` → `"01"` → `"1"`.
4. **Build hash input**: `nullifierDomainTag + "|" + userSecret + "|" + canonicalNonce`.
5. **Hash**: `SHA256Hex(...)` từ `pkg/hash` → trả `"0x"` + lowercase hex 64 ký tự.

Composition công thức:

```
nullifier = SHA256( "zkdex/nullifier/v0" | userSecret | canonical(nonce) )
```

### 3.2. `NullifierDomainTag() string`

Accessor cho domain tag hiện hành. P2 (circuit assert), P4 (sanity check trước khi submit) dùng để verify off-chain derivation chưa silently bump version.

### 3.3. Sentinel error

| Sentinel | Khi nào | HTTP map |
|---|---|---|
| `ErrInvalidNullifierInput` | userSecret empty / nonce empty / nonce non-negative-integer | 400 |

## 4. Quyết định thiết kế

### 4.1. Vì sao SHA-256 thay vì Poseidon ngay?

- `pkg/hash` đã có SHA-256, không kéo thêm dependency cho MVP.
- ZK-02 chưa lock circuit hash — chốt sớm sẽ bị rework khi đổi stack.
- Domain tag versioned (`v0`) là escape hatch: khi swap Poseidon chỉ cần bump `v1` và regenerate vector.

### 4.2. Vì sao có domain tag?

Để **domain-separate** hash này với các SHA-256 callsite khác trong hệ thống:

| Callsite | Input | Mục đích |
|---|---|---|
| `NullifierFor` | `"zkdex/nullifier/v0\|secret\|nonce"` | Nullifier off-chain |
| `mockTxHash` (P4) | `"deposit\|owner\|denom\|amount\|depId"` | Mock tx hash |
| `ComputeRoot` (root.go) | account state bytes | State root |

Không có domain tag → các bytes input có thể trùng → nullifier collide với hash khác → attacker submit fake nullifier.

### 4.3. Vì sao canonicalize nonce?

Attacker pattern (nếu KHÔNG canonicalize):

1. Wallet apply withdraw với `req.Nonce="1"`, nullifier `H("v0|s|1")` được mark applied.
2. Attacker forward `req.Nonce="01"` cùng pre-computed nullifier `H("v0|s|01")` (giá trị khác).
3. STATE-05 `appliedNullifiers["H(v0|s|01)"]` chưa có → **bypass idempotency**.

Canonicalize `"01"` → `"1"` trước khi hash → cả hai input collapse cùng nullifier → STATE-05 reject replay.

Test `TestNullifierFor_NonceCanonicalization` cover.

### 4.4. Vì sao tách STATE-06 ra hàm pure thay vì gắn vào LocalState?

- `types.WithdrawRequest` không chứa `userSecret` (secret không bao giờ lên chain/API).
- LocalState không biết secret của user.
- Pure function → unit test cô lập, không cần seed LocalState.
- Caller pattern rõ:
  ```go
  nullifier, _ := state.NullifierFor(secret, req.Nonce)
  ls.ApplyWithdrawal(req, nullifier)
  ```

### 4.5. Vì sao nonce=0 hợp lệ ở STATE-06 nhưng STATE-05 reject?

**Single responsibility**:

- STATE-06: pure hash. Validate shape input, không validate business rule.
- STATE-05: business rule. Reject `req.Nonce != account.Nonce + 1`.

Sau deposit Alice có `Account.Nonce = 0`. WithdrawRequest hợp lệ phải có `Nonce="1"`. Nếu attacker submit `Nonce="0"`:

- STATE-06 `NullifierFor(secret, "0")` → trả nullifier hợp lệ (function pure không biết account state).
- STATE-05 `ApplyWithdrawal(req, nullifier)` → `ErrNonceMismatch` vì expected=1.

Tách layer giúp:

- Test STATE-06 độc lập (không cần seed LocalState).
- Tránh circular dependency `NullifierFor → LocalState`.
- P4 có thể derive nullifier để hash-check trước khi call STATE-05.

## 5. Ví dụ

### 5.1. Happy path

```go
nullifier, err := state.NullifierFor("alice_secret", "1")
// nullifier == "0x1a1fdf4ccecb7040b7cd7e125226d14ed7717618d996dab969d4cb12550b22f7"
// err == nil
```

### 5.2. Drive idempotency

```go
ls := state.NewLocalState()
ls.ApplyDeposit(types.DepositRecord{DepositID: "dep-1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "100"})

wb := state.NewWithdrawRequestBuilder(ls)
req, _ := wb.Build(state.WithdrawIntent{Owner: "cosmos1alice", Denom: "uusdc", Amount: "40", Destination: "cosmos1alice"})

nullifier, _ := state.NullifierFor("alice_secret", req.Nonce)  // "0x1a1fdf…22f7"

ls.ApplyWithdrawal(req, nullifier)  // OK, balance 60, nonce 1, nullifier marked

// Replay:
again, _ := state.NullifierFor("alice_secret", req.Nonce)
// again == nullifier (deterministic)

ls.ApplyWithdrawal(req, again)
// returns ErrWithdrawAlreadyApplied
```

### 5.3. Failure modes

| Input | Kết quả |
|---|---|
| `("", "1")` | `ErrInvalidNullifierInput: userSecret is empty` |
| `("   ", "1")` | `ErrInvalidNullifierInput: userSecret is empty` (trim) |
| `("alice_secret", "")` | `ErrInvalidNullifierInput: nonce "" invalid` |
| `("alice_secret", "-1")` | `ErrInvalidNullifierInput: nonce "-1" invalid` |
| `("alice_secret", "abc")` | `ErrInvalidNullifierInput: nonce "abc" invalid` |
| `("alice_secret", "0x1")` | `ErrInvalidNullifierInput: nonce "0x1" invalid` (cần decimal) |

### 5.4. Canonicalization

```go
a, _ := state.NullifierFor("alice_secret", "1")
b, _ := state.NullifierFor("alice_secret", "01")
c, _ := state.NullifierFor("alice_secret", "  1  ")
// a == b == c
```

### 5.5. Domain separation

```go
a, _ := state.NullifierFor("a", "12")
b, _ := state.NullifierFor("a1", "2")
// a != b — separator '|' giữ chúng độc lập
```

## 6. Vector canonical

`testvectors/alice_100_40/nullifier_wd_1.json`:

```json
{
  "withdrawId": "wd-1",
  "owner": "cosmos1alice",
  "nonce": "1",
  "userSecret": "alice_secret",
  "domainTag": "zkdex/nullifier/v0",
  "hashAlgorithm": "sha256",
  "nullifier": "0x1a1fdf4ccecb7040b7cd7e125226d14ed7717618d996dab969d4cb12550b22f7"
}
```

Khi ZK-02 lock Poseidon, regenerate vector:

```bash
go run ./p3/script-test/gen_state_vectors
```

Đồng thời update constant `canonicalNullifier` trong `nullifier_test.go`.

## 7. Quy trình bump version (`v0 → v1`)

1. Thay implementation `NullifierFor` để dùng hash mới (Poseidon).
2. Đổi hằng `nullifierDomainTag` từ `"zkdex/nullifier/v0"` → `"zkdex/nullifier/v1"`.
3. Chạy generator: `go run ./p3/script-test/gen_state_vectors`.
4. Update `canonicalNullifier` trong `internal/state/nullifier_test.go` về giá trị mới.
5. Chạy `go test ./...` → PASS.
6. Notify P2 (regen proof), P1 (re-verify integration test), P4 (clear cached nullifier).
7. Commit kèm cả 3 thay đổi.

## 8. File đã chạm

| File | Trạng thái |
|---|---|
| `internal/state/nullifier.go` | replaced placeholder với implementation |
| `internal/state/nullifier_test.go` | mới (9 test + 10 sub) |
| `p3/script-test/gen_state_vectors/main.go` | refactor: dùng `state.NullifierFor`, emit `nullifier_wd_1.json` |
| `testvectors/alice_100_40/nullifier_wd_1.json` | mới |
| `p3/changenotes/2026-05-24-state-06.md` | changenote |
| `p3/docs/STATE06_document.md` | tài liệu này |
