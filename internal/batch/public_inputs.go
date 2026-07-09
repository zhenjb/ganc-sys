package batch

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ErrInvalidPublicInputs là sentinel cho mọi lỗi validation bên trong
// PublicInputBuilder. Callers chain bằng errors.Is. Tách khỏi
// ErrInvalidSettlementInputs / ErrInvalidWitnessInputs để P4/P1/P2 phân
// biệt rõ "settlement sai" vs "commitment sai" vs "binding public input
// sai".
var ErrInvalidPublicInputs = errors.New("batch: invalid public inputs")

// Public input layout LOCKED trong Agreements (mục "Encoding" và mục
// "ZK I/O contract"):
//
//	publicInputs[0] = oldStateRoot
//	publicInputs[1] = newStateRoot
//	publicInputs[2] = depositsRoot
//	publicInputs[3] = withdrawalsRoot
//	publicInputs[4] = nullifiersRoot
//	publicInputs[5] = withdrawOutputsRoot
//
// Thứ tự này phải khớp 1:1 giữa P3 (STATE-10 builder), P2 (circuit
// public input layout), P1 (verifier on-chain derive lại để check) và
// P4 (prover client / relayer client gửi đúng slice).
//
// Đổi thứ tự = break contract → mọi proof đã sinh ra trở thành invalid.
// Khi ZK-02 thêm public input mới (ví dụ accountsRoot), thêm const
// PublicInputIdx mới và bump PublicInputCount.
const (
	PublicInputIdxOldStateRoot        = 0
	PublicInputIdxNewStateRoot        = 1
	PublicInputIdxDepositsRoot        = 2
	PublicInputIdxWithdrawalsRoot     = 3
	PublicInputIdxNullifiersRoot      = 4
	PublicInputIdxWithdrawOutputsRoot = 5

	// PublicInputCount là tổng số public input mà circuit / verifier
	// MVP expect. Mọi caller (P1, P2, P4) đối chiếu len(slice) với
	// const này khi nhận proofBundle.
	PublicInputCount = 6
)

// publicInputLabels gắn nhãn cho từng vị trí — dùng cho error message
// và cho metadata trong test vector public_inputs.json. Thứ tự PHẢI
// khớp với PublicInputIdx* constants ở trên.
var publicInputLabels = [PublicInputCount]string{
	PublicInputIdxOldStateRoot:        "oldStateRoot",
	PublicInputIdxNewStateRoot:        "newStateRoot",
	PublicInputIdxDepositsRoot:        "depositsRoot",
	PublicInputIdxWithdrawalsRoot:     "withdrawalsRoot",
	PublicInputIdxNullifiersRoot:      "nullifiersRoot",
	PublicInputIdxWithdrawOutputsRoot: "withdrawOutputsRoot",
}

// PublicInputLabels expose copy danh sách label (định danh từng vị trí
// public input) cho debug log / JSON metadata. Trả về SLICE MỚI để
// caller không thể mutate global state.
func PublicInputLabels() []string {
	out := make([]string, PublicInputCount)
	copy(out, publicInputLabels[:])
	return out
}

// PublicInputBuilder convert một (SettlementUpdate, BatchCommitments)
// đã được STATE-08/extension validate sang slice public input theo thứ
// tự chốt trong Agreements.
//
// Builder là pure (stateless) — chọn cấu trúc struct + method để giữ
// đồng nhất với SettlementUpdateBuilder / WitnessBuilder và để mở đường
// cho config injection trong tương lai (ZK-02 có thể chuyển hex →
// little-endian field element representation).
//
// Caller stateless có thể dùng helper top-level BuildPublicInputs.
type PublicInputBuilder struct{}

// NewPublicInputBuilder khởi tạo builder mới. Không state — caller có
// thể tái sử dụng cùng một instance cho mọi batch hoặc tạo mới mỗi lần.
func NewPublicInputBuilder() *PublicInputBuilder {
	return &PublicInputBuilder{}
}

