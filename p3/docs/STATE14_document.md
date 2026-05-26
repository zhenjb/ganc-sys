# STATE-14 — Persistent off-chain state manager

## Mục tiêu

STATE-14 đóng vai trò **long-lived pending off-chain state mirror** cho
toàn bộ vòng đời process P4. Theo flow diagram
(`zkdex_cosmos_deposit_withdraw_flows.html`):

- Deposit bước 6: indexer đọc `DepositQueued` event, credit pending
  off-chain balance, recompute local state root.
- Withdraw bước 2-3: off-chain validate balance ≥ amount, debit
  balance, advance nonce, compute `newStateRoot`.

Hai dòng này YÊU CẦU một entity duy nhất giữ pending state qua nhiều
HTTP request, nhiều batch, và nhiều lần submit. STATE-13 (LocalBuilder)
trước đây tạo `LocalState` mới cho MỖI batch — shortcut này không phản
ánh đúng pending nature của off-chain mirror.

Theo Agreements (mục P3 task table):

> **STATE-14** — Long-lived `OffchainStateManager` mirroring the pending
> off-chain state from the flow diagram (deposit step 6, withdraw steps
> 2-3). Validates withdraw requests at request-time, debits pending
> balance immediately, rolls back when proof submit fails. Replaces the
> current "fresh state per batch" shortcut. Builder snapshots from
> manager instead of building from scratch.
>
> **Output**: `state.OffchainStateManager` với thread-safe
> Apply/Rollback/Snapshot.

## Vị trí trong pipeline

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ P4 INT-05 deposit indexer                                                   │
│   chain event → DepositRecord → manager.ApplyDeposit(d)  ◄── (P4 chưa wire) │
│                                                                             │
│ P4 INT-06 withdraw API                                                      │
│   user input → derive nullifier → manager.ApplyWithdrawRequest(req,         │
│   nullifier)                                              ◄── (P4 chưa wire)│
│                                                                             │
│ P4 INT-07 batch build                                                       │
│   snap := manager.Snapshot()                              ◄── (P4 chưa wire)│
│   builder consume snap → SettlementUpdate / Witness                         │
│                                                                             │
│ P4 INT-09 batch submit                                                      │
│   submit ok    → giữ state                                                  │
│   submit fail  → manager.Rollback(snap)                   ◄── (P4 chưa wire)│
└─────────────────────────────────────────────────────────────────────────────┘
```

STATE-14 ship phần **primitive** (manager + snapshot + constructor seed
LocalState từ snapshot). P4 sẽ wire INT-05/06/07/09 ở changeset riêng —
xem mục "Việc còn lại" cuối tài liệu.

## API chi tiết

### `state.OffchainStateManager`

| Method | Vai trò | Lỗi điển hình |
|---|---|---|
| `NewOffchainStateManager() *OffchainStateManager` | Khởi tạo. Root khớp `NewLocalState()` → genesis chain (ONCHAIN-03). | — |
| `ApplyDeposit(d types.DepositRecord) (string, error)` | INT-05 indexer gọi. Credit balance, advance root. Idempotent theo `depositId`. | `ErrDepositAlreadyApplied`, `ErrInvalidDepositRecord` |
| `ApplyWithdrawRequest(req types.WithdrawRequest, nullifier string) (string, error)` | INT-06 gọi tại request-time. Validate, debit, mark nullifier consumed. | `ErrInsufficientBalance`, `ErrNonceMismatch`, `ErrWithdrawAlreadyApplied`, `ErrInvalidWithdrawRequest` |
| `Snapshot() Snapshot` | Trả về deep-copy immutable view. An toàn dùng song song với mutator. | — |
| `Rollback(snap Snapshot)` | Restore state về snapshot. Snapshot vẫn reusable. | — (caller chịu trách nhiệm snapshot hợp lệ) |
| `Root() string` | Pending root hiện tại. | — |
| `Account(owner, denom string) types.Account` | Balance + nonce snapshot. Default `{0, 0}` nếu chưa tồn tại. | — |
| `IsDepositApplied(depositID string) bool` | Pre-check idempotency. | — |
| `IsNullifierApplied(nullifier string) bool` | Pre-check replay. | — |
| `Generation() uint64` | Số mutation thành công (debug/log). | — |

### `state.Snapshot`

Immutable value type, fields private. Chỉ mint qua
`OffchainStateManager.Snapshot()`. Zero value an toàn:

```go
var s state.Snapshot
s.Root()                          // ""
s.Account("alice","uusdc")        // {Owner:"alice", Denom:"uusdc", Balance:"0", Nonce:"0"}
s.IsDepositApplied("anything")    // false
```

| Method | Vai trò |
|---|---|
| `Root() string` | Root tại thời điểm chụp. |
| `Account(owner, denom string) types.Account` | Balance + nonce trong snapshot. |
| `Accounts() []types.Account` | Sort theo (owner, denom). Copy mới mỗi lần gọi. |
| `IsDepositApplied(id string) bool` | Snapshot có biết tới depositId này không. |
| `IsNullifierApplied(n string) bool` | Snapshot có ghi nhận nullifier này không. |
| `Generation() uint64` | Generation của manager khi snapshot được mint. |

### `state.NewLocalStateFromSnapshot(snap Snapshot) *LocalState`

Constructor đặc biệt phục vụ batch builder integration:

```go
ls := state.NewLocalStateFromSnapshot(manager.Snapshot())
// ls.Root() == snap.Root()
// ls.Account(...) == snap.Account(...)
// ls.IsDepositApplied(id) == snap.IsDepositApplied(id)
// Mutate ls không ảnh hưởng snapshot hoặc manager
```

Là điểm tích hợp duy nhất giữa manager (mutable, long-lived) và
LocalBuilder (per-batch, ephemeral). STATE-14 KHÔNG sửa LocalBuilder —
hook này chừa cho follow-up (P4 hoặc STATE-15).

## Mô hình tương tranh

### Bố cục lock

- Một mutex duy nhất `m.mu` (sync.Mutex) ở manager level.
- Tất cả mutator (`ApplyDeposit`, `ApplyWithdrawRequest`, `Rollback`)
  hold `m.mu` suốt phép operation.
- Tất cả reader (`Snapshot`, `Root`, `Account`, `IsDepositApplied`,
  `IsNullifierApplied`, `Generation`) cũng hold `m.mu` — đổi consistent
  view lấy ít contention; sẽ swap sang `sync.RWMutex` nếu profile sau
  này yêu cầu.

### Tuân thủ với LocalState

Manager wrap `LocalState`, không reimplement. `LocalState.mu` và
`AccountState.mu` vẫn được giữ — không deadlock vì:

```
m.mu.Lock()                              (level 1)
  ls.ApplyDeposit(d):
    s.mu.Lock()                          (level 2)
      s.accounts.Credit(...):
        s.accounts.mu.Lock()             (level 3)
        s.accounts.mu.Unlock()
    s.mu.Unlock()
