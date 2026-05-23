package handler

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/request"
	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// DepositHandler exposes deposit endpoints.
//
// INT-04 status:
// - POST /api/deposit calls DepositService.
// - DepositService currently uses chain.LocalClient.
// - Deposit indexing from emitted on-chain events is not implemented yet.
//
// TODO(INT-05):
// Add:
// - GET /api/deposits
// - GET /api/deposits/{depositId}
//
// The query endpoints should return DepositRecord data from the event-backed
// indexer/store, not from direct local construction.
type DepositHandler struct {
	depositService *service.DepositService
}

func NewDepositHandler(depositService *service.DepositService) *DepositHandler {
	return &DepositHandler{
		depositService: depositService,
	}
}

func (h *DepositHandler) CreateDeposit(w http.ResponseWriter, r *http.Request) {
	var req types.DepositRequestBody
	if !request.JSON(w, r, &req) {
		return
	}

	if req.Owner == "" || req.Denom == "" || req.Amount == "" {
		response.Error(w, http.StatusBadRequest, "owner, denom and amount are required")
		return
	}

	result, err := h.depositService.CreateDeposit(r.Context(), req)
	if err != nil {
		response.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}
