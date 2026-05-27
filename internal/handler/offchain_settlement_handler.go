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

type OffchainSettlementHandler struct {
	offchainSettlementService *service.OffchainSettlementService
}

func NewOffchainSettlementHandler(
	offchainSettlementService *service.OffchainSettlementService,
) *OffchainSettlementHandler {
	return &OffchainSettlementHandler{
		offchainSettlementService: offchainSettlementService,
	}
}

func (h *OffchainSettlementHandler) CancelIncludedBatch(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("batchId")
	if batchID == "" {
		response.Error(w, http.StatusBadRequest, "batchId is required")
		return
	}

	if h.offchainSettlementService == nil {
		response.Error(w, http.StatusBadRequest, "offchain settlement service unavailable")
		return
	}

	var req types.CancelOffchainSettlementBatchRequestBody
	if !request.JSON(w, r, &req) {
		return
	}

	if req.Reason == "" {
		req.Reason = "manual cancel before submit"
	}

	err := h.offchainSettlementService.CancelIncludedBatch(r.Context(), batchID, req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOffchainSettlementUnavailable):
			response.Error(w, http.StatusBadRequest, "offchain settlement service unavailable")
		case errors.Is(err, repository.ErrOffchainSettlementRecordNotFound):
			response.Error(w, http.StatusNotFound, "included settlement batch not found")
		default:
			response.Error(w, http.StatusBadRequest, err.Error())
		}

		return
	}

	response.JSON(w, http.StatusOK, types.CancelOffchainSettlementBatchResponse{
		BatchID: batchID,
		Status:  "reopened",
		Reason:  req.Reason,
	})
}
