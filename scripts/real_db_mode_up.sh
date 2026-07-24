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
#                GAZK_KEY_DIR, TRADE_PROVER_MODE, TRADE_SUBMIT_MODE, ORDER_SIG_MODE,
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
EXPECTED_VK_ID="${EXPECTED_VK_ID:-gazk-trade-v1}"  # TRD-UNIFY: mọi batch dùng circuit thật gazk-trade-v1
CORS_ALLOWED_ORIGINS="${CORS_ALLOWED_ORIGINS:-https://*.app.github.dev,http://localhost:3000}"
START_GAZK="${START_GAZK:-1}"

# TRD-A2/TRD-SYNC — gazk MUST prove with the PERSISTED key whose vk is embedded
# on-chain as GazkTradeV1VerifyingKeyHex. An unset GAZK_KEY_DIR makes gazk run a
# fresh groth16.Setup on every start, SILENTLY (gazk prover/key_store.go), so the
# proof never verifies on-chain and nothing warns you. Same default as
# scripts/export_trade_vk.sh, so the vk exported for B matches the pk used here.
GAZK_KEY_DIR="${GAZK_KEY_DIR:-$GANC_SYS_DIR/.gazk-keys}"

# TRD-V1.0 — trade prover and submitter are chosen INDEPENDENTLY. Real mode wants
# a REAL gazk proof SUBMITTED to the chain. Left unset these resolve to local-stub
# prover + chain submit (cmd/api/trade_settlement_wiring.go), i.e. a FAKE proof
# against the REAL on-chain verifier (TRD-B3) — always rejected.
TRADE_PROVER_MODE="${TRADE_PROVER_MODE:-remote}"
TRADE_SUBMIT_MODE="${TRADE_SUBMIT_MODE:-chain}"
GAZK_TRADE_URL="${GAZK_TRADE_URL:-$GAZK_URL}"

# INT-FE-A2 — order authentication. Default REAL (no mock): every order must carry
# a valid Cosmos ADR-036 secp256k1 wallet signature whose pubkey derives to the
# order's owner. This is why real mode needs a connected wallet (Keplr/Leap) to
# place orders. Override ORDER_SIG_MODE=mock ONLY for headless order e2e that
# cannot sign with a browser wallet (e.g. scripts using p3/.../sign_order).
ORDER_SIG_MODE="${ORDER_SIG_MODE:-adr36}"

# STP_MODE — Self-Trade Prevention policy when a new order would cross the
# owner's OWN resting order: cancel-newest (default; cancels the just-placed
# order), cancel-oldest (cancels the resting order), or cancel-both.
STP_MODE="${STP_MODE:-cancel-newest}"

# DEN-D1/DEN-D2: order-market registry seed. When BOTH are empty the backend uses
# its built-in DefaultMarkets (uatom/uusdc/uosmo). To trade on a chain that funds
# different denoms, point ORDER_MARKETS_FILE at a matching config, e.g.:
#   ORDER_MARKETS_FILE=docs/matching_orderbook/markets.sample.json
# or inline via ORDER_MARKETS_JSON='[{"market":"ATOM/USDT","baseDenom":"ATOM",...}]'.
# NOTE: ASSET_DENOM below is only the deposit/withdraw DEMO denom (shown in READY);
# the tradable market denoms come from here, not from ASSET_DENOM.
ORDER_MARKETS_JSON="${ORDER_MARKETS_JSON:-}"
ORDER_MARKETS_FILE="${ORDER_MARKETS_FILE:-}"

# DB / off-chain settlement config.
DATABASE_URL="${DATABASE_URL:-postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable}"
DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
# Pin the off-chain genesis root to the chain's genesis currentStateRoot so the
# FIRST pending batch's oldStateRoot is accepted on-chain. The ganc-trade zkdex
# module now seeds the genesis root to DefaultStateRoot = 32-byte all-zeros
# (x/zkdex/types/genesis.go), and validates every public input as 32-byte hex, so
# the old "0xrootA" placeholder is rejected. Match the all-zeros genesis here.
OFFCHAIN_GENESIS_ROOT="${OFFCHAIN_GENESIS_ROOT:-0x0000000000000000000000000000000000000000000000000000000000000000}"

