# P3 — STATE-07: Compute Withdraw Address Hash

## 1. Nhiệm vụ

STATE-07 nhận `destination` (địa chỉ nhận coin khi user claim) và trả về `withdrawAddressHash` deterministic, dùng làm:

1. **Public input** của ZK proof (P2 prover + P1 verifier — ZK-07).
2. **Anti-tampering binding** giữa `SettlementUpdate.WithdrawAddress` và proof: nếu relayer rewrite `WithdrawAddress` giữa prover và chain, hash mismatch → proof reject.
3. **Đầu vào** cho `SettlementUpdate` (STATE-08) trong field `withdrawAddressHash`.

Output là `0x`-prefixed lowercase hex (theo Agreements: "Roots/nullifiers/proofs are hex strings").

## 2. Luồng hoạt động

```
STATE-04 (build WithdrawRequest) ──► destination ──► STATE-07 ──► withdrawAddressHash
                                                                          │
                                          ┌───────────────────────────────┼───────────────────────────┐
                                          ▼                               ▼                           ▼
                                  STATE-08 SettlementUpdate       STATE-09 witness builder       STATE-10 public inputs
                                  .WithdrawAddressHash                  (P2)                     [oldRoot,newRoot,
                                                                                                  depAmt,wdAmt,
                                                                                                  ▶ wdAddrHash ◀,
                                                                                                  nullifier]
```

Trong backend:

```
P4 nhận POST /api/withdraw-request
       │
       ▼
STATE-04 build → req.Destination
       │
       ▼
STATE-07 derive → withdrawAddressHash
       │
       ▼
STATE-08 SettlementUpdate{withdrawAddress, withdrawAddressHash, ...}
       │
       ▼
P2 prover ─► proofBundle (bound to addrHash)
       │
       ▼
P1 MsgSubmitBatchProof
       │
       ▼ (verify)
re-derive addrHash from update.WithdrawAddress, so sánh public input
       │
       ▼ (accept)
WithdrawRecord{destination = update.WithdrawAddress}
       │
       ▼ (claim time)
x/bank.SendCoinsFromModuleToAccount(WithdrawRecord.Destination)
```

## 3. Hàm chính

### 3.1. `WithdrawAddressHash(destination string) (string, error)`

Pure function. Không state, không mutex, không I/O. Thực thi:

1. **Trim destination**: `strings.TrimSpace`.
2. **Validate non-empty**: empty / whitespace-only → `ErrInvalidWithdrawAddress`.
3. **Build hash input**: `withdrawAddressDomainTag + "|" + canonicalDestination`.
4. **Hash**: `SHA256Hex(...)` từ `pkg/hash` → trả `"0x"` + lowercase hex 64 ký tự.

Composition công thức:

```
withdrawAddressHash = SHA256( "zkdex/withdrawAddr/v0" | canonical(destination) )
```

### 3.2. `WithdrawAddressDomainTag() string`

Accessor cho domain tag hiện hành. P2 (circuit assert) và P4 (sanity check trước khi submit) dùng để verify off-chain derivation chưa silently bump version.

### 3.3. Sentinel error

| Sentinel | Khi nào | HTTP map |
|---|---|---|
| `ErrInvalidWithdrawAddress` | destination empty / whitespace-only sau trim | 400 |

## 4. Quyết định thiết kế

### 4.1. Vì sao SHA-256 thay vì Poseidon ngay?

Giống STATE-06:

- `pkg/hash` đã có SHA-256, không kéo thêm dependency cho MVP.
- ZK-02 chưa lock circuit hash — chốt sớm sẽ bị rework khi đổi stack.
- Domain tag versioned (`v0`) là escape hatch: khi swap Poseidon chỉ cần bump `v1` và regenerate vector.

### 4.2. Vì sao domain tag tách rời `nullifierDomainTag`?

| Callsite | Domain tag |
|---|---|
| `NullifierFor` | `"zkdex/nullifier/v0"` |
| `WithdrawAddressHash` | `"zkdex/withdrawAddr/v0"` |

