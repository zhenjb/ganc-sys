package types

type Witness struct {
	Accounts []WitnessAccount `json:"accounts"`
}

type WitnessAccount struct {
	Owner      string `json:"owner"`
	UserSecret string `json:"userSecret"`
	Nonce      string `json:"nonce"`
	OldBalance string `json:"oldBalance"`
	NewBalance string `json:"newBalance"`
}
