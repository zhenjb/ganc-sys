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
var ErrWithdrawRecordNotFound = errors.New("withdraw record not found")
var ErrWithdrawAlreadyClaimed = errors.New("withdraw already claimed")

// WithdrawRepository owns withdrawal request and withdrawal record access.
//
// INT-10 status:
// - Withdraw requests are persisted locally.
// - Submit batch stores withdrawRecords[] locally.
// - Claim withdraw reads and updates withdrawRecords locally.
//
// Still local/stubbed:
// - real MsgClaimWithdraw is not connected yet.
// - balances are local deterministic snapshots.
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

func (r *WithdrawRepository) SaveWithdrawRecords(ctx context.Context, records []types.WithdrawRecord) {
	for _, record := range records {
		r.store.SaveWithdrawRecord(record)
	}
}

func (r *WithdrawRepository) SaveWithdrawRecord(ctx context.Context, record types.WithdrawRecord) {
	r.store.SaveWithdrawRecord(record)
}

func (r *WithdrawRepository) GetWithdrawRecord(ctx context.Context, withdrawID string) (types.WithdrawRecord, error) {
	record, ok := r.store.GetWithdrawRecord(withdrawID)
	if !ok {
		return types.WithdrawRecord{}, ErrWithdrawRecordNotFound
	}

	return record, nil
}

func (r *WithdrawRepository) ClaimWithdrawRecord(ctx context.Context, withdrawID string) (types.WithdrawRecord, error) {
	record, ok := r.store.GetWithdrawRecord(withdrawID)
	if !ok {
		return types.WithdrawRecord{}, ErrWithdrawRecordNotFound
	}

	if record.Claimed {
		return types.WithdrawRecord{}, ErrWithdrawAlreadyClaimed
	}

	record.Claimed = true
	r.store.SaveWithdrawRecord(record)

	return record, nil
}

func (r *WithdrawRepository) ListWithdrawRecords(ctx context.Context) []types.WithdrawRecord {
	return r.store.ListWithdrawRecords()
}

func (r *WithdrawRepository) GetLocalClaimBalanceSnapshot(ctx context.Context, record types.WithdrawRecord) types.BalanceSnapshot {
	// TODO(INT-10 / P1):
	// Replace with real chain balance query after MsgClaimWithdraw.
	//
	// Current local meaning:
	// - user receives claimed withdraw amount,
	// - module balance keeps remaining amount from the demo deposit path.
	return types.BalanceSnapshot{
		UserBalances: map[string]string{
			record.Destination + "/" + record.Denom: record.Amount,
		},
		ModuleAccountBalance: map[string]string{
			record.Denom: "60",
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
