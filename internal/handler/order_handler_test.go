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
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// newTestServer wires the mock order handler into the real router so the tests
// exercise the actual route patterns (including the {market...} wildcard) and
// PathValue extraction — not just the handler methods in isolation.
func newTestServer() http.Handler {
	orderHandler := handler.NewOrderHandler(service.NewMockOrderService())
	return api.NewRouter(api.RouterDeps{OrderHandler: orderHandler}).Routes()
}

func TestListMarkets(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/markets", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}

	var resp types.MarketsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Markets) == 0 {
		t.Fatal("markets empty")
	}
	m := resp.Markets[0]
	if m.Market != "ATOM/USDC" || m.BaseDenom != "uatom" || m.QuoteDenom != "uusdc" {
		t.Fatalf("unexpected first market: %+v", m)
	}
	if m.Status != types.MarketActive {
		t.Fatalf("status = %q, want active", m.Status)
	}
}

func TestCreateOrderOpen(t *testing.T) {
	order := types.SignedOrder{
		Owner: "cosmos1alice", Market: "ATOM/USDC", Side: types.SideBuy,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1", Signature: "sig",
	}
	body, _ := json.Marshal(order)

	rec := httptest.NewRecorder()
	newTestServer().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/order", strings.NewReader(string(body))))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp types.OrderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != types.OrderStatusOpen {
		t.Fatalf("status = %q, want open", resp.Status)
	}
	if resp.Order.Owner != order.Owner || resp.Order.Qty != order.Qty {
		t.Fatalf("order not echoed: %+v", resp.Order)
	}
	if !strings.HasPrefix(resp.State.OrderHash, "0x") || len(resp.State.OrderHash) != 66 {
		t.Fatalf("orderHash not 0x+64 hex: %q", resp.State.OrderHash)
	}
	if !strings.HasPrefix(resp.State.OrderID, "ord-") {
		t.Fatalf("orderId = %q, want ord- prefix", resp.State.OrderID)
	}
	if resp.State.Remaining != "20" || resp.State.Filled != "0" {
		t.Fatalf("state remaining/filled = %q/%q, want 20/0", resp.State.Remaining, resp.State.Filled)
	}
}

// The mock order id must be deterministic (derived from canonical bytes) so FE
// and the future real handler agree; the same order posted twice yields the
// same id/hash.
func TestCreateOrderDeterministicID(t *testing.T) {
	order := types.SignedOrder{
		Owner: "cosmos1bob", Market: "ATOM/USDC", Side: types.SideSell,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "7", Signature: "sig",
	}
	body, _ := json.Marshal(order)
	srv := newTestServer()

	post := func() types.OrderResponse {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/order", strings.NewReader(string(body))))
		var resp types.OrderResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}
	a, b := post(), post()
	if a.State.OrderID != b.State.OrderID || a.State.OrderHash != b.State.OrderHash {
		t.Fatalf("non-deterministic id/hash: %+v vs %+v", a.State, b.State)
	}
}

func TestCreateOrderMissingFields(t *testing.T) {
	body := `{"owner":"cosmos1alice","market":"ATOM/USDC"}` // no side/price/qty
	rec := httptest.NewRecorder()
	newTestServer().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/order", strings.NewReader(body)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// The {market...} wildcard must capture a market id containing a slash.
func TestGetOrderbookWithSlash(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orderbook/ATOM/USDC", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var book types.OrderbookSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &book); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if book.Market != "ATOM/USDC" {
		t.Fatalf("market = %q, want ATOM/USDC", book.Market)
	}
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		t.Fatalf("empty book: %+v", book)
	}
	// Bids price-descending, asks price-ascending; best of book set.
	if book.BestBid != book.Bids[0].Price || book.BestAsk != book.Asks[0].Price {
		t.Fatalf("best-of-book mismatch: bestBid=%s bids[0]=%s bestAsk=%s asks[0]=%s",
			book.BestBid, book.Bids[0].Price, book.BestAsk, book.Asks[0].Price)
	}
}

func TestGetOrderbookUnknownMarket(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orderbook/NOPE/USDC", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
