package prover

import (
	"context"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// Verifier is the integration boundary for real ZK proof verification.
//
// In the target architecture the on-chain x/zkdex module (P1) verifies the
// proof inside MsgSubmitBatchProof. Until that module exists, the backend
// performs the same real Groth16 verification by calling the P2 gazk
// /verify endpoint before accepting a batch. This keeps the invariant
// "currentStateRoot advances only after a valid proof" enforced by real ZK.
type Verifier interface {
	Verify(ctx context.Context, input VerifyProofInput) error
}

type VerifyProofInput struct {
	SettlementUpdate types.SettlementUpdate
	BatchCommitments types.BatchCommitments
	ProofBundle      types.ProofBundle
}
