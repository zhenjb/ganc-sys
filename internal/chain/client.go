package chain

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/event"
)

type Client interface {
	Deposit(ctx context.Context, req DepositRequest) (TxResult, error)
}

type DepositRequest struct {
	Owner  string
	Denom  string
	Amount string
}

type TxResult struct {
	TxHash string        `json:"txHash"`
	Height int64         `json:"height"`
	Events []event.Event `json:"events"`
}
