package types

type AppState struct {
	Mode                 string            `json:"mode"`
	CurrentStateRoot     string            `json:"currentStateRoot"`
	UserBalances         map[string]string `json:"userBalances"`
	ModuleAccountBalance map[string]string `json:"moduleAccountBalance"`

	LatestDeposit         *DepositRecord    `json:"latestDeposit,omitempty"`
	LatestWithdrawRequest *WithdrawRequest  `json:"latestWithdrawRequest,omitempty"`
	LatestSettlement      *SettlementUpdate `json:"latestSettlement,omitempty"`
	LatestProof           *ProofBundle      `json:"latestProof,omitempty"`
	LatestWithdrawRecord  *WithdrawRecord   `json:"latestWithdrawRecord,omitempty"`

	ProofStatus    string `json:"proofStatus"`
	DepositStatus  string `json:"depositStatus"`
	WithdrawStatus string `json:"withdrawStatus"`
}

type PartialState struct {
	CurrentStateRoot string `json:"currentStateRoot,omitempty"`
	ProofStatus      string `json:"proofStatus,omitempty"`
	DepositStatus    string `json:"depositStatus,omitempty"`
	WithdrawStatus   string `json:"withdrawStatus,omitempty"`
}
