package batch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ErrInsufficientOffchainBalance là sentinel public của batch facade khi
// một WithdrawRequest trong BuildInput không có đủ balance off-chain để
// debit. P4 BatchService (INT-07) chain bằng errors.Is để map sang HTTP
// 400 với message "insufficient off-chain balance", KHÔNG leak internal
// state.* sentinels ra HTTP layer.
//
// Đây là phép map duy nhất từ state.ErrInsufficientBalance sang public
// surface — mọi lỗi insufficient-balance khác bên trong state package
// vẫn giữ nguyên cho caller nội bộ.
var ErrInsufficientOffchainBalance = errors.New("batch: insufficient off-chain balance")

// ErrInvalidBuildInput là sentinel cho mọi lỗi validation cấu trúc bên
// trong batch facade (root rỗng, owner trống, duplicate secret). Tách
// khỏi ErrInsufficientOffchainBalance để P4 biết phân biệt
// "user gửi sai" vs "user không đủ tiền".
var ErrInvalidBuildInput = errors.New("batch: invalid build input")

// INT-WD-NULLIFIER-peruser: khi P4 không truyền AccountSecrets cho một owner,
// fallback dùng state.WithdrawSecretForOwner(owner) — secret MOCK per-owner —
// thay cho literal "mock-user-secret" DÙNG CHUNG. Literal chung khiến hai owner
// khác nhau đụng cùng nullifier khi nonce trùng (sau reset lockstep). Per-owner
// cho mỗi owner một namespace chống-replay riêng, và vì determin theo owner nên
// batch rebuild + gazk prover re-derive ra đúng cùng giá trị từ witness
// UserSecret. Đây cũng là DE-MOCK SEAM: bước sau đổi nguồn secret sang
// wallet-derived (ADR-036) mà không đụng NullifierFor hay prover.

// AccountSecret gắn một owner với userSecret. P4 BatchService nạp
// secret từ keystore/wallet ngay tại thời điểm batch build — secret
// KHÔNG bao giờ rời khỏi máy P3 và KHÔNG được phép log/leak.
//
// AccountSecrets là OPTIONAL trong BuildInput: owner không có entry sẽ
// fallback về literal MVP-mock secret (xem mvpMockSecret).
// Khi P5/keystore sẵn sàng, P4 sẽ inject AccountSecrets thật và facade
// dùng nguyên đó (không pha trộn với mock).
type AccountSecret struct {
	Owner      string
	UserSecret string
}

// BuildInput là batch-shaped input P4 BatchService (INT-07) đẩy vào
// facade. Schema không bao giờ rớt về scalar — kể cả khi mock chỉ có 1
// deposit + 1 withdrawal, các mảng vẫn là slice.
//
//   - OldStateRoot:     root mà caller (P4) đọc từ chain
//     (QueryCurrentStateRoot) hoặc đặt làm placeholder
//     trong MVP. Builder KHÔNG tự reconcile với fresh
//     LocalState root — đó là việc của P1 verifier khi
//     kiểm public input. Builder chỉ echo lại
//     OldStateRoot vào output SettlementUpdate.
//   - Deposits:         các DepositRecord chain đã có
//     (processed=false) và P4 đã resolve qua depositId.
//   - WithdrawRequests: các WithdrawRequest đã được STATE-04 build trước
//     đó (nonce/withdrawId đã gán đúng).
//   - AccountSecrets:   OPTIONAL. Secret của từng owner. Owner không có
//     entry → fallback literal mvpMockSecret.
type BuildInput struct {
	OldStateRoot     string
	Deposits         []types.DepositRecord
	WithdrawRequests []types.WithdrawRequest
	AccountSecrets   []AccountSecret
}

// BuildOutput đóng gói toàn bộ artifact /api/batch/build trả về theo
// Agreements. Field name BatchCommitments khớp với
// types.BuildBatchResponse.BatchCommitments để P4 có thể relay 1:1
// xuống FE. Cả ba field được build atomically — partial output không
// bao giờ trả về.
type BuildOutput struct {
	SettlementUpdate types.SettlementUpdate
	BatchCommitments types.BatchCommitments
	Witness          types.Witness
}

