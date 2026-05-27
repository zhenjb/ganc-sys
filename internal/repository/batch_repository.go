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

// BatchRepository owns local and DB-backed batch read-model persistence.
//
// DB-03 status:
// - batch build outputs can be persisted in Postgres.
// - default mode remains MemoryStore for local tests.
// - submit batch state still updates MemoryStore for local flow.
type BatchRepository struct {
	store *store.MemoryStore

	dbPool          *pgxpool.Pool
	batchBuildStore string
}

func NewBatchRepository(store *store.MemoryStore) *BatchRepository {
	return &BatchRepository{
		store:           store,
		batchBuildStore: BatchBuildStoreMemory,
	}
}

func NewBatchRepositoryWithDB(
	store *store.MemoryStore,
	dbPool *pgxpool.Pool,
	batchBuildStore string,
) *BatchRepository {
	if batchBuildStore == "" {
		batchBuildStore = BatchBuildStoreMemory
	}

	return &BatchRepository{
		store:           store,
		dbPool:          dbPool,
		batchBuildStore: batchBuildStore,
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
	r.store.SaveBatchSubmitted(settlementUpdate, batchCommitments, withdrawRecords)
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
