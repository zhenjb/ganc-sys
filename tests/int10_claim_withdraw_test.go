package tests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestINT10ClaimWithdrawAfterBatchSubmit(t *testing.T) {
	server := newTestServer()

	submitBody := submitBatchForClaimTest(t, server)

	if len(submitBody.WithdrawRecords) != 1 {
		t.Fatalf("expected one withdraw record after submit")
	}

	withdrawID := submitBody.WithdrawRecords[0].WithdrawID

	claimReq := types.ClaimWithdrawRequestBody{
		WithdrawID: withdrawID,
	}

	claimRec := performRequest(t, server, http.MethodPost, "/api/withdraw/claim", claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("expected claim status 200, got %d, body=%s", claimRec.Code, claimRec.Body.String())
	}

	body := decodeJSON[types.ClaimWithdrawResponse](t, claimRec)

	if body.TxHash == "" {
		t.Fatalf("expected txHash to be generated")
	}

	if !strings.HasPrefix(body.TxHash, "0x") {
		t.Fatalf("expected txHash to start with 0x, got %q", body.TxHash)
	}

	if body.WithdrawRecord.WithdrawID != withdrawID {
		t.Fatalf("expected withdrawId=%q, got %q", withdrawID, body.WithdrawRecord.WithdrawID)
	}

	if !body.WithdrawRecord.Claimed {
		t.Fatalf("expected withdrawRecord.claimed=true")
	}

	if body.State.WithdrawStatus != "claimed" {
		t.Fatalf("expected withdrawStatus=claimed, got %q", body.State.WithdrawStatus)
	}

	balanceKey := body.WithdrawRecord.Destination + "/" + body.WithdrawRecord.Denom
	if body.Balances.UserBalances[balanceKey] != body.WithdrawRecord.Amount {
		t.Fatalf(
			"expected user balance %s=%s, got %q",
			balanceKey,
			body.WithdrawRecord.Amount,
			body.Balances.UserBalances[balanceKey],
		)
	}
}

func TestINT10ClaimWithdrawRejectsUnknownWithdrawRecord(t *testing.T) {
	server := newTestServer()

	claimReq := types.ClaimWithdrawRequestBody{
		WithdrawID: "unknown",
	}

	claimRec := performRequest(t, server, http.MethodPost, "/api/withdraw/claim", claimReq)
	if claimRec.Code != http.StatusNotFound {
		t.Fatalf("expected claim status 404, got %d, body=%s", claimRec.Code, claimRec.Body.String())
	}

	body := decodeJSON[map[string]string](t, claimRec)

	if body["error"] != "withdraw record not found" {
		t.Fatalf("expected withdraw record not found error, got %q", body["error"])
	}
}

func TestINT10ClaimWithdrawRejectsDoubleClaim(t *testing.T) {
	server := newTestServer()

	submitBody := submitBatchForClaimTest(t, server)
	withdrawID := submitBody.WithdrawRecords[0].WithdrawID

	claimReq := types.ClaimWithdrawRequestBody{
		WithdrawID: withdrawID,
	}

	firstClaimRec := performRequest(t, server, http.MethodPost, "/api/withdraw/claim", claimReq)
	if firstClaimRec.Code != http.StatusOK {
		t.Fatalf("expected first claim status 200, got %d, body=%s", firstClaimRec.Code, firstClaimRec.Body.String())
	}

	secondClaimRec := performRequest(t, server, http.MethodPost, "/api/withdraw/claim", claimReq)
	if secondClaimRec.Code != http.StatusBadRequest {
		t.Fatalf("expected second claim status 400, got %d, body=%s", secondClaimRec.Code, secondClaimRec.Body.String())
	}

	body := decodeJSON[map[string]string](t, secondClaimRec)

	if body["error"] != "withdraw already claimed" {
		t.Fatalf("expected withdraw already claimed error, got %q", body["error"])
	}
}

func submitBatchForClaimTest(t *testing.T, server http.Handler) types.SubmitBatchResponse {
	t.Helper()

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

	return decodeJSON[types.SubmitBatchResponse](t, submitRec)
}
