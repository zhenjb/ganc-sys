package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// GET /api/orders?owner= over HTTP reflects a created order and is empty for
// another owner (INT-T04 DoD).
func TestHTTPListOrders(t *testing.T) {
	srv := newRealServer(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
	})
	postAlice(t, srv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orders?owner=cosmos1alice", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp types.OpenOrdersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.OpenOrders) != 1 || resp.OpenOrders[0].Market != "ATOM/USDC" {
		t.Fatalf("openOrders = %+v, want one ATOM/USDC order", resp.OpenOrders)
	}

	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/orders?owner=cosmos1bob", nil))
	var empty types.OpenOrdersResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &empty)
	if len(empty.OpenOrders) != 0 {
		t.Fatalf("bob openOrders = %d, want 0", len(empty.OpenOrders))
	}
}

// GET /api/trades?market= over HTTP returns an empty (non-null) fills array
// before any match.
func TestHTTPListTradesEmpty(t *testing.T) {
	srv := newRealServer(t, nil)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/trades?market=ATOM/USDC", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Must serialize as {"fills":[]}, never {"fills":null} (FE array guard).
	if body := rec.Body.String(); body != "{\"fills\":[]}\n" {
		t.Fatalf("body = %q, want {\"fills\":[]}", body)
	}
}
