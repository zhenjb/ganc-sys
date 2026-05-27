package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

var ErrWithdrawRequestNotFound = errors.New("withdraw request not found")
var ErrWithdrawRecordNotFound = errors.New("withdraw record not found")
var ErrWithdrawAlreadyClaimed = errors.New("withdraw already claimed")

const WithdrawRequestStoreMemory = "memory"
const WithdrawRequestStorePostgres = "postgres"

// WithdrawRepository owns withdrawal request and withdrawal record access.
//
// DB-02 status:
// - withdraw_requests can be persisted in Postgres.
// - default mode remains MemoryStore for local tests.
// - withdrawRecords remain MemoryStore for now; they will move in a later DB task.
type WithdrawRepository struct {
	store *store.MemoryStore

	dbPool       *pgxpool.Pool
	requestStore string
}

func NewWithdrawRepository(store *store.MemoryStore) *WithdrawRepository {
	return &WithdrawRepository{
		store:        store,
		requestStore: WithdrawRequestStoreMemory,
	}
}

func NewWithdrawRepositoryWithDB(
	store *store.MemoryStore,
	dbPool *pgxpool.Pool,
	requestStore string,
) *WithdrawRepository {
	if requestStore == "" {
		requestStore = WithdrawRequestStoreMemory
	}

	return &WithdrawRepository{
		store:        store,
		dbPool:       dbPool,
		requestStore: requestStore,
	}
}

func (r *WithdrawRepository) CreateWithdrawRequest(ctx context.Context, req types.WithdrawRequestBody) types.WithdrawRequest {
	if r.requestStore == WithdrawRequestStorePostgres {
		withdrawReq, err := r.createWithdrawRequestPostgres(ctx, req)
		if err != nil {
			panic(fmt.Sprintf("create withdraw request in postgres: %v", err))
		}

		return withdrawReq
	}

	return r.createWithdrawRequestMemory(req)
}

func (r *WithdrawRepository) GetWithdrawRequest(ctx context.Context, withdrawID string) (types.WithdrawRequest, error) {
	if r.requestStore == WithdrawRequestStorePostgres {
		return r.getWithdrawRequestPostgres(ctx, withdrawID)
	}

	return r.getWithdrawRequestMemory(withdrawID)
}

func (r *WithdrawRepository) ListWithdrawRequests(ctx context.Context) []types.WithdrawRequest {
	if r.requestStore == WithdrawRequestStorePostgres {
		requests, err := r.listWithdrawRequestsPostgres(ctx)
		if err != nil {
			panic(fmt.Sprintf("list withdraw requests from postgres: %v", err))
		}

		return requests
	}

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

	claimedRecord, ok := r.store.ClaimWithdrawRecord(withdrawID)
	if !ok {
		return types.WithdrawRecord{}, ErrWithdrawRecordNotFound
	}

	return claimedRecord, nil
}

func (r *WithdrawRepository) ListWithdrawRecords(ctx context.Context) []types.WithdrawRecord {
	return r.store.ListWithdrawRecords()
}

func (r *WithdrawRepository) GetLocalClaimBalanceSnapshot(ctx context.Context, record types.WithdrawRecord) types.BalanceSnapshot {
	return r.store.GetBalanceSnapshot()
}

func (r *WithdrawRepository) createWithdrawRequestMemory(req types.WithdrawRequestBody) types.WithdrawRequest {
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

func (r *WithdrawRepository) getWithdrawRequestMemory(withdrawID string) (types.WithdrawRequest, error) {
	request, ok := r.store.GetWithdrawRequest(withdrawID)
	if !ok {
		return types.WithdrawRequest{}, ErrWithdrawRequestNotFound
	}

	return request, nil
}

func (r *WithdrawRepository) createWithdrawRequestPostgres(
	ctx context.Context,
	req types.WithdrawRequestBody,
) (types.WithdrawRequest, error) {
	if r.dbPool == nil {
		return types.WithdrawRequest{}, errors.New("postgres withdraw request store selected but db pool is nil")
	}

	var seq int64
	if err := r.dbPool.QueryRow(ctx, "SELECT nextval('withdraw_request_seq')").Scan(&seq); err != nil {
		return types.WithdrawRequest{}, err
	}

	withdrawID := fmt.Sprintf("wd-%d", seq)
	nonce := strconv.FormatInt(seq, 10)
	signature := localWithdrawSignature(req.Owner, req.Denom, req.Amount, req.Destination, nonce)

	withdrawRequest := types.WithdrawRequest{
		WithdrawID:  withdrawID,
		Owner:       req.Owner,
		Denom:       req.Denom,
		Amount:      req.Amount,
		Destination: req.Destination,
		Nonce:       nonce,
		Signature:   signature,
	}

	_, err := r.dbPool.Exec(
		ctx,
		`
		INSERT INTO withdraw_requests (
			withdraw_id,
			owner_address,
			denom,
			amount,
			destination_address,
			nonce,
			signature,
			status,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'requested', NOW())
		`,
		withdrawRequest.WithdrawID,
		withdrawRequest.Owner,
		withdrawRequest.Denom,
		withdrawRequest.Amount,
		withdrawRequest.Destination,
		withdrawRequest.Nonce,
		withdrawRequest.Signature,
	)
	if err != nil {
		return types.WithdrawRequest{}, err
	}

	return withdrawRequest, nil
}

func (r *WithdrawRepository) getWithdrawRequestPostgres(
	ctx context.Context,
	withdrawID string,
) (types.WithdrawRequest, error) {
	if r.dbPool == nil {
		return types.WithdrawRequest{}, errors.New("postgres withdraw request store selected but db pool is nil")
	}

	var request types.WithdrawRequest

	err := r.dbPool.QueryRow(
		ctx,
		`
		SELECT
			withdraw_id,
			owner_address,
			denom,
			amount,
			destination_address,
			nonce,
			signature
		FROM withdraw_requests
		WHERE withdraw_id = $1
		`,
		withdrawID,
	).Scan(
		&request.WithdrawID,
		&request.Owner,
		&request.Denom,
		&request.Amount,
		&request.Destination,
		&request.Nonce,
		&request.Signature,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return types.WithdrawRequest{}, ErrWithdrawRequestNotFound
		}

		return types.WithdrawRequest{}, err
	}

	return request, nil
}

func (r *WithdrawRepository) listWithdrawRequestsPostgres(ctx context.Context) ([]types.WithdrawRequest, error) {
	if r.dbPool == nil {
		return nil, errors.New("postgres withdraw request store selected but db pool is nil")
	}

	rows, err := r.dbPool.Query(
		ctx,
		`
		SELECT
			withdraw_id,
			owner_address,
			denom,
			amount,
			destination_address,
			nonce,
			signature
		FROM withdraw_requests
		ORDER BY created_at ASC, withdraw_id ASC
		`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	requests := make([]types.WithdrawRequest, 0)

	for rows.Next() {
		var request types.WithdrawRequest

		if err := rows.Scan(
			&request.WithdrawID,
			&request.Owner,
			&request.Denom,
			&request.Amount,
			&request.Destination,
			&request.Nonce,
			&request.Signature,
		); err != nil {
			return nil, err
		}

		requests = append(requests, request)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return requests, nil
}

func localWithdrawSignature(parts ...string) string {
	h := sha256.New()

	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}

	return "0x" + hex.EncodeToString(h.Sum(nil))[:32]
}
