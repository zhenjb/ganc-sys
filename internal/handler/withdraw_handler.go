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
// INT-10 status:
// - POST /api/withdraw/claim claims a submitted withdrawRecord.
// - unknown withdrawId returns 404.
// - repeated claim returns 400.
//
// P3INT-07:
//   - POST /api/withdraw-request can also apply the request to the pending
//     off-chain settlement state when OFFCHAIN_SETTLEMENT_ENABLED=true.
//   - insufficient pending balance returns HTTP 400.
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

	result, err := h.withdrawService.CreateWithdrawRequest(r.Context(), req)
	if err != nil {
		switch {
		case service.IsWithdrawInsufficientBalanceError(err):
			response.Error(w, http.StatusBadRequest, "insufficient off-chain balance")
		default:
			response.Error(w, http.StatusBadRequest, err.Error())
		}

		return
	}

	response.JSON(w, http.StatusOK, result)
}

func (h *WithdrawHandler) ListWithdrawRequests(w http.ResponseWriter, r *http.Request) {
	result := h.withdrawService.ListWithdrawRequests(r.Context())

	response.JSON(w, http.StatusOK, result)
}

// ListWithdrawRecords handles GET /api/withdraws — the settled-withdrawal
// history (analog of GET /api/deposits). Distinct from GET /api/withdraw-requests
// which lists pending requests before settlement.
func (h *WithdrawHandler) ListWithdrawRecords(w http.ResponseWriter, r *http.Request) {
	result := h.withdrawService.ListWithdrawRecords(r.Context())

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

	result, err := h.withdrawService.ClaimWithdraw(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrWithdrawRecordNotFound):
			response.Error(w, http.StatusNotFound, "withdraw record not found")
		case errors.Is(err, repository.ErrWithdrawAlreadyClaimed):
			response.Error(w, http.StatusBadRequest, "withdraw already claimed")
		default:
			response.Error(w, http.StatusBadRequest, err.Error())
		}

		return
	}

	response.JSON(w, http.StatusOK, result)
}
