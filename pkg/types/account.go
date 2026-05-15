package types

type Account struct {
	Owner   string `json:"owner"`
	Denom   string `json:"denom"`
	Balance string `json:"balance"`
	Nonce   string `json:"nonce"`
}
