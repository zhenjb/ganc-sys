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

// BatchHandler exposes batch build and submit endpoints.
type BatchHandler struct {
	batchService *service.BatchService
}

func NewBatchHandler(batchService *service.BatchService) *BatchHandler {
	return &BatchHandler{
		batchService: batchService,
	}
}

func (h *BatchHandler) BuildBatch(w http.ResponseWriter, r *http.Request) {
	var req types.BuildBatchRequestBody
	if !request.JSON(w, r, &req) {
		return
	}

	result, err := h.batchService.BuildBatch(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrDepositNotFound):
			response.Error(w, http.StatusNotFound, "deposit not found")
		case errors.Is(err, repository.ErrWithdrawRequestNotFound):
			response.Error(w, http.StatusNotFound, "withdraw request not found")
		case errors.Is(err, service.ErrNoPendingSettlementOperations):
			response.Error(w, http.StatusBadRequest, "no pending settlement operations")
		case errors.Is(err, service.ErrOffchainSettlementUnavailable):
			response.Error(w, http.StatusBadRequest, "offchain settlement service unavailable")
		case errors.Is(err, service.ErrOffchainSettlementServiceRequired):
			response.Error(w, http.StatusBadRequest, "offchain settlement service is required")
		case errors.Is(err, service.ErrManualBatchDepositNotFound):
			response.Error(w, http.StatusNotFound, "deposit not found")
		case errors.Is(err, service.ErrManualBatchInsufficientOffchainBalance):
			response.Error(w, http.StatusBadRequest, "insufficient off-chain balance")
		default:
			response.Error(w, http.StatusBadRequest, err.Error())
		}

		return
	}

	response.JSON(w, http.StatusOK, result)
}

func (h *BatchHandler) SubmitBatch(w http.ResponseWriter, r *http.Request) {
	var req types.SubmitBatchRequestBody
	if !request.JSON(w, r, &req) {
		return
	}

	if req.SettlementUpdate.BatchID == "" {
		response.Error(w, http.StatusBadRequest, "settlementUpdate.batchId is required")
		return
	}

	if req.ProofBundle.Proof == "" {
		response.Error(w, http.StatusBadRequest, "proofBundle.proof is required")
		return
	}

	result, err := h.batchService.SubmitBatch(r.Context(), req)
	if err != nil {
		response.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}
