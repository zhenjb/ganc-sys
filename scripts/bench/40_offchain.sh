#!/usr/bin/env bash
# =============================================================================
# 40_offchain.sh — Đo chi phí TÍNH TOÁN OFF-CHAIN (chạy LOCAL, không cần chain).
# -----------------------------------------------------------------------------
# Chạy song song lúc chờ Codespace. Thu:
#   (a) chi phí per-fill off-chain bằng PHƯƠNG PHÁP VI SAI
#       T(count_hi) − T(count_lo) / (hi − lo)  → loại sạch chi phí khởi động
#   (b) phân rã CPU/alloc theo chặng bằng pprof (TƯƠNG ĐỐI, không phải ns/op
#       tuyệt đối — repo chưa có Benchmark*, đã ghi rõ trong plan §7.9)
#
#   bash scripts/bench/40_offchain.sh
# =============================================================================

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
bench_init
require_tools go awk

SC=offchain; N=0
LO="${BENCH_COUNT_LO:-100}"
HI="${BENCH_COUNT_HI:-2000}"
REPS="${BENCH_REPS:-2}"

cd "$GANC_SYS_DIR" || die "không vào được $GANC_SYS_DIR"

head1 "OFF-CHAIN (local) — run_id=$RUN_ID"

# --- 1. Vi sai ---------------------------------------------------------------
head1 "1. Chi phí per-fill (vi sai count=$LO vs count=$HI, $REPS rep)"
run_count() {
  go test ./internal/service -run 'TestSettleTradesHappyPath$' -count="$1" 2>&1 \
    | awk '/^ok/{print $NF}' | sed 's/s$//'
}
# Lấy MIN qua các rep, không phải trung bình: nhiễu hệ điều hành chỉ CỘNG thêm
# thời gian, nên min là ước lượng sạch nhất của chi phí thực.
min_of() { awk 'BEGIN{m=""} {if($1!=""&&(m==""||$1+0<m+0))m=$1} END{print m}'; }
T_LO="$(for r in $(seq 1 "$REPS"); do v="$(run_count "$LO")"; [ -n "$v" ] && { echo "$v"; log "count=$LO rep$r → ${v}s" >&2; }; done | min_of)"
T_HI="$(for r in $(seq 1 "$REPS"); do v="$(run_count "$HI")"; [ -n "$v" ] && { echo "$v"; log "count=$HI rep$r → ${v}s" >&2; }; done | min_of)"

if [ -z "$T_LO" ] || [ -z "$T_HI" ]; then
  metric $SC $N 0 offchain_per_fill_ms NA ms "go test không trả thời gian"
else
  PER_MS="$(awk -v tl="$T_LO" -v th="$T_HI" -v lo="$LO" -v hi="$HI" \
    'BEGIN{ printf "%.4f", (th-tl)*1000/(hi-lo) }')"
  # GUARD: chênh lệch quá nhỏ so với nhiễu ⇒ vi sai có thể ra ÂM (vô nghĩa về
  # vật lý). Báo NA + hướng khắc phục, KHÔNG ghi số rác vào báo cáo.
  if awk -v p="$PER_MS" 'BEGIN{exit !(p<=0)}'; then
    warn "vi sai ra $PER_MS ms (≤0) — nhiễu lớn hơn tín hiệu ở spread $LO→$HI"
    metric $SC $N 0 offchain_per_fill_ms NA ms \
      "vi sai ≤0 (nhiễu > tín hiệu); tăng BENCH_COUNT_HI hoặc BENCH_REPS rồi đo lại"
  else
    metric $SC $N 0 offchain_per_fill_ms "$PER_MS" ms \
      "vi sai min(T($HI))-min(T($LO)) / ($HI-$LO); gồm setup ⇒ CẬN TRÊN"
    TICK="$(interval_secs)"
    metric $SC $N 0 offchain_share_of_cycle \
      "$(awk -v p="$PER_MS" -v t="$TICK" 'BEGIN{printf "%.4f", p/(t*1000)*100}')" % \
      "off-chain / chu kỳ tick ${TICK}s"
  fi
fi

