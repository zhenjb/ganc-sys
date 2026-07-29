#!/usr/bin/env bash
# =============================================================================
# lib.sh — thư viện dùng chung cho bộ script đo hiệu suất (BENCH-PERF).
# -----------------------------------------------------------------------------
# KHÔNG chạy trực tiếp — được `source` bởi 00_env.sh / 10_baseline.sh / ...
#
# Nguyên tắc thiết kế (theo BENCH-PERF-evaluation-plan.md §5.1):
#   1. Ghi CSV INCREMENTAL — mỗi phép đo ghi ngay, crash giữa chừng không mất số.
#   2. TÁCH thu thập khỏi phân tích — script chỉ ghi số thô dạng LONG
#      (run_id,scenario,n,rep,metric,value,unit,note); tính p50/p95/amortized
#      để 90_report.sh làm. Sai công thức thì tính lại, KHÔNG phải đo lại.
#   3. Mọi giá trị suy diễn đều ghi kèm `note` nói rõ nguồn gốc.
#   4. Thiếu dữ liệu ghi "NA" + lý do, KHÔNG bịa số.
# =============================================================================

set -uo pipefail

# --- Đường dẫn gốc -----------------------------------------------------------
BENCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GANC_SYS_DIR="$(cd "$BENCH_DIR/../.." && pwd)"

# --- Cấu hình (khớp default của scripts/real_db_mode_up.sh) -------------------
API_PORT="${API_PORT:-8080}"
API_BASE_URL="${API_BASE_URL:-http://localhost:$API_PORT}"
GAZK_PORT="${GAZK_PORT:-8090}"
GAZK_URL="${GAZK_URL:-http://localhost:$GAZK_PORT}"

CHAIN_BINARY="${CHAIN_BINARY:-obd}"
CHAIN_ID="${CHAIN_ID:-ob}"
CHAIN_NODE="${CHAIN_NODE:-http://localhost:26657}"
CHAIN_REST_URL="${CHAIN_REST_URL:-http://localhost:1317}"
CHAIN_KEYRING_BACKEND="${CHAIN_KEYRING_BACKEND:-test}"

DATABASE_URL="${DATABASE_URL:-postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable}"

ORDER_SIG_MODE="${ORDER_SIG_MODE:-adr36}"     # live default = adr36 (ví ký)
SETTLEMENT_INTERVAL="${SETTLEMENT_INTERVAL:-8s}"
MARKET="${MARKET:-ATOM/USDC}"

# Expiry của order tính bằng UNIX SECONDS ("now and expiry are unix seconds" —
# internal/state/order_validation.go:196). Mặc định của sign_order /
# sign_order_adr036 là 2000000 = 24/01/1970 ⇒ trên hệ chạy thật MỌI lệnh bị từ
# chối `expired` TRƯỚC cả khi kiểm chữ ký (validation bước 5 chạy trước bước 6).
# Vì vậy bench luôn truyền expiry tường minh = bây giờ + 1 ngày.
BENCH_EXPIRY="${BENCH_EXPIRY:-$(( $(date +%s) + 86400 ))}"

# Log backend — BẮT BUỘC để tính drop rate (đếm SETTLED/DROP).
# real_db_mode_up.sh đã ghi sẵn vào /tmp/api.real.log (cố định, KHÔNG rotate)
# nên mặc định trỏ thẳng vào đó — không phải tự redirect gì cả.
BENCH_SERVER_LOG="${BENCH_SERVER_LOG:-/tmp/api.real.log}"
BENCH_GAZK_LOG="${BENCH_GAZK_LOG:-/tmp/gazk.real.log}"

# Tài khoản đo: tên key trong keyring của chain (phải đã có tiền on-chain).
BENCH_MAKER_KEY="${BENCH_MAKER_KEY:-alice}"
BENCH_TAKER_KEY="${BENCH_TAKER_KEY:-bob}"

# --- Thư mục kết quả ---------------------------------------------------------
BENCH_OUT_ROOT="${BENCH_OUT_ROOT:-$GANC_SYS_DIR/bench-out}"
RUN_ID="${RUN_ID:-}"

