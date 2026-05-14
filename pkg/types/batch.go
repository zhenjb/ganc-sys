package types

type BatchStatus string

const (
	BatchStatusPending  BatchStatus = "pending"
	BatchStatusProved   BatchStatus = "proved"
	BatchStatusSubmitted BatchStatus = "submitted"
	BatchStatusAccepted BatchStatus = "accepted"
	BatchStatusRejected BatchStatus = "rejected"
)

type Batch struct {
	BatchID      string      `json:"batchId"`
	OldStateRoot string      `json:"oldStateRoot"`
	NewStateRoot string      `json:"newStateRoot"`
	DepositIDs   []string    `json:"depositIds"`
	WithdrawIDs  []string    `json:"withdrawIds"`
	Status       BatchStatus `json:"status"`
}
