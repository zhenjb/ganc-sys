// e2e_trade_failure is the INT-T10 failure demo — the opposite of INT-T09. It
// proves the trading system is SAFE by driving each negative case and collecting
// the rejection evidence, then checking the state has NO side effect (no orphan
// reserved, no garbage order/fill). Run from repo root:
//
//	go run ./p3/script-test/e2e_trade_failure
//
// Cases (mirroring the STATE-T11 failure vectors + the pipeline rollback):
//
//	C1 over-reserve   POST order > available            → 400 insufficient_balance ; nothing reserved
//	C2 non-crossing   bid < ask                         → 0 fills (no garbage trade) ; both rest
//	C3 order replay   place → cancel → re-submit        → 400 order_nullifier_used  ; nothing re-locked
//	C4 prove-fail     settle with a failing submitter   → rollback (STATE-T10) + re-enqueue ; then recover
//
// Self-contained and idempotent: a FRESH funded off-chain state per case over an
// in-process API server (no chain, no DB). Exits non-zero on any assertion
// failure, so it doubles as a CI safety gate.
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
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const (
	alice   = "cosmos1alice"
	bob     = "cosmos1bob"
	market  = "ATOM/USDC"
	quote   = "uusdc"
	base    = "uatom"
	demoNow = int64(1_000_000)
)

func main() {
	fmt.Println("========================================")
	fmt.Println(" INT-T10 — Trade FAILURE demo (safety evidence)")
	fmt.Println("========================================")

	caseOverReserve()
	caseNonCrossing()
	caseReplay()
	caseProveFailRollback()

	fmt.Println("\n========================================")
	fmt.Println(" ✓ ALL FAILURE CASES REJECTED CORRECTLY — no side effects, state safe")
	fmt.Println("========================================")
}

// C1 — order exceeding available is rejected insufficient_balance, and NO
// collateral is locked (no orphan reservation).
func caseOverReserve() {
	stage("C1", "over-reserve — POST buy 20@100 (needs 2020) with only 100 available")
	mgr, svc, client := boot(map[string]map[string]string{alice: {quote: "100"}})
	_ = svc

	status, reason := client.postOrderReject(signed(alice, types.SideBuy, "100", "20"))
	fmt.Printf("   reject: HTTP %d reason=%q\n", status, reason)
	assert(status == http.StatusBadRequest, "want 400, got %d", status)
	assert(reason == service.ReasonInsufficientBalance, "want insufficient_balance, got %q", reason)

	// State unchanged: available 100, nothing reserved.
	acc := mgr.Account(alice, quote)
	fmt.Printf("   state after reject: %s %s available=%s reserved=%q\n", alice, quote, acc.Balance, acc.Reserved)
	assert(acc.Balance == "100" && acc.Reserved == "", "over-reserve must not lock funds (got %s/%q)", acc.Balance, acc.Reserved)
	fmt.Println("   ✓ rejected, no reserved locked")
}

// C2 — non-crossing orders produce NO fill (no garbage trade); both rest.
func caseNonCrossing() {
	stage("C2", "non-crossing — alice BUY 20@99, bob SELL 20@100 (bid < ask)")
	_, svc, client := boot(map[string]map[string]string{
		alice: {quote: "5000"}, bob: {base: "50"},
	})

	client.postOrderOK(signed(alice, types.SideBuy, "99", "20"))
	client.postOrderOK(signed(bob, types.SideSell, "100", "20"))

	trades := client.getTrades(market)
	fmt.Printf("   GET /api/trades → %d fills\n", len(trades.Fills))
	assert(len(trades.Fills) == 0, "non-crossing must yield 0 fills, got %d", len(trades.Fills))

	aOpen := client.getOrders(alice)
	bOpen := client.getOrders(bob)
	fmt.Printf("   open orders: alice=%d bob=%d (both still resting)\n", len(aOpen.OpenOrders), len(bOpen.OpenOrders))
	assert(len(aOpen.OpenOrders) == 1 && len(bOpen.OpenOrders) == 1, "both orders should rest")
	assert(svc.PendingFillCount() == 0, "no fill should be queued")
	fmt.Println("   ✓ no garbage trade; both orders rest")
}

