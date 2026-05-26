package tests

import (
	"net/http"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestINT05DepositIsIndexedAfterCreateDeposit(t *testing.T) {
	server := newTestServer()

	depositReq := types.DepositRequestBody{
		Owner:  "cosmos1alice",
		Denom:  "uusdc",
		Amount: "100",
	}

	depositRec := performRequest(t, server, http.MethodPost, "/api/deposit", depositReq)
	if depositRec.Code != http.StatusOK {
		t.Fatalf("expected deposit status 200, got %d, body=%s", depositRec.Code, depositRec.Body.String())
	}

	depositBody := decodeJSON[types.DepositResponse](t, depositRec)

	if depositBody.DepositRecord.DepositID == "" {
		t.Fatalf("expected indexed depositId")
	}

	if depositBody.DepositRecord.TxHash == "" {
		t.Fatalf("expected indexed txHash")
	}

	if depositBody.DepositRecord.CreatedHeight == 0 {
		t.Fatalf("expected indexed createdHeight")
	}

	if depositBody.State.DepositStatus != "indexed" {
		t.Fatalf("expected depositStatus=indexed, got %q", depositBody.State.DepositStatus)
	}

	getRec := performRequest(t, server, http.MethodGet, "/api/deposits/"+depositBody.DepositRecord.DepositID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected get deposit status 200, got %d, body=%s", getRec.Code, getRec.Body.String())
	}

	getBody := decodeJSON[types.GetDepositResponse](t, getRec)

	if getBody.DepositRecord.DepositID != depositBody.DepositRecord.DepositID {
		t.Fatalf("expected depositId=%q, got %q", depositBody.DepositRecord.DepositID, getBody.DepositRecord.DepositID)
	}

	if getBody.DepositRecord.TxHash != depositBody.DepositRecord.TxHash {
		t.Fatalf("expected txHash=%q, got %q", depositBody.DepositRecord.TxHash, getBody.DepositRecord.TxHash)
	}
}

func TestINT05ListDepositsReturnsIndexedDeposits(t *testing.T) {
	server := newTestServer()

	depositReq := types.DepositRequestBody{
		Owner:  "cosmos1alice",
		Denom:  "uusdc",
		Amount: "100",
	}

	depositRec := performRequest(t, server, http.MethodPost, "/api/deposit", depositReq)
	if depositRec.Code != http.StatusOK {
		t.Fatalf("expected deposit status 200, got %d, body=%s", depositRec.Code, depositRec.Body.String())
	}

	listRec := performRequest(t, server, http.MethodGet, "/api/deposits", nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected list deposits status 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	listBody := decodeJSON[types.ListDepositsResponse](t, listRec)

	if len(listBody.Deposits) != 1 {
		t.Fatalf("expected 1 indexed deposit, got %d", len(listBody.Deposits))
	}

	if listBody.Deposits[0].DepositID != "dep-1" {
		t.Fatalf("expected depositId=dep-1, got %q", listBody.Deposits[0].DepositID)
	}
}

func TestINT05GetDepositNotFound(t *testing.T) {
	server := newTestServer()

	rec := performRequest(t, server, http.MethodGet, "/api/deposits/unknown", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]string](t, rec)

	if body["error"] != "deposit not found" {
		t.Fatalf("expected error deposit not found, got %q", body["error"])
	}
}
