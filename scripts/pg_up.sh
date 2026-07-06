#!/usr/bin/env bash
#
# pg_up.sh (Phase 1) — Postgres for ganc-sys offchain/DB mode.
# ---------------------------------------------------------------------------
# Starts a local Postgres container matching ganc-sys's DEFAULT DSN
#   postgres://ganc:ganc@localhost:5432/ganc_sys?sslmode=disable
# and applies the migrations in ./migrations. Idempotent — every migration
# uses CREATE ... IF NOT EXISTS, and the container is reused if present.
#
# Meant to run INSIDE the Codespace (where docker is available). After this,
# ganc-sys picks up the DB automatically via the default DATABASE_URL.
#
# Requirements: docker. Override via PG_CONTAINER / PG_IMAGE / PG_PORT env.
# ---------------------------------------------------------------------------
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GANC_SYS_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

CONTAINER="${PG_CONTAINER:-ganc-pg}"
PG_IMAGE="${PG_IMAGE:-postgres:16}"
PG_PORT="${PG_PORT:-5432}"
PG_USER=ganc; PG_PASS=ganc; PG_DB=ganc_sys

c_green=$'\033[1;32m'; c_red=$'\033[1;31m'; c_yellow=$'\033[1;33m'; c_reset=$'\033[0m'
ok()   { echo "${c_green}[ ok ]${c_reset} $*"; }
note() { echo "${c_yellow}note:${c_reset} $*"; }
die()  { echo "${c_red}FATAL:${c_reset} $*" >&2; exit 1; }

command -v docker >/dev/null || die "docker not found (chạy script này TRONG Codespace)"

# 1. Start or reuse the container.
if docker ps --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  ok "container '$CONTAINER' đang chạy"
elif docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  docker start "$CONTAINER" >/dev/null && ok "khởi động lại container '$CONTAINER'"
else
  docker run -d --name "$CONTAINER" \
    -e POSTGRES_USER="$PG_USER" -e POSTGRES_PASSWORD="$PG_PASS" -e POSTGRES_DB="$PG_DB" \
    -p "${PG_PORT}:5432" "$PG_IMAGE" >/dev/null \
    || die "docker run thất bại"
  ok "tạo container '$CONTAINER' ($PG_IMAGE) trên :$PG_PORT"
fi

# 2. Wait for readiness.
for i in $(seq 1 30); do
  if docker exec "$CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
    ok "postgres sẵn sàng"; break
  fi
  [ "$i" = 30 ] && die "postgres không sẵn sàng sau 30s"
  sleep 1
done

# 3. Apply migrations (idempotent).
for f in 001_init 002_withdraw_request_sequence 003_offchain_settlement; do
  path="$GANC_SYS_DIR/migrations/${f}.sql"
  [ -f "$path" ] || die "thiếu migration $path"
  docker exec -i "$CONTAINER" psql -v ON_ERROR_STOP=1 -U "$PG_USER" -d "$PG_DB" < "$path" >/dev/null \
    && ok "applied $f" || die "migration $f lỗi"
done

# 4. Verify the offchain pending tables exist.
count="$(docker exec "$CONTAINER" psql -tAq -U "$PG_USER" -d "$PG_DB" -c \
  "select count(*) from information_schema.tables where table_name in ('offchain_pending_withdrawals','offchain_pending_deposits','withdraw_requests');" 2>/dev/null | tr -d '[:space:]')"
[ "$count" = "3" ] && ok "schema verified (bảng offchain pending có mặt)" || die "schema check fail (thấy $count/3)"

echo
ok "Postgres READY  →  postgres://${PG_USER}:${PG_PASS}@localhost:${PG_PORT}/${PG_DB}?sslmode=disable"
note "khớp DSN mặc định của ganc-sys — KHÔNG cần set DATABASE_URL"
echo "  Next (Phase 2): bash scripts/real_db_mode_up.sh"
