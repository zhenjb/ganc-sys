package api

import (
	"net/http"

	"github.com/zhenjb/ganc-sys/internal/handler"
)

type RouterDeps struct {
	HealthHandler             *handler.HealthHandler
	StateHandler              *handler.StateHandler
	DepositHandler            *handler.DepositHandler
	WithdrawHandler           *handler.WithdrawHandler
	BatchHandler              *handler.BatchHandler
	ProofHandler              *handler.ProofHandler
	ChainQueryHandler         *handler.ChainQueryHandler
	OffchainSettlementHandler *handler.OffchainSettlementHandler
}

type Router struct {
	healthHandler             *handler.HealthHandler
	stateHandler              *handler.StateHandler
	depositHandler            *handler.DepositHandler
	withdrawHandler           *handler.WithdrawHandler
	batchHandler              *handler.BatchHandler
	proofHandler              *handler.ProofHandler
	chainQueryHandler         *handler.ChainQueryHandler
	offchainSettlementHandler *handler.OffchainSettlementHandler
}

func NewRouter(deps RouterDeps) *Router {
	return &Router{
		healthHandler:             deps.HealthHandler,
		stateHandler:              deps.StateHandler,
		depositHandler:            deps.DepositHandler,
		withdrawHandler:           deps.WithdrawHandler,
		batchHandler:              deps.BatchHandler,
		proofHandler:              deps.ProofHandler,
		chainQueryHandler:         deps.ChainQueryHandler,
		offchainSettlementHandler: deps.OffchainSettlementHandler,
	}
}

func (r *Router) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", r.healthHandler.GetHealth)

	mux.HandleFunc("GET /api/state", r.stateHandler.GetState)

	mux.HandleFunc("POST /api/deposit", r.depositHandler.CreateDeposit)
	mux.HandleFunc("GET /api/deposits", r.depositHandler.ListDeposits)
	mux.HandleFunc("GET /api/deposits/{depositId}", r.depositHandler.GetDeposit)

	mux.HandleFunc("POST /api/withdraw-request", r.withdrawHandler.CreateWithdrawRequest)
	mux.HandleFunc("GET /api/withdraw-requests", r.withdrawHandler.ListWithdrawRequests)
	mux.HandleFunc("GET /api/withdraw-requests/{withdrawId}", r.withdrawHandler.GetWithdrawRequest)
	mux.HandleFunc("POST /api/withdraw/claim", r.withdrawHandler.ClaimWithdraw)

	mux.HandleFunc("POST /api/batch/build", r.batchHandler.BuildBatch)
	mux.HandleFunc("POST /api/batch/submit", r.batchHandler.SubmitBatch)

	mux.HandleFunc("POST /api/proof/generate", r.proofHandler.GenerateProof)

	if r.chainQueryHandler != nil {
		mux.HandleFunc("GET /api/chain/withdraw-records/{withdrawId}", r.chainQueryHandler.GetWithdrawRecord)
		mux.HandleFunc("GET /api/chain/nullifiers/{nullifier}", r.chainQueryHandler.GetNullifierUsed)
	}

	if r.offchainSettlementHandler != nil {
		mux.HandleFunc(
			"POST /api/internal/offchain-settlement/batches/{batchId}/cancel",
			r.offchainSettlementHandler.CancelIncludedBatch,
		)
	}

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
