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
	// mode overrides the dashboard "mode" field to reflect the actual runtime
	// (e.g. "cosmos" against a real chain) instead of the MemoryStore seed "local".
	// Empty leaves the base value untouched (Nhóm 4 (b)).
	mode string
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

// SetMode wires the runtime mode reflected by GET /api/state's "mode" field
// (Nhóm 4 (b)). Called from cmd/api with the resolved chain-deposit mode so the
// dashboard shows "cosmos" on a real chain instead of the seed "local". Empty is
// a no-op.
func (h *StateHandler) SetMode(mode string) {
	h.mode = mode
}

func (h *StateHandler) GetState(w http.ResponseWriter, r *http.Request) {
	result := h.stateService.GetState(r.Context())

	// Nhóm 4 (b): reflect the real runtime mode instead of the MemoryStore seed.
	if h.mode != "" {
		result.Mode = h.mode
	}

	// INT-T07: append the trading slice (reserved balances, open orders for the
	// optional ?owner=, latest trades, market status). APPEND-ONLY for those.
	//
	// userBalances is ALSO overridden here with the real off-chain (L2) holdings
	// from the shared state manager (available + reserved per owner/denom),
	// replacing the legacy seeded memory ledger. moduleAccountBalance is left
	// exactly as StateService assembled it — the chain REST module balance (ground
	// truth) when CHAIN_QUERY_MODE is cosmos/rest, else the memory mirror.
	if h.tradeState != nil {
		ts := h.tradeState.TradeState(r.Context(), r.URL.Query().Get("owner"))
		result.ReservedBalances = ts.ReservedBalances
		result.OpenOrders = ts.OpenOrders
		result.LatestTrades = ts.LatestTrades
		result.MarketStatus = ts.MarketStatus
		result.UserBalances = ts.UserBalances
		result.Denoms = ts.Denoms
	}

	response.JSON(w, http.StatusOK, result)
}
