package handler

import (
	"errors"
	"net/http"

	batchbuilder "github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/request"
	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// BatchHandler exposes batch build and submit endpoints.
//
// P4 owns the HTTP boundary.
// P3 owns the builder implementation called by BatchService.
// P1 owns the relayer/chain submit implementation called by BatchService.
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

	if len(req.DepositIDs) == 0 || len(req.WithdrawIDs) == 0 {
		response.Error(w, http.StatusBadRequest, "depositIds and withdrawIds are required")
		return
	}

	result, err := h.batchService.BuildBatch(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrDepositNotFound):
			response.Error(w, http.StatusNotFound, "deposit not found")
		case errors.Is(err, repository.ErrWithdrawRequestNotFound):
			response.Error(w, http.StatusNotFound, "withdraw request not found")
		case errors.Is(err, batchbuilder.ErrInsufficientOffchainBalance):
			response.Error(w, http.StatusBadRequest, "insufficient off-chain balance")
		default:
			response.Error(w, http.StatusInternalServerError, err.Error())
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

	if req.SettlementUpdate.BatchID == "" ||
		req.BatchCommitments.DepositsRoot == "" ||
		req.ProofBundle.Proof == "" {
		response.Error(w, http.StatusBadRequest, "settlementUpdate, batchCommitments and proofBundle are required")
		return
	}

	result, err := h.batchService.SubmitBatch(r.Context(), req)
	if err != nil {
		response.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}