// C3 — replaying a used order (nullifier consumed on cancel) is rejected
// order_nullifier_used, and nothing is re-locked.
func caseReplay() {
	stage("C3", "order replay — place → cancel → re-submit the SAME order")
	mgr, _, client := boot(map[string]map[string]string{alice: {quote: "5000"}})

	order := signed(alice, types.SideBuy, "100", "20")
	placed := client.postOrderOK(order)
	fmt.Printf("   placed: status=%s orderHash=%s (reserved locked)\n", placed.Status, short(placed.State.OrderHash))

	client.cancel(placed.State.OrderHash, alice)
	fmt.Printf("   cancelled → nullifier consumed, reserved released\n")

	status, reason := client.postOrderReject(order) // replay
	fmt.Printf("   replay reject: HTTP %d reason=%q\n", status, reason)
	assert(status == http.StatusBadRequest, "want 400, got %d", status)
	assert(reason == string(state.ReasonNullifierUsed), "want order_nullifier_used, got %q", reason)

	acc := mgr.Account(alice, quote)
	fmt.Printf("   state after replay: %s %s available=%s reserved=%q\n", alice, quote, acc.Balance, acc.Reserved)
	assert(acc.Balance == "5000" && acc.Reserved == "", "replay must not re-lock (got %s/%q)", acc.Balance, acc.Reserved)
	fmt.Println("   ✓ replay rejected; no double reserve")
}

// C4 — a settle-time submit failure rolls the batch back (STATE-T10) and
// re-enqueues; the sequencer then recovers with a working submitter (queue not
// stuck).
func caseProveFailRollback() {
	stage("C4", "prove/submit fail — rollback + reopen + sequencer recovery")
	mgr, svc, client := boot(map[string]map[string]string{
		alice: {quote: "5000"}, bob: {base: "50"},
	})
	client.postOrderOK(signed(alice, types.SideBuy, "100", "20"))
	client.postOrderOK(signed(bob, types.SideSell, "100", "20")) // crosses → fill queued
	assert(svc.PendingFillCount() == 1, "expected 1 queued fill")

	// Force a submit failure.
	svc.SetTradeSettlement(nil, failingSubmitter{})
	rootBefore := mgr.Root()
	_, err := svc.SettleTradesOnce(context.Background())
	fmt.Printf("   settle #1 (failing submitter): err=%v\n", err != nil)
	assert(err != nil, "settle should fail")

	// Rollback: pre-apply balances restored (reserved still locked), fee not
	// credited, root unchanged, fills re-enqueued (not stuck/lost).
	assertAcct(mgr, alice, quote, "2980", "2020")
	assertAcct(mgr, state.FeeAccountOwner, quote, "0", "")
	assert(mgr.Root() == rootBefore, "root must not advance on failed settle")
	assert(svc.PendingFillCount() == 1, "fills must be re-enqueued for retry, got %d", svc.PendingFillCount())
	fmt.Printf("   rollback ✓ (alice 2980/2020, fee 0, root unchanged, 1 fill re-queued)\n")

	// Recover.
	svc.SetTradeSettlement(nil, service.NewRelayerTradeSubmitter(relayer.NewLocalClient()))
	settled, err := svc.SettleTradesOnce(context.Background())
	fmt.Printf("   settle #2 (recovered): settled=%v err=%v\n", settled, err)
	assert(settled && err == nil, "retry should settle")
	assertAcct(mgr, alice, quote, "2990", "")
	assertAcct(mgr, state.FeeAccountOwner, quote, "30", "")
	assert(svc.PendingFillCount() == 0, "queue must drain after recovery")
	fmt.Println("   ✓ sequencer recovered; batch settled on retry; queue not stuck")
}

