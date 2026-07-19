package batch

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ErrInvalidSettlementInputs là sentinel cho mọi lỗi validation bên
// trong SettlementUpdateBuilder. Callers chain bằng errors.Is. Một
// sentinel duy nhất giúp P4 (khi map qua /api/batch/build) chỉ cần một
// nhánh xử lý → trả HTTP 400 với message gốc.
var ErrInvalidSettlementInputs = errors.New("batch: invalid settlement inputs")

// WithdrawalInput gói một WithdrawRequest cùng với hai trường derived bắt
// buộc cho batch-shaped settlement:
//
//   - Nullifier: do state.NullifierFor(secret, request.Nonce) sinh ra
//     ở STATE-06. Phải truyền lại ở đây để builder ghi vào withdrawal
//     entry và để re-derive của witness builder bind đúng.
//   - DestinationHash: do state.WithdrawAddressHash(request.Destination)
//     sinh ra ở STATE-07. Builder sẽ re-derive lại từ request.Destination
//     và reject nếu mismatch — defense-in-depth chống tampering.
type WithdrawalInput struct {
	Request         types.WithdrawRequest
	Nullifier       string
	DestinationHash string
}

// SettlementInputs là batch-shaped input của SettlementUpdateBuilder.
// Theo Agreements, schema không bao giờ rớt về scalar — kể cả khi
// canonical Alice vector chỉ có 1 deposit + 1 withdrawal, mảng vẫn giữ
// nguyên dạng slice để contract đúng từ ngày đầu.
//
//   - OldStateRoot: LocalState.Root() trước khi apply mọi withdrawal
//     trong batch.
//   - NewStateRoot: LocalState.Root() sau khi apply toàn bộ batch.
//   - Deposits:     các DepositRecord (STATE-03) tham gia batch. Owner/
//     Denom/Amount phải khớp với những gì STATE-03 dùng
//     để credit LocalState.
//   - Withdrawals:  các WithdrawRequest (STATE-04) tham gia batch,
//     kèm Nullifier (STATE-06) và DestinationHash (STATE-07).
type SettlementInputs struct {
	OldStateRoot string
	NewStateRoot string
	Deposits     []types.DepositRecord
	Withdrawals  []WithdrawalInput
}

// SettlementUpdateBuilder sản xuất SettlementUpdate deterministic,
// đánh số tuần tự — đây là STATE-08 của pipeline P3. Builder là nơi DUY
// NHẤT mint BatchID; mọi giá trị khác (roots, ids, amounts, hashes,
// destination) do caller cung cấp sau khi STATE-03..07 đã tính.
//
// Concurrency: Build thread-safe; chỉ có seq counter mutable, được bảo
// vệ bằng mu. Builder không giữ tham chiếu tới LocalState.
type SettlementUpdateBuilder struct {
	mu  sync.Mutex
	seq uint64
	// prefix là namespace của BatchID ("batch-", "core-", "trade-"). INT-2SEQ:
	// đường core và đường trade dùng hai builder độc lập nhưng submit vào CÙNG
	// một namespace batchId trên chain — nếu cả hai đều phát "batch-N" thì trùng
	// (chain reject "batchId already exists"). Mỗi đường mang một prefix riêng để
	// hai chuỗi id tách biệt. Rỗng => DefaultBatchIDPrefix (backward-compat cho
	// builder cũ / zero-value).
	prefix string
}

// DefaultBatchIDPrefix là namespace mặc định khi builder không được cấp prefix
// riêng — giữ nguyên hành vi trước INT-2SEQ ("batch-N") cho LocalBuilder và mọi
// caller cũ/test hiện có.
const DefaultBatchIDPrefix = "batch-"

func NewSettlementUpdateBuilder() *SettlementUpdateBuilder {
	return &SettlementUpdateBuilder{prefix: DefaultBatchIDPrefix}
}

// NewSettlementUpdateBuilderWithPrefix tạo builder mint BatchID dưới namespace
// `prefix` (vd "core-", "trade-"). Prefix rỗng rơi về DefaultBatchIDPrefix.
// INT-2SEQ: core (SnapshotBuilder) dùng "core-", trade (RealOrderService) dùng
// "trade-" để hai đường không đụng batchId trên chain.
func NewSettlementUpdateBuilderWithPrefix(prefix string) *SettlementUpdateBuilder {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = DefaultBatchIDPrefix
	}
	return &SettlementUpdateBuilder{prefix: prefix}
}