m.mu.Unlock()
```

Mọi lock đi theo cùng thứ tự, không có nested cross-locking. Test
`TestOffchainStateManager_ConcurrentSnapshotWhileApplying` (8 reader +
8 writer × 20 ops) chạy với `-race` sạch.

### Snapshot là deep-copy

```go
accCopy := make(map[accountKey]types.Account, len(m.ls.accounts.accounts))
for k, v := range m.ls.accounts.accounts {
    accCopy[k] = v   // types.Account là value type → copy hoàn toàn
}
```

Vì `LocalState.accounts` sau đó tiếp tục mutate cùng map gốc (qua
`Credit/Debit`), nếu chỉ shallow-copy thì mutation kế tiếp sẽ leak vào
snapshot đã trả ra. Test
`TestOffchainStateManager_Snapshot_ImmutableAfterFurtherMutation` lock
ràng buộc này.

## Mô hình rollback

```
t0: manager state = S0
t1: snap := manager.Snapshot()                  // snap captures S0
t2: manager.ApplyDeposit(...)                   // state advances to S1
t3: manager.ApplyWithdrawRequest(...)           // state advances to S2
t4: <batch submit fails>
t5: manager.Rollback(snap)                      // state reset to S0

      Snapshot.gen=0, manager.gen=3 (S0 → S1 → S2 → rollback bump)
