package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrOffchainSettlementRecordNotFound = errors.New("offchain settlement record not found")

const (
	OffchainSettlementStatusPending   = "pending"
	OffchainSettlementStatusIncluded  = "included"
	OffchainSettlementStatusCommitted = "committed"
	OffchainSettlementStatusFailed    = "failed"
	OffchainSettlementStatusClaimed   = "claimed"
)

const DefaultOffchainSettlementCursorName = "default"

type OffchainStateCursor struct {
	Name                 string
	CommittedRoot        string
	PendingRoot          string
	LastCommittedBatchID string
}

type PendingDepositTransition struct {
	DepositID string

	OwnerAddress string
	Denom        string
	Amount       string

	RootBefore    string
	RootAfter     string
	BalanceBefore string
	BalanceAfter  string

	Status       string
	BatchID      string
	TxHash       string
	ErrorMessage string
}

type PendingWithdrawalTransition struct {
	WithdrawID string

	OwnerAddress       string
	Denom              string
	Amount             string
	DestinationAddress string
	Nonce              string
	Signature          string
	Nullifier          string
	DestinationHash    string

	RootBefore    string
	RootAfter     string
	BalanceBefore string
	BalanceAfter  string

	Status       string
	BatchID      string
	TxHash       string
	ErrorMessage string
}

// OffchainSettlementRepository persists the durable state needed by P3's
// off-chain settlement subsystem.
//
// It is the DB-backed companion of internal/state.OffchainStateManager.
// The manager owns in-memory pending state transitions; this repository stores
// the operation log and committed/pending root cursor needed for restart,
// batch inclusion, commit, failure, and later rollback flows.
type OffchainSettlementRepository struct {
	dbPool *pgxpool.Pool
}

func NewOffchainSettlementRepository(dbPool *pgxpool.Pool) *OffchainSettlementRepository {
	return &OffchainSettlementRepository{dbPool: dbPool}
}

func (r *OffchainSettlementRepository) GetCursor(
	ctx context.Context,
	name string,
) (OffchainStateCursor, error) {
	if r.dbPool == nil {
		return OffchainStateCursor{}, errors.New("offchain settlement repository db pool is nil")
	}

	if name == "" {
		name = DefaultOffchainSettlementCursorName
	}

	var cursor OffchainStateCursor

	err := r.dbPool.QueryRow(
		ctx,
		`
		SELECT
			name,
			committed_root,
			pending_root,
			COALESCE(last_committed_batch_id, '')
		FROM offchain_state_cursors
		WHERE name = $1
		`,
		name,
	).Scan(
		&cursor.Name,
		&cursor.CommittedRoot,
		&cursor.PendingRoot,
		&cursor.LastCommittedBatchID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OffchainStateCursor{}, ErrOffchainSettlementRecordNotFound
		}

		return OffchainStateCursor{}, err
	}

	return cursor, nil
}

func (r *OffchainSettlementRepository) UpsertCursor(
	ctx context.Context,
	cursor OffchainStateCursor,
) error {
	if r.dbPool == nil {
		return errors.New("offchain settlement repository db pool is nil")
	}

	if cursor.Name == "" {
		cursor.Name = DefaultOffchainSettlementCursorName
	}

	_, err := r.dbPool.Exec(
		ctx,
		`
		INSERT INTO offchain_state_cursors (
			name,
			committed_root,
			pending_root,
			last_committed_batch_id,
			updated_at
		)
		VALUES ($1, $2, $3, NULLIF($4, ''), NOW())
		ON CONFLICT (name) DO UPDATE SET
			committed_root = EXCLUDED.committed_root,
			pending_root = EXCLUDED.pending_root,
			last_committed_batch_id = EXCLUDED.last_committed_batch_id,
			updated_at = NOW()
		`,
		cursor.Name,
		cursor.CommittedRoot,
		cursor.PendingRoot,
		cursor.LastCommittedBatchID,
	)
	if err != nil {
		return fmt.Errorf("upsert offchain state cursor: %w", err)
	}

	return nil
}

