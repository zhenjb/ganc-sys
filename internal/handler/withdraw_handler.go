package handler

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/request"
	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// WithdrawHandler exposes withdrawal request and claim endpoints.
//
// INT-04 status:
// - POST /api/withdraw-request returns a local deterministic request.
// - POST /api/withdraw/claim returns a local deterministic claimed result.
// - No persisted withdrawal request store exists yet.
// - No real MsgClaimWithdraw integration exists yet.
//
// TODO(INT-06):
// Persist user-created withdrawal requests.
//
// TODO(INT-10):
// Submit MsgClaimWithdraw to chain and return indexed/query-backed results.
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
