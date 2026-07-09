package service

import (
	"strings"
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// TradeStore is the P4 fill-history read model behind GET /api/trades?market=
// (INT-T04). The matching trigger (INT-T05) Records fills as they are produced;
// the query reads them back per market. Kept as an interface so a durable
// (Postgres) implementation can replace the in-memory one without touching the
// order service.
type TradeStore interface {
	// Record appends fills to their market's history (grouped by Fill.Market).
	Record(fills []types.Fill)
	// ByMarket returns a copy of a market's fills in insertion order.
	ByMarket(market string) []types.Fill
}

// InMemoryTradeStore is the MVP in-memory fill history, keyed by market and
// thread-safe. Fills survive only for the process lifetime (persistence is a
// later concern, tracked alongside reservation/orderbook durability).
type InMemoryTradeStore struct {
	mu       sync.RWMutex
	byMarket map[string][]types.Fill
}

// NewInMemoryTradeStore returns an empty fill history.
func NewInMemoryTradeStore() *InMemoryTradeStore {
	return &InMemoryTradeStore{byMarket: make(map[string][]types.Fill)}
}

var _ TradeStore = (*InMemoryTradeStore)(nil)

// Record appends each fill to its market's slice. Fills with an empty market are
// skipped (a fill always carries its market — STATE-T05).
func (s *InMemoryTradeStore) Record(fills []types.Fill) {
	if len(fills) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range fills {
		m := strings.TrimSpace(f.Market)
		if m == "" {
			continue
		}
		s.byMarket[m] = append(s.byMarket[m], f)
	}
}

// ByMarket returns a copy of the market's fills (insertion order) so a caller
// cannot mutate the stored history. Empty/unknown market yields an empty slice.
func (s *InMemoryTradeStore) ByMarket(market string) []types.Fill {
	market = strings.TrimSpace(market)
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.byMarket[market]
	out := make([]types.Fill, len(src))
	copy(out, src)
	return out
}
