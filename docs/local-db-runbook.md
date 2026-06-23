# Local DB Runbook

This runbook explains how to run the P4 backend with PostgreSQL-backed persistence.

## What changes in DB mode?

The API contract does not change.

Same routes.
Same request bodies.
Same response bodies.

Only the persistence layer changes from in-memory storage to PostgreSQL.

## Persisted tables

| Data | Table |
|---|---|
| Withdraw requests | `withdraw_requests` |
| Batch build outputs | `batch_builds` |
| Proof bundles | `proof_bundles` |
| Batch submit results | `submit_batches` |
| Withdraw records | `indexed_withdraw_records` |
| Claim status | `indexed_withdraw_records.claimed` |

## Start Postgres

```bash
docker compose up -d postgres
````

## Run migrations

```bash
docker exec -i ganc_sys_postgres psql -U ganc -d ganc_sys < migrations/001_init.sql
docker exec -i ganc_sys_postgres psql -U ganc -d ganc_sys < migrations/002_withdraw_request_sequence.sql
docker exec -i ganc_sys_postgres psql -U ganc -d ganc_sys < migrations/003_offchain_settlement.sql
```

## Reset local DB workflow data

```bash
docker exec -it ganc_sys_postgres psql -U ganc -d ganc_sys -c "
DELETE FROM indexed_withdraw_records;
DELETE FROM submit_batches;
DELETE FROM proof_bundles;
DELETE FROM batch_builds;
DELETE FROM withdraw_requests;
ALTER SEQUENCE withdraw_request_seq RESTART WITH 1;
"
```

## Run backend in full DB mode

```bash
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
withdraw request store=postgres
withdraw record store=postgres
batch build store=postgres
proof bundle store=postgres
submit batch store=postgres
```

## Local E2E flow

This is still a local/dev flow.

`POST /api/deposit` is local/dev-only. In the real flow, P5 will sign and broadcast `MsgDeposit` directly from the wallet.

```txt
POST /api/deposit
-> POST /api/withdraw-request
-> POST /api/batch/build
-> POST /api/proof/generate
-> POST /api/batch/submit
-> POST /api/withdraw/claim
```

## Verify DB state

```bash
docker exec -it ganc_sys_postgres psql -U ganc -d ganc_sys \
  -c "select withdraw_id, owner_address, denom, amount, destination_address, nonce, status from withdraw_requests order by created_at;"
```

```bash
docker exec -it ganc_sys_postgres psql -U ganc -d ganc_sys \
  -c "select batch_id, old_state_root, new_state_root, status, created_at from batch_builds order by created_at;"
```

```bash
docker exec -it ganc_sys_postgres psql -U ganc -d ganc_sys \
  -c "select batch_id, proof, verification_key_id, status, created_at from proof_bundles order by created_at;"
```

```bash
docker exec -it ganc_sys_postgres psql -U ganc -d ganc_sys \
  -c "select batch_id, tx_hash, accepted, proof_status, submitted_at from submit_batches order by created_at;"
```

```bash
docker exec -it ganc_sys_postgres psql -U ganc -d ganc_sys \
  -c "select withdraw_id, owner_address, denom, amount, destination_address, nullifier, claimed, tx_hash from indexed_withdraw_records order by created_at;"
```

## Run tests

Normal tests:

```bash
go test ./...
```

DB tests:

```bash
RUN_DB_TESTS=1 DATABASE_URL="postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable" \
go test ./tests -run "TestDB" -v
```
