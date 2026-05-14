package types

type ProofBundle struct {
	Proof             string   `json:"proof"`
	PublicInputs      []string `json:"publicInputs"`
	VerificationKeyID string   `json:"verificationKeyId"`
}
