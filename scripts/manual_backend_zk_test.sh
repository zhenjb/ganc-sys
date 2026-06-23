#!/usr/bin/env bash
#
# manual_backend_zk_test.sh
# ---------------------------------------------------------------------------
# End-to-end manual test for the Backend (ganc-sys) <-> ZK (gazk) integration.
#
# It proves the REAL zero-knowledge path is wired:
#   - proof generation comes from gazk  (verificationKeyId = gazk-balance-smoke-v1)
#   - batch submit is gated by a REAL Groth16 verification (gazk /verify)
#   - a tampered proof is REJECTED with HTTP 400 and leaves chain state untouched
#   - the canonical Alice vector settles to 940 / 60
#
# The script manages the full lifecycle so the two golden rules are enforced:
#   RULE 1: backend is (re)started fresh AFTER the DB reset, so the in-memory
#           off-chain state manager and the persisted cursor start aligned.
#   RULE 2: POST /api/batch/build is called EXACTLY ONCE per batch (a second
#           call would find no pending ops; this script never makes it).
#
# Requirements: docker, go, curl, python, bash (Git Bash on Windows).
# On-chain (P1) is NOT involved: RELAYER_MODE stays "local" (mock relayer).
#
# Windows/Git Bash note: all data files use BARE filenames inside a real work
# dir we cd into, because native curl.exe/python.exe mis-translate MSYS
# absolute paths like /tmp/... . Bash-level redirects use absolute paths (fine).
# ---------------------------------------------------------------------------
set -uo pipefail

# --- Resolve dirs ----------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GANC_SYS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GAZK_DIR="${GAZK_DIR:-$(cd "$GANC_SYS_DIR/../gazk" 2>/dev/null && pwd)}"
WORK_DIR="$GANC_SYS_DIR/.manual_zk_tmp"

# --- Config (override via env) ---------------------------------------------
API_PORT="${API_PORT:-8080}"
GAZK_PORT="${GAZK_PORT:-8090}"
API_BASE_URL="${API_BASE_URL:-http://localhost:$API_PORT}"
GAZK_URL="${GAZK_URL:-http://localhost:$GAZK_PORT}"
DATABASE_URL="${DATABASE_URL:-postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable}"
PG_CONTAINER="${PG_CONTAINER:-ganc_sys_postgres}"
EXPECTED_VK_ID="${EXPECTED_VK_ID:-gazk-balance-smoke-v1}"

PASS=0
FAIL=0

# --- Pretty output ---------------------------------------------------------
c_reset=$'\033[0m'; c_blue=$'\033[1;34m'; c_green=$'\033[1;32m'; c_red=$'\033[1;31m'; c_yellow=$'\033[1;33m'
phase() { echo; echo "${c_blue}============================================================${c_reset}"; echo "${c_blue}# $*${c_reset}"; echo "${c_blue}============================================================${c_reset}"; }
note()  { echo "${c_yellow}note:${c_reset} $*"; }
ok()    { echo "${c_green}[ PASS ]${c_reset} $*"; PASS=$((PASS+1)); }
bad()   { echo "${c_red}[ FAIL ]${c_reset} $*"; FAIL=$((FAIL+1)); }
die()   { echo "${c_red}FATAL:${c_reset} $*" >&2; exit 1; }

# --- Helpers ---------------------------------------------------------------
kill_port() {
  local port="$1" pid
  for pid in $(netstat -ano 2>/dev/null | grep ":$port" | grep LISTENING | awk '{print $NF}' | sort -u); do
    powershell -Command "Stop-Process -Id $pid -Force" >/dev/null 2>&1 || kill -9 "$pid" >/dev/null 2>&1 || true
  done
}
psql_exec()   { docker exec -i "$PG_CONTAINER" psql -U ganc -d ganc_sys "$@"; }
psql_scalar() { docker exec -i "$PG_CONTAINER" psql -U ganc -d ganc_sys -t -A -c "$1" | tr -d '[:space:]'; }
# jget <bare-file> <python-expr-on-d>
jget() { python -c "import json;d=json.load(open('$1'));print($2)" 2>/dev/null; }
wait_http() {  # wait_http <url>
  local i
  for i in $(seq 1 60); do curl -sf "$1" >/dev/null 2>&1 && return 0; sleep 1; done
  return 1
}

cleanup() {
  phase "CLEANUP"
  note "stopping backend (:$API_PORT) and gazk (:$GAZK_PORT) started by this script"
  cd "$GANC_SYS_DIR" 2>/dev/null || true
  kill_port "$API_PORT"; kill_port "$GAZK_PORT"
  rm -rf "$WORK_DIR" 2>/dev/null || true
  echo "done."
}
trap cleanup EXIT