// Build assemble public input slice cho ProofBundle.PublicInputs.
//
// Validation pipeline (mọi failure → ErrInvalidPublicInputs wrap, không
// trả về partial slice):
//
//  1. SettlementUpdate.OldStateRoot và NewStateRoot non-empty, hex
//     prefixed (0x...), strictly khác nhau. Đây là defense-in-depth:
//     STATE-08 đã reject no-op batch, nhưng caller có thể đẩy
//     SettlementUpdate giả mạo (không qua builder) vào hàm này.
//  2. Cả 4 commitment root: non-empty, hex prefixed.
//
// Post-conditions on success:
//   - Slice length đúng bằng PublicInputCount (6).
//   - Thứ tự khớp PublicInputIdx* constants.
//   - Mọi phần tử là hex-prefixed string verbatim từ input — KHÔNG
//     normalize/hash thêm. Verifier P1 cũng đọc raw hex string này.
func (b *PublicInputBuilder) Build(upd types.SettlementUpdate, com types.BatchCommitments) ([]string, error) {
	if err := validateHex(upd.OldStateRoot, "settlementUpdate.oldStateRoot"); err != nil {
		return nil, wrapPublicInputErr(err)
	}
	if err := validateHex(upd.NewStateRoot, "settlementUpdate.newStateRoot"); err != nil {
		return nil, wrapPublicInputErr(err)
	}
	if upd.OldStateRoot == upd.NewStateRoot {
		return nil, fmt.Errorf("%w: oldStateRoot == newStateRoot (no-op batch)", ErrInvalidPublicInputs)
	}
	if err := validateHex(com.DepositsRoot, "batchCommitments.depositsRoot"); err != nil {
		return nil, wrapPublicInputErr(err)
	}
	if err := validateHex(com.WithdrawalsRoot, "batchCommitments.withdrawalsRoot"); err != nil {
		return nil, wrapPublicInputErr(err)
	}
	if err := validateHex(com.NullifiersRoot, "batchCommitments.nullifiersRoot"); err != nil {
		return nil, wrapPublicInputErr(err)
	}
	if err := validateHex(com.WithdrawOutputsRoot, "batchCommitments.withdrawOutputsRoot"); err != nil {
		return nil, wrapPublicInputErr(err)
	}

	out := make([]string, PublicInputCount)
	out[PublicInputIdxOldStateRoot] = upd.OldStateRoot
	out[PublicInputIdxNewStateRoot] = upd.NewStateRoot
	out[PublicInputIdxDepositsRoot] = com.DepositsRoot
	out[PublicInputIdxWithdrawalsRoot] = com.WithdrawalsRoot
	out[PublicInputIdxNullifiersRoot] = com.NullifiersRoot
	out[PublicInputIdxWithdrawOutputsRoot] = com.WithdrawOutputsRoot
	return out, nil
}

// BuildPublicInputs là helper top-level cho caller stateless (script
// gen vector, test, P2 prover client). Tương đương
// NewPublicInputBuilder().Build(upd, com).
func BuildPublicInputs(upd types.SettlementUpdate, com types.BatchCommitments) ([]string, error) {
	return (&PublicInputBuilder{}).Build(upd, com)
}

// ---------------------------------------------------------------------------
// STATE-T08 — extended (trade) public-input layout.
//
// The trade circuit APPENDS two inputs after the locked core [0..5]:
//
//	publicInputs[6] = tradesRoot
//	publicInputs[7] = ordersRoot
//
// This is additive: the existing 6-input Build / PublicInputCount for the core
// circuit (gazk v1) is UNCHANGED, so core proofs keep verifying. A batch with no
// trades uses the empty-root sentinels (state.EmptyTradesRoot/EmptyOrdersRoot)
// for [6]/[7] so a core payload still fits the fixed 8-input layout expected by
// the trade circuit (ZK append-not-reorder agreement).
// ---------------------------------------------------------------------------

const (
	PublicInputIdxTradesRoot = 6
	PublicInputIdxOrdersRoot = 7

	// PublicInputCountWithTrades is the length of the extended vector used by
	// the trade circuit. The core circuit still uses PublicInputCount (6).
	PublicInputCountWithTrades = 8
)

// PublicInputLabelsWithTrades returns the 8 labels for the extended layout.
func PublicInputLabelsWithTrades() []string {
	out := make([]string, 0, PublicInputCountWithTrades)
	out = append(out, PublicInputLabels()...)
	out = append(out, "tradesRoot", "ordersRoot")
	return out
}

// BuildPublicInputsWithTrades assembles the extended 8-input vector: the core
// [0..5] via Build, then [6]=tradesRoot, [7]=ordersRoot. Empty trade roots are
// replaced by the empty sentinels so a core (no-trade) batch produces a valid
// 8-input vector. Verbatim hex — no re-hashing.
func BuildPublicInputsWithTrades(upd types.SettlementUpdate, com types.BatchCommitments) ([]string, error) {
	core, err := (&PublicInputBuilder{}).Build(upd, com)
	if err != nil {
		return nil, err
	}

	tradesRoot := com.TradesRoot
	if strings.TrimSpace(tradesRoot) == "" {
		tradesRoot = state.EmptyTradesRoot()
	}
	ordersRoot := com.OrdersRoot
	if strings.TrimSpace(ordersRoot) == "" {
		ordersRoot = state.EmptyOrdersRoot()
	}
	if err := validateHex(tradesRoot, "batchCommitments.tradesRoot"); err != nil {
		return nil, wrapPublicInputErr(err)
	}
	if err := validateHex(ordersRoot, "batchCommitments.ordersRoot"); err != nil {
		return nil, wrapPublicInputErr(err)
	}

	out := make([]string, PublicInputCountWithTrades)
	copy(out, core)
	out[PublicInputIdxTradesRoot] = tradesRoot
	out[PublicInputIdxOrdersRoot] = ordersRoot
	return out, nil
}

// wrapPublicInputErr re-wrap lỗi từ validateHex (vốn đính
// ErrInvalidSettlementInputs) sang ErrInvalidPublicInputs để caller có
// thể errors.Is đúng sentinel cho STATE-10. Giữ message gốc làm chain
// cause để debug truy nguồn.
func wrapPublicInputErr(cause error) error {
	return fmt.Errorf("%w: %s", ErrInvalidPublicInputs, cause.Error())
}
