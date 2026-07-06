#!/usr/bin/env bash
#
# real_db_mode_up.sh (Phase 2) — REAL mode + Postgres + off-chain settlement.
# ---------------------------------------------------------------------------
# Same as real_mode_up.sh but backed by a durable Postgres pending queue so the
# settlement sequencer (scripts/settle_loop.sh, Phase 3) can auto-batch/prove/
# submit. Deposits + withdraw-requests are persisted and mirrored into the
# off-chain pending state; batch/build pulls from that pending source.
#
# PREREQ: `bash scripts/pg_up.sh` already ran (Postgres on localhost:5432,
#         migrations applied). Chain (obd / ignite) running separately.
#
# Usage (Codespace / Git Bash):  bash scripts/real_db_mode_up.sh
# Env overrides: DATABASE_URL, CHAIN_NODE, CHAIN_REST_URL, GAZK_URL, API_PORT,
#                RELAYER_FROM, ASSET_DENOM, START_GAZK, CHAIN_FEES.
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

# DB / off-chain settlement config.
DATABASE_URL="${DATABASE_URL:-postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable}"
DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"

c_reset=$'\033[0m'; c_blue=$'\033[1;34m'; c_green=$'\033[1;32m'; c_red=$'\033[1;31m'; c_yellow=$'\033[1;33m'
phase() { echo; echo "${c_blue}== $* ==${c_reset}"; }
ok()   { echo "${c_green}[ ok ]${c_reset} $*"; }
warn() { echo "${c_yellow}[warn]${c_reset} $*"; }
die()  { echo "${c_red}FATAL:${c_reset} $*" >&2; exit 1; }
wait_http() { local i; for i in $(seq 1 "${2:-60}"); do curl -sf "$1" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }
tcp_up()    { (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null && exec 3>&- && return 0 || return 1; }

phase "Preconditions"
command -v "$CHAIN_BINARY" >/dev/null || die "$CHAIN_BINARY not in PATH (need the ganc-trade node running)"
command -v go >/dev/null || die "go not found"

# Postgres must be reachable BEFORE the backend starts, else openDatabaseIfNeeded
# fails hard (stores=postgres / offchain enabled both force a DB connection).
if tcp_up "$DB_HOST" "$DB_PORT"; then
  ok "postgres $DB_HOST:$DB_PORT reachable"
else
  die "postgres $DB_HOST:$DB_PORT not reachable. Run Phase 1 first: bash scripts/pg_up.sh"
fi

if curl -sf "$CHAIN_RPC_URL/health" >/dev/null 2>&1; then
  ok "chain RPC $CHAIN_RPC_URL reachable"
else
  die "chain RPC $CHAIN_RPC_URL not reachable. Start the ganc-trade node FIRST (separate terminal):
    ganc chain    # = ignite chain serve --reset-once
  Wait until blocks commit (curl $CHAIN_RPC_URL/health -> {}), then re-run."
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

phase "backend (:$API_PORT) — REAL mode + Postgres + off-chain settlement"
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
  DATABASE_URL="$DATABASE_URL" \
  WITHDRAW_REQUEST_STORE=postgres WITHDRAW_RECORD_STORE=postgres \
  BATCH_BUILD_STORE=postgres PROOF_BUNDLE_STORE=postgres SUBMIT_BATCH_STORE=postgres \
  OFFCHAIN_SETTLEMENT_ENABLED=true BATCH_BUILD_SOURCE=pending \
  go run ./cmd/api >/tmp/api.real.log 2>&1
) &
wait_http "$API_BASE_URL/api/health" 90 || { tail -40 /tmp/api.real.log; die "backend did not start (see /tmp/api.real.log)"; }

# Assert the two markers that distinguish this mode from memory mode.
grep -q "real ZK verification enabled via remote prover" /tmp/api.real.log \
  && ok "backend real ZK gate enabled" \
  || { tail -30 /tmp/api.real.log; die "ZK-gate log missing — a STALE backend is likely still on :$API_PORT. Kill it and re-run."; }
grep -q "offchain settlement enabled=true" /tmp/api.real.log \
  && ok "off-chain settlement enabled (postgres pending queue)" \
  || { tail -30 /tmp/api.real.log; die "off-chain settlement NOT enabled — check DATABASE_URL / stores env."; }
ok "backend health OK"

phase "READY (DB mode)"
cat <<EOF
  API      : $API_BASE_URL
  gazk     : $GAZK_URL   (vkId=$GOT_VK)
  chain    : RPC $CHAIN_RPC_URL | REST $CHAIN_REST_URL | chain-id $CHAIN_ID
  db       : $DATABASE_URL
  stores   : postgres (withdraw/batch/proof/submit) + offchain pending queue
  build    : BATCH_BUILD_SOURCE=pending  (batch/build auto-collects pending)
  signer   : $RELAYER_FROM = $ALICE_ADDR
  denom    : $ASSET_DENOM
  logs     : /tmp/api.real.log , /tmp/gazk.real.log

  Next (Phase 3): bash scripts/settle_loop.sh    # sequencer: auto build->prove->submit
  Stop backend/gazk: kill the go processes on ports $API_PORT / $GAZK_PORT.
EOF
