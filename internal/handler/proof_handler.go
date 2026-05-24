package handler

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/request"
	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ProofHandler exposes proof generation endpoints.
//
// P4 owns the HTTP boundary.
// P2 owns the prover implementation called by ProofService.
type ProofHandler struct {
	proofService *service.ProofService
}

func NewProofHandler(proofService *service.ProofService) *ProofHandler {
	return &ProofHandler{
		proofService: proofService,
	}
}

func (h *ProofHandler) GenerateProof(w http.ResponseWriter, r *http.Request) {
	var req types.GenerateProofRequestBody
	if !request.JSON(w, r, &req) {
		return
	}

	if req.SettlementUpdate.BatchID == "" {
		response.Error(w, http.StatusBadRequest, "settlementUpdate is required")
		return
	}

	if req.BatchCommitments.DepositsRoot == "" {
		response.Error(w, http.StatusBadRequest, "batchCommitments is required")
		return
	}

	if len(req.Witness.Accounts) == 0 {
		response.Error(w, http.StatusBadRequest, "witness.accounts is required")
		return
	}

	result, err := h.proofService.GenerateProof(r.Context(), req)
	if err != nil {
		response.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}
