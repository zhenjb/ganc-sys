package prover

import (
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func ValidateVerifierArtifact(artifact types.VerifierArtifact) error {
	if strings.TrimSpace(artifact.VerificationKeyID) == "" {
		return fmt.Errorf("remote verifier artifact returned empty verificationKeyId")
	}

	if strings.TrimSpace(artifact.HashMode) == "" {
		return fmt.Errorf("remote verifier artifact returned empty hashMode")
	}

	if strings.TrimSpace(artifact.Curve) == "" {
		return fmt.Errorf("remote verifier artifact returned empty curve")
	}

	if strings.TrimSpace(artifact.Backend) == "" {
		return fmt.Errorf("remote verifier artifact returned empty backend")
	}

	if artifact.PublicInputCount != len(ExpectedPublicInputNames) {
		return fmt.Errorf(
			"remote verifier artifact publicInputCount mismatch: got %d, expected %d",
			artifact.PublicInputCount,
			len(ExpectedPublicInputNames),
		)
	}

	if len(artifact.PublicInputNames) != len(ExpectedPublicInputNames) {
		return fmt.Errorf(
			"remote verifier artifact publicInputNames length mismatch: got %d, expected %d",
			len(artifact.PublicInputNames),
			len(ExpectedPublicInputNames),
		)
	}

	for i := range ExpectedPublicInputNames {
		if artifact.PublicInputNames[i] != ExpectedPublicInputNames[i] {
			return fmt.Errorf(
				"remote verifier artifact publicInputNames[%d] mismatch: got %q, expected %q",
				i,
				artifact.PublicInputNames[i],
				ExpectedPublicInputNames[i],
			)
		}
	}

	if !strings.HasPrefix(strings.TrimSpace(artifact.VerifyingKey), "0x") {
		return fmt.Errorf("remote verifier artifact verifyingKey must have 0x prefix")
	}

	if len(strings.TrimSpace(artifact.VerifyingKey)) <= 2 {
		return fmt.Errorf("remote verifier artifact verifyingKey is empty")
	}

	return nil
}

func ValidateProofBundleMatchesVerifierArtifact(
	proofBundle types.ProofBundle,
	artifact types.VerifierArtifact,
) error {
	if err := ValidateVerifierArtifact(artifact); err != nil {
		return err
	}

	if strings.TrimSpace(proofBundle.VerificationKeyID) == "" {
		return fmt.Errorf("proofBundle.verificationKeyId is empty")
	}

	if proofBundle.VerificationKeyID != artifact.VerificationKeyID {
		return fmt.Errorf(
			"proofBundle.verificationKeyId does not match verifier artifact: proof=%q artifact=%q",
			proofBundle.VerificationKeyID,
			artifact.VerificationKeyID,
		)
	}

	if len(proofBundle.PublicInputs) != artifact.PublicInputCount {
		return fmt.Errorf(
			"proofBundle.publicInputs length does not match verifier artifact: proof=%d artifact=%d",
			len(proofBundle.PublicInputs),
			artifact.PublicInputCount,
		)
	}

	return nil
}
