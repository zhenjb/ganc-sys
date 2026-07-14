#!/usr/bin/env bash
# TRD-V1.0 — LOCAL proxy for the live trade settle path.
#
# Runs the same order→match→settle as e2e_trade_real, but wires the REAL gazk proof
# into the CHAIN-SUBMIT path (RemoteTradeProver + RelayerTradeSubmitter over a local
# relayer client) instead of the gazk verify loop. The local relayer client enforces
# the SAME 8-public-input + root binding the chain checks, so a settle that completes
# proves the real v0 proof reconciles into MsgSubmitBatchProof — the exact reconcile
# that FAILED under the old v1 proof. This is the furthest TRD-V1 can be exercised
# without the Cosmos chain (which needs Codespace: local build kẹt on bytedance/sonic).
#
# For the FULL live run against a real chain, see docs/matching_orderbook/INT-TRD-V1-
# live-settle.md (§ Runbook Lớp A) with TRADE_PROVER_MODE=remote TRADE_SUBMIT_MODE=
# chain RELAYER_MODE=cosmos.
#
# Usage (from ganc-sys repo root):
#   ./scripts/e2e_trade_chain_local.sh
set -euo pipefail
export TRADE_SUBMIT_MODE=chain
exec "$(dirname "$0")/e2e_trade_real_proof.sh"
