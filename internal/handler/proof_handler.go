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
// INT-04 status:
// - POST /api/proof/generate returns a local deterministic ProofBundle.
// - P2 prover is not connected yet.
// - No real ZK proof is generated here yet.
//
// TODO(INT-08):
// Call the real prover with SettlementUpdate + BatchCommitments + Witness.
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

	result := h.proofService.GenerateProof(r.Context(), req)
	response.JSON(w, http.StatusOK, result)
}
