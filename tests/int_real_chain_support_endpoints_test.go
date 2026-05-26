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

func TestRealChainWithdrawRecordEndpointReturnsNotFound(t *testing.T) {
	if os.Getenv("RUN_CHAIN_REST_TESTS") != "1" {
		t.Skip("set RUN_CHAIN_REST_TESTS=1 to run real chain REST tests")
	}

	server := newRealChainSupportTestServer(t)

	rec := performRequest(t, server, http.MethodGet, "/api/chain/withdraw-records/wd-1", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]string](t, rec)

	if body["error"] != "withdraw record not found" {
		t.Fatalf("expected withdraw record not found error, got %q", body["error"])
	}
}

func TestRealChainNullifierEndpointReturnsUsedFalse(t *testing.T) {
	if os.Getenv("RUN_CHAIN_REST_TESTS") != "1" {
		t.Skip("set RUN_CHAIN_REST_TESTS=1 to run real chain REST tests")
	}

	server := newRealChainSupportTestServer(t)

	rec := performRequest(t, server, http.MethodGet, "/api/chain/nullifiers/0xmocknullifier", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeJSON[map[string]any](t, rec)

	if body["nullifier"] != "0xmocknullifier" {
		t.Fatalf("expected nullifier=0xmocknullifier, got %v", body["nullifier"])
	}

	used, ok := body["used"].(bool)
	if !ok {
		t.Fatalf("expected used boolean, got %T", body["used"])
	}

	if used {
		t.Fatalf("expected used=false")
	}
}

func newRealChainSupportTestServer(t *testing.T) http.Handler {
	t.Helper()

	baseURL := os.Getenv("CHAIN_REST_URL")
	if baseURL == "" {
		baseURL = "http://localhost:1317"
	}

	memoryStore := store.NewMemoryStore()

	healthRepository := repository.NewHealthRepository()
	healthService := service.NewHealthService(healthRepository)
	healthHandler := handler.NewHealthHandler(healthService)

	chainQueryClient := chain.NewRestQueryClient(baseURL)

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

	return router.Routes()
}
