package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// fakeTradeBatchRecorder captures SaveLatestTradeBatch calls (Nhóm 4 (c)).
type fakeTradeBatchRecorder struct {
	calls    int
	batchIDs []string
}

func (f *fakeTradeBatchRecorder) SaveLatestTradeBatch(upd types.SettlementUpdate, _ types.BatchCommitments, _ types.ProofBundle) {
	f.calls++
	f.batchIDs = append(f.batchIDs, upd.BatchID)
}

// Nhóm 4 (c): after a trade settles, the recorder is called with the trade batch so
// GET /api/state's latest* pointers can reflect it.
func TestSettleTradesRecordsLatestTradeBatch(t *testing.T) {
	svc, _ := settleSetup(t)
	rec := &fakeTradeBatchRecorder{}
	svc.SetTradeBatchRecorder(rec)

	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil || !settled {
		t.Fatalf("settle = (%v,%v), want (true,nil)", settled, err)
	}
	if rec.calls != 1 {
		t.Fatalf("recorder called %d times, want 1", rec.calls)
	}
	if rec.batchIDs[0] == "" {
		t.Fatal("recorded trade batchID empty")
	}
}

// fakeCommittedRootSink records AdvanceCommittedRoot calls; err (if set) is
// returned to prove a sink failure does not fail the (already on-chain) trade.
type fakeCommittedRootSink struct {
	roots    []string
	batchIDs []string
	err      error
}

func (f *fakeCommittedRootSink) AdvanceCommittedRoot(_ context.Context, newCommittedRoot, batchID string) error {
	f.roots = append(f.roots, newCommittedRoot)
	f.batchIDs = append(f.batchIDs, batchID)
	return f.err
}

// DB-1: after a trade settles on-chain, the committed-root sink is advanced to the
// trade's new root + batchId, keeping the core settlement cursor in lockstep so
// the next core deposit/withdraw batch does not fail to build.
func TestSettleTradesAdvancesCommittedRootSink(t *testing.T) {
	svc, mgr := settleSetup(t)
	sink := &fakeCommittedRootSink{}
	svc.SetCommittedRootSink(sink)

	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil || !settled {
		t.Fatalf("settle = (%v,%v), want (true,nil)", settled, err)
	}
	if len(sink.roots) != 1 {
		t.Fatalf("sink called %d times, want 1", len(sink.roots))
	}
	// The advanced root must be the trade's new root — i.e. the manager root after
	// the fill was applied.
	if sink.roots[0] != mgr.Root() {
		t.Fatalf("sink root = %q, want manager root %q", sink.roots[0], mgr.Root())
	}
	if sink.batchIDs[0] == "" {
		t.Fatal("sink batchID empty")
	}
}

// A sink error must NOT fail the settle: the trade is already committed on-chain,
// so the cursor-sync failure is logged, not rolled back.
func TestSettleTradesSinkErrorDoesNotFailSettle(t *testing.T) {
	svc, _ := settleSetup(t)
	svc.SetCommittedRootSink(&fakeCommittedRootSink{err: errors.New("db down")})

	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil || !settled {
		t.Fatalf("settle with failing sink = (%v,%v), want (true,nil)", settled, err)
	}
}

// Backward-compat: with no sink wired (off-chain settlement disabled), trade
// settlement behaves exactly as before.
func TestSettleTradesNoSinkStillSettles(t *testing.T) {
	svc, _ := settleSetup(t)
	settled, err := svc.SettleTradesOnce(context.Background())
	if err != nil || !settled {
		t.Fatalf("settle with no sink = (%v,%v), want (true,nil)", settled, err)
	}
}
