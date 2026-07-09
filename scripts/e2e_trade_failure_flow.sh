#!/usr/bin/env bash
# INT-T10 — one-command trade FAILURE demo (safety evidence, opposite of INT-T09).
#
# Drives each negative case and collects the rejection evidence, checking the
# state has NO side effect after each reject:
#   C1 over-reserve  → 400 insufficient_balance ; nothing reserved
#   C2 non-crossing  → 0 fills (no garbage trade) ; both rest
#   C3 order replay  → 400 order_nullifier_used  ; nothing re-locked
#   C4 prove-fail    → rollback (STATE-T10) + re-enqueue ; sequencer recovers
#
# Self-contained and idempotent (fresh funded state per case over an in-process
# API server, no chain/DB). Exits non-zero on any assertion failure — a CI safety
# gate for the trading reject paths.
#
# Usage (from repo root):
#   ./scripts/e2e_trade_failure_flow.sh
set -euo pipefail

cd "$(dirname "$0")/.."

echo "[e2e_trade_failure_flow] go run ./p3/script-test/e2e_trade_failure"
go run ./p3/script-test/e2e_trade_failure

echo "[e2e_trade_failure_flow] OK"
