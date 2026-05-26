package handler

import (
	"errors"
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/request"
	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// DepositHandler exposes deposit endpoints.
//
// INT-05 status:
// - POST /api/deposit indexes the deposit from emitted tx events.
// - GET /api/deposits lists indexed deposits.
// - GET /api/deposits/{depositId} returns one indexed deposit.
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

func (h *DepositHandler) ListDeposits(w http.ResponseWriter, r *http.Request) {
	result := h.depositService.ListDeposits(r.Context())

	response.JSON(w, http.StatusOK, result)
}

func (h *DepositHandler) GetDeposit(w http.ResponseWriter, r *http.Request) {
	depositID := r.PathValue("depositId")
	if depositID == "" {
		response.Error(w, http.StatusBadRequest, "depositId is required")
		return
	}

	result, err := h.depositService.GetDeposit(r.Context(), depositID)
	if err != nil {
		if errors.Is(err, repository.ErrDepositNotFound) {
			response.Error(w, http.StatusNotFound, "deposit not found")
			return
		}

		response.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}
