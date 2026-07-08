#!/usr/bin/env bash
#
# e2e_real_chain_flow.sh (SYS-07)
# ---------------------------------------------------------------------------
# End-to-end acceptance of the canonical Alice 100/40 vector against a REAL
# ganc-trade node + gazk, driven through the ganc-sys REST API in real mode.
#
# Proves:
#   - deposit / submit / claim produce REAL on-chain txHashes (64-hex),
#   - the batch is submitted via the fixed relayer CLI (3 autocli flags),
#   - on-chain bank balances move by the canonical deltas:
#         alice  -= 60   (deposit 100, claim 40)
#         module += 60
#     i.e. with a genesis of 1000 → 940 / 60.
#
# PREREQUISITES (run first, in separate terminals):
#   1. ganc-trade node:  cd ganc-chain/sw/ob && ignite chain serve
#   2. real-mode stack:  bash scripts/real_mode_up.sh    (starts gazk + backend)
# This script does NOT start chain/gazk/backend — it only drives + asserts.
#
# Requirements: obd (in PATH), curl, jq, python, bash (Git Bash on Windows).
# ---------------------------------------------------------------------------
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GANC_SYS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
WORK_DIR="$GANC_SYS_DIR/.e2e_real_tmp"

API_PORT="${API_PORT:-8080}"
API="${API_BASE_URL:-http://localhost:$API_PORT}"
CHAIN_BINARY="${CHAIN_BINARY:-obd}"
NODE="${CHAIN_NODE:-http://localhost:26657}"
CHAIN_ID="${CHAIN_ID:-ob}"
KEYRING="${CHAIN_KEYRING_BACKEND:-test}"
SIGNER="${RELAYER_FROM:-alice}"
DENOM="${ASSET_DENOM:-USDT}"
DEPOSIT_AMT="${DEPOSIT_AMOUNT:-100}"
WITHDRAW_AMT="${WITHDRAW_AMOUNT:-40}"
COMMIT_WAIT="${COMMIT_WAIT:-6}"   # seconds to wait for a block to commit

PASS=0; FAIL=0
c_reset=$'\033[0m'; c_blue=$'\033[1;34m'; c_green=$'\033[1;32m'; c_red=$'\033[1;31m'; c_yellow=$'\033[1;33m'
phase() { echo; echo "${c_blue}== $* ==${c_reset}"; }
note()  { echo "${c_yellow}note:${c_reset} $*"; }
ok()    { echo "${c_green}[ PASS ]${c_reset} $*"; PASS=$((PASS+1)); }
bad()   { echo "${c_red}[ FAIL ]${c_reset} $*"; FAIL=$((FAIL+1)); }
die()   { echo "${c_red}FATAL:${c_reset} $*" >&2; exit 1; }
jget()  { python -c "import json;d=json.load(open('$1'));print($2)" 2>/dev/null; }
is_txhash() { [[ "$1" =~ ^[A-Fa-f0-9]{64}$ ]]; }

bank_balance() {  # bank_balance <addr> -> integer amount of DENOM (0 if none)
  "$CHAIN_BINARY" q bank balances "$1" --node "$NODE" -o json 2>/dev/null \
    | jq -r --arg d "$DENOM" '(.balances[]?|select(.denom==$d)|.amount) // "0"' | head -n1
}
module_balance() {  # module account spendable balance of DENOM (digits only)
  "$CHAIN_BINARY" q zkdex module-account-balance "$DENOM" --node "$NODE" -o json 2>/dev/null \
    | jq -r '((.balance.amount? // .balance // .amount // "0") | tostring | gsub("[^0-9]"; ""))' \
    | head -n1
}

