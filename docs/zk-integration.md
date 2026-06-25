# Backend ↔ ZK Integration (gazk)

This document records how the `ganc-sys` backend (P4) is wired to the real
`gazk` zero-knowledge prover/verifier (P2), replacing the previous mock.

Date: 2026-06-14

## Summary

`gazk` already exposes a complete real ZK service (gnark / Groth16, BN254):

- `POST /prove`   – generate a real proof + 6 settlement public inputs
- `POST /verify`  – real Groth16 verification of a proof bundle
- `GET  /verifier-artifact` – verifier metadata (vk id, curve, backend, input names)
- `GET  /health`

Before this change the backend had **two** ZK touch points, only one of which
was actually connected to real ZK:

| Touch point | Endpoint | Before | After |
|---|---|---|---|
| Proof generation | `POST /api/proof/generate` | mock `prover.LocalClient` by default; real `RemoteClient` available via `PROVER_MODE=remote` | unchanged (real path already existed) |
| **Proof verification** | `POST /api/batch/submit` | **mock only** – `relayer.LocalClient` always returns `accepted: true`, never verifies the proof | **real Groth16 verification** via `gazk /verify` before the batch is accepted |

The verification gap was the remaining ZK mock: any proof (even garbage) was
accepted on submit. The on-chain `x/zkdex` module (P1) that would normally
verify inside `MsgSubmitBatchProof` is not built yet, so the backend now
performs the same real verification as an integration adapter
(`ONCHAIN-07` verifier interface, served from P4 until P1 exists).

## What changed in code

All changes are additive and limited to the Backend ↔ ZK boundary.

1. `internal/prover/verifier.go` (new)
   - `Verifier` interface + `VerifyProofInput`.

2. `internal/prover/remote_client.go`
   - `RemoteClient.Verify(ctx, input)` calls `gazk POST /verify`.
   - Returns `nil` only when gazk responds `{"valid": true}`; any transport
     error, non-2xx status, or `{"valid": false}` is returned as an error.
   - `RemoteClient` now implements both `prover.Client` and `prover.Verifier`.

3. `internal/service/batch_service.go`
   - Optional `proofVerifier prover.Verifier` field + `SetProofVerifier(...)`.
   - `SubmitBatch` runs the verifier **before** the relayer. On failure it
     returns `ErrProofVerificationFailed` and performs **no state change**
     (no root update, no nullifier write, no withdraw record, no relayer call) —
     matching the "invalid proof => transaction fails" invariant.
   - When no verifier is injected (mock prover mode), behaviour is unchanged.

4. `cmd/api/main.go`
   - When the prover client also implements `prover.Verifier` (i.e. remote
     gazk) and `PROOF_VERIFY_ENABLED != "false"`, the same client is injected
     as the batch submit verifier.

5. Tests
   - `internal/prover/remote_verify_test.go` – `RemoteClient.Verify` against an
     httptest gazk (valid / invalid / non-2xx).
   - `internal/service/batch_submit_verify_test.go` – submit accepts on valid
     proof, rejects (`ErrProofVerificationFailed`, relayer not called) on
     invalid proof, and skips verification when no verifier is set.

## Configuration

| Env | Default | Meaning |
|---|---|---|
| `PROVER_MODE` | `local` | `remote` = use gazk for proof generation **and** verification |
| `PROVER_URL` | `http://localhost:8090` | gazk base URL |
| `PROOF_VERIFY_ENABLED` | `true` | set `false` to disable the submit-time verify gate even in remote mode |

In `local` prover mode the proof is a deterministic mock, so no real
verification is performed (the `LocalClient` does not implement `Verifier`).

## Run it (Git Bash)

Terminal 1 — Postgres:

    cd ganc-sys
    docker compose up -d postgres
    for f in migrations/*.sql; do docker exec -i ganc_sys_postgres psql -U ganc -d ganc_sys -v ON_ERROR_STOP=1 < "$f"; done

Terminal 2 — gazk prover/verifier:

    cd gazk
    go run main.go server          # listens on :8090, hashMode v0-sha256

Terminal 3 — backend (remote + verify):

    cd ganc-sys
    OFFCHAIN_SETTLEMENT_ENABLED=true \
    BATCH_BUILD_SOURCE=pending \
    PROVER_MODE=remote \
    PROVER_URL=http://localhost:8090 \
    PROOF_VERIFY_ENABLED=true \
    WITHDRAW_REQUEST_STORE=postgres WITHDRAW_RECORD_STORE=postgres \
    BATCH_BUILD_STORE=postgres PROOF_BUNDLE_STORE=postgres SUBMIT_BATCH_STORE=postgres \
    DATABASE_URL="postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable" \
    go run ./cmd/api

Expected startup log line:

    batch submit real ZK verification enabled via remote prover

## Verified end-to-end (canonical Alice 100/40 vector)

Happy path `deposit → withdraw-request → batch/build → proof/generate → batch/submit → claim`:

- proof bundle `verificationKeyId = gazk-balance-smoke-v1` (proved by gazk, not the mock)
- submit `accepted: true`, `currentStateRoot` advances to `newStateRoot`
- claim → `userBalances cosmos1alice/uusdc = 940`, `moduleAccountBalance uusdc = 60`
- DB: `offchain_pending_{deposits,withdrawals}.status = committed`,
  `offchain_state_cursors.committed_root = newStateRoot`,
  `last_committed_batch_id = batch-1`

Negative paths (the behaviour that the old mock could **not** enforce):

- Tampered `newStateRoot` → submit `HTTP 400`
  `proof verification failed: ... publicInputs[1] mismatch ...`
- Tampered proof bytes → submit `HTTP 400`
  `proof verification failed: ... cannot deserialize Groth16 proof ...`
- After a rejected submit, off-chain pending rows stay `included` and
  `committed_root` is unchanged (no state change on invalid proof).

## Compatibility note

The P3 batch builder produces `nullifier = Hash(userSecret, nonce)` and
`destinationHash = Hash(destination)` values that match gazk's `v0-sha256`
hash bindings exactly (verified live: `0xac44...` / `0xa75a...`), so a batch
built by the backend proves and verifies against gazk without modification.

## Current limitation (inherited from gazk v0-sha256)

The gnark circuit currently constrains only the balance transition
`oldBalance + depositAmount == newBalance + withdrawAmount`. The nullifier,
destinationHash, and state-root bindings are validated at gazk service level
(not yet inside the circuit). Binding all 6 settlement public inputs inside the
circuit is a later gazk task (`v1-mimc` mode is the start of this). This does
not affect the backend integration: the contract (6 public inputs,
`ProofBundle`, `/prove`, `/verify`) is stable across modes.

## Next steps

- Move nullifier / destinationHash / state-root constraints inside the gazk
  circuit (`GAZK_HASH_MODE=v1-mimc` path) and re-point `verificationKeyId`.
- When P1 `x/zkdex` ships, move proof verification on-chain inside
  `MsgSubmitBatchProof`; the backend gate can then become a pre-check or be
  retired behind `PROOF_VERIFY_ENABLED=false`.
- Add a negative-path E2E script (INT-13) that submits a tampered proof and
  asserts `HTTP 400` + unchanged DB state.
