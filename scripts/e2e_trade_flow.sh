#!/usr/bin/env bash
# INT-T09 — one-command E2E trading demo.
#
# Runs the full off-chain trading round-trip and prints report evidence:
#   alice BUY + bob SELL cross on ATOM/USDC → match (INT-T05) → build(trades[]) →
#   prove(stub) → submit(relayer) → new state root, with balances/reserved/fee
#   transitioning correctly.
#
# It is self-contained and idempotent: the demo boots the real API router over an
# in-process server with a FRESH funded off-chain state each run (no chain, no DB,
# no nonce/nullifier carryover), driving POST /api/order, GET /api/trades and
# GET /api/state over HTTP exactly as the frontend would. The program exits
# non-zero on any assertion failure, so this doubles as a CI gate.
#
# Usage (from repo root):
#   ./scripts/e2e_trade_flow.sh
#
# Wave 2 (live chain + gazk): point the backend at a real chain
#   (RELAYER_MODE=cosmos) and the gazk prover, then the same flow commits the
#   root on-chain via MsgSubmitBatchProof (INT-T08 + ONCHAIN-T04).
set -euo pipefail

cd "$(dirname "$0")/.."

echo "[e2e_trade_flow] go run ./p3/script-test/e2e_trade"
go run ./p3/script-test/e2e_trade

echo "[e2e_trade_flow] OK"
