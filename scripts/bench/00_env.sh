#!/usr/bin/env bash
# =============================================================================
# 00_env.sh — Pre-flight + ghi nhận môi trường đo.
# -----------------------------------------------------------------------------
# Chạy ĐẦU TIÊN. Kiểm tra mọi thành phần bắt buộc và ghi spec máy vào
# bench-out/<run>/env.txt. Nếu có mục BẮT BUỘC hỏng → thoát khác 0 để
# run_all.sh dừng ngay (đo trên môi trường hỏng chỉ tạo ra số rác).
#
#   bash scripts/bench/00_env.sh
# =============================================================================

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
bench_init

ENV_TXT="$OUT_DIR/env.txt"
fails=0
warns=0

{
  echo "# Môi trường đo — run_id=$RUN_ID"
  echo "# Sinh bởi scripts/bench/00_env.sh lúc $(date -Is)"
  echo
} > "$ENV_TXT"

record() { printf '%-24s %s\n' "$1" "$2" >> "$ENV_TXT"; }

head1 "1. Phần cứng & hệ điều hành"
CPU_MODEL="$(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2- | sed 's/^ *//')"
CPU_CORES="$(nproc 2>/dev/null || echo NA)"
MEM_TOTAL="$(awk '/MemTotal/{printf "%.1f GB", $2/1048576}' /proc/meminfo 2>/dev/null || echo NA)"
DISK_FREE="$(df -h . 2>/dev/null | tail -1 | awk '{print $4}')"
DISK_FREE_KB="$(df -k . 2>/dev/null | tail -1 | awk '{print $4}')"

record "cpu_model"   "${CPU_MODEL:-NA}"
record "cpu_cores"   "$CPU_CORES"
record "mem_total"   "$MEM_TOTAL"
record "disk_free"   "${DISK_FREE:-NA}"
record "os"          "$(uname -sr 2>/dev/null || echo NA)"
record "date"        "$(date -Is)"
ok "CPU $CPU_CORES cores · RAM $MEM_TOTAL · đĩa trống ${DISK_FREE:-NA}"

# Cảnh báo đĩa: N=1000 sinh 1000 batch → IAVL phình.
if [ -n "${DISK_FREE_KB:-}" ] && [ "${DISK_FREE_KB:-0}" -lt 5242880 ] 2>/dev/null; then
  warn "đĩa trống < 5 GB — mốc N=1000 có thể không đủ chỗ"; warns=$((warns+1))
fi

head1 "2. Công cụ"
for t in curl jq go awk; do
  if have "$t"; then ok "$t → $(command -v $t)"; else err "$t THIẾU (bắt buộc)"; fails=$((fails+1)); fi
done
for t in psql bc; do
  if have "$t"; then ok "$t → $(command -v $t)"
  else warn "$t thiếu — một số chỉ số sẽ là NA (psql: proof size · bc: số thập phân)"; warns=$((warns+1)); fi
done
record "go_version" "$(go version 2>/dev/null | awk '{print $3, $4}' || echo NA)"

head1 "3. Chain"
if have "$CHAIN_BINARY"; then
  ok "$CHAIN_BINARY → $(command -v $CHAIN_BINARY)"
  CH="$(chain_home)"
  record "chain_binary" "$CHAIN_BINARY"
  record "chain_home"   "$CH"
  record "chain_node"   "$CHAIN_NODE"
  record "chain_id"     "$CHAIN_ID"
  if [ -d "$CH/data" ]; then ok "data dir: $CH/data ($(data_bytes) bytes)"
  else warn "KHÔNG thấy $CH/data — đặt CHAIN_HOME=... rồi chạy lại (storage sẽ là NA)"; warns=$((warns+1)); fi

  H="$(curl -s "$CHAIN_NODE/status" | jq -r '.result.sync_info.latest_block_height // empty' 2>/dev/null)"
  if [ -n "$H" ]; then ok "node sống, block height=$H"; record "block_height_at_start" "$H"
  else err "KHÔNG kết nối được $CHAIN_NODE (bắt buộc)"; fails=$((fails+1)); fi
else
  err "$CHAIN_BINARY THIẾU (bắt buộc)"; fails=$((fails+1))
fi

head1 "4. Backend / prover / DB"
CODE="$(curl -s -o /dev/null -w '%{http_code}' "$API_BASE_URL/api/state" 2>/dev/null)"
if [ "$CODE" = "200" ]; then ok "ganc-sys $API_BASE_URL → HTTP 200"
else err "ganc-sys $API_BASE_URL → HTTP ${CODE:-timeout} (bắt buộc)"; fails=$((fails+1)); fi

