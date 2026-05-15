package types

type Witness struct {
	UserSecret string   `json:"userSecret"`
	Nonce      string   `json:"nonce"`
	OldBalance string   `json:"oldBalance"`
	NewBalance string   `json:"newBalance"`
	StatePath  []string `json:"statePath,omitempty"`
}
