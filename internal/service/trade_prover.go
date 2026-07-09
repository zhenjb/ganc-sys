package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// TradeProver and TradeSubmitter are the prove/submit seams of the trade batch
// pipeline (INT-T06). Wave 1 uses the local 8-input stubs below; Wave 2 swaps in
// A's real gazk trade prover (ZK-T10) and the real relayer trade submit
// (INT-T08) via SetTradeSettlement — without touching the settle orchestration.
//
// NOTE: the core prover/relayer LocalClients are hardcoded to the 6 core public
// inputs and CANNOT carry the trade roots [6]/[7]; the trade path therefore has
// its own 8-input stubs rather than reusing them.
type TradeProver interface {
	ProveTrade(ctx context.Context, upd types.SettlementUpdate, com types.BatchCommitments, witness types.Witness, publicInputs []string) (types.ProofBundle, error)
}

// TradeSubmitter submits a proven trade batch to the chain (MsgSubmitBatchProof).
// It returns the tx hash and whether the chain accepted the batch.
type TradeSubmitter interface {
	SubmitTrade(ctx context.Context, upd types.SettlementUpdate, com types.BatchCommitments, proof types.ProofBundle) (txHash string, accepted bool, err error)
}

// LocalTradeProver is the Wave-1 stub prover. It does NOT produce a real ZK
// proof; it validates the 8-input layout and returns a deterministic placeholder
// bundle carrying exactly those public inputs, so the rest of the pipeline
// (submit, verify, commit) can be integrated before A's real trade circuit lands.
type LocalTradeProver struct{}

// NewLocalTradeProver returns the stub prover.
func NewLocalTradeProver() *LocalTradeProver { return &LocalTradeProver{} }

var _ TradeProver = (*LocalTradeProver)(nil)

func (p *LocalTradeProver) ProveTrade(_ context.Context, upd types.SettlementUpdate, com types.BatchCommitments, witness types.Witness, publicInputs []string) (types.ProofBundle, error) {
	if len(publicInputs) != batch.PublicInputCountWithTrades {
		return types.ProofBundle{}, fmt.Errorf("trade prover: expected %d public inputs, got %d", batch.PublicInputCountWithTrades, len(publicInputs))
	}
	if upd.OldStateRoot == "" || upd.NewStateRoot == "" {
		return types.ProofBundle{}, fmt.Errorf("trade prover: settlement roots required")
	}
	proof := localTradeHash("local-trade-proof", map[string]any{
		"settlementUpdate": upd,
		"batchCommitments": com,
		"witness":          witness,
		"publicInputs":     publicInputs,
	})
	return types.ProofBundle{
		Proof:             proof,
		PublicInputs:      append([]string(nil), publicInputs...),
		VerificationKeyID: "local-trade-v1",
	}, nil
}

// LocalTradeSubmitter is the Wave-1 stub submitter. It re-checks that the proof's
// 8 public inputs bind the batch's roots (the exact consistency the chain
// verifier will enforce), then "accepts" and returns a deterministic tx hash. It
// transfers NO funds and verifies no real proof — the chain only commits the new
// state root; there is no x/bank movement for trades (plan pitfall).
type LocalTradeSubmitter struct{}

// NewLocalTradeSubmitter returns the stub submitter.
func NewLocalTradeSubmitter() *LocalTradeSubmitter { return &LocalTradeSubmitter{} }

var _ TradeSubmitter = (*LocalTradeSubmitter)(nil)

func (s *LocalTradeSubmitter) SubmitTrade(_ context.Context, upd types.SettlementUpdate, com types.BatchCommitments, proof types.ProofBundle) (string, bool, error) {
	if upd.BatchID == "" {
		return "", false, fmt.Errorf("trade submitter: settlementUpdate is required")
	}
	if proof.Proof == "" {
		return "", false, fmt.Errorf("trade submitter: proofBundle is required")
	}
	// Recompute the expected 8-input vector and match it against the proof — the
	// same append-not-reorder binding the on-chain verifier checks.
	expected, err := batch.BuildPublicInputsWithTrades(upd, com)
	if err != nil {
		return "", false, fmt.Errorf("trade submitter: rebuild public inputs: %w", err)
	}
	if len(proof.PublicInputs) != len(expected) {
		return "", false, fmt.Errorf("trade submitter: proof has %d public inputs, want %d", len(proof.PublicInputs), len(expected))
	}
	for i := range expected {
		if proof.PublicInputs[i] != expected[i] {
			return "", false, fmt.Errorf("trade submitter: public input %d mismatch (%q != %q)", i, proof.PublicInputs[i], expected[i])
		}
	}
	txHash := localTradeHash("local-trade-submit", map[string]any{
		"settlementUpdate": upd,
		"batchCommitments": com,
		"proofBundle":      proof,
	})
	return txHash, true, nil
}

func localTradeHash(label string, value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte("marshal-error")
	}
	h := sha256.New()
	h.Write([]byte(label))
	h.Write([]byte("|"))
	h.Write(raw)
	return "0x" + hex.EncodeToString(h.Sum(nil))
}
