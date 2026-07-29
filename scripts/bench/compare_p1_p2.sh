#!/usr/bin/env bash
# =============================================================================
# compare_p1_p2.sh — Ghép CSV của 2 phase → bảng so sánh Markdown.
# -----------------------------------------------------------------------------
#   bash scripts/bench/compare_p1_p2.sh <p1.csv> <p2.csv> [out.md]
#
# Chỉ ĐỌC 2 file CSV, không chạm hệ thống — sai công thức thì chạy lại script
# này, KHÔNG phải đo lại. Ô thiếu ghi "—", KHÔNG bịa số.
#
# P1 (ganc-trade) : orderbook + khớp lệnh TOÀN BỘ on-chain.
# P2 (ganc-sys)   : orderbook + khớp lệnh off-chain, chỉ settle bằng ZK proof.
# =============================================================================

set -uo pipefail

P1="${1:-}"; P2="${2:-}"; OUT="${3:-}"
[ -f "${P1:-}" ] || { echo "dùng: compare_p1_p2.sh <p1.csv> <p2.csv> [out.md]"; exit 2; }
[ -f "${P2:-}" ] || { echo "không đọc được p2 csv: $P2"; exit 2; }
[ -z "$OUT" ] && OUT="$(dirname "$P2")/compare_p1_p2.md"

# get <csv> <n> <colname>  → giá trị ô, rỗng nếu không có
get(){
  awk -F, -v n="$2" -v want="$3" '
    NR==1 { for(i=1;i<=NF;i++) if($i==want) c=i; next }
    c && $1==n { print $c; exit }' "$1"
}
cell(){ local v; v="$(get "$1" "$2" "$3")"; [ -z "$v" ] && echo "—" || echo "$v"; }
# ratio a b → "a/b×" hoặc "—"
ratio(){
  local a="$1" b="$2"
  { [ -z "$a" ] || [ -z "$b" ] || [ "$a" = "—" ] || [ "$b" = "—" ] || [ "$b" = "0" ] || [ "$b" = "0.000" ]; } && { echo "—"; return; }
  awk -v a="$a" -v b="$b" 'BEGIN{ if(b==0){print "—"} else printf "%.1f×", a/b }'
}

NS="$(awk -F, 'NR>1 && $1!="" {print $1}' "$P1" | sort -n | uniq)"

