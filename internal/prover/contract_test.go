package prover

import (
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func validArtifact() types.VerifierArtifact {
	return types.VerifierArtifact{
		VerificationKeyID: "gazk-balance-smoke-v1",
		HashMode:          "v0-sha256",
		Curve:             "BN254",
		Backend:           "groth16",
		PublicInputCount:  len(ExpectedPublicInputNames),
		PublicInputNames:  append([]string(nil), ExpectedPublicInputNames...),
		VerifyingKey:      "0xabcdef",
	}
}

func TestValidateArtifactMatchesExpected_OK(t *testing.T) {
	err := ValidateArtifactMatchesExpected(validArtifact(), ExpectedArtifact{
		VerificationKeyID: "gazk-balance-smoke-v1",
		HashMode:          "v0-sha256",
	})
	if err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

func TestValidateArtifactMatchesExpected_EmptyExpectationSkipsPin(t *testing.T) {
	// No pin set: only structural validation runs, so a valid artifact passes
	// regardless of its verificationKeyId / hashMode.
	if err := ValidateArtifactMatchesExpected(validArtifact(), ExpectedArtifact{}); err != nil {
		t.Fatalf("empty expectation should skip pin, got %v", err)
	}
}

func TestValidateArtifactMatchesExpected_VkIDMismatch(t *testing.T) {
	err := ValidateArtifactMatchesExpected(validArtifact(), ExpectedArtifact{
		VerificationKeyID: "gazk-balance-smoke-v2",
	})
	if err == nil || !strings.Contains(err.Error(), "verificationKeyId mismatch") {
		t.Fatalf("expected vkId mismatch error, got %v", err)
	}
}

func TestValidateArtifactMatchesExpected_HashModeMismatch(t *testing.T) {
	err := ValidateArtifactMatchesExpected(validArtifact(), ExpectedArtifact{
		HashMode: "v1-mimc",
	})
	if err == nil || !strings.Contains(err.Error(), "hashMode mismatch") {
		t.Fatalf("expected hashMode mismatch error, got %v", err)
	}
}

func TestValidateArtifactMatchesExpected_StructurallyInvalid(t *testing.T) {
	bad := validArtifact()
	bad.PublicInputCount = 5 // breaks structural contract before any pin check
	if err := ValidateArtifactMatchesExpected(bad, ExpectedArtifact{}); err == nil {
		t.Fatalf("expected structural validation failure")
	}
}

func TestExpectedArtifactIsZero(t *testing.T) {
	if !(ExpectedArtifact{}).IsZero() {
		t.Fatalf("empty expected artifact should be zero")
	}
	if (ExpectedArtifact{VerificationKeyID: "x"}).IsZero() {
		t.Fatalf("non-empty expected artifact should not be zero")
	}
}
