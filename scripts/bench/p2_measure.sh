#!/usr/bin/env bash
# =============================================================================
# p2_measure.sh — Đo Phase 2 theo ĐÚNG cấu trúc của Phase 1 để so 1:1.
# -----------------------------------------------------------------------------
# Bản sao cấu trúc của ganc-trade/test/bench/p1_measure.sh:
#
#   SEED  n lệnh SELL giá phân biệt, không có BUY → sổ sâu depth=n
#   A1b   đặt THÊM 1 lệnh SELL không khớp ở depth ⇒ gas + latency
#   A1a   đặt 1 lệnh BUY khớp ⇒ gas/trade
#   A3    K lệnh BUY khớp ⇒ throughput (trade/s)
#   A4    RAM đỉnh
#   A5    drop rate (riêng P2 — hệ quả per-order nullifier)
#
# CSV giữ NGUYÊN 10 cột đầu của P1 (ghép bảng là dùng được ngay), các cột riêng
# của P2 nối vào sau.
#
# KHÁC BIỆT KIẾN TRÚC ảnh hưởng cách đo (không phải lỗi script):
#   • Đặt lệnh ở P2 là HTTP off-chain ⇒ KHÔNG có tx, KHÔNG tốn gas.
#     ⇒ A1b_gas_nomatch = 0 THEO THIẾT KẾ. Thay vào đó đo LATENCY (ms).
#   • Chỉ khi khớp mới sinh 1 batch = 1 proof = 1 tx on-chain.
#     ⇒ gas/trade = gas(MsgSubmitBatchProof) của 1 fill, KHÔNG cộng 2 lệnh như P1.
#   • Seed không bị chặn 1 lệnh/block ⇒ nhanh hơn P1 nhiều bậc. Ghi A0_seed_seconds
#     để so trực tiếp với ~80 phút seed 1000 lệnh của P1.
#
#   bash scripts/bench/p2_measure.sh                 # 3 mốc 10/100/1000
#   SCALES="10" bash scripts/bench/p2_measure.sh
#   THROUGHPUT_K=50 bash scripts/bench/p2_measure.sh # giảm mẫu throughput
# =============================================================================

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
bench_init
require_tools curl jq go
bench_build_signers      # dựng signer 1 lần — KHÔNG `go run` mỗi lệnh (xem lib.sh)
bench_resolve_keys       # nạp+export địa chỉ/khoá 1 lần — KHÔNG `obd keys` mỗi lệnh

SCALES="${SCALES:-10 100 1000}"
QTY="${BENCH_QTY:-1}"
BASE_PRICE="${BASE_PRICE:-100}"        # ladder SELL: BASE_PRICE .. BASE_PRICE+n-1
THROUGHPUT_K="${THROUGHPUT_K:-}"       # mặc định = depth
NONCE_BASE="${BENCH_NONCE_BASE:-$(date +%s)}"
TICK="$(interval_secs)"

CSV="$OUT_DIR/p2_${RUN_ID}.csv"
echo "scale_n,depth,A1b_gas_nomatch,A1a_gas_buy,A1a_gas_per_trade,A3_trades_committed,A3_seconds,A3_trades_per_s,A4_ram_kb,A4_ram_mb,A0_seed_seconds,A1b_latency_ms,A5_drop_pct,A6_settle_tx,A7_gap_median_s,A7_gap_max_s,A8_drained_total" > "$CSV"

echo "P2 measurement — market=$MARKET seller=$BENCH_MAKER_KEY buyer=$BENCH_TAKER_KEY sig=$ORDER_SIG_MODE"
echo "Hệ: $SCALES | tick=$SETTLEMENT_INTERVAL | api=$API_BASE_URL"

