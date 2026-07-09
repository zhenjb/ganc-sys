package service

import (
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// FillQueue holds fills produced by the matching trigger (INT-T05) that are
// waiting to be settled on-chain by the trade batch pipeline (INT-T06). The
// matcher Enqueues; the batch builder Drains. It is DISTINCT from TradeStore:
// TradeStore is the permanent fill history for GET /api/trades, whereas this is
// the transient settlement work-list that is consumed once batched.
type FillQueue interface {
	// Enqueue appends fills awaiting settlement.
	Enqueue(fills []types.Fill)
	// Drain removes and returns all pending fills (INT-T06 takes the batch).
	Drain() []types.Fill
	// Len reports how many fills await settlement.
	Len() int
}

// InMemoryFillQueue is the MVP in-memory settlement work-list, thread-safe.
// Pending fills survive only for the process lifetime (persistence is a later
// concern, tracked with reservation/orderbook durability).
type InMemoryFillQueue struct {
	mu      sync.Mutex
	pending []types.Fill
}

// NewInMemoryFillQueue returns an empty queue.
func NewInMemoryFillQueue() *InMemoryFillQueue {
	return &InMemoryFillQueue{}
}

var _ FillQueue = (*InMemoryFillQueue)(nil)

func (q *InMemoryFillQueue) Enqueue(fills []types.Fill) {
	if len(fills) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, fills...)
}

func (q *InMemoryFillQueue) Drain() []types.Fill {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	out := q.pending
	q.pending = nil
	return out
}

func (q *InMemoryFillQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}
