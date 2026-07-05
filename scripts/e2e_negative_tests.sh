#!/usr/bin/env bash
#
# e2e_negative_tests.sh (SYS-08)
# ---------------------------------------------------------------------------
# Failure-path acceptance against a REAL ganc-trade node + gazk, via ganc-sys.
# Maps each negative to the layer that must reject it:
#
#   BACKEND GATE (HTTP 400, no chain tx) — SYS-05 verify gate:
#     N1  tampered proof bytes          -> gazk Groth16 reject
#     N2  tampered public input         -> public-input binding reject
#     N3  wrong verificationKeyId       -> vkId pin reject
#   CHAIN (submit not accepted / error) — x/zkdex keeper:
#     N4  re-submit same batch          -> duplicate batchId / deposit processed / nullifier used
#     N5  double claim                  -> "already claimed"
#   OFF-CHAIN / VALIDATION:
#     N6  over-withdraw (> balance)      -> rejected at request/build
#
# PREREQUISITES: same as SYS-07 (node + real_mode_up.sh running). Run this on a
# FRESH chain state (restart ignite) so batch/deposit/nullifier are unused.
#
# Requirements: obd, curl, jq, python, bash.
# ---------------------------------------------------------------------------
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GANC_SYS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
WORK_DIR="$GANC_SYS_DIR/.e2e_neg_tmp"

API="${API_BASE_URL:-http://localhost:${API_PORT:-8080}}"
CHAIN_BINARY="${CHAIN_BINARY:-obd}"
NODE="${CHAIN_NODE:-http://localhost:26657}"
KEYRING="${CHAIN_KEYRING_BACKEND:-test}"
SIGNER="${RELAYER_FROM:-alice}"
DENOM="${ASSET_DENOM:-USDT}"
COMMIT_WAIT="${COMMIT_WAIT:-6}"

PASS=0; FAIL=0
c_reset=$'\033[0m'; c_blue=$'\033[1;34m'; c_green=$'\033[1;32m'; c_red=$'\033[1;31m'; c_yellow=$'\033[1;33m'
phase() { echo; echo "${c_blue}== $* ==${c_reset}"; }
note()  { echo "${c_yellow}note:${c_reset} $*"; }
ok()    { echo "${c_green}[ PASS ]${c_reset} $*"; PASS=$((PASS+1)); }
bad()   { echo "${c_red}[ FAIL ]${c_reset} $*"; FAIL=$((FAIL+1)); }
die()   { echo "${c_red}FATAL:${c_reset} $*" >&2; exit 1; }
jget()  { python -c "import json;d=json.load(open('$1'));print($2)" 2>/dev/null; }
post_code() { curl -s -o "$2" -w '%{http_code}' -X POST "$API/$1" -H 'Content-Type: application/json' -d @"$3"; }

# ---------------------------------------------------------------------------
phase "PHASE 0 — Preconditions + happy path up to a valid proof"
for c in "$CHAIN_BINARY" curl jq python; do command -v "$c" >/dev/null || die "$c not found"; done
curl -sf "$API/api/health" >/dev/null || die "backend not up (run scripts/real_mode_up.sh)"
ALICE="$("$CHAIN_BINARY" keys show "$SIGNER" -a --keyring-backend "$KEYRING" 2>/dev/null)"
[ -n "$ALICE" ] || die "cannot resolve address for '$SIGNER'"
rm -rf "$WORK_DIR"; mkdir -p "$WORK_DIR"; cd "$WORK_DIR"

# N6 (self-contained): deposit 100, then request withdraw >> balance -> rejected.
phase "N6 — Over-withdraw (> deposited) must be rejected"
curl -s -X POST "$API/api/deposit" -H 'Content-Type: application/json' \
  -d "{\"owner\":\"$ALICE\",\"denom\":\"$DENOM\",\"amount\":\"100\"}" -o n6_dep.json
N6_DEP="$(jget n6_dep.json "d['depositRecord']['depositId']")"; [ -n "$N6_DEP" ] || die "N6 deposit failed"
sleep "$COMMIT_WAIT"
CODE="$(curl -s -o n6_wd.json -w '%{http_code}' -X POST "$API/api/withdraw-request" -H 'Content-Type: application/json' \
  -d "{\"owner\":\"$ALICE\",\"denom\":\"$DENOM\",\"amount\":\"999999999\",\"destination\":\"$ALICE\"}")"
if [ "$CODE" -ge 400 ]; then
  ok "over-withdraw rejected at request (HTTP $CODE)"
else
  N6_WD="$(jget n6_wd.json "d['withdrawRequest']['withdrawId']")"
  note "request accepted (HTTP $CODE) — backend defers; checking build with the over-withdraw id"
  CODE2="$(curl -s -o n6_build.json -w '%{http_code}' -X POST "$API/api/batch/build" -H 'Content-Type: application/json' \
    -d "{\"depositIds\":[\"$N6_DEP\"],\"withdrawIds\":[\"$N6_WD\"]}")"
  jget n6_build.json "d['settlementUpdate']['batchId']" >/dev/null 2>&1 \
    && bad "over-withdraw NOT rejected (built a batch, HTTP $CODE2)" || ok "over-withdraw rejected at build (HTTP $CODE2)"
fi

# Build a clean happy-path proof for N1..N5 (separate deposit/withdraw).
note "deposit 100 + withdraw 40 + build (explicit ids) + prove"
curl -s -X POST "$API/api/deposit" -H 'Content-Type: application/json' \
  -d "{\"owner\":\"$ALICE\",\"denom\":\"$DENOM\",\"amount\":\"100\"}" -o deposit.json
