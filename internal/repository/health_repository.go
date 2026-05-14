package repository

import "context"

type HealthRepository struct{}

func NewHealthRepository() *HealthRepository {
	return &HealthRepository{}
}

func (r *HealthRepository) GetStatus(ctx context.Context) string {
	return "ok"
}