// Builder là ranh giới Go interface giữa P3 primitives và P4 wiring.
// P4 BatchService phụ thuộc Builder (không phải LocalBuilder cụ thể) để
// dễ swap sang remote/RPC builder trong tương lai và để mock trong test.
//
// Build nhận context.Context để P4 có thể propagate request cancellation;
// LocalBuilder hiện chưa block trên I/O nhưng vẫn tôn trọng ctx.Err()
// tại các điểm dừng để chuẩn bị cho STATE-14 (persistent manager) sẽ có
// disk/network access.
type Builder interface {
	Build(ctx context.Context, in BuildInput) (BuildOutput, error)
}

// LocalBuilder là implementation in-process: orchestrate STATE-03..09
// trên một LocalState FRESH cho mỗi Build call. Đây là "fresh state per
// batch" shortcut được STATE-13 chấp nhận; STATE-14 (P2 priority) sẽ
// thay bằng OffchainStateManager persistent.
//
// Concurrency: mỗi Build tạo LocalState riêng → an toàn cho concurrent
// calls. Trạng thái mutable duy nhất nằm trong settlement builder
// (seq counter) đã được mutex bảo vệ sẵn.
type LocalBuilder struct {
	settlement *SettlementUpdateBuilder
	witness    *WitnessBuilder
}

// NewLocalBuilder khởi tạo LocalBuilder với một SettlementUpdateBuilder
// và WitnessBuilder mới. Trong một process, P4 nên giữ một LocalBuilder
// xuyên suốt để BatchID seq tiếp tục đếm — tạo nhiều instance sẽ reset
// counter và có thể clash batch-1. BatchID mang namespace mặc định "batch-".
func NewLocalBuilder() *LocalBuilder {
	return NewLocalBuilderWithPrefix(DefaultBatchIDPrefix)
}

// NewLocalBuilderWithPrefix khởi tạo LocalBuilder mint BatchID dưới namespace
// `prefix`. INT-2SEQ: đường core settle (deposit/withdraw) dùng LocalBuilder khi
// BATCH_BUILDER_MODE=local — cấu hình mặc định của live/DB mode — nên nó phải
// mang prefix "core-" (giống SnapshotBuilder) để không đụng namespace "trade-"
// của đường trade. Prefix rỗng rơi về DefaultBatchIDPrefix.
func NewLocalBuilderWithPrefix(prefix string) *LocalBuilder {
	return &LocalBuilder{
		settlement: NewSettlementUpdateBuilderWithPrefix(prefix),
		witness:    NewWitnessBuilder(),
	}
}

// participant tracks owner+denom (đã trim) tham gia batch theo
// insertion order — needed để build witness account list ổn định.
type participant struct {
	owner string
	denom string
}

