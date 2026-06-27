package service

import (
	"context"
	"errors"
	"testing"
)

// SYS-05 defense-in-depth: the submit gate must reject a proof that targets the
// wrong circuit or whose public inputs do not bind to the submitted settlement —
// BEFORE gazk is called — so the relayer never runs and state never advances.

func TestSubmitBatchRejectsWrongVerificationKeyID(t *testing.T) {
	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)
	verifier := &stubVerifier{err: nil} // gazk would say "valid"
	svc.SetProofVerifier(verifier)
	svc.SetExpectedVerificationKeyID("gazk-balance-smoke-v1")

	req := sampleSubmitRequest()
	req.ProofBundle.VerificationKeyID = "some-other-circuit-v9"

	_, err := svc.SubmitBatch(context.Background(), req)
	if err == nil || !errors.Is(err, ErrProofVerificationFailed) {
		t.Fatalf("expected ErrProofVerificationFailed for wrong vkId, got %v", err)
	}
	if verifier.called {
		t.Fatalf("verifier (gazk) must NOT be called when vkId is pinned wrong")
	}
	if rel.submitCalled {
		t.Fatalf("relayer must NOT be called when vkId is pinned wrong")
	}
}

func TestSubmitBatchAcceptsMatchingVerificationKeyID(t *testing.T) {
	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)
	verifier := &stubVerifier{err: nil}
	svc.SetProofVerifier(verifier)
	svc.SetExpectedVerificationKeyID("gazk-balance-smoke-v1")

	// sampleSubmitRequest already carries vkId = gazk-balance-smoke-v1 and public
	// inputs that bind to its settlement/commitments.
	resp, err := svc.SubmitBatch(context.Background(), sampleSubmitRequest())
	if err != nil {
		t.Fatalf("submit batch: %v", err)
	}
	if !verifier.called || !rel.submitCalled || !resp.Accepted {
		t.Fatalf("expected verify+relay+accept, got called=%v submit=%v accepted=%v",
			verifier.called, rel.submitCalled, resp.Accepted)
	}
}

func TestSubmitBatchRejectsUnboundPublicInputs(t *testing.T) {
	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)
	verifier := &stubVerifier{err: nil}
	svc.SetProofVerifier(verifier)

	req := sampleSubmitRequest()
	// Tamper one public input so it no longer matches the derived settlement input.
	req.ProofBundle.PublicInputs[1] = "0xTAMPERED"

	_, err := svc.SubmitBatch(context.Background(), req)
	if err == nil || !errors.Is(err, ErrProofVerificationFailed) {
		t.Fatalf("expected ErrProofVerificationFailed for unbound public inputs, got %v", err)
	}
	if verifier.called {
		t.Fatalf("verifier must NOT be called when public inputs do not bind to settlement")
	}
	if rel.submitCalled {
		t.Fatalf("relayer must NOT be called when public inputs do not bind to settlement")
	}
}

func TestSubmitBatchRejectsWrongPublicInputArity(t *testing.T) {
	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)
	svc.SetProofVerifier(&stubVerifier{err: nil})

	req := sampleSubmitRequest()
	req.ProofBundle.PublicInputs = req.ProofBundle.PublicInputs[:5] // 5 instead of 6

	_, err := svc.SubmitBatch(context.Background(), req)
	if err == nil || !errors.Is(err, ErrProofVerificationFailed) {
		t.Fatalf("expected ErrProofVerificationFailed for wrong arity, got %v", err)
	}
	if rel.submitCalled {
		t.Fatalf("relayer must NOT be called when public-input arity is wrong")
	}
}

func TestSubmitBatchContractGuardSkippedInMockMode(t *testing.T) {
	// No verifier set (mock mode): the contract guard is not enforced, so even a
	// mismatched vkId / inputs flows through the mock relayer unchanged. This keeps
	// the chain-free default behaviour intact.
	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)
	svc.SetExpectedVerificationKeyID("gazk-balance-smoke-v1") // pinned but not enforced without a verifier

	req := sampleSubmitRequest()
	req.ProofBundle.VerificationKeyID = "anything"

	resp, err := svc.SubmitBatch(context.Background(), req)
	if err != nil {
		t.Fatalf("mock-mode submit should not enforce the contract guard: %v", err)
	}
	if !rel.submitCalled || !resp.Accepted {
		t.Fatalf("expected mock relayer to accept, got submit=%v accepted=%v", rel.submitCalled, resp.Accepted)
	}
}