# ---------------------------------------------------------------------------
phase "PHASE 0 — Preconditions"
for c in "$CHAIN_BINARY" curl jq python; do command -v "$c" >/dev/null || die "$c not found"; done
curl -sf "$API/api/health" >/dev/null || die "backend not up at $API (run scripts/real_mode_up.sh first)"
curl -sf "$NODE/health" >/dev/null || die "chain RPC $NODE not reachable"
ALICE="$("$CHAIN_BINARY" keys show "$SIGNER" -a --keyring-backend "$KEYRING" 2>/dev/null)"
[ -n "$ALICE" ] || die "cannot resolve address for '$SIGNER'"
note "signer=$SIGNER addr=$ALICE denom=$DENOM"
rm -rf "$WORK_DIR"; mkdir -p "$WORK_DIR"; cd "$WORK_DIR"

ALICE_BAL0="$(bank_balance "$ALICE")"; MOD_BAL0="$(module_balance)"
[[ "$ALICE_BAL0" =~ ^[0-9]+$ ]] || die "could not read alice bank balance (got '$ALICE_BAL0')"
[[ "$MOD_BAL0" =~ ^[0-9]+$ ]] || MOD_BAL0=0
ok "baseline captured: alice=$ALICE_BAL0 $DENOM, module=$MOD_BAL0 $DENOM"

# ---------------------------------------------------------------------------
phase "PHASE 1 — Deposit $DEPOSIT_AMT (real tx)"
curl -s -X POST "$API/api/deposit" -H 'Content-Type: application/json' \
  -d "{\"owner\":\"$ALICE\",\"denom\":\"$DENOM\",\"amount\":\"$DEPOSIT_AMT\"}" -o deposit.json
DEP_ID="$(jget deposit.json "d['depositRecord']['depositId']")"
DEP_TX="$(jget deposit.json "d.get('txHash','')")"
[ -n "$DEP_ID" ] && ok "deposit indexed (depositId=$DEP_ID)" || { cat deposit.json; bad "deposit failed"; }
is_txhash "$DEP_TX" && ok "deposit REAL txHash=$DEP_TX" || bad "deposit txHash not a real 64-hex: '$DEP_TX'"
note "waiting ${COMMIT_WAIT}s for deposit commit"; sleep "$COMMIT_WAIT"

# ---------------------------------------------------------------------------
phase "PHASE 2 — Withdraw request $WITHDRAW_AMT"
curl -s -X POST "$API/api/withdraw-request" -H 'Content-Type: application/json' \
  -d "{\"owner\":\"$ALICE\",\"denom\":\"$DENOM\",\"amount\":\"$WITHDRAW_AMT\",\"destination\":\"$ALICE\"}" -o withdraw.json
WD_ID="$(jget withdraw.json "d['withdrawRequest']['withdrawId']")"
[ -n "$WD_ID" ] && ok "withdraw request created (withdrawId=$WD_ID)" || { cat withdraw.json; bad "withdraw-request failed"; }

# ---------------------------------------------------------------------------
phase "PHASE 3 — Build batch (manual source, explicit ids, memory store)"
curl -s -X POST "$API/api/batch/build" -H 'Content-Type: application/json' \
  -d "{\"depositIds\":[\"$DEP_ID\"],\"withdrawIds\":[\"$WD_ID\"]}" -o build.json
BATCH_ID="$(jget build.json "d['settlementUpdate']['batchId']")"
[ -n "$BATCH_ID" ] && ok "batch built (batchId=$BATCH_ID)" || { cat build.json; bad "batch/build failed"; }

# ---------------------------------------------------------------------------
phase "PHASE 4 — Generate proof (gazk)"
python -c "import json;d=json.load(open('build.json'));json.dump({'settlementUpdate':d['settlementUpdate'],'batchCommitments':d['batchCommitments'],'witness':d['witness']},open('genproof.json','w'))"
curl -s -X POST "$API/api/proof/generate" -H 'Content-Type: application/json' -d @genproof.json -o proof.json
PROOF_VK="$(jget proof.json "d['proofBundle']['verificationKeyId']")"
[ "$PROOF_VK" = "gazk-balance-smoke-v1" ] && ok "proof from gazk (vkId=$PROOF_VK)" || { cat proof.json; bad "proof vkId=$PROOF_VK"; }
python -c "import json;b=json.load(open('build.json'));p=json.load(open('proof.json'));json.dump({'settlementUpdate':b['settlementUpdate'],'batchCommitments':b['batchCommitments'],'proofBundle':p['proofBundle']},open('submit.json','w'))"

