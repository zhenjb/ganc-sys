package tests

import (
	"net/http"
	"os"
	"testing"

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

func TestRealDepositEndpointFallsBackToChainRestAndReturnsNotFound(t *testing.T) {
	if os.Getenv("RUN_CHAIN_REST_TESTS") != "1" {
		t.Skip("set RUN_CHAIN_REST_TESTS=1 to run real chain REST tests")
	}

	baseURL := os.Getenv("CHAIN_REST_URL")
	if baseURL == "" {
		baseURL = "http://localhost:1317"
	}

	server := newRealDepositTestServer(baseURL)

	rec := performRequest(t, server, http.MethodGet, "/api/deposits/dep-1", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected deposit status 404, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]string](t, rec)

	if body["error"] != "deposit not found" {
		t.Fatalf("expected deposit not found error, got %q", body["error"])
	}
}

func newRealDepositTestServer(chainRESTURL string) http.Handler {
	memoryStore := store.NewMemoryStore()

	healthRepository := repository.NewHealthRepository()
	healthService := service.NewHealthService(healthRepository)
	healthHandler := handler.NewHealthHandler(healthService)

	chainQueryClient := chain.NewRestQueryClient(chainRESTURL)

	stateRepository := repository.NewStateRepositoryWithChainQuery(
		memoryStore,
		chainQueryClient,
		"rest",
	)
	stateService := service.NewStateService(stateRepository)
	stateHandler := handler.NewStateHandler(stateService)

	chainClient := chain.NewLocalClient()
	relayerClient := relayer.NewLocalClient()

	depositRepository := repository.NewDepositRepositoryWithChainQuery(
		memoryStore,
		chainQueryClient,
		"rest",
	)
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
