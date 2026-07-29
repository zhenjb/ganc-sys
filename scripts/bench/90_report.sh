#!/usr/bin/env bash
# =============================================================================
# 90_report.sh — Đọc metrics.csv (dạng LONG) → bảng trực quan + Markdown.
# -----------------------------------------------------------------------------
# TÁCH KHỎI PHÉP ĐO: chỉ đọc CSV, không chạm hệ thống. Sai công thức thì chạy
# lại script này, KHÔNG phải đo lại.
#
#   bash scripts/bench/90_report.sh              # run mới nhất
#   RUN_ID=20260728-101500 bash scripts/bench/90_report.sh
#
# Xuất:  <out>/report.md  (dán thẳng vào BENCH-PERF-results-phase2.md)
#        <out>/summary.csv (dạng WIDE: metric × mốc)
# =============================================================================

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
bench_init
[ -f "$METRICS_CSV" ] || die "không thấy $METRICS_CSV — chạy 00_env.sh / 10_baseline.sh trước"

REPORT="$OUT_DIR/report.md"
SUMMARY="$OUT_DIR/summary.csv"

# get <scenario> <n> <metric>  → giá trị (rỗng nếu không có); nhiều rep → lấy TRUNG VỊ
get() {
  awk -F, -v s="$1" -v n="$2" -v m="$3" '
    $2==s && $3==n && $5==m && $6!="NA" && $6!="" { v[++c]=$6 }
    END{ if(c==0){print ""; exit}
         # sắp xếp nổi bọt (số ít phần tử)
         for(i=1;i<=c;i++) for(j=i+1;j<=c;j++) if(v[j]+0<v[i]+0){t=v[i];v[i]=v[j];v[j]=t}
         print v[int((c+1)/2)] }' "$METRICS_CSV"
}
note() {
  awk -F, -v s="$1" -v n="$2" -v m="$3" '$2==s && $3==n && $5==m {print $8; exit}' "$METRICS_CSV"
}
cell() { local v; v="$(get "$1" "$2" "$3")"; [ -z "$v" ] && echo "—" || echo "$v"; }
# div a b [scale] — chia an toàn, "—" nếu thiếu
div() {
  local a="$1" b="$2" s="${3:-2}"
  { [ -z "$a" ] || [ -z "$b" ] || [ "$b" = "0" ]; } && { echo "—"; return; }
  awk -v a="$a" -v b="$b" -v s="$s" 'BEGIN{printf "%.*f", s, a/b}'
}

