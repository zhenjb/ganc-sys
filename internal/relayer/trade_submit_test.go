package relayer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// sampleTradeInput is a trade batch: settlementUpdate carries trades[] and the
// proofBundle carries the 8 public inputs ([0..5] core + [6]tradesRoot + [7]ordersRoot).
func sampleTradeInput() SubmitBatchInput {
	return SubmitBatchInput{
		SettlementUpdate: types.SettlementUpdate{
			BatchID:              "batch-7",
			OldStateRoot:         "0xrootA",
			NewStateRoot:         "0xrootB",
			Trades: []types.SettlementTrade{
				{TradeID: "0xt1-buy", Market: "ATOM/USDC", MakerOrderID: "0xmk", TakerOrderID: "0xtk",
					OrderHash: "0xmk", OrderNullifier: "0xnullbuy", Owner: "cosmos1alice", Denom: "uatom", Side: "buy",
					Amount: "20", Price: "100", BaseQty: "20", QuoteQty: "2000", MakerFee: "10", TakerFee: "20"},
				{TradeID: "0xt1-sell", Market: "ATOM/USDC", MakerOrderID: "0xmk", TakerOrderID: "0xtk",
					OrderHash: "0xtk", OrderNullifier: "0xnullsell", Owner: "cosmos1bob", Denom: "uatom", Side: "sell",
					Amount: "20", Price: "100", BaseQty: "20", QuoteQty: "2000", MakerFee: "10", TakerFee: "20"},
			},
			TradeBatchCommitment: "0xtbc",
		},
		BatchCommitments: types.BatchCommitments{
			DepositsRoot:        "0xdep",
			WithdrawalsRoot:     "0xwd",
			NullifiersRoot:      "0xnull",
			WithdrawOutputsRoot: "0xout",
			TradesRoot:          "0xtrades",
			OrdersRoot:          "0xorders",
		},
		ProofBundle: types.ProofBundle{
			Proof: "0xproof",
			PublicInputs: []string{
				"0xrootA", "0xrootB", "0xdep", "0xwd", "0xnull", "0xout", "0xtrades", "0xorders",
			},
			VerificationKeyID: "local-trade-v1",
		},
	}
}

func TestBuildTradeFlagsRequires8Inputs(t *testing.T) {
	in := sampleTradeInput()
	in.ProofBundle.PublicInputs = in.ProofBundle.PublicInputs[:6] // core-length
	if _, _, _, err := buildTradeSubmitBatchProofFlags(in); err == nil {
		t.Fatal("expected error for 6 public inputs on a trade batch")
	}

	su, bc, pb, err := buildTradeSubmitBatchProofFlags(sampleTradeInput())
	if err != nil {
		t.Fatalf("build trade flags: %v", err)
	}
	// settlementUpdate JSON must carry trades[] as chain-shaped SettlementTrade
	// (TRD-D2): camelCase keys + both order nullifiers (two-per-fill, AGR-2b).
	for _, want := range []string{
		`"trades"`, `"tradeId"`, `"orderNullifier"`, `"owner"`, `"side"`, `"baseQty"`, `"quoteQty"`,
		"0xt1-buy", "0xt1-sell", "0xnullbuy", "0xnullsell",
	} {
		if !strings.Contains(su, want) {
			t.Fatalf("settlementUpdate missing %q: %s", want, su)
		}
	}
	// A raw Fill would leak Fill-only keys — the chain cannot parse those as Trade.
	for _, leak := range []string{"makerOrderHash", "takerOrderHash", `"buyer"`, `"seller"`} {
		if strings.Contains(su, leak) {
			t.Fatalf("settlementUpdate leaked Fill key %q (must be Trade-shaped): %s", leak, su)
		}
	}
	if !strings.Contains(bc, "0xtrades") || !strings.Contains(bc, "0xorders") {
		t.Fatalf("batchCommitments missing trade roots: %s", bc)
	}
	// proofBundle carries all 8 public inputs, in order.
	var got chainProofBundle
	if err := json.Unmarshal(pb, &got); err != nil {
		t.Fatalf("decode proofBundle: %v", err)
	}
	if len(got.PublicInputs) != 8 || got.PublicInputs[6] != "0xtrades" || got.PublicInputs[7] != "0xorders" {
		t.Fatalf("proofBundle public inputs wrong: %+v", got.PublicInputs)
	}
}

// The Cosmos relayer signs + broadcasts a trade batch via the SAME
// submit-batch-proof command and reports accepted on code 0.
func TestCosmosSubmitTradeBatchAccepted(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"txhash":"ABC123","code":0,"raw_log":""}`)}
	c := NewCosmosClient(CosmosConfig{ChainID: "zkdex", From: "relayer"}, runner)

	res, err := c.SubmitTradeBatch(context.Background(), sampleTradeInput())
	if err != nil {
		t.Fatalf("SubmitTradeBatch: %v", err)
	}
	if !res.Accepted || res.TxHash != "ABC123" {
		t.Fatalf("result = %+v, want accepted ABC123", res)
	}
	// It used the same submit-batch-proof command (no new message).
	if got := strings.Join(runner.gotArgs, " "); !strings.Contains(got, "submit-batch-proof") {
		t.Fatalf("did not call submit-batch-proof: %v", runner.gotArgs)
	}
}

// A CheckTx rejection (non-zero code) surfaces as (accepted=false, error) — the
// signal INT-T06 uses to roll back.
func TestCosmosSubmitTradeBatchRejected(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"txhash":"DEF456","code":18,"raw_log":"invalid proof"}`)}
	c := NewCosmosClient(CosmosConfig{ChainID: "zkdex", From: "relayer"}, runner)

	res, err := c.SubmitTradeBatch(context.Background(), sampleTradeInput())
	if err == nil {
		t.Fatal("expected rejection error")
	}
	if res.Accepted {
		t.Fatal("rejected batch must not be accepted")
	}
	if res.ProofStatus != "rejected" {
		t.Fatalf("proofStatus = %q, want rejected", res.ProofStatus)
	}
}

// The local relayer accepts a well-formed 8-input trade batch and rejects a
// wrong input count.
func TestLocalSubmitTradeBatch(t *testing.T) {
	c := NewLocalClient()
	res, err := c.SubmitTradeBatch(context.Background(), sampleTradeInput())
	if err != nil || !res.Accepted || res.TxHash == "" {
		t.Fatalf("local trade submit = (%+v, %v), want accepted", res, err)
	}

	bad := sampleTradeInput()
	bad.ProofBundle.PublicInputs = bad.ProofBundle.PublicInputs[:6]
	if _, err := c.SubmitTradeBatch(context.Background(), bad); err == nil {
		t.Fatal("expected error for 6-input trade batch")
	}
}
