package sequencer

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// --- fakes ---------------------------------------------------------------

type buildOutcome struct {
	resp types.BuildBatchResponse
	err  error
}

type fakeBatch struct {
	builds      []buildOutcome
	buildIdx    int
	buildCalls  int
	submitResp  types.SubmitBatchResponse
	submitErr   error
	submitCalls int
}

func (f *fakeBatch) BuildBatch(_ context.Context, _ types.BuildBatchRequestBody) (types.BuildBatchResponse, error) {
	f.buildCalls++
	if f.buildIdx < len(f.builds) {
		o := f.builds[f.buildIdx]
		f.buildIdx++
		return o.resp, o.err
	}
	// Default once the script is exhausted: idle.
	return types.BuildBatchResponse{}, service.ErrNoPendingSettlementOperations
}

func (f *fakeBatch) SubmitBatch(_ context.Context, _ types.SubmitBatchRequestBody) (types.SubmitBatchResponse, error) {
	f.submitCalls++
	return f.submitResp, f.submitErr
}

type fakeProof struct {
	resp  types.GenerateProofResponse
	err   error
	calls int
}

func (f *fakeProof) GenerateProof(_ context.Context, _ types.GenerateProofRequestBody) (types.GenerateProofResponse, error) {
	f.calls++
	return f.resp, f.err
}

type fakeRecovery struct {
	calls       int
	lastBatchID string
	lastReason  string
	err         error
}

func (f *fakeRecovery) CancelIncludedBatch(_ context.Context, batchID string, reason string) error {
	f.calls++
	f.lastBatchID = batchID
	f.lastReason = reason
	return f.err
}

func buildOK(batchID string) types.BuildBatchResponse {
	return types.BuildBatchResponse{
		SettlementUpdate: types.SettlementUpdate{BatchID: batchID},
	}
}

// --- tests ---------------------------------------------------------------

func TestSettleOnceIdle(t *testing.T) {
	batch := &fakeBatch{builds: []buildOutcome{{err: service.ErrNoPendingSettlementOperations}}}
	proof := &fakeProof{}
	rec := &fakeRecovery{}
	seq := New(batch, proof, rec, 0)

	settled, err := seq.SettleOnce(context.Background())
	if err != nil {
		t.Fatalf("idle should not error: %v", err)
	}
	if settled {
		t.Fatalf("idle should return settled=false")
	}
	if proof.calls != 0 || batch.submitCalls != 0 || rec.calls != 0 {
		t.Fatalf("idle must not prove/submit/reopen: prove=%d submit=%d reopen=%d",
			proof.calls, batch.submitCalls, rec.calls)
	}
}

func TestSettleOnceSuccess(t *testing.T) {
	batch := &fakeBatch{
		builds:     []buildOutcome{{resp: buildOK("batch-1")}},
		submitResp: types.SubmitBatchResponse{Accepted: true, TxHash: "0xtx"},
	}
	proof := &fakeProof{}
	rec := &fakeRecovery{}
	seq := New(batch, proof, rec, 0)

	settled, err := seq.SettleOnce(context.Background())
	if err != nil {
		t.Fatalf("success should not error: %v", err)
	}
	if !settled {
		t.Fatalf("success should return settled=true")
	}
	if proof.calls != 1 || batch.submitCalls != 1 {
		t.Fatalf("expected one prove and one submit: prove=%d submit=%d", proof.calls, batch.submitCalls)
	}
	if rec.calls != 0 {
		t.Fatalf("success must not reopen, got %d reopen calls", rec.calls)
	}
}

