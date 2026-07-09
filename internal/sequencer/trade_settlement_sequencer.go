package sequencer

import (
	"context"
	"log"
	"time"
)

const defaultTradeSettlementInterval = 4 * time.Second

// TradeSettler is the trade-batch settle surface the sequencer drives.
// *service.RealOrderService satisfies it: SettleTradesOnce drains the fill queue
// (INT-T05) and settles it via build → prove → submit (INT-T06), returning
// whether anything settled.
type TradeSettler interface {
	SettleTradesOnce(ctx context.Context) (bool, error)
}

// TradeSettlementSequencer drains matched fills and settles them on-chain on an
// interval (INT-T06) — the trade counterpart of the deposit/withdraw
// SettlementSequencer, sharing the same build→prove→submit shape. Self-healing:
// SettleTradesOnce rolls back + re-enqueues on a transient failure, so the next
// tick retries.
type TradeSettlementSequencer struct {
	settler  TradeSettler
	interval time.Duration
}

// NewTradeSettlementSequencer builds the sequencer. interval <= 0 uses the default.
func NewTradeSettlementSequencer(settler TradeSettler, interval time.Duration) *TradeSettlementSequencer {
	if interval <= 0 {
		interval = defaultTradeSettlementInterval
	}
	return &TradeSettlementSequencer{settler: settler, interval: interval}
}

// Run settles on an interval until ctx is cancelled. Each tick drains
// back-to-back until the queue is idle or a stage errors (the erroring batch was
// re-enqueued for the next tick).
func (s *TradeSettlementSequencer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	log.Printf("[trade-settlement-sequencer] started: interval=%s", s.interval)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[trade-settlement-sequencer] stopped: %v", ctx.Err())
			return
		case <-ticker.C:
			s.drain(ctx)
		}
	}
}

func (s *TradeSettlementSequencer) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		settled, err := s.settler.SettleTradesOnce(ctx)
		if err != nil {
			log.Printf("[trade-settlement-sequencer] %v", err)
			return
		}
		if !settled {
			return // idle
		}
	}
}
