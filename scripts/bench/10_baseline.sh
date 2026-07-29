#!/usr/bin/env bash
# =============================================================================
# 10_baseline.sh — Đo BASELINE: đúng 1 trade, thu mọi chỉ số "1 lần lấy được".
# -----------------------------------------------------------------------------
# Thu:  gas SubmitBatchProof · proof size · pk/vk size · state growth/trade
#       · time-to-finality · RSS · CHU KỲ/FILL (để dự toán ngân sách N lớn)
#
#   bash scripts/bench/10_baseline.sh
#
# Yêu cầu: 00_env.sh đã xanh, và 2 tài khoản đã có số dư off-chain (deposit).
# =============================================================================

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
bench_init
require_tools curl jq

SC=baseline; N=1; REP=1
PRICE="${BENCH_PRICE:-100}"
QTY="${BENCH_QTY:-20}"
NONCE_BASE="${BENCH_NONCE_BASE:-$(date +%s)}"   # tránh đụng nonce giữa các lần chạy

head1 "BASELINE — 1 trade (run_id=$RUN_ID)"
log "market=$MARKET price=$PRICE qty=$QTY sig=$ORDER_SIG_MODE"

# --- 1. Mốc 0 ----------------------------------------------------------------
head1 "1. Mốc 0 — trước khi giao dịch"
BYTES0="$(data_bytes)"
LOG0="$(log_lines)"
ROOT0="$(curl -s "$CHAIN_REST_URL/ob/zkdex/v1/current_state_root" | jq -r '.stateRoot // .state_root // "NA"' 2>/dev/null)"
metric $SC $N $REP data_bytes_before "$BYTES0" B "du -sb \$CHAIN_HOME/data"
log "state root trước: ${ROOT0:0:18}…"

# --- 2. Đặt 2 lệnh khớp nhau -------------------------------------------------
head1 "2. Đặt cặp lệnh khớp"
T0="$(now_ms)"
R1="$(post_order "$BENCH_MAKER_KEY" sell "$PRICE" "$QTY" "$((NONCE_BASE))")"
echo "$R1" | jq -c '{status,reason}' 2>/dev/null || echo "  maker resp: $R1"
R2="$(post_order "$BENCH_TAKER_KEY" buy  "$PRICE" "$QTY" "$((NONCE_BASE))")"
echo "$R2" | jq -c '{status,reason}' 2>/dev/null || echo "  taker resp: $R2"

if ! echo "$R2" | jq -e '.status' >/dev/null 2>&1; then
  err "Đặt lệnh THẤT BẠI. Kiểm tra: số dư off-chain (deposit chưa?), ORDER_SIG_MODE, market."
  metric $SC $N $REP order_placement FAILED - "$(echo "$R2" | head -c 120)"
  exit 1
fi
ok "đã đặt 2 lệnh, t0 ghi nhận"

# --- 3. Chờ settle -----------------------------------------------------------
head1 "3. Chờ settlement (tick=$SETTLEMENT_INTERVAL)"
WAIT_MAX="${BENCH_WAIT_MAX:-120}"
TXH=""; waited=0
while [ "$waited" -lt "$WAIT_MAX" ]; do
  sleep 3; waited=$((waited+3))
  TXH="$(settle_txhashes_since "$LOG0" | head -1)"
  [ -n "$TXH" ] && break
  printf '\r    …chờ %ds/%ds' "$waited" "$WAIT_MAX"
done
echo
if [ -z "$TXH" ]; then
  err "Không thấy SETTLED sau ${WAIT_MAX}s. Xem log: tail -30 $BENCH_SERVER_LOG"
  metric $SC $N $REP settle_timeout "$WAIT_MAX" s "không có dòng SETTLED"
  exit 1
fi
T1="$(now_ms)"
ok "SETTLED tx=0x${TXH:0:16}… sau ${waited}s"
metric $SC $N $REP cycle_per_fill "$waited" s "chờ từ lúc đặt lệnh tới SETTLED — DÙNG ĐỂ DỰ TOÁN N lớn"

# --- 4. Gas ------------------------------------------------------------------
head1 "4. Gas — SubmitBatchProof"
GAS="$(tx_field "$TXH" '.gas_used')"
GASW="$(tx_field "$TXH" '.gas_wanted')"
HEIGHT="$(tx_field "$TXH" '.height')"
CODE="$(tx_field "$TXH" '.code')"
metric $SC $N $REP gas_submit_batch_proof "$GAS"  gas "tx=0x${TXH:0:12} code=$CODE"
metric $SC $N $REP gas_wanted             "$GASW" gas ""
tx_json "$TXH" > "$OUT_DIR/tx_baseline.json" 2>/dev/null
log "JSON đầy đủ: $OUT_DIR/tx_baseline.json"