# --- RAM sampler: lấy đỉnh của CẢ BA tiến trình (P1 chỉ có mỗi obd) ----------
RAM_PID=""
start_ram(){
  local f="$1"; echo 0 > "$f"
  ( peak=0
    while :; do
      local tot=0 v
      for p in ganc-sys gazk obd; do
        v="$(rss_kb "$p")"; [ "$v" != "NA" ] && tot=$((tot + v))
      done
      [ "$tot" -gt "$peak" ] && { peak=$tot; echo "$peak" > "$f"; }
      sleep 0.5
    done ) &
  RAM_PID=$!
}
stop_ram(){ [ -n "$RAM_PID" ] && kill "$RAM_PID" 2>/dev/null; RAM_PID=""; }
# QUAN TRỌNG: handler cho INT/TERM PHẢI tự `exit`. Nếu chỉ dọn dẹp rồi trả về,
# bash chạy tiếp lệnh kế — Ctrl-C sẽ KHÔNG dừng được script (đã vấp).
trap 'stop_ram' EXIT
trap 'stop_ram; echo; err "đã hủy bởi người dùng — số liệu run này KHÔNG dùng được"; exit 130' INT TERM

# --- Chờ đúng N tx settle mới xuất hiện trong log; in số đếm được -----------
wait_settled(){
  local from_line="$1" want="$2" budget="$3" waited=0 cnt=0 last=-1 stable=0
  while [ "$waited" -lt "$budget" ]; do
    cnt="$(settle_txhashes_since "$from_line" | wc -l)"
    [ "$cnt" -ge "$want" ] && break
    if [ "$cnt" -eq "$last" ]; then stable=$((stable+2)); else stable=0; last="$cnt"; fi
    # đứng yên quá 4 chu kỳ tick ⇒ coi như drain xong (phần dư đã bị drop)
    [ "$stable" -ge $(( (TICK + 20) * 4 )) ] && break
    sleep 2; waited=$((waited+2))
  done
  echo "$cnt"
}

# --- Rào chắn drain: chờ hàng đợi fill CẠN HẲN trước khi sang mốc sau -------
# Không có rào này, fill của mốc n settle lấn sang cửa sổ của mốc n+1 và phân
# tách theo mốc trở nên vô nghĩa (đã vấp: mốc 100 đếm 62/101, mốc 1000 đếm 1040
# > 1001 vì hứng phần thừa). Điều kiện dừng: KHÔNG có SETTLED mới trong 30s.
drain_barrier(){
  local from="$1" quiet=0 last=-1 cnt waited=0 max="${2:-600}"
  while [ "$quiet" -lt 30 ] && [ "$waited" -lt "$max" ]; do
    cnt="$(settle_txhashes_since "$from" | wc -l)"
    if [ "$cnt" -eq "$last" ]; then quiet=$((quiet+5)); else quiet=0; last="$cnt"; fi
    sleep 5; waited=$((waited+5))
  done
  echo "$last"
}

# --- Thống kê KHOẢNG CÁCH giữa các lần SETTLED trong cửa sổ của mốc này -----
# Đây là chỉ số phân biệt NĂNG LỰC với THỜI GIAN CHẾT:
#   • gap trung vị  ≈ chi phí thật để chốt 1 fill (năng lực)
#   • gap tối đa    = khoảng đứng im dài nhất (chờ tick / prover khởi động)
# Throughput thô = số fill ÷ tổng span, nên bị thời gian chết làm loãng; gap
# trung vị thì KHÔNG. Nếu trung vị gần như nhau ở cả 3 mốc ⇒ năng lực PHẲNG,
# và mọi biến thiên của throughput chỉ là overhead — đúng như lý thuyết đòi hỏi.
# In "<median> <max> <count>"; "NA NA 0" nếu chưa đủ mẫu.
settle_gap_stats(){
  local from="$1" f="$OUT_DIR/.gaps.$$"
  tail -n +"$((from + 1))" "$BENCH_SERVER_LOG" 2>/dev/null \
    | grep '\[trade-settlement\] SETTLED' \
    | awk '{split($2,t,":"); s=t[1]*3600+t[2]*60+t[3];
            if(n++){g=s-p; if(g<0)g+=86400; print g} p=s}' \
    | sort -n > "$f"
  local c; c=$(wc -l < "$f")
  if [ "${c:-0}" -lt 2 ]; then rm -f "$f"; echo "NA NA ${c:-0}"; return; fi
  local med mx
  med=$(awk -v c="$c" 'NR==int((c+1)/2){print $1}' "$f")
  mx=$(tail -1 "$f")
  rm -f "$f"
  echo "$med $mx $c"
}

