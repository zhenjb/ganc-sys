package prover

import (
	"fmt"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ExpectedArtifact pins the ZK contract the backend expects gazk to advertise.
//
// SYS-05 requires the backend to "đảm bảo verificationKeyId/artifact khớp gazk":
// shape validation alone is not enough, because a gazk instance running a
// DIFFERENT circuit (different verifying key, different hash mode) would still
// pass the structural checks. Pinning the expected verificationKeyId / hashMode
// lets the backend refuse to close the ZK loop against the wrong prover.
//
// Both fields are optional. An empty field means "do not pin this attribute",
// so the default (unset) behaviour is backward compatible: only the structural
// ValidateVerifierArtifact checks run.
type ExpectedArtifact struct {
	VerificationKeyID string
	HashMode          string
}

// IsZero reports whether no expectation is pinned at all.
func (e ExpectedArtifact) IsZero() bool {
	return strings.TrimSpace(e.VerificationKeyID) == "" &&
		strings.TrimSpace(e.HashMode) == ""
}

// ValidateArtifactMatchesExpected runs the structural artifact validation and,
// when a value is pinned, asserts the gazk-advertised verificationKeyId /
// hashMode match it exactly. This is the single source of truth used by both the
// startup preflight (cmd/api) and any per-request check.
func ValidateArtifactMatchesExpected(artifact types.VerifierArtifact, expected ExpectedArtifact) error {
	if err := ValidateVerifierArtifact(artifact); err != nil {
		return err
	}

	if want := strings.TrimSpace(expected.VerificationKeyID); want != "" {
		if artifact.VerificationKeyID != want {
			return fmt.Errorf(
				"verifier artifact verificationKeyId mismatch: gazk=%q expected=%q",
				artifact.VerificationKeyID, want,
			)
		}
	}

	if want := strings.TrimSpace(expected.HashMode); want != "" {
		if artifact.HashMode != want {
			return fmt.Errorf(
				"verifier artifact hashMode mismatch: gazk=%q expected=%q",
				artifact.HashMode, want,
			)
		}
	}

	return nil
}
