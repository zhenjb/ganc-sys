package service

import (
	"context"
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

	marketStatus := map[string]types.MarketStatus{}
	for _, m := range s.markets.List() {
		marketStatus[m.Market] = m.Status
	}

	return TradeStateResult{
		ReservedBalances: s.reservedBalances(owner),
		OpenOrders:       openOrders,
		LatestTrades:     s.trades.Recent(defaultLatestTradesLimit),
		MarketStatus:     marketStatus,
	}
}

// reservedBalances reports each account's available/reserved split. With an
// owner, every account of that owner is returned (so the dashboard shows the full
// available+reserved picture); without one, only accounts with locked reserved
// are listed (avoids dumping every account). Accounts come from a manager
// snapshot (sorted by owner,denom) — a consistent read.
func (s *RealOrderService) reservedBalances(owner string) []types.ReservedBalance {
	accounts := s.manager.Snapshot().Accounts()
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
