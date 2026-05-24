package tests

import (
	"net/http"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestINT06WithdrawRequestIsPersistedAfterCreate(t *testing.T) {
	server := newTestServer()

	createReq := types.WithdrawRequestBody{
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
	}

	createRec := performRequest(t, server, http.MethodPost, "/api/withdraw-request", createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("expected create withdraw status 200, got %d, body=%s", createRec.Code, createRec.Body.String())
	}

	createBody := decodeJSON[types.WithdrawRequestResponse](t, createRec)

	if createBody.WithdrawRequest.WithdrawID != "wd-1" {
		t.Fatalf("expected withdrawId=wd-1, got %q", createBody.WithdrawRequest.WithdrawID)
	}

	if createBody.WithdrawRequest.Owner != "cosmos1alice" {
		t.Fatalf("expected owner=cosmos1alice, got %q", createBody.WithdrawRequest.Owner)
	}

	if createBody.WithdrawRequest.Denom != "uusdc" {
		t.Fatalf("expected denom=uusdc, got %q", createBody.WithdrawRequest.Denom)
	}

	if createBody.WithdrawRequest.Amount != "40" {
		t.Fatalf("expected amount=40, got %q", createBody.WithdrawRequest.Amount)
	}

	if createBody.WithdrawRequest.Destination != "cosmos1alice" {
		t.Fatalf("expected destination=cosmos1alice, got %q", createBody.WithdrawRequest.Destination)
	}

	if createBody.WithdrawRequest.Nonce != "1" {
		t.Fatalf("expected nonce=1, got %q", createBody.WithdrawRequest.Nonce)
	}

	if createBody.WithdrawRequest.Signature == "" {
		t.Fatalf("expected local signature to be generated")
	}

	if createBody.State.WithdrawStatus != "requested" {
		t.Fatalf("expected withdrawStatus=requested, got %q", createBody.State.WithdrawStatus)
	}

	getRec := performRequest(t, server, http.MethodGet, "/api/withdraw-requests/"+createBody.WithdrawRequest.WithdrawID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected get withdraw request status 200, got %d, body=%s", getRec.Code, getRec.Body.String())
	}

	getBody := decodeJSON[types.GetWithdrawRequestResponse](t, getRec)

	if getBody.WithdrawRequest.WithdrawID != createBody.WithdrawRequest.WithdrawID {
		t.Fatalf("expected withdrawId=%q, got %q", createBody.WithdrawRequest.WithdrawID, getBody.WithdrawRequest.WithdrawID)
	}

	if getBody.WithdrawRequest.Signature != createBody.WithdrawRequest.Signature {
		t.Fatalf("expected signature=%q, got %q", createBody.WithdrawRequest.Signature, getBody.WithdrawRequest.Signature)
	}
}

func TestINT06ListWithdrawRequestsReturnsPersistedRequests(t *testing.T) {
	server := newTestServer()

	createReq := types.WithdrawRequestBody{
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
	}

	createRec := performRequest(t, server, http.MethodPost, "/api/withdraw-request", createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("expected create withdraw status 200, got %d, body=%s", createRec.Code, createRec.Body.String())
	}

	listRec := performRequest(t, server, http.MethodGet, "/api/withdraw-requests", nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected list withdraw requests status 200, got %d, body=%s", listRec.Code, listRec.Body.String())
	}

	listBody := decodeJSON[types.ListWithdrawRequestsResponse](t, listRec)

	if len(listBody.WithdrawRequests) != 1 {
		t.Fatalf("expected 1 withdraw request, got %d", len(listBody.WithdrawRequests))
	}

	if listBody.WithdrawRequests[0].WithdrawID != "wd-1" {
		t.Fatalf("expected withdrawId=wd-1, got %q", listBody.WithdrawRequests[0].WithdrawID)
	}
}

func TestINT06MultipleWithdrawRequestsUseUniqueIDsAndNonces(t *testing.T) {
	server := newTestServer()

	req1 := types.WithdrawRequestBody{
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
	}

	req2 := types.WithdrawRequestBody{
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "25",
		Destination: "cosmos1alice",
	}

	rec1 := performRequest(t, server, http.MethodPost, "/api/withdraw-request", req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected first withdraw status 200, got %d, body=%s", rec1.Code, rec1.Body.String())
	}

	rec2 := performRequest(t, server, http.MethodPost, "/api/withdraw-request", req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected second withdraw status 200, got %d, body=%s", rec2.Code, rec2.Body.String())
	}

	body1 := decodeJSON[types.WithdrawRequestResponse](t, rec1)
	body2 := decodeJSON[types.WithdrawRequestResponse](t, rec2)

	if body1.WithdrawRequest.WithdrawID == body2.WithdrawRequest.WithdrawID {
		t.Fatalf("expected unique withdrawIds, both got %q", body1.WithdrawRequest.WithdrawID)
	}

	if body1.WithdrawRequest.Nonce == body2.WithdrawRequest.Nonce {
		t.Fatalf("expected unique nonces, both got %q", body1.WithdrawRequest.Nonce)
	}

	if body2.WithdrawRequest.WithdrawID != "wd-2" {
		t.Fatalf("expected second withdrawId=wd-2, got %q", body2.WithdrawRequest.WithdrawID)
	}

	if body2.WithdrawRequest.Nonce != "2" {
		t.Fatalf("expected second nonce=2, got %q", body2.WithdrawRequest.Nonce)
	}
}

func TestINT06GetWithdrawRequestNotFound(t *testing.T) {
	server := newTestServer()

	rec := performRequest(t, server, http.MethodGet, "/api/withdraw-requests/unknown", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]string](t, rec)

	if body["error"] != "withdraw request not found" {
		t.Fatalf("expected error withdraw request not found, got %q", body["error"])
	}
}
