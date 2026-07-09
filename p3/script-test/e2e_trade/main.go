// e2e_trade is the INT-T09 one-command trading demo: alice buys, bob sells,
// they cross on ATOM/USDC, the fill settles through build → prove(stub) →
// submit(relayer), and the balances/reserved/fee/root transition is printed as
// report evidence. Run from repo root:
//
//	go run ./p3/script-test/e2e_trade
//
// It is SELF-CONTAINED and IDEMPOTENT: it boots the real API router over an
// in-process httptest server with a FRESH funded off-chain state each run (no
// chain, no DB, no nonce/nullifier carryover), and drives the actual HTTP
// endpoints POST /api/order, GET /api/trades and GET /api/state exactly as the
// frontend would. The settlement step is triggered explicitly (the in-process
// trade-settlement sequencer would otherwise run it on a tick).
//
// Wave 2: against a live chain + gazk, swap the relayer to cosmos and the prover
// to gazk (SetTradeSettlement); the same flow then commits the root on-chain.
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
	fmt.Println("========================================")
	fmt.Println(" INT-T09 — E2E trade demo (alice buy / bob sell → match → settle)")
	fmt.Println("========================================")

	// --- Fresh, funded off-chain state (idempotent: new manager each run). ---
	mgr := state.NewOffchainStateManager()
	mustCredit(mgr, "d-alice", alice, quote, "5000") // alice funds quote to buy
	mustCredit(mgr, "d-bob", bob, base, "50")        // bob funds base to sell

	svc, err := service.NewRealOrderService(mgr, service.DefaultMarkets(), func() int64 { return demoNow })
	if err != nil {
		fail("build order service: %v", err)
	}
	// INT-T08 submit path (local relayer — accepts without a chain).
	svc.SetTradeSettlement(nil, service.NewRelayerTradeSubmitter(relayer.NewLocalClient()))

	// --- Real API router over an in-process server. ---
	stateHandler := handler.NewStateHandler(service.NewStateService(repository.NewStateRepository(store.NewMemoryStore())))
	stateHandler.SetTradeStateProvider(svc)
	router := api.NewRouter(api.RouterDeps{
		OrderHandler: handler.NewOrderHandler(svc),
		StateHandler: stateHandler,
	})
	srv := httptest.NewServer(router.Routes())
	defer srv.Close()
	client := &apiClient{base: srv.URL}

	stage(1, "BEFORE — funded, no orders")
	printState(client)

	// --- Stage 2: place two crossing orders via POST /api/order. ---
	stage(2, "POST /api/order — alice BUY 20 @ 100, bob SELL 20 @ 100")
	aliceResp := client.postOrder(signed(alice, types.SideBuy, "100", "20"))
	fmt.Printf("   alice → status=%s remaining=%s orderHash=%s\n", aliceResp.Status, aliceResp.State.Remaining, short(aliceResp.State.OrderHash))
	bobResp := client.postOrder(signed(bob, types.SideSell, "100", "20"))
	fmt.Printf("   bob   → status=%s remaining=%s orderHash=%s\n", bobResp.Status, bobResp.State.Remaining, short(bobResp.State.OrderHash))
	assert(bobResp.Status == types.OrderStatusFilled, "bob order should be FILLED on entry (crossing), got %s", bobResp.Status)

	// --- Stage 3: matching produced a fill — verify via GET /api/trades. ---
	stage(3, "GET /api/trades?market=ATOM/USDC — matching (INT-T05) produced a fill")
	trades := client.getTrades(market)
	assert(len(trades.Fills) == 1, "expected 1 fill, got %d", len(trades.Fills))
	f := trades.Fills[0]
	fmt.Printf("   fill: %s buys %s @ %s (maker price) from %s | makerFee=%s takerFee=%s tradeId=%s\n",
		f.Buyer, f.Qty, f.Price, f.Seller, f.MakerFee, f.TakerFee, short(f.TradeID))
	assert(f.Price == "100" && f.Qty == "20", "fill must be 20 @ 100 (maker price/min qty)")

	stage(4, "AFTER MATCH, BEFORE SETTLE — reserved locked, balances unchanged")
	printState(client)
	assertAcct(mgr, alice, quote, "2980", "2020")
	assertAcct(mgr, bob, base, "30", "20")

	// --- Stage 5: settle the trade batch (INT-T06 build→prove(stub)→submit). ---
	stage(5, "SETTLE — build(trades[]) → prove(stub) → submit(relayer)")
	rootBefore := mgr.Root()
	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil {
		fail("settle: %v", err)
	}
	assert(settled, "expected a batch to settle")
	rootAfter := mgr.Root()
	fmt.Printf("   settled ✓   oldRoot=%s\n           newRoot=%s\n", short(rootBefore), short(rootAfter))
	assert(rootBefore != rootAfter, "state root must advance after settle")

	// --- Stage 6: final state — balances moved, reserved released, fee credited. ---
	stage(6, "AFTER SETTLE — balances transitioned (evidence)")
	printState(client)
	//  alice paid 2000 notional + 10 maker fee → uusdc 2990; received 20 uatom.
	//  bob delivered 20 uatom → 30 left; received 2000 − 20 taker fee → 1980 uusdc.
	//  fee account: 10 + 20 = 30 uusdc. Conservation: uusdc 5000, uatom 50.
	assertAcct(mgr, alice, quote, "2990", "")
	assertAcct(mgr, alice, base, "20", "")
	assertAcct(mgr, bob, quote, "1980", "")
	assertAcct(mgr, bob, base, "30", "")
	assertAcct(mgr, feeOwner, quote, "30", "")
	assertConserved(mgr)

	fmt.Println("\n========================================")
	fmt.Println(" ✓ E2E TRADE DEMO PASSED — 1 command, order → match → settle, balances correct")
	fmt.Println("========================================")
}

// ---------------------------------------------------------------------------
// HTTP client over the in-process API.
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
// Evidence printing + assertions.
// ---------------------------------------------------------------------------

// printState renders GET /api/state (reservedBalances + latestTrades) for alice,
// bob and the fee account — the report evidence.
func printState(c *apiClient) {
	for _, owner := range []string{alice, bob, feeOwner} {
		st := c.getState(owner)
		for _, rb := range st.ReservedBalances {
			fmt.Printf("   %-22s %-6s available=%-6s reserved=%-6s\n", rb.Owner, rb.Denom, rb.Available, rb.Reserved)
		}
	}
}

func signed(owner string, side types.OrderSide, price, qty string) types.SignedOrder {
	o := types.SignedOrder{
		Owner: owner, Market: market, Side: side,
		Price: price, Qty: qty, Expiry: "2000000", Nonce: "1",
	}
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

// assertConserved checks total uusdc == 5000 and total uatom == 50 across all
// participants (value conservation — no mint/burn).
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

func stage(n int, msg string) { fmt.Printf("\n[%d] %s\n", n, msg) }

func short(hexStr string) string {
	if len(hexStr) <= 14 {
		return hexStr
	}
	return hexStr[:10] + "…" + hexStr[len(hexStr)-4:]
}

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