# ---------------------------------------------------------------------------
phase "PHASE 5 — Submit batch (real relayer -> obd, 3 autocli flags)"
curl -s -X POST "$API/api/batch/submit" -H 'Content-Type: application/json' -d @submit.json -o submit_resp.json
ACCEPTED="$(jget submit_resp.json "d['accepted']")"
SUB_TX="$(jget submit_resp.json "d.get('txHash','')")"
[ "$ACCEPTED" = "True" ] && ok "batch accepted on chain" || { cat submit_resp.json; bad "submit not accepted"; }
is_txhash "$SUB_TX" && ok "submit REAL txHash=$SUB_TX" || bad "submit txHash not a real 64-hex: '$SUB_TX'"
note "waiting ${COMMIT_WAIT}s for submit commit"; sleep "$COMMIT_WAIT"

# ---------------------------------------------------------------------------
phase "PHASE 6 — Claim withdrawal (signed by destination)"
curl -s -X POST "$API/api/withdraw/claim" -H 'Content-Type: application/json' -d "{\"withdrawId\":\"$WD_ID\"}" -o claim.json
CLAIMED="$(jget claim.json "d['withdrawRecord']['claimed']")"
CLAIM_TX="$(jget claim.json "d.get('txHash','')")"
[ "$CLAIMED" = "True" ] && ok "withdraw claimed" || { cat claim.json; bad "claim failed"; }
is_txhash "$CLAIM_TX" && ok "claim REAL txHash=$CLAIM_TX" || bad "claim txHash not a real 64-hex: '$CLAIM_TX'"
note "waiting ${COMMIT_WAIT}s for claim commit"; sleep "$COMMIT_WAIT"

# ---------------------------------------------------------------------------
phase "PHASE 7 — On-chain balance deltas (canonical)"
ALICE_BAL1="$(bank_balance "$ALICE")"; MOD_BAL1="$(module_balance)"
[[ "$MOD_BAL1" =~ ^[0-9]+$ ]] || MOD_BAL1=0
EXP_ALICE=$(( ALICE_BAL0 - DEPOSIT_AMT + WITHDRAW_AMT ))
EXP_MOD=$(( MOD_BAL0 + DEPOSIT_AMT - WITHDRAW_AMT ))
echo "  alice : $ALICE_BAL0 -> $ALICE_BAL1  (expect $EXP_ALICE)"
echo "  module: $MOD_BAL0 -> $MOD_BAL1  (expect $EXP_MOD)"
[ "$ALICE_BAL1" = "$EXP_ALICE" ] && ok "alice balance moved by -$((DEPOSIT_AMT-WITHDRAW_AMT)) (canonical)" || bad "alice balance = $ALICE_BAL1, expected $EXP_ALICE"
[ "$MOD_BAL1" = "$EXP_MOD" ] && ok "module balance moved by +$((DEPOSIT_AMT-WITHDRAW_AMT)) (canonical)" || bad "module balance = $MOD_BAL1, expected $EXP_MOD"

# ---------------------------------------------------------------------------
phase "SUMMARY"
echo "PASS: $PASS   FAIL: $FAIL"
echo "txHashes — deposit=$DEP_TX submit=$SUB_TX claim=$CLAIM_TX"
[ "$FAIL" -eq 0 ] && { echo "${c_green}SYS-07 e2e (real chain): ALL CHECKS PASSED${c_reset}"; exit 0; } \
                  || { echo "${c_red}SYS-07 e2e (real chain): $FAIL CHECK(S) FAILED${c_reset}"; exit 1; }
