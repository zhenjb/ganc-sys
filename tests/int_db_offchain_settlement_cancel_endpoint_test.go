package tests

import (
	"net/http"
	"os"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestDBOffchainSettlementCancelEndpointReopensIncludedBatch(t *testing.T) {
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("set RUN_DB_TESTS=1 to run postgres tests")
	}

	server := newTestServerWithPendingOffchainSettlement(t)

	depositResp := createDepositForTest(t, server, "100")
	withdrawResp := createWithdrawRequestForTest(t, server, "40")

	if depositResp.DepositRecord.DepositID == "" {
		t.Fatalf("expected deposit id")
	}

	if withdrawResp.WithdrawRequest.WithdrawID == "" {
		t.Fatalf("expected withdraw id")
	}

	buildRec := performRequest(t, server, http.MethodPost, "/api/batch/build", types.BuildBatchRequestBody{})
	if buildRec.Code != http.StatusOK {
		t.Fatalf("expected build batch status 200, got %d, body=%s", buildRec.Code, buildRec.Body.String())
	}

	buildResp := decodeJSON[types.BuildBatchResponse](t, buildRec)

	if buildResp.SettlementUpdate.BatchID == "" {
		t.Fatalf("expected batch id")
	}

	cancelRec := performRequest(
		t,
		server,
		http.MethodPost,
		"/api/internal/offchain-settlement/batches/"+buildResp.SettlementUpdate.BatchID+"/cancel",
		types.CancelOffchainSettlementBatchRequestBody{
			Reason: "proof generation failed",
		},
	)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("expected cancel status 200, got %d, body=%s", cancelRec.Code, cancelRec.Body.String())
	}

	cancelResp := decodeJSON[types.CancelOffchainSettlementBatchResponse](t, cancelRec)

	if cancelResp.BatchID != buildResp.SettlementUpdate.BatchID {
		t.Fatalf("expected batchId=%q, got %q", buildResp.SettlementUpdate.BatchID, cancelResp.BatchID)
	}

	if cancelResp.Status != "reopened" {
		t.Fatalf("expected status=reopened, got %q", cancelResp.Status)
	}

	if cancelResp.Reason != "proof generation failed" {
		t.Fatalf("expected reason=proof generation failed, got %q", cancelResp.Reason)
	}

	retryBuildRec := performRequest(t, server, http.MethodPost, "/api/batch/build", types.BuildBatchRequestBody{})
	if retryBuildRec.Code != http.StatusOK {
		t.Fatalf("expected retry build batch status 200, got %d, body=%s", retryBuildRec.Code, retryBuildRec.Body.String())
	}

	retryBuildResp := decodeJSON[types.BuildBatchResponse](t, retryBuildRec)

	if retryBuildResp.SettlementUpdate.BatchID == "" {
		t.Fatalf("expected retry batch id")
	}

	if retryBuildResp.SettlementUpdate.BatchID == buildResp.SettlementUpdate.BatchID {
		t.Fatalf("expected retry batch id to advance")
	}

	if retryBuildResp.SettlementUpdate.OldStateRoot != buildResp.SettlementUpdate.OldStateRoot {
		t.Fatalf(
			"expected retry oldStateRoot=%q, got %q",
			buildResp.SettlementUpdate.OldStateRoot,
			retryBuildResp.SettlementUpdate.OldStateRoot,
		)
	}

	if retryBuildResp.SettlementUpdate.NewStateRoot != buildResp.SettlementUpdate.NewStateRoot {
		t.Fatalf(
			"expected retry newStateRoot=%q, got %q",
			buildResp.SettlementUpdate.NewStateRoot,
			retryBuildResp.SettlementUpdate.NewStateRoot,
		)
	}
}