type failingSubmitter struct{}

func (failingSubmitter) SubmitTrade(_ context.Context, _ types.SettlementUpdate, _ types.BatchCommitments, _ types.ProofBundle) (string, bool, error) {
	return "", false, fmt.Errorf("submit-batch-proof rejected by chain (code=18): invalid proof")
}

// ---------------------------------------------------------------------------
// Harness: boot a fresh funded API server per case.
// ---------------------------------------------------------------------------

func boot(funding map[string]map[string]string) (*state.OffchainStateManager, *service.RealOrderService, *apiClient) {
	mgr := state.NewOffchainStateManager()
	i := 0
	for owner, denoms := range funding {
		for denom, amount := range denoms {
			i++
			if _, err := mgr.ApplyDeposit(types.DepositRecord{DepositID: fmt.Sprintf("d%d", i), Owner: owner, Denom: denom, Amount: amount}); err != nil {
				fail("fund %s/%s: %v", owner, denom, err)
			}
		}
	}
	svc, err := service.NewRealOrderService(mgr, service.DefaultMarkets(), func() int64 { return demoNow })
	if err != nil {
		fail("order service: %v", err)
	}
	svc.SetTradeSettlement(nil, service.NewRelayerTradeSubmitter(relayer.NewLocalClient()))
	router := api.NewRouter(api.RouterDeps{OrderHandler: handler.NewOrderHandler(svc)})
	srv := httptest.NewServer(router.Routes())
	return mgr, svc, &apiClient{base: srv.URL, srv: srv}
}

type apiClient struct {
	base string
	srv  *httptest.Server
}

func (c *apiClient) postOrderOK(o types.SignedOrder) types.OrderResponse {
	status, body := c.postOrderRaw(o)
	if status != http.StatusOK {
		fail("expected 200, got %d: %s", status, string(body))
	}
	var out types.OrderResponse
	mustJSON(body, &out, "order response")
	return out
}

func (c *apiClient) postOrderReject(o types.SignedOrder) (int, string) {
	status, body := c.postOrderRaw(o)
	var out struct {
		Error  string `json:"error"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &out)
	return status, out.Reason
}

func (c *apiClient) postOrderRaw(o types.SignedOrder) (int, []byte) {
	raw, _ := json.Marshal(o)
	resp, err := http.Post(c.base+"/api/order", "application/json", bytes.NewReader(raw))
	if err != nil {
		fail("POST /api/order: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func (c *apiClient) cancel(orderHash, owner string) {
	req, _ := http.NewRequest(http.MethodDelete, c.base+"/api/order/"+orderHash+"?owner="+owner, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail("DELETE /api/order: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fail("cancel status %d: %s", resp.StatusCode, string(body))
	}
}

func (c *apiClient) getTrades(mkt string) types.TradesResponse {
	var out types.TradesResponse
	mustJSON(c.get("/api/trades?market="+mkt), &out, "trades")
	return out
}

func (c *apiClient) getOrders(owner string) types.OpenOrdersResponse {
	var out types.OpenOrdersResponse
	mustJSON(c.get("/api/orders?owner="+owner), &out, "orders")
	return out
}

func (c *apiClient) get(path string) []byte {
	resp, err := http.Get(c.base + path)
	if err != nil {
		fail("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fail("GET %s status %d: %s", path, resp.StatusCode, string(body))
	}
	return body
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

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

func assertAcct(m *state.OffchainStateManager, owner, denom, wantAvail, wantReserved string) {
	acc := m.Account(owner, denom)
	assert(acc.Balance == wantAvail, "%s/%s available=%q want %q", owner, denom, acc.Balance, wantAvail)
	assert(acc.Reserved == wantReserved, "%s/%s reserved=%q want %q", owner, denom, acc.Reserved, wantReserved)
}

func stage(id, msg string) { fmt.Printf("\n[%s] %s\n", id, msg) }

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
