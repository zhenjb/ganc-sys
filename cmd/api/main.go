package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

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
	"github.com/rs/cors"
)

const BatchBuilderModeLocal = "local"
const BatchBuilderModeSnapshot = "snapshot"

func main() {
	port := getenv("PORT", "8080")

	memoryStore := store.NewMemoryStore()
	offchainStateManager := appstate.NewOffchainStateManager()

	chainQueryMode := getenv("CHAIN_QUERY_MODE", "local")
	chainRESTURL := getenv("CHAIN_REST_URL", "http://localhost:1317")
	chainQueryClient := chain.NewRestQueryClient(chainRESTURL)

	withdrawRequestStore := getenv("WITHDRAW_REQUEST_STORE", repository.WithdrawRequestStoreMemory)
	withdrawRecordStore := getenv("WITHDRAW_RECORD_STORE", repository.WithdrawRecordStoreMemory)
	batchBuildStore := getenv("BATCH_BUILD_STORE", repository.BatchBuildStoreMemory)
	proofBundleStore := getenv("PROOF_BUNDLE_STORE", repository.ProofBundleStoreMemory)
	submitBatchStore := getenv("SUBMIT_BATCH_STORE", repository.SubmitBatchStoreMemory)

	batchBuilderMode := getenv("BATCH_BUILDER_MODE", BatchBuilderModeLocal)
	batchBuildSource := getenv("BATCH_BUILD_SOURCE", service.BatchBuildSourceManual)
	offchainSettlementEnabled := getenv("OFFCHAIN_SETTLEMENT_ENABLED", "false") == "true"

	proverMode := getenv("PROVER_MODE", "local")
	proverURL := getenv("PROVER_URL", prover.DefaultRemoteProverURL)

	dbPool := openDatabaseIfNeeded(
		withdrawRequestStore,
		withdrawRecordStore,
		batchBuildStore,
		proofBundleStore,
		submitBatchStore,
		batchBuildSource,
		offchainSettlementEnabled,
	)
	if dbPool != nil {
		defer dbPool.Close()
	}

	var offchainSettlementService *service.OffchainSettlementService
	if dbPool != nil {
		offchainSettlementRepository := repository.NewOffchainSettlementRepository(dbPool)
		offchainSettlementService = service.NewOffchainSettlementService(
			offchainStateManager,
			offchainSettlementRepository,
		)
	}

	// Rebuild the in-memory off-chain state from the durable pending tables so a
	// restart does not regenerate already-consumed nonces/nullifiers and collide
	// with persisted unique constraints. Must run before serving any request.
	if offchainSettlementEnabled && offchainSettlementService != nil {
		if err := offchainSettlementService.RehydrateFromStore(context.Background()); err != nil {
			log.Printf("offchain settlement rehydrate failed: %v", err)
		}
	}

	var offchainSettlementHandler *handler.OffchainSettlementHandler
	if offchainSettlementService != nil {
		offchainSettlementHandler = handler.NewOffchainSettlementHandler(offchainSettlementService)
	}

	healthRepository := repository.NewHealthRepository()
	healthService := service.NewHealthService(healthRepository)
	healthHandler := handler.NewHealthHandler(healthService)

	stateRepository := repository.NewStateRepositoryWithChainQuery(
		memoryStore,
		chainQueryClient,
		chainQueryMode,
	)
	stateService := service.NewStateService(stateRepository)
	stateHandler := handler.NewStateHandler(stateService)

	chainClient := chain.NewLocalClient()
	relayerClient := relayer.NewLocalClient()

	depositRepository := repository.NewDepositRepositoryWithChainQuery(
		memoryStore,
		chainQueryClient,
		chainQueryMode,
	)

	var depositIndexer *indexer.DepositIndexer
	if offchainSettlementEnabled {
		depositIndexer = indexer.NewDepositIndexerWithOffchainSettlement(
			depositRepository,
			offchainSettlementService,
		)
	} else {
		depositIndexer = indexer.NewDepositIndexer(depositRepository)
	}

	depositService := service.NewDepositService(depositRepository, depositIndexer, chainClient)
	depositHandler := handler.NewDepositHandler(depositService)

	withdrawRepository := repository.NewWithdrawRepositoryWithDB(
		memoryStore,
		dbPool,
		withdrawRequestStore,
		withdrawRecordStore,
	)
	withdrawService := service.NewWithdrawServiceWithOffchainSettlement(
		withdrawRepository,
		relayerClient,
		offchainSettlementEnabled,
		offchainSettlementService,
	)
	withdrawHandler := handler.NewWithdrawHandler(withdrawService)

	batchRepository := repository.NewBatchRepositoryWithDB(
		memoryStore,
		dbPool,
		batchBuildStore,
		submitBatchStore,
	)
	batchBuilder := newBatchBuilder(batchBuilderMode, offchainStateManager)
	batchService := service.NewBatchServiceWithOffchainSettlement(
		batchRepository,
		depositRepository,
		withdrawRepository,
		batchBuilder,
		relayerClient,
		batchBuildSource,
		offchainSettlementService,
	)
	batchHandler := handler.NewBatchHandler(batchService)

	proverClient := newProverClient(proverMode, proverURL)
	proofRepository := repository.NewProofRepositoryWithDB(
		memoryStore,
		dbPool,
		proofBundleStore,
	)
	proofService := service.NewProofService(proverClient, proofRepository)
	proofHandler := handler.NewProofHandler(proofService)

	// Enable real ZK verification on batch submit when the prover client can
	// also verify (remote gazk) and it has not been explicitly disabled. This
	// makes /api/batch/submit reject batches whose Groth16 proof is invalid,
	// instead of relying on the mock relayer that accepts everything.
	proofVerifyEnabled := getenv("PROOF_VERIFY_ENABLED", "true") == "true"
	if verifier, ok := proverClient.(prover.Verifier); ok && proofVerifyEnabled {
		batchService.SetProofVerifier(verifier)
		log.Printf("batch submit real ZK verification enabled via %s prover", proverMode)
	} else {
		log.Printf("batch submit real ZK verification disabled (proverMode=%s, enabled=%v)", proverMode, proofVerifyEnabled)
	}

	chainQueryService := service.NewChainQueryService(chainQueryClient)
	chainQueryHandler := handler.NewChainQueryHandler(chainQueryService)

	router := api.NewRouter(api.RouterDeps{
		HealthHandler:             healthHandler,
		StateHandler:              stateHandler,
		DepositHandler:            depositHandler,
		WithdrawHandler:           withdrawHandler,
		BatchHandler:              batchHandler,
		ProofHandler:              proofHandler,
		ChainQueryHandler:         chainQueryHandler,
		OffchainSettlementHandler: offchainSettlementHandler,
	})

	addr := ":" + port
	log.Printf("ganc-sys backend API listening on http://localhost%s", addr)
	log.Printf("chain query mode=%s rest=%s", chainQueryMode, chainRESTURL)
	log.Printf("withdraw request store=%s", withdrawRequestStore)
	log.Printf("withdraw record store=%s", withdrawRecordStore)
	log.Printf("batch build store=%s", batchBuildStore)
	log.Printf("proof bundle store=%s", proofBundleStore)
	log.Printf("submit batch store=%s", submitBatchStore)
	log.Printf("batch builder mode=%s", batchBuilderMode)
	log.Printf("batch build source=%s", batchBuildSource)
	log.Printf("offchain settlement enabled=%v", offchainSettlementEnabled)
	log.Printf("prover mode=%s", proverMode)
	if proverMode == "remote" {
		log.Printf("prover url=%s", proverURL)
	}

	// CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000"}, // Đổi thành port chạy Frontend của bạn nếu khác
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	})

	// 2. Bọc router.Routes() bằng middleware cors
	handlerWithCORS := c.Handler(router.Routes())

	// 3. Truyền handler đã có CORS vào đây
	if err := http.ListenAndServe(addr, handlerWithCORS); err != nil {
		log.Fatal(err)
	}
}

