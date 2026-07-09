package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// postAlice submits a funded, signed buy through the router and returns the
// orderHash so the DELETE test can target it.
func postAlice(t *testing.T, srv http.Handler) string {
	t.Helper()
	order := signOrder(t, types.SignedOrder{
		Owner: "cosmos1alice", Market: "ATOM/USDC", Side: types.SideBuy,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1",
	})
	body, _ := json.Marshal(order)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/order", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp types.OrderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.State.OrderHash
}

// Full HTTP lifecycle: create → DELETE (200 cancelled) → order gone from the book.
func TestHTTPCancelHappyPath(t *testing.T) {
	srv := newRealServer(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
	})
	hash := postAlice(t, srv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/order/"+hash+"?owner=cosmos1alice", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp types.OrderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != types.OrderStatusCancelled {
		t.Fatalf("status = %q, want cancelled", resp.Status)
	}

	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/orderbook/ATOM/USDC", nil))
	var book types.OrderbookSnapshot
	_ = json.Unmarshal(rec2.Body.Bytes(), &book)
	if len(book.Bids) != 0 {
		t.Fatalf("order still in book after cancel: %+v", book)
	}
}

// DELETE without ?owner= → 400.
func TestHTTPCancelMissingOwner(t *testing.T) {
	srv := newRealServer(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
	})
	hash := postAlice(t, srv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/order/"+hash, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// DELETE by a non-owner → 403.
func TestHTTPCancelForbidden(t *testing.T) {
	srv := newRealServer(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
	})
	hash := postAlice(t, srv)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/order/"+hash+"?owner=cosmos1mallory", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// DELETE an unknown id → 404.
func TestHTTPCancelNotFound(t *testing.T) {
	srv := newRealServer(t, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/order/0xdeadbeef?owner=cosmos1alice", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
