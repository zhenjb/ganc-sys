package prover

import (
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ExpectedPublicInputNamesWithTrades is the 8-input layout of the unified circuit
// (gazk-trade-v1): the locked core [0..5] plus [6]=tradesRoot, [7]=ordersRoot
// (TRD-UNIFY). A core (no-trade) batch is proven by the SAME circuit with the two
// trade roots set to the empty sentinel, so both artifacts are valid.
var ExpectedPublicInputNamesWithTrades = append(
	append([]string(nil), ExpectedPublicInputNames...),
	"batchCommitments.tradesRoot",
	"batchCommitments.ordersRoot",
)

// ValidateVerifierArtifact accepts EITHER the 6-input core artifact
// (gazk-balance-smoke-v1) OR the 8-input unified artifact (gazk-trade-v1).
//
// The unified/trade circuit binds its roots as opaque v0 wire values (TRD-A1) and
// does NOT advertise a hashMode, so hashMode is required only for the 6-input core
// artifact. The structural fields (vkId, curve, backend, verifyingKey) and the
// publicInputNames layout are checked for both.
func ValidateVerifierArtifact(artifact types.VerifierArtifact) error {
	if strings.TrimSpace(artifact.VerificationKeyID) == "" {
		return fmt.Errorf("remote verifier artifact returned empty verificationKeyId")
	}

	if strings.TrimSpace(artifact.Curve) == "" {
		return fmt.Errorf("remote verifier artifact returned empty curve")
	}

	if strings.TrimSpace(artifact.Backend) == "" {
		return fmt.Errorf("remote verifier artifact returned empty backend")
	}

	var expectedNames []string
	switch artifact.PublicInputCount {
	case len(ExpectedPublicInputNames): // 6 — core circuit (advertises hashMode)
		if strings.TrimSpace(artifact.HashMode) == "" {
			return fmt.Errorf("remote verifier artifact returned empty hashMode")
		}
		expectedNames = ExpectedPublicInputNames
	case len(ExpectedPublicInputNamesWithTrades): // 8 — unified circuit (no hashMode)
		expectedNames = ExpectedPublicInputNamesWithTrades
	default:
		return fmt.Errorf(
			"remote verifier artifact publicInputCount mismatch: got %d, expected %d (core) or %d (unified)",
			artifact.PublicInputCount,
			len(ExpectedPublicInputNames),
			len(ExpectedPublicInputNamesWithTrades),
		)
	}

	if len(artifact.PublicInputNames) != len(expectedNames) {
		return fmt.Errorf(
			"remote verifier artifact publicInputNames length mismatch: got %d, expected %d",
			len(artifact.PublicInputNames),
			len(expectedNames),
		)
	}

	for i := range expectedNames {
		if artifact.PublicInputNames[i] != expectedNames[i] {
			return fmt.Errorf(
				"remote verifier artifact publicInputNames[%d] mismatch: got %q, expected %q",
				i,
				artifact.PublicInputNames[i],
				expectedNames[i],
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
