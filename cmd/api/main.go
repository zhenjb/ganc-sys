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
	"github.com/zhenjb/ganc-sys/internal/store"
)

func main() {
	port := getenv("PORT", "8080")

	memoryStore := store.NewMemoryStore()

	chainQueryMode := getenv("CHAIN_QUERY_MODE", "local")
	chainRESTURL := getenv("CHAIN_REST_URL", "http://localhost:1317")
	chainQueryClient := chain.NewRestQueryClient(chainRESTURL)

	withdrawRequestStore := getenv("WITHDRAW_REQUEST_STORE", repository.WithdrawRequestStoreMemory)
	batchBuildStore := getenv("BATCH_BUILD_STORE", repository.BatchBuildStoreMemory)
	proofBundleStore := getenv("PROOF_BUNDLE_STORE", repository.ProofBundleStoreMemory)

	dbPool := openDatabaseIfNeeded(withdrawRequestStore, batchBuildStore, proofBundleStore)
	if dbPool != nil {
		defer dbPool.Close()
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
	depositIndexer := indexer.NewDepositIndexer(depositRepository)
	depositService := service.NewDepositService(depositRepository, depositIndexer, chainClient)
	depositHandler := handler.NewDepositHandler(depositService)

	withdrawRepository := repository.NewWithdrawRepositoryWithDB(
		memoryStore,
		dbPool,
		withdrawRequestStore,
	)
	withdrawService := service.NewWithdrawService(withdrawRepository, relayerClient)
	withdrawHandler := handler.NewWithdrawHandler(withdrawService)

	batchRepository := repository.NewBatchRepositoryWithDB(
		memoryStore,
		dbPool,
		batchBuildStore,
	)
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
	proofRepository := repository.NewProofRepositoryWithDB(
		memoryStore,
		dbPool,
		proofBundleStore,
	)
	proofService := service.NewProofService(proverClient, proofRepository)
	proofHandler := handler.NewProofHandler(proofService)

	chainQueryService := service.NewChainQueryService(chainQueryClient)
	chainQueryHandler := handler.NewChainQueryHandler(chainQueryService)

	router := api.NewRouter(api.RouterDeps{
		HealthHandler:     healthHandler,
		StateHandler:      stateHandler,
		DepositHandler:    depositHandler,
		WithdrawHandler:   withdrawHandler,
		BatchHandler:      batchHandler,
		ProofHandler:      proofHandler,
		ChainQueryHandler: chainQueryHandler,
	})

	addr := ":" + port
	log.Printf("ganc-sys backend API listening on http://localhost%s", addr)
	log.Printf("chain query mode=%s rest=%s", chainQueryMode, chainRESTURL)
	log.Printf("withdraw request store=%s", withdrawRequestStore)
	log.Printf("batch build store=%s", batchBuildStore)
	log.Printf("proof bundle store=%s", proofBundleStore)

	if err := http.ListenAndServe(addr, router.Routes()); err != nil {
		log.Fatal(err)
	}
}

func openDatabaseIfNeeded(
	withdrawRequestStore string,
	batchBuildStore string,
	proofBundleStore string,
) *pgxpool.Pool {
	needsDB :=
		withdrawRequestStore == repository.WithdrawRequestStorePostgres ||
			batchBuildStore == repository.BatchBuildStorePostgres ||
			proofBundleStore == repository.ProofBundleStorePostgres

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
