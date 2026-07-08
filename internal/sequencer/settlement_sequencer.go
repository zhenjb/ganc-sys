// Package sequencer contains the in-process settlement "operator" — the
// background worker that drains the off-chain pending queue and settles it
// on-chain (build -> prove -> submit). It is the asynchronous counterpart to
// the deposit indexer poller: settlement is a core backend responsibility, so
// it runs inside the BE process (a goroutine started from cmd/api), NOT an
// external shell script polling the HTTP API.
//
// scripts/settle_loop.sh remains only as a manual/dev tool (ONESHOT); it must
// not run at the same time as this worker (single-writer: two drivers would
// race for the same pending operations).
package sequencer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const defaultInterval = 8 * time.Second

// ErrBatchNotAccepted is returned by SettleOnce when the chain accepted the
// submit call but rejected the batch (Accepted=false).
var ErrBatchNotAccepted = errors.New("settlement sequencer: batch submit not accepted")

// BatchDriver is the batch-build/submit surface the sequencer needs.
// *service.BatchService satisfies it. In pending build mode BuildBatch ignores
// its request body and auto-collects the pending queue.
type BatchDriver interface {
	BuildBatch(ctx context.Context, req types.BuildBatchRequestBody) (types.BuildBatchResponse, error)
	SubmitBatch(ctx context.Context, req types.SubmitBatchRequestBody) (types.SubmitBatchResponse, error)
}

// ProofDriver is the prove surface. *service.ProofService satisfies it.
type ProofDriver interface {
	GenerateProof(ctx context.Context, req types.GenerateProofRequestBody) (types.GenerateProofResponse, error)
}

// RecoveryDriver reopens an included-but-unsettled batch so its operations
// return to the pending queue. *service.OffchainSettlementService satisfies it.
// Without this, a prove/submit failure would strand the ops in the `included`
// state forever (never pending, never committed) and the user could not claim.
type RecoveryDriver interface {
	CancelIncludedBatch(ctx context.Context, batchID string, reason string) error
}

// SettlementSequencer drains the off-chain pending queue on an interval and
// settles it on-chain in-process, with self-healing recovery on failure.
type SettlementSequencer struct {
	batch    BatchDriver
	proof    ProofDriver
	recovery RecoveryDriver
	interval time.Duration
}

// New builds a sequencer. interval <= 0 uses the default. recovery may be nil,
// in which case a failed batch is not reopened (logged only).
func New(batch BatchDriver, proof ProofDriver, recovery RecoveryDriver, interval time.Duration) *SettlementSequencer {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &SettlementSequencer{
		batch:    batch,
		proof:    proof,
		recovery: recovery,
		interval: interval,
	}
}

// SettleOnce runs a single build -> prove -> submit pass.
//
// Return values:
//   - (false, nil): idle — nothing was pending.
//   - (true, nil):  one batch was built, proven, and accepted on-chain.
//   - (false, err): a stage failed. On a prove/submit failure the batch's
//     operations are reopened (CancelIncludedBatch) so the next pass rebuilds
//     them; this mirrors the recovery behaviour of scripts/settle_loop.sh.
//
// The build step (in pending mode) has already MarkIncluded'd the operations,
// so any later failure must reopen them or they are stranded.
func (s *SettlementSequencer) SettleOnce(ctx context.Context) (bool, error) {
	build, err := s.batch.BuildBatch(ctx, types.BuildBatchRequestBody{})
	if err != nil {
		if errors.Is(err, service.ErrNoPendingSettlementOperations) {
			return false, nil // idle
		}
		return false, fmt.Errorf("build: %w", err)
	}

	batchID := build.SettlementUpdate.BatchID

	proof, err := s.proof.GenerateProof(ctx, types.GenerateProofRequestBody{
		SettlementUpdate: build.SettlementUpdate,
		BatchCommitments: build.BatchCommitments,
		Witness:          build.Witness,
	})
	if err != nil {
		s.reopen(ctx, batchID, "prove failed")
		return false, fmt.Errorf("prove batch %s: %w", batchID, err)
	}

	submit, err := s.batch.SubmitBatch(ctx, types.SubmitBatchRequestBody{
		SettlementUpdate: build.SettlementUpdate,
		BatchCommitments: build.BatchCommitments,
		ProofBundle:      proof.ProofBundle,
	})
	if err != nil {
		s.reopen(ctx, batchID, "submit failed")
		return false, fmt.Errorf("submit batch %s: %w", batchID, err)
	}
	if !submit.Accepted {
		s.reopen(ctx, batchID, "submit not accepted")
		return false, fmt.Errorf("%w: batch %s (%s)", ErrBatchNotAccepted, batchID, submit.ProofStatus)
	}

	log.Printf("[settlement-sequencer] SETTLED batch=%s txHash=%s", batchID, submit.TxHash)
	return true, nil
}

// reopen returns a failed batch's operations to the pending queue. Failures to
// reopen are logged but not fatal to the tick — the next pass retries.
func (s *SettlementSequencer) reopen(ctx context.Context, batchID string, reason string) {
	if s.recovery == nil {
		return
	}
	if err := s.recovery.CancelIncludedBatch(ctx, batchID, reason); err != nil {
		log.Printf("[settlement-sequencer] reopen batch=%s failed: %v", batchID, err)
		return
	}
	log.Printf("[settlement-sequencer] reopened batch=%s (%s)", batchID, reason)
}

// Run drains and settles on an interval until ctx is cancelled.
//
// On each tick it settles repeatedly until the queue is idle (or a stage
// errors), so a burst of N pending operations clears within one tick instead of
// taking N ticks. A per-pass error breaks the drain; the next tick retries
// (the operations were reopened, so they are picked up again).
func (s *SettlementSequencer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	log.Printf("[settlement-sequencer] started: interval=%s", s.interval)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[settlement-sequencer] stopped: %v", ctx.Err())
			return
		case <-ticker.C:
			s.drain(ctx)
		}
	}
}

// drain settles batches back-to-back until the queue is idle, a stage errors,
// or the context is cancelled.
func (s *SettlementSequencer) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		settled, err := s.SettleOnce(ctx)
		if err != nil {
			log.Printf("[settlement-sequencer] %v", err)
			return
		}
		if !settled {
			return // idle
		}
	}
}
