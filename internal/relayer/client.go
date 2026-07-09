package relayer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// Client is the integration boundary for settlement and withdraw chain actions.
//
// P4 owns:
// - API integration,
// - calling this interface,
// - returning tx/result to FE.
//
// P1 owns the real on-chain implementation:
// - MsgSubmitBatchProof,
// - MsgClaimWithdraw,
// - proof verification,
// - currentStateRoot update,
// - depositProcessed update,
// - nullifierUsed update,
// - WithdrawRecord creation,
// - module-account fund transfer.
type Client interface {
	SubmitBatch(ctx context.Context, input SubmitBatchInput) (SubmitBatchResult, error)
	ClaimWithdraw(ctx context.Context, input ClaimWithdrawInput) (ClaimWithdrawResult, error)
}

// TradeClient extends the relayer with the trade batch submit (INT-T08). It
// reuses the SAME MsgSubmitBatchProof / submit-batch-proof command, but the
// SettlementUpdate carries trades[] and the ProofBundle carries the 8 public
// inputs ([0..5] core + [6]=tradesRoot + [7]=ordersRoot). Both LocalClient and
// CosmosClient implement it, so the trade settlement pipeline (INT-T06) submits
// through the same signer/broadcast machinery as the core path. No new message,
// no bank transfer — the chain only verifies the proof and commits the new root.
type TradeClient interface {
	SubmitTradeBatch(ctx context.Context, input SubmitBatchInput) (SubmitBatchResult, error)
}

type SubmitBatchInput struct {
	SettlementUpdate types.SettlementUpdate
	BatchCommitments types.BatchCommitments
	ProofBundle      types.ProofBundle
}

type SubmitBatchResult struct {
	TxHash          string
	Accepted        bool
	ProofStatus     string
	WithdrawRecords []types.WithdrawRecord
}

type ClaimWithdrawInput struct {
	WithdrawRecord types.WithdrawRecord
}

type ClaimWithdrawResult struct {
	TxHash         string
	WithdrawRecord types.WithdrawRecord
}

// LocalClient is a temporary deterministic relayer placeholder.
//
// TODO(P1):
// Replace this with a real Cosmos relayer/client that submits:
// - MsgSubmitBatchProof
// - MsgClaimWithdraw
//
// This local implementation does not verify a real ZK proof and does not
// transfer real funds. It only preserves API shape and validates contract-level
// consistency.
type LocalClient struct{}

func NewLocalClient() *LocalClient {
	return &LocalClient{}
}

var (
	_ Client      = (*LocalClient)(nil)
	_ TradeClient = (*LocalClient)(nil)
)

func (c *LocalClient) SubmitBatch(ctx context.Context, input SubmitBatchInput) (SubmitBatchResult, error) {
	if err := validatePublicInputs(input); err != nil {
		return SubmitBatchResult{}, err
	}

	withdrawRecords := make([]types.WithdrawRecord, 0, len(input.SettlementUpdate.Withdrawals))
	for _, withdrawal := range input.SettlementUpdate.Withdrawals {
		withdrawRecords = append(withdrawRecords, types.WithdrawRecord{
			WithdrawID:  withdrawal.WithdrawID,
			Owner:       withdrawal.Owner,
			Denom:       withdrawal.Denom,
			Amount:      withdrawal.Amount,
			Destination: withdrawal.Destination,
			Nullifier:   withdrawal.Nullifier,
			Claimed:     false,
		})
	}

	txHash := hashJSON("local-submit-batch", map[string]any{
		"settlementUpdate": input.SettlementUpdate,
		"batchCommitments": input.BatchCommitments,
		"proofBundle":      input.ProofBundle,
	})

	return SubmitBatchResult{
		TxHash:          txHash,
		Accepted:        true,
		ProofStatus:     "accepted",
		WithdrawRecords: withdrawRecords,
	}, nil
}

// SubmitTradeBatch is the local (no-chain) trade submit (INT-T08). It validates
// the 8-input layout and that the first two inputs bind the settlement roots (the
// same append-not-reorder consistency the chain verifier enforces), then accepts
// with a deterministic tx hash. It transfers no funds and verifies no real proof.
func (c *LocalClient) SubmitTradeBatch(ctx context.Context, input SubmitBatchInput) (SubmitBatchResult, error) {
	if input.SettlementUpdate.BatchID == "" {
		return SubmitBatchResult{}, fmt.Errorf("settlementUpdate is required")
	}
	if input.ProofBundle.Proof == "" {
		return SubmitBatchResult{}, fmt.Errorf("proofBundle is required")
	}
	if len(input.ProofBundle.PublicInputs) != 8 {
		return SubmitBatchResult{}, fmt.Errorf("trade proof public inputs must contain 8 values")
	}
	if input.ProofBundle.PublicInputs[0] != input.SettlementUpdate.OldStateRoot ||
		input.ProofBundle.PublicInputs[1] != input.SettlementUpdate.NewStateRoot {
		return SubmitBatchResult{}, fmt.Errorf("trade proof public inputs do not bind the settlement roots")
	}

	txHash := hashJSON("local-submit-trade-batch", map[string]any{
		"settlementUpdate": input.SettlementUpdate,
		"batchCommitments": input.BatchCommitments,
		"proofBundle":      input.ProofBundle,
	})
	return SubmitBatchResult{
		TxHash:      txHash,
		Accepted:    true,
		ProofStatus: "accepted",
	}, nil
}

func (c *LocalClient) ClaimWithdraw(ctx context.Context, input ClaimWithdrawInput) (ClaimWithdrawResult, error) {
	if input.WithdrawRecord.WithdrawID == "" {
		return ClaimWithdrawResult{}, fmt.Errorf("withdrawRecord is required")
	}

	if input.WithdrawRecord.Claimed {
		return ClaimWithdrawResult{}, fmt.Errorf("withdraw already claimed")
	}

	claimedRecord := input.WithdrawRecord
	claimedRecord.Claimed = true

	txHash := hashJSON("local-claim-withdraw", claimedRecord)

	return ClaimWithdrawResult{
		TxHash:         txHash,
		WithdrawRecord: claimedRecord,
	}, nil
}

func validatePublicInputs(input SubmitBatchInput) error {
	if input.SettlementUpdate.BatchID == "" {
		return fmt.Errorf("settlementUpdate is required")
	}

	if input.ProofBundle.Proof == "" {
		return fmt.Errorf("proofBundle is required")
	}

	if len(input.ProofBundle.PublicInputs) != 6 {
		return fmt.Errorf("proof public inputs must contain 6 values")
	}

	expected := []string{
		input.SettlementUpdate.OldStateRoot,
		input.SettlementUpdate.NewStateRoot,
		input.BatchCommitments.DepositsRoot,
		input.BatchCommitments.WithdrawalsRoot,
		input.BatchCommitments.NullifiersRoot,
		input.BatchCommitments.WithdrawOutputsRoot,
	}

	for i := range expected {
		if input.ProofBundle.PublicInputs[i] != expected[i] {
			return fmt.Errorf("proof public inputs do not match settlement")
		}
	}

	return nil
}

func hashJSON(label string, value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return localHash(label, "marshal-error")
	}

	return localHash(label, string(raw))
}

func localHash(parts ...string) string {
	h := sha256.New()

	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}

	return "0x" + hex.EncodeToString(h.Sum(nil))
}
