# Trade test vectors — `trade_alice_bob` (STATE-T11)

**Single source of truth** for P1 (verifier), P2 (prover/circuit) and P4 (backend)
to cross-check trading matching, commitments and reject reasons bit-for-bit.

Every expected value here is **generated from the STATE-T01..T10 code** — no
hand-authored roots (plan pitfall). Regenerate, never hand-edit:

```bash
go run ./p3/script-test/gen_trade_vectors
```

## Canonical scenario

Market **ATOM/USDC** (base `uatom`, quote `uusdc`, tick `0.1`, lot `1`,
makerFee `50` bps, takerFee `100` bps, `active`).

| Party | Order | Funded |
|---|---|---|
| Alice | **buy** 20 @ 100 (rests first → maker) | 5000 `uusdc` |
| Bob | **sell** 20 @ 100 (taker) | 50 `uatom` |

→ one Fill: price `100` (maker), qty `20`, makerFee `10`, takerFee `20`,
buyer alice, seller bob. Conservation holds (uusdc 5000, uatom 50 unchanged).

## Files

| File | STATE task | Schema | Consumers |
|---|---|---|---|
| `market.json` | T01/T03 | `types.Market` | P2,P3,P4,P5 |
| `initial_state.json` | T02 | `[]TradeBalanceRecord` | P2,P3,P4 |
| `orders.json` | T03 | `[]TradeOrderRecord` (order+hash+nullifier+verdict) | P1,P2,P4 |
| `fills.json` | T05 | `[]types.Fill` (matching order) | P1,P2,P4 |
| `roots.json` | T06/T07/T08 | `TradeRootsVector` (old/new/orders/trades/tradeBatchCommitment) | P1,P2 |
| `state_after_trade.json` | T06 | `[]TradeBalanceRecord` (incl. feeAccount) | P2,P3,P4 |
| `settlement_update.json` | T08 | `types.SettlementUpdate` (with `trades[]`) | P1,P2,P4 |
| `batch_commitments.json` | T08 | `types.BatchCommitments` (+tradesRoot/ordersRoot) | P1,P2,P4 |
| `public_inputs.json` | T08 | 8-input vector `[0..5]`+`[6]=tradesRoot,[7]=ordersRoot` | P1,P2,P4 |
| `witness.json` | T09 | `types.Witness{Trade}` (avail/reserved old+new) | P2 |
| `failure_vectors/*.json` | T02/T03/T05 | `TradeFailureVector` | P1,P2,P4 |

## Failure vectors

| File | Stage | Expected |
|---|---|---|
| `forged_signature.json` | validation (T03) | reject `bad_signature` (price tampered after signing) |
| `non_crossing.json` | matching (T05) | 0 fills (bid 99 < ask 100) |
| `over_reserve.json` | validation (T03) | reject `insufficient_available` (needs 2020, has 21; T02 Reserve would also reject) |

## Verification

`pkg/testvectors` re-derives fills + roots from the recorded orders via the live
T05/T07 code and re-runs each failure vector:

```bash
go test ./pkg/testvectors/ -run TradeVectors -v
```

`MANIFEST.json` binds every file's SHA-256; a hand-edit fails
`TestTradeVectorsManifestIntegrity`. Bump `vectorVersion` when ZK-T01 locks the
circuit hash (MiMC/Poseidon) and regenerate.
