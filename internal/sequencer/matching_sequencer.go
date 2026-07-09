package sequencer

import (
	"context"
	"log"
	"time"
)

const defaultMatchingInterval = 2 * time.Second

// Matcher is the matching-trigger surface the matching sequencer drives.
// *service.RealOrderService satisfies it: RunMatchingOnce matches every market
// once under the order service's single-writer lock and returns the number of
// fills produced.
type Matcher interface {
	RunMatchingOnce() (int, error)
}

// MatchingSequencer periodically runs the matching engine over all markets
// (INT-T05). It is the backstop to the synchronous match POST /api/order already
// performs on insert: the tick catches any crossing missed by an event (e.g. a
// market config change) and keeps the book converging. It shares the order
// service's matchMu, so it never runs concurrently with an insert-time match —
// preserving the single-writer determinism the ZK proof relies on.
type MatchingSequencer struct {
	matcher  Matcher
	interval time.Duration
}

// NewMatchingSequencer builds the sequencer. interval <= 0 uses the default.
func NewMatchingSequencer(matcher Matcher, interval time.Duration) *MatchingSequencer {
	if interval <= 0 {
		interval = defaultMatchingInterval
	}
	return &MatchingSequencer{matcher: matcher, interval: interval}
}

// Run matches all markets on an interval until ctx is cancelled. A match error
// is logged and the next tick retries; idle ticks (no fills) are silent.
func (s *MatchingSequencer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	log.Printf("[matching-sequencer] started: interval=%s", s.interval)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[matching-sequencer] stopped: %v", ctx.Err())
			return
		case <-ticker.C:
			n, err := s.matcher.RunMatchingOnce()
			if err != nil {
				log.Printf("[matching-sequencer] %v", err)
				continue
			}
			if n > 0 {
				log.Printf("[matching-sequencer] produced %d fill(s)", n)
			}
		}
	}
}
