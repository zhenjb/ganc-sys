package main

import (
	"log"
	"net/http"
	"os"

	"github.com/zhenjb/ganc-sys/internal/api"
	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/chain"
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

	addr := ":" + port
	log.Printf("ganc-sys backend API listening on http://localhost%s", addr)

	if err := http.ListenAndServe(addr, router.Routes()); err != nil {
		log.Fatal(err)
	}
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
