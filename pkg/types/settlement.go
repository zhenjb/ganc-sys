package types

type SettlementUpdate struct {
	BatchID      string                 `json:"batchId"`
	OldStateRoot string                 `json:"oldStateRoot"`
	NewStateRoot string                 `json:"newStateRoot"`
	Deposits     []SettlementDeposit    `json:"deposits"`
	Withdrawals  []SettlementWithdrawal `json:"withdrawals"`
}

type SettlementDeposit struct {
	DepositID string `json:"depositId"`
	Owner     string `json:"owner"`
	Denom     string `json:"denom"`
	Amount    string `json:"amount"`
}

type SettlementWithdrawal struct {
	WithdrawID      string `json:"withdrawId"`
	Owner           string `json:"owner"`
	Denom           string `json:"denom"`
	Amount          string `json:"amount"`
	Destination     string `json:"destination"`
	DestinationHash string `json:"destinationHash"`
	Nullifier       string `json:"nullifier"`
}

type BatchCommitments struct {
	DepositsRoot        string `json:"depositsRoot"`
	WithdrawalsRoot     string `json:"withdrawalsRoot"`
	NullifiersRoot      string `json:"nullifiersRoot"`
	WithdrawOutputsRoot string `json:"withdrawOutputsRoot"`
}