# --- 5. Latency --------------------------------------------------------------
head1 "5. Time-to-finality"
E2E_MS=$((T1 - T0))
metric $SC $N $REP finality_wall_ms "$E2E_MS" ms "t(đặt lệnh) → t(thấy SETTLED)"
if [ "$HEIGHT" != "NA" ]; then
  BT="$(block_time_ms "$HEIGHT")"
  if [ "$BT" != "NA" ]; then
    metric $SC $N $REP finality_block_ms "$((BT - T0))" ms "t(đặt lệnh) → timestamp block $HEIGHT"
  else
    metric $SC $N $REP finality_block_ms NA ms "không đọc được timestamp block $HEIGHT"
  fi
fi

# --- 6. Storage --------------------------------------------------------------
head1 "6. Storage growth"
sleep 2
BYTES1="$(data_bytes)"
metric $SC $N $REP data_bytes_after "$BYTES1" B ""
if [ "$BYTES0" != "NA" ] && [ "$BYTES1" != "NA" ]; then
  metric $SC $N $REP state_growth_per_trade "$((BYTES1 - BYTES0))" B "delta du -sb cho 1 trade"
else
  metric $SC $N $REP state_growth_per_trade NA B "không xác định được CHAIN_HOME/data"
fi

# --- 7. Proof size / pk-vk ---------------------------------------------------
head1 "7. Proof & khoá"
PSZ="$(psql_one "SELECT length(proof_bundle::text) FROM proof_bundles ORDER BY created_at DESC LIMIT 1;")"
[ -z "$PSZ" ] && PSZ=NA
metric $SC $N $REP proof_size "$PSZ" B "length(proof_bundle) bản ghi mới nhất"

KEYDIR="${GAZK_KEY_DIR:-$GANC_SYS_DIR/.gazk-keys}"
if [ -d "$KEYDIR" ]; then
  ls -la "$KEYDIR" > "$OUT_DIR/gazk_keys.txt" 2>/dev/null
  PK_B="$(find "$KEYDIR" -type f \( -name '*pk*' -o -name '*proving*' \) -printf '%s\n' 2>/dev/null | sort -rn | head -1)"
  VK_B="$(find "$KEYDIR" -type f \( -name '*vk*' -o -name '*verif*' \) -printf '%s\n' 2>/dev/null | sort -rn | head -1)"
  metric $SC $N $REP proving_key_size  "${PK_B:-NA}" B "$KEYDIR"
  metric $SC $N $REP verifying_key_size "${VK_B:-NA}" B "$KEYDIR"
else
  metric $SC $N $REP proving_key_size  NA B "không thấy GAZK_KEY_DIR=$KEYDIR"
  metric $SC $N $REP verifying_key_size NA B "không thấy GAZK_KEY_DIR=$KEYDIR"
fi

# --- 8. RSS ------------------------------------------------------------------
head1 "8. Bộ nhớ tiến trình"
metric $SC $N $REP rss_ganc_sys "$(rss_kb ganc-sys)" KB "ps rss"
metric $SC $N $REP rss_gazk     "$(rss_kb gazk)"     KB "ps rss"

# --- 9. Drop rate (phải = 0 ở baseline) --------------------------------------
head1 "9. Kiểm tra tính đúng"
read -r S D <<< "$(settle_counts)"
metric $SC $N $REP settled_count "$S" count "tích lũy trong log"
metric $SC $N $REP dropped_count "$D" count "tích lũy trong log"
if [ "$D" != "NA" ] && [ "${D:-0}" -gt 0 ] 2>/dev/null; then
  warn "có $D dòng DROP trong log — baseline đáng lẽ 0. Log có thể còn dữ liệu lần chạy trước."
fi

# --- 10. Chỉ số KHÔNG lấy được (ghi rõ, không bịa) ---------------------------
metric $SC $N $REP constraints_r1cs NA count "cần role A (gazk không expose)"
metric $SC $N $REP prove_time_pure  NA ms    "cần role A hoặc timer quanh ProveTrade"
metric $SC $N $REP gas_verify_split NA gas   "cần gas checkpoint trong keeper (role B)"

head1 "XONG BASELINE"
CYC="$waited"
echo "  Chu kỳ/fill đo được: ${CYC}s"
echo "  ⇒ Dự toán ngân sách kịch bản A:"
echo "       N=10   ≈ $((CYC * 10))s"
echo "       N=100  ≈ $((CYC * 100))s  (~$((CYC * 100 / 60)) phút)"
echo "       N=1000 ≈ $((CYC * 1000))s (~$((CYC * 1000 / 3600)) giờ)"
echo
echo "  Kịch bản B rẻ hơn NHIỀU (chỉ 1 fill settle, phần còn lại drop trước prove)."
ok "Bước kế: bash scripts/bench/20_scale.sh 10 B"