```

- `Rollback` chỉ thay thế internal `LocalState` bằng một bản mới
  derive từ snapshot data. Snapshot KHÔNG bị consume → có thể rollback
  cùng snapshot nhiều lần (test
  `TestOffchainStateManager_Rollback_SnapshotReusableAfterRollback`
  lock).
- Generation tăng → log thấy "đã từng rollback". Snapshot.gen giữ
  nguyên giá trị lúc chụp.

### Trade-off: KHÔNG validate ancestry

Rollback API hiện không kiểm "snap có phải là tổ tiên hợp lệ của state
hiện tại". Lý do:

- API mỏng, dễ hiểu.
- Caller (P4 INT-09) luôn chụp snapshot ngay trước batch build và
  rollback ngay sau khi submit fail trong cùng request → khó nhầm.
- Validate ancestry đòi thêm Merkle/log mutations → đắt và phức tạp
  cho MVP.

Nếu cần safeguard, caller có thể so sánh
`manager.Generation() > snap.Generation()` trước khi rollback.

## Các kịch bản đã được test bằng demo

`p3/script-test/state14_manager_demo/main.go` chạy hai trace
(`go run ./p3/script-test/state14_manager_demo`):

**Happy path** — deposit → withdraw → batch → submit accept:

```
[init]               root=0xe4029e12…40805d gen=0
[after dep-1=100]   root=0x9b325b41…8f8620 gen=1 balance=100 nonce=0
[after wd-1=40]     root=0x44d60f77…76250c gen=2 balance=60 nonce=1 nullifier=0xac441258…48ea91
[snapshot for batch] root=0x44d60f77…76250c gen=2 (immutable)
[submit accept]      manager state unchanged. final balance=60
```

**Rollback path** — submit FAIL → rollback:

```
[after deposit]      balance=100
[pre-batch snapshot] root=0x9b325b41…8f8620
[after withdraw]     balance=60 nonce=1 nullifier_consumed=true
[submit FAIL]        rolling back to pre-batch snapshot...
[after rollback]     root=0x9b325b41…8f8620 balance=100 nonce=0 nullifier_consumed=false gen=3
[ok]                 rollback restored pre-batch state correctly
```

Rollback ROLL BACK đúng:

- Balance: 60 → 100 (hoàn lại 40 đã debit).
- Nonce: 1 → 0 (rollback withdrawal đã làm tăng nonce).
- Nullifier: consumed → cleared (user có thể submit lại request với
  cùng nonce/secret).
- Deposit dep-1 (có TRƯỚC snapshot): vẫn ghi nhận → đúng vì nó là
  một sự kiện chain confirm độc lập, không thuộc batch failed.

## Liên hệ với các STATE đã có

| STATE | Cách STATE-14 dùng/tôn trọng |
|---|---|
| STATE-01..02 `AccountState`, `NewAccountState` | Manager wrap `LocalState` → wrap `AccountState`. Không reimplement primitive. |
| STATE-03 `ApplyDeposit` | `manager.ApplyDeposit` delegate. Idempotency `depositId` được thừa kế. |
| STATE-04 `WithdrawRequestBuilder.Build` | KHÔNG bị manager replace. P4 (hoặc P3) tiếp tục dùng để mint `withdrawId/nonce`. Manager nhận `WithdrawRequest` đã được build sẵn. |
| STATE-05 `ApplyWithdrawal` | `manager.ApplyWithdrawRequest` delegate. Tất cả validate balance/nonce/nullifier giữ nguyên. |
| STATE-06 `NullifierFor(secret, nonce)` | Caller tự derive trước khi gọi manager. Manager không động vào secret. |
| STATE-07 `WithdrawAddressHash` | Không liên quan ở manager — builder (STATE-08) mới cần. |
| STATE-08 `SettlementUpdateBuilder` | Khi LocalBuilder seed từ snapshot, settlement input vẫn là cùng schema. Tích hợp đầy đủ thuộc về P4 wire follow-up. |
| STATE-09 `WitnessBuilder` | Tương tự STATE-08 — không bị manager thay. |
| STATE-10 public input builder | Vô can. |
| STATE-11 canonical vector folder | Không tái sinh — manager tests dùng helper riêng (`mgrAlice*`) tách biệt. |
| STATE-12 failure vectors | Không tái sinh. |
| STATE-13 `LocalBuilder` | KHÔNG bị sửa ở STATE-14 (giữ INT-02/INT-07 xanh). Hook tương lai là `NewLocalStateFromSnapshot`. |

## Việc còn lại / hand-off cho P4

STATE-14 đã giao đủ primitive theo spec. Bốn điểm cần P4 wire trong
changeset tiếp theo:

1. **INT-05 indexer**: sau `i.depositRepository.SaveDeposit(ctx, record)`,
   gọi `manager.ApplyDeposit(record)`. Nếu manager trả
   `ErrDepositAlreadyApplied` → log info, không fail (replay tự nhiên).

2. **INT-06 withdraw service**: trước
   `r.store.SaveWithdrawRequest(...)`, derive nullifier
   `state.NullifierFor(secret, nonce)` và gọi
   `manager.ApplyWithdrawRequest(req, nullifier)`. Nếu manager trả
   `ErrInsufficientBalance` → HTTP 400 `"insufficient pending balance"`.
   Nếu trả `ErrNonceMismatch` → HTTP 400. KHÔNG save request nếu manager
   reject.

3. **INT-07 + INT-09 batch lifecycle**:
   - Trước build, gọi `snap := manager.Snapshot()` và LƯU cùng batch.
   - Builder seed từ snapshot (cần option mới ở LocalBuilder hoặc
     manager.PrepareBatch helper).
   - Submit thành công → `snap` discard, state stays.
   - Submit fail → `manager.Rollback(snap)`.

4. **INT-11 state endpoint**: trả balance/nonce từ
   `manager.Account(owner, denom)` thay vì repo deterministic. UI sẽ
   thấy pending state, không phải confirmed state.

Sau khi P4 wire xong, `LocalBuilder.Build` (STATE-13) cần một option
`WithSnapshot(snap)` để seed `LocalState` từ snapshot thay vì
`NewLocalState()` — nếu không, builder sẽ tự apply deposit/withdraw
lần nữa trên state đã được manager mutate (double-apply, fail
`ErrDepositAlreadyApplied`).