# --- 2. pprof ----------------------------------------------------------------
head1 "2. Phân rã CPU / alloc theo chặng (pprof — TƯƠNG ĐỐI)"
CPU="$OUT_DIR/cpu.out"; MEM="$OUT_DIR/mem.out"; BIN="$OUT_DIR/svc.test.exe"
PROF_COUNT="${BENCH_PROF_COUNT:-500}"
go test ./internal/service -run 'TestSettleTrades' -count="$PROF_COUNT" \
  -cpuprofile "$CPU" -memprofile "$MEM" -o "$BIN" >/dev/null 2>&1 \
  || warn "profiling lỗi — bỏ qua"

if [ -f "$CPU" ]; then
  go tool pprof -top -cum -nodecount=400 "$BIN" "$CPU" 2>/dev/null \
    | grep -E "ganc-sys/(internal|pkg)" > "$OUT_DIR/pprof_cpu.txt"
  go tool pprof -top -cum -sample_index=alloc_space -nodecount=400 "$BIN" "$MEM" 2>/dev/null \
    | grep -E "ganc-sys/(internal|pkg)" > "$OUT_DIR/pprof_alloc.txt"

  # Trích giá trị `cum` của một chặng. Layout pprof -top:
  #   flat  flat%  sum%  CUM  cum%  tên-hàm      ⇒ cum LUÔN ở cột 4,
  # đúng cho cả CPU ("420ms") lẫn alloc ("304.76MB") — giá trị tự mang đơn vị.
  extract() {  # $1=file  $2=regex
    local v
    v="$(grep -E "$2" "$1" 2>/dev/null | head -1 | awk '{print $4}')"
    echo "${v:-NA}"
  }
  for pair in \
    "settleOneFill|settle_one_fill" \
    "TradeApplier..Apply|apply" \
    "BuildTradeBatch|build_batch" \
    "buildTradeWitness|witness" \
    "MatchingEngine..Match|match" \
    "state.ComputeRoot|compute_root" \
    "RealOrderService..CreateOrder|create_order"
  do
    rx="${pair%%|*}"; nm="${pair##*|}"
    cv="$(extract "$OUT_DIR/pprof_cpu.txt"   "$rx")"
    av="$(extract "$OUT_DIR/pprof_alloc.txt" "$rx")"
    # NA ở đây KHÔNG phải lỗi: hàm chạy nhanh hơn chu kỳ lấy mẫu của pprof
    # (~10ms) nên không xuất hiện — bản thân điều đó đã là kết luận "rất rẻ".
    metric $SC $N 0 "cpu_${nm}"   "$cv" cum \
      "$([ "$cv" = NA ] && echo "dưới ngưỡng lấy mẫu pprof ⇒ chi phí không đáng kể" || echo "pprof CPU cum")"
    metric $SC $N 0 "alloc_${nm}" "$av" cum \
      "$([ "$av" = NA ] && echo "không xuất hiện trong profile" || echo "pprof alloc_space cum")"
  done
  ok "chi tiết: $OUT_DIR/pprof_cpu.txt · $OUT_DIR/pprof_alloc.txt"
fi

# --- 3. E2E in-process -------------------------------------------------------
head1 "3. E2E in-process (1 trade, prover STUB)"
E2E="$OUT_DIR/e2e.exe"
if go build -o "$E2E" ./p3/script-test/e2e_trade 2>/dev/null; then
  "$E2E" >/dev/null 2>&1                      # warm-up (bỏ lần cold)
  best="";
  for i in 1 2 3 4 5; do
    t0="$(now_ms)"; "$E2E" > "$OUT_DIR/e2e_run$i.txt" 2>&1; t1="$(now_ms)"
    d=$((t1-t0)); log "run$i: ${d} ms"
    best="$best $d"
  done
  P50="$(echo $best | tr ' ' '\n' | sort -n | awk '{a[NR]=$1} END{print a[int((NR+1)/2)]}')"
  metric $SC $N 0 e2e_inprocess_p50_ms "$P50" ms "5 lần sau warm-up; prover STUB"
else
  metric $SC $N 0 e2e_inprocess_p50_ms NA ms "build e2e_trade lỗi"
fi

head1 "XONG OFF-CHAIN"
warn "prove/submit ở đây là STUB ⇒ 2 chặng đó KHÔNG đại diện hệ thật."
ok "Số off-chain (match/apply/build/witness) thì hợp lệ. CSV: $METRICS_CSV"
