package service

import (
	"context"

	batchbuilder "github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// BatchService owns batch endpoint orchestration.
//
// P4 owns this service as integration glue.
// P3 owns the actual batch builder implementation behind batch.Builder.
type BatchService struct {
	batchRepository    *repository.BatchRepository
	depositRepository  *repository.DepositRepository
	withdrawRepository *repository.WithdrawRepository
	batchBuilder       batchbuilder.Builder
}

func NewBatchService(
	batchRepository *repository.BatchRepository,
	depositRepository *repository.DepositRepository,
	withdrawRepository *repository.WithdrawRepository,
	batchBuilder batchbuilder.Builder,
) *BatchService {
	return &BatchService{
		batchRepository:    batchRepository,
		depositRepository:  depositRepository,
		withdrawRepository: withdrawRepository,
		batchBuilder:       batchBuilder,
	}
}

func (s *BatchService) BuildBatch(ctx context.Context, req types.BuildBatchRequestBody) (types.BuildBatchResponse, error) {
	deposits := make([]types.DepositRecord, 0, len(req.DepositIDs))
	for _, depositID := range req.DepositIDs {
		deposit, err := s.depositRepository.GetDeposit(ctx, depositID)
		if err != nil {
			return types.BuildBatchResponse{}, err
		}

		deposits = append(deposits, deposit)
	}

	withdrawRequests := make([]types.WithdrawRequest, 0, len(req.WithdrawIDs))
	for _, withdrawID := range req.WithdrawIDs {
		withdrawReq, err := s.withdrawRepository.GetWithdrawRequest(ctx, withdrawID)
		if err != nil {
			return types.BuildBatchResponse{}, err
		}

		withdrawRequests = append(withdrawRequests, withdrawReq)
	}

	// P4 integration point:
	// This calls the P3 batch builder interface.
	// Today this is wired to batch.LocalBuilder.
	// Later it should be replaced with P3's real implementation.
	output, err := s.batchBuilder.Build(ctx, batchbuilder.BuildInput{
		OldStateRoot:     "0xrootA",
		Deposits:         deposits,
		WithdrawRequests: withdrawRequests,
	})
	if err != nil {
		return types.BuildBatchResponse{}, err
	}

	return types.BuildBatchResponse{
		SettlementUpdate: output.SettlementUpdate,
		BatchCommitments: output.BatchCommitments,
		Witness:          output.Witness,
		State: types.PartialState{
			BatchStatus:    "built",
			ProofStatus:    "idle",
			WithdrawStatus: "batchBuilt",
		},
	}, nil
}

func (s *BatchService) SubmitBatch(ctx context.Context, req types.SubmitBatchRequestBody) types.SubmitBatchResponse {
	// TODO(INT-09 / P1):
	// Submit MsgSubmitBatchProof to x/zkdex and index resulting events.
	withdrawRecord := s.withdrawRepository.GetLocalWithdrawRecord(ctx, false)

	return types.SubmitBatchResponse{
		TxHash:           "0xmocksubmitbatch",
		Accepted:         true,
		ProofStatus:      "accepted",
		SettlementUpdate: s.batchRepository.GetLocalSettlementUpdate(ctx),
		BatchCommitments: s.batchRepository.GetLocalBatchCommitments(ctx),
		WithdrawRecords:  []types.WithdrawRecord{withdrawRecord},
		State: types.PartialState{
			CurrentStateRoot: "0xrootB",
			DepositStatus:    "processed",
			ProofStatus:      "accepted",
			WithdrawStatus:   "readyToClaim",
			BatchStatus:      "accepted",
		},
	}
}
