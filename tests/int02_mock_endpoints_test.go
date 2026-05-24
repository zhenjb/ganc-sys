package tests

import (
	"net/http"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestINT02GetStateLocalContract(t *testing.T) {
	server := newTestServer()

	rec := performRequest(t, server, http.MethodGet, "/api/state", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[types.AppState](t, rec)

	if body.Mode != "local" {
		t.Fatalf("expected mode=local, got %q", body.Mode)
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

	if body.BatchStatus != "none" {
		t.Fatalf("expected batchStatus=none, got %q", body.BatchStatus)
	}

	if body.LatestDeposit != nil {
		t.Fatalf("expected latestDeposit=nil for initial state")
	}

	if body.LatestSettlement != nil {
		t.Fatalf("expected latestSettlement=nil for initial state")
	}

	if body.LatestBatchCommitments != nil {
		t.Fatalf("expected latestBatchCommitments=nil for initial state")
	}

	if body.LatestWithdrawRecords != nil {
		t.Fatalf("expected latestWithdrawRecords=nil for initial state")
	}
}

func TestINT02WithdrawRequestLocalContract(t *testing.T) {
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

	if body.WithdrawRequest.Owner != "cosmos1alice" {
		t.Fatalf("expected owner=cosmos1alice, got %q", body.WithdrawRequest.Owner)
	}

	if body.WithdrawRequest.Denom != "uusdc" {
		t.Fatalf("expected denom=uusdc, got %q", body.WithdrawRequest.Denom)
	}

	if body.WithdrawRequest.Amount != "40" {
		t.Fatalf("expected amount=40, got %q", body.WithdrawRequest.Amount)
	}

	if body.WithdrawRequest.Destination != "cosmos1alice" {
		t.Fatalf("expected destination=cosmos1alice, got %q", body.WithdrawRequest.Destination)
	}

	if body.State.WithdrawStatus != "requested" {
		t.Fatalf("expected withdrawStatus=requested, got %q", body.State.WithdrawStatus)
	}
}

func TestINT02BuildBatchLocalContract(t *testing.T) {
	server := newTestServer()

	depositResp := createDepositForTest(t, server, "100")
	withdrawResp := createWithdrawRequestForTest(t, server, "40")

	req := types.BuildBatchRequestBody{
		DepositIDs:  []string{depositResp.DepositRecord.DepositID},
		WithdrawIDs: []string{withdrawResp.WithdrawRequest.WithdrawID},
	}

	rec := performRequest(t, server, http.MethodPost, "/api/batch/build", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[types.BuildBatchResponse](t, rec)

	if body.SettlementUpdate.BatchID == "" {
		t.Fatalf("expected batchId to be generated")
	}

	if body.SettlementUpdate.OldStateRoot != "0xrootA" {
		t.Fatalf("expected oldStateRoot=0xrootA, got %q", body.SettlementUpdate.OldStateRoot)
	}

	if body.SettlementUpdate.NewStateRoot == "" {
		t.Fatalf("expected newStateRoot to be generated")
	}

	if len(body.SettlementUpdate.Deposits) != 1 {
		t.Fatalf("expected one deposit in local batch, got %d", len(body.SettlementUpdate.Deposits))
	}

	if body.SettlementUpdate.Deposits[0].DepositID != depositResp.DepositRecord.DepositID {
		t.Fatalf("expected depositId=%q, got %q", depositResp.DepositRecord.DepositID, body.SettlementUpdate.Deposits[0].DepositID)
	}

	if body.SettlementUpdate.Deposits[0].Owner != "cosmos1alice" {
		t.Fatalf("expected deposit owner=cosmos1alice, got %q", body.SettlementUpdate.Deposits[0].Owner)
	}

	if body.SettlementUpdate.Deposits[0].Amount != "100" {
		t.Fatalf("expected deposit amount=100, got %q", body.SettlementUpdate.Deposits[0].Amount)
	}

	if len(body.SettlementUpdate.Withdrawals) != 1 {
		t.Fatalf("expected one withdrawal in local batch, got %d", len(body.SettlementUpdate.Withdrawals))
	}

	if body.SettlementUpdate.Withdrawals[0].WithdrawID != withdrawResp.WithdrawRequest.WithdrawID {
		t.Fatalf("expected withdrawId=%q, got %q", withdrawResp.WithdrawRequest.WithdrawID, body.SettlementUpdate.Withdrawals[0].WithdrawID)
	}

	if body.SettlementUpdate.Withdrawals[0].DestinationHash == "" {
		t.Fatalf("expected destinationHash to be generated")
	}

	if body.SettlementUpdate.Withdrawals[0].DestinationHash == "0xmockdestinationhash" {
		t.Fatalf("expected computed destinationHash, got placeholder")
	}

	if body.SettlementUpdate.Withdrawals[0].Nullifier == "" {
		t.Fatalf("expected nullifier to be generated")
	}

	if body.SettlementUpdate.Withdrawals[0].Nullifier == "0xmocknullifier" {
		t.Fatalf("expected computed nullifier, got placeholder")
	}

	if body.BatchCommitments.DepositsRoot == "" {
		t.Fatalf("expected depositsRoot to be generated")
	}

	if body.BatchCommitments.DepositsRoot == "0xdepositsRoot" {
		t.Fatalf("expected computed depositsRoot, got placeholder")
	}

	if body.BatchCommitments.WithdrawalsRoot == "" {
		t.Fatalf("expected withdrawalsRoot to be generated")
	}

	if body.BatchCommitments.WithdrawalsRoot == "0xwithdrawalsRoot" {
		t.Fatalf("expected computed withdrawalsRoot, got placeholder")
	}

	if body.BatchCommitments.NullifiersRoot == "" {
		t.Fatalf("expected nullifiersRoot to be generated")
	}

	if body.BatchCommitments.NullifiersRoot == "0xnullifiersRoot" {
		t.Fatalf("expected computed nullifiersRoot, got placeholder")
	}

	if body.BatchCommitments.WithdrawOutputsRoot == "" {
		t.Fatalf("expected withdrawOutputsRoot to be generated")
	}

	if body.BatchCommitments.WithdrawOutputsRoot == "0xwithdrawOutputsRoot" {
		t.Fatalf("expected computed withdrawOutputsRoot, got placeholder")
	}

	if len(body.Witness.Accounts) != 1 {
		t.Fatalf("expected witness.accounts length 1, got %d", len(body.Witness.Accounts))
	}

	if body.Witness.Accounts[0].Owner != "cosmos1alice" {
		t.Fatalf("expected witness owner=cosmos1alice, got %q", body.Witness.Accounts[0].Owner)
	}

	if body.Witness.Accounts[0].UserSecret != "mock-user-secret" {
		t.Fatalf("expected witness userSecret=mock-user-secret, got %q", body.Witness.Accounts[0].UserSecret)
	}

	if body.Witness.Accounts[0].OldBalance != "0" {
		t.Fatalf("expected witness oldBalance=0, got %q", body.Witness.Accounts[0].OldBalance)
	}

	if body.Witness.Accounts[0].NewBalance != "60" {
		t.Fatalf("expected witness newBalance=60, got %q", body.Witness.Accounts[0].NewBalance)
	}

	if body.State.BatchStatus != "built" {
		t.Fatalf("expected batchStatus=built, got %q", body.State.BatchStatus)
	}

	if body.State.WithdrawStatus != "batchBuilt" {
		t.Fatalf("expected withdrawStatus=batchBuilt, got %q", body.State.WithdrawStatus)
	}
}

func TestINT02GenerateProofLocalContract(t *testing.T) {
	server := newTestServer()

	req := canonicalGenerateProofRequest()

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

	if body.ProofBundle.PublicInputs[0] != "0xrootA" {
		t.Fatalf("expected publicInputs[0]=oldStateRoot, got %q", body.ProofBundle.PublicInputs[0])
	}

	if body.ProofBundle.PublicInputs[1] != "0xrootB" {
		t.Fatalf("expected publicInputs[1]=newStateRoot, got %q", body.ProofBundle.PublicInputs[1])
	}

	if body.ProofBundle.PublicInputs[2] != "0xdepositsRoot" {
		t.Fatalf("expected publicInputs[2]=depositsRoot, got %q", body.ProofBundle.PublicInputs[2])
	}

	if body.ProofBundle.PublicInputs[3] != "0xwithdrawalsRoot" {
		t.Fatalf("expected publicInputs[3]=withdrawalsRoot, got %q", body.ProofBundle.PublicInputs[3])
	}

	if body.ProofBundle.PublicInputs[4] != "0xnullifiersRoot" {
		t.Fatalf("expected publicInputs[4]=nullifiersRoot, got %q", body.ProofBundle.PublicInputs[4])
	}

	if body.ProofBundle.PublicInputs[5] != "0xwithdrawOutputsRoot" {
		t.Fatalf("expected publicInputs[5]=withdrawOutputsRoot, got %q", body.ProofBundle.PublicInputs[5])
	}

	if body.State.ProofStatus != "ready" {
		t.Fatalf("expected proofStatus=ready, got %q", body.State.ProofStatus)
	}
}

func TestINT02SubmitBatchLocalContract(t *testing.T) {
	server := newTestServer()

	req := types.SubmitBatchRequestBody{
		SettlementUpdate: canonicalSettlementUpdate(),
		BatchCommitments: canonicalBatchCommitments(),
		ProofBundle: types.ProofBundle{
			Proof: "0xmockproof",
			PublicInputs: []string{
				"0xrootA",
				"0xrootB",
				"0xdepositsRoot",
				"0xwithdrawalsRoot",
				"0xnullifiersRoot",
				"0xwithdrawOutputsRoot",
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

	if body.BatchCommitments.DepositsRoot != "0xdepositsRoot" {
		t.Fatalf("expected depositsRoot=0xdepositsRoot, got %q", body.BatchCommitments.DepositsRoot)
	}

	if len(body.WithdrawRecords) != 1 {
		t.Fatalf("expected one withdrawRecord, got %d", len(body.WithdrawRecords))
	}

	if body.WithdrawRecords[0].WithdrawID != "wd-1" {
		t.Fatalf("expected withdrawId=wd-1, got %q", body.WithdrawRecords[0].WithdrawID)
	}

	if body.WithdrawRecords[0].Claimed {
		t.Fatalf("expected withdrawRecords[0].claimed=false after submit batch")
	}

	if body.State.CurrentStateRoot != "0xrootB" {
		t.Fatalf("expected currentStateRoot=0xrootB, got %q", body.State.CurrentStateRoot)
	}

	if body.State.DepositStatus != "processed" {
		t.Fatalf("expected depositStatus=processed, got %q", body.State.DepositStatus)
	}

	if body.State.ProofStatus != "accepted" {
		t.Fatalf("expected proofStatus=accepted, got %q", body.State.ProofStatus)
	}

	if body.State.WithdrawStatus != "readyToClaim" {
		t.Fatalf("expected withdrawStatus=readyToClaim, got %q", body.State.WithdrawStatus)
	}

	if body.State.BatchStatus != "accepted" {
		t.Fatalf("expected batchStatus=accepted, got %q", body.State.BatchStatus)
	}
}

func TestINT02ClaimWithdrawLocalContract(t *testing.T) {
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

	if body.Balances.UserBalances["cosmos1alice/uusdc"] != "940" {
		t.Fatalf("expected alice balance=940, got %q", body.Balances.UserBalances["cosmos1alice/uusdc"])
	}

	if body.Balances.ModuleAccountBalance["uusdc"] != "60" {
		t.Fatalf("expected module balance=60, got %q", body.Balances.ModuleAccountBalance["uusdc"])
	}

	if body.State.WithdrawStatus != "claimed" {
		t.Fatalf("expected withdrawStatus=claimed, got %q", body.State.WithdrawStatus)
	}
}

func canonicalGenerateProofRequest() types.GenerateProofRequestBody {
	return types.GenerateProofRequestBody{
		SettlementUpdate: canonicalSettlementUpdate(),
		BatchCommitments: canonicalBatchCommitments(),
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
}

func canonicalSettlementUpdate() types.SettlementUpdate {
	return types.SettlementUpdate{
		BatchID:      "batch-1",
		OldStateRoot: "0xrootA",
		NewStateRoot: "0xrootB",
		Deposits: []types.SettlementDeposit{
			{
				DepositID: "dep-1",
				Owner:     "cosmos1alice",
				Denom:     "uusdc",
				Amount:    "100",
			},
		},
		Withdrawals: []types.SettlementWithdrawal{
			{
				WithdrawID:      "wd-1",
				Owner:           "cosmos1alice",
				Denom:           "uusdc",
				Amount:          "40",
				Destination:     "cosmos1alice",
				DestinationHash: "0xmockdestinationhash",
				Nullifier:       "0xmocknullifier",
			},
		},
	}
}

func canonicalBatchCommitments() types.BatchCommitments {
	return types.BatchCommitments{
		DepositsRoot:        "0xdepositsRoot",
		WithdrawalsRoot:     "0xwithdrawalsRoot",
		NullifiersRoot:      "0xnullifiersRoot",
		WithdrawOutputsRoot: "0xwithdrawOutputsRoot",
	}
}
