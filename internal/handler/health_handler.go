package handler

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/response"
	"github.com/zhenjb/ganc-sys/internal/service"
)

type HealthHandler struct {
	healthService *service.HealthService
}

func NewHealthHandler(healthService *service.HealthService) *HealthHandler {
	return &HealthHandler{
		healthService: healthService,
	}
}

func (h *HealthHandler) GetHealth(w http.ResponseWriter, r *http.Request) {
	result := h.healthService.GetHealth(r.Context())

	response.JSON(w, http.StatusOK, result)
}
