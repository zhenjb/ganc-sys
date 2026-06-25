package service

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/prover"
	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// recordingRelayer records whether SubmitBatch was invoked so tests can assert
// that an invalid proof never reaches the relayer / state update.
type recordingRelayer struct {
	submitCalled bool
}

func (r *recordingRelayer) SubmitBatch(ctx context.Context, input relayer.SubmitBatchInput) (relayer.SubmitBatchResult, error) {
	r.submitCalled = true
	return relayer.SubmitBatchResult{
		TxHash:      "0xtx",
		Accepted:    true,
		ProofStatus: "accepted",
	}, nil
}

func (r *recordingRelayer) ClaimWithdraw(ctx context.Context, input relayer.ClaimWithdrawInput) (relayer.ClaimWithdrawResult, error) {
	return relayer.ClaimWithdrawResult{}, nil
}

type stubVerifier struct {
	err    error
	called bool
}

func (v *stubVerifier) Verify(ctx context.Context, input prover.VerifyProofInput) error {
	v.called = true
	return v.err
}

func newBatchServiceForVerifyTest(rel relayer.Client) *BatchService {
	memoryStore := store.NewMemoryStore()
	return NewBatchService(
		repository.NewBatchRepository(memoryStore),
		repository.NewDepositRepository(memoryStore),
		repository.NewWithdrawRepository(memoryStore),
		batch.NewLocalBuilder(),
		rel,
	)
}

func sampleSubmitRequest() types.SubmitBatchRequestBody {
	return types.SubmitBatchRequestBody{
		SettlementUpdate: types.SettlementUpdate{
			BatchID:      "batch-1",
			OldStateRoot: "0xrootA",
			NewStateRoot: "0xrootB",
		},
		BatchCommitments: types.BatchCommitments{
			DepositsRoot:        "0xdepositsRoot",
			WithdrawalsRoot:     "0xwithdrawalsRoot",
			NullifiersRoot:      "0xnullifiersRoot",
			WithdrawOutputsRoot: "0xwithdrawOutputsRoot",
		},
		ProofBundle: types.ProofBundle{
			Proof: "0xrealproof",
			PublicInputs: []string{
				"0xrootA", "0xrootB", "0xdepositsRoot",
				"0xwithdrawalsRoot", "0xnullifiersRoot", "0xwithdrawOutputsRoot",
			},
			VerificationKeyID: "gazk-balance-smoke-v1",
		},
	}
}

func TestSubmitBatchAcceptsWhenVerifierPasses(t *testing.T) {
	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)
	verifier := &stubVerifier{err: nil}
	svc.SetProofVerifier(verifier)

	resp, err := svc.SubmitBatch(context.Background(), sampleSubmitRequest())
	if err != nil {
		t.Fatalf("submit batch: %v", err)
	}

	if !verifier.called {
		t.Fatalf("expected verifier to be called")
	}
	if !rel.submitCalled {
		t.Fatalf("expected relayer to be called after valid proof")
	}
	if !resp.Accepted {
		t.Fatalf("expected accepted=true")
	}
	if resp.State.CurrentStateRoot != "0xrootB" {
		t.Fatalf("expected root advanced to 0xrootB, got %q", resp.State.CurrentStateRoot)
	}
}

func TestSubmitBatchRejectsWhenVerifierFails(t *testing.T) {
	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)
	verifier := &stubVerifier{err: errors.New("Groth16 verification failed")}
	svc.SetProofVerifier(verifier)

	_, err := svc.SubmitBatch(context.Background(), sampleSubmitRequest())
	if err == nil {
		t.Fatalf("expected submit to fail when proof is invalid")
	}
	if !errors.Is(err, ErrProofVerificationFailed) {
		t.Fatalf("expected ErrProofVerificationFailed, got %v", err)
	}
	if rel.submitCalled {
		t.Fatalf("relayer must not be called when proof verification fails")
	}
}

func TestSubmitBatchSkipsVerificationWhenNoVerifier(t *testing.T) {
	rel := &recordingRelayer{}
	svc := newBatchServiceForVerifyTest(rel)

	resp, err := svc.SubmitBatch(context.Background(), sampleSubmitRequest())
	if err != nil {
		t.Fatalf("submit batch: %v", err)
	}
	if !rel.submitCalled {
		t.Fatalf("expected relayer to be called in mock mode")
	}
	if !resp.Accepted {
		t.Fatalf("expected accepted=true in mock mode")
	}
}
