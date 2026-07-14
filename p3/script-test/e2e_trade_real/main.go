// e2e_trade_real is the ZK-T10 integration demo: the SAME alice-buy/bob-sell
// trade as INT-T09, but settled through the REAL gazk prover + verifier over HTTP
// (RemoteTradeProver → POST /prove {trade}; RemoteTradeVerifierSubmitter → POST
// /verify-trade) instead of the Wave-1 local stub. A settle that completes proves
// that gazk produced a real Groth16 trade proof (vkId gazk-trade-v1) AND verified
// it against the real vk — the same crypto check B runs on-chain (ONCHAIN-T04).
//
// Requires a gazk server reachable at GAZK_TRADE_URL (default http://localhost:8090)
// started with a stable GAZK_KEY_DIR (so pk/vk persist). The wrapper script
// scripts/e2e_trade_real_proof.sh starts gazk, runs this, and stops it.
//
// v0 roots (TRD-A1): the real proof now commits P4's v0 (SHA-256) WIRE roots as its
// 8 public inputs — the circuit binds them opaquely (ToBinary) instead of recomputing
// v1 MiMC. So proofBundle.PublicInputs == BuildPublicInputsWithTrades(upd, com)
// byte-exact, and the submitter re-binds P4's v0 roots against the proof (the same
// consistency the chain verifier enforces) before the crypto check. Byte-exact
// P4↔chain root composition now HOLDS end-to-end.
//
//	GAZK_TRADE_URL=http://localhost:8090 go run ./p3/script-test/e2e_trade_real
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/zhenjb/ganc-sys/internal/api"
	"github.com/zhenjb/ganc-sys/internal/handler"
	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const (
	alice    = "cosmos1alice"
	bob      = "cosmos1bob"
	market   = "ATOM/USDC"
	quote    = "uusdc"
	base     = "uatom"
	demoNow  = int64(1_000_000)
	feeOwner = state.FeeAccountOwner
)

