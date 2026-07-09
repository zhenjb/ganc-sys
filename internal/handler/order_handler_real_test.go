package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/api"
	"github.com/zhenjb/ganc-sys/internal/handler"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// newRealServer wires the REAL order service (over a funded manager) into the
// actual router, so these tests exercise HTTP status + body mapping end-to-end.
func newRealServer(t *testing.T, deposits []types.DepositRecord) http.Handler {
	t.Helper()
	mgr := state.NewOffchainStateManager()
	for _, d := range deposits {
		if _, err := mgr.ApplyDeposit(d); err != nil {
			t.Fatalf("deposit: %v", err)
		}
	}
	svc, err := service.NewRealOrderService(mgr, service.DefaultMarkets(), func() int64 { return 1_000_000 })
	if err != nil {
		t.Fatalf("real service: %v", err)
	}
	oh := handler.NewOrderHandler(svc)
	return api.NewRouter(api.RouterDeps{OrderHandler: oh}).Routes()
}

func signOrder(t *testing.T, o types.SignedOrder) types.SignedOrder {
	t.Helper()
	sig, err := state.MockOrderSignature(o)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	o.Signature = sig
	return o
}

// Happy path over HTTP: 200 open, and the order then appears in GET /api/orderbook
// (the INT-T02 DoD).
func TestRealHTTPCreateThenOrderbook(t *testing.T) {
	srv := newRealServer(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "3000"},
	})
	order := signOrder(t, types.SignedOrder{
		Owner: "cosmos1alice", Market: "ATOM/USDC", Side: types.SideBuy,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1",
	})
	body, _ := json.Marshal(order)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/order", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp types.OrderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != types.OrderStatusOpen {
		t.Fatalf("status = %q, want open", resp.Status)
	}

	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/orderbook/ATOM/USDC", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec2.Code)
	}
	var book types.OrderbookSnapshot
	if err := json.Unmarshal(rec2.Body.Bytes(), &book); err != nil {
		t.Fatalf("decode book: %v", err)
	}
	if book.BestBid != "100" || len(book.Bids) != 1 {
		t.Fatalf("book = %+v, want one bid @100", book)
	}
}

// Insufficient balance over HTTP: 400 with reason "insufficient_balance".
func TestRealHTTPInsufficientBalanceReason(t *testing.T) {
	srv := newRealServer(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "100"},
	})
	order := signOrder(t, types.SignedOrder{
		Owner: "cosmos1alice", Market: "ATOM/USDC", Side: types.SideBuy,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1",
	})
	body, _ := json.Marshal(order)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/order", strings.NewReader(string(body))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var errResp struct {
		Error  string `json:"error"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode err body: %v", err)
	}
	if errResp.Reason != service.ReasonInsufficientBalance {
		t.Fatalf("reason = %q, want %q", errResp.Reason, service.ReasonInsufficientBalance)
	}
}
