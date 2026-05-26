package prover

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// Client is the integration boundary for the P2 prover.
//
// P4 owns:
// - API endpoint integration,
// - request validation,
// - calling this interface,
// - returning ProofBundle.
//
// P2 owns the real implementation:
// - circuit,
// - witness interpretation,
// - proof generation,
// - proving key,
// - verification key compatibility.
type Client interface {
	GenerateProof(ctx context.Context, input GenerateProofInput) (types.ProofBundle, error)
}

type GenerateProofInput struct {
	SettlementUpdate types.SettlementUpdate
	BatchCommitments types.BatchCommitments
	Witness          types.Witness
}

// LocalClient is a temporary deterministic prover placeholder.
//
// TODO(P2):
// Replace this implementation with the real prover client.
// This local implementation only preserves API shape and public input order.
// It does not generate a real zero-knowledge proof.
type LocalClient struct{}

func NewLocalClient() *LocalClient {
	return &LocalClient{}
}

func (c *LocalClient) GenerateProof(ctx context.Context, input GenerateProofInput) (types.ProofBundle, error) {
	if input.SettlementUpdate.OldStateRoot == "" {
		return types.ProofBundle{}, fmt.Errorf("oldStateRoot is required")
	}

	if input.SettlementUpdate.NewStateRoot == "" {
		return types.ProofBundle{}, fmt.Errorf("newStateRoot is required")
	}

	if input.BatchCommitments.DepositsRoot == "" ||
		input.BatchCommitments.WithdrawalsRoot == "" ||
		input.BatchCommitments.NullifiersRoot == "" ||
		input.BatchCommitments.WithdrawOutputsRoot == "" {
		return types.ProofBundle{}, fmt.Errorf("batchCommitments are incomplete")
	}

	if len(input.Witness.Accounts) == 0 {
		return types.ProofBundle{}, fmt.Errorf("witness.accounts is required")
	}

	publicInputs := []string{
		input.SettlementUpdate.OldStateRoot,
		input.SettlementUpdate.NewStateRoot,
		input.BatchCommitments.DepositsRoot,
		input.BatchCommitments.WithdrawalsRoot,
		input.BatchCommitments.NullifiersRoot,
		input.BatchCommitments.WithdrawOutputsRoot,
	}

	proof := hashJSON("local-proof", map[string]any{
		"settlementUpdate": input.SettlementUpdate,
		"batchCommitments": input.BatchCommitments,
		"witness":          input.Witness,
		"publicInputs":     publicInputs,
	})

	return types.ProofBundle{
		Proof:             proof,
		PublicInputs:      publicInputs,
		VerificationKeyID: "local-v1",
	}, nil
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