# The off-chain DB (pending queue + state cursor) MUST reset in lockstep with the
# chain. Since `ganc chain` uses --reset-once (chain always boots at genesis
# "0xrootA"), stale pending rows / an old cursor from a previous run would (a)
# collide on the unique nullifier index and (b) break the transition chain
# ("cannot continue from <old root>"). Default: reset. Set RESET_OFFCHAIN_DB=0
# ONLY if you did NOT reset the chain and want to keep the durable queue.
RESET_OFFCHAIN_DB="${RESET_OFFCHAIN_DB:-1}"
PG_CONTAINER="${PG_CONTAINER:-ganc-pg}"

# In-process settlement sequencer (the "operator"). Default ON — it is the
# production driver now: the backend itself drains the pending queue and does
# build->prove->submit on an interval. Set SETTLEMENT_WORKER_ENABLED=false to
# disable it and drive settlement manually with scripts/settle_loop.sh instead.
# Do NOT run both at once (single-writer: they would race for the same pending).
SETTLEMENT_WORKER_ENABLED="${SETTLEMENT_WORKER_ENABLED:-true}"
SETTLEMENT_INTERVAL="${SETTLEMENT_INTERVAL:-8s}"

# By default this script stays ATTACHED after startup: it streams the backend
# log to your terminal and Ctrl-C stops the backend (and gazk, if it started
# it). Set DETACH=1 to keep the old behaviour — start everything in the
# background, print READY, and exit (useful for scripted e2e runs).
DETACH="${DETACH:-0}"
# FG=1 chạy backend Ở FOREGROUND: log đi THẲNG ra terminal này (không redirect vào
# /tmp/api.real.log, không tail). Dùng khi muốn xem log backend realtime trực tiếp.
# gazk vẫn chạy nền; Ctrl-C dừng backend (trap cleanup dọn cả gazk). Loại trừ với
# DETACH (foreground không thể detach).
FG="${FG:-0}"
STARTED_GAZK=0

