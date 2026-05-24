# P3 — STATE-04: Tạo Withdraw Request

## 1. Nhiệm vụ

STATE-04 nhận **ý định rút tiền** từ user (`WithdrawIntent`), validate và đóng gói thành `WithdrawRequest` chuẩn để gửi xuống pipeline tiếp theo.

Điểm cốt lõi:
- **Không** sửa state (balance, nonce, root đều giữ nguyên).
- Validate sớm (format + đủ balance) trước khi tốn CPU của prover ZK.
- Cấp `WithdrawID` tuần tự (`wd-1`, `wd-2`, …) và **reserved** trước nonce kế tiếp.

---

## 2. Luồng hoạt động

```
STATE-04 → STATE-05 → STATE-06 → STATE-07 → STATE-08 → P2 prove → SubmitBatchProof → ClaimWithdraw
```

- **STATE-04 (tài liệu này)**: build `WithdrawRequest` — chỉ đọc state, validate, gán ID + nonce.
- **STATE-05**: thật sự debit balance, ++nonce, tính `rootC`.
- **STATE-06**: tính `nullifier = Hash(userSecret, nonce)`.
- **STATE-07/08**: hash destination + assemble `SettlementUpdate`.

Vị trí trong backend:

```
User → P4 (POST /api/withdraw-request)
        │
        ▼
     P3.WithdrawRequestBuilder.Build(intent)   ← STATE-04
        │
        ▼
     types.WithdrawRequest (trả về cho FE để ký)
        │
        ▼
     STATE-05 (apply thật)
```

Tách STATE-04 và STATE-05 vì: (1) validate sớm tiết kiệm chi phí ZK; (2) FE có thể preview balance còn lại mà không gây side-effect.

---

## 3. Các hàm chính

### 3.1. Struct

```go
type WithdrawIntent struct {
    Owner, Denom, Amount, Destination string  // input từ user
}

type WithdrawRequestBuilder struct {
    state *LocalState   // đọc balance/nonce hiện tại
    mu    sync.Mutex    // bảo vệ seq++ và snapshot account
    seq   uint64        // counter cho WithdrawID
}
```

Output `types.WithdrawRequest` có 7 field: 4 từ user (Owner/Denom/Amount/Destination) + `WithdrawID` & `Nonce` do builder gán + `Signature` để trống (wallet ký sau).

### 3.2. `NewWithdrawRequestBuilder(state *LocalState)`

Constructor. Nhận con trỏ tới `LocalState` để luôn đọc dữ liệu mới nhất. `seq = 0`. Mỗi P4 instance giữ 1 builder dùng chung.

### 3.3. `Build(intent WithdrawIntent) (types.WithdrawRequest, error)`

Hàm chính, 5 phase:

1. **Trim** whitespace ở Owner/Denom/Amount/Destination.
2. **Validate**: 3 field non-empty + `parsePositiveAmount(Amount)` (phải > 0).
3. **Lock mutex** + snapshot account (`state.Account(owner, denom)`). Nếu account chưa tồn tại → zero-value `{Balance:"0", Nonce:"0"}`.
4. **Check balance ≥ amount** bằng `big.Int.Cmp`. Cho phép rút full (`==`), reject khi `<`.
5. **Gán nonce kế tiếp** = `Account.Nonce + 1`, `seq++`, build `WithdrawRequest`.

`seq++` đặt **sau** mọi check — fail thì counter không tăng.

### 3.4. `Seq() uint64`

Debug helper, đọc counter hiện tại (có lock).

### 3.5. Sentinel error

| Sentinel | Khi nào | HTTP |
|---|---|---|
| `ErrInvalidWithdrawIntent` | format sai (empty, amount âm/zero/non-numeric) | 400 |
| `ErrInsufficientBalance` | balance < amount | 422 |

Caller dùng `errors.Is(err, sentinel)` để map (không dùng substring match).

### 3.6. Vì sao `Nonce = Account.Nonce + 1` chứ không phải `Account.Nonce`?

Vì `nullifier = Hash(userSecret, nonce)` phải bind với nonce **sẽ được ghi** sau khi STATE-05 chạy (`Account.Nonce` sẽ thành `old + 1`). Chọn `+1` để cả 3 phía (request, nullifier, Account post-STATE-05) khớp một con số.

---

## 4. Ví dụ

### 4.1. Happy path — Alice rút 40 uusdc

Tiền đề: `Account{Balance:"100", Nonce:"0"}`, `root = rootB`.

```go
req, err := builder.Build(WithdrawIntent{
    Owner: "cosmos1alice", Denom: "uusdc",
    Amount: "40", Destination: "cosmos1alice",
})
```

Kết quả:
```go
req = WithdrawRequest{
    WithdrawID: "wd-1", Owner: "cosmos1alice", Denom: "uusdc",
    Amount: "40", Destination: "cosmos1alice",
    Nonce: "1", Signature: "",
}
```
State **không đổi**: balance vẫn 100, nonce vẫn 0, root vẫn rootB.

### 4.2. Các nhánh failure

| Input | Sentinel | Lý do |
|---|---|---|
| `Amount: "200"` (balance=100) | `ErrInsufficientBalance` | balance < amount |
| `Amount: "-5"` | `ErrInvalidWithdrawIntent` | parse fail |
| `Owner: ""` | `ErrInvalidWithdrawIntent` | empty |
| Account chưa deposit | `ErrInsufficientBalance` | balance zero-value < amount |
| `Amount: "999…999"` (vượt int64) | `ErrInsufficientBalance` | `big.Int` xử lý OK, fail check balance |

Mọi failure: `req = zero-value`, `builder.Seq()` không tăng, state không đổi.

### 4.3. Vector canonical

`testvectors/alice_100_40/withdraw_request_wd_1.json`:
```json
{
  "withdrawId": "wd-1", "owner": "cosmos1alice", "denom": "uusdc",
  "amount": "40", "destination": "cosmos1alice",
  "nonce": "1", "signature": ""
}
```

Regenerate:
```powershell
go run ./p3/script-test/gen_state_vectors
```

---

## File đã chạm

| File | Trạng thái |
|---|---|
| `internal/state/withdraw_request.go` | mới |
| `internal/state/withdraw_request_test.go` | mới (13 test) |
| `p3/script-test/gen_state_vectors/main.go` | mở rộng |
| `testvectors/alice_100_40/withdraw_request_wd_1.json` | mới |
| `p3/docs/STATE04_document.md` | mới (tài liệu này) |
