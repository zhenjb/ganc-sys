package service

import (
	"context"
	"errors"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

var ErrChainWithdrawRecordNotFound = errors.New("withdraw record not found")

// ChainQueryService owns read-only chain inspection endpoints.
//
// These endpoints are support/debug/failure-demo queries.
// They do not mutate MemoryStore and do not participate in the core local flow.
type ChainQueryService struct {
	chainQueryClient *chain.RestQueryClient
}

func NewChainQueryService(chainQueryClient *chain.RestQueryClient) *ChainQueryService {
	return &ChainQueryService{
		chainQueryClient: chainQueryClient,
	}
}

type ChainWithdrawRecordResponse struct {
	WithdrawRecord types.WithdrawRecord `json:"withdrawRecord"`
}

type ChainNullifierUsedResponse struct {
	Nullifier string `json:"nullifier"`
	Used      bool   `json:"used"`
}

func (s *ChainQueryService) GetWithdrawRecord(
	ctx context.Context,
	withdrawID string,
) (ChainWithdrawRecordResponse, error) {
	record, found, err := s.chainQueryClient.GetWithdrawRecord(ctx, withdrawID)
	if err != nil {
		return ChainWithdrawRecordResponse{}, err
	}

	if !found {
		return ChainWithdrawRecordResponse{}, ErrChainWithdrawRecordNotFound
	}

	return ChainWithdrawRecordResponse{
		WithdrawRecord: record,
	}, nil
}

func (s *ChainQueryService) GetNullifierUsed(
	ctx context.Context,
	nullifier string,
) (ChainNullifierUsedResponse, error) {
	used, err := s.chainQueryClient.GetNullifierUsed(ctx, nullifier)
	if err != nil {
		return ChainNullifierUsedResponse{}, err
	}

	return ChainNullifierUsedResponse{
		Nullifier: nullifier,
		Used:      used,
	}, nil
}