// Build thực thi STATE-03..09 + commitments theo đúng thứ tự pipeline:
//
//  1. Honor ctx.Err() — nếu caller đã huỷ request, bail out ngay.
//  2. Validate OldStateRoot non-empty + 0x-prefixed (echo-only field).
//  3. Index AccountSecrets (duplicate-free). Owner không có entry sẽ
//     dùng literal mvpMockSecret.
//  4. Collect participants (owner+denom) deposits-first/withdrawals-next.
//  5. Tạo fresh LocalState. KHÔNG so sánh in.OldStateRoot với root fresh
//     — đó là echo metadata, validator P1 sẽ kiểm public input về sau.
//  6. ApplyDeposit (STATE-03) tuần tự; ID trùng → reject.
//  7. Với từng WithdrawRequest:
//     a. Derive nullifier qua state.NullifierFor (STATE-06).
//     b. Derive destinationHash qua state.WithdrawAddressHash (STATE-07).
//     c. ApplyWithdrawal (STATE-05); map state.ErrInsufficientBalance →
//     ErrInsufficientOffchainBalance.
//  8. newStateRoot = ls.Root() sau toàn bộ batch.
//  9. Build SettlementUpdate qua SettlementUpdateBuilder (STATE-08).
//  10. Snapshot balance cho từng participant từ LocalState; oldBalance = 0
//     (state fresh), newBalance = ls.Account(owner, denom).Balance.
//  11. Build Witness qua WitnessBuilder (STATE-09).
//  12. BuildCommitments(upd) → 4 root buộc public inputs.
//
// Failure semantics:
//   - ctx cancelled trước khi mutation → context.Canceled trả thẳng.
//   - Lỗi structural validation → ErrInvalidBuildInput wrap.
//   - Lỗi balance < amount khi withdraw → ErrInsufficientOffchainBalance.
//   - Lỗi từ STATE-08/09 (root mismatch, ZK invariant) → propagate
//     ErrInvalidSettlementInputs / ErrInvalidWitnessInputs.
//   - LocalState là throwaway nên không có partial side-effect ở caller.
func (b *LocalBuilder) Build(ctx context.Context, in BuildInput) (BuildOutput, error) {
	if err := ctx.Err(); err != nil {
		return BuildOutput{}, err
	}

	if err := validateRoot(in.OldStateRoot, "oldStateRoot"); err != nil {
		return BuildOutput{}, err
	}

	secrets, err := indexSecrets(in.AccountSecrets)
	if err != nil {
		return BuildOutput{}, err
	}

	parts, err := collectParticipants(in.Deposits, in.WithdrawRequests)
	if err != nil {
		return BuildOutput{}, err
	}

	ls := state.NewLocalState()

	for i, d := range in.Deposits {
		if _, err := ls.ApplyDeposit(d); err != nil {
			return BuildOutput{}, fmt.Errorf("%w: deposits[%d] depositId=%q: %v",
				ErrInvalidBuildInput, i, d.DepositID, err)
		}
	}

	withdrawals := make([]WithdrawalInput, 0, len(in.WithdrawRequests))
	for i, req := range in.WithdrawRequests {
		owner := strings.TrimSpace(req.Owner)
		secret := resolveSecret(secrets, owner)

		nullifier, err := state.NullifierFor(secret, req.Nonce)
		if err != nil {
			return BuildOutput{}, fmt.Errorf("%w: withdrawRequests[%d] withdrawId=%q nullifier derivation: %v",
				ErrInvalidBuildInput, i, req.WithdrawID, err)
		}
		destHash, err := state.WithdrawAddressHash(req.Destination)
		if err != nil {
			return BuildOutput{}, fmt.Errorf("%w: withdrawRequests[%d] withdrawId=%q destinationHash derivation: %v",
				ErrInvalidBuildInput, i, req.WithdrawID, err)
		}

		if _, err := ls.ApplyWithdrawal(req, nullifier); err != nil {
			if errors.Is(err, state.ErrInsufficientBalance) {
				return BuildOutput{}, fmt.Errorf(
					"%w: withdrawRequests[%d] withdrawId=%q owner=%q amount=%s: %v",
					ErrInsufficientOffchainBalance, i, req.WithdrawID, req.Owner, req.Amount, err,
				)
			}
			return BuildOutput{}, fmt.Errorf("%w: withdrawRequests[%d] withdrawId=%q apply: %v",
				ErrInvalidBuildInput, i, req.WithdrawID, err)
		}

		withdrawals = append(withdrawals, WithdrawalInput{
			Request:         req,
			Nullifier:       nullifier,
			DestinationHash: destHash,
		})
	}

	newRoot := ls.Root()

	sin := SettlementInputs{
		OldStateRoot: in.OldStateRoot,
		NewStateRoot: newRoot,
		Deposits:     append([]types.DepositRecord(nil), in.Deposits...),
		Withdrawals:  withdrawals,
	}
	upd, err := b.settlement.Build(sin)
	if err != nil {
		return BuildOutput{}, err
	}

	accounts := make([]AccountWitnessSecret, 0, len(parts))
	for _, p := range parts {
		acc := ls.Account(p.owner, p.denom)
		accounts = append(accounts, AccountWitnessSecret{
			Owner:      p.owner,
			UserSecret: resolveSecret(secrets, p.owner),
			OldBalance: "0",
			NewBalance: acc.Balance,
			Denom:      p.denom,
		})
	}

	win := WitnessInputs{
		Settlement: sin,
		Accounts:   accounts,
	}
	wit, err := b.witness.Build(win)
	if err != nil {
		return BuildOutput{}, err
	}

	com := BuildCommitments(upd)

	return BuildOutput{
		SettlementUpdate: upd,
		BatchCommitments: com,
		Witness:          wit,
	}, nil
}