func newBatchBuilder(
	mode string,
	offchainStateManager *appstate.OffchainStateManager,
) batch.Builder {
	switch mode {
	case BatchBuilderModeSnapshot:
		return batch.NewSnapshotBuilder(offchainStateManager)
	case BatchBuilderModeLocal:
		return batch.NewLocalBuilder()
	default:
		log.Printf("unknown BATCH_BUILDER_MODE=%q, falling back to local", mode)
		return batch.NewLocalBuilder()
	}
}

func newProverClient(mode string, remoteURL string) prover.Client {
	switch mode {
	case "remote":
		return prover.NewRemoteClient(remoteURL)
	case "local":
		return prover.NewLocalClient()
	default:
		log.Printf("unknown PROVER_MODE=%q, falling back to local", mode)
		return prover.NewLocalClient()
	}
}

func openDatabaseIfNeeded(
	withdrawRequestStore string,
	withdrawRecordStore string,
	batchBuildStore string,
	proofBundleStore string,
	submitBatchStore string,
	batchBuildSource string,
	offchainSettlementEnabled bool,
) *pgxpool.Pool {
	needsDB :=
		withdrawRequestStore == repository.WithdrawRequestStorePostgres ||
			withdrawRecordStore == repository.WithdrawRecordStorePostgres ||
			batchBuildStore == repository.BatchBuildStorePostgres ||
			proofBundleStore == repository.ProofBundleStorePostgres ||
			submitBatchStore == repository.SubmitBatchStorePostgres ||
			batchBuildSource == service.BatchBuildSourcePending ||
			offchainSettlementEnabled

	if !needsDB {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	return pool
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
