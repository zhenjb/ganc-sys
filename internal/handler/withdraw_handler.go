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

// WithdrawHandler exposes withdrawal request and claim endpoints.
//
// INT-06 status:
// - POST /api/withdraw-request creates and persists a local withdraw request.
// - GET /api/withdraw-requests lists persisted withdraw requests.
// - GET /api/withdraw-requests/{withdrawId} returns one persisted request.
//
// Still local/stubbed:
// - no real balance debit yet.
// - no real nullifier generation here.
// - claim withdraw is still local fixture until INT-10.
type WithdrawHandler struct {
	withdrawService *service.WithdrawService
}

func NewWithdrawHandler(withdrawService *service.WithdrawService) *WithdrawHandler {
	return &WithdrawHandler{
		withdrawService: withdrawService,
	}
}

func (h *WithdrawHandler) CreateWithdrawRequest(w http.ResponseWriter, r *http.Request) {
	var req types.WithdrawRequestBody
	if !request.JSON(w, r, &req) {
		return
	}

	if req.Owner == "" || req.Denom == "" || req.Amount == "" || req.Destination == "" {
		response.Error(w, http.StatusBadRequest, "owner, denom, amount and destination are required")
		return
	}

	result := h.withdrawService.CreateWithdrawRequest(r.Context(), req)
	response.JSON(w, http.StatusOK, result)
}

func (h *WithdrawHandler) ListWithdrawRequests(w http.ResponseWriter, r *http.Request) {
	result := h.withdrawService.ListWithdrawRequests(r.Context())

	response.JSON(w, http.StatusOK, result)
}

func (h *WithdrawHandler) GetWithdrawRequest(w http.ResponseWriter, r *http.Request) {
	withdrawID := r.PathValue("withdrawId")
	if withdrawID == "" {
		response.Error(w, http.StatusBadRequest, "withdrawId is required")
		return
	}

	result, err := h.withdrawService.GetWithdrawRequest(r.Context(), withdrawID)
	if err != nil {
		if errors.Is(err, repository.ErrWithdrawRequestNotFound) {
			response.Error(w, http.StatusNotFound, "withdraw request not found")
			return
		}

		response.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}

func (h *WithdrawHandler) ClaimWithdraw(w http.ResponseWriter, r *http.Request) {
	var req types.ClaimWithdrawRequestBody
	if !request.JSON(w, r, &req) {
		return
	}

	if req.WithdrawID == "" {
		response.Error(w, http.StatusBadRequest, "withdrawId is required")
		return
	}

	result := h.withdrawService.ClaimWithdraw(r.Context(), req)
	response.JSON(w, http.StatusOK, result)
}
