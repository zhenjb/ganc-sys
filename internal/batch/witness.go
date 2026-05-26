package batch

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ErrInvalidWitnessInputs là sentinel cho mọi lỗi validation bên trong
// WitnessBuilder. Callers chain bằng errors.Is.
var ErrInvalidWitnessInputs = errors.New("batch: invalid witness inputs")

// AccountWitnessSecret là phần secret + balance snapshot mà caller
// (P3 orchestrator) cung cấp cho mỗi account tham gia batch. Mọi tham
// chiếu tới deposit/withdraw amount của account đó sẽ được builder TỰ
// derive từ SettlementInputs (đã được STATE-08 validate), không cho
// caller truyền lại để tránh drift.
//
//   - Owner:      bech32 address (hoặc opaque id) của account. PHẢI trùng
//                 với owner xuất hiện ở Deposit/Withdraw trong settlement.
//   - UserSecret: opaque bytes ví/P3 sở hữu. Bind vào nullifier qua
//                 state.NullifierFor(secret, nonce). KHÔNG bao giờ
//                 escape ra ngoài witness file.
//   - OldBalance: balance của (Owner, Denom) TRƯỚC khi batch apply.
//   - NewBalance: balance của (Owner, Denom) SAU khi batch apply.
type AccountWitnessSecret struct {
	Owner      string
	UserSecret string
	OldBalance string
	NewBalance string
}

// WitnessInputs là batch-shaped input của WitnessBuilder. Tái sử dụng
// SettlementInputs đã được STATE-08 validate (cùng nguồn truth) — và
// thêm mảng AccountWitnessSecret cho từng account tham gia batch.
//
// Builder enforce một invariant quan trọng: với mỗi entry trong
// Accounts, phải tồn tại ÍT NHẤT một deposit hoặc withdrawal trong
// Settlement có cùng Owner — account không-liên-quan tới batch sẽ bị
// reject.
type WitnessInputs struct {
	Settlement SettlementInputs
	Accounts   []AccountWitnessSecret
	StatePath  []string
}

// WitnessBuilder sản xuất Witness canonical cho P2 prover (ZK-09).
// Stateless — concurrent calls an toàn.
type WitnessBuilder struct{}

func NewWitnessBuilder() *WitnessBuilder { return &WitnessBuilder{} }

