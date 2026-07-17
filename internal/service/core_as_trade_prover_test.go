package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/prover"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func hex32(b byte) string {
	return "0x" + strings.Repeat(string("0123456789abcdef"[b%16]), 64)
}

func coreInput() prover.GenerateProofInput {
	return prover.GenerateProofInput{
		SettlementUpdate: types.SettlementUpdate{
			BatchID:      "batch-core-1",
			OldStateRoot: hex32(1),
			NewStateRoot: hex32(2),
			Deposits: []types.SettlementDeposit{
				{DepositID: "dep-1", Owner: "alice", Denom: "uusdc", Amount: "500000"},
			},
		},
		BatchCommitments: types.BatchCommitments{
			DepositsRoot:        hex32(3),
			WithdrawalsRoot:     hex32(4),
			NullifiersRoot:      hex32(5),
			WithdrawOutputsRoot: hex32(6),
		},
		Witness: types.Witness{
			Accounts: []types.WitnessAccount{
				{Owner: "alice", Denom: "uusdc", OldBalance: "0", NewBalance: "500000"},
			},
		},
	}
}

func TestBuildCoreCellsDepositIsCredit(t *testing.T) {
	cells, err := buildCoreCells([]types.WitnessAccount{
		{Owner: "alice", Denom: "uusdc", OldBalance: "0", NewBalance: "500000"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cells) != 1 {
		t.Fatalf("cells = %d, want 1", len(cells))
	}
	c := cells[0]
	if c.Owner != "alice" || c.Denom != "uusdc" || c.OldBalance != "0" || c.DeltaIn != "500000" || c.DeltaOut != "0" {
		t.Fatalf("deposit cell wrong: %+v", c)
	}
}

func TestBuildCoreCellsWithdrawIsDebit(t *testing.T) {
	cells, err := buildCoreCells([]types.WitnessAccount{
		{Owner: "bob", Denom: "uatom", OldBalance: "5000", NewBalance: "2000"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := cells[0]
	if c.DeltaIn != "0" || c.DeltaOut != "3000" || c.OldBalance != "5000" {
		t.Fatalf("withdraw cell wrong: %+v", c)
	}
}

func TestBuildCoreCellsRejectsTooMany(t *testing.T) {
	accts := make([]types.WitnessAccount, maxCoreCells+1)
	for i := range accts {
		accts[i] = types.WitnessAccount{Owner: "o", Denom: "d", OldBalance: "0", NewBalance: "1"}
	}
	if _, err := buildCoreCells(accts); err == nil {
		t.Fatalf("expected error for %d accounts (max %d)", len(accts), maxCoreCells)
	}
}

func TestBuildCoreAsTradeRequestShapeAndSentinels(t *testing.T) {
	req, pis, err := buildCoreAsTradeRequest(coreInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pis) != batch.PublicInputCountWithTrades {
		t.Fatalf("public inputs = %d, want %d", len(pis), batch.PublicInputCountWithTrades)
	}
	// [0..5] from the core builder, [6]/[7] = chain sentinel.
	if pis[0] != hex32(1) || pis[1] != hex32(2) {
		t.Fatalf("state roots not carried: [0]=%s [1]=%s", pis[0], pis[1])
	}
	if pis[batch.PublicInputIdxTradesRoot] != coreEmptyTradeRootSentinel ||
		pis[batch.PublicInputIdxOrdersRoot] != coreEmptyTradeRootSentinel {
		t.Fatalf("trade roots not sentinel: [6]=%s [7]=%s",
			pis[batch.PublicInputIdxTradesRoot], pis[batch.PublicInputIdxOrdersRoot])
	}
	// Fixed circuit shape.
	if len(req.Orders) != 2 || len(req.Fills) != 1 {
		t.Fatalf("shape wrong: orders=%d fills=%d", len(req.Orders), len(req.Fills))
	}
	if req.TradesRoot != coreEmptyTradeRootSentinel || req.OrdersRoot != coreEmptyTradeRootSentinel {
		t.Fatalf("request trade roots not sentinel")
	}
	if len(req.Cells) != 1 || req.Cells[0].DeltaIn != "500000" {
		t.Fatalf("cells wrong: %+v", req.Cells)
	}
}

func TestBuildCoreAsTradeRequestRejectsTradeBatch(t *testing.T) {
	in := coreInput()
	in.SettlementUpdate.Trades = []types.SettlementTrade{{TradeID: "t1"}}
	if _, _, err := buildCoreAsTradeRequest(in); err == nil {
		t.Fatalf("expected error for a batch carrying trades")
	}
}

// mockGazk echoes a proof bundle whose public inputs the test computed, so
// GenerateProof's byte-exact drift guard passes.
func mockGazk(t *testing.T, pis []string, vkID string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/prove", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(gazkProveResponse{ProofBundle: gazkProofBundle{
			Proof: "0xabcdef", PublicInputs: pis, VerificationKeyID: vkID,
		}})
	})
	mux.HandleFunc("/verify-trade", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(gazkVerifyResponse{Valid: true})
	})
	mux.HandleFunc("/trade-verifier-artifact", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(types.VerifierArtifact{
			VerificationKeyID: unifiedTradeVKID,
			Curve:             "BN254",
			Backend:           "groth16",
			PublicInputCount:  batch.PublicInputCountWithTrades,
			PublicInputNames:  prover.ExpectedPublicInputNamesWithTrades,
			VerifyingKey:      "0x91d1",
		})
	})
	return httptest.NewServer(mux)
}