func main() {
	gazkURL := envOr("GAZK_TRADE_URL", "http://localhost:8090")

	fmt.Println("========================================")
	fmt.Println(" ZK-T10 — E2E trade with REAL gazk proof (alice buy / bob sell)")
	fmt.Printf("  gazk: %s\n", gazkURL)
	fmt.Println("========================================")

	// Precondition: gazk up + serving the real trade verifier.
	checkGazk(gazkURL)

	// --- Fresh, funded off-chain state. ---
	mgr := state.NewOffchainStateManager()
	mustCredit(mgr, "d-alice", alice, quote, "5000")
	mustCredit(mgr, "d-bob", bob, base, "50")

	svc, err := service.NewRealOrderService(mgr, service.DefaultMarkets(), func() int64 { return demoNow })
	if err != nil {
		fail("build order service: %v", err)
	}
	// REAL gazk prover. The submitter is env-selected (TRD-V1.0):
	//   default / "gazk-verify" → RemoteTradeVerifierSubmitter (ZK-T10 prove→verify loop)
	//   "chain"                 → RelayerTradeSubmitter(LocalClient) — proves the SAME
	//     real gazk proof flows into the CHAIN-SUBMIT path and is accepted (the local
	//     client enforces the 8-input + root binding the chain checks). This is the
	//     local proxy for TRD-V1: a real v0 proof reaching MsgSubmitBatchProof.
	submitMode := envOr("TRADE_SUBMIT_MODE", "gazk-verify")
	var submitter service.TradeSubmitter
	switch submitMode {
	case "chain":
		submitter = service.NewRelayerTradeSubmitter(relayer.NewLocalClient())
	default:
		submitter = service.NewRemoteTradeVerifierSubmitter(gazkURL)
	}
	svc.SetTradeSettlement(service.NewRemoteTradeProver(gazkURL), submitter)
	fmt.Printf("  trade submit mode: %s\n", submitMode)

	stateHandler := handler.NewStateHandler(service.NewStateService(repository.NewStateRepository(store.NewMemoryStore())))
	stateHandler.SetTradeStateProvider(svc)
	router := api.NewRouter(api.RouterDeps{
		OrderHandler: handler.NewOrderHandler(svc),
		StateHandler: stateHandler,
	})
	srv := httptest.NewServer(router.Routes())
	defer srv.Close()
	client := &apiClient{base: srv.URL}

	// --- Place two crossing orders. ---
	stage(1, "POST /api/order — alice BUY 20 @ 100, bob SELL 20 @ 100")
	client.postOrder(signed(alice, types.SideBuy, "100", "20"))
	bobResp := client.postOrder(signed(bob, types.SideSell, "100", "20"))
	assert(bobResp.Status == types.OrderStatusFilled, "bob should be FILLED on entry, got %s", bobResp.Status)

	stage(2, "GET /api/trades — matching produced a fill")
	trades := client.getTrades(market)
	assert(len(trades.Fills) == 1, "expected 1 fill, got %d", len(trades.Fills))
	f := trades.Fills[0]
	fmt.Printf("   fill: %s buys %s @ %s from %s | makerFee=%s takerFee=%s\n", f.Buyer, f.Qty, f.Price, f.Seller, f.MakerFee, f.TakerFee)

	stage(3, "AFTER MATCH — reserved locked")
	assertAcct(mgr, alice, quote, "2980", "2020")
	assertAcct(mgr, bob, base, "30", "20")

	// --- Settle through the REAL gazk prover; submitter per mode. ---
	settleStep := "prove(gazk REAL) → verify(gazk REAL vk)"
	if submitMode == "chain" {
		settleStep = "prove(gazk REAL) → submit CHAIN-path (relayer, 8-input v0 accepted)"
	}
	stage(4, "SETTLE — build → "+settleStep)
	rootBefore := mgr.Root()
	start := time.Now()
	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil {
		fail("settle with real gazk proof: %v", err)
	}
	assert(settled, "expected a batch to settle")
	fmt.Printf("   settled ✓ in %v  (real Groth16 proof; submit mode=%s)\n", time.Since(start).Round(time.Millisecond), submitMode)
	assert(rootBefore != mgr.Root(), "state root must advance after settle")

	// --- Final balances — identical to INT-T09 (only the prover/verifier changed). ---
	stage(5, "AFTER SETTLE — balances transitioned (evidence)")
	printState(client)
	assertAcct(mgr, alice, quote, "2990", "")
	assertAcct(mgr, alice, base, "20", "")
	assertAcct(mgr, bob, quote, "1980", "")
	assertAcct(mgr, bob, base, "30", "")
	assertAcct(mgr, feeOwner, quote, "30", "")
	assertConserved(mgr)

	fmt.Println("\n========================================")
	fmt.Println(" ✓ E2E REAL-PROOF PASSED — order → match → settle with a real gazk ZK proof")
	fmt.Println("========================================")
}

