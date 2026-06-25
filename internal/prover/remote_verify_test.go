package prover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func sampleVerifyInput() VerifyProofInput {
	return VerifyProofInput{
		SettlementUpdate: types.SettlementUpdate{
			BatchID:      "batch-1",
			OldStateRoot: "0xrootA",
			NewStateRoot: "0xrootB",
		},
		BatchCommitments: types.BatchCommitments{
			DepositsRoot:        "0xdepositsRoot",
			WithdrawalsRoot:     "0xwithdrawalsRoot",
			NullifiersRoot:      "0xnullifiersRoot",
			WithdrawOutputsRoot: "0xwithdrawOutputsRoot",
		},
		ProofBundle: types.ProofBundle{
			Proof: "0xrealproof",
			PublicInputs: []string{
				"0xrootA", "0xrootB", "0xdepositsRoot",
				"0xwithdrawalsRoot", "0xnullifiersRoot", "0xwithdrawOutputsRoot",
			},
			VerificationKeyID: "gazk-balance-smoke-v1",
		},
	}
}

func TestRemoteClientVerifyCallsGazkVerifyEndpoint(t *testing.T) {
	var received remoteVerifyRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/verify" {
			t.Fatalf("expected /verify, got %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteVerifyResponse{Valid: true})
	}))
	defer server.Close()

	client := NewRemoteClient(server.URL)

	if err := client.Verify(context.Background(), sampleVerifyInput()); err != nil {
		t.Fatalf("verify: %v", err)
	}

	if received.SettlementUpdate.BatchID != "batch-1" {
		t.Fatalf("expected batchId=batch-1 forwarded, got %q", received.SettlementUpdate.BatchID)
	}
	if received.ProofBundle.Proof != "0xrealproof" {
		t.Fatalf("expected proof forwarded, got %q", received.ProofBundle.Proof)
	}
}

func TestRemoteClientVerifyRejectsInvalidProof(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteVerifyResponse{
			Valid: false,
			Error: "Groth16 verification failed",
		})
	}))
	defer server.Close()

	client := NewRemoteClient(server.URL)

	err := client.Verify(context.Background(), sampleVerifyInput())
	if err == nil {
		t.Fatalf("expected error for invalid proof")
	}
}

func TestRemoteClientVerifyReturnsErrorOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(remoteErrorResponse{Error: "invalid verify request"})
	}))
	defer server.Close()

	client := NewRemoteClient(server.URL)

	if err := client.Verify(context.Background(), sampleVerifyInput()); err == nil {
		t.Fatalf("expected error on non-2xx response")
	}
}
