package types

type SettlementUpdate struct {
	BatchID      string `json:"batchId"`
	OldStateRoot string `json:"oldStateRoot"`
	NewStateRoot string `json:"newStateRoot"`

	DepositID     string `json:"depositId"`
	DepositAmount string `json:"depositAmount"`

	WithdrawID          string `json:"withdrawId"`
	WithdrawAmount      string `json:"withdrawAmount"`
	WithdrawAddress     string `json:"withdrawAddress"`
	WithdrawAddressHash string `json:"withdrawAddressHash"`

	Nullifier string `json:"nullifier"`
}
