package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/prover"
)

// fakeGazk emulates the gazk /verify endpoint: it reports a proof valid only when
// the proof bytes equal the canonical value, so a tampered proof is rejected by
// the real RemoteClient.Verify HTTP path (not a stub). This closes the SYS-05
// loop end-to-end in a unit test, without a live gazk service.
func fakeGazk(t *testing.T, canonicalProof string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/verify" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body struct {
			ProofBundle struct {
				Proof string `json:"proof"`
			} `json:"proofBundle"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if body.ProofBundle.Proof == canonicalProof {
			_ = json.NewEncoder(w).Encode(map[string]any{"valid": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"valid": false, "error": "Groth16 verification failed"})
	}))
}

func TestSubmitBatchClosesZKLoopWithRealRemoteVerifier(t *testing.T) {
	server := fakeGazk(t, "0xrealproof")
	defer server.Close()

	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)
	svc.SetProofVerifier(prover.NewRemoteClient(server.URL))
	svc.SetExpectedVerificationKeyID("gazk-balance-smoke-v1")

	// Happy path: canonical proof -> gazk says valid -> batch accepted.
	resp, err := svc.SubmitBatch(context.Background(), sampleSubmitRequest())
	if err != nil {
		t.Fatalf("submit batch (valid proof): %v", err)
	}
	if !resp.Accepted || !rel.submitCalled {
		t.Fatalf("expected accepted+relayed on valid proof, got accepted=%v submit=%v", resp.Accepted, rel.submitCalled)
	}
}

func TestSubmitBatchRejectsTamperedProofViaRealRemoteVerifier(t *testing.T) {
	server := fakeGazk(t, "0xrealproof")
	defer server.Close()

	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)
	svc.SetProofVerifier(prover.NewRemoteClient(server.URL))
	svc.SetExpectedVerificationKeyID("gazk-balance-smoke-v1")

	req := sampleSubmitRequest()
	req.ProofBundle.Proof = "0xtampered" // gazk will return valid:false

	_, err := svc.SubmitBatch(context.Background(), req)
	if err == nil || !errors.Is(err, ErrProofVerificationFailed) {
		t.Fatalf("expected ErrProofVerificationFailed for tampered proof, got %v", err)
	}
	if rel.submitCalled {
		t.Fatalf("relayer must NOT be called when gazk rejects the proof")
	}
}