# --- Màu / log ---------------------------------------------------------------
if [ -t 1 ]; then
  C_OK=$'\033[32m'; C_WARN=$'\033[33m'; C_ERR=$'\033[31m'; C_DIM=$'\033[2m'; C_B=$'\033[1m'; C_0=$'\033[0m'
else
  C_OK=""; C_WARN=""; C_ERR=""; C_DIM=""; C_B=""; C_0=""
fi

log()   { printf '%s[bench]%s %s\n'  "$C_DIM" "$C_0" "$*"; }
ok()    { printf '%s  ✓%s %s\n'      "$C_OK"  "$C_0" "$*"; }
warn()  { printf '%s  ⚠%s %s\n'      "$C_WARN" "$C_0" "$*"; }
err()   { printf '%s  ✗%s %s\n'      "$C_ERR" "$C_0" "$*" >&2; }
head1() { printf '\n%s══ %s ══%s\n'  "$C_B"   "$*" "$C_0"; }
die()   { err "$*"; exit 1; }

# =============================================================================
# Khởi tạo run: tạo thư mục + header CSV. Gọi ở đầu mỗi script.
# =============================================================================
bench_init() {
  if [ -z "$RUN_ID" ]; then
    # Ưu tiên run đang mở (file .current) để nhiều script cùng ghi 1 run.
    if [ -f "$BENCH_OUT_ROOT/.current" ]; then
      RUN_ID="$(cat "$BENCH_OUT_ROOT/.current")"
    else
      RUN_ID="$(date +%Y%m%d-%H%M%S)"
    fi
  fi
  OUT_DIR="$BENCH_OUT_ROOT/$RUN_ID"
  mkdir -p "$OUT_DIR"
  echo "$RUN_ID" > "$BENCH_OUT_ROOT/.current"

  METRICS_CSV="$OUT_DIR/metrics.csv"
  if [ ! -f "$METRICS_CSV" ]; then
    echo "run_id,scenario,n,rep,metric,value,unit,note" > "$METRICS_CSV"
  fi
  export RUN_ID OUT_DIR METRICS_CSV
}

# =============================================================================
# metric <scenario> <n> <rep> <metric> <value> <unit> [note]
#   Ghi 1 dòng CSV NGAY (incremental). value="NA" là hợp lệ — kèm note lý do.
# =============================================================================
metric() {
  local scenario="$1" n="$2" rep="$3" name="$4" value="$5" unit="$6" note="${7:-}"
  note="${note//,/;}"                      # CSV-safe
  printf '%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "$RUN_ID" "$scenario" "$n" "$rep" "$name" "$value" "$unit" "$note" >> "$METRICS_CSV"
  printf '    %-26s %s %s %s%s%s\n' "$name" "$value" "$unit" "$C_DIM" "$note" "$C_0"
}

# --- Thời gian (ms) ----------------------------------------------------------
now_ms() { date +%s%3N; }

# --- Chuyển "8s" -> 8 --------------------------------------------------------
interval_secs() { echo "${SETTLEMENT_INTERVAL%s}"; }

# =============================================================================
# have <cmd> — kiểm tra công cụ tồn tại
# =============================================================================
have() { command -v "$1" >/dev/null 2>&1; }

# =============================================================================
# chain_home — dò CHAIN_HOME thật (env > tiến trình đang chạy > mặc định)
# =============================================================================
chain_home() {
  if [ -n "${CHAIN_HOME:-}" ]; then echo "$CHAIN_HOME"; return; fi
  # dò từ dòng lệnh tiến trình chain đang chạy
  local from_ps
  from_ps="$(ps -eo args 2>/dev/null | grep "[${CHAIN_BINARY:0:1}]${CHAIN_BINARY:1}" \
             | grep -oE -- '--home[= ][^ ]+' | head -1 | sed -E 's/--home[= ]//')"
  if [ -n "$from_ps" ]; then echo "$from_ps"; return; fi
  echo "$HOME/.$CHAIN_BINARY"
}

