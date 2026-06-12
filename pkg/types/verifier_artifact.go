package types

// VerifierArtifact is metadata exported by gazk's /verifier-artifact endpoint.
//
// D7-B scope:
//   - ganc-sys consumes this metadata to bind a proofBundle.verificationKeyId
//     to the verifier artifact advertised by the remote prover.
//   - This is not on-chain verification yet.
type VerifierArtifact struct {
	VerificationKeyID string   `json:"verificationKeyId"`
	HashMode          string   `json:"hashMode"`
	Curve             string   `json:"curve"`
	Backend           string   `json:"backend"`
	PublicInputCount  int      `json:"publicInputCount"`
	PublicInputNames  []string `json:"publicInputNames"`
	VerifyingKey      string   `json:"verifyingKey"`
}
