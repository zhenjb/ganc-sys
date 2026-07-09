package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// OrderService is the P4 order/orderbook API boundary consumed by the HTTP
// handlers (INT-T01). It is an INTERFACE on purpose: INT-T01 ships a static
// MockOrderService so the frontend (P5) can start immediately, and INT-T02..T04
// will provide a real implementation (validate → reserve → insert book, live
// snapshots) WITHOUT changing the routes or the response shapes.
type OrderService interface {
	// ListMarkets returns the off-chain market registry (GET /api/markets).
	ListMarkets(ctx context.Context) types.MarketsResponse
	// CreateOrder accepts a SignedOrder (POST /api/order) and returns the order
	// with its status and lifecycle state.
	CreateOrder(ctx context.Context, order types.SignedOrder) (types.OrderResponse, error)
	// GetOrderbook returns a depth snapshot for a market
	// (GET /api/orderbook/{market}).
	GetOrderbook(ctx context.Context, market string) (types.OrderbookSnapshot, error)
}

// Order API service errors. Handlers map these to HTTP status codes so the
// error shape is stable across the mock and the real implementation.
var (
	// ErrOrderFieldsRequired — a required order field is missing/empty.
	ErrOrderFieldsRequired = errors.New("order service: owner, market, side, price and qty are required")
	// ErrMarketNotFound — the requested market is not in the registry.
	ErrMarketNotFound = errors.New("order service: market not found")
)

// MockOrderService is the INT-T01 static implementation. It serves a fixed
// market registry and orderbook fixture, and echoes any posted order back as
// "open". It performs NO signature/reserve/matching logic — those arrive with
// INT-T02..T05. The order id is derived from the canonical order bytes so it is
// deterministic and forward-compatible with the real orderHash (SHA-256 of
// CanonicalBytes, same definition as STATE-T03 OrderHash), but this service
// deliberately does not import P3 state (INT-T01 has no P3 dependency).
type MockOrderService struct {
	markets    []types.Market
	orderbooks map[string]types.OrderbookSnapshot
}

// NewMockOrderService builds the mock with a frozen ATOM/USDC + OSMO/USDC
// fixture. The shapes here are the contract handed to P5; keep them stable.
func NewMockOrderService() *MockOrderService {
	markets := []types.Market{
		{
			Market:      "ATOM/USDC",
			BaseDenom:   "uatom",
			QuoteDenom:  "uusdc",
			TickSize:    "0.1",
			LotSize:     "1",
			MakerFeeBps: 50,
			TakerFeeBps: 100,
			Status:      types.MarketActive,
		},
		{
			Market:      "OSMO/USDC",
			BaseDenom:   "uosmo",
			QuoteDenom:  "uusdc",
			TickSize:    "0.01",
			LotSize:     "1",
			MakerFeeBps: 50,
			TakerFeeBps: 100,
			Status:      types.MarketActive,
		},
	}

	orderbooks := map[string]types.OrderbookSnapshot{
		"ATOM/USDC": {
			Market: "ATOM/USDC",
			// Bids: price-descending (best/highest first).
			Bids: []types.PriceLevel{
				{Price: "99.9", Qty: "12"},
				{Price: "99.8", Qty: "30"},
				{Price: "99.5", Qty: "75"},
			},
			// Asks: price-ascending (best/lowest first).
			Asks: []types.PriceLevel{
				{Price: "100.1", Qty: "8"},
				{Price: "100.2", Qty: "25"},
				{Price: "100.5", Qty: "60"},
			},
			BestBid: "99.9",
			BestAsk: "100.1",
		},
		"OSMO/USDC": {
			Market:  "OSMO/USDC",
			Bids:    []types.PriceLevel{{Price: "1.23", Qty: "500"}},
			Asks:    []types.PriceLevel{{Price: "1.25", Qty: "400"}},
			BestBid: "1.23",
			BestAsk: "1.25",
		},
	}

	return &MockOrderService{markets: markets, orderbooks: orderbooks}
}

// compile-time assertion that the mock satisfies the boundary.
var _ OrderService = (*MockOrderService)(nil)

func (s *MockOrderService) ListMarkets(_ context.Context) types.MarketsResponse {
	// Return a copy so a caller cannot mutate the fixture slice.
	out := make([]types.Market, len(s.markets))
	copy(out, s.markets)
	return types.MarketsResponse{Markets: out}
}

func (s *MockOrderService) CreateOrder(_ context.Context, order types.SignedOrder) (types.OrderResponse, error) {
	if strings.TrimSpace(order.Owner) == "" ||
		strings.TrimSpace(order.Market) == "" ||
		strings.TrimSpace(string(order.Side)) == "" ||
		strings.TrimSpace(order.Price) == "" ||
		strings.TrimSpace(order.Qty) == "" {
		return types.OrderResponse{}, ErrOrderFieldsRequired
	}

	orderHash := mockOrderHash(order)
	orderID := "ord-" + orderHash[2:18] // "0x" + first 16 hex chars

	return types.OrderResponse{
		Order:  order,
		Status: types.OrderStatusOpen,
		State: types.OrderState{
			OrderID:   orderID,
			OrderHash: orderHash,
			Status:    types.OrderStatusOpen,
			Remaining: order.Qty,
			Filled:    "0",
		},
	}, nil
}

func (s *MockOrderService) GetOrderbook(_ context.Context, market string) (types.OrderbookSnapshot, error) {
	book, ok := s.orderbooks[market]
	if !ok {
		return types.OrderbookSnapshot{}, ErrMarketNotFound
	}
	return book, nil
}

// mockOrderHash returns "0x"+hex(SHA-256(CanonicalBytes)). It matches the
// STATE-T03 OrderHash definition so the mock id lines up with the real hash once
// INT-T02 wires P3, but is computed locally to keep INT-T01 free of any P3
// import. Malformed orders (empty canonical fields) fall back to hashing the
// raw owner|market|nonce join so the mock still returns a stable id.
func mockOrderHash(order types.SignedOrder) string {
	preimage, err := order.CanonicalBytes()
	if err != nil {
		preimage = []byte(order.Owner + "|" + order.Market + "|" + order.Nonce)
	}
	sum := sha256.Sum256(preimage)
	return "0x" + hex.EncodeToString(sum[:])
}
