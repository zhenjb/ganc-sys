package repository

import "context"

// HealthRepository provides basic service health data.
//
// This repository has no external dependency.
type HealthRepository struct{}

func NewHealthRepository() *HealthRepository {
	return &HealthRepository{}
}

func (r *HealthRepository) GetStatus(ctx context.Context) string {
	return "ok"
}