// batchIDPrefix trả về prefix đang dùng, fallback DefaultBatchIDPrefix cho một
// builder zero-value (được construct qua struct literal, không qua constructor).
func (b *SettlementUpdateBuilder) batchIDPrefix() string {
	if b.prefix == "" {
		return DefaultBatchIDPrefix
	}
	return b.prefix
}

// Seq trả về số lượng SettlementUpdate đã build (debug/testing).
func (b *SettlementUpdateBuilder) Seq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Build validate SettlementInputs và assemble một SettlementUpdate
// canonical.
//
// Validation pipeline (mọi failure → ErrInvalidSettlementInputs wrap
// với cause; không partial mutation):
//
//  1. Roots non-empty, hex-prefixed (0x...), strictly khác nhau.
//  2. Có ÍT NHẤT 1 deposit hoặc 1 withdrawal (batch rỗng bị reject).
//  3. Mỗi deposit: identity fields non-empty, Amount > 0.
//  4. Mỗi withdrawal: identity fields non-empty, Amount > 0, Nonce >= 0;
//     Nullifier và DestinationHash non-empty + hex-prefixed.
//  5. Single-denom invariant: mọi deposit & withdrawal cùng denom
//     (MVP — circuit ZK-04 được viết cho shape này).
//  6. Re-derive DestinationHash từ Withdraw.Destination qua
//     state.WithdrawAddressHash và assert equality với supplied hash.
//
// Post-conditions on success:
//   - BatchID = "<prefix>N" với N là post-increment seq counter (prefix mặc
//     định "batch-"; core dùng "core-", trade dùng "trade-" — INT-2SEQ).
//   - Mọi amount normalize qua big.Int ("0100" → "100").
//   - Owner/denom/destination giữ verbatim (đã trim ở STATE-03/04).
func (b *SettlementUpdateBuilder) Build(in SettlementInputs) (types.SettlementUpdate, error) {
	if err := validateRoot(in.OldStateRoot, "oldStateRoot"); err != nil {
		return types.SettlementUpdate{}, err
	}
	if err := validateRoot(in.NewStateRoot, "newStateRoot"); err != nil {
		return types.SettlementUpdate{}, err
	}
	if in.OldStateRoot == in.NewStateRoot {
		return types.SettlementUpdate{}, fmt.Errorf("%w: oldStateRoot == newStateRoot (no-op batch)", ErrInvalidSettlementInputs)
	}

	if len(in.Deposits) == 0 && len(in.Withdrawals) == 0 {
		return types.SettlementUpdate{}, fmt.Errorf("%w: empty batch (no deposits, no withdrawals)", ErrInvalidSettlementInputs)
	}

	// INT-MULTIDENOM: một batch KHÔNG còn bị ép single-denom. Nó gom deposit/
	// withdraw đa denom — circuit gazk-trade-v1 bind mỗi state cell một denom
	// riêng và chain validate từng op độc lập (không đòi đồng denom). Mỗi op vẫn
	// tự kiểm denom non-empty + amount > 0 qua validateDeposit/validateWithdraw.
	deposits := make([]types.SettlementDeposit, 0, len(in.Deposits))
	for i, d := range in.Deposits {
		amt, err := validateDeposit(d)
		if err != nil {
			return types.SettlementUpdate{}, fmt.Errorf("%w (deposits[%d])", err, i)
		}
		deposits = append(deposits, types.SettlementDeposit{
			DepositID: d.DepositID,
			Owner:     d.Owner,
			Denom:     d.Denom,
			Amount:    amt.String(),
		})
	}

	withdrawals := make([]types.SettlementWithdrawal, 0, len(in.Withdrawals))
	for i, w := range in.Withdrawals {
		amt, err := validateWithdraw(w.Request)
		if err != nil {
			return types.SettlementUpdate{}, fmt.Errorf("%w (withdrawals[%d])", err, i)
		}
		if err := validateHex(w.Nullifier, fmt.Sprintf("withdrawals[%d].nullifier", i)); err != nil {
			return types.SettlementUpdate{}, err
		}
		if err := validateHex(w.DestinationHash, fmt.Sprintf("withdrawals[%d].destinationHash", i)); err != nil {
			return types.SettlementUpdate{}, err
		}
		rederived, err := state.WithdrawAddressHash(w.Request.Destination)
		if err != nil {
			return types.SettlementUpdate{}, fmt.Errorf(
				"%w: withdrawals[%d] cannot re-derive destinationHash from destination %q: %v",
				ErrInvalidSettlementInputs, i, w.Request.Destination, err,
			)
		}
		if rederived != w.DestinationHash {
			return types.SettlementUpdate{}, fmt.Errorf(
				"%w: withdrawals[%d].destinationHash mismatch: supplied=%s, re-derived(destination=%q)=%s",
				ErrInvalidSettlementInputs, i, w.DestinationHash, w.Request.Destination, rederived,
			)
		}
		withdrawals = append(withdrawals, types.SettlementWithdrawal{
			WithdrawID:      w.Request.WithdrawID,
			Owner:           w.Request.Owner,
			Denom:           w.Request.Denom,
			Amount:          amt.String(),
			Destination:     w.Request.Destination,
			DestinationHash: w.DestinationHash,
			Nullifier:       w.Nullifier,
		})
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++

	return types.SettlementUpdate{
		BatchID:      b.batchIDPrefix() + strconv.FormatUint(b.seq, 10),
		OldStateRoot: in.OldStateRoot,
		NewStateRoot: in.NewStateRoot,
		Deposits:     deposits,
		Withdrawals:  withdrawals,
	}, nil
}

func validateRoot(root, label string) error {
	r := strings.TrimSpace(root)
	if r == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidSettlementInputs, label)
	}
	if !hash.IsHexPrefixed(r) {
		return fmt.Errorf("%w: %s %q missing 0x prefix", ErrInvalidSettlementInputs, label, r)
	}
	if len(hash.StripHex(r)) == 0 {
		return fmt.Errorf("%w: %s %q is empty after stripping 0x prefix", ErrInvalidSettlementInputs, label, r)
	}
	return nil
}

