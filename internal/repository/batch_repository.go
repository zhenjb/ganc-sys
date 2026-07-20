package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const BatchBuildStoreMemory = "memory"
const BatchBuildStorePostgres = "postgres"

const SubmitBatchStoreMemory = "memory"
const SubmitBatchStorePostgres = "postgres"

// BatchRepository owns batch build and submit persistence.
//
// DB-03:
// - batch build outputs can be persisted in Postgres.
//
// DB-05:
// - submit batch results can be persisted in Postgres.
// - withdrawRecords created by submit batch can be persisted in Postgres.
type BatchRepository struct {
	store *store.MemoryStore

	dbPool *pgxpool.Pool

	batchBuildStore  string
	submitBatchStore string
}

func NewBatchRepository(store *store.MemoryStore) *BatchRepository {
	return &BatchRepository{
		store:            store,
		batchBuildStore:  BatchBuildStoreMemory,
		submitBatchStore: SubmitBatchStoreMemory,
	}
}

func NewBatchRepositoryWithDB(
	store *store.MemoryStore,
	dbPool *pgxpool.Pool,
	batchBuildStore string,
	submitBatchStoreValues ...string,
) *BatchRepository {
	if batchBuildStore == "" {
		batchBuildStore = BatchBuildStoreMemory
	}

	submitBatchStore := SubmitBatchStoreMemory
	if len(submitBatchStoreValues) > 0 && submitBatchStoreValues[0] != "" {
		submitBatchStore = submitBatchStoreValues[0]
	}

	return &BatchRepository{
		store:            store,
		dbPool:           dbPool,
		batchBuildStore:  batchBuildStore,
		submitBatchStore: submitBatchStore,
	}
}

func (r *BatchRepository) SaveBatchBuild(
	ctx context.Context,
	settlementUpdate types.SettlementUpdate,
	batchCommitments types.BatchCommitments,
	witness types.Witness,
) {
	r.store.SaveBatchBuild(settlementUpdate, batchCommitments)

	if r.batchBuildStore == BatchBuildStorePostgres {
		if err := r.saveBatchBuildPostgres(ctx, settlementUpdate, batchCommitments, witness); err != nil {
			panic(fmt.Sprintf("save batch build in postgres: %v", err))
		}
	}
}

func (r *BatchRepository) SaveBatchSubmitted(
	ctx context.Context,
	settlementUpdate types.SettlementUpdate,
	batchCommitments types.BatchCommitments,
	withdrawRecords []types.WithdrawRecord,
) {
	r.SaveBatchSubmitResult(
		ctx,
		settlementUpdate,
		batchCommitments,
		"",
		true,
		"accepted",
		withdrawRecords,
	)
}

func (r *BatchRepository) SaveBatchSubmitResult(
	ctx context.Context,
	settlementUpdate types.SettlementUpdate,
	batchCommitments types.BatchCommitments,
	txHash string,
	accepted bool,
	proofStatus string,
	withdrawRecords []types.WithdrawRecord,
) {
	r.store.SaveBatchSubmitted(settlementUpdate, batchCommitments, withdrawRecords)

	if r.submitBatchStore == SubmitBatchStorePostgres {
		if err := r.saveBatchSubmitResultPostgres(
			ctx,
			settlementUpdate,
			txHash,
			accepted,
			proofStatus,
			withdrawRecords,
		); err != nil {
			panic(fmt.Sprintf("save submit batch result in postgres: %v", err))
		}
	}
}

func (r *BatchRepository) saveBatchBuildPostgres(
	ctx context.Context,
	settlementUpdate types.SettlementUpdate,
	batchCommitments types.BatchCommitments,
	witness types.Witness,
) error {
	if r.dbPool == nil {
		return errors.New("postgres batch build store selected but db pool is nil")
	}

	settlementRaw, err := json.Marshal(settlementUpdate)
	if err != nil {
		return fmt.Errorf("marshal settlement update: %w", err)
	}

	commitmentsRaw, err := json.Marshal(batchCommitments)
	if err != nil {
		return fmt.Errorf("marshal batch commitments: %w", err)
	}

	witnessRaw, err := json.Marshal(witness)
	if err != nil {
		return fmt.Errorf("marshal witness: %w", err)
	}

	_, err = r.dbPool.Exec(
		ctx,
		`
		INSERT INTO batch_builds (
			batch_id,
			old_state_root,
			new_state_root,
			settlement_update,
			batch_commitments,
			witness,
			status,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'built', NOW())
		ON CONFLICT (batch_id) DO UPDATE SET
			old_state_root = EXCLUDED.old_state_root,
			new_state_root = EXCLUDED.new_state_root,
			settlement_update = EXCLUDED.settlement_update,
			batch_commitments = EXCLUDED.batch_commitments,
			witness = EXCLUDED.witness,
			status = EXCLUDED.status,
			updated_at = NOW()
		`,
		settlementUpdate.BatchID,
		settlementUpdate.OldStateRoot,
		settlementUpdate.NewStateRoot,
		settlementRaw,
		commitmentsRaw,
		witnessRaw,
	)
	if err != nil {
		return err
	}

	return nil
}