// Build validate WitnessInputs và assemble một Witness canonical.
//
// Validation pipeline cho mỗi account (failure → ErrInvalidWitnessInputs
// wrap với cause; không partial output):
//
//  1. Owner non-empty; phải có ít nhất 1 deposit hoặc 1 withdrawal trong
//     Settlement với cùng Owner (account không liên quan tới batch bị
//     reject).
//  2. UserSecret non-empty (sau trim).
//  3. OldBalance / NewBalance parse non-negative int.
//  4. Tính sum(deposits của Owner).amount và sum(withdrawals của
//     Owner).amount, sau đó assert ZK-04:
//        newBalance + sumWithdraw == oldBalance + sumDeposit
//  5. Với MỖI withdrawal của Owner trong settlement: re-derive
//     state.NullifierFor(secret, request.Nonce) và assert bằng
//     Nullifier mà settlement entry đang mang. Stale (secret, nonce)
//     bị bắt ngay tại đây — tiết kiệm prover round-trip.
//
// Nonce ghi vào WitnessAccount = nonce của withdrawal cuối cùng của
// account đó trong batch (sau đó account.Nonce ở local state đã tăng tới
// giá trị này). Trong canonical Alice vector, mỗi account chỉ có 1
// withdrawal nên không có ambiguity.
//
// Post-conditions on success:
//   - OldBalance/NewBalance canonical big.Int strings ("01" → "1").
//   - UserSecret giữ verbatim (sau khi trim outer whitespace).
//   - StatePath defensive copy; nil khi caller không truyền (omitempty).
func (b *WitnessBuilder) Build(in WitnessInputs) (types.Witness, error) {
	if len(in.Accounts) == 0 {
		return types.Witness{}, fmt.Errorf("%w: accounts is empty", ErrInvalidWitnessInputs)
	}

	out := make([]types.WitnessAccount, 0, len(in.Accounts))
	for i, acc := range in.Accounts {
		owner := strings.TrimSpace(acc.Owner)
		if owner == "" {
			return types.Witness{}, fmt.Errorf("%w: accounts[%d].owner is empty", ErrInvalidWitnessInputs, i)
		}
		secret := strings.TrimSpace(acc.UserSecret)
		if secret == "" {
			return types.Witness{}, fmt.Errorf("%w: accounts[%d].userSecret is empty", ErrInvalidWitnessInputs, i)
		}
		oldBal, err := parseNonNegative(acc.OldBalance)
		if err != nil {
			return types.Witness{}, fmt.Errorf("%w: accounts[%d].oldBalance %q invalid: %v",
				ErrInvalidWitnessInputs, i, acc.OldBalance, err)
		}
		newBal, err := parseNonNegative(acc.NewBalance)
		if err != nil {
			return types.Witness{}, fmt.Errorf("%w: accounts[%d].newBalance %q invalid: %v",
				ErrInvalidWitnessInputs, i, acc.NewBalance, err)
		}

		sumDeposit := new(big.Int)
		for _, d := range in.Settlement.Deposits {
			if d.Owner != owner {
				continue
			}
			amt, err := parsePositive(d.Amount)
			if err != nil {
				return types.Witness{}, fmt.Errorf("%w: accounts[%d] deposit %q amount %q invalid: %v",
					ErrInvalidWitnessInputs, i, d.DepositID, d.Amount, err)
			}
			sumDeposit.Add(sumDeposit, amt)
		}

		sumWithdraw := new(big.Int)
		var lastNonce string
		var ownerWithdrawals []WithdrawalInput
		for _, w := range in.Settlement.Withdrawals {
			if w.Request.Owner != owner {
				continue
			}
			amt, err := parsePositive(w.Request.Amount)
			if err != nil {
				return types.Witness{}, fmt.Errorf("%w: accounts[%d] withdraw %q amount %q invalid: %v",
					ErrInvalidWitnessInputs, i, w.Request.WithdrawID, w.Request.Amount, err)
			}
			sumWithdraw.Add(sumWithdraw, amt)
			lastNonce = w.Request.Nonce
			ownerWithdrawals = append(ownerWithdrawals, w)
		}

		if sumDeposit.Sign() == 0 && sumWithdraw.Sign() == 0 {
			return types.Witness{}, fmt.Errorf(
				"%w: accounts[%d] owner %q has no deposit or withdrawal in settlement",
				ErrInvalidWitnessInputs, i, owner,
			)
		}

		// ZK-04: newBalance + sumWithdraw == oldBalance + sumDeposit
		lhs := new(big.Int).Add(newBal, sumWithdraw)
		rhs := new(big.Int).Add(oldBal, sumDeposit)
		if lhs.Cmp(rhs) != 0 {
			return types.Witness{}, fmt.Errorf(
				"%w: accounts[%d] balance transition violated (ZK-04): newBalance(%s)+sumWithdraw(%s)=%s != oldBalance(%s)+sumDeposit(%s)=%s",
				ErrInvalidWitnessInputs, i,
				newBal.String(), sumWithdraw.String(), lhs.String(),
				oldBal.String(), sumDeposit.String(), rhs.String(),
			)
		}

		// ZK-05: với từng withdrawal của account, re-derive nullifier
		// và assert khớp với nullifier settlement entry đang mang.
		for _, w := range ownerWithdrawals {
			nonce, err := parseNonNegative(w.Request.Nonce)
			if err != nil {
				return types.Witness{}, fmt.Errorf(
					"%w: accounts[%d] withdraw %q nonce %q invalid: %v",
					ErrInvalidWitnessInputs, i, w.Request.WithdrawID, w.Request.Nonce, err,
				)
			}
			rederived, err := state.NullifierFor(secret, nonce.String())
			if err != nil {
				return types.Witness{}, fmt.Errorf(
					"%w: accounts[%d] cannot re-derive nullifier for withdraw %q: %v",
					ErrInvalidWitnessInputs, i, w.Request.WithdrawID, err,
				)
			}
			if rederived != w.Nullifier {
				return types.Witness{}, fmt.Errorf(
					"%w: accounts[%d] nullifier mismatch (ZK-05) for withdraw %q: supplied=%s, re-derived(userSecret, nonce=%s)=%s",
					ErrInvalidWitnessInputs, i, w.Request.WithdrawID, w.Nullifier, nonce.String(), rederived,
				)
			}
		}

		// Nonce field on the witness = nonce post-last-withdraw. Nếu
		// account chỉ có deposit (không withdraw), giữ "0" như local
		// state.NewAccount default.
		nonceField := "0"
		if lastNonce != "" {
			n, err := parseNonNegative(lastNonce)
			if err != nil {
				return types.Witness{}, fmt.Errorf("%w: accounts[%d] nonce normalization failed: %v",
					ErrInvalidWitnessInputs, i, err)
			}
			nonceField = n.String()
		}

		out = append(out, types.WitnessAccount{
			Owner:      owner,
			UserSecret: secret,
			Nonce:      nonceField,
			OldBalance: oldBal.String(),
			NewBalance: newBal.String(),
		})
	}

	var statePath []string
	if len(in.StatePath) > 0 {
		statePath = append([]string(nil), in.StatePath...)
	}

	return types.Witness{
		Accounts:  out,
		StatePath: statePath,
	}, nil
}
