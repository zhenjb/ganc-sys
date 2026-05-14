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

type MockClient struct {
	nextDepositSeq int
}

func NewMockClient() *MockClient {
	return &MockClient{
		nextDepositSeq: 1,
	}
}

func (c *MockClient) Deposit(ctx context.Context, req DepositRequest) (DepositResult, error) {
	if req.Owner == "" || req.Denom == "" || req.Amount == "" {
		return DepositResult{}, fmt.Errorf("owner, denom and amount are required")
	}

	amount, err := strconv.ParseInt(req.Amount, 10, 64)
	if err != nil || amount <= 0 {
		return DepositResult{}, fmt.Errorf("amount must be a positive integer string")
	}

	depositID := fmt.Sprintf("dep-%d", c.nextDepositSeq)
	c.nextDepositSeq++

	txHash := mockTxHash("deposit", req.Owner, req.Denom, req.Amount, depositID)

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

func mockTxHash(parts ...string) string {
	h := sha256.New()

	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}

	return "0x" + hex.EncodeToString(h.Sum(nil))[:32]
}