func (r *BatchRepository) saveBatchSubmitResultPostgres(
	ctx context.Context,
	settlementUpdate types.SettlementUpdate,
	txHash string,
	accepted bool,
	proofStatus string,
	withdrawRecords []types.WithdrawRecord,
) error {
	if r.dbPool == nil {
		return errors.New("postgres submit batch store selected but db pool is nil")
	}

	if settlementUpdate.BatchID == "" {
		return errors.New("batch id is required to persist submit batch result")
	}

	if proofStatus == "" {
		proofStatus = "accepted"
	}

	tx, err := r.dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(
		ctx,
		`
		INSERT INTO submit_batches (
			batch_id,
			tx_hash,
			accepted,
			proof_status,
			error_message,
			submitted_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, NULL, NOW(), NOW())
		ON CONFLICT (batch_id) DO UPDATE SET
			tx_hash = EXCLUDED.tx_hash,
			accepted = EXCLUDED.accepted,
			proof_status = EXCLUDED.proof_status,
			error_message = NULL,
			submitted_at = EXCLUDED.submitted_at,
			updated_at = NOW()
		`,
		settlementUpdate.BatchID,
		txHash,
		accepted,
		proofStatus,
	)
	if err != nil {
		return err
	}

	// Nhóm 3: advance batch_builds.status past 'built' to reflect the submit result
	// (audit view). Same tx as the submit_batches row. No-op if the build was not
	// persisted (WHERE matches nothing).
	buildStatus := "accepted"
	if !accepted {
		buildStatus = "rejected"
	}
	if _, err := tx.Exec(
		ctx,
		`UPDATE batch_builds SET status = $2, updated_at = NOW() WHERE batch_id = $1`,
		settlementUpdate.BatchID,
		buildStatus,
	); err != nil {
		return err
	}

	for _, record := range withdrawRecords {
		_, err = tx.Exec(
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
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
			ON CONFLICT (withdraw_id) DO UPDATE SET
				owner_address = EXCLUDED.owner_address,
				denom = EXCLUDED.denom,
				amount = EXCLUDED.amount,
				destination_address = EXCLUDED.destination_address,
				nullifier = EXCLUDED.nullifier,
				claimed = EXCLUDED.claimed,
				tx_hash = EXCLUDED.tx_hash,
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
	}

	return tx.Commit(ctx)
}

func (r *BatchRepository) GetLocalSettlementUpdate(ctx context.Context) types.SettlementUpdate {
	return types.SettlementUpdate{
		BatchID:      "batch-1",
		OldStateRoot: "0xrootA",
		NewStateRoot: "0xrootB",
		Deposits: []types.SettlementDeposit{
			{
				DepositID: "dep-1",
				Owner:     "cosmos1alice",
				Denom:     "uusdc",
				Amount:    "100",
			},
		},
		Withdrawals: []types.SettlementWithdrawal{
			{
				WithdrawID:      "wd-1",
				Owner:           "cosmos1alice",
				Denom:           "uusdc",
				Amount:          "40",
				Destination:     "cosmos1alice",
				DestinationHash: "0xmockdestinationhash",
				Nullifier:       "0xmocknullifier",
			},
		},
	}
}

func (r *BatchRepository) GetLocalBatchCommitments(ctx context.Context) types.BatchCommitments {
	return types.BatchCommitments{
		DepositsRoot:        "0xdepositsRoot",
		WithdrawalsRoot:     "0xwithdrawalsRoot",
		NullifiersRoot:      "0xnullifiersRoot",
		WithdrawOutputsRoot: "0xwithdrawOutputsRoot",
	}
}

func (r *BatchRepository) GetLocalWitness(ctx context.Context) types.Witness {
	return types.Witness{
		Accounts: []types.WitnessAccount{
			{
				Owner:      "cosmos1alice",
				UserSecret: "mock-user-secret",
				Nonce:      "1",
				OldBalance: "0",
				NewBalance: "60",
			},
		},
	}
}
