#!/usr/bin/env bash
# =============================================================================
# run_all.sh — Chạy TOÀN BỘ quy trình đo bằng một lệnh.
# -----------------------------------------------------------------------------
#   bash scripts/bench/run_all.sh                 # đủ 3 mốc 10/100/1000
#   BENCH_NS="10 100" bash scripts/bench/run_all.sh
#   BENCH_SKIP_A=1 bash scripts/bench/run_all.sh  # chỉ kịch bản B (nhanh)
#
# Thứ tự CÓ CHỦ Ý (plan §11):
#   00 env → 40 off-chain (local, rẻ) → 10 baseline → B (rẻ) → A (đắt) → 90 report
#   Chạy B TRƯỚC A: B chỉ settle 1 fill nên rất nhanh, cho đường cong drop-rate
#   sớm; A là ô tốn thời gian nhất (N tx thật).
#
# LƯU Ý: reset sạch TRƯỚC mỗi mốc là việc của bạn (script không tự reset để
# tránh xoá nhầm dữ liệu):
#   RESET_OFFCHAIN_DB=1 bash scripts/real_db_mode_up.sh
# =============================================================================

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
bench_init
B="$BENCH_DIR"

NS="${BENCH_NS:-10 100 1000}"
REPS="${BENCH_SCALE_REPS:-1}"

head1 "BẮT ĐẦU TOÀN BỘ QUY TRÌNH ĐO — run_id=$RUN_ID"
log "mốc N: $NS · rep mỗi mốc: $REPS"
log "kết quả: $OUT_DIR/"

# 1) Pre-flight — hỏng thì dừng ngay, đo trên môi trường hỏng chỉ ra số rác.
bash "$B/00_env.sh" || die "pre-flight thất bại — sửa rồi chạy lại"

# 2) Off-chain (local, không cần chain) — rẻ, làm sớm.
bash "$B/40_offchain.sh" || warn "off-chain lỗi — bỏ qua, tiếp tục"

# 3) Baseline 1 trade — cho chu kỳ/fill để dự toán, và mọi chỉ số 'một lần'.
bash "$B/10_baseline.sh" || die "baseline thất bại — không nên chạy scale khi 1 trade còn chưa settle"

# 4) Lấy mẫu RSS chạy nền suốt phần scale.
bash "$B/30_rss.sh" start

# 5) Scale — B trước (rẻ), rồi A (đắt).
for n in $NS; do
  for r in $(seq 1 "$REPS"); do
    bash "$B/20_scale.sh" "$n" B "$r" || warn "N=$n B rep$r lỗi — tiếp tục"
  done
done

if [ "${BENCH_SKIP_A:-0}" != "1" ]; then
  for n in $NS; do
    for r in $(seq 1 "$REPS"); do
      bash "$B/20_scale.sh" "$n" A "$r" || warn "N=$n A rep$r lỗi — tiếp tục"
    done
  done
else
  warn "BỎ QUA kịch bản A (BENCH_SKIP_A=1)"
fi

# 6) Dừng lấy mẫu + báo cáo.
bash "$B/30_rss.sh" stop
bash "$B/90_report.sh"

head1 "HOÀN TẤT"
ok "Báo cáo: $OUT_DIR/report.md"
