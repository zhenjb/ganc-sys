package types

type DepositRequestBody struct {
	Owner  string `json:"owner"`
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

type DepositResponse struct {
	TxHash        string        `json:"txHash"`
	DepositRecord DepositRecord `json:"depositRecord"`
	State         PartialState  `json:"state"`
}

type WithdrawRequestBody struct {
	Owner       string `json:"owner"`
	Denom       string `json:"denom"`
	Amount      string `json:"amount"`
	Destination string `json:"destination"`
}

type WithdrawRequestResponse struct {
	WithdrawRequest WithdrawRequest `json:"withdrawRequest"`
	State           PartialState    `json:"state"`
}

type BuildBatchRequestBody struct {
	DepositIDs  []string `json:"depositIds"`
	WithdrawIDs []string `json:"withdrawIds"`
}

type BuildBatchResponse struct {
	SettlementUpdate SettlementUpdate `json:"settlementUpdate"`
	BatchCommitments BatchCommitments `json:"batchCommitments"`
	Witness          Witness          `json:"witness"`
	State            PartialState     `json:"state"`
}

type GenerateProofRequestBody struct {
	SettlementUpdate SettlementUpdate `json:"settlementUpdate"`
	BatchCommitments BatchCommitments `json:"batchCommitments"`
	Witness          Witness          `json:"witness"`
}

type GenerateProofResponse struct {
	ProofBundle ProofBundle  `json:"proofBundle"`
	State       PartialState `json:"state"`
}

type SubmitBatchRequestBody struct {
	SettlementUpdate SettlementUpdate `json:"settlementUpdate"`
	BatchCommitments BatchCommitments `json:"batchCommitments"`
	ProofBundle      ProofBundle      `json:"proofBundle"`
}

type SubmitBatchResponse struct {
	TxHash           string           `json:"txHash"`
	Accepted         bool             `json:"accepted"`
	ProofStatus      string           `json:"proofStatus"`
	SettlementUpdate SettlementUpdate `json:"settlementUpdate"`
	BatchCommitments BatchCommitments `json:"batchCommitments"`
	WithdrawRecords  []WithdrawRecord `json:"withdrawRecords"`
	State            PartialState     `json:"state"`
}

type ClaimWithdrawRequestBody struct {
	WithdrawID string `json:"withdrawId"`
}

type ClaimWithdrawResponse struct {
	TxHash         string          `json:"txHash"`
	WithdrawRecord WithdrawRecord  `json:"withdrawRecord"`
	Balances       BalanceSnapshot `json:"balances"`
	State          PartialState    `json:"state"`
}

type ListDepositsResponse struct {
	Deposits []DepositRecord `json:"deposits"`
}

type GetDepositResponse struct {
	DepositRecord DepositRecord `json:"depositRecord"`
}
