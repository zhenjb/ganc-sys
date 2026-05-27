package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhenjb/ganc-sys/internal/api"
	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/chain"
	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/handler"
	"github.com/zhenjb/ganc-sys/internal/indexer"
	"github.com/zhenjb/ganc-sys/internal/prover"
	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/internal/store"
)

func newTestServer() http.Handler {
	memoryStore := store.NewMemoryStore()

	healthRepository := repository.NewHealthRepository()
	healthService := service.NewHealthService(healthRepository)
	healthHandler := handler.NewHealthHandler(healthService)

	stateRepository := repository.NewStateRepository(memoryStore)
	stateService := service.NewStateService(stateRepository)
	stateHandler := handler.NewStateHandler(stateService)

	chainClient := chain.NewLocalClient()
	relayerClient := relayer.NewLocalClient()

	depositRepository := repository.NewDepositRepository(memoryStore)
	depositIndexer := indexer.NewDepositIndexer(depositRepository)
	depositService := service.NewDepositService(depositRepository, depositIndexer, chainClient)
	depositHandler := handler.NewDepositHandler(depositService)

	withdrawRepository := repository.NewWithdrawRepository(memoryStore)
	withdrawService := service.NewWithdrawService(withdrawRepository, relayerClient)
	withdrawHandler := handler.NewWithdrawHandler(withdrawService)

	batchRepository := repository.NewBatchRepository(memoryStore)
	batchBuilder := batch.NewLocalBuilder()
	batchService := service.NewBatchService(
		batchRepository,
		depositRepository,
		withdrawRepository,
		batchBuilder,
		relayerClient,
	)
	batchHandler := handler.NewBatchHandler(batchService)

	proverClient := prover.NewLocalClient()
	proofRepository := repository.NewProofRepository(memoryStore)
	proofService := service.NewProofService(proverClient, proofRepository)
	proofHandler := handler.NewProofHandler(proofService)

	router := api.NewRouter(api.RouterDeps{
		HealthHandler:   healthHandler,
		StateHandler:    stateHandler,
		DepositHandler:  depositHandler,
		WithdrawHandler: withdrawHandler,
		BatchHandler:    batchHandler,
		ProofHandler:    proofHandler,
	})

	return router.Routes()
}

func newTestServerWithPendingOffchainSettlement(t *testing.T) http.Handler {
	t.Helper()

	ctx := context.Background()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(pool.Close)

	cleanOffchainSettlementTables(t, ctx, pool)

	if _, err := pool.Exec(ctx, "DELETE FROM withdraw_requests"); err != nil {
		t.Fatalf("clean withdraw_requests: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM batch_builds"); err != nil {
		t.Fatalf("clean batch_builds: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM proof_bundles"); err != nil {
		t.Fatalf("clean proof_bundles: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM submit_batches"); err != nil {
		t.Fatalf("clean submit_batches: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM indexed_withdraw_records"); err != nil {
		t.Fatalf("clean indexed_withdraw_records: %v", err)
	}
	if _, err := pool.Exec(ctx, "ALTER SEQUENCE withdraw_request_seq RESTART WITH 1"); err != nil {
		t.Fatalf("reset withdraw_request_seq: %v", err)
	}

	memoryStore := store.NewMemoryStore()
	offchainStateManager := appstate.NewOffchainStateManager()

	offchainSettlementRepository := repository.NewOffchainSettlementRepository(pool)
	offchainSettlementService := service.NewOffchainSettlementService(
		offchainStateManager,
		offchainSettlementRepository,
	)
	offchainSettlementHandler := handler.NewOffchainSettlementHandler(offchainSettlementService)

	healthRepository := repository.NewHealthRepository()
	healthService := service.NewHealthService(healthRepository)
	healthHandler := handler.NewHealthHandler(healthService)

	stateRepository := repository.NewStateRepository(memoryStore)
	stateService := service.NewStateService(stateRepository)
	stateHandler := handler.NewStateHandler(stateService)

	chainClient := chain.NewLocalClient()
	relayerClient := relayer.NewLocalClient()

	depositRepository := repository.NewDepositRepository(memoryStore)
	depositIndexer := indexer.NewDepositIndexerWithOffchainSettlement(
		depositRepository,
		offchainSettlementService,
	)
	depositService := service.NewDepositService(depositRepository, depositIndexer, chainClient)
	depositHandler := handler.NewDepositHandler(depositService)

	withdrawRepository := repository.NewWithdrawRepositoryWithDB(
		memoryStore,
		pool,
		repository.WithdrawRequestStorePostgres,
		repository.WithdrawRecordStoreMemory,
	)
	withdrawService := service.NewWithdrawServiceWithOffchainSettlement(
		withdrawRepository,
		relayerClient,
		true,
		offchainSettlementService,
	)
	withdrawHandler := handler.NewWithdrawHandler(withdrawService)

	batchRepository := repository.NewBatchRepositoryWithDB(
		memoryStore,
		pool,
		repository.BatchBuildStorePostgres,
		repository.SubmitBatchStoreMemory,
	)
	batchBuilder := batch.NewLocalBuilder()
	batchService := service.NewBatchServiceWithOffchainSettlement(
		batchRepository,
		depositRepository,
		withdrawRepository,
		batchBuilder,
		relayerClient,
		service.BatchBuildSourcePending,
		offchainSettlementService,
	)
	batchHandler := handler.NewBatchHandler(batchService)

	proverClient := prover.NewLocalClient()
	proofRepository := repository.NewProofRepository(memoryStore)
	proofService := service.NewProofService(proverClient, proofRepository)
	proofHandler := handler.NewProofHandler(proofService)

	router := api.NewRouter(api.RouterDeps{
		HealthHandler:             healthHandler,
		StateHandler:              stateHandler,
		DepositHandler:            depositHandler,
		WithdrawHandler:           withdrawHandler,
		BatchHandler:              batchHandler,
		ProofHandler:              proofHandler,
		OffchainSettlementHandler: offchainSettlementHandler,
	})

	return router.Routes()
}

func performRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()

	var reqBody *bytes.Reader

	if body == nil {
		reqBody = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}

		reqBody = bytes.NewReader(raw)
	}

	req := httptest.NewRequest(method, path, reqBody)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var out T
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode response JSON: %v\nbody=%s", err, rec.Body.String())
	}

	return out
}

func cleanOffchainSettlementTables(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()

	if _, err := pool.Exec(ctx, "DELETE FROM offchain_pending_withdrawals"); err != nil {
		t.Fatalf("clean offchain_pending_withdrawals: %v", err)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM offchain_pending_deposits"); err != nil {
		t.Fatalf("clean offchain_pending_deposits: %v", err)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM offchain_state_cursors"); err != nil {
		t.Fatalf("clean offchain_state_cursors: %v", err)
	}
}
