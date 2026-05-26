package tests

import (
	"net/http"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestINT11InitialDashboardState(t *testing.T) {
	server := newTestServer()

	state := getStateForTest(t, server)

	if state.Mode != "local" {
		t.Fatalf("expected mode=local, got %q", state.Mode)
	}

	if state.CurrentStateRoot != "0xrootA" {
		t.Fatalf("expected currentStateRoot=0xrootA, got %q", state.CurrentStateRoot)
	}

	if state.DepositStatus != "none" {
		t.Fatalf("expected depositStatus=none, got %q", state.DepositStatus)
	}

	if state.WithdrawStatus != "none" {
		t.Fatalf("expected withdrawStatus=none, got %q", state.WithdrawStatus)
	}

	if state.ProofStatus != "idle" {
		t.Fatalf("expected proofStatus=idle, got %q", state.ProofStatus)
	}

	if state.BatchStatus != "none" {
		t.Fatalf("expected batchStatus=none, got %q", state.BatchStatus)
	}

	if state.LatestDeposit != nil {
		t.Fatalf("expected latestDeposit=nil")
	}

	if state.LatestWithdrawRequest != nil {
		t.Fatalf("expected latestWithdrawRequest=nil")
	}

	if state.LatestSettlement != nil {
		t.Fatalf("expected latestSettlement=nil")
	}

	if state.LatestBatchCommitments != nil {
		t.Fatalf("expected latestBatchCommitments=nil")
	}

	if state.LatestProof != nil {
		t.Fatalf("expected latestProof=nil")
	}

	if state.LatestWithdrawRecords != nil {
		t.Fatalf("expected latestWithdrawRecords=nil")
	}
}

func TestINT11DashboardStateTracksFullFlow(t *testing.T) {
	server := newTestServer()

	depositResp := createDepositForTest(t, server, "100")

	stateAfterDeposit := getStateForTest(t, server)

	if stateAfterDeposit.LatestDeposit == nil {
		t.Fatalf("expected latestDeposit after deposit")
	}

	if stateAfterDeposit.LatestDeposit.DepositID != depositResp.DepositRecord.DepositID {
		t.Fatalf("expected latestDeposit=%q, got %q", depositResp.DepositRecord.DepositID, stateAfterDeposit.LatestDeposit.DepositID)
	}

	if stateAfterDeposit.DepositStatus != "indexed" {
		t.Fatalf("expected depositStatus=indexed, got %q", stateAfterDeposit.DepositStatus)
	}

	if stateAfterDeposit.UserBalances["cosmos1alice/uusdc"] != "900" {
		t.Fatalf("expected user balance after deposit=900, got %q", stateAfterDeposit.UserBalances["cosmos1alice/uusdc"])
	}

	if stateAfterDeposit.ModuleAccountBalance["uusdc"] != "100" {
		t.Fatalf("expected module balance after deposit=100, got %q", stateAfterDeposit.ModuleAccountBalance["uusdc"])
	}

	withdrawResp := createWithdrawRequestForTest(t, server, "40")

	stateAfterWithdrawRequest := getStateForTest(t, server)

	if stateAfterWithdrawRequest.LatestWithdrawRequest == nil {
		t.Fatalf("expected latestWithdrawRequest after withdraw request")
	}

	if stateAfterWithdrawRequest.LatestWithdrawRequest.WithdrawID != withdrawResp.WithdrawRequest.WithdrawID {
		t.Fatalf(
			"expected latestWithdrawRequest=%q, got %q",
			withdrawResp.WithdrawRequest.WithdrawID,
			stateAfterWithdrawRequest.LatestWithdrawRequest.WithdrawID,
		)
	}

	if stateAfterWithdrawRequest.WithdrawStatus != "requested" {
		t.Fatalf("expected withdrawStatus=requested, got %q", stateAfterWithdrawRequest.WithdrawStatus)
	}

	buildReq := types.BuildBatchRequestBody{
		DepositIDs:  []string{depositResp.DepositRecord.DepositID},
		WithdrawIDs: []string{withdrawResp.WithdrawRequest.WithdrawID},
	}

	buildRec := performRequest(t, server, http.MethodPost, "/api/batch/build", buildReq)
	if buildRec.Code != http.StatusOK {
		t.Fatalf("expected build batch status 200, got %d, body=%s", buildRec.Code, buildRec.Body.String())
	}

	buildBody := decodeJSON[types.BuildBatchResponse](t, buildRec)

	stateAfterBuild := getStateForTest(t, server)

	if stateAfterBuild.LatestSettlement == nil {
		t.Fatalf("expected latestSettlement after batch build")
	}

	if stateAfterBuild.LatestBatchCommitments == nil {
		t.Fatalf("expected latestBatchCommitments after batch build")
	}

	if stateAfterBuild.LatestSettlement.BatchID != buildBody.SettlementUpdate.BatchID {
		t.Fatalf("expected latestSettlement batchId=%q, got %q", buildBody.SettlementUpdate.BatchID, stateAfterBuild.LatestSettlement.BatchID)
	}

	if stateAfterBuild.BatchStatus != "built" {
		t.Fatalf("expected batchStatus=built, got %q", stateAfterBuild.BatchStatus)
	}

	if stateAfterBuild.WithdrawStatus != "batchBuilt" {
		t.Fatalf("expected withdrawStatus=batchBuilt, got %q", stateAfterBuild.WithdrawStatus)
	}

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

	stateAfterProof := getStateForTest(t, server)

	if stateAfterProof.LatestProof == nil {
		t.Fatalf("expected latestProof after proof generate")
	}

	if stateAfterProof.LatestProof.Proof != proofBody.ProofBundle.Proof {
		t.Fatalf("expected latestProof to match generated proof")
	}

	if stateAfterProof.ProofStatus != "ready" {
		t.Fatalf("expected proofStatus=ready, got %q", stateAfterProof.ProofStatus)
	}

	submitReq := types.SubmitBatchRequestBody{
		SettlementUpdate: buildBody.SettlementUpdate,
		BatchCommitments: buildBody.BatchCommitments,
		ProofBundle:      proofBody.ProofBundle,
	}

	submitRec := performRequest(t, server, http.MethodPost, "/api/batch/submit", submitReq)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("expected submit status 200, got %d, body=%s", submitRec.Code, submitRec.Body.String())
	}

	submitBody := decodeJSON[types.SubmitBatchResponse](t, submitRec)

	stateAfterSubmit := getStateForTest(t, server)

	if stateAfterSubmit.CurrentStateRoot != buildBody.SettlementUpdate.NewStateRoot {
		t.Fatalf("expected currentStateRoot=%q, got %q", buildBody.SettlementUpdate.NewStateRoot, stateAfterSubmit.CurrentStateRoot)
	}

	if len(stateAfterSubmit.LatestWithdrawRecords) != 1 {
		t.Fatalf("expected one latestWithdrawRecord, got %d", len(stateAfterSubmit.LatestWithdrawRecords))
	}

	if stateAfterSubmit.LatestWithdrawRecords[0].WithdrawID != submitBody.WithdrawRecords[0].WithdrawID {
		t.Fatalf("expected latest withdraw record to match submit response")
	}

	if stateAfterSubmit.DepositStatus != "processed" {
		t.Fatalf("expected depositStatus=processed, got %q", stateAfterSubmit.DepositStatus)
	}

	if stateAfterSubmit.ProofStatus != "accepted" {
		t.Fatalf("expected proofStatus=accepted, got %q", stateAfterSubmit.ProofStatus)
	}

	if stateAfterSubmit.BatchStatus != "accepted" {
		t.Fatalf("expected batchStatus=accepted, got %q", stateAfterSubmit.BatchStatus)
	}

	if stateAfterSubmit.WithdrawStatus != "readyToClaim" {
		t.Fatalf("expected withdrawStatus=readyToClaim, got %q", stateAfterSubmit.WithdrawStatus)
	}

	claimReq := types.ClaimWithdrawRequestBody{
		WithdrawID: submitBody.WithdrawRecords[0].WithdrawID,
	}

	claimRec := performRequest(t, server, http.MethodPost, "/api/withdraw/claim", claimReq)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("expected claim status 200, got %d, body=%s", claimRec.Code, claimRec.Body.String())
	}

	stateAfterClaim := getStateForTest(t, server)

	if len(stateAfterClaim.LatestWithdrawRecords) != 1 {
		t.Fatalf("expected one latestWithdrawRecord after claim, got %d", len(stateAfterClaim.LatestWithdrawRecords))
	}

	if !stateAfterClaim.LatestWithdrawRecords[0].Claimed {
		t.Fatalf("expected latestWithdrawRecords[0].claimed=true")
	}

	if stateAfterClaim.WithdrawStatus != "claimed" {
		t.Fatalf("expected withdrawStatus=claimed, got %q", stateAfterClaim.WithdrawStatus)
	}

	if stateAfterClaim.UserBalances["cosmos1alice/uusdc"] != "940" {
		t.Fatalf("expected final user balance=940, got %q", stateAfterClaim.UserBalances["cosmos1alice/uusdc"])
	}

	if stateAfterClaim.ModuleAccountBalance["uusdc"] != "60" {
		t.Fatalf("expected final module balance=60, got %q", stateAfterClaim.ModuleAccountBalance["uusdc"])
	}
}

func getStateForTest(t *testing.T, server http.Handler) types.AppState {
	t.Helper()

	rec := performRequest(t, server, http.MethodGet, "/api/state", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected state status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	return decodeJSON[types.AppState](t, rec)
}