# --- Đặt 1 lệnh; in "<ok> <latency_ms>" ------------------------------------
post_timed(){
  local key="$1" side="$2" price="$3" qty="$4" nonce="$5" t0 t1 resp
  t0="$(now_ms)"
  resp="$(post_order "$key" "$side" "$price" "$qty" "$nonce")"
  t1="$(now_ms)"
  if echo "$resp" | jq -e '.status' >/dev/null 2>&1; then echo "1 $((t1-t0))"; else echo "0 $((t1-t0))"; fi
}

# ============================== MỘT HỆ (scale n) ============================
run_scale(){
  local n="$1"
  echo -e "\n${C_B}==================== HỆ n=$n ====================${C_0}"
  local ramfile="$OUT_DIR/ram_${n}.txt"; start_ram "$ramfile"

  local base=$(( NONCE_BASE + n * 10 ))    # dải nonce riêng cho mỗi mốc
  local log0; log0="$(log_lines)"
  read -r S0 D0 <<< "$(settle_counts)"

  # --- SEED: n lệnh SELL, KHÔNG có BUY nên nằm im trong sổ ------------------
  log "Seed $n lệnh SELL (giá $BASE_PRICE..$((BASE_PRICE+n-1)))… (HTTP off-chain, không tx)"
  local t0 t1 depth=0 i price ok_ lat
  t0="$(now_ms)"
  for ((i=0;i<n;i++)); do
    price=$((BASE_PRICE+i))
    read -r ok_ lat < <(post_timed "$BENCH_MAKER_KEY" sell "$price" "$QTY" "$((base+i))")
    [ "$ok_" = "1" ] && depth=$((depth+1))
    (( i % 100 == 0 )) && log "  …đã seed $((i+1))/$n (depth=$depth)"
  done
  t1="$(now_ms)"
  local seed_s; seed_s="$(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.3f",(b-a)/1000}')"
  ok "Độ sâu sổ đạt được: depth=$depth (mục tiêu n=$n) — seed ${seed_s}s"
  [ "$depth" -ge 1 ] || { err "Seed thất bại (depth=0) — kiểm số dư off-chain / nonce"; stop_ram; return 1; }

  # --- A1b: 1 lệnh SELL nữa, KHÔNG khớp -------------------------------------
  # Ở P2 lệnh nằm hoàn toàn off-chain ⇒ 0 gas THEO THIẾT KẾ. Giá trị so sánh
  # tương đương là LATENCY (P1: phải chờ vào block; P2: chỉ 1 lượt HTTP).
  log "A1b: lệnh KHÔNG khớp ở depth≈$depth (P2: off-chain ⇒ 0 gas)…"
  local gas_nomatch=0 lat_nomatch
  read -r ok_ lat_nomatch < <(post_timed "$BENCH_MAKER_KEY" sell "$((BASE_PRICE+n+5))" "$QTY" "$((base+n+1))")
  ok "A1b gas=0 (off-chain) · latency=${lat_nomatch}ms"

  # --- A1a: 1 lệnh BUY khớp → 1 fill → 1 batch → 1 tx -----------------------
  log "A1a: 1 lệnh BUY khớp ở depth≈$depth → chờ settle…"
  local lg; lg="$(log_lines)"
  read -r ok_ lat < <(post_timed "$BENCH_TAKER_KEY" buy "$((BASE_PRICE+n+5))" "$QTY" "$((base+n+2))")
  local got; got="$(wait_settled "$lg" 1 $(( (TICK+25)*3 )))"
  local gas_buy=0 txh=""
  if [ "${got:-0}" -ge 1 ]; then
    txh="$(settle_txhashes_since "$lg" | head -1)"
    gas_buy="$(tx_field "$txh" '.gas_used')"
    [ "$gas_buy" = "NA" ] && gas_buy=0
  else
    warn "A1a: không thấy SETTLED — gas=0 (KHÔNG tin cậy)"
  fi
  # P2 chỉ có MỘT tx cho cả trade (P1 cần 2 lệnh on-chain) ⇒ không cộng dồn.
  local gas_trade="$gas_buy"
  ok "A1a gas(SubmitBatchProof)=$gas_buy = gas/trade=$gas_trade  [P2: 1 tx/trade]"

  # --- A3: throughput -------------------------------------------------------
  local K="${THROUGHPUT_K:-$depth}"
  [ "$K" -gt "$depth" ] && K="$depth"
  log "A3: $K lệnh BUY khớp → đo throughput…"
  local lg3; lg3="$(log_lines)"
  local t3a t3b
  t3a="$(now_ms)"
  for ((i=0;i<K;i++)); do
    post_timed "$BENCH_TAKER_KEY" buy "$((BASE_PRICE+n+5))" "$QTY" "$((base+n+10+i))" >/dev/null
  done
  local committed; committed="$(wait_settled "$lg3" "$K" $(( (TICK+25)*K + 120 )))"
  t3b="$(now_ms)"
  local dt tput
  dt="$(awk -v a="$t3a" -v b="$t3b" 'BEGIN{printf "%.3f",(b-a)/1000}')"
  tput="$(awk -v k="$committed" -v d="$dt" 'BEGIN{ if(d>0) printf "%.3f", k/d; else print "NA"}')"
  ok "A3 throughput = $committed trade / ${dt}s = ${tput} trade/s"

  # --- A6: năng lực thật (gap trung vị) vs thời gian chết (gap tối đa) ------
  local gmed gmax gcnt
  read -r gmed gmax gcnt <<< "$(settle_gap_stats "$lg3")"
  ok "A6 gap giữa 2 lần chốt: trung vị=${gmed}s · tối đa=${gmax}s · mẫu=${gcnt}"
  [ "$gmed" != "NA" ] && log "   ⇒ năng lực ≈ $(awk -v g="$gmed" 'BEGIN{if(g>0)printf "%.3f",1/g; else print "NA"}') trade/s (không tính thời gian chết)"

  # --- Rào chắn: cạn hàng đợi trước khi sang mốc sau ------------------------
  log "Chờ hàng đợi fill cạn hẳn trước khi sang mốc kế…"
  local drained; drained="$(drain_barrier "$lg3" 900)"
  [ "${drained:-0}" -gt "$committed" ] && \
    warn "sau rào chắn có thêm $(( drained - committed )) fill settle — số A3 đã bị cắt sớm"

  # --- A4 RAM (3 tiến trình) + A5 drop rate ---------------------------------
  stop_ram
  local ram_kb ram_mb; ram_kb="$(cat "$ramfile" 2>/dev/null || echo 0)"
  ram_mb="$(awk -v k="$ram_kb" 'BEGIN{printf "%.1f", k/1024}')"
  ok "A4 RAM đỉnh (ganc-sys+gazk+obd) = ${ram_mb} MB"

  read -r S1 D1 <<< "$(settle_counts)"
  local set_d=$(( S1 - S0 )) drop_d=$(( D1 - D0 )) tot=0 drop_pct=0
  tot=$(( set_d + drop_d ))
  [ "$tot" -gt 0 ] && drop_pct="$(awk -v d="$drop_d" -v t="$tot" 'BEGIN{printf "%.2f", d*100/t}')"
  ok "A5 settled=$set_d dropped=$drop_d → drop=${drop_pct}%"

  printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "$n" "$depth" "$gas_nomatch" "$gas_buy" "$gas_trade" \
    "$committed" "$dt" "$tput" "$ram_kb" "$ram_mb" \
    "$seed_s" "$lat_nomatch" "$drop_pct" "$set_d" \
    "$gmed" "$gmax" "$drained" >> "$CSV"
}

for n in $SCALES; do run_scale "$n" || warn "hệ n=$n lỗi — tiếp tục"; done

echo
head1 "KẾT QUẢ PHASE 2"
column -t -s, "$CSV" 2>/dev/null || cat "$CSV"
echo
ok "CSV: $CSV"
log "So sánh với P1: bash scripts/bench/compare_p1_p2.sh <p1.csv> $CSV"