Mặc dù cả hai dùng SHA-256 placeholder, dùng chung tag sẽ mở cửa cho attacker chọn `destination` có hình dạng `"alice_secret|1"` — input của nullifier-without-domain — để tạo hash trùng nullifier hợp lệ. Test `TestWithdrawAddressHash_DomainSeparatedFromNullifier` cover.

Chi phí: 1 hằng bổ sung. Lợi ích: khoá hoàn toàn cross-domain forgery, ngay cả khi attacker control phần lớn input.

### 4.3. Vì sao canonicalization chỉ là TrimSpace?

| Phương án | Ưu | Nhược |
|---|---|---|
| **TrimSpace only** *(đã chọn)* | Đồng bộ với `WithdrawRequestBuilder.Build` (STATE-04). Không lock-in semantics. | Mixed-case input không tự sửa. |
| TrimSpace + lowercase | "Cứu" được mixed-case bech32. | Phá vỡ tương lai với EVM checksummed address (case-sensitive). |
| TrimSpace + bech32 validate | Reject malformed sớm. | Lock vào bech32; thêm dependency; sai chỗ — bech32 validation là việc của `x/bank` khi claim. |

Trách nhiệm "đẩy" validate format ra ngoài là cố ý:

- **P5 UI**: định hướng user nhập đúng prefix.
- **P4 backend**: sanitize trước khi forward.
- **P1 chain `x/bank`**: rejected nếu sai khi `SendCoinsFromModuleToAccount`.

STATE-07 chỉ guarantee: **bytes prover hash = bytes chain verifier hash**.

### 4.4. Vì sao tách STATE-07 ra hàm pure thay vì attach vào WithdrawRequest?

- `types.WithdrawRequest` đã có `Destination`; thêm `WithdrawAddressHash` field sẽ duplicate state (hash derive được từ destination).
- Pure function → unit test cô lập, không cần seed builder.
- Caller pattern rõ:
  ```go
  req, _    := wb.Build(intent)
  addrHash, _ := state.WithdrawAddressHash(req.Destination)
  ```

### 4.5. Vì sao test có `MatchesWithdrawRequestDestination`?

Test này khoá hợp đồng STATE-04 ↔ STATE-07. Nếu sau này STATE-04 thêm canonicalization (vd. lowercase), STATE-07 phải follow trong cùng commit — test sẽ fail trước khi prover/verifier bị silent drift.

```go
req, _ := wb.Build(state.WithdrawIntent{
    Destination: "  cosmos1alice  ",   // whitespace input
})
fromRequest, _ := state.WithdrawAddressHash(req.Destination)   // trimmed
fromIntent,  _ := state.WithdrawAddressHash("cosmos1alice")    // already clean
// fromRequest == fromIntent == canonicalWithdrawAddressHash
```

## 5. Ví dụ

### 5.1. Happy path

```go
hash, err := state.WithdrawAddressHash("cosmos1alice")
// hash == "0xa75ac956249df4c45b83281c5af6187c59df9709fd1c25b5e61b12d71a8eb417"
// err == nil
```

### 5.2. Tích hợp với STATE-04

```go
ls := state.NewLocalState()
ls.ApplyDeposit(types.DepositRecord{DepositID: "dep-1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "100"})

wb := state.NewWithdrawRequestBuilder(ls)
req, _ := wb.Build(state.WithdrawIntent{
    Owner:       "cosmos1alice",
    Denom:       "uusdc",
    Amount:      "40",
    Destination: "cosmos1alice",
})

addrHash, _ := state.WithdrawAddressHash(req.Destination)
// addrHash == "0xa75ac956…b417"
```

### 5.3. Failure modes

| Input | Kết quả |
|---|---|
| `""` | `ErrInvalidWithdrawAddress: destination is empty` |
| `"   "` | `ErrInvalidWithdrawAddress: destination is empty` (trim) |
| `"\t\n  "` | `ErrInvalidWithdrawAddress: destination is empty` (trim) |

### 5.4. Whitespace canonicalization

```go
a, _ := state.WithdrawAddressHash("cosmos1alice")
b, _ := state.WithdrawAddressHash("  cosmos1alice  ")
// a == b
```

