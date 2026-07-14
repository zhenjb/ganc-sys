#!/usr/bin/env bash
# TRD-A2 — export the REAL trade verifying key (vkId gazk-trade-v1) for B to embed
# in x/zkdex (ONCHAIN-T04 / TRD-B).
#
# The vk MUST come from the SAME persisted keys gazk runs with (groth16.Setup is
# randomized — a fresh setup would mint a different vk that verifies nothing). So we
# point GAZK_KEY_DIR at the persisted key dir (default: ganc-sys/.gazk-keys, the one
# the e2e_trade_real wrapper uses) and export from there.
#
# Output: docs/matching_orderbook/gazk-trade-v1.verifier-artifact.json
#   { verificationKeyId, curve, backend, publicInputCount, publicInputNames[8],
#     verifyingKey (0x hex, BN254 groth16), stub:false }
#
# Usage (from ganc-sys repo root):
#   ./scripts/export_trade_vk.sh [OUTPUT_JSON]
set -euo pipefail

cd "$(dirname "$0")/.."
GANC_SYS_DIR="$(pwd)"
GAZK_DIR="${GAZK_DIR:-$(cd "$GANC_SYS_DIR/../gazk" 2>/dev/null && pwd || true)}"
KEY_DIR="${GAZK_KEY_DIR:-$GANC_SYS_DIR/.gazk-keys}"
OUT="${1:-$GANC_SYS_DIR/docs/matching_orderbook/gazk-trade-v1.verifier-artifact.json}"

if [ -z "${GAZK_DIR:-}" ] || [ ! -d "$GAZK_DIR" ]; then
  echo "[export_trade_vk] FATAL: gazk dir not found (set GAZK_DIR=/path/to/gazk)"; exit 1
fi
mkdir -p "$KEY_DIR"

echo "[export_trade_vk] building gazk ..."
GAZK_BIN="$(mktemp -d)/gazk"
( cd "$GAZK_DIR" && go build -o "$GAZK_BIN" . )

echo "[export_trade_vk] exporting trade vk (GAZK_KEY_DIR=$KEY_DIR) ..."
GAZK_KEY_DIR="$KEY_DIR" "$GAZK_BIN" export-trade-verifier-artifact "$OUT"

echo "[export_trade_vk] OK -> $OUT"
echo "[export_trade_vk] hand this file to B; vkId + 8 public-input names are locked."
rm -f "$GAZK_BIN" 2>/dev/null || true
