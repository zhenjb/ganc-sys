#!/usr/bin/env bash
#
# real_mode_up.sh (SYS-06)
# ---------------------------------------------------------------------------
# Bring up ganc-sys in REAL mode (chain + gazk + backend) and print a ready
# checklist. Assumes the ganc-trade node (obd) is ALREADY running — this script
# does NOT start ignite (that is a heavy, long-lived process you run separately).
#
# What it does:
#   1. Verifies obd + chain RPC/REST + gazk are reachable.
#   2. Starts gazk if not already up (optional; skip with START_GAZK=0).
#   3. Starts the backend with the full real-mode env (memory store — no DB).
#   4. Prints the real alice address + denom to use in e2e.
#
# Usage (Codespace / Git Bash):
#   bash scripts/real_mode_up.sh
# Env overrides: CHAIN_NODE, CHAIN_REST_URL, GAZK_URL, API_PORT, RELAYER_FROM,
#                ASSET_DENOM, START_GAZK, CHAIN_FEES.
# ---------------------------------------------------------------------------
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GANC_SYS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GAZK_DIR="${GAZK_DIR:-$(cd "$GANC_SYS_DIR/../gazk" 2>/dev/null && pwd)}"

API_PORT="${API_PORT:-8080}"
GAZK_PORT="${GAZK_PORT:-8090}"
API_BASE_URL="http://localhost:$API_PORT"
GAZK_URL="${GAZK_URL:-http://localhost:$GAZK_PORT}"

CHAIN_BINARY="${CHAIN_BINARY:-obd}"
CHAIN_ID="${CHAIN_ID:-ob}"
CHAIN_NODE="${CHAIN_NODE:-http://localhost:26657}"
CHAIN_RPC_URL="${CHAIN_RPC_URL:-$CHAIN_NODE}"
CHAIN_REST_URL="${CHAIN_REST_URL:-http://localhost:1317}"
RELAYER_FROM="${RELAYER_FROM:-alice}"
CHAIN_KEYRING_BACKEND="${CHAIN_KEYRING_BACKEND:-test}"
ASSET_DENOM="${ASSET_DENOM:-USDT}"
CHAIN_FEES="${CHAIN_FEES:-0USDT}"
EXPECTED_VK_ID="${EXPECTED_VK_ID:-gazk-balance-smoke-v1}"
CORS_ALLOWED_ORIGINS="${CORS_ALLOWED_ORIGINS:-https://*.app.github.dev,http://localhost:3000}"
START_GAZK="${START_GAZK:-1}"

# DEN-D1/DEN-D2: order-market registry seed. Empty → built-in DefaultMarkets
# (uatom/uusdc). Point ORDER_MARKETS_FILE at a chain-matched config to trade with
# the denoms this chain funds. ASSET_DENOM below is only the deposit-demo denom.
ORDER_MARKETS_JSON="${ORDER_MARKETS_JSON:-}"
ORDER_MARKETS_FILE="${ORDER_MARKETS_FILE:-}"

c_reset=$'\033[0m'; c_blue=$'\033[1;34m'; c_green=$'\033[1;32m'; c_red=$'\033[1;31m'; c_yellow=$'\033[1;33m'
phase() { echo; echo "${c_blue}== $* ==${c_reset}"; }
ok()   { echo "${c_green}[ ok ]${c_reset} $*"; }
warn() { echo "${c_yellow}[warn]${c_reset} $*"; }
die()  { echo "${c_red}FATAL:${c_reset} $*" >&2; exit 1; }
wait_http() { local i; for i in $(seq 1 "${2:-60}"); do curl -sf "$1" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }

phase "Preconditions"
command -v "$CHAIN_BINARY" >/dev/null || die "$CHAIN_BINARY not in PATH (need the ganc-trade node running)"
command -v go >/dev/null || die "go not found"
if curl -sf "$CHAIN_RPC_URL/health" >/dev/null 2>&1; then
  ok "chain RPC $CHAIN_RPC_URL reachable"
else
  die "chain RPC $CHAIN_RPC_URL not reachable. Start the ganc-trade node FIRST (separate terminal):
    cd <ganc-chain repo root>          # dir with exe.sh + sw/ob
    ./exe.sh && source ~/.bashrc       # once: installs the 'ganc' CLI
    ganc chain                         # = cd sw/ob && ignite chain serve --reset-once (first run compiles, ~minutes)
  Wait until blocks commit (curl $CHAIN_RPC_URL/health -> {}), then re-run this script."
fi
curl -sf "$CHAIN_REST_URL/cosmos/base/tendermint/v1beta1/node_info" >/dev/null 2>&1 && ok "chain REST $CHAIN_REST_URL reachable" || warn "chain REST $CHAIN_REST_URL not reachable (balances query will fail)"

ALICE_ADDR="$("$CHAIN_BINARY" keys show "$RELAYER_FROM" -a --keyring-backend "$CHAIN_KEYRING_BACKEND" 2>/dev/null)"
[ -n "$ALICE_ADDR" ] && ok "signer '$RELAYER_FROM' = $ALICE_ADDR" || die "cannot resolve address for key '$RELAYER_FROM'"