DEP_ID="$(jget deposit.json "d['depositRecord']['depositId']")"; [ -n "$DEP_ID" ] || die "deposit failed (see deposit.json)"
sleep "$COMMIT_WAIT"
curl -s -X POST "$API/api/withdraw-request" -H 'Content-Type: application/json' \
  -d "{\"owner\":\"$ALICE\",\"denom\":\"$DENOM\",\"amount\":\"40\",\"destination\":\"$ALICE\"}" -o withdraw.json
WD_ID="$(jget withdraw.json "d['withdrawRequest']['withdrawId']")"; [ -n "$WD_ID" ] || die "withdraw-request failed"
curl -s -X POST "$API/api/batch/build" -H 'Content-Type: application/json' \
  -d "{\"depositIds\":[\"$DEP_ID\"],\"withdrawIds\":[\"$WD_ID\"]}" -o build.json
BATCH_ID="$(jget build.json "d['settlementUpdate']['batchId']")"; [ -n "$BATCH_ID" ] || die "build failed"
python -c "import json;d=json.load(open('build.json'));json.dump({'settlementUpdate':d['settlementUpdate'],'batchCommitments':d['batchCommitments'],'witness':d['witness']},open('genproof.json','w'))"
curl -s -X POST "$API/api/proof/generate" -H 'Content-Type: application/json' -d @genproof.json -o proof.json
python -c "import json;b=json.load(open('build.json'));p=json.load(open('proof.json'));json.dump({'settlementUpdate':b['settlementUpdate'],'batchCommitments':b['batchCommitments'],'proofBundle':p['proofBundle']},open('submit.json','w'))"
[ -s submit.json ] && ok "happy-path proof ready (batchId=$BATCH_ID)" || die "could not build submit payload"

# ---------------------------------------------------------------------------
phase "N1 — Tampered proof bytes -> HTTP 400 (gazk reject)"
python -c "import json;s=json.load(open('submit.json'));p=s['proofBundle']['proof'];s['proofBundle']['proof']='0x'+'cd'*((len(p)-2)//2);json.dump(s,open('n1.json','w'))"
CODE="$(post_code api/batch/submit n1_resp.json n1.json)"
[ "$CODE" = "400" ] && ok "tampered proof rejected (HTTP 400)" || bad "expected 400, got $CODE ($(cat n1_resp.json))"

phase "N2 — Tampered public input -> HTTP 400 (binding reject)"
python -c "import json;s=json.load(open('submit.json'));s['settlementUpdate']['newStateRoot']='0xdeadbeef';json.dump(s,open('n2.json','w'))"
CODE="$(post_code api/batch/submit n2_resp.json n2.json)"
[ "$CODE" = "400" ] && ok "tampered public input rejected (HTTP 400)" || bad "expected 400, got $CODE ($(cat n2_resp.json))"

phase "N3 — Wrong verificationKeyId -> HTTP 400 (vkId pin)"
python -c "import json;s=json.load(open('submit.json'));s['proofBundle']['verificationKeyId']='sai-circuit-v9';json.dump(s,open('n3.json','w'))"
CODE="$(post_code api/batch/submit n3_resp.json n3.json)"
[ "$CODE" = "400" ] && ok "wrong vkId rejected (HTTP 400)" || bad "expected 400, got $CODE ($(cat n3_resp.json))"

# ---------------------------------------------------------------------------
phase "N4 — Valid submit, then RE-submit same batch -> chain rejects"
CODE="$(post_code api/batch/submit ok_resp.json submit.json)"
ACCEPTED="$(jget ok_resp.json "d['accepted']")"
{ [ "$CODE" = "200" ] && [ "$ACCEPTED" = "True" ]; } && ok "first submit accepted (HTTP 200)" || { cat ok_resp.json; bad "valid submit not accepted (HTTP $CODE)"; }
sleep "$COMMIT_WAIT"
CODE="$(post_code api/batch/submit resub_resp.json submit.json)"
ACCEPTED2="$(jget resub_resp.json "d['accepted']")"
{ [ "$CODE" != "200" ] || [ "$ACCEPTED2" != "True" ]; } && ok "duplicate re-submit rejected (HTTP $CODE, accepted=$ACCEPTED2)" || bad "duplicate submit was accepted — chain dedup failed"

# ---------------------------------------------------------------------------
phase "N5 — Valid claim, then DOUBLE claim -> rejected"
curl -s -o claim1.json -X POST "$API/api/withdraw/claim" -H 'Content-Type: application/json' -d "{\"withdrawId\":\"$WD_ID\"}"
CLAIMED="$(jget claim1.json "d['withdrawRecord']['claimed']")"
[ "$CLAIMED" = "True" ] && ok "first claim succeeded" || { cat claim1.json; bad "first claim failed"; }
sleep "$COMMIT_WAIT"
CODE="$(curl -s -o claim2.json -w '%{http_code}' -X POST "$API/api/withdraw/claim" -H 'Content-Type: application/json' -d "{\"withdrawId\":\"$WD_ID\"}")"
[ "$CODE" != "200" ] && ok "double claim rejected (HTTP $CODE)" || bad "double claim was accepted (HTTP 200)"

# ---------------------------------------------------------------------------
phase "SUMMARY"
echo "PASS: $PASS   FAIL: $FAIL"
[ "$FAIL" -eq 0 ] && { echo "${c_green}SYS-08 negatives: ALL CHECKS PASSED${c_reset}"; exit 0; } \
                  || { echo "${c_red}SYS-08 negatives: $FAIL CHECK(S) FAILED${c_reset}"; exit 1; }