func TestCoreAsTradeProverGenerateProofReturnsUnifiedBundle(t *testing.T) {
	_, pis, err := buildCoreAsTradeRequest(coreInput())
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	srv := mockGazk(t, pis, unifiedTradeVKID)
	defer srv.Close()

	p := NewCoreAsTradeProver(srv.URL)
	bundle, err := p.GenerateProof(context.Background(), coreInput())
	if err != nil {
		t.Fatalf("GenerateProof: %v", err)
	}
	if bundle.VerificationKeyID != unifiedTradeVKID {
		t.Fatalf("vkId = %q, want %q", bundle.VerificationKeyID, unifiedTradeVKID)
	}
	if len(bundle.PublicInputs) != batch.PublicInputCountWithTrades {
		t.Fatalf("public inputs = %d, want 8", len(bundle.PublicInputs))
	}
}

func TestCoreAsTradeProverRejectsWrongVKID(t *testing.T) {
	_, pis, _ := buildCoreAsTradeRequest(coreInput())
	srv := mockGazk(t, pis, "gazk-balance-smoke-v1") // wrong circuit
	defer srv.Close()

	p := NewCoreAsTradeProver(srv.URL)
	if _, err := p.GenerateProof(context.Background(), coreInput()); err == nil {
		t.Fatalf("expected error when gazk returns the wrong vkId")
	}
}

func TestCoreAsTradeProverDetectsPublicInputDrift(t *testing.T) {
	_, pis, _ := buildCoreAsTradeRequest(coreInput())
	drift := append([]string(nil), pis...)
	drift[1] = hex32(9) // gazk bound a different newStateRoot
	srv := mockGazk(t, drift, unifiedTradeVKID)
	defer srv.Close()

	p := NewCoreAsTradeProver(srv.URL)
	if _, err := p.GenerateProof(context.Background(), coreInput()); err == nil {
		t.Fatalf("expected drift error")
	}
}

func TestCoreAsTradeProverVerifyAndArtifact(t *testing.T) {
	_, pis, _ := buildCoreAsTradeRequest(coreInput())
	srv := mockGazk(t, pis, unifiedTradeVKID)
	defer srv.Close()

	p := NewCoreAsTradeProver(srv.URL)

	if err := p.Verify(context.Background(), prover.VerifyProofInput{
		SettlementUpdate: coreInput().SettlementUpdate,
		ProofBundle:      types.ProofBundle{Proof: "0xabcdef", PublicInputs: pis, VerificationKeyID: unifiedTradeVKID},
	}); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	art, err := p.GetVerifierArtifact(context.Background())
	if err != nil {
		t.Fatalf("GetVerifierArtifact: %v", err)
	}
	if art.VerificationKeyID != unifiedTradeVKID || art.PublicInputCount != batch.PublicInputCountWithTrades {
		t.Fatalf("artifact wrong: %+v", art)
	}
}
