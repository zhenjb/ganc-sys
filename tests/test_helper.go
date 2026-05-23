package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/api"
	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/handler"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
)

func newTestServer() http.Handler {
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
