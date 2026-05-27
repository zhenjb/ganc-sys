package service

import (
	"context"
	"errors"

	batchbuilder "github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const (
	BatchBuildSourceManual  = "manual"
	BatchBuildSourcePending = "pending"
)

var ErrOffchainSettlementServiceRequired = errors.New("offchain settlement service is required for pending batch build source")

// BatchService owns batch endpoint orchestration.
//
// P4 owns this service as integration glue.
// P3 owns the actual batch builder / off-chain settlement implementation.
// P1 owns the actual batch submit implementation behind relayer.Client.
type BatchService struct {
	batchRepository    *repository.BatchRepository
	depositRepository  *repository.DepositRepository
	withdrawRepository *repository.WithdrawRepository
	batchBuilder       batchbuilder.Builder
	relayerClient      relayer.Client

	buildSource               string
	offchainSettlementService *OffchainSettlementService
}

func NewBatchService(
	batchRepository *repository.BatchRepository,
	depositRepository *repository.DepositRepository,
	withdrawRepository *repository.WithdrawRepository,
	batchBuilder batchbuilder.Builder,
	relayerClient relayer.Client,
) *BatchService {
	return &BatchService{
		batchRepository:    batchRepository,
		depositRepository:  depositRepository,
		withdrawRepository: withdrawRepository,
		batchBuilder:       batchBuilder,
		relayerClient:      relayerClient,
		buildSource:        BatchBuildSourceManual,
	}
}

func NewBatchServiceWithOffchainSettlement(
	batchRepository *repository.BatchRepository,
	depositRepository *repository.DepositRepository,
	withdrawRepository *repository.WithdrawRepository,
	batchBuilder batchbuilder.Builder,
	relayerClient relayer.Client,
	buildSource string,
	offchainSettlementService *OffchainSettlementService,
) *BatchService {
	if buildSource == "" {
		buildSource = BatchBuildSourceManual
	}

	return &BatchService{
		batchRepository:           batchRepository,
		depositRepository:         depositRepository,
		withdrawRepository:        withdrawRepository,
		batchBuilder:              batchBuilder,
		relayerClient:             relayerClient,
		buildSource:               buildSource,
		offchainSettlementService: offchainSettlementService,
	}
}

func (s *BatchService) BuildBatch(ctx context.Context, req types.BuildBatchRequestBody) (types.BuildBatchResponse, error) {
	if s.buildSource == BatchBuildSourcePending {
		return s.buildPendingBatch(ctx)
	}

	return s.buildManualBatch(ctx, req)
}

func (s *BatchService) buildManualBatch(ctx context.Context, req types.BuildBatchRequestBody) (types.BuildBatchResponse, error) {
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

	output, err := s.batchBuilder.Build(ctx, batchbuilder.BuildInput{
		OldStateRoot:     "0xrootA",
		Deposits:         deposits,
		WithdrawRequests: withdrawRequests,
	})
	if err != nil {
		return types.BuildBatchResponse{}, err
	}

	s.batchRepository.SaveBatchBuild(
		ctx,
		output.SettlementUpdate,
		output.BatchCommitments,
		output.Witness,
	)

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

func (s *BatchService) buildPendingBatch(ctx context.Context) (types.BuildBatchResponse, error) {
	if s.offchainSettlementService == nil {
		return types.BuildBatchResponse{}, ErrOffchainSettlementServiceRequired
	}

	output, err := s.offchainSettlementService.BuildPendingBatch(ctx, nil)
	if err != nil {
		return types.BuildBatchResponse{}, err
	}

	s.batchRepository.SaveBatchBuild(
		ctx,
		output.SettlementUpdate,
		output.BatchCommitments,
		output.Witness,
	)

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

func (s *BatchService) SubmitBatch(ctx context.Context, req types.SubmitBatchRequestBody) (types.SubmitBatchResponse, error) {
	result, err := s.relayerClient.SubmitBatch(ctx, relayer.SubmitBatchInput{
		SettlementUpdate: req.SettlementUpdate,
		BatchCommitments: req.BatchCommitments,
		ProofBundle:      req.ProofBundle,
	})
	if err != nil {
		return types.SubmitBatchResponse{}, err
	}

	s.batchRepository.SaveBatchSubmitResult(
		ctx,
		req.SettlementUpdate,
		req.BatchCommitments,
		result.TxHash,
		result.Accepted,
		result.ProofStatus,
		result.WithdrawRecords,
	)

	return types.SubmitBatchResponse{
		TxHash:           result.TxHash,
		Accepted:         result.Accepted,
		ProofStatus:      result.ProofStatus,
		SettlementUpdate: req.SettlementUpdate,
		BatchCommitments: req.BatchCommitments,
		WithdrawRecords:  result.WithdrawRecords,
		State: types.PartialState{
			CurrentStateRoot: req.SettlementUpdate.NewStateRoot,
			DepositStatus:    "processed",
			ProofStatus:      result.ProofStatus,
			WithdrawStatus:   "readyToClaim",
			BatchStatus:      "accepted",
		},
	}, nil
}