{
echo "# So sánh Phase 1 ↔ Phase 2"
echo
echo "*Sinh bởi \`compare_p1_p2.sh\` lúc $(date -Is). P1=\`$(basename "$P1")\` · P2=\`$(basename "$P2")\`.*"
echo
echo "| | Phase 1 | Phase 2 |"
echo "|---|---|---|"
echo "| Kiến trúc | orderbook + khớp lệnh **on-chain** | orderbook + khớp lệnh **off-chain**, settle bằng **ZK proof** |"
echo "| Tx on-chain / trade | **2** (lệnh maker + lệnh taker) | **1** (\`MsgSubmitBatchProof\`) |"
echo "| Đặt lệnh | 1 tx on-chain, chờ vào block | 1 lượt HTTP, không tx |"
echo
echo '> `—` = không có số · Ô Phase 2 ghi **0** ở gas-đặt-lệnh là **theo thiết kế** (lệnh nằm off-chain), không phải thiếu dữ liệu.'
echo

# ---------- 1. Gas ----------
echo "## 1. Gas — chi phí on-chain"
echo
echo "| Độ sâu sổ | P1 gas lệnh không khớp | P2 | P1 gas/trade | P2 gas/trade | P1÷P2 |"
echo "|---|---|---|---|---|---|"
for n in $NS; do
  a="$(cell "$P1" "$n" A1b_gas_nomatch)"; b="$(cell "$P2" "$n" A1b_gas_nomatch)"
  c="$(cell "$P1" "$n" A1a_gas_per_trade)"; d="$(cell "$P2" "$n" A1a_gas_per_trade)"
  printf '| %s | %s | %s | %s | %s | %s |\n' "$n" "$a" "$b" "$c" "$d" "$(ratio "$c" "$d")"
done
echo
echo "**Đọc bảng:** cột *lệnh không khớp* là khác biệt kiến trúc rõ nhất — P1 trả gas cho **mọi** lệnh kể cả không khớp; P2 trả **0** vì lệnh chưa chạm chain."
echo

# ---------- 2. Thông lượng & thời gian ----------
echo "## 2. Thông lượng & thời gian"
echo
echo "| Độ sâu sổ | P1 trade/s | P2 trade/s | P1 seed (s) | P2 seed (s) | P2 nhanh hơn |"
echo "|---|---|---|---|---|---|"
for n in $NS; do
  a="$(cell "$P1" "$n" A3_trades_per_s)"; b="$(cell "$P2" "$n" A3_trades_per_s)"
  c="$(cell "$P1" "$n" A0_seed_seconds)"; d="$(cell "$P2" "$n" A0_seed_seconds)"
  printf '| %s | %s | %s | %s | %s | %s |\n' "$n" "$a" "$b" "$c" "$d" "$(ratio "$c" "$d")"
done
echo
echo "> P1 không ghi thời gian seed vào CSV — điền tay nếu có (đo được ~80 phút cho n=1000, do trần **1 lệnh/market/block**)."
echo

# ---------- 3. Bộ nhớ ----------
echo "## 3. Bộ nhớ đỉnh"
echo
echo "| Độ sâu sổ | P1 (MB) | P2 (MB) |"
echo "|---|---|---|"
for n in $NS; do
  printf '| %s | %s | %s |\n' "$n" "$(cell "$P1" "$n" A4_ram_mb)" "$(cell "$P2" "$n" A4_ram_mb)"
done
echo
echo "> Không cùng phạm vi: P1 = **node chain**; P2 = **node chain + backend + prover**. Nêu rõ khi báo cáo."
echo

# ---------- 4. Chỉ số riêng ----------
echo "## 4. Chỉ số chỉ một bên có"
echo
echo "| Độ sâu sổ | P2 độ trễ đặt lệnh (ms) | P2 drop rate (%) | P2 số tx settle |"
echo "|---|---|---|---|"
for n in $NS; do
  printf '| %s | %s | %s | %s |\n' "$n" \
    "$(cell "$P2" "$n" A1b_latency_ms)" "$(cell "$P2" "$n" A5_drop_pct)" "$(cell "$P2" "$n" A6_settle_tx)"
done
echo
echo "**drop rate** là chi phí riêng của P2 (per-order nullifier: một order chỉ settle được 1 lần). P1 không có hiện tượng này."
echo

# ---------- 5. Xu hướng theo độ sâu ----------
echo "## 5. Chi phí có tăng theo độ sâu sổ không?"
echo
echo "| Phase | Chỉ số | nhỏ nhất → lớn nhất | Bội số |"
echo "|---|---|---|---|"
first="$(echo "$NS" | tr ' ' '\n' | head -1)"; last="$(echo "$NS" | tr ' ' '\n' | tail -1)"
for pair in "$P1|P1|A1b_gas_nomatch|gas lệnh không khớp" \
            "$P2|P2|A1b_gas_nomatch|gas lệnh không khớp" \
            "$P1|P1|A1a_gas_per_trade|gas/trade" \
            "$P2|P2|A1a_gas_per_trade|gas/trade" \
            "$P1|P1|A3_trades_per_s|throughput" \
            "$P2|P2|A3_trades_per_s|throughput"; do
  IFS='|' read -r f ph col label <<< "$pair"
  x="$(get "$f" "$first" "$col")"; y="$(get "$f" "$last" "$col")"
  [ -z "$x" ] && x="—"; [ -z "$y" ] && y="—"
  printf '| %s | %s | %s → %s | %s |\n' "$ph" "$label" "$x" "$y" "$(ratio "$y" "$x")"
done
echo
echo "Đây là câu hỏi kiến trúc cốt lõi: **chi phí có phụ thuộc độ sâu sổ không?** P1 khớp lệnh trong chain nên phải quét sổ on-chain; P2 khớp off-chain nên proof chỉ cam kết **một** fill."
echo
echo "---"
echo
echo "*Nguồn: \`$P1\` · \`$P2\`.*"
} > "$OUT"

echo "Bảng so sánh: $OUT"
echo
sed -n '1,70p' "$OUT"
