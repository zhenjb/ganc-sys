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

const WithdrawRecordStoreMemory = "memory"
const WithdrawRecordStorePostgres = "postgres"

// WithdrawRepository owns withdrawal request and withdrawal record access.
//
// DB-02:
// - withdraw_requests can be persisted in Postgres.
//
// DB-06:
// - indexed_withdraw_records can be read from Postgres.
// - claim updates indexed_withdraw_records.claimed=true in Postgres.
//
// Default mode remains MemoryStore for local tests.
type WithdrawRepository struct {
	store *store.MemoryStore

	dbPool *pgxpool.Pool

	requestStore string
	recordStore  string
}

func NewWithdrawRepository(store *store.MemoryStore) *WithdrawRepository {
	return &WithdrawRepository{
		store:        store,
		requestStore: WithdrawRequestStoreMemory,
		recordStore:  WithdrawRecordStoreMemory,
	}
}

func NewWithdrawRepositoryWithDB(
	store *store.MemoryStore,
	dbPool *pgxpool.Pool,
	requestStore string,
	recordStoreValues ...string,
) *WithdrawRepository {
	if requestStore == "" {
		requestStore = WithdrawRequestStoreMemory
	}

	recordStore := WithdrawRecordStoreMemory
	if len(recordStoreValues) > 0 && recordStoreValues[0] != "" {
		recordStore = recordStoreValues[0]
	}

	return &WithdrawRepository{
		store:        store,
		dbPool:       dbPool,
		requestStore: requestStore,
		recordStore:  recordStore,
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
		r.SaveWithdrawRecord(ctx, record)
	}
}

func (r *WithdrawRepository) SaveWithdrawRecord(ctx context.Context, record types.WithdrawRecord) {
	r.store.SaveWithdrawRecord(record)

	if r.recordStore == WithdrawRecordStorePostgres {
		if err := r.saveWithdrawRecordPostgres(ctx, record, ""); err != nil {
			panic(fmt.Sprintf("save withdraw record in postgres: %v", err))
		}
	}
}

func (r *WithdrawRepository) GetWithdrawRecord(ctx context.Context, withdrawID string) (types.WithdrawRecord, error) {
	if r.recordStore == WithdrawRecordStorePostgres {
		record, err := r.getWithdrawRecordPostgres(ctx, withdrawID)
		if err == nil {
			return record, nil
		}

		if !errors.Is(err, ErrWithdrawRecordNotFound) {
			return types.WithdrawRecord{}, err
		}

		// Transitional fallback only. DB must never override chain/DB truth,
		// but this keeps local flow compatible while migration is incremental.
	}

	record, ok := r.store.GetWithdrawRecord(withdrawID)
	if !ok {
		return types.WithdrawRecord{}, ErrWithdrawRecordNotFound
	}

	return record, nil
}

