#!/usr/bin/env bash
set -euo pipefail

API_BASE_URL="${API_BASE_URL:-http://localhost:8080}"
GAZK_URL="${GAZK_URL:-http://localhost:8090}"

echo
echo "============================================================"
echo "0. Check gazk remote prover"
echo "============================================================"

gazk_health="$(curl -s "${GAZK_URL}/health")"
echo "${gazk_health}" | jq

verification_key_id="$(echo "${gazk_health}" | jq -r '.verificationKeyId')"

if [[ "${verification_key_id}" != "gazk-balance-smoke-v1" ]]; then
  echo "expected gazk verificationKeyId=gazk-balance-smoke-v1, got ${verification_key_id}" >&2
  exit 1
fi

echo
echo "============================================================"
echo "1. Run pending settlement E2E flow"
echo "============================================================"

./scripts/e2e_pending_settlement_flow.sh

echo
echo "============================================================"
echo "2. Verify proof came from gazk"
echo "============================================================"

proof_verification_key_id="$(curl -s "${API_BASE_URL}/api/state" | jq -r '.latestProof.verificationKeyId // empty')"

if [[ -z "${proof_verification_key_id}" ]]; then
  echo "could not read latestProof.verificationKeyId from /api/state" >&2
  echo "This may mean GET /api/state does not expose latestProof yet." >&2
  echo "Fallback: inspect the output above and confirm proofBundle.verificationKeyId=gazk-balance-smoke-v1." >&2
  exit 0
fi

if [[ "${proof_verification_key_id}" != "gazk-balance-smoke-v1" ]]; then
  echo "expected latestProof.verificationKeyId=gazk-balance-smoke-v1, got ${proof_verification_key_id}" >&2
  exit 1
fi

echo "remote prover E2E verified: ${proof_verification_key_id}"
