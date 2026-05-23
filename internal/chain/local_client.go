package chain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/zhenjb/ganc-sys/internal/event"
)

// LocalClient is a local development implementation of the chain Client.
//
// It simulates the on-chain MsgDeposit flow by returning a tx result containing
// the same typed event that the real x/zkdex module emits:
//
//	event type: ob.zkdex.v1.EventDeposit
//
// The real CosmosClient should later return the actual tx result and events
// from the chain. The deposit indexer should not care whether the event came
// from LocalClient or CosmosClient.
type LocalClient struct {
	nextDepositSeq int
}

func NewLocalClient() *LocalClient {
	return &LocalClient{
		nextDepositSeq: 1,
	}
}

func (c *LocalClient) Deposit(ctx context.Context, req DepositRequest) (TxResult, error) {
	if req.Owner == "" || req.Denom == "" || req.Amount == "" {
		return TxResult{}, fmt.Errorf("owner, denom and amount are required")
	}

	amount, err := strconv.ParseInt(req.Amount, 10, 64)
	if err != nil || amount <= 0 {
		return TxResult{}, fmt.Errorf("amount must be a positive integer string")
	}

	depositID := fmt.Sprintf("dep-%d", c.nextDepositSeq)
	c.nextDepositSeq++

	height := time.Now().Unix()
	txHash := localTxHash("deposit", req.Owner, req.Denom, req.Amount, depositID)

	return TxResult{
		TxHash: txHash,
		Height: height,
		Events: []event.Event{
			{
				Type: event.TypeDeposit,
				Attributes: map[string]string{
					// The real chain emits typed EventDeposit fields:
					// DepositId, Creator, Denom, Amount.
					//
					// We use camelCase here for local JSON-style attributes.
					// DepositIndexer also supports snake_case for Cosmos event compatibility.
					"depositId": depositID,
					"creator":   req.Owner,
					"denom":     req.Denom,
					"amount":    req.Amount,
				},
			},
		},
	}, nil
}

func localTxHash(parts ...string) string {
	h := sha256.New()

	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}

	return "0x" + hex.EncodeToString(h.Sum(nil))[:32]
}