### 5.5. Case-sensitivity (giữ nguyên)

```go
a, _ := state.WithdrawAddressHash("cosmos1alice")
b, _ := state.WithdrawAddressHash("Cosmos1Alice")
// a != b — STATE-07 KHÔNG lowercase. UI/P4 chịu trách nhiệm normalize trước.
```

### 5.6. Domain separation với nullifier

```go
addr, _ := state.WithdrawAddressHash("alice_secret|1")
null, _ := state.NullifierFor("alice_secret", "1")
// addr != null — domain tag khác nhau bảo vệ.
```

## 6. Vector canonical

`testvectors/alice_100_40/withdraw_address_hash_wd_1.json`:

```json
{
  "withdrawId": "wd-1",
  "destination": "cosmos1alice",
  "domainTag": "zkdex/withdrawAddr/v0",
  "hashAlgorithm": "sha256",
  "withdrawAddressHash": "0xa75ac956249df4c45b83281c5af6187c59df9709fd1c25b5e61b12d71a8eb417"
}
```

Khi ZK-02 lock Poseidon, regenerate vector:

```bash
go run ./p3/script-test/gen_state_vectors
```

Đồng thời update constant `canonicalWithdrawAddressHash` trong `withdraw_address_hash_test.go`.

## 7. Quy trình bump version (`v0 → v1`)

1. Thay implementation `WithdrawAddressHash` để dùng hash mới (Poseidon).
2. Đổi hằng `withdrawAddressDomainTag` từ `"zkdex/withdrawAddr/v0"` → `"zkdex/withdrawAddr/v1"`.
3. Chạy generator: `go run ./p3/script-test/gen_state_vectors`.
4. Update `canonicalWithdrawAddressHash` trong `internal/state/withdraw_address_hash_test.go` về giá trị mới.
5. Chạy `go test ./...` → PASS.
6. Notify P2 (regen proof), P1 (re-verify integration test), P4 (clear cached hash nếu có).
7. Commit kèm cả 3 thay đổi.

Lưu ý: nếu bump song song với `nullifierDomainTag` (likely scenario khi ZK-02 chốt Poseidon cho toàn bộ circuit), update cả 2 vector trong cùng commit để consumer không phải merge nhiều lần.

## 8. Quan hệ giữa các STATE đã hoàn thành

```
STATE-01  Shared structs (WithdrawRequest schema)
   │
   ▼
STATE-02  Initial local state (rootA, balance=0, nonce=0)
   │
   ▼
STATE-03  Apply deposit (rootA → rootB, balance=100)
   │
   ▼
STATE-04  Build WithdrawRequest (nonce=1, destination="cosmos1alice", trim applied)
   │
   ├──► STATE-06  NullifierFor(secret, req.Nonce) → 0x1a1f…22f7
   │
   └──► STATE-07  WithdrawAddressHash(req.Destination) → 0xa75a…b417   ◄── ĐANG TRIỂN KHAI
                                                  │
                                                  ▼
                                          STATE-05  ApplyWithdrawal(req, nullifier)
                                                  │
                                                  ▼
                                          rootB → rootC, balance=60, nonce=1
                                                  │
                                                  ▼
                                          STATE-08  Build SettlementUpdate (tiếp theo)
                                                  ├── OldStateRoot = rootB
                                                  ├── NewStateRoot = rootC
                                                  ├── WithdrawAddress = req.Destination
                                                  ├── WithdrawAddressHash = (STATE-07 output)
                                                  └── Nullifier = (STATE-06 output)
```

## 9. File đã chạm

| File | Trạng thái |
|---|---|
| `internal/state/withdraw_address_hash.go` | mới — implementation |
| `internal/state/withdraw_address_hash_test.go` | mới (9 test + 6 sub) |
| `p3/script-test/gen_state_vectors/main.go` | thêm emitter cho `withdraw_address_hash_wd_1.json` |
| `testvectors/alice_100_40/withdraw_address_hash_wd_1.json` | mới |
| `p3/changenotes/2026-05-24-state-07.md` | changenote |
| `p3/docs/STATE07_document.md` | tài liệu này |
