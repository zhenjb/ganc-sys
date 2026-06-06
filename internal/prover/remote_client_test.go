package prover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestRemoteClientGenerateProofCallsProverService(t *testing.T) {
	var received remoteProveRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		if r.URL.Path != "/prove" {
			t.Fatalf("expected /prove, got %s", r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteProveResponse{
			ProofBundle: types.ProofBundle{
				Proof: "0xremoteproof",
				PublicInputs: []string{
					"0xrootA",
					"0xrootB",
					"0xdepositsRoot",
					"0xwithdrawalsRoot",
					"0xnullifiersRoot",
					"0xwithdrawOutputsRoot",
				},
				VerificationKeyID: "gazk-balance-smoke-v1",
			},
		})
	}))
	defer server.Close()

	client := NewRemoteClient(server.URL)

	proofBundle, err := client.GenerateProof(context.Background(), GenerateProofInput{
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
		Witness: types.Witness{
			Accounts: []types.WitnessAccount{
				{
					Owner:      "cosmos1alice",
					UserSecret: "mock-user-secret",
					Nonce:      "1",
					OldBalance: "0",
					NewBalance: "60",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("generate proof: %v", err)
	}

	if received.SettlementUpdate.BatchID != "batch-1" {
		t.Fatalf("expected remote request batchId=batch-1, got %q", received.SettlementUpdate.BatchID)
	}

	if proofBundle.Proof != "0xremoteproof" {
		t.Fatalf("expected remote proof, got %q", proofBundle.Proof)
	}

	if len(proofBundle.PublicInputs) != 6 {
		t.Fatalf("expected 6 public inputs, got %d", len(proofBundle.PublicInputs))
	}

	if proofBundle.VerificationKeyID != "gazk-balance-smoke-v1" {
		t.Fatalf("expected gazk verification key id, got %q", proofBundle.VerificationKeyID)
	}
}

func TestRemoteClientGenerateProofReturnsRemoteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(remoteErrorResponse{
			Error: "invalid prove request: balance transition violated",
		})
	}))
	defer server.Close()

	client := NewRemoteClient(server.URL)

	_, err := client.GenerateProof(context.Background(), GenerateProofInput{
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
		Witness: types.Witness{
			Accounts: []types.WitnessAccount{
				{
					Owner:      "cosmos1alice",
					UserSecret: "mock-user-secret",
					Nonce:      "1",
					OldBalance: "0",
					NewBalance: "70",
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected remote error")
	}
}
