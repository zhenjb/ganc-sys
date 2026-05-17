package types

type AppState struct {
	Mode                 string            `json:"mode"`
	CurrentStateRoot     string            `json:"currentStateRoot"`
	UserBalances         map[string]string `json:"userBalances"`
	ModuleAccountBalance map[string]string `json:"moduleAccountBalance"`

	LatestDeposit          *DepositRecord    `json:"latestDeposit"`
	LatestWithdrawRequest  *WithdrawRequest  `json:"latestWithdrawRequest"`
	LatestSettlement       *SettlementUpdate `json:"latestSettlement"`
	LatestBatchCommitments *BatchCommitments `json:"latestBatchCommitments"`
	LatestProof            *ProofBundle      `json:"latestProof"`
	LatestWithdrawRecords  []WithdrawRecord  `json:"latestWithdrawRecords"`

	ProofStatus    string `json:"proofStatus"`
	DepositStatus  string `json:"depositStatus"`
	WithdrawStatus string `json:"withdrawStatus"`
	BatchStatus    string `json:"batchStatus"`
}

type PartialState struct {
	CurrentStateRoot string `json:"currentStateRoot,omitempty"`
	ProofStatus      string `json:"proofStatus,omitempty"`
	DepositStatus    string `json:"depositStatus,omitempty"`
	WithdrawStatus   string `json:"withdrawStatus,omitempty"`
	BatchStatus      string `json:"batchStatus,omitempty"`
}

type BalanceSnapshot struct {
	UserBalances         map[string]string `json:"userBalances"`
	ModuleAccountBalance map[string]string `json:"moduleAccountBalance"`
}
