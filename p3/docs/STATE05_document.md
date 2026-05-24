# P3 — STATE-05: Apply Withdrawal

## 1. Nhiệm vụ

STATE-05 nhận `WithdrawRequest` (từ STATE-04) + `nullifier` (từ STATE-06), thực sự **mutate state** off-chain:

- Trừ balance (`Balance -= Amount`).
- Tăng nonce (`Nonce += 1`).
- Đánh dấu nullifier đã dùng (chống replay).
- Tính lại root mới (`rootC`).

Đây là điểm state thật sự đổi trong pipeline withdraw.

Sau STATE-05:

| Field | Trước (rootB) | Sau (rootC) |
|---|---|---|
| `Account[alice, uusdc].Balance` | 100 | 60 |
| `Account[alice, uusdc].Nonce` | 0 | 1 |
| `LocalState.root` | rootB | rootC |
| `appliedNullifiers[nullifier]` | absent | present |

---

## 2. Luồng hoạt động

```
STATE-04 → STATE-05 → STATE-06 → STATE-07 → STATE-08 → P2 prove → SubmitBatchProof → ClaimWithdraw
```

- **STATE-04**: build request, không mutate.
- **STATE-05 (tài liệu này)**: mutate state, sinh `rootC`.
- **STATE-06**: tính nullifier (hash scheme chưa chốt — STATE-05 nhận nullifier qua tham số để decouple).
- **STATE-07/08**: hash destination + assemble cho prover.

Vị trí trong backend:

```
P4 nhận req đã ký → tính nullifier (STATE-06) → ls.ApplyWithdrawal(req, nullifier)
                                                       │
                                                       ▼
                                              Debit + mark nullifier + ComputeRoot
                                                       │
                                                       ▼
                                              rootC → pipeline STATE-07/08
```

---

## 3. Các hàm chính

### 3.1. Cấu trúc dữ liệu mới

```go
type LocalState struct {
    // ... các field cũ
    appliedNullifiers map[string]struct{}   // mới — set chống replay
}
```

### 3.2. `AccountState.Debit(owner, denom, amount) (types.Account, error)`

Primitive atomic: trừ balance + tăng nonce trong cùng critical section của `AccountState.mu`. Counterpart của `Credit`. Re-check balance một lần nữa (defense in depth nếu caller khác gọi trực tiếp).

### 3.3. `LocalState.ApplyWithdrawal(req, nullifier) (string, error)` — hàm chính

7 phase:

1. **Validate request** (`validateWithdrawRequest`): 6 field (WithdrawID/Owner/Denom/Destination non-empty, Amount > 0, Nonce ≥ 0).
2. **Validate nullifier**: trim + non-empty.
3. **Parse số** (amount, reqNonce) ngoài lock.
4. **Lock `LocalState.mu`**.
5. **Check idempotency**: nullifier đã có trong `appliedNullifiers`? → `ErrWithdrawAlreadyApplied`.
6. **Check balance + nonce**:
   - `bal ≥ amount`? → `ErrInsufficientBalance`.
   - `reqNonce == acc.Nonce + 1`? → `ErrNonceMismatch`.
7. **Mutate** (đúng thứ tự):
   - `Debit` (trừ balance, ++nonce).
   - Mark nullifier.
   - Recompute root.

Trả về `rootC` mới (hoặc `""` + error).

### 3.4. `IsNullifierApplied(nullifier) bool`

Query helper cho P4 — kiểm tra nullifier đã consume chưa, không mutation. Mirror `IsDepositApplied`.

### 3.5. `validateWithdrawRequest(req)`

Validate shape của request. Không validate `Signature` (P4 layer chịu trách nhiệm verify chữ ký).

### 3.6. Sentinel error

| Sentinel | Khi nào | HTTP |
|---|---|---|
| `ErrInvalidWithdrawRequest` | Shape sai (field empty, amount/nonce invalid, nullifier empty) | 400 |
| `ErrInsufficientBalance` | balance < amount | 422 |
| `ErrNonceMismatch` | request.Nonce ≠ acc.Nonce + 1 (stale/out-of-order) | 409 |
| `ErrWithdrawAlreadyApplied` | nullifier replay | 409 (hoặc 200 idempotent) |

