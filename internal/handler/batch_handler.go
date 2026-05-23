package handler

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/request"
	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// BatchHandler exposes batch build and batch submit endpoints.
//
// INT-04 status:
// - POST /api/batch/build returns local deterministic batch-shaped data.
// - POST /api/batch/submit returns local deterministic accepted result.
// - P3 batch builder is not connected yet.
// - P1 MsgSubmitBatchProof is not connected yet.
//
// TODO(INT-07):
// BuildBatch should call P3 batch builder with depositIds[] and withdrawIds[].
//
// TODO(INT-09):
// SubmitBatch should submit MsgSubmitBatchProof to x/zkdex and index the emitted
// batch/withdrawal events.
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

	result := h.batchService.BuildBatch(r.Context(), req)
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

	result := h.batchService.SubmitBatch(r.Context(), req)
	response.JSON(w, http.StatusOK, result)
}
