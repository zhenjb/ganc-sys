# Manual Backend ↔ ZK Test (one command)

Script: [`scripts/manual_backend_zk_test.sh`](../scripts/manual_backend_zk_test.sh)

End-to-end manual test proving the **real ZK path** between the backend
(`ganc-sys`) and the prover/verifier (`gazk`). On-chain (P1) is **not** involved
— `RELAYER_MODE=local` (mock relayer). The script manages the whole lifecycle,
prints a clear `[ PASS ]` / `[ FAIL ]` per phase, and exits non-zero on any
failure.

## Run

```bash
cd ganc-sys
bash scripts/manual_backend_zk_test.sh
```

Requirements: `docker`, `go`, `curl`, `python`, Git Bash. It starts Postgres
(via `docker compose`), `gazk` (:8090) and the backend (:8080) itself, then
stops the services it started on exit.

Override defaults via env, e.g. `GAZK_DIR=/path/to/gazk API_PORT=18080 bash scripts/manual_backend_zk_test.sh`.

## Phases

| Phase | What it checks |
|---|---|
| 0 | Preconditions, free ports, create work dir |
| 1 | Postgres healthy + migrations applied |
| 2 | DB reset (incl. `offchain_state_cursors`) — **while backend is stopped** |
| 3 | Start gazk + backend; assert `verificationKeyId=gazk-balance-smoke-v1` and the verify-gate log line |
| 4 | `POST /api/deposit` 100 |
| 5 | `POST /api/withdraw-request` 40 |
| 6 | `POST /api/batch/build` **(called exactly once)** |
| 7 | `POST /api/proof/generate` → proof comes from gazk (not the `local-v1` mock) |
| 8 | NEGATIVE: tampered `newStateRoot` and tampered proof bytes → **HTTP 400**, and DB state unchanged |
| 9 | Valid submit passes the real verify gate (`accepted=true`, root advances) |
| 10 | Claim → `940` / `60` (canonical Alice vector) |
| 11 | DB assertions: `proof_bundles.verification_key_id`, `submit_batches.accepted`, `indexed_withdraw_records.claimed` |

## Two golden rules (why the script is structured this way)

1. **Reset DB while the backend is stopped, then start it fresh.** The off-chain
   state lives in two places: the persisted cursor/tables AND an in-memory
   `OffchainStateManager`. Resetting only the DB while the backend keeps running
   leaves the in-memory manager advanced and the cursor stale → build fails with
   `cannot continue transition chain from root ...`. Always reset the cursor too.

2. **Call `POST /api/batch/build` exactly once per batch.** After a build the
   pending deposits/withdrawals move `pending → included`; a second build finds
   nothing and returns `no pending settlement operations`. If you accidentally
   overwrite `build.json`, recover the batch from the DB without resetting:

   ```bash
   docker exec -i ganc_sys_postgres psql -U ganc -d ganc_sys -t -A -c \
   "select jsonb_build_object('settlementUpdate',settlement_update,'batchCommitments',batch_commitments,'witness',witness) from batch_builds where batch_id='batch-1';" > genproof.json
   ```

## Related

- [zk-integration.md](zk-integration.md) — design of the backend↔ZK wiring.
- [remote-prover-e2e-runbook.md](remote-prover-e2e-runbook.md) — manual step-by-step variant.
