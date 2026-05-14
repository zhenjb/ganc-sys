package types

type DepositRecord struct {
	DepositID     string `json:"depositId"`
	Owner         string `json:"owner"`
	Denom         string `json:"denom"`
	Amount        string `json:"amount"`
	Processed     bool   `json:"processed"`
	CreatedHeight int64  `json:"createdHeight"`
	TxHash        string `json:"txHash"`
}
