package batch

import (
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// Domain tags cho 4 commitment root. Tách thành 4 tag riêng để không có
// hai root nào có thể colide ngay cả khi nội dung byte-string bên trong
// trùng nhau. Khi ZK-02 chốt hash circuit thật (Poseidon/MiMC), bump
// version "v0" → "v1" và regenerate vectors.
const (
	depositsRootDomainTag        = "zkdex/batch/depositsRoot/v0"
	withdrawalsRootDomainTag     = "zkdex/batch/withdrawalsRoot/v0"
	nullifiersRootDomainTag      = "zkdex/batch/nullifiersRoot/v0"
	withdrawOutputsRootDomainTag = "zkdex/batch/withdrawOutputsRoot/v0"
)

// BuildCommitments derives 4 commitment root từ một SettlementUpdate
// đã hoàn chỉnh. Output 4 root là hex string "0x"-prefixed.
//
// Recipe (placeholder cho MVP):
//
//	depositsRoot       = SHA256( domain | for each d: d.depositId|owner|denom|amount; )
//	withdrawalsRoot    = SHA256( domain | for each w: w.withdrawId|owner|denom|amount|destination|destinationHash|nullifier; )
//	nullifiersRoot     = SHA256( domain | for each w: w.nullifier; )
//	withdrawOutputsRoot= SHA256( domain | for each w: w.destinationHash|amount|denom; )
//
// Mọi field được nối bằng '|' giữa các thuộc tính của cùng một entry và
// ';' giữa các entry. Empty batch (slice rỗng) vẫn cho ra một hash hợp
// lệ (chỉ chứa domain tag) — phía chain verifier biết empty-slice root
// ứng với batch không có entry tương ứng.
//
// Determinism: hash chỉ phụ thuộc thứ tự và nội dung slice — caller phải
// giữ thứ tự ổn định giữa các lần build cho cùng một batch (P3 builder
// luôn preserve insertion order).
//
// Pure function — không state, không mutex, không clock.
func BuildCommitments(upd types.SettlementUpdate) types.BatchCommitments {
	return types.BatchCommitments{
		DepositsRoot:        depositsRoot(upd.Deposits),
		WithdrawalsRoot:     withdrawalsRoot(upd.Withdrawals),
		NullifiersRoot:      nullifiersRoot(upd.Withdrawals),
		WithdrawOutputsRoot: withdrawOutputsRoot(upd.Withdrawals),
	}
}

func depositsRoot(items []types.SettlementDeposit) string {
	var b strings.Builder
	b.WriteString(depositsRootDomainTag)
	b.WriteByte('|')
	for _, d := range items {
		b.WriteString(d.DepositID)
		b.WriteByte('|')
		b.WriteString(d.Owner)
		b.WriteByte('|')
		b.WriteString(d.Denom)
		b.WriteByte('|')
		b.WriteString(d.Amount)
		b.WriteByte(';')
	}
	return hash.SHA256Hex([]byte(b.String()))
}

func withdrawalsRoot(items []types.SettlementWithdrawal) string {
	var b strings.Builder
	b.WriteString(withdrawalsRootDomainTag)
	b.WriteByte('|')
	for _, w := range items {
		b.WriteString(w.WithdrawID)
		b.WriteByte('|')
		b.WriteString(w.Owner)
		b.WriteByte('|')
		b.WriteString(w.Denom)
		b.WriteByte('|')
		b.WriteString(w.Amount)
		b.WriteByte('|')
		b.WriteString(w.Destination)
		b.WriteByte('|')
		b.WriteString(w.DestinationHash)
		b.WriteByte('|')
		b.WriteString(w.Nullifier)
		b.WriteByte(';')
	}
	return hash.SHA256Hex([]byte(b.String()))
}

func nullifiersRoot(items []types.SettlementWithdrawal) string {
	var b strings.Builder
	b.WriteString(nullifiersRootDomainTag)
	b.WriteByte('|')
	for _, w := range items {
		b.WriteString(w.Nullifier)
		b.WriteByte(';')
	}
	return hash.SHA256Hex([]byte(b.String()))
}

func withdrawOutputsRoot(items []types.SettlementWithdrawal) string {
	var b strings.Builder
	b.WriteString(withdrawOutputsRootDomainTag)
	b.WriteByte('|')
	for _, w := range items {
		b.WriteString(w.DestinationHash)
		b.WriteByte('|')
		b.WriteString(w.Amount)
		b.WriteByte('|')
		b.WriteString(w.Denom)
		b.WriteByte(';')
	}
	return hash.SHA256Hex([]byte(b.String()))
}

// CommitmentDomainTags expose các domain tag để cross-role (P2 circuit,
// P1 verifier, P4 backend) có thể assert off-chain derivation chưa
// silently bump version.
func CommitmentDomainTags() (deposits, withdrawals, nullifiers, withdrawOutputs string) {
	return depositsRootDomainTag,
		withdrawalsRootDomainTag,
		nullifiersRootDomainTag,
		withdrawOutputsRootDomainTag
}