func (r *OffchainSettlementRepository) SavePendingDeposit(
	ctx context.Context,
	transition PendingDepositTransition,
) error {
	if r.dbPool == nil {
		return errors.New("offchain settlement repository db pool is nil")
	}

	status := transition.Status
	if status == "" {
		status = OffchainSettlementStatusPending
	}

	_, err := r.dbPool.Exec(
		ctx,
		`
		INSERT INTO offchain_pending_deposits (
			deposit_id,
			owner_address,
			denom,
			amount,
			root_before,
			root_after,
			balance_before,
			balance_after,
			status,
			batch_id,
			tx_hash,
			error_message,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''), NOW())
		ON CONFLICT (deposit_id) DO UPDATE SET
			owner_address = EXCLUDED.owner_address,
			denom = EXCLUDED.denom,
			amount = EXCLUDED.amount,
			root_before = EXCLUDED.root_before,
			root_after = EXCLUDED.root_after,
			balance_before = EXCLUDED.balance_before,
			balance_after = EXCLUDED.balance_after,
			status = EXCLUDED.status,
			batch_id = EXCLUDED.batch_id,
			tx_hash = EXCLUDED.tx_hash,
			error_message = EXCLUDED.error_message,
			updated_at = NOW()
		`,
		transition.DepositID,
		transition.OwnerAddress,
		transition.Denom,
		transition.Amount,
		transition.RootBefore,
		transition.RootAfter,
		transition.BalanceBefore,
		transition.BalanceAfter,
		status,
		transition.BatchID,
		transition.TxHash,
		transition.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("save pending deposit transition: %w", err)
	}

	return nil
}

func (r *OffchainSettlementRepository) SavePendingWithdrawal(
	ctx context.Context,
	transition PendingWithdrawalTransition,
) error {
	if r.dbPool == nil {
		return errors.New("offchain settlement repository db pool is nil")
	}

	status := transition.Status
	if status == "" {
		status = OffchainSettlementStatusPending
	}

	_, err := r.dbPool.Exec(
		ctx,
		`
		INSERT INTO offchain_pending_withdrawals (
			withdraw_id,
			owner_address,
			denom,
			amount,
			destination_address,
			nonce,
			signature,
			nullifier,
			destination_hash,
			root_before,
			root_after,
			balance_before,
			balance_after,
			status,
			batch_id,
			tx_hash,
			error_message,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NULLIF($15, ''), NULLIF($16, ''), NULLIF($17, ''), NOW())
		ON CONFLICT (withdraw_id) DO UPDATE SET
			owner_address = EXCLUDED.owner_address,
			denom = EXCLUDED.denom,
			amount = EXCLUDED.amount,
			destination_address = EXCLUDED.destination_address,
			nonce = EXCLUDED.nonce,
			signature = EXCLUDED.signature,
			nullifier = EXCLUDED.nullifier,
			destination_hash = EXCLUDED.destination_hash,
			root_before = EXCLUDED.root_before,
			root_after = EXCLUDED.root_after,
			balance_before = EXCLUDED.balance_before,
			balance_after = EXCLUDED.balance_after,
			status = EXCLUDED.status,
			batch_id = EXCLUDED.batch_id,
			tx_hash = EXCLUDED.tx_hash,
			error_message = EXCLUDED.error_message,
			updated_at = NOW()
		`,
		transition.WithdrawID,
		transition.OwnerAddress,
		transition.Denom,
		transition.Amount,
		transition.DestinationAddress,
		transition.Nonce,
		transition.Signature,
		transition.Nullifier,
		transition.DestinationHash,
		transition.RootBefore,
		transition.RootAfter,
		transition.BalanceBefore,
		transition.BalanceAfter,
		status,
		transition.BatchID,
		transition.TxHash,
		transition.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("save pending withdrawal transition: %w", err)
	}

	return nil
}

