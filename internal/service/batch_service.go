package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// BatchService owns batch build and batch submit use cases.
//
// INT-04 status:
// - BuildBatch returns local deterministic batch-shaped data.
// - SubmitBatch returns local deterministic accepted result.
// - P3 batch builder is not connected yet.
// - P1 MsgSubmitBatchProof is not connected yet.
//
// TODO(INT-07 / P3):
// BuildBatch should call P3 batch builder using:
// - req.DepositIDs,
// - req.WithdrawIDs,
// - indexed deposit records,
// - persisted withdrawal requests.
//
// TODO(INT-09 / P1):
// SubmitBatch should submit MsgSubmitBatchProof(
// settlementUpdate,
// batchCommitments,
// proofBundle,
// ) to x/zkdex and index resulting withdrawRecords[].
type BatchService struct {
	batchRepository    *repository.BatchRepository
	withdrawRepository *repository.WithdrawRepository
}

func NewBatchService(
	batchRepository *repository.BatchRepository,
	withdrawRepository *repository.WithdrawRepository,
) *BatchService {
	return &BatchService{
		batchRepository:    batchRepository,
		withdrawRepository: withdrawRepository,
	}
}

func (s *BatchService) BuildBatch(ctx context.Context, req types.BuildBatchRequestBody) types.BuildBatchResponse {
	// TODO(INT-07):
	// Replace local fixture with real P3 batch builder output.
	return types.BuildBatchResponse{
		SettlementUpdate: s.batchRepository.GetLocalSettlementUpdate(ctx),
		BatchCommitments: s.batchRepository.GetLocalBatchCommitments(ctx),
		Witness:          s.batchRepository.GetLocalWitness(ctx),
		State: types.PartialState{
			BatchStatus:    "built",
			ProofStatus:    "idle",
			WithdrawStatus: "batchBuilt",
		},
	}
}

func (s *BatchService) SubmitBatch(ctx context.Context, req types.SubmitBatchRequestBody) types.SubmitBatchResponse {
	// TODO(INT-09):
	// Submit to chain and build withdrawRecords[] from chain events/query.
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