func validateHex(value, label string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidSettlementInputs, label)
	}
	if !hash.IsHexPrefixed(v) {
		return fmt.Errorf("%w: %s %q missing 0x prefix", ErrInvalidSettlementInputs, label, v)
	}
	if len(hash.StripHex(v)) == 0 {
		return fmt.Errorf("%w: %s %q is empty after stripping 0x prefix", ErrInvalidSettlementInputs, label, v)
	}
	return nil
}

func validateDeposit(d types.DepositRecord) (*big.Int, error) {
	if strings.TrimSpace(d.DepositID) == "" {
		return nil, fmt.Errorf("%w: deposit.depositId is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(d.Owner) == "" {
		return nil, fmt.Errorf("%w: deposit.owner is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(d.Denom) == "" {
		return nil, fmt.Errorf("%w: deposit.denom is empty", ErrInvalidSettlementInputs)
	}
	amt, err := parsePositive(d.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: deposit.amount %q invalid: %v", ErrInvalidSettlementInputs, d.Amount, err)
	}
	return amt, nil
}

func validateWithdraw(w types.WithdrawRequest) (*big.Int, error) {
	if strings.TrimSpace(w.WithdrawID) == "" {
		return nil, fmt.Errorf("%w: withdraw.withdrawId is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(w.Owner) == "" {
		return nil, fmt.Errorf("%w: withdraw.owner is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(w.Denom) == "" {
		return nil, fmt.Errorf("%w: withdraw.denom is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(w.Destination) == "" {
		return nil, fmt.Errorf("%w: withdraw.destination is empty", ErrInvalidSettlementInputs)
	}
	amt, err := parsePositive(w.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: withdraw.amount %q invalid: %v", ErrInvalidSettlementInputs, w.Amount, err)
	}
	if _, err := parseNonNegative(w.Nonce); err != nil {
		return nil, fmt.Errorf("%w: withdraw.nonce %q invalid: %v", ErrInvalidSettlementInputs, w.Nonce, err)
	}
	return amt, nil
}

func parsePositive(amount string) (*big.Int, error) {
	v, err := parseNonNegative(amount)
	if err != nil {
		return nil, err
	}
	if v.Sign() == 0 {
		return nil, errors.New("must be > 0")
	}
	return v, nil
}

func parseNonNegative(amount string) (*big.Int, error) {
	s := strings.TrimSpace(amount)
	if s == "" {
		return nil, errors.New("empty")
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, errors.New("not a base-10 integer")
	}
	if v.Sign() < 0 {
		return nil, errors.New("negative")
	}
	return v, nil
}