// checkGazk asserts gazk is reachable and serving the real trade verifier.
func checkGazk(baseURL string) {
	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		fail("gazk not reachable at %s (start it with GAZK_KEY_DIR): %v", baseURL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var h map[string]any
	if err := json.Unmarshal(raw, &h); err != nil {
		fail("gazk /health decode: %v", err)
	}
	if h["tradeVerificationKeyId"] != "gazk-trade-v1" {
		fail("gazk not serving trade vkId gazk-trade-v1, got %v", h["tradeVerificationKeyId"])
	}
	fmt.Printf("   gazk /health ✓  tradeVkId=%v tradeProve=%v tradeVerifierStub=%v\n",
		h["tradeVerificationKeyId"], h["tradeProve"], h["tradeVerifierStub"])
}

// ---------------------------------------------------------------------------
// HTTP client over the in-process API (same as INT-T09).
// ---------------------------------------------------------------------------

type apiClient struct{ base string }

func (c *apiClient) postOrder(o types.SignedOrder) types.OrderResponse {
	body, _ := json.Marshal(o)
	resp, err := http.Post(c.base+"/api/order", "application/json", bytes.NewReader(body))
	if err != nil {
		fail("POST /api/order: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fail("POST /api/order status %d: %s", resp.StatusCode, string(raw))
	}
	var out types.OrderResponse
	mustJSON(raw, &out, "order response")
	return out
}

func (c *apiClient) getTrades(mkt string) types.TradesResponse {
	raw := c.get("/api/trades?market=" + mkt)
	var out types.TradesResponse
	mustJSON(raw, &out, "trades")
	return out
}

func (c *apiClient) getState(owner string) types.AppState {
	raw := c.get("/api/state?owner=" + owner)
	var out types.AppState
	mustJSON(raw, &out, "state")
	return out
}

func (c *apiClient) get(path string) []byte {
	resp, err := http.Get(c.base + path)
	if err != nil {
		fail("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fail("GET %s status %d: %s", path, resp.StatusCode, string(raw))
	}
	return raw
}

// ---------------------------------------------------------------------------
// Evidence + assertions (same as INT-T09).
// ---------------------------------------------------------------------------

func printState(c *apiClient) {
	for _, owner := range []string{alice, bob, feeOwner} {
		st := c.getState(owner)
		for _, rb := range st.ReservedBalances {
			fmt.Printf("   %-22s %-6s available=%-6s reserved=%-6s\n", rb.Owner, rb.Denom, rb.Available, rb.Reserved)
		}
	}
}

func signed(owner string, side types.OrderSide, price, qty string) types.SignedOrder {
	o := types.SignedOrder{Owner: owner, Market: market, Side: side, Price: price, Qty: qty, Expiry: "2000000", Nonce: "1"}
	sig, err := state.MockOrderSignature(o)
	if err != nil {
		fail("sign order: %v", err)
	}
	o.Signature = sig
	return o
}

func mustCredit(m *state.OffchainStateManager, id, owner, denom, amount string) {
	if _, err := m.ApplyDeposit(types.DepositRecord{DepositID: id, Owner: owner, Denom: denom, Amount: amount}); err != nil {
		fail("fund %s/%s: %v", owner, denom, err)
	}
}

func assertAcct(m *state.OffchainStateManager, owner, denom, wantAvail, wantReserved string) {
	acc := m.Account(owner, denom)
	assert(acc.Balance == wantAvail, "%s/%s available=%q want %q", owner, denom, acc.Balance, wantAvail)
	assert(acc.Reserved == wantReserved, "%s/%s reserved=%q want %q", owner, denom, acc.Reserved, wantReserved)
}

func assertConserved(m *state.OffchainStateManager) {
	q := bal(m, alice, quote) + bal(m, bob, quote) + bal(m, feeOwner, quote)
	b := bal(m, alice, base) + bal(m, bob, base) + bal(m, feeOwner, base)
	assert(q == 5000, "uusdc conservation: total %d want 5000", q)
	assert(b == 50, "uatom conservation: total %d want 50", b)
	fmt.Printf("   conservation ✓  uusdc total=%d  uatom total=%d\n", q, b)
}

func bal(m *state.OffchainStateManager, owner, denom string) int {
	acc := m.Account(owner, denom)
	n := 0
	for _, c := range acc.Balance {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func stage(n int, msg string) { fmt.Printf("\n[%d] %s\n", n, msg) }

func assert(ok bool, format string, args ...any) {
	if !ok {
		fail("ASSERT FAILED: "+format, args...)
	}
}

func mustJSON(raw []byte, v any, what string) {
	if err := json.Unmarshal(raw, v); err != nil {
		fail("decode %s: %v (body=%s)", what, err, string(raw))
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\n✗ "+format+"\n", args...)
	os.Exit(1)
}
