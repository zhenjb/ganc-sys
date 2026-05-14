package chain

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

type Client interface {
	Deposit(ctx context.Context, req DepositRequest) (DepositResult, error)
}

type DepositRequest struct {
	Owner  string
	Denom  string
	Amount string
}

type DepositResult struct {
	TxHash        string
	DepositRecord types.DepositRecord
}
