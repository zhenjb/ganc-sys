package main

import (
	"log"
	"net/http"
	"os"

	"github.com/zhenjb/ganc-sys/internal/api"
	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/handler"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
)

func main() {
	port := getenv("PORT", "8080")

	healthRepository := repository.NewHealthRepository()
	healthService := service.NewHealthService(healthRepository)
	healthHandler := handler.NewHealthHandler(healthService)

	stateRepository := repository.NewStateRepository()
	stateService := service.NewStateService(stateRepository)
	stateHandler := handler.NewStateHandler(stateService)

	chainClient := chain.NewLocalClient()

	depositRepository := repository.NewDepositRepository()
	depositService := service.NewDepositService(depositRepository, chainClient)
	depositHandler := handler.NewDepositHandler(depositService)

	withdrawRepository := repository.NewWithdrawRepository()
	withdrawService := service.NewWithdrawService(withdrawRepository)
	withdrawHandler := handler.NewWithdrawHandler(withdrawService)

	batchRepository := repository.NewBatchRepository()
	batchService := service.NewBatchService(batchRepository, withdrawRepository)
	batchHandler := handler.NewBatchHandler(batchService)

	proofRepository := repository.NewProofRepository()
	proofService := service.NewProofService(proofRepository)
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
