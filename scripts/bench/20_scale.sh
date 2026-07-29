#!/usr/bin/env bash
# =============================================================================
# 20_scale.sh — Đo theo QUY MÔ cho một mốc N và một kịch bản.
# -----------------------------------------------------------------------------
#   bash scripts/bench/20_scale.sh <N> <A|B> [rep]
#
# Kịch bản:
#   A  fill ĐỘC LẬP   — N cặp order riêng biệt → N fill đều settle → N tx.
#                       Best case. Ô TỐN THỜI GIAN NHẤT.
#   B  SHARED-ORDER   — N maker + 1 taker quét hết → N fill dùng chung 1 order.
#                       Chỉ fill ĐẦU settle, N−1 fill bị guard DROP trước khi
#                       prove/submit ⇒ nhanh & rẻ. Đây là ô đo DROP RATE.
#
# Ô tự-kiểm-tra (in cảnh báo nếu sai — nghĩa là dựng sai kịch bản, KHÔNG phải
# phát hiện mới):   A → drop=0, tx=N   ·   B → tx=1, drop≈(N−1)/N
# =============================================================================

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
bench_init
require_tools curl jq

N="${1:-}"; SC="${2:-}"; REP="${3:-1}"
[ -z "$N" ] || [ -z "$SC" ] && die "dùng: 20_scale.sh <N> <A|B> [rep]"
case "$SC" in A|B) ;; *) die "kịch bản phải là A hoặc B" ;; esac

PRICE="${BENCH_PRICE:-100}"
QTY="${BENCH_QTY:-1}"
NONCE_BASE="${BENCH_NONCE_BASE:-$(date +%s)}"
TICK="$(interval_secs)"

head1 "SCALE — N=$N kịch bản=$SC rep=$REP (run_id=$RUN_ID)"

# --- Mốc 0 -------------------------------------------------------------------
BYTES0="$(data_bytes)"
LOG0="$(log_lines)"
read -r S0 D0 <<< "$(settle_counts)"
metric "$SC" "$N" "$REP" data_bytes_before "$BYTES0" B ""

# --- Bơm lệnh ----------------------------------------------------------------
head1 "1. Bơm lệnh"
T0="$(now_ms)"
placed=0; rejected=0

if [ "$SC" = "A" ]; then
  # N cặp độc lập: maker_i rồi taker_i khớp ngay với nhau.
  for i in $(seq 1 "$N"); do
    n=$((NONCE_BASE + i))
    r1="$(post_order "$BENCH_MAKER_KEY" sell "$PRICE" "$QTY" "$n")"
    r2="$(post_order "$BENCH_TAKER_KEY" buy  "$PRICE" "$QTY" "$n")"
    if echo "$r2" | jq -e '.status' >/dev/null 2>&1; then placed=$((placed+1)); else rejected=$((rejected+1)); fi
    printf '\r    đặt %d/%d (lỗi %d)' "$i" "$N" "$rejected"
  done
else
  # B: N maker nằm sổ, rồi 1 taker qty=N quét hết.
  for i in $(seq 1 "$N"); do
    n=$((NONCE_BASE + i))
    r1="$(post_order "$BENCH_MAKER_KEY" sell "$PRICE" "$QTY" "$n")"
    if echo "$r1" | jq -e '.status' >/dev/null 2>&1; then placed=$((placed+1)); else rejected=$((rejected+1)); fi
    printf '\r    maker %d/%d (lỗi %d)' "$i" "$N" "$rejected"
  done
  echo
  TQ=$((QTY * N))
  log "taker quét: qty=$TQ"
  r2="$(post_order "$BENCH_TAKER_KEY" buy "$PRICE" "$TQ" "$((NONCE_BASE + N + 1))")"
  echo "$r2" | jq -c '{status,reason}' 2>/dev/null || echo "  taker resp: $r2"
fi
echo
T_PLACED="$(now_ms)"
metric "$SC" "$N" "$REP" orders_placed  "$placed"   count ""
metric "$SC" "$N" "$REP" orders_rejected "$rejected" count "$([ "$rejected" -gt 0 ] && echo 'KIỂM TRA số dư/nonce' || echo '')"
metric "$SC" "$N" "$REP" place_ms "$((T_PLACED - T0))" ms "chỉ thời gian bơm lệnh"

# --- Chờ drain ---------------------------------------------------------------
head1 "2. Chờ settlement drain"
# A: tối đa N fill × (tick + prove + commit). B: chỉ 1 fill settle.
EXPECT=$([ "$SC" = "A" ] && echo "$N" || echo 1)
BUDGET="${BENCH_WAIT_MAX:-$(( (TICK + 25) * EXPECT + 60 ))}"
log "kỳ vọng $EXPECT tx settle · ngân sách chờ ${BUDGET}s"

