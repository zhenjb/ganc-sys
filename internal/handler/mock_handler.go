package handler

import (
	"encoding/json"
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

type MockHandler struct {
	mockService *service.MockService
}

func NewMockHandler(mockService *service.MockService) *MockHandler {
	return &MockHandler{
		mockService: mockService,
	}
}

func (h *MockHandler) GetState(w http.ResponseWriter, r *http.Request) {
	result := h.mockService.GetState(r.Context())

	response.JSON(w, http.StatusOK, result)
}

func (h *MockHandler) MockDeposit(w http.ResponseWriter, r *http.Request) {
	var req types.DepositRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Owner == "" || req.Denom == "" || req.Amount == "" {
		response.Error(w, http.StatusBadRequest, "owner, denom and amount are required")
		return
	}

	result, err := h.mockService.MockDeposit(r.Context(), req)
	if err != nil {
		response.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}

func (h *MockHandler) MockWithdrawRequest(w http.ResponseWriter, r *http.Request) {
	var req types.WithdrawRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Owner == "" || req.Denom == "" || req.Amount == "" || req.Destination == "" {
		response.Error(w, http.StatusBadRequest, "owner, denom, amount and destination are required")
		return
	}

	result := h.mockService.MockWithdrawRequest(r.Context(), req)
	response.JSON(w, http.StatusOK, result)
}

func (h *MockHandler) MockBuildBatch(w http.ResponseWriter, r *http.Request) {
	var req types.BuildBatchRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}

	if len(req.DepositIDs) == 0 || len(req.WithdrawIDs) == 0 {
		response.Error(w, http.StatusBadRequest, "depositIds and withdrawIds are required")
		return
	}

	result := h.mockService.MockBuildBatch(r.Context(), req)
	response.JSON(w, http.StatusOK, result)
}

func (h *MockHandler) MockGenerateProof(w http.ResponseWriter, r *http.Request) {
	var req types.GenerateProofRequestBody
	if !decodeJSON(w, r, &req) {
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

	result := h.mockService.MockGenerateProof(r.Context(), req)
	response.JSON(w, http.StatusOK, result)
}

func (h *MockHandler) MockSubmitBatch(w http.ResponseWriter, r *http.Request) {
	var req types.SubmitBatchRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.SettlementUpdate.BatchID == "" ||
		req.BatchCommitments.DepositsRoot == "" ||
		req.ProofBundle.Proof == "" {
		response.Error(w, http.StatusBadRequest, "settlementUpdate, batchCommitments and proofBundle are required")
		return
	}

	result := h.mockService.MockSubmitBatch(r.Context(), req)
	response.JSON(w, http.StatusOK, result)
}

func (h *MockHandler) MockClaimWithdraw(w http.ResponseWriter, r *http.Request) {
	var req types.ClaimWithdrawRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.WithdrawID == "" {
		response.Error(w, http.StatusBadRequest, "withdrawId is required")
		return
	}

	result := h.mockService.MockClaimWithdraw(r.Context(), req)
	response.JSON(w, http.StatusOK, result)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}

	return true
}
