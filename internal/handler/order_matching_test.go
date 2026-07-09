package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func postOrder(t *testing.T, srv http.Handler, o types.SignedOrder) types.OrderResponse {
	t.Helper()
	body, _ := json.Marshal(signOrder(t, o))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/order", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp types.OrderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// Full HTTP flow (INT-T05 DoD): two crossing orders → a fill surfaces at
// GET /api/trades, and the taker order comes back "filled".
func TestHTTPCrossingProducesTrade(t *testing.T) {
	srv := newRealServer(t, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "5000"},
		{DepositID: "d2", Owner: "cosmos1bob", Denom: "uatom", Amount: "50"},
	})

	postOrder(t, srv, types.SignedOrder{
		Owner: "cosmos1alice", Market: "ATOM/USDC", Side: types.SideBuy,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1",
	})
	bob := postOrder(t, srv, types.SignedOrder{
		Owner: "cosmos1bob", Market: "ATOM/USDC", Side: types.SideSell,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1",
	})
	if bob.Status != types.OrderStatusFilled {
		t.Fatalf("bob status = %q, want filled", bob.Status)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/trades?market=ATOM/USDC", nil))
	var trades types.TradesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &trades); err != nil {
		t.Fatalf("decode trades: %v", err)
	}
	if len(trades.Fills) != 1 || trades.Fills[0].Price != "100" || trades.Fills[0].Qty != "20" {
		t.Fatalf("trades = %+v, want one 100/20 fill", trades.Fills)
	}
}
