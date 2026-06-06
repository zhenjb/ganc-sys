# Remote Prover E2E Runbook

This runbook verifies that `ganc-sys` uses the remote `gazk` prover instead of the local mock prover.

## Services

Run three services/terminals.

## Terminal 1: Postgres

Start Postgres:

    docker compose up -d postgres

Apply migrations if the database is empty:

    for f in migrations/*.sql; do
      echo "==> applying $f"
      docker exec -i ganc_sys_postgres psql -U ganc -d ganc_sys -v ON_ERROR_STOP=1 < "$f"
    done

Verify tables:

    docker exec -it ganc_sys_postgres psql -U ganc -d ganc_sys -c "\dt"

## Terminal 2: gazk

Start the remote prover:

    cd /workspaces/gazk
    go run main.go server

Expected health check:

    curl -s http://localhost:8090/health | jq

Expected response:

    {
      "service": "gazk",
      "status": "ok",
      "verificationKeyId": "gazk-balance-smoke-v1"
    }

## Terminal 3: ganc-sys

Start `ganc-sys` with remote prover mode:

    cd /workspaces/ganc-sys

    OFFCHAIN_SETTLEMENT_ENABLED=true \
    BATCH_BUILD_SOURCE=pending \
    PROVER_MODE=remote \
    PROVER_URL=http://localhost:8090 \
    WITHDRAW_REQUEST_STORE=postgres \
    WITHDRAW_RECORD_STORE=postgres \
    BATCH_BUILD_STORE=postgres \
    PROOF_BUNDLE_STORE=postgres \
    SUBMIT_BATCH_STORE=postgres \
    DATABASE_URL="postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable" \
    go run ./cmd/api

Important env flags:

    PROVER_MODE=remote
    PROVER_URL=http://localhost:8090

Without these, `ganc-sys` may use the local prover.

## Run E2E

In another terminal:

    cd /workspaces/ganc-sys
    ./scripts/e2e_pending_settlement_remote_prover_flow.sh

## Expected proof result

During `Generate proof`, the response must contain:

    {
      "proofBundle": {
        "verificationKeyId": "gazk-balance-smoke-v1",
        "publicInputs": [
          "oldStateRoot",
          "newStateRoot",
          "depositsRoot",
          "withdrawalsRoot",
          "nullifiersRoot",
          "withdrawOutputsRoot"
        ]
      }
    }

The value `gazk-balance-smoke-v1` proves that the proof came from `gazk`, not the local prover.

## Expected final DB state

After successful submit:

    offchain_pending_deposits.status = committed
    offchain_pending_withdrawals.status = committed
    offchain_state_cursors.committed_root = settlementUpdate.newStateRoot
    offchain_state_cursors.pending_root = settlementUpdate.newStateRoot
    offchain_state_cursors.last_committed_batch_id = batch-1

Manual DB check:

    docker exec -it ganc_sys_postgres psql -U ganc -d ganc_sys -c "
    select deposit_id, status, batch_id, tx_hash from offchain_pending_deposits;
    select withdraw_id, status, batch_id, tx_hash from offchain_pending_withdrawals;
    select name, committed_root, pending_root, last_committed_batch_id from offchain_state_cursors;
    "

## Current limitation

`gazk` currently uses a real gnark Groth16 smoke circuit for the balance transition:

    oldBalance + depositAmount = newBalance + withdrawAmount

It does not yet prove:

    nullifier = Hash(userSecret, nonce)
    destinationHash = Hash(destination)
    state root transition constraints

Those are later `gazk` tasks.
