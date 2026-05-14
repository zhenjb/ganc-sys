package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

type StateService struct {
	stateRepository *repository.StateRepository
}

func NewStateService(stateRepository *repository.StateRepository) *StateService {
	return &StateService{
		stateRepository: stateRepository,
	}
}

func (s *StateService) GetState(ctx context.Context) types.AppState {
	return s.stateRepository.GetState(ctx)
}
