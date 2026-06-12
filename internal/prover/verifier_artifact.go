package prover

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

var ExpectedPublicInputNames = []string{
	"settlementUpdate.oldStateRoot",
	"settlementUpdate.newStateRoot",
	"batchCommitments.depositsRoot",
	"batchCommitments.withdrawalsRoot",
	"batchCommitments.nullifiersRoot",
	"batchCommitments.withdrawOutputsRoot",
}

// VerifierArtifactProvider is implemented by prover clients that can expose
// verifier metadata for a generated proof.
type VerifierArtifactProvider interface {
	GetVerifierArtifact(ctx context.Context) (types.VerifierArtifact, error)
}
