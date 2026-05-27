package types

// SettlementDeposit là entry deposit nằm trong SettlementUpdate.Deposits[].
// Chỉ giữ những trường ZK circuit / chain verifier cần để bind vào public
// inputs: id, owner, denom, amount. Mọi metadata khác (txHash, height,
// processed flag) thuộc về DepositRecord ở phía chain và không bao giờ
// đi vào settlement payload.
type SettlementDeposit struct {
	DepositID string `json:"depositId"`
	Owner     string `json:"owner"`
	Denom     string `json:"denom"`
	Amount    string `json:"amount"`
}

// SettlementWithdrawal là entry withdrawal nằm trong
// SettlementUpdate.Withdrawals[]. Đặt tên trường theo Agreements:
//   - destination       (KHÔNG dùng withdrawAddress)
//   - destinationHash   (KHÔNG dùng withdrawAddressHash)
//
// Mỗi withdrawal mang theo nullifier của chính nó để ZK-05 / verifier
// có thể bind 1:1.
type SettlementWithdrawal struct {
	WithdrawID      string `json:"withdrawId"`
	Owner           string `json:"owner"`
	Denom           string `json:"denom"`
	Amount          string `json:"amount"`
	Destination     string `json:"destination"`
	DestinationHash string `json:"destinationHash"`
	Nullifier       string `json:"nullifier"`
}

// SettlementUpdate là batch-shaped settlement P3 đẩy lên prover/chain
// theo schema chốt trong Agreements. Hai mảng Deposits/Withdrawals dùng
// được cho cả batch 1 entry (canonical Alice vector) lẫn batch nhiều
// entry trong tương lai — schema không bao giờ rớt về dạng scalar.
type SettlementUpdate struct {
	BatchID      string                 `json:"batchId"`
	OldStateRoot string                 `json:"oldStateRoot"`
	NewStateRoot string                 `json:"newStateRoot"`
	Deposits     []SettlementDeposit    `json:"deposits"`
	Withdrawals  []SettlementWithdrawal `json:"withdrawals"`
}
