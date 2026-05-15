# P3 — STATE-04: Tạo Withdraw Request

> **Đối tượng đọc:** thành viên nhóm chưa quen với codebase. Tài liệu này dày hơn cần thiết — mục đích là để bất kỳ ai cũng có thể đọc một lần và hiểu được toàn bộ STATE-04 mà không phải hỏi lại.

---

## Mục lục

1. [Khái niệm cốt lõi — đọc trước khi vào code](#1-khái-niệm-cốt-lõi--đọc-trước-khi-vào-code)
2. [Vị trí STATE-04 trong toàn bộ hệ thống](#2-vị-trí-state-04-trong-toàn-bộ-hệ-thống)
3. [Các struct được dùng — giải thích từng field](#3-các-struct-được-dùng--giải-thích-từng-field)
4. [Các hàm helper được tái sử dụng](#4-các-hàm-helper-được-tái-sử-dụng-từ-state-01-03)
5. [Hàm `NewWithdrawRequestBuilder` — constructor](#5-hàm-newwithdrawrequestbuilder--constructor)
6. [Hàm `Build` — chi tiết từng dòng](#6-hàm-build--chi-tiết-từng-dòng)
7. [Hàm `Seq` — debug helper](#7-hàm-seq--debug-helper)
8. [Sentinel error — phân loại lỗi](#8-sentinel-error--phân-loại-lỗi)
9. [Ví dụ chạy thật — Alice rút 40 uusdc](#9-ví-dụ-chạy-thật--alice-rút-40-uusdc)
10. [Ví dụ chạy thật — các nhánh failure](#10-ví-dụ-chạy-thật--các-nhánh-failure)
11. [Ngữ nghĩa Nonce — vì sao `+1`](#11-ngữ-nghĩa-nonce--vì-sao-1)
12. [Cấp `WithdrawID`](#12-cấp-withdrawid)
13. [Concurrency — vì sao có mutex](#13-concurrency--vì-sao-có-mutex)
14. [Tests — 13 test case, mỗi case bảo vệ điều gì](#14-tests--13-test-case-mỗi-case-bảo-vệ-điều-gì)
15. [Vector canonical & generator](#15-vector-canonical--generator)
16. [Hướng dẫn cho consumer khác (P2/P4/P5)](#16-hướng-dẫn-cho-consumer-khác-p2p4p5)
17. [Failure mode & HTTP mapping](#17-failure-mode--http-mapping)
18. [Glossary](#18-glossary)

---

## 1. Khái niệm cốt lõi — đọc trước khi vào code

Trước khi đọc code, cần nắm 5 khái niệm:

### 1.1. "Off-chain state" là gì

Hệ thống ZKDEX có hai bản sổ cái:

- **On-chain state** (do P1 quản trên Cosmos chain): chỉ chứa root rất gọn — `currentStateRoot`, `DepositRecord`, `WithdrawRecord`, `nullifierUsed`. Đắt nên giữ tối thiểu.
- **Off-chain state** (do P3 quản trong bộ nhớ tiến trình backend): chứa balance đầy đủ của từng user, theo từng denom. Rẻ nên giữ đầy đủ.

Hai bản sổ cái không trực tiếp nói chuyện với nhau. Cầu nối là **ZK proof**: P3 đưa state off-chain qua P2 (prover), prover sinh proof, relayer gửi proof lên chain, chain verify rồi mới cập nhật root on-chain.

### 1.2. Quy trình deposit (đã làm ở STATE-03) — recap

1. User gọi `MsgDeposit` trên chain → x/bank chuyển coin sang module account → on-chain ghi `DepositRecord{processed:false}`.
2. Indexer của P4 đọc event → gọi `LocalState.ApplyDeposit(record)`.
3. P3 cộng `Balance` cho user, đánh dấu deposit đã apply, tính lại `root` (gọi là `rootB`).

Đến đây **on-chain** vẫn ở `rootA` (chưa biết deposit), **off-chain** đã ở `rootB`. Sự lệch nhau này sẽ được "san phẳng" bởi `MsgSubmitBatchProof` về sau.

### 1.3. Quy trình withdraw — STATE-04 ở đâu

Withdraw phức tạp hơn deposit vì có yếu tố privacy + double-spend prevention. Chuỗi bước:

```
STATE-04 → STATE-05 → STATE-06 → STATE-07 → STATE-08 → [P2 sinh proof] → MsgSubmitBatchProof → MsgClaimWithdraw
```

- **STATE-04** (file này): user nói "tôi muốn rút 40 uusdc về địa chỉ X". P3 validate, gán ID + nonce, đóng gói thành `WithdrawRequest`. **Không** sửa state.
- **STATE-05**: nhận `WithdrawRequest`, debit balance, ++nonce, tính `rootC` mới.
- **STATE-06**: tính `nullifier = Hash(userSecret, nonce)` — một identifier ẩn danh để chain biết "withdrawal này đã xử lý" mà không lộ user là ai.
- **STATE-07**: hash địa chỉ đích để bind proof với destination cụ thể.
- **STATE-08**: assemble tất cả thành `SettlementUpdate` để gửi cho prover P2.

### 1.4. Vì sao tách STATE-04 và STATE-05

Hai lý do:

1. **Validation sớm**: nếu user nhập sai (số âm, balance không đủ), trả lỗi ngay tại STATE-04 — không tốn chu kỳ CPU của prover (ZK proof generation tốn vài giây đến vài chục giây).
2. **Một số tình huống chỉ cần preview**: ví dụ frontend FE-05 muốn hiển thị "nếu bạn rút 40, balance còn lại sẽ là 60" — gọi STATE-04 để lấy số, không thực sự debit. Nếu STATE-04 và STATE-05 dính vào một hàm, không thể preview mà không gây side-effect.

### 1.5. Nonce là gì và tại sao quan trọng

`Account.Nonce` là **bộ đếm withdrawal** của một (user, denom). Mỗi withdrawal làm `Nonce` tăng 1.

Tác dụng:
- **Chống replay**: nullifier = `Hash(userSecret, nonce)`. Hai withdrawal liên tiếp của cùng user có nonce khác → nullifier khác → chain phân biệt được. Nếu cùng nonce → cùng nullifier → chain reject lần thứ hai.
- **Tuần tự**: chain & off-chain đồng thuận về thứ tự withdrawal của một account.

STATE-04 **chọn** nonce sẽ dùng cho withdrawal này, nhưng **không** ghi vào `Account.Nonce`. Việc ghi là của STATE-05. Chi tiết tại [mục 11](#11-ngữ-nghĩa-nonce--vì-sao-1).

---

## 2. Vị trí STATE-04 trong toàn bộ hệ thống

```
┌───────────────────────────────────────────────────────────────────┐
│ User: "Tôi muốn rút 40 uusdc về cosmos1alice"                     │
└──────────────────────────────┬────────────────────────────────────┘
                               │  (HTTP POST)
                               ▼
        ┌─────────────────────────────────────────────────┐
        │ P4: POST /api/withdraw-request                  │
        │   - parse JSON body                             │
        │   - validate format                             │
        │   - gọi P3.WithdrawRequestBuilder.Build(intent) │
        └────────────────────┬────────────────────────────┘
                             │
                             ▼
        ┌─────────────────────────────────────────────────┐
        │ P3 STATE-04 — Build(intent)   ← TÀI LIỆU NÀY    │
        │   ┌─────────────────────────────────────────┐   │
        │   │ 1. Trim các field                       │   │
        │   │ 2. Validate (4 field, amount > 0)       │   │
        │   │ 3. Snapshot account (read-only)         │   │
        │   │ 4. Check balance ≥ amount               │   │
        │   │ 5. nextNonce = currentNonce + 1         │   │
        │   │ 6. seq++, withdrawId = "wd-N"           │   │
        │   │ 7. Trả về WithdrawRequest               │   │
        │   └─────────────────────────────────────────┘   │
        │                                                 │
        │   KHÔNG sửa balance, nonce, root.               │
        └────────────────────┬────────────────────────────┘
                             │
                             ▼
            ┌──────────────────────────────────────┐
            │ types.WithdrawRequest                │
            │ {                                    │
            │   "withdrawId": "wd-1",              │
            │   "owner": "cosmos1alice",           │
            │   "denom": "uusdc",                  │
            │   "amount": "40",                    │
            │   "destination": "cosmos1alice",     │
            │   "nonce": "1",                      │
            │   "signature": ""                    │
            │ }                                    │
            └────────────────────┬─────────────────┘
                                 │
                                 ▼ (P4 lưu, trả response cho FE)
              ┌──────────────────────────────────┐
              │ P5 hiển thị cho user             │
              │ User ký bằng wallet              │
              │ → Signature được gắn vào         │
              └──────────────────────────────────┘
                                 │
                                 ▼ (sau đó vào pipeline batch)
              STATE-05 → STATE-06 → … → SubmitBatchProof → ClaimWithdraw
```

---

## 3. Các struct được dùng — giải thích từng field

### 3.1. `WithdrawIntent` — input (P3 tự định nghĩa)

```go
type WithdrawIntent struct {
    Owner       string
    Denom       string
    Amount      string
    Destination string
}
```

| Field | Ý nghĩa | Ví dụ | Validate |
|---|---|---|---|
| `Owner` | Địa chỉ Cosmos của user muốn rút | `"cosmos1alice"` | non-empty sau trim |
| `Denom` | Loại token (denom là khái niệm Cosmos) | `"uusdc"` (micro USDC) | non-empty sau trim |
| `Amount` | Số lượng cần rút, **dưới dạng chuỗi thập phân** | `"40"`, `"100"`, `"99999999999999999999"` | parse được, > 0 |
| `Destination` | Địa chỉ nhận coin sau khi claim | `"cosmos1alice"` (thường = Owner, nhưng có thể khác) | non-empty sau trim |

**Vì sao `Amount` là `string` chứ không phải `int64`?**

- Cosmos amounts có thể lớn hơn `int64.MaxValue` (ví dụ NFT collection cap, hoặc cộng dồn supply token có 18 decimals).
- JSON tunnel: JavaScript của FE chỉ chính xác đến 2^53, vượt qua sẽ mất chính xác → string an toàn.
- Đồng nhất với agreement đã chốt trong `zkdex_final_parallel_plan_fixed.html`.

**Vì sao `WithdrawIntent` tách rời `types.WithdrawRequest`?**

Vì `WithdrawRequest` có 7 field, trong đó 3 field (`WithdrawID`, `Nonce`, `Signature`) **không** do user cung cấp. Nếu cho user truyền cả struct, sẽ tạo ra rủi ro forge — user tự gán `Nonce="0"` để gây collision, hay tự gán `WithdrawID="wd-999"` để spoof. Tách thành 2 type khiến compiler chặn ngay tại bước biên dịch, không cần validation runtime.

### 3.2. `types.WithdrawRequest` — output (định nghĩa trong `pkg/types/withdraw.go`)

```go
type WithdrawRequest struct {
    WithdrawID  string `json:"withdrawId"`
    Owner       string `json:"owner"`
    Denom       string `json:"denom"`
    Amount      string `json:"amount"`
    Destination string `json:"destination"`
    Nonce       string `json:"nonce"`
    Signature   string `json:"signature"`
}
```

| Field | Người gán | Mục đích |
|---|---|---|
| `WithdrawID` | **Builder STATE-04** | ID dạng `"wd-1"`, `"wd-2"`, ... để truy vết request qua các stage |
| `Owner` | User (qua intent) | Ai đang rút |
| `Denom` | User (qua intent) | Token nào |
| `Amount` | User (qua intent) | Bao nhiêu |
| `Destination` | User (qua intent) | Gửi tới đâu |
| `Nonce` | **Builder STATE-04** | Nonce mới (= current + 1) — sẽ bind với nullifier |
| `Signature` | **Wallet user / P4 layer** | Để trống bởi builder; gắn sau khi user ký |

### 3.3. `WithdrawRequestBuilder` — engine

```go
type WithdrawRequestBuilder struct {
    state *LocalState  // tham chiếu read-only tới sổ cái off-chain
    mu    sync.Mutex   // bảo vệ seq + snapshot
    seq   uint64       // counter cho WithdrawID
}
```

| Field | Vai trò |
|---|---|
| `state` | Để builder đọc `Account.Balance` và `Account.Nonce` hiện tại. Builder không sửa state, chỉ đọc. |
| `mu` | Mutex để hai goroutine gọi `Build` đồng thời không bị race khi `seq++` và khi snapshot account. |
| `seq` | Bắt đầu từ 0; tăng lên 1 trước khi tạo `wd-1`, lên 2 trước khi tạo `wd-2`, ... |

---

## 4. Các hàm helper được tái sử dụng (từ STATE-01..03)

STATE-04 dùng 2 hàm parsing số và 1 method đọc account đã có sẵn:

### 4.1. `parsePositiveAmount` — `internal/state/account_state.go`

```go
func parsePositiveAmount(amount string) (*big.Int, error)
```

| Input | Output |
|---|---|
| `"100"` | `big.Int{100}`, `nil` |
| `"0"` | `nil`, `ErrAmountNegative` |
| `"-5"` | `nil`, `ErrInvalidAmount` |
| `"abc"` | `nil`, `ErrInvalidAmount` |
| `""` | `nil`, `ErrInvalidAmount` |
| `"  50  "` | `big.Int{50}`, `nil` (đã trim trong helper) |

Quy tắc: phải parse được như số nguyên thập phân **và > 0**.

### 4.2. `parseNonNegativeAmount` — `internal/state/account_state.go`

```go
func parseNonNegativeAmount(amount string) (*big.Int, error)
```

Giống `parsePositiveAmount` nhưng cho phép `"0"`. Dùng cho balance và nonce hiện tại (vì balance có thể là 0 cho account chưa giao dịch).

### 4.3. `LocalState.Account(owner, denom)` — `internal/state/local_state.go`

```go
func (s *LocalState) Account(owner, denom string) types.Account
```

Trả về `Account` hiện tại. Nếu account chưa tồn tại trong map (ví dụ user chưa bao giờ deposit), trả về account zero-value:

```go
types.Account{Owner: owner, Denom: denom, Balance: "0", Nonce: "0"}
```

Vì vậy builder không cần phân biệt "account chưa tồn tại" vs "account có balance 0" — cùng đường code.

---

## 5. Hàm `NewWithdrawRequestBuilder` — constructor

```go
func NewWithdrawRequestBuilder(state *LocalState) *WithdrawRequestBuilder {
    return &WithdrawRequestBuilder{state: state}
}
```

### Phân tích

- **Tham số**: con trỏ tới `LocalState`. Builder không sở hữu state — nhiều builder có thể chia sẻ cùng state nếu cần (hiện tại MVP chỉ dùng 1 builder).
- **Khởi tạo**: `seq = 0` (zero-value của `uint64`), `mu` là zero-value `sync.Mutex` (Go cho phép dùng ngay không cần init).
- **Trả về**: con trỏ `*WithdrawRequestBuilder` để các method nhận pointer receiver có thể sửa `seq`.

### Vì sao nhận con trỏ chứ không phải value của `LocalState`?

Nếu nhận value (`state LocalState`), Go sẽ **copy** struct — và bản copy đó không bao giờ "thấy" các deposit mới được apply trên `LocalState` thật. Builder dùng pointer để luôn đọc dữ liệu mới nhất.

### Vòng đời

Trong P4 backend, builder được tạo một lần (ví dụ trong `main.go`) và lưu trong DI container, dùng chung cho mọi request HTTP:

```go
// Pseudo-code phía P4
localState := state.NewLocalState()
withdrawBuilder := state.NewWithdrawRequestBuilder(localState)

router.POST("/api/withdraw-request", func(c *gin.Context) {
    var intent state.WithdrawIntent
    c.BindJSON(&intent)
    req, err := withdrawBuilder.Build(intent)
    // ...
})
```

---

## 6. Hàm `Build` — chi tiết từng dòng

Đây là trái tim của STATE-04. Tôi sẽ chia làm 5 phase và giải thích từng dòng.

### Signature

```go
func (b *WithdrawRequestBuilder) Build(intent WithdrawIntent) (types.WithdrawRequest, error)
```

- **Receiver `b *WithdrawRequestBuilder`**: pointer receiver vì sẽ sửa `b.seq`.
- **Tham số `intent WithdrawIntent`**: nhận **value** (không phải con trỏ). Lý do: builder sẽ trim các field của intent — nếu nhận con trỏ thì sẽ vô tình sửa struct của caller. Nhận value → có bản copy riêng, an toàn.
- **Trả về `(types.WithdrawRequest, error)`**: pattern chuẩn Go. Khi error ≠ nil, WithdrawRequest là zero-value và caller phải bỏ.

### Phase 1: chuẩn hoá input (trim whitespace)

```go
intent.Owner = strings.TrimSpace(intent.Owner)
intent.Denom = strings.TrimSpace(intent.Denom)
intent.Destination = strings.TrimSpace(intent.Destination)
intent.Amount = strings.TrimSpace(intent.Amount)
```

**Vì sao trim?**

User có thể vô tình copy-paste address kèm space đầu/cuối:

```
"  cosmos1alice   "   →  "cosmos1alice"
" 40\n"               →  "40"
```

Không trim sẽ gây:
- `Owner != ""` (vì có space) → pass check empty.
- Nhưng so sánh với địa chỉ trong storage (key của map) sẽ miss → builder báo "balance = 0" sai lầm.

**Cảnh báo**: hiện tại trim ở builder. Tương lai cần trim ở MỌI điểm vào — bao gồm cả `LocalState.ApplyDeposit`. STATE-03 đã trim trong `newAccountKey()` (xem `account_state.go:26-36`). Cần audit lại nếu thêm code path mới.

### Phase 2: validate 4 field

```go
if intent.Owner == "" {
    return types.WithdrawRequest{}, fmt.Errorf("%w: owner is empty", ErrInvalidWithdrawIntent)
}
if intent.Denom == "" {
    return types.WithdrawRequest{}, fmt.Errorf("%w: denom is empty", ErrInvalidWithdrawIntent)
}
if intent.Destination == "" {
    return types.WithdrawRequest{}, fmt.Errorf("%w: destination is empty", ErrInvalidWithdrawIntent)
}
amount, err := parsePositiveAmount(intent.Amount)
if err != nil {
    return types.WithdrawRequest{}, fmt.Errorf("%w: amount %q invalid: %v", ErrInvalidWithdrawIntent, intent.Amount, err)
}
```

**Giải thích pattern `%w`:**

`fmt.Errorf("%w: …", ErrInvalidWithdrawIntent, ...)` tạo error mới bọc (wrap) sentinel `ErrInvalidWithdrawIntent`. Caller có thể dùng:

```go
err := builder.Build(intent)
if errors.Is(err, state.ErrInvalidWithdrawIntent) {
    // map sang HTTP 400 Bad Request
}
```

`errors.Is` đi qua chuỗi wrap để tìm sentinel — robust hơn `err == ErrInvalidWithdrawIntent` hay `strings.Contains(err.Error(), "invalid")`.

**Vì sao validate trước khi lock mutex?**

Validate là pure function, không cần đụng `b.seq` hay state. Reject sớm ngoài critical section để mutex không bị giữ lâu — tăng throughput khi có nhiều caller đồng thời.

**Vì sao có 4 lần `if` rời nhau thay vì 1 hàm `validate(intent)`?**

Có thể refactor được, nhưng:
- Mỗi case có thông điệp lỗi riêng (giúp debug).
- Nếu gom vào hàm, phải truyền vào error map hoặc trả về string lỗi — phức tạp hơn lợi ích.
- 4 dòng if cùng pattern dễ đọc, không phải khi nào DRY cũng là tốt.

### Phase 3: vào critical section (mutex), snapshot account

```go
b.mu.Lock()
defer b.mu.Unlock()

acc := b.state.Account(intent.Owner, intent.Denom)
```

**Vì sao lock?**

`b.seq++` ở cuối hàm phải atomic so với `b.seq` của các goroutine khác. Hai goroutine cùng đọc `b.seq=5`, cả hai cùng cộng → cả hai cùng tạo `wd-6` → ID trùng. Mutex chặn.

Snapshot account cũng phải nằm trong critical section để **race với deposit/withdraw khác không xảy ra**. Tình huống xấu nếu không lock:

```
Goroutine A: bal := state.Account(alice, uusdc).Balance   → 100
Goroutine B: state.ApplyDeposit(alice, uusdc, 50)         → balance=150
Goroutine A: kiểm tra bal=100 >= amount=120, FAIL
```

Trong thực tế, hệ thống MVP single-process nên hiếm gặp, nhưng lock có chi phí gần như zero và bảo vệ tuyệt đối.

**`defer b.mu.Unlock()`**: Go pattern — đảm bảo unlock dù function thoát bằng `return` hay panic.

### Phase 4: check balance ≥ amount

```go
bal, err := parseNonNegativeAmount(acc.Balance)
if err != nil {
    return types.WithdrawRequest{}, fmt.Errorf("corrupt balance for %s/%s: %w", intent.Owner, intent.Denom, err)
}
if bal.Cmp(amount) < 0 {
    return types.WithdrawRequest{}, fmt.Errorf("%w: have %s, want %s", ErrInsufficientBalance, bal.String(), amount.String())
}
```

**Vì sao re-parse balance?**

`acc.Balance` là `string`. Để so sánh với `amount` (đã là `*big.Int`), phải convert. `parseNonNegativeAmount` cũng kiểm tra balance là chuỗi số hợp lệ — nếu state bị corrupt (ví dụ lỗi serialization), ta phát hiện ngay thay vì gây undefined behavior.

**`bal.Cmp(amount)` trả về gì?**

`big.Int.Cmp` là cách so sánh chuẩn cho `*big.Int`:
- `-1` nếu `bal < amount`
- `0` nếu `bal == amount`
- `+1` nếu `bal > amount`

`< 0` đồng nghĩa "balance nhỏ hơn amount" → reject.

**Boundary case `bal == amount`**:

`Cmp` trả về 0 — không < 0 → cho qua. User được phép rút hết toàn bộ balance. Test `TestBuildWithdrawRequest_ExactBalanceSucceeds` bảo vệ tính chất này.

**Sao không dùng `<=`?**

Vì `<=` sẽ chặn full-balance withdraw, là use-case hợp lệ. Off-by-one cổ điển — test `TestBuildWithdrawRequest_OneOverBalanceFails` chuyên để cảnh báo regression nếu ai sửa thành `<=`.

**Error message chứa `have/want`**:

```
state: insufficient balance for withdraw: have 100, want 200
```

Giúp P4 hiển thị cho user thay vì chỉ "lỗi không xác định".

### Phase 5: gán nonce, ID, build struct

```go
currentNonce, err := parseNonNegativeAmount(acc.Nonce)
if err != nil {
    return types.WithdrawRequest{}, fmt.Errorf("corrupt nonce for %s/%s: %w", intent.Owner, intent.Denom, err)
}
nextNonce := new(big.Int).Add(currentNonce, big.NewInt(1)).String()

b.seq++
return types.WithdrawRequest{
    WithdrawID:  "wd-" + strconv.FormatUint(b.seq, 10),
    Owner:       intent.Owner,
    Denom:       intent.Denom,
    Amount:      amount.String(),
    Destination: intent.Destination,
    Nonce:       nextNonce,
    Signature:   "",
}, nil
```

**`new(big.Int).Add(currentNonce, big.NewInt(1))`**:

- `new(big.Int)` tạo `*big.Int` zero-value.
- `.Add(a, b)` set receiver = `a + b` và trả về chính receiver.
- Pattern Go chuẩn cho big.Int (`a + b` không hoạt động vì big.Int là struct lớn, không phải số nguyên primitive).

Tương đương:
```go
nextNonce := big.NewInt(0)
nextNonce.Add(currentNonce, big.NewInt(1))
nextNonceStr := nextNonce.String()
```

**`b.seq++` đặt SAU mọi check**:

Quan trọng. Nếu fail ở phase 1-4, hàm return sớm và `b.seq` chưa tăng. Lần build tiếp theo vẫn nhận `wd-1`. Test `TestBuildWithdrawRequest_FailedBuildDoesNotConsumeID` bảo vệ điều này.

**`strconv.FormatUint(b.seq, 10)`**:

Convert `uint64` sang chuỗi thập phân. Tương đương `fmt.Sprintf("%d", b.seq)` nhưng nhanh hơn (không cần parse format string).

**`Signature: ""`**:

Để trống. Wallet của user (qua P4/P5) sẽ ký request và gán signature sau. P3 không có private key.

**`amount.String()` chứ không phải `intent.Amount`**:

Sau khi parse rồi serialize lại, ta được dạng canonical: không khoảng trắng đầu/cuối, không leading zero. Ví dụ `"  040  "` → `"40"`. Bảo vệ consumer downstream khỏi các biến thể không cần thiết.

---

## 7. Hàm `Seq` — debug helper

```go
func (b *WithdrawRequestBuilder) Seq() uint64 {
    b.mu.Lock()
    defer b.mu.Unlock()
    return b.seq
}
```

Trả về counter hiện tại. Chỉ dùng cho test và debug — không phải phần của contract STATE-04.

**Vì sao vẫn lock?**

Đọc `uint64` trên kiến trúc 64-bit là atomic, nhưng Go spec không bảo đảm. Lock vẫn đúng và rẻ.

---

## 8. Sentinel error — phân loại lỗi

```go
var (
    ErrInvalidWithdrawIntent = errors.New("state: invalid withdraw intent")
    ErrInsufficientBalance   = errors.New("state: insufficient balance for withdraw")
)
```

**`errors.New` vs `fmt.Errorf`?**

- `errors.New` tạo error tĩnh, dùng làm "sentinel" (mốc) để so sánh.
- `fmt.Errorf("%w: …")` tạo error chứa context cụ thể, wrap quanh sentinel.

Pattern:

```go
// Tạo error
return fmt.Errorf("%w: amount %q invalid", ErrInvalidWithdrawIntent, intent.Amount)

// Caller check
if errors.Is(err, ErrInvalidWithdrawIntent) { … }
```

Hai sentinel chia thế giới làm 2 phe:

| Sentinel | Nguyên nhân | HTTP |
|---|---|---|
| `ErrInvalidWithdrawIntent` | User nhập sai (4xx do user) | 400 |
| `ErrInsufficientBalance` | Logic refuse (4xx do business rule) | 422 |

Còn các error wrap `corrupt balance/nonce` không có sentinel → nếu xảy ra → bug ở STATE-03 hoặc storage layer → 500.

---

## 9. Ví dụ chạy thật — Alice rút 40 uusdc

Lấy đúng canonical vector của bộ test.

### Tiền đề (sau STATE-03)

```
LocalState:
  root = rootB = 0x9b325b4150d417adfd816930b6f291aaf9493995fe0f960864c616ff178f8620
  accounts:
    cosmos1alice / uusdc → Account{Balance: "100", Nonce: "0"}
  appliedDeposits: {"dep-1"}
```

### Setup

```go
ls := state.NewLocalState()
ls.ApplyDeposit(types.DepositRecord{
    DepositID: "dep-1",
    Owner: "cosmos1alice",
    Denom: "uusdc",
    Amount: "100",
})
// → state.root == rootB

builder := state.NewWithdrawRequestBuilder(ls)
// builder.seq == 0
```

### Gọi `Build`

```go
req, err := builder.Build(state.WithdrawIntent{
    Owner:       "cosmos1alice",
    Denom:       "uusdc",
    Amount:      "40",
    Destination: "cosmos1alice",
})
```

### Trace từng phase

| Phase | Hành động | Giá trị |
|---|---|---|
| 1 | Trim các field | tất cả không có whitespace, giữ nguyên |
| 2 | Validate empty | tất cả non-empty ✓ |
| 2 | `parsePositiveAmount("40")` | `*big.Int{40}` ✓ |
| 3 | Lock mutex; `state.Account("cosmos1alice", "uusdc")` | `Account{Balance:"100", Nonce:"0"}` |
| 4 | `parseNonNegativeAmount("100")` | `*big.Int{100}` |
| 4 | `bal.Cmp(amount)` = `100.Cmp(40)` | `+1` (≥ 0, không reject) |
| 5 | `parseNonNegativeAmount("0")` | `*big.Int{0}` |
| 5 | `nextNonce = 0 + 1` | `*big.Int{1}` → `"1"` |
| 5 | `b.seq++` | `b.seq = 1` |
| 5 | Tạo `WithdrawRequest` | xem dưới |

### Kết quả

```go
req == types.WithdrawRequest{
    WithdrawID:  "wd-1",
    Owner:       "cosmos1alice",
    Denom:       "uusdc",
    Amount:      "40",
    Destination: "cosmos1alice",
    Nonce:       "1",
    Signature:   "",
}
err == nil
```

### State sau Build — không đổi

```
LocalState.Root() == rootB  ← VẪN LÀ rootB, không phải rootC
LocalState.Account("cosmos1alice", "uusdc") == Account{Balance:"100", Nonce:"0"}
```

STATE-04 hoàn thành. STATE-05 sẽ là người ghi `Balance="60"`, `Nonce="1"`, và tính rootC.

### Vector tương ứng

`testvectors/alice_100_40/withdraw_request_wd_1.json`:

```json
{
  "withdrawId": "wd-1",
  "owner": "cosmos1alice",
  "denom": "uusdc",
  "amount": "40",
  "destination": "cosmos1alice",
  "nonce": "1",
  "signature": ""
}
```

---

## 10. Ví dụ chạy thật — các nhánh failure

### 10.1. Balance không đủ

```go
req, err := builder.Build(state.WithdrawIntent{
    Owner: "cosmos1alice", Denom: "uusdc", Amount: "200", Destination: "cosmos1alice",
})
```

Trace:

| Phase | Hành động | Giá trị |
|---|---|---|
| 1-2 | Trim + validate | pass ✓ |
| 3 | Snapshot account | `Account{Balance:"100", Nonce:"0"}` |
| 4 | `bal.Cmp(amount)` = `100.Cmp(200)` | `-1` (< 0, REJECT) |

Kết quả:

```go
req == types.WithdrawRequest{}  // zero-value
err.Error() == "state: insufficient balance for withdraw: have 100, want 200"
errors.Is(err, state.ErrInsufficientBalance) == true
builder.Seq() == 0  // counter KHÔNG tăng
```

### 10.2. Amount âm

```go
builder.Build(state.WithdrawIntent{
    Owner: "cosmos1alice", Denom: "uusdc", Amount: "-5", Destination: "cosmos1alice",
})
```

Fail tại phase 2: `parsePositiveAmount("-5")` trả `ErrInvalidAmount`.

```go
err.Error() == `state: invalid withdraw intent: amount "-5" invalid: state: amount is not a valid non-negative integer string`
errors.Is(err, state.ErrInvalidWithdrawIntent) == true
```

### 10.3. Owner empty

```go
builder.Build(state.WithdrawIntent{
    Denom: "uusdc", Amount: "40", Destination: "cosmos1alice",
})
```

Fail tại phase 2: `intent.Owner == ""`.

```go
err.Error() == "state: invalid withdraw intent: owner is empty"
```

### 10.4. Account chưa tồn tại

```go
builder.Build(state.WithdrawIntent{
    Owner: "cosmos1bob", Denom: "uusdc", Amount: "1", Destination: "cosmos1bob",
})
```

Trace:

| Phase | Hành động | Giá trị |
|---|---|---|
| 1-2 | Trim + validate | pass |
| 3 | `state.Account("cosmos1bob", "uusdc")` | Bob chưa có → trả zero-value `Account{Balance:"0", Nonce:"0"}` |
| 4 | `bal.Cmp(amount)` = `0.Cmp(1)` | `-1` REJECT |

```go
err.Error() == "state: insufficient balance for withdraw: have 0, want 1"
```

**Quan sát**: không có error riêng "account not found". User nhìn cùng response như user có balance < amount. Đây là **design choice** — không để rò rỉ thông tin "address X có tồn tại trong hệ thống hay không".

### 10.5. Amount cực lớn (vượt int64)

```go
builder.Build(state.WithdrawIntent{
    Owner: "cosmos1alice", Denom: "uusdc",
    Amount: "99999999999999999999999999999999",
    Destination: "cosmos1alice",
})
```

`big.Int` xử lý đúng:

| Phase | Giá trị |
|---|---|
| 2 | `parsePositiveAmount("999...")` → `*big.Int{999…}` ✓ |
| 4 | `bal=100`, `amount=999…` → `Cmp = -1` REJECT |

Không panic, không overflow.

---

## 11. Ngữ nghĩa Nonce — vì sao `+1`

Đây là quyết định quan trọng nhất của STATE-04. Hai cách hiểu khả dĩ:

| Đọc | `request.Nonce` =  | Ý nghĩa |
|---|---|---|
| (A) | `Account.Nonce` | nonce user *đã có* khi submit |
| (B) | `Account.Nonce + 1` | nonce withdrawal này *sẽ dùng* |

Chọn **(B)**. Lý do:

### 11.1. Agreement đã chốt với (B)

Bảng Phase 1 trong `zkdex_cosmos_deposit_withdraw_flows.html#p3`:

```
OFF-CHAIN — Account State
  balance[uusdc]:  100 → 60
  nonce:           0 → 1                   ← Account.Nonce thành 1 sau withdraw
  local root:      rootB → rootC

OFF-CHAIN — WithdrawRequest
  withdraw_id:  wd-1
  amount:       40
  nullifier:    0xNNN... computed          ← bound với nonce nào?
```

Vector canonical Alice: trước withdrawal `Account.Nonce="0"`, request có `nonce="1"`. Chỉ khớp (B).

### 11.2. Bind đúng với nullifier

```
nullifier = Hash(userSecret, nonce)
```

- "nonce" trong công thức này phải là **nonce của withdrawal cụ thể** này — không phải nonce user đang có lúc submit, mà là nonce sẽ được ghi vào Account khi withdrawal được apply.
- Nếu chọn (A), STATE-05 ghi `Account.Nonce = old+1` thì state on-chain (sau khi MsgSubmitBatchProof verify thành công) sẽ có `Account.Nonce` = `request.Nonce + 1` — không khớp.

### 11.3. Bind đúng với on-chain state

Sau STATE-05 chạy:
- `Account.Nonce` (off-chain) trở thành `currentNonce + 1`.
- `request.Nonce` cần bằng giá trị này để on-chain sau verify proof cũng nhất quán.

Vậy chọn (B) là cách duy nhất cả 3 phía (request, nullifier, Account post-STATE-05) khớp một con số duy nhất.

### 11.4. Hệ quả thiết kế

STATE-04 thực ra là người **reserved trước** một nonce. Việc thực sự "tiêu nonce" là STATE-05. Nếu hai builds liên tiếp trên cùng account mà STATE-05 chưa chạy → cả hai pin cùng nonce → cả hai có cùng nullifier → STATE-05 hoặc verifier sẽ reject cái thứ hai.

Đây là hành vi được document trong test `TestBuildWithdrawRequest_SequentialIDs` và trong package comment.

---

## 12. Cấp `WithdrawID`

### Cách hoạt động

```go
b.seq++
withdrawId := "wd-" + strconv.FormatUint(b.seq, 10)
```

- Build đầu tiên (sau khi pass validation): `seq=0 → 1`, ID = `"wd-1"`
- Build thứ hai: `seq=1 → 2`, ID = `"wd-2"`
- ...

### Tính chất

| Tính chất | Có/Không | Lý do |
|---|---|---|
| Tăng monotonic | Có | `seq++` chỉ tăng, không giảm |
| Duy nhất trong instance builder | Có | mutex bảo vệ `seq++` |
| Duy nhất giữa nhiều instance builder | **Không** | mỗi instance độc lập đếm từ 0 |
| Persistent qua restart process | **Không** | `seq` ở memory; restart → reset về 0 |
| Có nghĩa nghiệp vụ | **Không** | chỉ là handle |

### Hạn chế và workaround

**Vấn đề: restart process → counter reset → có thể tạo `wd-1` trùng với một `wd-1` cũ.**

Trong MVP không sao vì:
- P4 lưu request đầy đủ vào storage của mình ngay khi build → nếu trùng ID sẽ phát hiện ở storage layer.
- Chain không thấy counter — chỉ thấy `withdrawId` cuối được đính vào `SettlementUpdate`.

**Vấn đề: chạy nhiều P4 instance song song → counter trùng.**

MVP single-process → không xảy ra. Nếu sau này cần horizontal scale:
- Thay format ID bằng UUID v4 hoặc `cuid`.
- Hoặc pull next-ID từ một registry chung (Postgres sequence, Redis INCR).

Chỉ đổi 1 dòng tại `Build`; phần còn lại của pipeline không phụ thuộc format ID.

---

## 13. Concurrency — vì sao có mutex

### Race condition #1: `seq++` không atomic

Nếu không lock:

```
Goroutine A: tmp = b.seq      → 5
Goroutine B: tmp = b.seq      → 5
Goroutine A: b.seq = tmp + 1  → 6
Goroutine B: b.seq = tmp + 1  → 6
```

Cả hai tạo `wd-6` → collision. Mutex chặn.

### Race condition #2: snapshot account không đồng bộ với check balance

Nếu không lock:

```
Goroutine A: acc = state.Account(alice, uusdc)   → balance 100
Goroutine A: bal.Cmp(amount=80) >= 0             → pass
... (A chưa kịp gán request) ...
Goroutine B: state.ApplyWithdrawal(req=70)       → balance 30
Goroutine A: tạo request 80                       → khi STATE-05 chạy, fail
```

Hậu quả: request A được build thành công nhưng STATE-05 sẽ reject — gây UX xấu (user nghĩ giao dịch thành công nhưng sau đó fail).

Mutex giữ snapshot account + check balance + tạo request đồng bộ. Nếu B muốn debit phải đợi A xong.

**Lưu ý**: mutex của builder không lock `LocalState`. `LocalState.ApplyDeposit/ApplyWithdrawal` có mutex riêng. Race vẫn có thể xảy ra giữa "snapshot trong Build" và "ApplyWithdrawal đang chạy" nếu chúng không cùng critical section. Hiện tại pipeline MVP gọi tuần tự (Build → Apply) trong cùng goroutine xử lý HTTP request, nên không phát sinh race. Khi mở rộng cần re-audit.

### `defer Unlock` — pattern an toàn

```go
b.mu.Lock()
defer b.mu.Unlock()
```

Nếu hàm panic giữa chừng (ví dụ corrupt balance gây panic), `defer` vẫn chạy → mutex được giải phóng → process không deadlock. Không bao giờ viết `b.mu.Lock(); ...; b.mu.Unlock()` rời nhau.

---

## 14. Tests — 13 test case, mỗi case bảo vệ điều gì

File: `internal/state/withdraw_request_test.go`

### Bảng tổng hợp

| # | Test | Bảo vệ tính chất |
|---|---|---|
| 1 | `TestBuildWithdrawRequest_Canonical` | Happy path: `wd-1`, `nonce="1"`, balance/nonce không đổi |
| 2 | `TestBuildWithdrawRequest_SequentialIDs` | Liên tiếp 2 build → `wd-1`, `wd-2`; nonce cùng `"1"` (documented limit) |
| 3 | `TestBuildWithdrawRequest_InvalidIntent` (8 sub) | Tất cả nhánh empty/blank/zero/âm/non-numeric reject với `ErrInvalidWithdrawIntent` |
| 4 | `TestBuildWithdrawRequest_InsufficientBalance` | balance 100, rút 200 → `ErrInsufficientBalance` |
| 5 | `TestBuildWithdrawRequest_ExactBalanceSucceeds` | balance 100, rút 100 → OK (boundary) |
| 6 | `TestBuildWithdrawRequest_OneOverBalanceFails` | balance 100, rút 101 → `ErrInsufficientBalance` (off-by-one) |
| 7 | `TestBuildWithdrawRequest_FailedBuildDoesNotConsumeID` | Build fail → `seq=0`; build tiếp theo trả `wd-1` |
| 8 | `TestBuildWithdrawRequest_FailedBuildLeavesStateClean` | Build fail → balance/nonce/root byte-identical |
| 9 | `TestBuildWithdrawRequest_InsufficientBalance_WrongDenom` | Alice có 100 uusdc, rút 1 uatom → reject |
| 10 | `TestBuildWithdrawRequest_HugeAmountAgainstSmallBalance` | amount > int64 max → so sánh đúng qua `big.Int` |
| 11 | `TestBuildWithdrawRequest_UnknownAccountIsZeroBalance` | account chưa deposit → reject |
| 12 | `TestBuildWithdrawRequest_TrimsWhitespace` | input có padding → request canonical |
| 13 | `TestBuildWithdrawRequest_DoesNotAffectRoot` | root trước == root sau build thành công |

### Tại sao mỗi test này tồn tại

- **Test 5, 6**: bảo vệ khỏi off-by-one. Nếu ai sửa `bal.Cmp(amount) < 0` thành `<= 0`, test 5 sẽ fail. Nếu sửa thành `>= 0`, test 6 sẽ fail.
- **Test 7, 8**: "no side effects on failure" — quan trọng cho retry. Nếu user submit lỗi và retry sau, hệ thống phải hoạt động đúng.
- **Test 9**: cross-denom isolation. Bảo vệ khỏi bug "tôi có 100 USDC nên có thể rút 50 ATOM".
- **Test 10**: bảo vệ khỏi overflow. Nếu ai refactor sang `int64`, test này fail.
- **Test 13**: bảo vệ tính chất read-only của STATE-04. Nếu ai vô tình gọi `state.ApplyWithdrawal` trong `Build`, test này fail.

### Cách chạy

```powershell
cd D:\HCMUS\Graduation\Project\Phase2\ganc-sys
go test -v ./internal/state/...
```

Kết quả mong đợi: tất cả PASS, 13 test top-level + 16 sub-test.

---

## 15. Vector canonical & generator

### File output

`testvectors/alice_100_40/withdraw_request_wd_1.json`:

```json
{
  "withdrawId": "wd-1",
  "owner": "cosmos1alice",
  "denom": "uusdc",
  "amount": "40",
  "destination": "cosmos1alice",
  "nonce": "1",
  "signature": ""
}
```

### Generator: `p3/script-test/gen_state_vectors/main.go`

Đoạn cập nhật:

```go
// STATE-04 — build the canonical Alice withdraw request (40 uusdc).
wb := state.NewWithdrawRequestBuilder(ls)
wdReq, err := wb.Build(state.WithdrawIntent{
    Owner:       aliceAddr,
    Denom:       denom,
    Amount:      "40",
    Destination: aliceAddr,
})
if err != nil {
    die("build withdraw request: %v", err)
}
write("withdraw_request_wd_1.json", wdReq)

// Re-snapshot to assert STATE-04 left the state unchanged.
postBuildRoot := ls.Root()
if postBuildRoot != newRoot {
    die("STATE-04 mutated root: rootB=%s, after-build=%s", newRoot, postBuildRoot)
}
```

Có 2 đặc điểm:

1. **Build dựa trên state đã có sau STATE-03**: cùng `ls` đã apply `dep-1`. Vector phản ánh đúng kịch bản Alice.
2. **Assert root không đổi sau build**: nếu ai vô tình làm STATE-04 mutate state, generator báo `die()` ngay — vector không được sinh.

### Cách regenerate

```powershell
cd D:\HCMUS\Graduation\Project\Phase2\ganc-sys
go run ./p3/script-test/gen_state_vectors
```

Output:
```
rootA: 0xe4029e127d0d318624204f91c87aed84377819b97f1c80cc53edf9b35840805d
rootB: 0x9b325b4150d417adfd816930b6f291aaf9493995fe0f960864c616ff178f8620
withdrawRequest: wd-1 nonce: 1
wrote vectors into testvectors/alice_100_40
```

### Các file vector hiện có

| File | Sinh bởi task | Mục đích |
|---|---|---|
| `initial_state.json` | STATE-02 | rootA, accounts rỗng |
| `deposit_dep_1.json` | STATE-03 | DepositRecord canonical |
| `state_after_deposit.json` | STATE-03 | rootB, Alice balance=100 |
| `withdraw_request_wd_1.json` | **STATE-04** | Request canonical Alice rút 40 |

---

## 16. Hướng dẫn cho consumer khác (P2/P4/P5)

### Cho P2 (ZK prover, STATE-09)

Khi build witness, đọc `nonce` từ `withdraw_request_wd_1.json` làm input cho:

```
nullifier = Hash(userSecret, request.nonce)
```

Với canonical: `Hash("alice_secret", "1")`.

Public input `nullifier` của proof phải khớp với on-chain `nullifier` mà chain tính cho cùng nonce → tính nhất quán.

### Cho P4 (backend, INT-06)

Endpoint:

```
POST /api/withdraw-request
Body: { "owner": "...", "denom": "...", "amount": "...", "destination": "..." }
```

Pseudo-code handler:

```go
var body state.WithdrawIntent
if err := c.BindJSON(&body); err != nil {
    return c.JSON(400, gin.H{"error": "invalid JSON"})
}

req, err := withdrawBuilder.Build(body)
if err != nil {
    switch {
    case errors.Is(err, state.ErrInvalidWithdrawIntent):
        return c.JSON(400, gin.H{"error": err.Error()})
    case errors.Is(err, state.ErrInsufficientBalance):
        return c.JSON(422, gin.H{"error": err.Error()})
    default:
        return c.JSON(500, gin.H{"error": "internal error"})
    }
}

// lưu req vào storage P4 (in-memory map hay Postgres)
withdrawStore.Save(req)

return c.JSON(200, gin.H{"withdrawRequest": req})
```

Sau đó user ký bằng wallet, P4 cập nhật `req.Signature` và chuyển sang pipeline batch (STATE-05+).

### Cho P5 (frontend, FE-05)

Màn hình `Withdraw request`:
- Form: amount, destination.
- Submit → fetch `POST /api/withdraw-request`.
- Hiển thị response: `withdrawId`, `nonce`, `amount`, `destination`.
- Nút "Sign with wallet" → gọi wallet API → gắn signature.

---

## 17. Failure mode & HTTP mapping

| Tình huống | Trả về từ `Build` | HTTP suggest | Message cho user |
|---|---|---|---|
| Body JSON sai cú pháp | (P4 trả trước khi gọi Build) | 400 | "Yêu cầu không hợp lệ" |
| Field empty (owner/denom/destination) | `ErrInvalidWithdrawIntent: <field> is empty` | 400 | "Vui lòng nhập [field]" |
| Amount empty/zero | `ErrInvalidWithdrawIntent: amount "0" invalid: …` | 400 | "Số tiền phải > 0" |
| Amount âm | `ErrInvalidWithdrawIntent: amount "-5" invalid: …` | 400 | "Số tiền phải > 0" |
| Amount không phải số | `ErrInvalidWithdrawIntent: amount "abc" invalid: …` | 400 | "Số tiền không hợp lệ" |
| Balance < amount | `ErrInsufficientBalance: have X, want Y` | 422 | "Số dư không đủ. Hiện có X" |
| Account chưa từng deposit | `ErrInsufficientBalance: have 0, want Y` | 422 | "Số dư không đủ" |
| State corrupt (balance/nonce không phải số) | wrapped `corrupt …` | 500 | "Lỗi hệ thống, vui lòng thử lại" |

**Lưu ý cho P4**: khi map errors, dùng `errors.Is` để bắt sentinel — không dùng `err.Error() == "…"` hay `strings.Contains`. Test trên error wrapped chain.

---

## 18. Glossary

| Thuật ngữ | Nghĩa nhanh |
|---|---|
| **Denom** | Tên token theo cách Cosmos đặt. `"uusdc"` = micro USDC = 10^-6 USDC. |
| **Account** | Một dòng trong sổ cái off-chain, định danh bởi `(Owner, Denom)`. Có Balance và Nonce. |
| **Local state** | Bản sổ cái off-chain do P3 quản lý, đối ứng với on-chain `currentStateRoot`. |
| **Local root** | Hash của toàn bộ accounts hiện tại. `rootA`, `rootB`, `rootC` là các phiên bản theo thời gian. |
| **Nonce** | Bộ đếm withdrawal cho mỗi (owner, denom). Mỗi withdrawal +1. |
| **Nullifier** | `Hash(userSecret, nonce)` — identifier ẩn danh cho 1 withdrawal. Chain dùng để chống double-spend. |
| **Withdraw intent** | Input từ user: owner/denom/amount/destination. |
| **Withdraw request** | Output từ STATE-04: intent + withdrawId + nonce (+ signature do wallet gắn). |
| **Sentinel error** | Một `error` được khai báo dưới dạng biến package, dùng làm "mốc" để so sánh. |
| **`%w` verb** | Wrap error trong `fmt.Errorf`. Caller dùng `errors.Is` để phát hiện sentinel xuyên qua wrap chain. |
| **Critical section** | Đoạn code được bảo vệ bởi mutex — chỉ 1 goroutine chạy tại 1 thời điểm. |
| **Mutate / mutation** | Sửa đổi state. STATE-04 không mutate. STATE-05 mutate. |
| **Read-only** | Chỉ đọc, không sửa. |
| **Idempotent** | Gọi nhiều lần cho cùng kết quả (giống lần đầu). STATE-04 không idempotent vì `seq` tăng mỗi lần — nhưng STATE-05 sẽ idempotent dựa trên `nullifier`. |
| **Sequential ID** | ID được sinh theo thứ tự (1, 2, 3, …). Khác với UUID (ngẫu nhiên). |
| **Snapshot** | Bản chụp trạng thái tại một thời điểm. |
| **Big.Int** | Kiểu số nguyên không giới hạn của Go, dùng để xử lý số lớn an toàn. |

---

## File đã chạm

| File | Trạng thái |
|---|---|
| `internal/state/withdraw_request.go` | mới |
| `internal/state/withdraw_request_test.go` | mới |
| `p3/script-test/gen_state_vectors/main.go` | mở rộng (vector STATE-04 + assert root invariance) |
| `testvectors/alice_100_40/withdraw_request_wd_1.json` | mới |
| `p3/changenotes/2026-05-15-state-04.md` | mới |
| `p3/docs/STATE04_document.md` | mới (tài liệu này) |

---

## Câu hỏi thường gặp

**Q: Vì sao không lưu `WithdrawRequest` vào map trong builder?**
A: STATE-04 chỉ chịu trách nhiệm tạo struct. Việc lưu request là của P4 (backend storage). Tách trách nhiệm rõ ràng giúp test dễ hơn.

**Q: Nếu user submit cùng `intent` 2 lần, có 2 request hay 1?**
A: 2 request, ID khác nhau (`wd-1`, `wd-2`), cùng `nonce="1"` (nếu STATE-05 chưa chạy giữa hai lần). P4 nên dedupe theo idempotency key tự nó định nghĩa (ví dụ client-supplied request ID).

**Q: Vì sao `Signature` để trống, không yêu cầu user ký từ STATE-04?**
A: Vì wallet ký một payload đã canonical (đã có `withdrawId`, `nonce`). User không thể ký trước khi nhận response từ STATE-04. Quy trình thực: STATE-04 build → trả về client → client ký → client gửi signature lên P4 → P4 đính vào request.

**Q: Có thể rút về địa chỉ khác Owner không?**
A: Có. `Destination` tách rời `Owner` chính vì tính chất này. Ví dụ Alice rút về địa chỉ exchange.

**Q: `Account.Nonce` có bao giờ giảm không?**
A: Không. Nonce chỉ tăng. Nếu cần "huỷ" một withdrawal, không thể decrement — thay vào đó, withdrawal đó được đánh dấu rejected qua nullifier hoặc bị bỏ.

**Q: Sao không dùng UUID cho `WithdrawID`?**
A: Sequential ID dễ debug, dễ đọc log, đủ cho MVP single-process. Nếu mở rộng → đổi sang UUID không ảnh hưởng pipeline (xem [§12](#12-cấp-withdrawid)).
