package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// DepositService owns deposit use cases.
//
// INT-04 status:
// - CreateDeposit uses chain.Client interface.
// - Current chain implementation is LocalClient.
// - DepositRecord is still returned directly from the local chain client.
//
// TODO(INT-05):
// Change the flow to:
// 1. chainClient.Deposit(...) broadcasts MsgDeposit,
// 2. chain returns tx result with emitted events,
// 3. DepositIndexer consumes zkdex.deposit_queued event,
// 4. DepositRepository stores indexed DepositRecord,
// 5. response returns the indexed DepositRecord.
//
// The final source of truth for deposits must be indexed on-chain events.
type DepositService struct {
	depositRepository *repository.DepositRepository
	chainClient       chain.Client
}

func NewDepositService(
	depositRepository *repository.DepositRepository,
	chainClient chain.Client,
) *DepositService {
	return &DepositService{
		depositRepository: depositRepository,
		chainClient:       chainClient,
	}
}

func (s *DepositService) CreateDeposit(ctx context.Context, req types.DepositRequestBody) (types.DepositResponse, error) {
	result, err := s.chainClient.Deposit(ctx, chain.DepositRequest{
		Owner:  req.Owner,
		Denom:  req.Denom,
		Amount: req.Amount,
	})
	if err != nil {
		return types.DepositResponse{}, err
	}

	// TODO(INT-05):
	// Do not rely on result.DepositRecord as final source of truth.
	// Replace this with the record produced by the event indexer.
	return types.DepositResponse{
		TxHash:        result.TxHash,
		DepositRecord: result.DepositRecord,
		State: types.PartialState{
			CurrentStateRoot: "0xrootA",
			DepositStatus:    "locked",
			ProofStatus:      "idle",
			WithdrawStatus:   "none",
			BatchStatus:      "none",
		},
	}, nil
}
