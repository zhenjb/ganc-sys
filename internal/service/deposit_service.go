package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/indexer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// DepositService owns deposit use cases.
//
// INT-05 status:
// - CreateDeposit calls chain.Client.Deposit.
// - The chain client returns TxResult with events.
// - DepositIndexer consumes EventDeposit and saves DepositRecord.
// - Response returns the indexed DepositRecord.
//
// Later, LocalClient can be replaced by CosmosClient without changing this flow.
type DepositService struct {
	depositRepository *repository.DepositRepository
	depositIndexer    *indexer.DepositIndexer
	chainClient       chain.Client
}

func NewDepositService(
	depositRepository *repository.DepositRepository,
	depositIndexer *indexer.DepositIndexer,
	chainClient chain.Client,
) *DepositService {
	return &DepositService{
		depositRepository: depositRepository,
		depositIndexer:    depositIndexer,
		chainClient:       chainClient,
	}
}

func (s *DepositService) CreateDeposit(ctx context.Context, req types.DepositRequestBody) (types.DepositResponse, error) {
	txResult, err := s.chainClient.Deposit(ctx, chain.DepositRequest{
		Owner:  req.Owner,
		Denom:  req.Denom,
		Amount: req.Amount,
	})
	if err != nil {
		return types.DepositResponse{}, err
	}

	depositRecord, err := s.depositIndexer.IndexDepositFromTx(ctx, txResult)
	if err != nil {
		return types.DepositResponse{}, err
	}

	return types.DepositResponse{
		TxHash:        txResult.TxHash,
		DepositRecord: depositRecord,
		State: types.PartialState{
			CurrentStateRoot: "0xrootA",
			DepositStatus:    "indexed",
			ProofStatus:      "idle",
			WithdrawStatus:   "none",
			BatchStatus:      "none",
		},
	}, nil
}

func (s *DepositService) ListDeposits(ctx context.Context) types.ListDepositsResponse {
	return types.ListDepositsResponse{
		Deposits: s.depositRepository.ListDeposits(ctx),
	}
}

func (s *DepositService) GetDeposit(ctx context.Context, depositID string) (types.GetDepositResponse, error) {
	record, err := s.depositRepository.GetDeposit(ctx, depositID)
	if err != nil {
		return types.GetDepositResponse{}, err
	}

	return types.GetDepositResponse{
		DepositRecord: record,
	}, nil
}
