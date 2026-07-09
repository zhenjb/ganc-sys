package handler

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
)

// StateHandler exposes dashboard/read-model state endpoints.
//
// INT-04 status:
// - GET /api/state returns local initial state.
// - It does not read indexed chain events yet.
// - It does not query real chain balances yet.
//
// TODO(INT-05+):
// StateService should assemble state from indexed deposits, batches,
// withdrawal records, and chain/module balance queries.
type StateHandler struct {
	stateService *service.StateService
	// tradeState is the optional INT-T07 trading extension source. When nil the
	// response is exactly the base deposit/withdraw dashboard (backward-compat).
	tradeState service.TradeStateProvider
}

func NewStateHandler(stateService *service.StateService) *StateHandler {
	return &StateHandler{
		stateService: stateService,
	}
}

// SetTradeStateProvider wires the trading extension (INT-T07). Called from
// cmd/api after the real order service is built.
func (h *StateHandler) SetTradeStateProvider(provider service.TradeStateProvider) {
	h.tradeState = provider
}

func (h *StateHandler) GetState(w http.ResponseWriter, r *http.Request) {
	result := h.stateService.GetState(r.Context())

	// INT-T07: append the trading slice (reserved balances, open orders for the
	// optional ?owner=, latest trades, market status). APPEND-ONLY — the base
	// fields above are untouched.
	if h.tradeState != nil {
		ts := h.tradeState.TradeState(r.Context(), r.URL.Query().Get("owner"))
		result.ReservedBalances = ts.ReservedBalances
		result.OpenOrders = ts.OpenOrders
		result.LatestTrades = ts.LatestTrades
		result.MarketStatus = ts.MarketStatus
	}

	response.JSON(w, http.StatusOK, result)
}