// Seq trả về settlement seq counter hiện tại — phục vụ debug/testing
// (BatchID = "batch-<seq>"). KHÔNG dùng để mint batch id thủ công.
func (b *LocalBuilder) Seq() uint64 {
	return b.settlement.Seq()
}

// indexSecrets build map owner → userSecret, validate non-empty và
// duplicate-free. Duplicate owner → reject ngay vì có thể là dấu hiệu
// caller copy-paste sai key wallet.
//
// AccountSecrets có thể nil/empty: trả về map rỗng và resolveSecret sẽ
// fallback sang MVP-mock secret.
func indexSecrets(in []AccountSecret) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for i, s := range in {
		owner := strings.TrimSpace(s.Owner)
		secret := strings.TrimSpace(s.UserSecret)
		if owner == "" {
			return nil, fmt.Errorf("%w: accountSecrets[%d].owner is empty", ErrInvalidBuildInput, i)
		}
		if secret == "" {
			return nil, fmt.Errorf("%w: accountSecrets[%d].userSecret is empty for owner %q",
				ErrInvalidBuildInput, i, owner)
		}
		if _, exists := out[owner]; exists {
			return nil, fmt.Errorf("%w: accountSecrets[%d] duplicate owner %q",
				ErrInvalidBuildInput, i, owner)
		}
		out[owner] = secret
	}
	return out, nil
}

// resolveSecret trả về secret cho owner. Nếu caller có truyền
// AccountSecret, dùng giá trị đó; nếu không, fallback sang secret MOCK
// per-owner state.WithdrawSecretForOwner(owner). Determinism: cùng owner luôn
// cùng secret trong cùng chế độ (real vs mock) — request-time, batch rebuild và
// prover re-derive khớp nhau.
func resolveSecret(provided map[string]string, owner string) string {
	if s, ok := provided[owner]; ok {
		return s
	}
	return state.WithdrawSecretForOwner(owner)
}

// collectParticipants trả về danh sách (owner, denom) duy nhất tham
// gia batch, theo first-seen order: deposits trước, withdrawals sau.
// Đây cũng là thứ tự WitnessAccount xuất hiện trong output — quan
// trọng cho determinism của vector test.
//
// Trim owner/denom trước khi so sánh để khớp với normalization của
// AccountState (newAccountKey cũng trim).
func collectParticipants(deps []types.DepositRecord, reqs []types.WithdrawRequest) ([]participant, error) {
	seen := make(map[string]struct{})
	out := make([]participant, 0)

	add := func(rawOwner, rawDenom, ctx string, idx int) error {
		owner := strings.TrimSpace(rawOwner)
		denom := strings.TrimSpace(rawDenom)
		if owner == "" {
			return fmt.Errorf("%w: %s[%d].owner is empty", ErrInvalidBuildInput, ctx, idx)
		}
		if denom == "" {
			return fmt.Errorf("%w: %s[%d].denom is empty", ErrInvalidBuildInput, ctx, idx)
		}
		if _, ok := seen[owner]; ok {
			return nil
		}
		seen[owner] = struct{}{}
		out = append(out, participant{owner: owner, denom: denom})
		return nil
	}

	for i, d := range deps {
		if err := add(d.Owner, d.Denom, "deposits", i); err != nil {
			return nil, err
		}
	}
	for i, r := range reqs {
		if err := add(r.Owner, r.Denom, "withdrawRequests", i); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// compile-time assertion: LocalBuilder satisfies Builder.
var _ Builder = (*LocalBuilder)(nil)
