package handler

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
)

type StateHandler struct {
	stateService *service.StateService
}

func NewStateHandler(stateService *service.StateService) *StateHandler {
	return &StateHandler{
		stateService: stateService,
	}
}

func (h *StateHandler) GetState(w http.ResponseWriter, r *http.Request) {
	result := h.stateService.GetState(r.Context())

	response.JSON(w, http.StatusOK, result)
}