func (r *OffchainSettlementRepository) ListPendingDeposits(
	ctx context.Context,
) ([]PendingDepositTransition, error) {
	if r.dbPool == nil {
		return nil, errors.New("offchain settlement repository db pool is nil")
	}

	rows, err := r.dbPool.Query(
		ctx,
		`
		SELECT
			deposit_id,
			owner_address,
			denom,
			amount,
			root_before,
			root_after,
			balance_before,
			balance_after,
			status,
			COALESCE(batch_id, ''),
			COALESCE(tx_hash, ''),
			COALESCE(error_message, '')
		FROM offchain_pending_deposits
		WHERE status = 'pending'
		ORDER BY created_at ASC, deposit_id ASC
		`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	transitions := make([]PendingDepositTransition, 0)

	for rows.Next() {
		var transition PendingDepositTransition

		if err := rows.Scan(
			&transition.DepositID,
			&transition.OwnerAddress,
			&transition.Denom,
			&transition.Amount,
			&transition.RootBefore,
			&transition.RootAfter,
			&transition.BalanceBefore,
			&transition.BalanceAfter,
			&transition.Status,
			&transition.BatchID,
			&transition.TxHash,
			&transition.ErrorMessage,
		); err != nil {
			return nil, err
		}

		transitions = append(transitions, transition)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return transitions, nil
}

func (r *OffchainSettlementRepository) ListPendingWithdrawals(
	ctx context.Context,
) ([]PendingWithdrawalTransition, error) {
	if r.dbPool == nil {
		return nil, errors.New("offchain settlement repository db pool is nil")
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
			signature,
			nullifier,
			destination_hash,
			root_before,
			root_after,
			balance_before,
			balance_after,
			status,
			COALESCE(batch_id, ''),
			COALESCE(tx_hash, ''),
			COALESCE(error_message, '')
		FROM offchain_pending_withdrawals
		WHERE status = 'pending'
		ORDER BY created_at ASC, withdraw_id ASC
		`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	transitions := make([]PendingWithdrawalTransition, 0)

	for rows.Next() {
		var transition PendingWithdrawalTransition

		if err := rows.Scan(
			&transition.WithdrawID,
			&transition.OwnerAddress,
			&transition.Denom,
			&transition.Amount,
			&transition.DestinationAddress,
			&transition.Nonce,
			&transition.Signature,
			&transition.Nullifier,
			&transition.DestinationHash,
			&transition.RootBefore,
			&transition.RootAfter,
			&transition.BalanceBefore,
			&transition.BalanceAfter,
			&transition.Status,
			&transition.BatchID,
			&transition.TxHash,
			&transition.ErrorMessage,
		); err != nil {
			return nil, err
		}

		transitions = append(transitions, transition)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return transitions, nil
}

func (r *OffchainSettlementRepository) MarkIncluded(
	ctx context.Context,
	batchID string,
	depositIDs []string,
	withdrawIDs []string,
) error {
	if r.dbPool == nil {
		return errors.New("offchain settlement repository db pool is nil")
	}

	tx, err := r.dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, depositID := range depositIDs {
		tag, err := tx.Exec(
			ctx,
			`
			UPDATE offchain_pending_deposits
			SET status = 'included',
				batch_id = $2,
				updated_at = NOW()
			WHERE deposit_id = $1
			`,
			depositID,
			batchID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: deposit %s", ErrOffchainSettlementRecordNotFound, depositID)
		}
	}

	for _, withdrawID := range withdrawIDs {
		tag, err := tx.Exec(
			ctx,
			`
			UPDATE offchain_pending_withdrawals
			SET status = 'included',
				batch_id = $2,
				updated_at = NOW()
			WHERE withdraw_id = $1
			`,
			withdrawID,
			batchID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: withdrawal %s", ErrOffchainSettlementRecordNotFound, withdrawID)
		}

		// Nhóm 3: mirror the settlement lifecycle onto withdraw_requests.status (audit
		// view) so it advances past 'requested'. Best-effort — no matching row (memory
		// request store) is a harmless no-op; a SQL error rolls back with the pending.
		if _, err := tx.Exec(
			ctx,
			`UPDATE withdraw_requests SET status = 'included', updated_at = NOW() WHERE withdraw_id = $1`,
			withdrawID,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (r *OffchainSettlementRepository) MarkCommitted(
	ctx context.Context,
	batchID string,
	txHash string,
) error {
	return r.markByBatch(ctx, batchID, OffchainSettlementStatusCommitted, txHash, "")
}

func (r *OffchainSettlementRepository) MarkFailed(
	ctx context.Context,
	batchID string,
	errorMessage string,
) error {
	return r.markByBatch(ctx, batchID, OffchainSettlementStatusFailed, "", errorMessage)
}

func (r *OffchainSettlementRepository) markByBatch(
	ctx context.Context,
	batchID string,
	status string,
	txHash string,
	errorMessage string,
) error {
	if r.dbPool == nil {
		return errors.New("offchain settlement repository db pool is nil")
	}

	tx, err := r.dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(
		ctx,
		`
		UPDATE offchain_pending_deposits
		SET status = $2,
			tx_hash = NULLIF($3, ''),
			error_message = NULLIF($4, ''),
			updated_at = NOW()
		WHERE batch_id = $1
		`,
		batchID,
		status,
		txHash,
		errorMessage,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(
		ctx,
		`
		UPDATE offchain_pending_withdrawals
		SET status = $2,
			tx_hash = NULLIF($3, ''),
			error_message = NULLIF($4, ''),
			updated_at = NOW()
		WHERE batch_id = $1
		`,
		batchID,
		status,
		txHash,
		errorMessage,
	)
	if err != nil {
		return err
	}

	// Nhóm 3: mirror the batch's committed/failed status onto withdraw_requests
	// (audit view) for every withdrawal in the batch. Same batch_id the pending rows
	// just took, so the subquery finds them within this tx.
	if _, err := tx.Exec(
		ctx,
		`UPDATE withdraw_requests SET status = $2, updated_at = NOW()
		 WHERE withdraw_id IN (SELECT withdraw_id FROM offchain_pending_withdrawals WHERE batch_id = $1)`,
		batchID,
		status,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *OffchainSettlementRepository) ReopenIncluded(
	ctx context.Context,
	batchID string,
	reason string,
) error {
	if r.dbPool == nil {
		return errors.New("offchain settlement repository db pool is nil")
	}

	tx, err := r.dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	depositTag, err := tx.Exec(
		ctx,
		`
		UPDATE offchain_pending_deposits
		SET status = 'pending',
			batch_id = NULL,
			tx_hash = NULL,
			error_message = NULLIF($2, ''),
			updated_at = NOW()
		WHERE batch_id = $1
		  AND status = 'included'
		`,
		batchID,
		reason,
	)
	if err != nil {
		return err
	}

	// Nhóm 3: reset the mirrored withdraw_requests.status back to 'requested' for the
	// reopened withdrawals. MUST run before the pending update below nulls batch_id,
	// so the subquery can still match them by batch_id.
	if _, err := tx.Exec(
		ctx,
		`UPDATE withdraw_requests SET status = 'requested', updated_at = NOW()
		 WHERE withdraw_id IN (SELECT withdraw_id FROM offchain_pending_withdrawals WHERE batch_id = $1 AND status = 'included')`,
		batchID,
	); err != nil {
		return err
	}

	withdrawTag, err := tx.Exec(
		ctx,
		`
		UPDATE offchain_pending_withdrawals
		SET status = 'pending',
			batch_id = NULL,
			tx_hash = NULL,
			error_message = NULLIF($2, ''),
			updated_at = NOW()
		WHERE batch_id = $1
		  AND status = 'included'
		`,
		batchID,
		reason,
	)
	if err != nil {
		return err
	}

	if depositTag.RowsAffected()+withdrawTag.RowsAffected() == 0 {
		return fmt.Errorf("%w: included batch %s", ErrOffchainSettlementRecordNotFound, batchID)
	}

	return tx.Commit(ctx)
}