func TestSettleOnceProveFailReopens(t *testing.T) {
	batch := &fakeBatch{builds: []buildOutcome{{resp: buildOK("batch-9")}}}
	proof := &fakeProof{err: errors.New("nullifier mismatch")}
	rec := &fakeRecovery{}
	seq := New(batch, proof, rec, 0)

	settled, err := seq.SettleOnce(context.Background())
	if err == nil || settled {
		t.Fatalf("prove failure should error and not settle: settled=%v err=%v", settled, err)
	}
	if batch.submitCalls != 0 {
		t.Fatalf("must not submit after prove failure")
	}
	if rec.calls != 1 || rec.lastBatchID != "batch-9" || rec.lastReason != "prove failed" {
		t.Fatalf("prove failure must reopen batch-9: calls=%d id=%q reason=%q",
			rec.calls, rec.lastBatchID, rec.lastReason)
	}
}

func TestSettleOnceSubmitErrorReopens(t *testing.T) {
	batch := &fakeBatch{
		builds:    []buildOutcome{{resp: buildOK("batch-3")}},
		submitErr: errors.New("relayer down"),
	}
	proof := &fakeProof{}
	rec := &fakeRecovery{}
	seq := New(batch, proof, rec, 0)

	settled, err := seq.SettleOnce(context.Background())
	if err == nil || settled {
		t.Fatalf("submit error should error and not settle")
	}
	if rec.calls != 1 || rec.lastBatchID != "batch-3" || rec.lastReason != "submit failed" {
		t.Fatalf("submit error must reopen batch-3: calls=%d id=%q reason=%q",
			rec.calls, rec.lastBatchID, rec.lastReason)
	}
}

func TestSettleOnceNotAcceptedReopens(t *testing.T) {
	batch := &fakeBatch{
		builds:     []buildOutcome{{resp: buildOK("batch-7")}},
		submitResp: types.SubmitBatchResponse{Accepted: false, ProofStatus: "rejected"},
	}
	proof := &fakeProof{}
	rec := &fakeRecovery{}
	seq := New(batch, proof, rec, 0)

	settled, err := seq.SettleOnce(context.Background())
	if settled || !errors.Is(err, ErrBatchNotAccepted) {
		t.Fatalf("not-accepted should return ErrBatchNotAccepted, got settled=%v err=%v", settled, err)
	}
	if rec.calls != 1 || rec.lastReason != "submit not accepted" {
		t.Fatalf("not-accepted must reopen: calls=%d reason=%q", rec.calls, rec.lastReason)
	}
}

// drain must settle back-to-back until idle, so a burst of N pending ops clears
// within a single tick rather than N ticks.
func TestDrainSettlesUntilIdle(t *testing.T) {
	batch := &fakeBatch{
		builds: []buildOutcome{
			{resp: buildOK("batch-1")},
			{resp: buildOK("batch-2")},
			{resp: buildOK("batch-3")},
			// 4th build (and beyond) falls through to idle.
		},
		submitResp: types.SubmitBatchResponse{Accepted: true, TxHash: "0xtx"},
	}
	proof := &fakeProof{}
	rec := &fakeRecovery{}
	seq := New(batch, proof, rec, 0)

	seq.drain(context.Background())

	if batch.submitCalls != 3 {
		t.Fatalf("drain should settle all 3 pending batches in one pass, got %d submits", batch.submitCalls)
	}
	if rec.calls != 0 {
		t.Fatalf("clean drain must not reopen, got %d", rec.calls)
	}
}

// A stage error mid-drain stops the drain (the failed batch was reopened; the
// next tick retries).
func TestDrainStopsOnError(t *testing.T) {
	batch := &fakeBatch{
		builds: []buildOutcome{
			{resp: buildOK("batch-1")},
			{resp: buildOK("batch-2")},
		},
		submitResp: types.SubmitBatchResponse{Accepted: true, TxHash: "0xtx"},
	}
	proof := &fakeProof{err: errors.New("prove boom")}
	rec := &fakeRecovery{}
	seq := New(batch, proof, rec, 0)

	seq.drain(context.Background())

	if batch.buildCalls != 1 {
		t.Fatalf("drain should stop after first failing pass, got %d builds", batch.buildCalls)
	}
	if rec.calls != 1 {
		t.Fatalf("expected one reopen on the failed batch, got %d", rec.calls)
	}
}
