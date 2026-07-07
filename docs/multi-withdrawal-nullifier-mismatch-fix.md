# Fix: multi-withdrawal nullifier mismatch on rapid withdraws

## Symptom

Creating several withdraw requests in quick succession, then running the
settlement sequencer, fails at prove time:

```
[18:30:18] prove failed (HTTP 400): {"error":"remote prover returned 400:
  invalid prove request: settlementUpdate.withdrawals[0].nullifier mismatch:
  got  "0x4daf9858…de7fda4",
  expected "0xa6f2cea3…d271546""}
```

After the failure the affected withdraws can no longer be claimed — Claim
returns not-found and the sequencer keeps logging `idle (no pending)` or
`cannot continue transition chain from root …`.

## Root cause

### 1. A batch bundled more than one withdrawal, but the ZK stack is one-per-batch

The gazk settlement circuit **v1** proves exactly ONE withdrawal per batch. The
withdrawal nullifier is bound to a single per-account nonce:

- `gazk/prover/balance.go` only reads `witness.Accounts[0]`.
- `gazk/prover/settlement_circuit_v1.go` (`VerifyProof`) rejects
  `len(withdrawals) != 1`.
- `gazk/prover/nullifier.go` re-derives `NullifierFor(secret, account.Nonce)`
  and compares it against **every** withdrawal for that owner — using the one
  account nonce.

On the backend side:

- `BuildPendingBatch` put **all** pending withdrawals into a single batch.
- `internal/batch/witness.go` sets the witness account nonce to the **last**
  withdrawal's nonce only (`lastNonce`).

So when two withdrawals for the same account (say nonce 3 and nonce 4) landed in
one batch, the witness carried nonce 4. The prover then checked
`withdrawals[0]` (nonce-3 nullifier) against `NullifierFor(secret, 4)` → mismatch.

The exact numbers confirm it:

| value in error | `NullifierFor("mock-user-secret", n)` |
| --- | --- |
| got `0x4daf…` | **n = 2** (the stored withdrawal's nullifier) |
| expected `0xa6f2…` | **n = 4** (the witness account's single nonce) |

The first two batches succeeded only because each happened to contain a single
withdrawal — they were built on separate 8-second polls. The third poll caught
two queued withdrawals at once and broke.

### 2. Failed batches stranded their operations (can't claim)

`BuildPendingBatch` calls `MarkIncluded` on the ops **before** prove/submit. On a
prove/submit failure, `settle_loop.sh` simply returned — it never called the
existing `.../cancel` endpoint (`ReopenIncluded`). The withdrawals were left in
the `included` state: not `pending` (the loop skips them) and not `committed` (no
on-chain record), so Claim could never find them, and the next build failed with
`cannot continue transition chain`.

## Fix

### Part 1 — cap each batch at one withdrawal

`internal/service/offchain_settlement_service.go`

`BuildPendingBatch` now takes the ordered transition chain and keeps only the
prefix up to and including the **first withdrawal** (deposit-only chains take the
whole prefix), via the new helper `boundSingleWithdrawalPrefix`. It then:

- builds `Deposits` / `Withdrawals` from **only** the selected ids,
- sets `NewStateRoot` to the prefix's final `rootAfter` (not `cursor.PendingRoot`,
  which reflects the full pending set),
- builds the witness from the bounded prefix,
- `MarkIncluded` only the selected ids.

Remaining pending operations drain in subsequent sequencer passes. N rapid
withdrawals now settle as N sequential single-withdrawal batches, each valid.

### Part 2 — auto-recover a failed batch

`scripts/settle_loop.sh`

On any prove/submit failure (and on a not-accepted submit), the loop now calls:

```
POST /api/internal/offchain-settlement/batches/{batchId}/cancel  {"reason": "..."}
```

which `ReopenIncluded`s the batch. The stranded ops return to `pending` and the
next pass rebuilds them (now correctly bounded). This makes the sequencer
self-healing for transient failures instead of stranding withdrawals.

## Tests

- `tests/int_db_offchain_settlement_single_withdrawal_test.go` (new) — creates
  two pending withdrawals for one account and asserts each build yields a batch
  with exactly one withdrawal, in chain order, with `NewStateRoot` pinned to that
  withdrawal's own `rootAfter` and the witness nonce matching. Gated behind
  `RUN_DB_TESTS=1`.
- Existing `int_db_offchain_settlement_build_test.go` (1 deposit + 1 withdrawal)
  still holds: the bounded prefix equals the whole chain, so
  `NewStateRoot == PendingRoot == withdrawTransition.RootAfter`.

## Verify on Codespace

```bash
# 1. rebuild + restart BE (real DB mode auto-resets offchain tables)
bash scripts/real_db_mode_up.sh

# 2. run the regression test against the live DB
RUN_DB_TESTS=1 go test ./tests/ -run TestDBOffchainSettlementBoundsBatchToSingleWithdrawal -v

# 3. e2e: deposit (as alice) then fire several withdraws back-to-back, run the loop
bash scripts/settle_loop.sh
#    expect: batch-1 SETTLED, batch-2 SETTLED, batch-3 SETTLED, … one per withdrawal
#    then Claim each on the FE
```

## Note / follow-up

This bounds the batch to the circuit's current one-withdrawal limit; it does not
raise that limit. Supporting multiple withdrawals per batch is a gazk (P2) circuit
change — a per-withdrawal nonce vector instead of one account nonce — tracked in
the Orderbook/Matching plan, not here.
