#!/usr/bin/env bash
#
# settle_loop.sh (Phase 3) — settlement sequencer (the "operator" process).
# ---------------------------------------------------------------------------
# Users never trigger settlement. This standalone loop plays the operator role:
# every SETTLE_INTERVAL seconds it drains the off-chain pending queue by calling
#
#     POST /api/batch/build   ({} — BATCH_BUILD_SOURCE=pending auto-collects)
#  -> POST /api/proof/generate (gazk)
#  -> POST /api/batch/submit   (relayer signs, chain settles)
#
# After a batch is submitted+accepted, the withdrawals in it become on-chain
# records → the user's Claim works. When nothing is pending, build returns
# HTTP 400 "no pending settlement operations" and this loop simply waits.
#
# PREREQ: scripts/real_db_mode_up.sh running (DB + BATCH_BUILD_SOURCE=pending),
#         chain + gazk up.
#
# Usage:  bash scripts/settle_loop.sh
# Env:    API_BASE_URL (default http://localhost:8080)
#         SETTLE_INTERVAL (seconds, default 8)
#         ONESHOT=1   (run a single settle pass and exit — handy for e2e)
# ---------------------------------------------------------------------------
set -uo pipefail

API="${API_BASE_URL:-http://localhost:${API_PORT:-8080}}"
INTERVAL="${SETTLE_INTERVAL:-8}"
ONESHOT="${ONESHOT:-0}"

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT

c_reset=$'\033[0m'; c_green=$'\033[1;32m'; c_red=$'\033[1;31m'; c_dim=$'\033[2m'; c_blue=$'\033[1;34m'
ts()  { date +%H:%M:%S; }
log() { echo "[$(ts)] $*"; }
ok()  { echo "[$(ts)] ${c_green}$*${c_reset}"; }
err() { echo "[$(ts)] ${c_red}$*${c_reset}"; }
jget(){ python -c "import json,sys;d=json.load(open('$1'));print(d$2)" 2>/dev/null; }

for c in curl python; do command -v "$c" >/dev/null || { echo "need $c in PATH"; exit 1; }; done
curl -sf "$API/api/health" >/dev/null || { err "backend not up at $API (run scripts/real_db_mode_up.sh)"; exit 1; }

echo "${c_blue}== settlement sequencer ==${c_reset}"
echo "  API=$API   interval=${INTERVAL}s   oneshot=$ONESHOT"
echo "  drains pending  ->  build -> prove -> submit   (Ctrl-C to stop)"

# One settlement pass. Returns: 0 settled, 2 idle (nothing pending), 1 error.
settle_once() {
  local code
  code="$(curl -s -o "$W/build.json" -w '%{http_code}' -X POST "$API/api/batch/build" \
    -H 'Content-Type: application/json' -d '{}')"

  if [ "$code" = "400" ] && grep -q "no pending" "$W/build.json" 2>/dev/null; then
    echo "${c_dim}[$(ts)] idle (no pending)${c_reset}"; return 2
  fi
  [ "$code" = "200" ] || { err "build failed (HTTP $code): $(cat "$W/build.json")"; return 1; }

  local batch; batch="$(jget "$W/build.json" "['settlementUpdate']['batchId']")"
  log "pending batch built: batchId=$batch — proving…"

  # prove
  python -c "import json;d=json.load(open('$W/build.json'));json.dump({'settlementUpdate':d['settlementUpdate'],'batchCommitments':d['batchCommitments'],'witness':d['witness']},open('$W/gen.json','w'))"
  code="$(curl -s -o "$W/proof.json" -w '%{http_code}' -X POST "$API/api/proof/generate" \
    -H 'Content-Type: application/json' -d @"$W/gen.json")"
  [ "$code" = "200" ] || { err "prove failed (HTTP $code): $(cat "$W/proof.json")"; return 1; }

  # submit
  python -c "import json;b=json.load(open('$W/build.json'));p=json.load(open('$W/proof.json'));json.dump({'settlementUpdate':b['settlementUpdate'],'batchCommitments':b['batchCommitments'],'proofBundle':p['proofBundle']},open('$W/submit_req.json','w'))"
  code="$(curl -s -o "$W/submit_resp.json" -w '%{http_code}' -X POST "$API/api/batch/submit" \
    -H 'Content-Type: application/json' -d @"$W/submit_req.json")"
  [ "$code" = "200" ] || { err "submit failed (HTTP $code): $(cat "$W/submit_resp.json")"; return 1; }

  local accepted txhash
  accepted="$(jget "$W/submit_resp.json" "['accepted']")"
  txhash="$(jget "$W/submit_resp.json" ".get('txHash','')")"
  if [ "$accepted" = "True" ]; then
    ok "SETTLED  batchId=$batch  accepted  txHash=$txhash"; return 0
  fi
  err "submit NOT accepted: batchId=$batch (accepted=$accepted) $(cat "$W/submit_resp.json")"; return 1
}

if [ "$ONESHOT" = "1" ]; then
  settle_once; rc=$?; [ "$rc" = 2 ] && log "nothing to settle"; exit 0
fi

while true; do
  settle_once || true
  sleep "$INTERVAL"
done