c_reset=$'\033[0m'; c_blue=$'\033[1;34m'; c_green=$'\033[1;32m'; c_red=$'\033[1;31m'; c_yellow=$'\033[1;33m'
phase() { echo; echo "${c_blue}== $* ==${c_reset}"; }
ok()   { echo "${c_green}[ ok ]${c_reset} $*"; }
warn() { echo "${c_yellow}[warn]${c_reset} $*"; }
die()  { echo "${c_red}FATAL:${c_reset} $*" >&2; exit 1; }
wait_http() { local i; for i in $(seq 1 "${2:-60}"); do curl -sf "$1" >/dev/null 2>&1 && return 0; sleep 1; done; return 1; }
tcp_up()    { (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null && exec 3>&- && return 0 || return 1; }

# Kill whatever listens on a TCP port (the compiled backend/gazk binaries that
# `go run` spawned; killing `go run` alone would leave the child running).
kill_port() {
  { lsof -ti "tcp:$1" 2>/dev/null | xargs -r kill -9; } 2>/dev/null || true
  fuser -k "$1/tcp" 2>/dev/null || true
}

# Ctrl-C handler for attached mode: tear down the backend (and gazk if we
# started it) so the terminal returns cleanly instead of orphaning processes.
cleanup() {
  echo; echo "${c_yellow}stopping backend on :$API_PORT...${c_reset}"
  kill_port "$API_PORT"
  if [ "$STARTED_GAZK" = "1" ]; then
    echo "${c_yellow}stopping gazk on :$GAZK_PORT...${c_reset}"
    kill_port "$GAZK_PORT"
  fi
  exit 0
}
trap cleanup INT TERM

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
  warn "gazk was ALREADY running — this script cannot verify its key dir."
  warn "  It must be GAZK_KEY_DIR=$GAZK_KEY_DIR, else the trade proof will NOT verify on-chain."
elif [ "$START_GAZK" = "1" ] && [ -d "$GAZK_DIR" ]; then
  ( cd "$GAZK_DIR" && GAZK_KEY_DIR="$GAZK_KEY_DIR" GAZK_ADDR=":$GAZK_PORT" go run main.go server >/tmp/gazk.real.log 2>&1 ) &
  STARTED_GAZK=1
  wait_http "$GAZK_URL/health" 120 || die "gazk did not become healthy (see /tmp/gazk.real.log)"
  ok "gazk started"
else
  die "gazk not up and START_GAZK!=1 (or gazk dir missing)"
fi
GOT_VK="$(curl -s "$GAZK_URL/health" | python -c "import sys,json;d=json.load(sys.stdin);print(d.get('tradeVerificationKeyId') or d.get('verificationKeyId',''))" 2>/dev/null)"
[ "$GOT_VK" = "$EXPECTED_VK_ID" ] && ok "gazk vkId=$GOT_VK" || warn "gazk vkId=$GOT_VK (expected $EXPECTED_VK_ID)"

phase "backend (:$API_PORT) — REAL mode + Postgres + off-chain settlement"
kill_port "$API_PORT"
sleep 1

# Reset the off-chain DB to match the fresh chain (must happen AFTER the old
# backend is killed and BEFORE the new one starts + rehydrates).
if [ "$RESET_OFFCHAIN_DB" = "1" ]; then
  if docker exec -i "$PG_CONTAINER" psql -U ganc -d ganc_sys >/dev/null 2>&1 <<'SQL'
TRUNCATE offchain_pending_deposits, offchain_pending_withdrawals,
         withdraw_requests, batch_builds,
         proof_bundles, submit_batches, indexed_withdraw_records;
DELETE FROM offchain_state_cursors;
ALTER SEQUENCE withdraw_request_seq RESTART;
SQL
  then
    ok "off-chain DB reset (pending queue + cursor cleared — matches fresh chain)"
  else
    warn "off-chain DB reset skipped (container '$PG_CONTAINER' unreachable). If you reset the chain, clear it manually or set the right PG_CONTAINER."
  fi
else
  warn "RESET_OFFCHAIN_DB=0 — keeping durable queue. Only correct if the chain was NOT reset."
fi

# run_api khởi chạy backend với toàn bộ env REAL + Postgres + off-chain settlement.
# KHÔNG tự redirect — caller quyết định: nền + ghi file (mặc định) hoặc foreground
# (FG=1, log đi thẳng ra terminal).
run_api() {
  cd "$GANC_SYS_DIR" && \
  PORT="$API_PORT" \
  CORS_ALLOWED_ORIGINS="$CORS_ALLOWED_ORIGINS" \
  PROVER_MODE=remote PROVER_URL="$GAZK_URL" \
  PROOF_VERIFY_ENABLED=true \
  PROOF_VERIFICATION_KEY_ID="$EXPECTED_VK_ID" PROOF_HASH_MODE="" PROOF_PREFLIGHT_STRICT=true \
  TRADE_PROVER_MODE="$TRADE_PROVER_MODE" TRADE_SUBMIT_MODE="$TRADE_SUBMIT_MODE" \
  GAZK_TRADE_URL="$GAZK_TRADE_URL" \
  RELAYER_MODE=cosmos CHAIN_DEPOSIT_MODE=cosmos INDEXER_MODE=chain CHAIN_QUERY_MODE=cosmos \
  ORDER_SIG_MODE="$ORDER_SIG_MODE" STP_MODE="$STP_MODE" \
  CHAIN_BINARY="$CHAIN_BINARY" CHAIN_ID="$CHAIN_ID" \
  CHAIN_NODE="$CHAIN_NODE" CHAIN_RPC_URL="$CHAIN_RPC_URL" CHAIN_REST_URL="$CHAIN_REST_URL" \
  RELAYER_FROM="$RELAYER_FROM" CHAIN_KEYRING_BACKEND="$CHAIN_KEYRING_BACKEND" \
  CHAIN_FEES="$CHAIN_FEES" \
  ORDER_MARKETS_JSON="$ORDER_MARKETS_JSON" ORDER_MARKETS_FILE="$ORDER_MARKETS_FILE" \
  DATABASE_URL="$DATABASE_URL" \
  WITHDRAW_REQUEST_STORE=postgres WITHDRAW_RECORD_STORE=postgres \
  BATCH_BUILD_STORE=postgres PROOF_BUNDLE_STORE=postgres SUBMIT_BATCH_STORE=postgres \
  OFFCHAIN_SETTLEMENT_ENABLED=true BATCH_BUILD_SOURCE=pending \
  OFFCHAIN_GENESIS_ROOT="$OFFCHAIN_GENESIS_ROOT" \
  SETTLEMENT_WORKER_ENABLED="$SETTLEMENT_WORKER_ENABLED" SETTLEMENT_INTERVAL="$SETTLEMENT_INTERVAL" \
  go run ./cmd/api
}

# FG=1 — backend chạy FOREGROUND: log đi thẳng ra terminal này (không file, không
# tail). Bỏ qua auto-check marker (bạn thấy trực tiếp trong log: "real ZK
# verification enabled via remote prover", "settlement sequencer started"). gazk
# preflight phía trên đã xác nhận vkId. In READY gọn rồi block cho tới Ctrl-C.
if [ "$FG" = "1" ]; then
  phase "READY (DB mode · FOREGROUND — log realtime)"
  cat <<EOF
  API      : $API_BASE_URL
  gazk     : $GAZK_URL   (vkId=$GOT_VK, keyDir=$GAZK_KEY_DIR)
  trade    : prover=$TRADE_PROVER_MODE submit=$TRADE_SUBMIT_MODE
  order-sig: ORDER_SIG_MODE=$ORDER_SIG_MODE   (adr36 = real wallet ADR-036; needs a connected wallet to place orders)
  chain    : RPC $CHAIN_RPC_URL | REST $CHAIN_REST_URL | chain-id $CHAIN_ID
  signer   : $RELAYER_FROM = $ALICE_ADDR
  sequencer: SETTLEMENT_WORKER_ENABLED=$SETTLEMENT_WORKER_ENABLED (interval=$SETTLEMENT_INTERVAL)
  gazk log : /tmp/gazk.real.log
EOF
  echo
  echo "${c_blue}== backend log (realtime) — Ctrl-C để dừng backend (và gazk) ==${c_reset}"
  echo
  run_api          # block; log ra thẳng terminal; trap cleanup dọn gazk khi Ctrl-C
  exit 0
fi

: > /tmp/api.real.log
run_api >/tmp/api.real.log 2>&1 &
wait_http "$API_BASE_URL/api/health" 90 || { tail -40 /tmp/api.real.log; die "backend did not start (see /tmp/api.real.log)"; }

# Assert the two markers that distinguish this mode from memory mode.
grep -q "real ZK verification enabled via remote prover" /tmp/api.real.log \
  && ok "backend real ZK gate enabled" \
  || { tail -30 /tmp/api.real.log; die "ZK-gate log missing — a STALE backend is likely still on :$API_PORT. Kill it and re-run."; }
grep -q "offchain settlement enabled=true" /tmp/api.real.log \
  && ok "off-chain settlement enabled (postgres pending queue)" \
  || { tail -30 /tmp/api.real.log; die "off-chain settlement NOT enabled — check DATABASE_URL / stores env."; }

# In-process sequencer marker. When enabled, the backend auto-settles — no need
# to run scripts/settle_loop.sh separately.
if [ "$SETTLEMENT_WORKER_ENABLED" = "true" ]; then
  grep -q "settlement sequencer started (in-process)" /tmp/api.real.log \
    && ok "in-process settlement sequencer RUNNING (interval=$SETTLEMENT_INTERVAL) — auto build->prove->submit" \
    || { tail -30 /tmp/api.real.log; die "sequencer did NOT start — check SETTLEMENT_WORKER_ENABLED / BATCH_BUILD_SOURCE=pending."; }
else
  warn "in-process sequencer DISABLED — drive settlement manually: bash scripts/settle_loop.sh"
fi
ok "backend health OK"

if [ -n "$ORDER_MARKETS_JSON" ]; then MARKETS_SRC="inline ORDER_MARKETS_JSON"
elif [ -n "$ORDER_MARKETS_FILE" ]; then MARKETS_SRC="file:$ORDER_MARKETS_FILE"
else MARKETS_SRC="default (uatom/uusdc, uosmo/uusdc)"; fi

phase "READY (DB mode)"
cat <<EOF
  API      : $API_BASE_URL
  gazk     : $GAZK_URL   (vkId=$GOT_VK, keyDir=$GAZK_KEY_DIR)
  trade    : prover=$TRADE_PROVER_MODE submit=$TRADE_SUBMIT_MODE   (TRD-V1: remote+chain = real gazk proof -> chain)
  order-sig: ORDER_SIG_MODE=$ORDER_SIG_MODE   (adr36 = real wallet ADR-036 signature bound to owner; no mock)
  chain    : RPC $CHAIN_RPC_URL | REST $CHAIN_REST_URL | chain-id $CHAIN_ID
  db       : $DATABASE_URL
  stores   : postgres (withdraw/batch/proof/submit) + offchain pending queue
  build    : BATCH_BUILD_SOURCE=pending  (batch/build auto-collects pending)
  genesis  : OFFCHAIN_GENESIS_ROOT=$OFFCHAIN_GENESIS_ROOT  (matches chain genesis root)
  signer   : $RELAYER_FROM = $ALICE_ADDR
  denom    : $ASSET_DENOM   (deposit/withdraw demo denom only)
  markets  : $MARKETS_SRC   (tradable order-book denoms — set ORDER_MARKETS_FILE to align to this chain)
  sequencer: SETTLEMENT_WORKER_ENABLED=$SETTLEMENT_WORKER_ENABLED (interval=$SETTLEMENT_INTERVAL)
  logs     : /tmp/api.real.log , /tmp/gazk.real.log

  Backend runs in the BACKGROUND — its logs (incl. the sequencer) go to /tmp/api.real.log.
  Watch settlement live:
    tail -f /tmp/api.real.log | grep --line-buffered settlement-sequencer
  Or watch the whole backend:
    tail -f /tmp/api.real.log

  Settlement is AUTOMATIC when the sequencer is enabled (above) — you do NOT need
  scripts/settle_loop.sh. Just deposit + withdraw; the worker settles each pending
  op and logs: "SETTLED batch=... txHash=...".
  (Manual/dev only, and ONLY if SETTLEMENT_WORKER_ENABLED=false: bash scripts/settle_loop.sh)
EOF

if [ "$DETACH" = "1" ]; then
  echo
  ok "DETACH=1 — backend + gazk left running in the background. Logs: /tmp/api.real.log"
  ok "Stop later: kill the go processes on ports $API_PORT / $GAZK_PORT."
  exit 0
fi

echo
echo "${c_blue}== streaming backend log — press Ctrl-C to STOP the backend ==${c_reset}"
echo "${c_yellow}(tip: run 'DETACH=1 bash scripts/real_db_mode_up.sh' to start it in the background instead)${c_reset}"
echo
# Foreground stream. On Ctrl-C the INT trap (cleanup) tears down backend + gazk.
# NOT exec'd, so the trap stays installed to run cleanup.
tail -n +1 -f /tmp/api.real.log
