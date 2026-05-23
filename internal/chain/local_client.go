package chain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// LocalClient is a local development implementation of the chain Client.
//
// TODO(INT-05+ / P1 integration):
// Replace this local implementation with a real Cosmos client that:
// 1. builds and signs MsgDeposit,
// 2. broadcasts the tx,
// 3. reads tx result/events from the chain,
// 4. returns event-backed data for the indexer.
//
// For now this still creates deterministic local data so P4/P5 can continue
// developing before the real x/zkdex module is ready.
type LocalClient struct {
	nextDepositSeq int
}

func NewLocalClient() *LocalClient {
	return &LocalClient{
		nextDepositSeq: 1,
	}
}

func (c *LocalClient) Deposit(ctx context.Context, req DepositRequest) (DepositResult, error) {
	if req.Owner == "" || req.Denom == "" || req.Amount == "" {
		return DepositResult{}, fmt.Errorf("owner, denom and amount are required")
	}

	amount, err := strconv.ParseInt(req.Amount, 10, 64)
	if err != nil || amount <= 0 {
		return DepositResult{}, fmt.Errorf("amount must be a positive integer string")
	}

	depositID := fmt.Sprintf("dep-%d", c.nextDepositSeq)
	c.nextDepositSeq++

	txHash := localTxHash("deposit", req.Owner, req.Denom, req.Amount, depositID)

	// TODO(INT-05):
	// This DepositRecord should eventually be produced by parsing the
	// zkdex.deposit_queued event emitted by the chain, not constructed
	// directly here. The current return shape is kept temporarily so INT-04
	// remains stable until the event indexer is introduced.
	depositRecord := types.DepositRecord{
		DepositID:     depositID,
		Owner:         req.Owner,
		Denom:         req.Denom,
		Amount:        req.Amount,
		Processed:     false,
		CreatedHeight: time.Now().Unix(),
		TxHash:        txHash,
	}

	return DepositResult{
		TxHash:        txHash,
		DepositRecord: depositRecord,
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
