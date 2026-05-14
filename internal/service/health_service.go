package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/repository"
)

type HealthService struct {
	healthRepository *repository.HealthRepository
}

func NewHealthService(healthRepository *repository.HealthRepository) *HealthService {
	return &HealthService{
		healthRepository: healthRepository,
	}
}

func (s *HealthService) GetHealth(ctx context.Context) HealthResponse {
	status := s.healthRepository.GetStatus(ctx)

	return HealthResponse{
		Status:  status,
		Service: "offchain-backend",
	}
}

type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}
