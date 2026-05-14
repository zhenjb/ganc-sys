package types

type WithdrawRequest struct {
	WithdrawID  string `json:"withdrawId"`
	Owner       string `json:"owner"`
	Denom       string `json:"denom"`
	Amount      string `json:"amount"`
	Destination string `json:"destination"`
	Nonce       string `json:"nonce"`
	Signature   string `json:"signature"`
}

type WithdrawRecord struct {
	WithdrawID  string `json:"withdrawId"`
	Owner       string `json:"owner"`
	Denom       string `json:"denom"`
	Amount      string `json:"amount"`
	Destination string `json:"destination"`
	Nullifier   string `json:"nullifier"`
	Claimed     bool   `json:"claimed"`
}