phase "gazk (:$GAZK_PORT)"
if curl -sf "$GAZK_URL/health" >/dev/null 2>&1; then
  ok "gazk already up"
elif [ "$START_GAZK" = "1" ] && [ -d "$GAZK_DIR" ]; then
  ( cd "$GAZK_DIR" && GAZK_ADDR=":$GAZK_PORT" go run main.go server >/tmp/gazk.real.log 2>&1 ) &
  wait_http "$GAZK_URL/health" 120 || die "gazk did not become healthy (see /tmp/gazk.real.log)"
  ok "gazk started"
else
  die "gazk not up and START_GAZK!=1 (or gazk dir missing)"
fi
GOT_VK="$(curl -s "$GAZK_URL/health" | python -c "import sys,json;print(json.load(sys.stdin)['verificationKeyId'])" 2>/dev/null)"
[ "$GOT_VK" = "$EXPECTED_VK_ID" ] && ok "gazk vkId=$GOT_VK" || warn "gazk vkId=$GOT_VK (expected $EXPECTED_VK_ID)"

phase "backend (:$API_PORT) — REAL mode (memory store)"
# Free the API port first: a STALE backend still holding it would shadow the new
# build (the new go-run fails to bind, health check hits the old process).
{ lsof -ti "tcp:$API_PORT" 2>/dev/null | xargs -r kill -9; } 2>/dev/null || true
fuser -k "${API_PORT}/tcp" 2>/dev/null || true
sleep 1
: > /tmp/api.real.log
(
  cd "$GANC_SYS_DIR" && \
  PORT="$API_PORT" \
  CORS_ALLOWED_ORIGINS="$CORS_ALLOWED_ORIGINS" \
  PROVER_MODE=remote PROVER_URL="$GAZK_URL" \
  PROOF_VERIFY_ENABLED=true \
  PROOF_VERIFICATION_KEY_ID="$EXPECTED_VK_ID" PROOF_HASH_MODE=v0-sha256 PROOF_PREFLIGHT_STRICT=true \
  RELAYER_MODE=cosmos CHAIN_DEPOSIT_MODE=cosmos INDEXER_MODE=chain CHAIN_QUERY_MODE=cosmos \
  CHAIN_BINARY="$CHAIN_BINARY" CHAIN_ID="$CHAIN_ID" \
  CHAIN_NODE="$CHAIN_NODE" CHAIN_RPC_URL="$CHAIN_RPC_URL" CHAIN_REST_URL="$CHAIN_REST_URL" \
  RELAYER_FROM="$RELAYER_FROM" CHAIN_KEYRING_BACKEND="$CHAIN_KEYRING_BACKEND" \
  CHAIN_FEES="$CHAIN_FEES" \
  ORDER_MARKETS_JSON="$ORDER_MARKETS_JSON" ORDER_MARKETS_FILE="$ORDER_MARKETS_FILE" \
  go run ./cmd/api >/tmp/api.real.log 2>&1
) &
wait_http "$API_BASE_URL/api/health" 60 || { tail -30 /tmp/api.real.log; die "backend did not start (see /tmp/api.real.log)"; }
# This config ALWAYS logs the ZK-gate line. If it's missing, the new backend
# failed to bind (a stale one is answering) — fail loudly instead of testing it.
if grep -q "real ZK verification enabled via remote prover" /tmp/api.real.log; then
  ok "backend real ZK gate enabled (fresh build)"
else
  tail -20 /tmp/api.real.log
  die "backend ZK-gate log missing — a STALE backend is likely still on :$API_PORT (look for 'address already in use'). Kill it and re-run."
fi
ok "backend health OK"

if [ -n "$ORDER_MARKETS_JSON" ]; then MARKETS_SRC="inline ORDER_MARKETS_JSON"
elif [ -n "$ORDER_MARKETS_FILE" ]; then MARKETS_SRC="file:$ORDER_MARKETS_FILE"
else MARKETS_SRC="default (uatom/uusdc, uosmo/uusdc)"; fi

phase "READY"
cat <<EOF
  API      : $API_BASE_URL
  gazk     : $GAZK_URL   (vkId=$GOT_VK)
  chain    : RPC $CHAIN_RPC_URL | REST $CHAIN_REST_URL | chain-id $CHAIN_ID
  signer   : $RELAYER_FROM = $ALICE_ADDR
  denom    : $ASSET_DENOM   (deposit/withdraw demo denom only)
  markets  : $MARKETS_SRC   (tradable order-book denoms — set ORDER_MARKETS_FILE to align)
  logs     : /tmp/api.real.log , /tmp/gazk.real.log

  Next: bash scripts/e2e_real_chain_flow.sh     # SYS-07 Alice 100/40
        bash scripts/e2e_negative_tests.sh      # SYS-08 negatives
  Stop backend/gazk: kill the go processes on ports $API_PORT / $GAZK_PORT.
EOF
