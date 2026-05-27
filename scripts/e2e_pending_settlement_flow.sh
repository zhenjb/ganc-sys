#!/usr/bin/env bash
set -euo pipefail

API_BASE_URL="${API_BASE_URL:-http://localhost:8080}"
DB_CONTAINER="${DB_CONTAINER:-ganc_sys_postgres}"
DB_USER="${DB_USER:-ganc}"
DB_NAME="${DB_NAME:-ganc_sys}"

OWNER="${OWNER:-cosmos1alice}"
DENOM="${DENOM:-uusdc}"
DEPOSIT_AMOUNT="${DEPOSIT_AMOUNT:-100}"
WITHDRAW_AMOUNT="${WITHDRAW_AMOUNT:-40}"
DESTINATION="${DESTINATION:-cosmos1alice}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

post_json() {
  local path="$1"
  local body="$2"

  curl -s -X POST "${API_BASE_URL}${path}" \
    -H "Content-Type: application/json" \
    -d "${body}"
}

echo_section() {
  echo
  echo "============================================================"
  echo "$1"
  echo "============================================================"
}

require_cmd curl
require_cmd jq
require_cmd docker

echo_section "0. Reset pending settlement workflow data"

docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" <<'SQL'
DELETE FROM offchain_pending_withdrawals;
DELETE FROM offchain_pending_deposits;
DELETE FROM offchain_state_cursors;

DELETE FROM indexed_withdraw_records;
DELETE FROM submit_batches;
DELETE FROM proof_bundles;
DELETE FROM batch_builds;
DELETE FROM withdraw_requests;

ALTER SEQUENCE withdraw_request_seq RESTART WITH 1;
SQL

echo_section "1. Create dev deposit substitute"

deposit_response="$(post_json "/api/deposit" "{
  \"owner\": \"${OWNER}\",
  \"denom\": \"${DENOM}\",
  \"amount\": \"${DEPOSIT_AMOUNT}\"
}")"

echo "${deposit_response}" | jq

deposit_id="$(echo "${deposit_response}" | jq -r '.depositRecord.depositId')"

if [[ "${deposit_id}" == "null" || -z "${deposit_id}" ]]; then
  echo "failed to read depositId from deposit response" >&2
  exit 1
fi

echo "deposit_id=${deposit_id}"

echo_section "2. Verify pending deposit"

docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" <<SQL
SELECT deposit_id, owner_address, denom, amount, balance_before, balance_after, status
FROM offchain_pending_deposits
ORDER BY created_at;
SQL

echo_section "3. Create withdraw request"

withdraw_response="$(post_json "/api/withdraw-request" "{
  \"owner\": \"${OWNER}\",
  \"denom\": \"${DENOM}\",
  \"amount\": \"${WITHDRAW_AMOUNT}\",
  \"destination\": \"${DESTINATION}\"
}")"

echo "${withdraw_response}" | jq

withdraw_id="$(echo "${withdraw_response}" | jq -r '.withdrawRequest.withdrawId')"

if [[ "${withdraw_id}" == "null" || -z "${withdraw_id}" ]]; then
  echo "failed to read withdrawId from withdraw response" >&2
  exit 1
fi

echo "withdraw_id=${withdraw_id}"

echo_section "4. Verify pending withdrawal"

docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" <<SQL
SELECT withdraw_id, owner_address, denom, amount, balance_before, balance_after, status
FROM offchain_pending_withdrawals
ORDER BY created_at;
SQL

echo_section "5. Build pending settlement batch"

build_response="$(post_json "/api/batch/build" "{}")"

echo "${build_response}" | jq

batch_id="$(echo "${build_response}" | jq -r '.settlementUpdate.batchId')"

if [[ "${batch_id}" == "null" || -z "${batch_id}" ]]; then
  echo "failed to read batchId from build response" >&2
  exit 1
fi

echo "batch_id=${batch_id}"

echo_section "6. Verify operations included"

docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" <<SQL
SELECT deposit_id, status, batch_id
FROM offchain_pending_deposits
ORDER BY created_at;

SELECT withdraw_id, status, batch_id
FROM offchain_pending_withdrawals
ORDER BY created_at;
SQL

echo_section "7. Generate proof"

proof_request="$(echo "${build_response}" | jq '{
  settlementUpdate: .settlementUpdate,
  batchCommitments: .batchCommitments,
  witness: .witness
}')"

proof_response="$(post_json "/api/proof/generate" "${proof_request}")"

echo "${proof_response}" | jq

proof="$(echo "${proof_response}" | jq -r '.proofBundle.proof')"

if [[ "${proof}" == "null" || -z "${proof}" ]]; then
  echo "failed to read proof from proof response" >&2
  exit 1
fi

public_input_count="$(echo "${proof_response}" | jq '.proofBundle.publicInputs | length')"

if [[ "${public_input_count}" != "6" ]]; then
  echo "expected 6 proof public inputs, got ${public_input_count}" >&2
  exit 1
fi

echo_section "8. Submit batch"

submit_request="$(jq -n \
  --argjson build "${build_response}" \
  --argjson proof "${proof_response}" \
  '{
    settlementUpdate: $build.settlementUpdate,
    batchCommitments: $build.batchCommitments,
    proofBundle: $proof.proofBundle
  }'
)"

submit_response="$(post_json "/api/batch/submit" "${submit_request}")"

echo "${submit_response}" | jq

accepted="$(echo "${submit_response}" | jq -r '.accepted')"

if [[ "${accepted}" != "true" ]]; then
  echo "batch submit was not accepted" >&2
  exit 1
fi

new_state_root="$(echo "${build_response}" | jq -r '.settlementUpdate.newStateRoot')"

echo_section "9. Verify committed offchain settlement"

docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" <<SQL
SELECT deposit_id, status, batch_id, tx_hash
FROM offchain_pending_deposits
ORDER BY created_at;

SELECT withdraw_id, status, batch_id, tx_hash
FROM offchain_pending_withdrawals
ORDER BY created_at;

SELECT name, committed_root, pending_root, last_committed_batch_id
FROM offchain_state_cursors
ORDER BY name;
SQL

deposit_status="$(docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -t -A -c "SELECT status FROM offchain_pending_deposits WHERE deposit_id = '${deposit_id}';")"
withdraw_status="$(docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -t -A -c "SELECT status FROM offchain_pending_withdrawals WHERE withdraw_id = '${withdraw_id}';")"
committed_root="$(docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -t -A -c "SELECT committed_root FROM offchain_state_cursors WHERE name = 'default';")"
pending_root="$(docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -t -A -c "SELECT pending_root FROM offchain_state_cursors WHERE name = 'default';")"
last_batch="$(docker exec -i "${DB_CONTAINER}" psql -U "${DB_USER}" -d "${DB_NAME}" -t -A -c "SELECT last_committed_batch_id FROM offchain_state_cursors WHERE name = 'default';")"

if [[ "${deposit_status}" != "committed" ]]; then
  echo "expected deposit status=committed, got ${deposit_status}" >&2
  exit 1
fi

if [[ "${withdraw_status}" != "committed" ]]; then
  echo "expected withdrawal status=committed, got ${withdraw_status}" >&2
  exit 1
fi

if [[ "${committed_root}" != "${new_state_root}" ]]; then
  echo "expected committed_root=${new_state_root}, got ${committed_root}" >&2
  exit 1
fi

if [[ "${pending_root}" != "${new_state_root}" ]]; then
  echo "expected pending_root=${new_state_root}, got ${pending_root}" >&2
  exit 1
fi

if [[ "${last_batch}" != "${batch_id}" ]]; then
  echo "expected last_committed_batch_id=${batch_id}, got ${last_batch}" >&2
  exit 1
fi

echo_section "Pending settlement E2E flow completed successfully"