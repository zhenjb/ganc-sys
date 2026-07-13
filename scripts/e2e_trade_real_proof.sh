#!/usr/bin/env bash
# ZK-T10 integration — one-command E2E trade with a REAL gazk ZK proof.
#
# Starts gazk (persisted keys via GAZK_KEY_DIR), waits for /health, drives the
# alice-buy/bob-sell trade through the RealOrderService wired to the RemoteTrade
# Prover (POST /prove {trade}) + RemoteTradeVerifierSubmitter (POST /verify-trade),
# asserts the settle completes with a real proof, then stops gazk.
#
# gazk lives as a sibling of ganc-sys by default; override with GAZK_DIR.
#
# Usage (from ganc-sys repo root):
#   ./scripts/e2e_trade_real_proof.sh
set -euo pipefail

cd "$(dirname "$0")/.."
GANC_SYS_DIR="$(pwd)"
GAZK_DIR="${GAZK_DIR:-$(cd "$GANC_SYS_DIR/../gazk" 2>/dev/null && pwd || true)}"
GAZK_PORT="${GAZK_PORT:-8090}"
GAZK_URL="http://localhost:${GAZK_PORT}"

if [ -z "${GAZK_DIR:-}" ] || [ ! -d "$GAZK_DIR" ]; then
  echo "[e2e_trade_real] FATAL: gazk dir not found (set GAZK_DIR=/path/to/gazk)"; exit 1
fi

# Persisted keys: pk/vk are generated once, reused across runs, and — crucially —
# the same setup produces the proof AND the vk that verifies it.
KEY_DIR="${GAZK_KEY_DIR:-$GANC_SYS_DIR/.gazk-keys}"
mkdir -p "$KEY_DIR"

echo "[e2e_trade_real] building gazk ..."
GAZK_BIN="$(mktemp -d)/gazk"
( cd "$GAZK_DIR" && go build -o "$GAZK_BIN" . )

echo "[e2e_trade_real] starting gazk on :${GAZK_PORT} (GAZK_KEY_DIR=$KEY_DIR) ..."
GAZK_KEY_DIR="$KEY_DIR" GAZK_ADDR=":${GAZK_PORT}" "$GAZK_BIN" server >/tmp/e2e_gazk.log 2>&1 &
GAZK_PID=$!
cleanup() { kill "$GAZK_PID" 2>/dev/null || true; rm -f "$GAZK_BIN" 2>/dev/null || true; }
trap cleanup EXIT

# Wait for health (first request also warms the lazy trade engine setup).
for i in $(seq 1 40); do
  if curl -s --max-time 2 "$GAZK_URL/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
if ! curl -s --max-time 2 "$GAZK_URL/health" >/dev/null 2>&1; then
  echo "[e2e_trade_real] FATAL: gazk did not come up"; cat /tmp/e2e_gazk.log; exit 1
fi

echo "[e2e_trade_real] running e2e driver ..."
GAZK_TRADE_URL="$GAZK_URL" go run ./p3/script-test/e2e_trade_real

echo "[e2e_trade_real] OK"