# =============================================================================
# data_bytes — dung lượng thư mục data của chain (bytes chính xác, -sb)
#   In "NA" nếu không tìm thấy (KHÔNG bịa 0).
# =============================================================================
data_bytes() {
  local d; d="$(chain_home)/data"
  if [ -d "$d" ]; then du -sb "$d" 2>/dev/null | awk '{print $1}'; else echo "NA"; fi
}

# =============================================================================
# tx_field <txhash> <jq-path> — query tx, chịu được cả 2 dạng JSON
#   (bọc trong .tx_response hoặc phẳng).
# =============================================================================
tx_json() {
  local h="$1"
  "$CHAIN_BINARY" q tx "$h" --node "$CHAIN_NODE" --output json 2>/dev/null
}
tx_field() {
  local h="$1" path="$2" j
  j="$(tx_json "$h")" || { echo "NA"; return; }
  [ -z "$j" ] && { echo "NA"; return; }
  echo "$j" | jq -r "(.tx_response // .) | $path // \"NA\"" 2>/dev/null || echo "NA"
}

# =============================================================================
# block_time_ms <height> — timestamp block (ms) từ RPC Tendermint
# =============================================================================
block_time_ms() {
  local h="$1" ts
  ts="$(curl -s "$CHAIN_NODE/block?height=$h" \
        | jq -r '.result.block.header.time // empty' 2>/dev/null)"
  [ -z "$ts" ] && { echo "NA"; return; }
  # ISO8601 -> epoch ms (cắt nano còn mili)
  date -d "$ts" +%s%3N 2>/dev/null || echo "NA"
}

# =============================================================================
# rss_kb <pattern> — RSS (KB) của tiến trình khớp pattern; "NA" nếu không thấy
# =============================================================================
rss_kb() {
  local pat="$1" v
  v="$(ps -eo rss=,args= 2>/dev/null | grep -F "$pat" | grep -v grep \
       | sort -rn | head -1 | awk '{print $1}')"
  [ -z "$v" ] && echo "NA" || echo "$v"
}

# =============================================================================
# psql_one <sql> — chạy 1 truy vấn, in giá trị đơn; "NA" nếu lỗi
# =============================================================================
psql_one() {
  have psql || { echo "NA"; return; }
  psql "$DATABASE_URL" -tAc "$1" 2>/dev/null | head -1 | tr -d '[:space:]' || echo "NA"
}

# =============================================================================
# settle_counts — đếm SETTLED / DROP trong log backend
#   In "settled drop" (2 số). "NA NA" nếu không có log.
# =============================================================================
settle_counts() {
  if [ ! -f "$BENCH_SERVER_LOG" ]; then echo "NA NA"; return; fi
  local s d
  s="$(grep -c '\[trade-settlement\] SETTLED' "$BENCH_SERVER_LOG" 2>/dev/null || echo 0)"
  d="$(grep -c '\[trade-settlement\] DROP'    "$BENCH_SERVER_LOG" 2>/dev/null || echo 0)"
  echo "$s $d"
}

# =============================================================================
# settle_txhashes_since <start_line> — các txhash SETTLED xuất hiện SAU dòng thứ N
# =============================================================================
settle_txhashes_since() {
  local start="${1:-0}"
  [ -f "$BENCH_SERVER_LOG" ] || return 0
  # HAI định dạng txhash cùng tồn tại — phải nhận cả hai:
  #   • relayer cosmos THẬT : tx=B97647E7…  (HOA, KHÔNG có tiền tố 0x)
  #   • LocalTradeSubmitter : tx=0xb867313…  (thường, CÓ 0x)
  # Regex cũ chỉ bắt dạng có `0x` ⇒ trên hệ live luôn đếm ra 0, khiến A1a mất
  # txhash (gas=0) và A3 bỏ cuộc sớm dù settlement vẫn chạy bình thường.
  tail -n +"$((start + 1))" "$BENCH_SERVER_LOG" 2>/dev/null \
    | grep '\[trade-settlement\] SETTLED' \
    | grep -oE 'tx=(0x)?[0-9a-fA-F]{40,}' \
    | sed -E 's/^tx=(0x)?//' | tr 'a-f' 'A-F'
}

