# scripts/bench — Bộ đo hiệu suất ZKDEX

Tự động hoá toàn bộ quy trình thu thập thông số Phase 2.
Kế hoạch: [`docs/matching_orderbook/BENCH-PERF-evaluation-plan.md`](../../docs/matching_orderbook/BENCH-PERF-evaluation-plan.md) ·
Kết quả: [`BENCH-PERF-results-phase2.md`](../../docs/matching_orderbook/BENCH-PERF-results-phase2.md)

## Chạy nhanh

```bash
bash scripts/bench/run_all.sh                    # đủ 3 mốc 10/100/1000
BENCH_NS="10 100" bash scripts/bench/run_all.sh  # rút gọn
BENCH_SKIP_A=1     bash scripts/bench/run_all.sh # chỉ kịch bản B (rất nhanh)
```

Hoặc từng bước:

```bash
bash scripts/bench/00_env.sh          # pre-flight — hỏng thì DỪNG
bash scripts/bench/40_offchain.sh     # local, không cần chain
bash scripts/bench/10_baseline.sh     # 1 trade → mọi chỉ số "một lần" + chu kỳ/fill
bash scripts/bench/30_rss.sh start
bash scripts/bench/20_scale.sh 10   B # B trước (rẻ)
bash scripts/bench/20_scale.sh 1000 A # A sau (đắt)
bash scripts/bench/30_rss.sh stop
bash scripts/bench/90_report.sh       # CSV → report.md
```

## Kết quả

```
bench-out/<run_id>/
  metrics.csv    ← dữ liệu THÔ dạng long (run,scenario,n,rep,metric,value,unit,note)
  report.md      ← bảng trực quan, dán thẳng vào tài liệu
  summary.csv    ← dạng wide (metric × mốc)
  env.txt        ← spec máy & cấu hình
  rss.csv        ← chuỗi thời gian RAM
  tx_baseline.json · gas_*.txt · pprof_*.txt · e2e_run*.txt
```

## 4 nguyên tắc đã áp

1. **CSV incremental** — mỗi phép đo ghi ngay; chạy vài giờ mà crash cũng không mất số.
2. **Tách thu thập / phân tích** — script chỉ ghi số thô; `90_report.sh` mới tính p50, amortized, TPS. Sai công thức thì chạy lại báo cáo, **không phải đo lại**.
3. **Không bịa số** — thiếu dữ liệu ghi `NA` kèm **lý do** trong cột `note`; báo cáo có hẳn mục "Không đo được".
4. **Tự kiểm tra** — `20_scale.sh` đối chiếu ô đã biết trước:
   `A → drop=0, tx=N` · `B → tx=1, drop≈(N−1)/N`.
   Lệch ⇒ cảnh báo **"dựng sai kịch bản"**, không phải phát hiện mới.

## Hai kịch bản

| | Cách dựng | Kết quả | Chi phí |
|---|---|---|---|
| **A** fill độc lập | N cặp order riêng | N fill settle → **N tx** | **chậm nhất** — `T ≈ N × (tick + prove + commit)` |
| **B** shared-order | N maker + 1 taker quét hết | chỉ fill đầu settle, N−1 **drop trước prove** | **nhanh & rẻ** |

→ `run_all.sh` chạy **B trước A** để có đường cong drop-rate sớm.

## Biến môi trường

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `BENCH_SERVER_LOG` | `/tmp/api.real.log` | log backend (`real_db_mode_up.sh` ghi sẵn) — thiếu thì không tính được drop rate |
| `BENCH_MAKER_KEY` / `BENCH_TAKER_KEY` | `alice` / `bob` | key trong keyring chain, phải có tiền |
| `BENCH_NS` | `10 100 1000` | các mốc N |
| `BENCH_SKIP_A` | `0` | `1` = bỏ kịch bản A |
| `BENCH_PRICE` / `BENCH_QTY` | `100` / `1` | giá & lượng mỗi lệnh |
| `BENCH_WAIT_MAX` | tự tính | trần thời gian chờ drain |
| `CHAIN_HOME` | tự dò | đặt tay nếu dò sai |
| `RUN_ID` | timestamp | ghi tiếp vào run cũ |

Kế thừa từ `real_db_mode_up.sh`: `API_PORT` `GAZK_URL` `CHAIN_NODE` `CHAIN_ID`
`DATABASE_URL` `ORDER_SIG_MODE` `SETTLEMENT_INTERVAL`.

## Chữ ký order — không cần ví

Live mặc định `ORDER_SIG_MODE=adr36`. ADR-036 chỉ là **secp256k1 trên một sign-doc
tất định** — Keplr là *một* cách tạo, không phải điều kiện. Script ký bằng khoá
lấy từ keyring chain qua [`p3/script-test/sign_order_adr036`](../../p3/script-test/sign_order_adr036),
dùng lại `state.ADR036SignBytes` / `AccAddressFromPubKey` / `CanonicalBytes()`
của repo nên **byte-exact với verifier**.

> Ở chế độ `adr36`, `owner` **phải** derive từ pubkey — tài khoản đo phải là key
> thật trong keyring, và **deposit phải vào đúng địa chỉ đó**.
> `mock` thì script tự chuyển sang `sign_order` có sẵn.

## Chuẩn bị bắt buộc

```bash
# 1. Chain chạy (cửa sổ riêng)
# 2. Backend + gazk + DB — log tự vào /tmp/api.real.log, KHÔNG cần redirect:
DETACH=1 RESET_OFFCHAIN_DB=1 bash scripts/real_db_mode_up.sh
# 3. alice & bob phải có số dư OFF-CHAIN (đã deposit) — 00_env.sh cảnh báo nếu chưa.
# 4. Reset sạch TRƯỚC mỗi mốc N (script KHÔNG tự reset để tránh xoá nhầm).
```

> Dùng `DETACH=1` — không có nó, `real_db_mode_up.sh` chạy foreground và `tail -f`
> log, chiếm luôn terminal.

## Không đo được (giới hạn dữ liệu, không phải giới hạn script)

| Chỉ số | Vì sao | Cần |
|---|---|---|
| Constraints (R1CS), prove-time sạch | gazk không expose | role **A** |
| Tách gas verify vs ghi-state | chưa có gas checkpoint trong keeper | role **B** |
| ns/op tuyệt đối per-chặng | repo chưa có `Benchmark*` | thêm file test |

Cả ba đều được ghi `NA` + lý do trong báo cáo — **không bịa số**.
