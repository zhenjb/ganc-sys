package indexer

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/zhenjb/ganc-sys/internal/state"
)

const defaultPollInterval = 3 * time.Second

// DepositPoller periodically reads real deposit events from a DepositEventSource
// and feeds each one into the DepositIndexer, mirroring on-chain deposits into
// the deposit store and the off-chain settlement state without a synchronous
// POST /api/deposit call.
//
// It is the asynchronous counterpart to DepositService.CreateDeposit: the same
// DepositIndexer.IndexDepositFromTx path runs, so deposits are applied
// idempotently (by depositId) regardless of which side observed the event first.
type DepositPoller struct {
	source   DepositEventSource
	indexer  *DepositIndexer
	interval time.Duration

	mu     sync.Mutex
	cursor int64
}

// NewDepositPoller builds a poller starting from startHeight (exclusive). Pass 0
// to index from genesis. interval <= 0 uses the default poll interval.
func NewDepositPoller(source DepositEventSource, indexer *DepositIndexer, startHeight int64, interval time.Duration) *DepositPoller {
	if interval <= 0 {
		interval = defaultPollInterval
	}
	return &DepositPoller{
		source:   source,
		indexer:  indexer,
		interval: interval,
		cursor:   startHeight,
	}
}

// Cursor returns the height the poller will resume from on the next poll.
func (p *DepositPoller) Cursor() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cursor
}

// PollOnce fetches deposits since the current cursor, indexes each, and advances
// the cursor. It returns the number of newly indexed deposits.
//
// Per-tx indexing failures are logged and skipped rather than aborting the whole
// poll: a single malformed event (or an already-applied replay) must not stall
// the cursor and block later deposits. The cursor only advances to the source's
// reported nextHeight, so a transient source error leaves it where it was.
func (p *DepositPoller) PollOnce(ctx context.Context) (int, error) {
	p.mu.Lock()
	from := p.cursor
	p.mu.Unlock()

	txs, nextHeight, err := p.source.FetchDepositsSince(ctx, from)
	if err != nil {
		return 0, err
	}

	indexed := 0
	for _, tx := range txs {
		record, indexErr := p.indexer.IndexDepositFromTx(ctx, tx)
		if indexErr != nil {
			if errors.Is(indexErr, state.ErrDepositAlreadyApplied) {
				// Idempotent replay — already mirrored, nothing to do.
				continue
			}
			log.Printf("[deposit-poller] skip tx %s height=%d: %v", tx.TxHash, tx.Height, indexErr)
			continue
		}
		indexed++
		log.Printf("[deposit-poller] indexed deposit %s (tx=%s height=%d)", record.DepositID, tx.TxHash, tx.Height)
	}

	p.mu.Lock()
	if nextHeight > p.cursor {
		p.cursor = nextHeight
	}
	p.mu.Unlock()

	return indexed, nil
}

// Run polls until the context is cancelled. Individual poll errors are logged and
// retried on the next tick so a flaky RPC does not kill the loop.
func (p *DepositPoller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	log.Printf("[deposit-poller] started: interval=%s startHeight=%d", p.interval, p.Cursor())

	for {
		select {
		case <-ctx.Done():
			log.Printf("[deposit-poller] stopped: %v", ctx.Err())
			return
		case <-ticker.C:
			if _, err := p.PollOnce(ctx); err != nil {
				log.Printf("[deposit-poller] poll error: %v", err)
			}
		}
	}
}
