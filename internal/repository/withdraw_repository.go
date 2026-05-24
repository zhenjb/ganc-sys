package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

var ErrWithdrawRequestNotFound = errors.New("withdraw request not found")

// WithdrawRepository owns withdrawal request and withdrawal record access.
//
// INT-06 status:
// - Withdraw requests are now created from user input.
// - Withdraw requests are saved in local MemoryStore.
// - P3 can query withdraw requests by withdrawId for batch building.
//
// Still local/stubbed:
// - signature is a deterministic local placeholder.
// - no real user authorization signature is verified yet.
// - claim withdraw still returns local deterministic data.
//
// TODO(INT-07 / P3):
// Batch builder should consume persisted withdraw requests.
//
// TODO(INT-10 / P1):
// Replace local claim fixture with MsgClaimWithdraw chain integration.
type WithdrawRepository struct {
	store *store.MemoryStore
}

func NewWithdrawRepository(store *store.MemoryStore) *WithdrawRepository {
	return &WithdrawRepository{
		store: store,
	}
}

func (r *WithdrawRepository) CreateWithdrawRequest(ctx context.Context, req types.WithdrawRequestBody) types.WithdrawRequest {
	seq := r.store.NextWithdrawSequence()

	withdrawID := fmt.Sprintf("wd-%d", seq)
	nonce := strconv.Itoa(seq)

	withdrawRequest := types.WithdrawRequest{
		WithdrawID:  withdrawID,
		Owner:       req.Owner,
		Denom:       req.Denom,
		Amount:      req.Amount,
		Destination: req.Destination,
		Nonce:       nonce,
		Signature:   localWithdrawSignature(req.Owner, req.Denom, req.Amount, req.Destination, nonce),
	}

	r.store.SaveWithdrawRequest(withdrawRequest)

	return withdrawRequest
}

func (r *WithdrawRepository) GetWithdrawRequest(ctx context.Context, withdrawID string) (types.WithdrawRequest, error) {
	request, ok := r.store.GetWithdrawRequest(withdrawID)
	if !ok {
		return types.WithdrawRequest{}, ErrWithdrawRequestNotFound
	}

	return request, nil
}

func (r *WithdrawRepository) ListWithdrawRequests(ctx context.Context) []types.WithdrawRequest {
	return r.store.ListWithdrawRequests()
}

func (r *WithdrawRepository) GetLocalWithdrawRecord(ctx context.Context, claimed bool) types.WithdrawRecord {
	return types.WithdrawRecord{
		WithdrawID:  "wd-1",
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
		Nullifier:   "0xmocknullifier",
		Claimed:     claimed,
	}
}

func (r *WithdrawRepository) GetLocalClaimBalanceSnapshot(ctx context.Context) types.BalanceSnapshot {
	return types.BalanceSnapshot{
		UserBalances: map[string]string{
			"cosmos1alice/uusdc": "940",
		},
		ModuleAccountBalance: map[string]string{
			"uusdc": "60",
		},
	}
}

func localWithdrawSignature(parts ...string) string {
	h := sha256.New()

	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}

	return "0x" + hex.EncodeToString(h.Sum(nil))[:32]
}