log_lines() { [ -f "$BENCH_SERVER_LOG" ] && wc -l < "$BENCH_SERVER_LOG" || echo 0; }

# =============================================================================
# TĂNG TỐC ĐẶT LỆNH — bắt buộc cho các mốc lớn.
# -----------------------------------------------------------------------------
# Bản đầu gọi 3 tiến trình con MỖI LỆNH: `go run` (biên dịch lại signer!) +
# `keys show` + `keys export` ≈ 0.5 s/lệnh. Ở n=1000 (seed 1000 + A3 1000 = 2000
# lệnh) là ~17 phút chi phí của chính công cụ đo — và nó BÓP MÉO
# A0_seed_seconds / A1b_latency_ms (đo thời gian biên dịch Go, không phải tốc độ
# hệ thống), đúng chỗ P2 đáng lẽ hơn P1 nhiều nhất.
# ⇒ Dựng signer MỘT LẦN thành binary, cache địa chỉ + khoá theo tên key.
# =============================================================================
# LƯU Ý (bẫy đã vấp): post_order luôn được gọi qua `$( … )` ⇒ chạy trong SUBSHELL.
# Mọi biến gán bên trong (kể cả cache) BIẾN MẤT khi subshell thoát. Vì vậy cache
# phải được nạp ở SHELL CHA và **export** (bench_resolve_keys) — cache cục bộ
# trong hàm là vô dụng và khiến `obd keys show/export` vẫn chạy MỖI lệnh (~500ms).
BENCH_BIN_DIR="${BENCH_BIN_DIR:-$GANC_SYS_DIR/.bench-bin}"

# bench_build_signers — biên dịch sẵn 2 signer (gọi 1 lần ở đầu script đo)
bench_build_signers() {
  mkdir -p "$BENCH_BIN_DIR"
  ( cd "$GANC_SYS_DIR" \
    && go build -o "$BENCH_BIN_DIR/sign_order"        ./p3/script-test/sign_order \
    && go build -o "$BENCH_BIN_DIR/sign_order_adr036" ./p3/script-test/sign_order_adr036 ) \
    || die "không build được signer (xem go build ./p3/script-test/...)"
  ok "đã dựng signer: $BENCH_BIN_DIR"
}

# =============================================================================
# order_owner_for <keyname> — địa chỉ bech32 (có cache)
# =============================================================================
order_owner_for() {
  "$CHAIN_BINARY" keys show "$1" -a \
    --keyring-backend "$CHAIN_KEYRING_BACKEND" \
    ${CHAIN_HOME:+--home "$CHAIN_HOME"} 2>/dev/null || echo ""
}

# =============================================================================
# privkey_hex_for <keyname> — private key hex (có cache)
#   Chỉ dùng cho TÀI KHOẢN TEST trên máy đo. KHÔNG dùng với ví thật.
# =============================================================================
privkey_hex_for() {
  yes | "$CHAIN_BINARY" keys export "$1" --unarmored-hex --unsafe \
    --keyring-backend "$CHAIN_KEYRING_BACKEND" \
    ${CHAIN_HOME:+--home "$CHAIN_HOME"} 2>/dev/null | tail -1 | tr -d '[:space:]'
}

