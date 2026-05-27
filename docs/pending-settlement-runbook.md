# Pending Settlement Runbook

This runbook verifies the P3/P4 pending off-chain settlement flow.

## Purpose

This flow tests the production-direction off-chain settlement architecture:

```txt
Deposit indexed
-> pending deposit transition

Withdraw requested
-> pending withdrawal transition

Build pending batch
-> settlementUpdate + batchCommitments + witness
-> pending operations marked included

Generate proof
-> proofBundle

Submit batch
-> pending operations marked committed
-> committedRoot = pendingRoot = settlementUpdate.newStateRoot
````

## Important note about deposit

`POST /api/deposit` is still a dev-only substitute.

The real production flow is:

```txt
P5 wallet
-> sign MsgDeposit
-> broadcast to P1 chain
-> P4 indexes EventDeposit
-> DepositIndexer applies deposit to off-chain settlement
```

For local E2E, `POST /api/deposit` simulates the indexed deposit event.

## Required backend mode

Run the backend with:

```bash
OFFCHAIN_SETTLEMENT_ENABLED=true \
BATCH_BUILD_SOURCE=pending \
WITHDRAW_REQUEST_STORE=postgres \
WITHDRAW_RECORD_STORE=postgres \
BATCH_BUILD_STORE=postgres \
PROOF_BUNDLE_STORE=postgres \
SUBMIT_BATCH_STORE=postgres \
DATABASE_URL="postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable" \
go run ./cmd/api
```

Expected logs:

```txt
offchain settlement enabled=true
batch build source=pending
withdraw request store=postgres
withdraw record store=postgres
batch build store=postgres
proof bundle store=postgres
submit batch store=postgres
```

## Run the script

```bash
./scripts/e2e_pending_settlement_flow.sh
```

## Expected DB result

After successful submit:

```txt
offchain_pending_deposits.status = committed
offchain_pending_withdrawals.status = committed
offchain_state_cursors.committed_root = settlementUpdate.newStateRoot
offchain_state_cursors.pending_root = settlementUpdate.newStateRoot
offchain_state_cursors.last_committed_batch_id = batchId
```

## Manual DB check

```bash
docker exec -it ganc_sys_postgres psql -U ganc -d ganc_sys -c "
select deposit_id, status, batch_id, tx_hash from offchain_pending_deposits;
select withdraw_id, status, batch_id, tx_hash from offchain_pending_withdrawals;
select name, committed_root, pending_root, last_committed_batch_id from offchain_state_cursors;
"
```
