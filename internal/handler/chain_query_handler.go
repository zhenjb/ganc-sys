package handler

import (
	"errors"
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
)

// ChainQueryHandler exposes read-only chain inspection endpoints.
//
// These routes are intentionally isolated under /api/chain/*
// so they cannot affect the core deposit/build/proof/submit/claim flow.
type ChainQueryHandler struct {
	chainQueryService *service.ChainQueryService
}

func NewChainQueryHandler(chainQueryService *service.ChainQueryService) *ChainQueryHandler {
	return &ChainQueryHandler{
		chainQueryService: chainQueryService,
	}
}

func (h *ChainQueryHandler) GetWithdrawRecord(w http.ResponseWriter, r *http.Request) {
	withdrawID := r.PathValue("withdrawId")
	if withdrawID == "" {
		response.Error(w, http.StatusBadRequest, "withdrawId is required")
		return
	}

	result, err := h.chainQueryService.GetWithdrawRecord(r.Context(), withdrawID)
	if err != nil {
		if errors.Is(err, service.ErrChainWithdrawRecordNotFound) {
			response.Error(w, http.StatusNotFound, "withdraw record not found")
			return
		}

		response.Error(w, http.StatusBadGateway, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}

func (h *ChainQueryHandler) GetNullifierUsed(w http.ResponseWriter, r *http.Request) {
	nullifier := r.PathValue("nullifier")
	if nullifier == "" {
		response.Error(w, http.StatusBadRequest, "nullifier is required")
		return
	}

	result, err := h.chainQueryService.GetNullifierUsed(r.Context(), nullifier)
	if err != nil {
		response.Error(w, http.StatusBadGateway, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, result)
}
