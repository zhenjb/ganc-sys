package state

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// MarketRegistry is the MVP off-chain market registry (Market config Agreement:
// "Off-chain registry (MVP); on-chain registry = future"). It is the source of
// truth STATE-T03 consults for market existence, active status and the
// tick/lot/fee parameters an order is validated against. Thread-safe.
type MarketRegistry struct {
	mu      sync.RWMutex
	markets map[string]types.Market
}

var (
	// ErrInvalidMarket is returned when a Market being registered is malformed.
	ErrInvalidMarket = errors.New("state: invalid market config")
	// ErrMarketNotFound is returned by MustGet when a market id is unknown.
	ErrMarketNotFound = errors.New("state: market not found")
)

// NewMarketRegistry returns an empty registry.
func NewMarketRegistry() *MarketRegistry {
	return &MarketRegistry{markets: make(map[string]types.Market)}
}

// Register validates and stores a market, keyed by Market.Market. Re-registering
// the same id overwrites (config update). It validates the fields STATE-T03
// relies on: non-empty id/denoms, positive decimal tick/lot, non-negative fees,
// and a known status.
func (r *MarketRegistry) Register(m types.Market) error {
	id := strings.TrimSpace(m.Market)
	if id == "" {
		return fmt.Errorf("%w: market id is empty", ErrInvalidMarket)
	}
	if strings.TrimSpace(m.BaseDenom) == "" || strings.TrimSpace(m.QuoteDenom) == "" {
		return fmt.Errorf("%w: market %q has empty base/quote denom", ErrInvalidMarket, id)
	}
	if _, err := parsePositiveDecimal(m.TickSize); err != nil {
		return fmt.Errorf("%w: market %q tickSize %q: %v", ErrInvalidMarket, id, m.TickSize, err)
	}
	if _, err := parsePositiveDecimal(m.LotSize); err != nil {
		return fmt.Errorf("%w: market %q lotSize %q: %v", ErrInvalidMarket, id, m.LotSize, err)
	}
	if m.MakerFeeBps < 0 || m.TakerFeeBps < 0 {
		return fmt.Errorf("%w: market %q has negative fee bps", ErrInvalidMarket, id)
	}
	if !m.Status.IsValid() {
		return fmt.Errorf("%w: market %q status %q invalid", ErrInvalidMarket, id, m.Status)
	}

	m.Market = id
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markets[id] = m
	return nil
}

// Get returns the market for id, if registered.
func (r *MarketRegistry) Get(market string) (types.Market, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.markets[strings.TrimSpace(market)]
	return m, ok
}

// List returns all registered markets sorted by id (deterministic).
func (r *MarketRegistry) List() []types.Market {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]types.Market, 0, len(r.markets))
	for _, m := range r.markets {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Market < out[j].Market })
	return out
}
