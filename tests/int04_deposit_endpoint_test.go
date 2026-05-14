package tests

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestINT04DepositEndpointUsesChainClientContract(t *testing.T) {
	server := newTestServer()

	req := types.DepositRequestBody{
		Owner:  "cosmos1alice",
		Denom:  "uusdc",
		Amount: "100",
	}

	rec := performRequest(t, server, http.MethodPost, "/api/deposit", req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[types.DepositResponse](t, rec)

	if body.TxHash == "" {
		t.Fatalf("expected txHash to be generated")
	}

	if !strings.HasPrefix(body.TxHash, "0x") {
		t.Fatalf("expected txHash to have 0x prefix, got %q", body.TxHash)
	}

	if body.DepositRecord.TxHash != body.TxHash {
		t.Fatalf("expected depositRecord.txHash to match txHash")
	}

	if body.DepositRecord.DepositID != "dep-1" {
		t.Fatalf("expected depositId=dep-1, got %q", body.DepositRecord.DepositID)
	}

	if body.DepositRecord.Owner != "cosmos1alice" {
		t.Fatalf("expected owner=cosmos1alice, got %q", body.DepositRecord.Owner)
	}

	if body.DepositRecord.Denom != "uusdc" {
		t.Fatalf("expected denom=uusdc, got %q", body.DepositRecord.Denom)
	}

	if body.DepositRecord.Amount != "100" {
		t.Fatalf("expected amount=100, got %q", body.DepositRecord.Amount)
	}

	if body.DepositRecord.Processed {
		t.Fatalf("expected processed=false")
	}

	if body.State.CurrentStateRoot != "0xrootA" {
		t.Fatalf("expected currentStateRoot=0xrootA, got %q", body.State.CurrentStateRoot)
	}

	if body.State.DepositStatus != "locked" {
		t.Fatalf("expected depositStatus=locked, got %q", body.State.DepositStatus)
	}
}

func TestINT04DepositEndpointRejectsInvalidAmount(t *testing.T) {
	server := newTestServer()

	req := types.DepositRequestBody{
		Owner:  "cosmos1alice",
		Denom:  "uusdc",
		Amount: "abc",
	}

	rec := performRequest(t, server, http.MethodPost, "/api/deposit", req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]string](t, rec)

	if body["error"] != "amount must be a positive integer string" {
		t.Fatalf("unexpected error: %q", body["error"])
	}
}

func TestINT04DepositEndpointRejectsMissingAmount(t *testing.T) {
	server := newTestServer()

	req := map[string]string{
		"owner": "cosmos1alice",
		"denom": "uusdc",
	}

	rec := performRequest(t, server, http.MethodPost, "/api/deposit", req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]string](t, rec)

	if body["error"] != "owner, denom and amount are required" {
		t.Fatalf("unexpected error: %q", body["error"])
	}
}
