package api

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/handler"
)

type RouterDeps struct {
	HealthHandler *handler.HealthHandler
	MockHandler   *handler.MockHandler
}

type Router struct {
	healthHandler *handler.HealthHandler
	mockHandler   *handler.MockHandler
}

func NewRouter(deps RouterDeps) *Router {
	return &Router{
		healthHandler: deps.HealthHandler,
		mockHandler:   deps.MockHandler,
	}
}

func (r *Router) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", r.healthHandler.GetHealth)

	mux.HandleFunc("GET /api/state", r.mockHandler.GetState)
	mux.HandleFunc("POST /api/deposit", r.mockHandler.MockDeposit)
	mux.HandleFunc("POST /api/withdraw-request", r.mockHandler.MockWithdrawRequest)
	mux.HandleFunc("POST /api/batch/build", r.mockHandler.MockBuildBatch)
	mux.HandleFunc("POST /api/proof/generate", r.mockHandler.MockGenerateProof)
	mux.HandleFunc("POST /api/batch/submit", r.mockHandler.MockSubmitBatch)
	mux.HandleFunc("POST /api/withdraw/claim", r.mockHandler.MockClaimWithdraw)

	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")

		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, req)
	})
}
