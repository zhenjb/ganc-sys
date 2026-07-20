package service

import (
	"context"
	"math/big"
	"sort"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// INT-T07 — GET /api/state trading extension.
//
// The dashboard state endpoint appends trading data sourced from the SAME
// off-chain state / registry / trade store the order API uses (INT-T02..T06), so
// the dashboard never drifts from the order/orderbook/trades endpoints.

// defaultLatestTradesLimit caps how many recent fills GET /api/state returns.
const defaultLatestTradesLimit = 20

// TradeStateProvider supplies the trading extension of GET /api/state. The
// StateHandler treats it as optional (nil in mock/non-trading mode → the base
// deposit/withdraw response is unchanged). *RealOrderService implements it.
type TradeStateProvider interface {
	TradeState(ctx context.Context, owner string) TradeStateResult
}

// TradeStateResult is the appended trading slice of the dashboard state.
type TradeStateResult struct {
	ReservedBalances []types.ReservedBalance
	OpenOrders       []types.OpenOrder
	LatestTrades     []types.Fill
	MarketStatus     map[string]types.MarketStatus
	// UserBalances is the REAL off-chain (L2) holding per "owner/denom" =
	// available + reserved, sourced from the shared state manager. It replaces the
	// legacy seeded memory ledger on GET /api/state. moduleAccountBalance is NOT
	// here: the state endpoint takes it from the chain REST module balance (ground
	// truth) instead of re-deriving it off-chain.
	UserBalances map[string]string
	// Denoms is the sorted, de-duplicated set of denoms the registry trades (every
	// market's base + quote), e.g. ["uatom","uosmo","uusdc"]. FE reads it to know
	// which denoms are in use/active.
	Denoms []string
}

var _ TradeStateProvider = (*RealOrderService)(nil)

// TradeState assembles the trading dashboard slice. When owner is given, reserved
// balances and open orders are filtered to that owner; otherwise reserved
// balances list only accounts that currently have collateral locked and open
// orders is empty (the dashboard needs an owner to scope orders). latestTrades
// and marketStatus are global.
func (s *RealOrderService) TradeState(ctx context.Context, owner string) TradeStateResult {
	owner = strings.TrimSpace(owner)

	openOrders := []types.OpenOrder{}
	if owner != "" {
		openOrders = s.ListOpenOrders(ctx, owner).OpenOrders
	}

	// One consistent snapshot feeds reservedBalances AND userBalances so the two
	// never disagree.
	accounts := s.manager.Snapshot().Accounts()

	// userBalances: per "owner/denom" total holding = available + reserved (the
	// full rollup balance; reservedBalances still shows the locked split). Keyed
	// "owner/denom" to match the legacy dashboard shape.
	userBalances := map[string]string{}
	for _, a := range accounts {
		userBalances[a.Owner+"/"+a.Denom] = addAmounts(a.Balance, a.Reserved)
	}

	// marketStatus keyed by the denom pair (baseDenom/quoteDenom) so it shares the
	// denom vocabulary of userBalances / moduleAccountBalance (e.g. "uatom/uusdc").
	// The same pass collects the distinct denom set (base + quote) for `denoms`.
	marketStatus := map[string]types.MarketStatus{}
	denomSet := map[string]struct{}{}
	for _, m := range s.markets.List() {
		marketStatus[m.BaseDenom+"/"+m.QuoteDenom] = m.Status
		if m.BaseDenom != "" {
			denomSet[m.BaseDenom] = struct{}{}
		}
		if m.QuoteDenom != "" {
			denomSet[m.QuoteDenom] = struct{}{}
		}
	}
	denoms := make([]string, 0, len(denomSet))
	for d := range denomSet {
		denoms = append(denoms, d)
	}
	sort.Strings(denoms)

	return TradeStateResult{
		ReservedBalances: s.reservedBalances(owner, accounts),
		OpenOrders:       openOrders,
		LatestTrades:     s.trades.Recent(defaultLatestTradesLimit),
		MarketStatus:     marketStatus,
		UserBalances:     userBalances,
		Denoms:           denoms,
	}
}

// addAmounts returns the base-10 sum of two non-negative integer amount strings,
// treating "" as 0 (Reserved is "" when unlocked). Uses big.Int because token
// amounts in their smallest unit can exceed int64.
func addAmounts(a, b string) string {
	sum := new(big.Int)
	for _, s := range []string{a, b} {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if v, ok := new(big.Int).SetString(s, 10); ok {
			sum.Add(sum, v)
		}
	}
	return sum.String()
}

// reservedBalances reports each account's available/reserved split. With an
// owner, every account of that owner is returned (so the dashboard shows the full
// available+reserved picture); without one, only accounts with locked reserved
// are listed (avoids dumping every account). accounts is the caller's snapshot
// slice (sorted by owner,denom) so this shares one consistent read with the
// userBalances rollup.
func (s *RealOrderService) reservedBalances(owner string, accounts []types.Account) []types.ReservedBalance {
	out := make([]types.ReservedBalance, 0)
	for _, a := range accounts {
		if owner != "" {
			if a.Owner != owner {
				continue
			}
		} else if strings.TrimSpace(a.Reserved) == "" {
			continue // no owner filter: only accounts with locked collateral
		}
		reserved := a.Reserved
		if reserved == "" {
			reserved = "0"
		}
		out = append(out, types.ReservedBalance{
			Owner:     a.Owner,
			Denom:     a.Denom,
			Available: a.Balance,
			Reserved:  reserved,
		})
	}
	return out
}
