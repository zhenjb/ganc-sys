#!/usr/bin/env bash
# =============================================================================
# 30_rss.sh — Lấy mẫu RAM (RSS) của ganc-sys + gazk theo thời gian.
# -----------------------------------------------------------------------------
# Chạy NỀN, song song với 20_scale.sh, để thấy bộ nhớ phình theo N (orderbook /
# order / trade đều IN-MEMORY). Lấy mẫu nền — KHÔNG xen vào vòng lặp đo, tránh
# làm nhiễu chính phép đo.
#
#   bash scripts/bench/30_rss.sh start      # chạy nền, ghi PID
#   bash scripts/bench/30_rss.sh stop       # dừng + in tóm tắt (min/max/cuối)
#   INTERVAL=5 bash scripts/bench/30_rss.sh start
# =============================================================================

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
bench_init

INTERVAL="${INTERVAL:-5}"
RSS_CSV="$OUT_DIR/rss.csv"
PIDFILE="$OUT_DIR/.rss.pid"

sample_loop() {
  [ -f "$RSS_CSV" ] || echo "epoch_ms,ganc_sys_kb,gazk_kb" > "$RSS_CSV"
  while true; do
    printf '%s,%s,%s\n' "$(now_ms)" "$(rss_kb ganc-sys)" "$(rss_kb gazk)" >> "$RSS_CSV"
    sleep "$INTERVAL"
  done
}

summarize() {
  [ -f "$RSS_CSV" ] || { warn "chưa có mẫu nào"; return; }
  local n; n=$(($(wc -l < "$RSS_CSV") - 1))
  [ "$n" -le 0 ] && { warn "chưa có mẫu nào"; return; }
  head1 "Tóm tắt RSS ($n mẫu, mỗi ${INTERVAL}s)"
  awk -F, 'NR>1 && $2!="NA" {
      if(mn==""||$2<mn)mn=$2; if($2>mx)mx=$2; last=$2; s+=$2; c++
    } END {
      if(c>0) printf "  ganc-sys : min %.1f MB · max %.1f MB · tb %.1f MB · cuối %.1f MB\n",
        mn/1024, mx/1024, s/c/1024, last/1024
    }' "$RSS_CSV"
  awk -F, 'NR>1 && $3!="NA" {
      if(mn==""||$3<mn)mn=$3; if($3>mx)mx=$3; last=$3; s+=$3; c++
    } END {
      if(c>0) printf "  gazk     : min %.1f MB · max %.1f MB · tb %.1f MB · cuối %.1f MB\n",
        mn/1024, mx/1024, s/c/1024, last/1024
    }' "$RSS_CSV"

  # đưa max vào metrics.csv để 90_report.sh dùng
  local g z
  g="$(awk -F, 'NR>1 && $2!="NA" && $2>m {m=$2} END{print (m==""?"NA":m)}' "$RSS_CSV")"
  z="$(awk -F, 'NR>1 && $3!="NA" && $3>m {m=$3} END{print (m==""?"NA":m)}' "$RSS_CSV")"
  metric rss 0 0 rss_ganc_sys_max "$g" KB "đỉnh trong toàn phiên lấy mẫu"
  metric rss 0 0 rss_gazk_max     "$z" KB "đỉnh trong toàn phiên lấy mẫu"
}

case "${1:-}" in
  start)
    if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
      warn "đang chạy rồi (pid $(cat "$PIDFILE"))"; exit 0
    fi
    sample_loop & echo $! > "$PIDFILE"
    ok "lấy mẫu RSS mỗi ${INTERVAL}s → $RSS_CSV (pid $(cat "$PIDFILE"))"
    ;;
  stop)
    if [ -f "$PIDFILE" ]; then
      kill "$(cat "$PIDFILE")" 2>/dev/null && ok "đã dừng"
      rm -f "$PIDFILE"
    else warn "không thấy tiến trình đang chạy"; fi
    summarize
    ;;
  *) echo "dùng: 30_rss.sh start|stop"; exit 2 ;;
esac
