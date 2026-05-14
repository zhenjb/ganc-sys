package api

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/handler"
)

type RouterDeps struct {
	HealthHandler *handler.HealthHandler
}

type Router struct {
	healthHandler *handler.HealthHandler
}

func NewRouter(deps RouterDeps) *Router {
	return &Router{
		healthHandler: deps.HealthHandler,
	}
}

func (r *Router) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", r.healthHandler.GetHealth)

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