{
echo "# Kết quả đo — run \`$RUN_ID\`"
echo
echo "*Sinh tự động bởi \`scripts/bench/90_report.sh\` lúc $(date -Is).*"
echo
echo '> Quy ước: `—` = chưa đo · `NA` = **không đo được** (lý do ở cột Ghi chú).'
echo

# ---------- Môi trường ----------
echo "## 0. Môi trường"
echo
if [ -f "$OUT_DIR/env.txt" ]; then
  echo '```'
  grep -vE '^\s*#|^\s*$' "$OUT_DIR/env.txt"
  echo '```'
else
  echo "*(chưa chạy 00_env.sh)*"
fi
echo

# ---------- Baseline ----------
echo "## 1. Baseline — 1 trade"
echo
echo "| Chỉ số | Giá trị | Đơn vị | Ghi chú |"
echo "|---|---|---|---|"
for m in gas_submit_batch_proof gas_wanted state_growth_per_trade proof_size \
         proving_key_size verifying_key_size finality_wall_ms finality_block_ms \
         cycle_per_fill rss_ganc_sys rss_gazk; do
  v="$(get baseline 1 "$m")"; [ -z "$v" ] && v="—"
  u="$(awk -F, -v m="$m" '$5==m {print $7; exit}' "$METRICS_CSV")"
  echo "| \`$m\` | **$v** | ${u:--} | $(note baseline 1 "$m") |"
done
echo

CYC="$(get baseline 1 cycle_per_fill)"
if [ -n "$CYC" ]; then
  echo "**Dự toán ngân sách kịch bản A** (chu kỳ/fill = ${CYC}s):"
  echo
  echo "| N | Ước tính |"
  echo "|---|---|"
  for n in 10 100 1000; do
    echo "| $n | $(awk -v c="$CYC" -v n="$n" 'BEGIN{s=c*n; if(s<3600) printf "%.0f s (~%.0f phút)", s, s/60; else printf "%.0f s (~%.1f giờ)", s, s/3600}') |"
  done
  echo
fi

# ---------- Scale ----------
echo "## 2. Quy mô — kịch bản A (fill độc lập) vs B (shared-order)"
echo
echo "| Chỉ số | 10 A | 100 A | 1000 A | 10 B | 100 B | 1000 B |"
echo "|---|---|---|---|---|---|---|"
row() {
  local m="$1"
  printf '| `%s` | %s | %s | %s | %s | %s | %s |\n' "$m" \
    "$(cell A 10 "$m")" "$(cell A 100 "$m")" "$(cell A 1000 "$m")" \
    "$(cell B 10 "$m")" "$(cell B 100 "$m")" "$(cell B 1000 "$m")"
}
for m in orders_placed tx_settled settled_delta dropped_delta drop_rate_pct \
         wall_clock_ms gas_total gas_per_tx state_growth_total rss_ganc_sys; do
  row "$m"
done
echo

# ---------- Amortized ----------
echo "### 2.1 Quy về /trade (tính từ bảng trên)"
echo
echo "| Chỉ số | 10 A | 100 A | 1000 A |"
echo "|---|---|---|---|"
printf '| gas / trade | %s | %s | %s |\n' \
  "$(div "$(get A 10 gas_total)" 10 0)" "$(div "$(get A 100 gas_total)" 100 0)" "$(div "$(get A 1000 gas_total)" 1000 0)"
printf '| bytes / trade | %s | %s | %s |\n' \
  "$(div "$(get A 10 state_growth_total)" 10 0)" "$(div "$(get A 100 state_growth_total)" 100 0)" "$(div "$(get A 1000 state_growth_total)" 1000 0)"
printf '| giây / trade | %s | %s | %s |\n' \
  "$(div "$(get A 10 wall_clock_ms)" 10000 2)" "$(div "$(get A 100 wall_clock_ms)" 100000 2)" "$(div "$(get A 1000 wall_clock_ms)" 1000000 2)"
printf '| **TPS** (settled/s) | %s | %s | %s |\n' \
  "$(div 10000 "$(get A 10 wall_clock_ms)" 3)" "$(div 100000 "$(get A 100 wall_clock_ms)" 3)" "$(div 1000000 "$(get A 1000 wall_clock_ms)" 3)"
echo
echo "> **TPS ở đây = giao dịch ĐÃ SETTLE ON-CHAIN mỗi giây** — không phải tốc độ nhận lệnh (intake/gateway) như đa số nền tảng công bố. Khi so sánh phải nói rõ định nghĩa."
echo

# ---------- Off-chain ----------
echo "## 3. Off-chain (local, prover STUB)"
echo
PF="$(get offchain 0 offchain_per_fill_ms)"
SH="$(get offchain 0 offchain_share_of_cycle)"
if [ -n "$PF" ]; then
  echo "- **Chi phí off-chain/fill ≈ ${PF} ms** (vi sai; đã gồm setup ⇒ cận trên)."
  [ -n "$SH" ] && echo "- Chiếm **${SH}%** chu kỳ 1 fill ⇒ nút thắt nằm ở **prove + chain commit**, không phải off-chain."
  echo
fi
echo "| Chặng | CPU (cum) | Alloc (cum) |"
echo "|---|---|---|"
for nm in settle_one_fill apply build_batch witness match compute_root create_order; do
  printf '| `%s` | %s | %s |\n' "$nm" "$(cell offchain 0 "cpu_$nm")" "$(cell offchain 0 "alloc_$nm")"
done
echo
echo "> pprof cho **tỷ lệ tương đối**, không phải ns/op tuyệt đối (repo chưa có \`Benchmark*\` — xem plan §7.9)."
echo

# ---------- RAM ----------
echo "## 4. Bộ nhớ"
echo
printf '| Tiến trình | Đỉnh RSS |\n|---|---|\n'
for m in rss_ganc_sys_max rss_gazk_max; do
  v="$(get rss 0 "$m")"
  [ -z "$v" ] && { echo "| \`$m\` | — |"; continue; }
  echo "| \`$m\` | $(awk -v v="$v" 'BEGIN{printf "%.1f MB", v/1024}') |"
done
echo

# ---------- NA ----------
echo "## 5. Không đo được (ghi rõ lý do — KHÔNG bịa số)"
echo
echo "| Chỉ số | Lý do |"
echo "|---|---|"
awk -F, '$6=="NA" && $8!="" {key=$5; if(!(key in seen)){seen[key]=1; printf "| `%s` | %s |\n", $5, $8}}' "$METRICS_CSV"
echo

# ---------- Tự kiểm tra ----------
echo "## 6. Tự kiểm tra"
echo
fail="$(awk -F, '$5=="selfcheck" && $6=="FAILED"' "$METRICS_CSV" | wc -l)"
if [ "$fail" -gt 0 ]; then
  echo "⚠️ **$fail ô LỆCH kỳ vọng** — nhiều khả năng dựng sai kịch bản, cần xem lại trước khi dùng số:"
  echo
  awk -F, '$5=="selfcheck" && $6=="FAILED" {printf "- kịch bản **%s**, N=%s, rep %s\n", $2,$3,$4}' "$METRICS_CSV"
else
  echo "✅ Mọi ô đo đều khớp kỳ vọng (A → drop=0, tx=N · B → tx=1, drop≈(N−1)/N)."
fi
echo
echo "---"
echo
echo "*Dữ liệu thô: \`$(basename "$METRICS_CSV")\` ($(($(wc -l < "$METRICS_CSV") - 1)) phép đo).*"
} > "$REPORT"

# ---------- summary.csv (WIDE) ----------
{
  echo "metric,unit,baseline,A10,A100,A1000,B10,B100,B1000"
  awk -F, 'NR>1 && $5!="" {print $5"|"$7}' "$METRICS_CSV" | sort -u | while IFS='|' read -r m u; do
    printf '%s,%s,%s,%s,%s,%s,%s,%s,%s\n' "$m" "$u" \
      "$(get baseline 1 "$m")" "$(get A 10 "$m")" "$(get A 100 "$m")" "$(get A 1000 "$m")" \
      "$(get B 10 "$m")" "$(get B 100 "$m")" "$(get B 1000 "$m")"
  done
} > "$SUMMARY"

head1 "BÁO CÁO"
echo "  Markdown : $REPORT"
echo "  CSV wide : $SUMMARY"
echo "  CSV thô  : $METRICS_CSV"
echo
sed -n '1,60p' "$REPORT"
echo
ok "Dán report.md vào docs/matching_orderbook/BENCH-PERF-results-phase2.md"
