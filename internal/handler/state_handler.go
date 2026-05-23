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
}

func NewStateHandler(stateService *service.StateService) *StateHandler {
	return &StateHandler{
		stateService: stateService,
	}
}

func (h *StateHandler) GetState(w http.ResponseWriter, r *http.Request) {
	result := h.stateService.GetState(r.Context())

	response.JSON(w, http.StatusOK, result)
}