waited=0; stable=0; last=-1
while [ "$waited" -lt "$BUDGET" ]; do
  sleep 5; waited=$((waited+5))
  cnt="$(settle_txhashes_since "$LOG0" | wc -l)"
  printf '\r    settled %s/%s  (%ds/%ds)' "$cnt" "$EXPECT" "$waited" "$BUDGET"
  [ "$cnt" -ge "$EXPECT" ] && break
  if [ "$cnt" -eq "$last" ]; then stable=$((stable+5)); else stable=0; last="$cnt"; fi
  # đứng yên lâu hơn 4 chu kỳ tick ⇒ coi như đã drain xong (phần còn lại bị drop)
  [ "$stable" -ge $(( (TICK + 20) * 4 )) ] && { echo; warn "không tiến triển ${stable}s — coi như đã drain"; break; }
done
echo
T1="$(now_ms)"

# --- Thu số ------------------------------------------------------------------
head1 "3. Thu kết quả"
mapfile -t HASHES < <(settle_txhashes_since "$LOG0")
NTX="${#HASHES[@]}"
read -r S1 D1 <<< "$(settle_counts)"
SET=$(( S1 - S0 )); DROP=$(( D1 - D0 ))

metric "$SC" "$N" "$REP" wall_clock_ms "$((T1 - T0))" ms "bơm lệnh → drain xong"
metric "$SC" "$N" "$REP" tx_settled     "$NTX"  count "txhash duy nhất trong log"
metric "$SC" "$N" "$REP" settled_delta  "$SET"  count "SETTLED tăng thêm"
metric "$SC" "$N" "$REP" dropped_delta  "$DROP" count "DROP tăng thêm"

TOT=$(( SET + DROP ))
if [ "$TOT" -gt 0 ]; then
  metric "$SC" "$N" "$REP" drop_rate_pct "$(awk -v d="$DROP" -v t="$TOT" 'BEGIN{printf "%.2f", d*100/t}')" % "DROP/(SETTLED+DROP)"
else
  metric "$SC" "$N" "$REP" drop_rate_pct NA % "không có fill nào"
fi

# Gas: cộng dồn từng tx (chính là chi phí operator thật)
head1 "4. Gas từng tx"
GSUM=0; GN=0
: > "$OUT_DIR/gas_${SC}_${N}_${REP}.txt"
for h in "${HASHES[@]}"; do
  g="$(tx_field "$h" '.gas_used')"
  echo "$h $g" >> "$OUT_DIR/gas_${SC}_${N}_${REP}.txt"
  if [ "$g" != "NA" ] && [ -n "$g" ]; then GSUM=$((GSUM + g)); GN=$((GN + 1)); fi
  printf '\r    query gas %d/%d' "$GN" "$NTX"
done
echo
if [ "$GN" -gt 0 ]; then
  metric "$SC" "$N" "$REP" gas_total   "$GSUM"          gas "cộng $GN tx"
  metric "$SC" "$N" "$REP" gas_per_tx  "$((GSUM / GN))" gas "trung bình"
else
  metric "$SC" "$N" "$REP" gas_total NA gas "không query được tx nào"
fi

# Storage
BYTES1="$(data_bytes)"
metric "$SC" "$N" "$REP" data_bytes_after "$BYTES1" B ""
if [ "$BYTES0" != "NA" ] && [ "$BYTES1" != "NA" ]; then
  metric "$SC" "$N" "$REP" state_growth_total "$((BYTES1 - BYTES0))" B "delta du -sb"
fi

# RSS cuối
metric "$SC" "$N" "$REP" rss_ganc_sys "$(rss_kb ganc-sys)" KB "cuối run"
metric "$SC" "$N" "$REP" rss_gazk     "$(rss_kb gazk)"     KB "cuối run"

# --- 5. Tự kiểm tra ----------------------------------------------------------
head1 "5. Tự kiểm tra (ô đã biết trước)"
bad=0
if [ "$SC" = "A" ]; then
  [ "$DROP" -ne 0 ] && { warn "A: drop=$DROP nhưng ĐÁNG LẼ 0 → có order dùng chung ngoài ý muốn"; bad=1; }
  [ "$NTX" -ne "$N" ] && { warn "A: tx=$NTX nhưng ĐÁNG LẼ $N → có fill chưa settle hoặc bị drop"; bad=1; }
else
  [ "$NTX" -ne 1 ] && { warn "B: tx=$NTX nhưng ĐÁNG LẼ 1 → taker không quét đủ N maker"; bad=1; }
  exp=$(awk -v n="$N" 'BEGIN{printf "%.1f", (n-1)*100/n}')
  [ "$DROP" -ne $((N - 1)) ] && { warn "B: drop=$DROP nhưng ĐÁNG LẼ $((N-1)) (~${exp}%)"; bad=1; }
fi
if [ "$bad" -eq 0 ]; then ok "khớp kỳ vọng — số liệu đáng tin"
else
  warn "LỆCH kỳ vọng ⇒ nhiều khả năng DỰNG SAI KỊCH BẢN, không phải phát hiện mới."
  metric "$SC" "$N" "$REP" selfcheck FAILED - "xem cảnh báo ở trên"
fi

head1 "XONG N=$N $SC rep=$REP"
echo "  CSV: $METRICS_CSV"
echo "  gas từng tx: $OUT_DIR/gas_${SC}_${N}_${REP}.txt"