func (r *WithdrawRepository) ClaimWithdrawRecord(ctx context.Context, withdrawID string) (types.WithdrawRecord, error) {
	if r.recordStore == WithdrawRecordStorePostgres {
		record, err := r.claimWithdrawRecordPostgres(ctx, withdrawID)
		if err == nil {
			// Keep in-memory dashboard snapshot in sync when the record exists
			// in the current process. Ignore if this backend restarted and only
			// DB has the record.
			if _, ok := r.store.GetWithdrawRecord(withdrawID); ok {
				_, _ = r.store.ClaimWithdrawRecord(withdrawID)
			}

			return record, nil
		}

		if !errors.Is(err, ErrWithdrawRecordNotFound) {
			return types.WithdrawRecord{}, err
		}

		// Transitional fallback only.
	}

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
	if r.recordStore == WithdrawRecordStorePostgres {
		records, err := r.listWithdrawRecordsPostgres(ctx)
		if err != nil {
			panic(fmt.Sprintf("list withdraw records from postgres: %v", err))
		}

		return records
	}

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

func (r *WithdrawRepository) saveWithdrawRecordPostgres(
	ctx context.Context,
	record types.WithdrawRecord,
	txHash string,
) error {
	if r.dbPool == nil {
		return errors.New("postgres withdraw record store selected but db pool is nil")
	}

	_, err := r.dbPool.Exec(
		ctx,
		`
		INSERT INTO indexed_withdraw_records (
			withdraw_id,
			owner_address,
			denom,
			amount,
			destination_address,
			nullifier,
			claimed,
			tx_hash,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NOW())
		ON CONFLICT (withdraw_id) DO UPDATE SET
			owner_address = EXCLUDED.owner_address,
			denom = EXCLUDED.denom,
			amount = EXCLUDED.amount,
			destination_address = EXCLUDED.destination_address,
			nullifier = EXCLUDED.nullifier,
			claimed = EXCLUDED.claimed,
			tx_hash = COALESCE(EXCLUDED.tx_hash, indexed_withdraw_records.tx_hash),
			updated_at = NOW()
		`,
		record.WithdrawID,
		record.Owner,
		record.Denom,
		record.Amount,
		record.Destination,
		record.Nullifier,
		record.Claimed,
		txHash,
	)
	if err != nil {
		return err
	}

	return nil
}

func (r *WithdrawRepository) getWithdrawRecordPostgres(
	ctx context.Context,
	withdrawID string,
) (types.WithdrawRecord, error) {
	if r.dbPool == nil {
		return types.WithdrawRecord{}, errors.New("postgres withdraw record store selected but db pool is nil")
	}

	var record types.WithdrawRecord

	err := r.dbPool.QueryRow(
		ctx,
		`
		SELECT
			withdraw_id,
			owner_address,
			denom,
			amount,
			destination_address,
			nullifier,
			claimed
		FROM indexed_withdraw_records
		WHERE withdraw_id = $1
		`,
		withdrawID,
	).Scan(
		&record.WithdrawID,
		&record.Owner,
		&record.Denom,
		&record.Amount,
		&record.Destination,
		&record.Nullifier,
		&record.Claimed,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return types.WithdrawRecord{}, ErrWithdrawRecordNotFound
		}

		return types.WithdrawRecord{}, err
	}

	return record, nil
}

func (r *WithdrawRepository) claimWithdrawRecordPostgres(
	ctx context.Context,
	withdrawID string,
) (types.WithdrawRecord, error) {
	if r.dbPool == nil {
		return types.WithdrawRecord{}, errors.New("postgres withdraw record store selected but db pool is nil")
	}

	record, err := r.getWithdrawRecordPostgres(ctx, withdrawID)
	if err != nil {
		return types.WithdrawRecord{}, err
	}

	if record.Claimed {
		return types.WithdrawRecord{}, ErrWithdrawAlreadyClaimed
	}

	_, err = r.dbPool.Exec(
		ctx,
		`
		UPDATE indexed_withdraw_records
		SET claimed = TRUE,
			updated_at = NOW()
		WHERE withdraw_id = $1
		`,
		withdrawID,
	)
	if err != nil {
		return types.WithdrawRecord{}, err
	}

	record.Claimed = true
	return record, nil
}

func (r *WithdrawRepository) listWithdrawRecordsPostgres(ctx context.Context) ([]types.WithdrawRecord, error) {
	if r.dbPool == nil {
		return nil, errors.New("postgres withdraw record store selected but db pool is nil")
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
			nullifier,
			claimed
		FROM indexed_withdraw_records
		ORDER BY created_at ASC, withdraw_id ASC
		`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]types.WithdrawRecord, 0)

	for rows.Next() {
		var record types.WithdrawRecord

		if err := rows.Scan(
			&record.WithdrawID,
			&record.Owner,
			&record.Denom,
			&record.Amount,
			&record.Destination,
			&record.Nullifier,
			&record.Claimed,
		); err != nil {
			return nil, err
		}

		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return records, nil
}

func localWithdrawSignature(parts ...string) string {
	h := sha256.New()

	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}

	return "0x" + hex.EncodeToString(h.Sum(nil))[:32]
}