GCODE="$(curl -s -o /dev/null -w '%{http_code}' "$GAZK_URL/healthz" 2>/dev/null)"
if [ "$GCODE" = "200" ]; then ok "gazk $GAZK_URL → HTTP 200"
else warn "gazk $GAZK_URL → HTTP ${GCODE:-timeout} (thử /healthz; nếu prover chưa chạy thì trade KHÔNG settle được)"; warns=$((warns+1)); fi

if [ "$(psql_one 'select 1')" = "1" ]; then ok "DB kết nối OK"
else warn "DB không kết nối được → proof size sẽ là NA"; warns=$((warns+1)); fi

head1 "5. Cấu hình ảnh hưởng phép đo"
record "order_sig_mode"      "$ORDER_SIG_MODE"
record "settlement_interval" "$SETTLEMENT_INTERVAL"
record "stp_mode"            "${STP_MODE:-cancel-newest}"
record "market"              "$MARKET"
log "ORDER_SIG_MODE = $ORDER_SIG_MODE"
if [ "$ORDER_SIG_MODE" = "adr36" ]; then
  ok "adr36 — dùng sign_order_adr036 (ký bằng khoá keyring, KHÔNG cần ví)"
else
  ok "mock — dùng sign_order"
fi
log "SETTLEMENT_INTERVAL = $SETTLEMENT_INTERVAL  ⇒ chu kỳ tối thiểu 1 fill ≈ ${SETTLEMENT_INTERVAL} + prove + commit"

head1 "6. Log backend (BẮT BUỘC cho drop rate)"
if [ -f "$BENCH_SERVER_LOG" ]; then
  ok "log: $BENCH_SERVER_LOG ($(log_lines) dòng)"
  record "server_log" "$BENCH_SERVER_LOG"
else
  err "KHÔNG thấy $BENCH_SERVER_LOG"
  err "  → real_db_mode_up.sh ghi log vào /tmp/api.real.log — backend đã chạy chưa?"
  err "  → nếu khởi động kiểu khác: BENCH_SERVER_LOG=/đường/dẫn/log bash ..."
  err "  Thiếu log ⇒ KHÔNG tính được drop rate (chỉ số đặc thù quan trọng nhất)."
  fails=$((fails+1))
fi

head1 "7. Tài khoản đo"
for k in "$BENCH_MAKER_KEY" "$BENCH_TAKER_KEY"; do
  a="$(order_owner_for "$k")"
  if [ -n "$a" ]; then ok "key '$k' → $a"; record "acct_$k" "$a"
  else err "key '$k' KHÔNG có trong keyring ($CHAIN_KEYRING_BACKEND)"; fails=$((fails+1)); fi
done

# Số dư off-chain — không có thì mọi lệnh sẽ bị từ chối reserve.
ST="$(curl -s "$API_BASE_URL/api/state" 2>/dev/null)"
MA="$(order_owner_for "$BENCH_MAKER_KEY")"; TA="$(order_owner_for "$BENCH_TAKER_KEY")"
for a in "$MA" "$TA"; do
  [ -z "$a" ] && continue
  bal="$(echo "$ST" | jq -r --arg a "$a" '[.userBalances // {} | to_entries[] | select(.key|startswith($a))] | length' 2>/dev/null)"
  if [ "${bal:-0}" -gt 0 ] 2>/dev/null; then ok "$a có $bal mục số dư off-chain"
  else warn "$a CHƯA có số dư off-chain → cần deposit trước khi đặt lệnh"; warns=$((warns+1)); fi
done

# --- Ghi metric spec vào CSV -------------------------------------------------
metric env 0 0 cpu_cores      "$CPU_CORES"            cores  "$CPU_MODEL"
metric env 0 0 disk_free_kb   "${DISK_FREE_KB:-NA}"   KB     ""
metric env 0 0 settle_interval "$(interval_secs)"     s      "SETTLEMENT_INTERVAL"

head1 "KẾT QUẢ PRE-FLIGHT"
echo "  run_id   : $RUN_ID"
echo "  kết quả  : $OUT_DIR/"
echo "  env      : $ENV_TXT"
if [ "$fails" -gt 0 ]; then
  err "$fails mục BẮT BUỘC hỏng — SỬA rồi chạy lại trước khi đo."
  exit 1
fi
[ "$warns" -gt 0 ] && warn "$warns cảnh báo — đo được, nhưng vài chỉ số sẽ là NA."
ok "Sẵn sàng. Bước kế: bash scripts/bench/10_baseline.sh"
