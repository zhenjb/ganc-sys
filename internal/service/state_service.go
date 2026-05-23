package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// StateService coordinates dashboard state reads.
//
// INT-04 status:
// - Returns local initial state through StateRepository.
// - Does not read indexed deposits yet.
// - Does not query chain balances yet.
//
// TODO(INT-05+):
// Build state from indexed deposits, indexed withdrawal records,
// batch/proof status, and chain/module balance queries.
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
