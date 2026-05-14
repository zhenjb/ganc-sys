package tests

import (
	"net/http"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestINT02GetStateMockContract(t *testing.T) {
	server := newTestServer()

	rec := performRequest(t, server, http.MethodGet, "/api/state", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[types.AppState](t, rec)

	if body.Mode != "mock" {
		t.Fatalf("expected mode=mock, got %q", body.Mode)
	}

	if body.CurrentStateRoot != "0xrootA" {
		t.Fatalf("expected currentStateRoot=0xrootA, got %q", body.CurrentStateRoot)
	}

	if body.UserBalances["cosmos1alice/uusdc"] != "1000" {
		t.Fatalf("expected alice balance 1000, got %q", body.UserBalances["cosmos1alice/uusdc"])
	}

	if body.ModuleAccountBalance["uusdc"] != "0" {
		t.Fatalf("expected module balance 0, got %q", body.ModuleAccountBalance["uusdc"])
	}

	if body.ProofStatus != "idle" {
		t.Fatalf("expected proofStatus=idle, got %q", body.ProofStatus)
	}

	if body.DepositStatus != "none" {
		t.Fatalf("expected depositStatus=none, got %q", body.DepositStatus)
	}

	if body.WithdrawStatus != "none" {
		t.Fatalf("expected withdrawStatus=none, got %q", body.WithdrawStatus)
	}

	if body.LatestDeposit != nil {
		t.Fatalf("expected latestDeposit=nil for initial state")
	}
}

func TestINT02WithdrawRequestMockContract(t *testing.T) {
	server := newTestServer()

	req := types.WithdrawRequestBody{
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
	}

	rec := performRequest(t, server, http.MethodPost, "/api/withdraw-request", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[types.WithdrawRequestResponse](t, rec)

	if body.WithdrawRequest.WithdrawID != "wd-1" {
		t.Fatalf("expected withdrawId=wd-1, got %q", body.WithdrawRequest.WithdrawID)
	}

	if body.WithdrawRequest.Amount != "40" {
		t.Fatalf("expected amount=40, got %q", body.WithdrawRequest.Amount)
	}

	if body.State.WithdrawStatus != "requested" {
		t.Fatalf("expected withdrawStatus=requested, got %q", body.State.WithdrawStatus)
	}
}

func TestINT02BuildBatchMockContract(t *testing.T) {
	server := newTestServer()

	req := types.BuildBatchRequestBody{
		DepositID:  "dep-1",
		WithdrawID: "wd-1",
	}

	rec := performRequest(t, server, http.MethodPost, "/api/batch/build", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[types.BuildBatchResponse](t, rec)

	if body.SettlementUpdate.BatchID != "batch-1" {
		t.Fatalf("expected batchId=batch-1, got %q", body.SettlementUpdate.BatchID)
	}

	if body.SettlementUpdate.OldStateRoot != "0xrootA" {
		t.Fatalf("expected oldStateRoot=0xrootA, got %q", body.SettlementUpdate.OldStateRoot)
	}

	if body.SettlementUpdate.NewStateRoot != "0xrootB" {
		t.Fatalf("expected newStateRoot=0xrootB, got %q", body.SettlementUpdate.NewStateRoot)
	}

	if body.Witness.NewBalance != "60" {
		t.Fatalf("expected witness newBalance=60, got %q", body.Witness.NewBalance)
	}

	if body.State.WithdrawStatus != "batchBuilt" {
		t.Fatalf("expected withdrawStatus=batchBuilt, got %q", body.State.WithdrawStatus)
	}
}

func TestINT02GenerateProofMockContract(t *testing.T) {
	server := newTestServer()

	req := types.GenerateProofRequestBody{
		SettlementUpdate: types.SettlementUpdate{
			BatchID:             "batch-1",
			OldStateRoot:        "0xrootA",
			NewStateRoot:        "0xrootB",
			DepositID:           "dep-1",
			DepositAmount:       "100",
			WithdrawID:          "wd-1",
			WithdrawAmount:      "40",
			WithdrawAddress:     "cosmos1alice",
			WithdrawAddressHash: "0xmockaddresshash",
			Nullifier:           "0xmocknullifier",
		},
		Witness: types.Witness{
			UserSecret: "mock-user-secret",
			Nonce:      "1",
			OldBalance: "0",
			NewBalance: "60",
		},
	}

	rec := performRequest(t, server, http.MethodPost, "/api/proof/generate", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[types.GenerateProofResponse](t, rec)

	if body.ProofBundle.Proof != "0xmockproof" {
		t.Fatalf("expected proof=0xmockproof, got %q", body.ProofBundle.Proof)
	}

	if len(body.ProofBundle.PublicInputs) != 6 {
		t.Fatalf("expected 6 public inputs, got %d", len(body.ProofBundle.PublicInputs))
	}

	if body.ProofBundle.PublicInputs[4] != "0xmockaddresshash" {
		t.Fatalf("expected publicInputs[4]=withdrawAddressHash, got %q", body.ProofBundle.PublicInputs[4])
	}

	if body.ProofBundle.PublicInputs[5] != "0xmocknullifier" {
		t.Fatalf("expected publicInputs[5]=nullifier, got %q", body.ProofBundle.PublicInputs[5])
	}

	if body.State.ProofStatus != "ready" {
		t.Fatalf("expected proofStatus=ready, got %q", body.State.ProofStatus)
	}
}

func TestINT02SubmitBatchMockContract(t *testing.T) {
	server := newTestServer()

	req := types.SubmitBatchRequestBody{
		SettlementUpdate: types.SettlementUpdate{
			BatchID:             "batch-1",
			OldStateRoot:        "0xrootA",
			NewStateRoot:        "0xrootB",
			DepositID:           "dep-1",
			DepositAmount:       "100",
			WithdrawID:          "wd-1",
			WithdrawAmount:      "40",
			WithdrawAddress:     "cosmos1alice",
			WithdrawAddressHash: "0xmockaddresshash",
			Nullifier:           "0xmocknullifier",
		},
		ProofBundle: types.ProofBundle{
			Proof: "0xmockproof",
			PublicInputs: []string{
				"0xrootA",
				"0xrootB",
				"100",
				"40",
				"0xmockaddresshash",
				"0xmocknullifier",
			},
			VerificationKeyID: "v1",
		},
	}

	rec := performRequest(t, server, http.MethodPost, "/api/batch/submit", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[types.SubmitBatchResponse](t, rec)

	if !body.Accepted {
		t.Fatalf("expected accepted=true")
	}

	if body.ProofStatus != "accepted" {
		t.Fatalf("expected proofStatus=accepted, got %q", body.ProofStatus)
	}

	if body.WithdrawRecord.Claimed {
		t.Fatalf("expected withdrawRecord.claimed=false after submit batch")
	}

	if body.State.WithdrawStatus != "readyToClaim" {
		t.Fatalf("expected withdrawStatus=readyToClaim, got %q", body.State.WithdrawStatus)
	}
}

func TestINT02ClaimWithdrawMockContract(t *testing.T) {
	server := newTestServer()

	req := types.ClaimWithdrawRequestBody{
		WithdrawID: "wd-1",
	}

	rec := performRequest(t, server, http.MethodPost, "/api/withdraw/claim", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[types.ClaimWithdrawResponse](t, rec)

	if !body.WithdrawRecord.Claimed {
		t.Fatalf("expected withdrawRecord.claimed=true after claim")
	}

	if body.Balances["cosmos1alice/uusdc"] != "940" {
		t.Fatalf("expected alice balance=940, got %q", body.Balances["cosmos1alice/uusdc"])
	}

	if body.Balances["moduleAccount/uusdc"] != "60" {
		t.Fatalf("expected module balance=60, got %q", body.Balances["moduleAccount/uusdc"])
	}

	if body.State.WithdrawStatus != "claimed" {
		t.Fatalf("expected withdrawStatus=claimed, got %q", body.State.WithdrawStatus)
	}
}