### 3.7. Vì sao nullifier truyền qua tham số?

- `types.WithdrawRequest` schema đã chốt — không thêm field.
- Decouple STATE-05 với hash scheme (Poseidon/SHA-256/Keccak — STATE-06 quyết).
- Testable không cần STATE-06.

### 3.8. Vì sao cần cả nullifier-check VÀ nonce-check?

Hai dimensions khác nhau:
- **Nullifier-check**: bắt "identifier đã consume". Rẻ (map lookup), đặt trước.
- **Nonce-check**: bắt "state position đã pass qua". Chặn tình huống attacker forge nullifier mới cho req cũ.

Defense in depth.

---

## 4. Ví dụ

### 4.1. Happy path — Alice rút 40 uusdc

Tiền đề: `Account{Balance:"100", Nonce:"0"}`, `root = rootB`, `appliedNullifiers = {}`.

```go
nullifier := "0x1a1fdf4ccecb…22f7"  // canonical placeholder
rootC, err := ls.ApplyWithdrawal(req, nullifier)
```

Kết quả:
```go
rootC == "0x44d60f…250c"
ls.Account("cosmos1alice","uusdc") == {Balance:"60", Nonce:"1"}
ls.IsNullifierApplied(nullifier) == true
```

### 4.2. Các nhánh failure

| Tình huống | Sentinel | Hành vi |
|---|---|---|
| Apply lại cùng `(req, nullifier)` | `ErrWithdrawAlreadyApplied` | state không đổi |
| Apply lại req với nullifier khác | `ErrNonceMismatch` | acc.Nonce đã advance, expected=2≠1 |
| `req.Amount = "999"` (balance=100) | `ErrInsufficientBalance` | bal < amount |
| `nullifier = ""` hoặc `"   "` | `ErrInvalidWithdrawRequest` | trim → empty |
| Shape sai (WithdrawID empty, Nonce non-numeric, …) | `ErrInvalidWithdrawRequest` | 12 sub-test cover |
| `req.Nonce = 2` khi acc.Nonce = 0 (future) | `ErrNonceMismatch` | expected=1≠2 |

Mọi failure: state byte-identical pre/post (test `FailedApplyLeavesStateClean` bảo vệ).

### 4.3. Hai withdrawal liên tiếp

```go
req1 := build(40); ls.ApplyWithdrawal(req1, n1)  // balance=60, nonce=1
req2 := build(30); ls.ApplyWithdrawal(req2, n2)  // balance=30, nonce=2
```
`req2.Nonce = "2"` được builder tính từ `acc.Nonce=1 + 1`.

### 4.4. Vector canonical

`testvectors/alice_100_40/state_after_withdrawal.json`:
```json
{
  "root": "0x44d60f77db879799be6212c46d198243cbe2fe2969901ca3663dfb204276250c",
  "accounts": [
    { "owner": "cosmos1alice", "denom": "uusdc", "balance": "60", "nonce": "1" }
  ]
}
```

Nullifier hiện là placeholder (`sha256("zkdex/nullifier/v0|secret|nonce")`) — STATE-06/ZK-02 sẽ thay scheme thật, bump tag `v1`.

---

## File đã chạm

| File | Trạng thái |
|---|---|
| `internal/state/withdrawal.go` | mới |
| `internal/state/withdrawal_test.go` | mới (14 test + 12 sub) |
| `internal/state/account_state.go` | mở rộng (`Debit`) |
| `internal/state/local_state.go` | mở rộng (`appliedNullifiers`) |
| `p3/script-test/gen_state_vectors/main.go` | mở rộng |
| `testvectors/alice_100_40/state_after_withdrawal.json` | mới |
| `p3/docs/STATE05_document.md` | mới (tài liệu này) |
