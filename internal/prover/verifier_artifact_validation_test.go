package prover

import (
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestValidateVerifierArtifactAcceptsGazkShape(t *testing.T) {
	artifact := validVerifierArtifactForTest()

	if err := ValidateVerifierArtifact(artifact); err != nil {
		t.Fatalf("expected artifact to be valid: %v", err)
	}
}

func TestValidateVerifierArtifactRejectsPublicInputNameMismatch(t *testing.T) {
	artifact := validVerifierArtifactForTest()
	artifact.PublicInputNames[1] = "wrong"

	err := ValidateVerifierArtifact(artifact)
	if err == nil {
		t.Fatalf("expected public input name mismatch to fail")
	}
}

func TestValidateProofBundleMatchesVerifierArtifact(t *testing.T) {
	artifact := validVerifierArtifactForTest()
	proofBundle := types.ProofBundle{
		Proof:             "0xproof",
		PublicInputs:      []string{"0xrootA", "0xrootB", "0xdepositsRoot", "0xwithdrawalsRoot", "0xnullifiersRoot", "0xwithdrawOutputsRoot"},
		VerificationKeyID: artifact.VerificationKeyID,
	}

	if err := ValidateProofBundleMatchesVerifierArtifact(proofBundle, artifact); err != nil {
		t.Fatalf("expected proof bundle to match artifact: %v", err)
	}
}

func TestValidateProofBundleMatchesVerifierArtifactRejectsKeyIDMismatch(t *testing.T) {
	artifact := validVerifierArtifactForTest()
	proofBundle := types.ProofBundle{
		Proof:             "0xproof",
		PublicInputs:      []string{"0xrootA", "0xrootB", "0xdepositsRoot", "0xwithdrawalsRoot", "0xnullifiersRoot", "0xwithdrawOutputsRoot"},
		VerificationKeyID: "other-key",
	}

	err := ValidateProofBundleMatchesVerifierArtifact(proofBundle, artifact)
	if err == nil {
		t.Fatalf("expected verificationKeyId mismatch to fail")
	}
}

func validVerifierArtifactForTest() types.VerifierArtifact {
	return types.VerifierArtifact{
		VerificationKeyID: "gazk-balance-smoke-v1",
		HashMode:          "v0-sha256",
		Curve:             "BN254",
		Backend:           "groth16",
		PublicInputCount:  6,
		PublicInputNames:  append([]string(nil), ExpectedPublicInputNames...),
		VerifyingKey:      "0x1234",
	}
}
