package tests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestINT08GenerateProofUsesBatchBuildOutput(t *testing.T) {
	server := newTestServer()

	depositResp := createDepositForTest(t, server, "100")
	withdrawResp := createWithdrawRequestForTest(t, server, "40")

	buildReq := types.BuildBatchRequestBody{
		DepositIDs:  []string{depositResp.DepositRecord.DepositID},
		WithdrawIDs: []string{withdrawResp.WithdrawRequest.WithdrawID},
	}

	buildRec := performRequest(t, server, http.MethodPost, "/api/batch/build", buildReq)
	if buildRec.Code != http.StatusOK {
		t.Fatalf("expected build batch status 200, got %d, body=%s", buildRec.Code, buildRec.Body.String())
	}

	buildBody := decodeJSON[types.BuildBatchResponse](t, buildRec)

	proofReq := types.GenerateProofRequestBody{
		SettlementUpdate: buildBody.SettlementUpdate,
		BatchCommitments: buildBody.BatchCommitments,
		Witness:          buildBody.Witness,
	}

	proofRec := performRequest(t, server, http.MethodPost, "/api/proof/generate", proofReq)
	if proofRec.Code != http.StatusOK {
		t.Fatalf("expected proof status 200, got %d, body=%s", proofRec.Code, proofRec.Body.String())
	}

	proofBody := decodeJSON[types.GenerateProofResponse](t, proofRec)

	if proofBody.ProofBundle.Proof == "" {
		t.Fatalf("expected proof to be generated")
	}

	if !strings.HasPrefix(proofBody.ProofBundle.Proof, "0x") {
		t.Fatalf("expected proof to start with 0x, got %q", proofBody.ProofBundle.Proof)
	}

	if proofBody.ProofBundle.Proof == "0xmockproof" {
		t.Fatalf("expected local prover generated proof, got old fixture")
	}

	if proofBody.ProofBundle.VerificationKeyID != "local-v1" {
		t.Fatalf("expected verificationKeyId=local-v1, got %q", proofBody.ProofBundle.VerificationKeyID)
	}

	expectedPublicInputs := []string{
		buildBody.SettlementUpdate.OldStateRoot,
		buildBody.SettlementUpdate.NewStateRoot,
		buildBody.BatchCommitments.DepositsRoot,
		buildBody.BatchCommitments.WithdrawalsRoot,
		buildBody.BatchCommitments.NullifiersRoot,
		buildBody.BatchCommitments.WithdrawOutputsRoot,
	}

	if len(proofBody.ProofBundle.PublicInputs) != len(expectedPublicInputs) {
		t.Fatalf("expected %d public inputs, got %d", len(expectedPublicInputs), len(proofBody.ProofBundle.PublicInputs))
	}

	for i := range expectedPublicInputs {
		if proofBody.ProofBundle.PublicInputs[i] != expectedPublicInputs[i] {
			t.Fatalf("expected publicInputs[%d]=%q, got %q", i, expectedPublicInputs[i], proofBody.ProofBundle.PublicInputs[i])
		}
	}

	if proofBody.State.ProofStatus != "ready" {
		t.Fatalf("expected proofStatus=ready, got %q", proofBody.State.ProofStatus)
	}
}

func TestINT08GenerateProofRejectsMissingWitnessAccounts(t *testing.T) {
	server := newTestServer()

	req := types.GenerateProofRequestBody{
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
		Witness: types.Witness{},
	}

	rec := performRequest(t, server, http.MethodPost, "/api/proof/generate", req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]string](t, rec)

	if body["error"] != "witness.accounts is required" {
		t.Fatalf("expected witness.accounts error, got %q", body["error"])
	}
}

func TestINT08GenerateProofRejectsIncompleteCommitments(t *testing.T) {
	server := newTestServer()

	req := types.GenerateProofRequestBody{
		SettlementUpdate: types.SettlementUpdate{
			BatchID:      "batch-1",
			OldStateRoot: "0xrootA",
			NewStateRoot: "0xrootB",
		},
		BatchCommitments: types.BatchCommitments{
			DepositsRoot: "0xdepositsRoot",
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
	}

	rec := performRequest(t, server, http.MethodPost, "/api/proof/generate", req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]string](t, rec)

	if body["error"] != "batchCommitments are incomplete" {
		t.Fatalf("expected incomplete commitments error, got %q", body["error"])
	}
}
