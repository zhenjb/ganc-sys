package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/handler"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// newStateHandler builds a base StateHandler; if wire is true it also wires a
// real order service (funded via deposits) as the INT-T07 trading extension.
func newStateHandler(t *testing.T, wire bool, deposits []types.DepositRecord) (*handler.StateHandler, *service.RealOrderService) {
	t.Helper()
	stateSvc := service.NewStateService(repository.NewStateRepository(store.NewMemoryStore()))
	h := handler.NewStateHandler(stateSvc)
	if !wire {
		return h, nil
	}
	mgr := state.NewOffchainStateManager()
	for _, d := range deposits {
		if _, err := mgr.ApplyDeposit(d); err != nil {
			t.Fatalf("deposit: %v", err)
		}
	}
	svc, err := service.NewRealOrderService(mgr, service.DefaultMarkets(), func() int64 { return 1_000_000 })
	if err != nil {
		t.Fatalf("order service: %v", err)
	}
	h.SetTradeStateProvider(svc)
	return h, svc
}

// Backward-compat: with no trading extension wired, GET /api/state omits every
// trading field (omitempty) — the deposit/withdraw dashboard is unchanged.
func TestStateBackwardCompatNoTradeFields(t *testing.T) {
	h, _ := newStateHandler(t, false, nil)
	rec := httptest.NewRecorder()
	h.GetState(rec, httptest.NewRequest(http.MethodGet, "/api/state", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, field := range []string{"reservedBalances", "openOrders", "latestTrades", "marketStatus"} {
		if strings.Contains(body, field) {
			t.Fatalf("base state unexpectedly contains %q: %s", field, body)
		}
	}
	// A core field is still present.
	if !strings.Contains(body, "currentStateRoot") {
		t.Fatalf("base state missing currentStateRoot: %s", body)
	}
}

// Nhóm 4 (b): SetMode overrides the dashboard "mode" field to reflect the runtime
// (e.g. "cosmos") instead of the MemoryStore seed "local". Empty leaves it as-is.
func TestStateModeReflectsRuntime(t *testing.T) {
	h, _ := newStateHandler(t, false, nil)
	h.SetMode("cosmos")

	rec := httptest.NewRecorder()
	h.GetState(rec, httptest.NewRequest(http.MethodGet, "/api/state", nil))

	var st types.AppState
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Mode != "cosmos" {
		t.Fatalf("mode = %q, want cosmos", st.Mode)
	}
}

// With the extension wired, GET /api/state?owner= returns the trading slice.
func TestStateExtReturnsTradingFields(t *testing.T) {
	h, svc := newStateHandler(t, true, []types.DepositRecord{
		{DepositID: "d1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "5000"},
	})
	order := signOrder(t, types.SignedOrder{
		Owner: "cosmos1alice", Market: "ATOM/USDC", Side: types.SideBuy,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1",
	})
	if _, err := svc.CreateOrder(context.Background(), order); err != nil {
		t.Fatalf("order: %v", err)
	}

	rec := httptest.NewRecorder()
	h.GetState(rec, httptest.NewRequest(http.MethodGet, "/api/state?owner=cosmos1alice", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var st types.AppState
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(st.OpenOrders) != 1 || st.OpenOrders[0].Owner != "cosmos1alice" {
		t.Fatalf("openOrders = %+v, want alice's one order", st.OpenOrders)
	}
	var reserved string
	for _, rb := range st.ReservedBalances {
		if rb.Owner == "cosmos1alice" && rb.Denom == "uusdc" {
			reserved = rb.Reserved
		}
	}
	if reserved != "2020" {
		t.Fatalf("alice reserved uusdc = %q, want 2020", reserved)
	}
	// marketStatus is keyed by the denom pair (uatom/uusdc), not the display symbol.
	if st.MarketStatus["uatom/uusdc"] != types.MarketActive {
		t.Fatalf("marketStatus[uatom/uusdc] = %q, want active", st.MarketStatus["uatom/uusdc"])
	}
	// userBalances is overridden with the real off-chain total (available+reserved):
	// alice's 5000 uusdc stays 5000 after locking 2020 as collateral.
	if st.UserBalances["cosmos1alice/uusdc"] != "5000" {
		t.Fatalf("userBalances[cosmos1alice/uusdc] = %q, want 5000", st.UserBalances["cosmos1alice/uusdc"])
	}
	// denoms lists the registry's traded denoms (base+quote), sorted+deduped.
	if len(st.Denoms) != 3 || st.Denoms[0] != "uatom" || st.Denoms[1] != "uosmo" || st.Denoms[2] != "uusdc" {
		t.Fatalf("denoms = %v, want [uatom uosmo uusdc]", st.Denoms)
	}
	// Core fields still present.
	if st.CurrentStateRoot == "" {
		t.Fatal("core currentStateRoot missing after extension")
	}
}
