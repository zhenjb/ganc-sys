package tests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestINT09SubmitBatchUsesProofAndSettlementOutput(t *testing.T) {
	server := newTestServer()

	buildBody := buildBatchForSubmitTest(t, server)
	proofBody := generateProofForSubmitTest(t, server, buildBody)

	submitReq := types.SubmitBatchRequestBody{
		SettlementUpdate: buildBody.SettlementUpdate,
		BatchCommitments: buildBody.BatchCommitments,
		ProofBundle:      proofBody.ProofBundle,
	}

	submitRec := performRequest(t, server, http.MethodPost, "/api/batch/submit", submitReq)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("expected submit batch status 200, got %d, body=%s", submitRec.Code, submitRec.Body.String())
	}

	body := decodeJSON[types.SubmitBatchResponse](t, submitRec)

	if body.TxHash == "" {
		t.Fatalf("expected txHash to be generated")
	}

	if !strings.HasPrefix(body.TxHash, "0x") {
		t.Fatalf("expected txHash to start with 0x, got %q", body.TxHash)
	}

	if !body.Accepted {
		t.Fatalf("expected accepted=true")
	}

	if body.ProofStatus != "accepted" {
		t.Fatalf("expected proofStatus=accepted, got %q", body.ProofStatus)
	}

	if body.State.CurrentStateRoot != buildBody.SettlementUpdate.NewStateRoot {
		t.Fatalf("expected currentStateRoot=%q, got %q", buildBody.SettlementUpdate.NewStateRoot, body.State.CurrentStateRoot)
	}

	if body.State.DepositStatus != "processed" {
		t.Fatalf("expected depositStatus=processed, got %q", body.State.DepositStatus)
	}

	if body.State.WithdrawStatus != "readyToClaim" {
		t.Fatalf("expected withdrawStatus=readyToClaim, got %q", body.State.WithdrawStatus)
	}

	if body.State.BatchStatus != "accepted" {
		t.Fatalf("expected batchStatus=accepted, got %q", body.State.BatchStatus)
	}

	if len(body.WithdrawRecords) != len(buildBody.SettlementUpdate.Withdrawals) {
		t.Fatalf("expected %d withdrawRecords, got %d", len(buildBody.SettlementUpdate.Withdrawals), len(body.WithdrawRecords))
	}

	withdrawal := buildBody.SettlementUpdate.Withdrawals[0]
	record := body.WithdrawRecords[0]

	if record.WithdrawID != withdrawal.WithdrawID {
		t.Fatalf("expected withdrawId=%q, got %q", withdrawal.WithdrawID, record.WithdrawID)
	}

	if record.Owner != withdrawal.Owner {
		t.Fatalf("expected owner=%q, got %q", withdrawal.Owner, record.Owner)
	}

	if record.Amount != withdrawal.Amount {
		t.Fatalf("expected amount=%q, got %q", withdrawal.Amount, record.Amount)
	}

	if record.Nullifier != withdrawal.Nullifier {
		t.Fatalf("expected nullifier=%q, got %q", withdrawal.Nullifier, record.Nullifier)
	}

	if record.Claimed {
		t.Fatalf("expected withdraw record claimed=false after batch submit")
	}
}

func TestINT09SubmitBatchRejectsMismatchedPublicInputs(t *testing.T) {
	server := newTestServer()

	buildBody := buildBatchForSubmitTest(t, server)
	proofBody := generateProofForSubmitTest(t, server, buildBody)

	proofBody.ProofBundle.PublicInputs[1] = "0xbadroot"

	submitReq := types.SubmitBatchRequestBody{
		SettlementUpdate: buildBody.SettlementUpdate,
		BatchCommitments: buildBody.BatchCommitments,
		ProofBundle:      proofBody.ProofBundle,
	}

	submitRec := performRequest(t, server, http.MethodPost, "/api/batch/submit", submitReq)
	if submitRec.Code != http.StatusBadRequest {
		t.Fatalf("expected submit batch status 400, got %d, body=%s", submitRec.Code, submitRec.Body.String())
	}

	body := decodeJSON[map[string]string](t, submitRec)

	if body["error"] != "proof public inputs do not match settlement" {
		t.Fatalf("expected public input mismatch error, got %q", body["error"])
	}
}

func TestINT09SubmitBatchRejectsMissingPublicInputs(t *testing.T) {
	server := newTestServer()

	buildBody := buildBatchForSubmitTest(t, server)

	submitReq := types.SubmitBatchRequestBody{
		SettlementUpdate: buildBody.SettlementUpdate,
		BatchCommitments: buildBody.BatchCommitments,
		ProofBundle: types.ProofBundle{
			Proof:             "0xlocalproof",
			PublicInputs:      []string{"0xrootA"},
			VerificationKeyID: "local-v1",
		},
	}

	submitRec := performRequest(t, server, http.MethodPost, "/api/batch/submit", submitReq)
	if submitRec.Code != http.StatusBadRequest {
		t.Fatalf("expected submit batch status 400, got %d, body=%s", submitRec.Code, submitRec.Body.String())
	}

	body := decodeJSON[map[string]string](t, submitRec)

	if body["error"] != "proof public inputs must contain 6 values" {
		t.Fatalf("expected public input length error, got %q", body["error"])
	}
}

func buildBatchForSubmitTest(t *testing.T, server http.Handler) types.BuildBatchResponse {
	t.Helper()

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

	return decodeJSON[types.BuildBatchResponse](t, buildRec)
}

func generateProofForSubmitTest(
	t *testing.T,
	server http.Handler,
	buildBody types.BuildBatchResponse,
) types.GenerateProofResponse {
	t.Helper()

	proofReq := types.GenerateProofRequestBody{
		SettlementUpdate: buildBody.SettlementUpdate,
		BatchCommitments: buildBody.BatchCommitments,
		Witness:          buildBody.Witness,
	}

	proofRec := performRequest(t, server, http.MethodPost, "/api/proof/generate", proofReq)
	if proofRec.Code != http.StatusOK {
		t.Fatalf("expected proof status 200, got %d, body=%s", proofRec.Code, proofRec.Body.String())
	}

	return decodeJSON[types.GenerateProofResponse](t, proofRec)
}