# =============================================================================
# bench_resolve_keys — nạp địa chỉ + khoá của maker/taker MỘT LẦN rồi EXPORT.
#   Phải gọi ở shell CHA (đầu script đo). Biến export sống sót qua subshell nên
#   post_order không phải gọi `obd keys` nữa (bỏ ~500ms/lệnh).
# =============================================================================
bench_resolve_keys() {
  BENCH_MAKER_ADDR="$(order_owner_for "$BENCH_MAKER_KEY")"
  BENCH_TAKER_ADDR="$(order_owner_for "$BENCH_TAKER_KEY")"
  [ -n "$BENCH_MAKER_ADDR" ] || die "không lấy được địa chỉ key '$BENCH_MAKER_KEY'"
  [ -n "$BENCH_TAKER_ADDR" ] || die "không lấy được địa chỉ key '$BENCH_TAKER_KEY'"
  export BENCH_MAKER_ADDR BENCH_TAKER_ADDR

  if [ "$ORDER_SIG_MODE" != "mock" ] && [ -n "$ORDER_SIG_MODE" ]; then
    BENCH_MAKER_PK="$(privkey_hex_for "$BENCH_MAKER_KEY")"
    BENCH_TAKER_PK="$(privkey_hex_for "$BENCH_TAKER_KEY")"
    [ -n "$BENCH_MAKER_PK" ] || die "không export được khoá '$BENCH_MAKER_KEY'"
    [ -n "$BENCH_TAKER_PK" ] || die "không export được khoá '$BENCH_TAKER_KEY'"
    export BENCH_MAKER_PK BENCH_TAKER_PK
  fi
  ok "đã nạp khoá: $BENCH_MAKER_KEY=${BENCH_MAKER_ADDR:0:14}… · $BENCH_TAKER_KEY=${BENCH_TAKER_ADDR:0:14}…"
}

# =============================================================================
# post_order <keyname> <side> <price> <qty> <nonce>
#   Ký order đúng ORDER_SIG_MODE rồi POST /api/order. In body trả về.
# =============================================================================
post_order() {
  local keyname="$1" side="$2" price="$3" qty="$4" nonce="$5"
  local owner pk signed

  # Lấy từ biến ĐÃ EXPORT (bench_resolve_keys) — KHÔNG gọi `obd keys` ở đây:
  # hàm này chạy trong subshell nên mọi cache cục bộ đều vô ích, và 2 lệnh CLI
  # sẽ tốn ~500ms MỖI lệnh, bóp méo A0_seed_seconds / A1b_latency_ms.
  case "$keyname" in
    "$BENCH_MAKER_KEY") owner="${BENCH_MAKER_ADDR:-}"; pk="${BENCH_MAKER_PK:-}" ;;
    "$BENCH_TAKER_KEY") owner="${BENCH_TAKER_ADDR:-}"; pk="${BENCH_TAKER_PK:-}" ;;
    *)                  owner="$(order_owner_for "$keyname")"; pk="$(privkey_hex_for "$keyname")" ;;
  esac
  [ -z "$owner" ] && { echo '{"error":"key chưa resolve — gọi bench_resolve_keys trước"}'; return 1; }

  # Dùng BINARY đã dựng sẵn (bench_build_signers) — không `go run` mỗi lệnh.
  if [ "$ORDER_SIG_MODE" = "mock" ] || [ -z "$ORDER_SIG_MODE" ]; then
    signed="$("$BENCH_BIN_DIR/sign_order" -owner "$owner" -market "$MARKET" -side "$side" \
      -price "$price" -qty "$qty" -nonce "$nonce" -expiry "$BENCH_EXPIRY" 2>/dev/null)"
  else
    [ -z "$pk" ] && { echo '{"error":"thiếu privkey — gọi bench_resolve_keys trước"}'; return 1; }
    signed="$("$BENCH_BIN_DIR/sign_order_adr036" -privkey-hex "$pk" -market "$MARKET" -side "$side" \
      -price "$price" -qty "$qty" -nonce "$nonce" -expiry "$BENCH_EXPIRY" 2>/dev/null)"
  fi
  [ -z "$signed" ] && { echo '{"error":"sign failed"}'; return 1; }

  printf '%s' "$signed" | curl -s -X POST "$API_BASE_URL/api/order" \
    -H 'content-type: application/json' --data @-
}

# =============================================================================
# require_tools — dừng sớm nếu thiếu công cụ bắt buộc
# =============================================================================
require_tools() {
  local missing=""
  for t in "$@"; do have "$t" || missing="$missing $t"; done
  [ -n "$missing" ] && die "thiếu công cụ:$missing"
  return 0
}
