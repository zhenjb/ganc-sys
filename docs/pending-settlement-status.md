# Pending Settlement Integration Status

## Current status

P3/P4 pending off-chain settlement lifecycle is working in local/dev mode.

Verified flows:

```txt
Deposit indexed
-> offchain pending deposit created

Withdraw requested
-> offchain pending withdrawal created

Build pending batch
-> pending operations marked included
-> settlementUpdate + batchCommitments + witness generated

Generate proof
-> proofBundle generated with 6 public inputs

Submit accepted
-> pending operations marked committed
-> committed_root = pending_root = settlementUpdate.newStateRoot

Cancel/retry before submit
-> included operations reopened to pending
-> retry build creates a new batch
````

Manual verification passed for:

```txt
happy path:
pending -> included -> committed

retry path:
included -> pending -> included again
```

The pending settlement E2E script completed successfully with deposit and withdrawal committed under `batch-1`, and `committed_root` / `pending_root` both equal to the accepted batch `newStateRoot`.

The cancel endpoint manual test also passed: `batch-1` was reopened, `dep-1` and `wd-1` returned to `pending`, then retry build produced `batch-2` and marked both operations `included` again.

## Runtime flags

Use this mode for the production-direction pending settlement flow:

```bash
OFFCHAIN_SETTLEMENT_ENABLED=true
BATCH_BUILD_SOURCE=pending
WITHDRAW_REQUEST_STORE=postgres
WITHDRAW_RECORD_STORE=postgres
BATCH_BUILD_STORE=postgres
PROOF_BUNDLE_STORE=postgres
SUBMIT_BATCH_STORE=postgres
DATABASE_URL="postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable"
```

Recommended local run command:

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

## Important architecture decisions

### Deposit flow

`POST /api/deposit` is dev-only.

It is only a local substitute for the real deposit path.

Real production path must be:

```txt
P5 wallet
-> sign MsgDeposit
-> broadcast to P1 chain
-> P1 emits EventDeposit
-> P4 indexes EventDeposit
-> DepositIndexer creates DepositRecord
-> OffchainSettlementService.ApplyIndexedDeposit()
```

Do not treat `POST /api/deposit` as a production user deposit API.

### Batch build source

Current supported modes:

```txt
BATCH_BUILD_SOURCE=manual
```

Legacy/manual mode. `/api/batch/build` expects:

```json
{
  "depositIds": ["dep-1"],
  "withdrawIds": ["wd-1"]
}
```

```txt
BATCH_BUILD_SOURCE=pending
```

Production-direction mode. `/api/batch/build` reads pending off-chain settlement operations from DB and ignores request body IDs.

Expected request:

```json
{}
```

### Off-chain settlement state model

The system tracks two roots:

```txt
committed_root = latest root accepted by chain
pending_root   = committed_root + indexed deposits + requested withdrawals
```

Batch build must produce:

```txt
oldStateRoot = committed_root
newStateRoot = pending_root
```

Witness must prove the account transition from committed balance to pending balance.

For the canonical local Alice flow:

```txt
deposit 100
withdraw 40

witness.oldBalance = 0
witness.newBalance = 60
```

### Operation lifecycle

Pending settlement operations follow this lifecycle:

```txt
pending
-> included
-> committed
```

Retry/cancel path:

```txt
included
-> pending
-> included again under a new batch
```

Submit reject/failure path:

```txt
included
-> failed
```

## Current internal endpoints

### Cancel/reopen included batch

```txt
POST /api/internal/offchain-settlement/batches/{batchId}/cancel
```

Request:

```json
{
  "reason": "proof generation failed"
}
```

Response:

```json
{
  "batchId": "batch-1",
  "status": "reopened",
  "reason": "proof generation failed"
}
```

Behavior:

```txt
offchain_pending_deposits:
included -> pending

offchain_pending_withdrawals:
included -> pending

batch_id cleared
tx_hash cleared
error_message = reason
```

This endpoint is internal/dev recovery tooling. It must not be exposed publicly without authentication/authorization.

## Scripts

### Legacy/manual DB flow

```bash
./scripts/e2e_local_db_flow.sh
```

This tests the older local DB/manual orchestration path.

### Pending settlement flow

```bash
./scripts/e2e_pending_settlement_flow.sh
```

This is the main production-direction local/dev baseline.

It verifies:

```txt
dev deposit substitute
-> pending deposit

withdraw request
-> pending withdrawal

batch build
-> included

proof generate
-> proofBundle

batch submit
-> committed
```

## Boundaries and TODOs

### Waiting on P5 + P1

Blocked until real `MsgDeposit` flow is ready:

```txt
P5:
- wallet signs MsgDeposit
- frontend broadcasts tx to chain

P1:
- MsgDeposit works on chain
- EventDeposit emitted with stable fields
- DepositRecord query available and stable
```

After that, replace local deposit substitute with:

```txt
real chain tx
-> real EventDeposit indexing
-> DepositIndexer path already wired to off-chain settlement
```

### Waiting on P2

P2 has not integrated yet.

Current `/api/proof/generate` uses local/mock prover.

Future P2 integration should replace:

```txt
prover.NewLocalClient()
```

with a real P2 prover adapter.

P2 prover input contract is already shaped as:

```txt
settlementUpdate
batchCommitments
witness
```

Proof public inputs must contain 6 values:

```txt
1. oldStateRoot
2. newStateRoot
3. depositsRoot
4. withdrawalsRoot
5. nullifiersRoot
6. withdrawOutputsRoot
```

### Later hardening

Do later, not now:

```txt
- auto-cancel/reopen included batch when proof generation fails
- stronger submit rejected / failed batch policy
- auth guard for /api/internal/offchain-settlement/... endpoints
- startup recovery: rebuild OffchainStateManager from DB after backend restart
- replace mock userSecret with P5/wallet/keystore-provided secret
- remove or hard-disable POST /api/deposit in production
```

## Current conclusion

P3/P4 local-dev pending settlement integration is ready to pause.

The system can now wait for:

```txt
1. P5 + P1 real MsgDeposit path
2. P2 real prover integration
```