# ===========================================================================
phase "PHASE 0 — Preconditions"
# ===========================================================================
command -v docker >/dev/null || die "docker not found"
command -v go     >/dev/null || die "go not found"
command -v python >/dev/null || die "python not found"
[ -d "$GAZK_DIR" ] || die "gazk dir not found (set GAZK_DIR=...)"
note "ganc-sys = $GANC_SYS_DIR"
note "gazk     = $GAZK_DIR"
note "freeing ports $API_PORT and $GAZK_PORT if held by an old run"
kill_port "$API_PORT"; kill_port "$GAZK_PORT"
rm -rf "$WORK_DIR"; mkdir -p "$WORK_DIR"; cd "$WORK_DIR"
note "work dir = $WORK_DIR (data files use bare names from here)"
ok "preconditions satisfied"

# ===========================================================================
phase "PHASE 1 — Postgres up + migrations"
# ===========================================================================
( cd "$GANC_SYS_DIR" && docker compose up -d postgres ) || die "docker compose up postgres failed"
note "waiting for postgres healthy"
for i in $(seq 1 30); do
  [ "$(docker inspect -f '{{.State.Health.Status}}' "$PG_CONTAINER" 2>/dev/null)" = "healthy" ] && break
  sleep 2
done
[ "$(docker inspect -f '{{.State.Health.Status}}' "$PG_CONTAINER" 2>/dev/null)" = "healthy" ] || die "postgres not healthy"
for f in "$GANC_SYS_DIR"/migrations/*.sql; do psql_exec -v ON_ERROR_STOP=1 < "$f" >/dev/null 2>&1 || true; done
ok "postgres healthy + migrations applied"

# ===========================================================================
phase "PHASE 2 — Reset DB (backend NOT running yet — RULE 1)"
# ===========================================================================
note "wiping off-chain settlement state INCLUDING offchain_state_cursors"
psql_exec >/dev/null <<'SQL'
DELETE FROM offchain_pending_withdrawals;
DELETE FROM offchain_pending_deposits;
DELETE FROM offchain_state_cursors;
DELETE FROM indexed_withdraw_records;
DELETE FROM submit_batches;
DELETE FROM proof_bundles;
DELETE FROM batch_builds;
DELETE FROM withdraw_requests;
ALTER SEQUENCE withdraw_request_seq RESTART WITH 1;
SQL
[ "$(psql_scalar 'select count(*) from offchain_state_cursors;')" = "0" ] \
  && ok "clean slate (0 cursors, 0 pending)" || bad "DB not clean after reset"

# ===========================================================================
phase "PHASE 3 — Start gazk (ZK) + backend (remote prover + real verify)"
# ===========================================================================
note "starting gazk on :$GAZK_PORT (first run compiles circuit + sets up keys)"
( cd "$GAZK_DIR" && GAZK_ADDR=":$GAZK_PORT" go run main.go server >"$WORK_DIR/gazk.log" 2>&1 ) &
wait_http "$GAZK_URL/health" || { cat "$WORK_DIR/gazk.log"; die "gazk did not become healthy"; }
GAZK_HEALTH="$(curl -s "$GAZK_URL/health")"
echo "  health: $GAZK_HEALTH"
GOT_VK="$(echo "$GAZK_HEALTH" | python -c "import sys,json;print(json.load(sys.stdin)['verificationKeyId'])")"
[ "$GOT_VK" = "$EXPECTED_VK_ID" ] && ok "gazk healthy (verificationKeyId=$GOT_VK)" || bad "gazk vkId=$GOT_VK, expected $EXPECTED_VK_ID"

note "starting backend on :$API_PORT (PROVER_MODE=remote, PROOF_VERIFY_ENABLED=true, RELAYER_MODE=local)"
(
  cd "$GANC_SYS_DIR" && \
  OFFCHAIN_SETTLEMENT_ENABLED=true \
  BATCH_BUILD_SOURCE=pending \
  PROVER_MODE=remote \
  PROVER_URL="$GAZK_URL" \
  PROOF_VERIFY_ENABLED=true \
  RELAYER_MODE=local \
  WITHDRAW_REQUEST_STORE=postgres \
  WITHDRAW_RECORD_STORE=postgres \
  BATCH_BUILD_STORE=postgres \
  PROOF_BUNDLE_STORE=postgres \
  SUBMIT_BATCH_STORE=postgres \
  DATABASE_URL="$DATABASE_URL" \
  PORT="$API_PORT" \
  go run ./cmd/api >"$WORK_DIR/api.log" 2>&1
) &
wait_http "$API_BASE_URL/api/state" || { cat "$WORK_DIR/api.log"; die "backend did not start"; }
if grep -q "real ZK verification enabled via remote prover" "$WORK_DIR/api.log"; then
  ok "backend up with REAL ZK verification gate enabled"
else
  bad "backend up but verify-gate log line missing (see $WORK_DIR/api.log)"
fi

# ===========================================================================
phase "PHASE 4 — Deposit 100"
# ===========================================================================
note "POST /api/deposit {owner:cosmos1alice, denom:uusdc, amount:100}"
curl -s -X POST "$API_BASE_URL/api/deposit" -H 'Content-Type: application/json' \
  -d '{"owner":"cosmos1alice","denom":"uusdc","amount":"100"}' -o deposit.json
DEP_ID="$(jget deposit.json "d['depositRecord']['depositId']")"
[ -n "$DEP_ID" ] && ok "deposit indexed (depositId=$DEP_ID)" || { cat deposit.json; bad "deposit failed"; }

# ===========================================================================
phase "PHASE 5 — Withdraw request 40"
# ===========================================================================
note "POST /api/withdraw-request {amount:40, destination:cosmos1alice}"
curl -s -X POST "$API_BASE_URL/api/withdraw-request" -H 'Content-Type: application/json' \
  -d '{"owner":"cosmos1alice","denom":"uusdc","amount":"40","destination":"cosmos1alice"}' -o withdraw.json
WD_ID="$(jget withdraw.json "d['withdrawRequest']['withdrawId']")"
[ -n "$WD_ID" ] && ok "withdraw request created (withdrawId=$WD_ID)" || { cat withdraw.json; bad "withdraw-request failed"; }

# ===========================================================================
phase "PHASE 6 — Build batch (called EXACTLY ONCE — RULE 2)"
# ===========================================================================
note "POST /api/batch/build (pending source gathers all pending deposits+withdrawals)"
curl -s -X POST "$API_BASE_URL/api/batch/build" -H 'Content-Type: application/json' -d '{}' -o build.json
BATCH_ID="$(jget build.json "d['settlementUpdate']['batchId']")"
if [ -n "$BATCH_ID" ]; then
  ok "batch built (batchId=$BATCH_ID)"
  echo "  oldStateRoot:    $(jget build.json "d['settlementUpdate']['oldStateRoot']")"
  echo "  newStateRoot:    $(jget build.json "d['settlementUpdate']['newStateRoot']")"
  echo "  nullifier:       $(jget build.json "d['settlementUpdate']['withdrawals'][0]['nullifier']")"
  echo "  destinationHash: $(jget build.json "d['settlementUpdate']['withdrawals'][0]['destinationHash']")"
else
  cat build.json; bad "batch/build failed"
fi

# ===========================================================================
phase "PHASE 7 — Generate proof (REAL ZK from gazk)"
# ===========================================================================
note "POST /api/proof/generate {settlementUpdate, batchCommitments, witness}"
python -c "import json;d=json.load(open('build.json'));json.dump({'settlementUpdate':d['settlementUpdate'],'batchCommitments':d['batchCommitments'],'witness':d['witness']},open('genproof.json','w'))"
curl -s -X POST "$API_BASE_URL/api/proof/generate" -H 'Content-Type: application/json' -d @genproof.json -o proof.json
PROOF_VK="$(jget proof.json "d['proofBundle']['verificationKeyId']")"
if [ "$PROOF_VK" = "$EXPECTED_VK_ID" ]; then
  ok "proof generated by gazk (verificationKeyId=$PROOF_VK — NOT the local mock)"
else
  cat proof.json; bad "proof vkId=$PROOF_VK, expected $EXPECTED_VK_ID"
fi
python -c "import json;b=json.load(open('build.json'));p=json.load(open('proof.json'));json.dump({'settlementUpdate':b['settlementUpdate'],'batchCommitments':b['batchCommitments'],'proofBundle':p['proofBundle']},open('submit.json','w'))"

# ===========================================================================
phase "PHASE 8 — NEGATIVE: tampered proofs must be REJECTED (HTTP 400)"
# ===========================================================================
note "8a) tamper newStateRoot -> public input no longer matches the proof"
python -c "import json;s=json.load(open('submit.json'));s['settlementUpdate']['newStateRoot']='0xdeadbeef';json.dump(s,open('bad_root.json','w'))"
CODE="$(curl -s -o bad_root_resp.json -w '%{http_code}' -X POST "$API_BASE_URL/api/batch/submit" -H 'Content-Type: application/json' -d @bad_root.json)"
[ "$CODE" = "400" ] && ok "tampered newStateRoot rejected (HTTP 400)" || bad "expected 400, got $CODE"
echo "  -> $(cat bad_root_resp.json)"

note "8b) tamper proof bytes -> Groth16 deserialize/verify fails"
python -c "import json;s=json.load(open('submit.json'));p=s['proofBundle']['proof'];s['proofBundle']['proof']='0x'+'cd'*((len(p)-2)//2);json.dump(s,open('bad_proof.json','w'))"
CODE="$(curl -s -o bad_proof_resp.json -w '%{http_code}' -X POST "$API_BASE_URL/api/batch/submit" -H 'Content-Type: application/json' -d @bad_proof.json)"
[ "$CODE" = "400" ] && ok "tampered proof bytes rejected (HTTP 400)" || bad "expected 400, got $CODE"
echo "  -> $(cat bad_proof_resp.json)"

note "8c) invariant: rejection must leave state untouched (no committed root yet)"
LAST_BATCH="$(psql_scalar "select coalesce(last_committed_batch_id,'') from offchain_state_cursors;")"
DEP_STATUS="$(psql_scalar "select status from offchain_pending_deposits where deposit_id='$DEP_ID';")"
if [ -z "$LAST_BATCH" ] && [ "$DEP_STATUS" = "included" ]; then
  ok "no state change after rejection (last_committed_batch_id empty, $DEP_ID still 'included')"
else
  bad "state changed after rejection (last_committed_batch_id='$LAST_BATCH', $DEP_ID='$DEP_STATUS')"
fi

# ===========================================================================
phase "PHASE 9 — VALID submit (passes the real verify gate)"
# ===========================================================================
note "POST /api/batch/submit with the genuine proof"
curl -s -X POST "$API_BASE_URL/api/batch/submit" -H 'Content-Type: application/json' -d @submit.json -o submit_resp.json
ACCEPTED="$(jget submit_resp.json "d['accepted']")"
PROOF_STATUS="$(jget submit_resp.json "d['proofStatus']")"
[ "$ACCEPTED" = "True" ] && ok "batch accepted (proofStatus=$PROOF_STATUS, root advanced)" || { cat submit_resp.json; bad "valid submit not accepted"; }

# ===========================================================================
phase "PHASE 10 — Claim withdrawal"
# ===========================================================================
note "POST /api/withdraw/claim {withdrawId:$WD_ID}"
curl -s -X POST "$API_BASE_URL/api/withdraw/claim" -H 'Content-Type: application/json' \
  -d "{\"withdrawId\":\"$WD_ID\"}" -o claim.json
CLAIMED="$(jget claim.json "d['withdrawRecord']['claimed']")"
USER_BAL="$(jget claim.json "d['balances']['userBalances']['cosmos1alice/uusdc']")"
MOD_BAL="$(jget claim.json "d['balances']['moduleAccountBalance']['uusdc']")"
[ "$CLAIMED" = "True" ] && ok "withdraw claimed" || { cat claim.json; bad "claim failed"; }
[ "$USER_BAL" = "940" ] && ok "user balance = 940 (canonical)" || bad "user balance = $USER_BAL, expected 940"
[ "$MOD_BAL" = "60" ]  && ok "module account balance = 60 (canonical)" || bad "module balance = $MOD_BAL, expected 60"

# ===========================================================================
phase "PHASE 11 — Final DB assertions"
# ===========================================================================
VK_DB="$(psql_scalar "select verification_key_id from proof_bundles where batch_id='$BATCH_ID';")"
ACC_DB="$(psql_scalar "select accepted from submit_batches where batch_id='$BATCH_ID';")"
CLAIMED_DB="$(psql_scalar "select claimed from indexed_withdraw_records where withdraw_id='$WD_ID';")"
[ "$VK_DB" = "$EXPECTED_VK_ID" ] && ok "proof_bundles.verification_key_id = $VK_DB" || bad "vk in DB = $VK_DB"
[ "$ACC_DB" = "t" ] && ok "submit_batches.accepted = t" || bad "accepted in DB = $ACC_DB"
[ "$CLAIMED_DB" = "t" ] && ok "indexed_withdraw_records.claimed = t" || bad "claimed in DB = $CLAIMED_DB"

# ===========================================================================
phase "SUMMARY"
# ===========================================================================
echo "PASS: $PASS    FAIL: $FAIL"
if [ "$FAIL" -eq 0 ]; then
  echo "${c_green}Backend <-> ZK integration: ALL CHECKS PASSED${c_reset}"; exit 0
else
  echo "${c_red}Backend <-> ZK integration: $FAIL CHECK(S) FAILED${c_reset}"
  echo "logs preserved? (work dir is removed on cleanup; rerun with TRAP disabled to inspect)"; exit 1
fi
