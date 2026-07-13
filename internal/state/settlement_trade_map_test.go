package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

func atomUsdcRegistry(t *testing.T) *MarketRegistry {
	t.Helper()
	r := NewMarketRegistry()
	if err := r.Register(types.Market{
		Market: "ATOM/USDC", BaseDenom: "uatom", QuoteDenom: "uusdc",
		TickSize: "0.1", LotSize: "1", MakerFeeBps: 50, TakerFeeBps: 100,
		Status: types.MarketActive,
	}); err != nil {
		t.Fatalf("register market: %v", err)
	}
	return r
}

const (
	aliceHash = "0x" + "aa" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bobHash   = "0x" + "bb" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func order(hash, owner string, side types.OrderSide, price, qty string, seq uint64) OrderCommitmentInput {
	return OrderCommitmentInput{
		OrderHash: hash, Owner: owner, Side: side, Price: price, Qty: qty,
		Remaining: "0", Filled: true, Sequence: seq,
	}
}

// alice BUY (maker) crosses bob SELL (taker): the SettlementTrade takes the
// buyer's identity, base denom, and quoteQty = price*qty.
func TestBuildSettlementTradesBuyerPerspective(t *testing.T) {
	orders := []OrderCommitmentInput{
		order(aliceHash, "cosmos1alice", types.SideBuy, "100", "20", 1),
		order(bobHash, "cosmos1bob", types.SideSell, "100", "20", 2),
	}
	fills := []types.Fill{{
		TradeID: "0xc42239", Market: "ATOM/USDC",
		MakerOrderHash: aliceHash, TakerOrderHash: bobHash,
		Price: "100", Qty: "20", MakerFee: "10", TakerFee: "20",
		Buyer: "cosmos1alice", Seller: "cosmos1bob",
	}}

	got, err := BuildSettlementTrades(fills, orders, atomUsdcRegistry(t))
	if err != nil {
		t.Fatalf("BuildSettlementTrades: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	wantNullifier, _ := OrderNullifierFor("cosmos1alice", aliceHash)
	want := types.SettlementTrade{
		TradeID: "0xc42239", Market: "ATOM/USDC",
		MakerOrderID: aliceHash, TakerOrderID: bobHash,
		OrderHash: aliceHash, OrderNullifier: wantNullifier,
		Owner: "cosmos1alice", Denom: "uatom", Side: "buy",
		Amount: "20", Price: "100", BaseQty: "20", QuoteQty: "2000",
		MakerFee: "10", TakerFee: "20",
	}
	if got[0] != want {
		t.Fatalf("trade mismatch\n got=%+v\nwant=%+v", got[0], want)
	}
}

// The buyer may be the TAKER: the mapper must still pick the buy side (owner,
// orderHash, side, nullifier) and never assume maker==buyer.
func TestBuildSettlementTradesBuyerIsTaker(t *testing.T) {
	orders := []OrderCommitmentInput{
		order(aliceHash, "cosmos1alice", types.SideSell, "100", "20", 1),
		order(bobHash, "cosmos1bob", types.SideBuy, "100", "20", 2),
	}
	fills := []types.Fill{{
		TradeID: "t1", Market: "ATOM/USDC",
		MakerOrderHash: aliceHash, TakerOrderHash: bobHash,
		Price: "100", Qty: "20", MakerFee: "10", TakerFee: "20",
		Buyer: "cosmos1bob", Seller: "cosmos1alice",
	}}
	got, err := BuildSettlementTrades(fills, orders, atomUsdcRegistry(t))
	if err != nil {
		t.Fatalf("BuildSettlementTrades: %v", err)
	}
	if got[0].Owner != "cosmos1bob" || got[0].OrderHash != bobHash || got[0].Side != "buy" {
		t.Fatalf("expected buyer=bob (taker), got %+v", got[0])
	}
	wantNullifier, _ := OrderNullifierFor("cosmos1bob", bobHash)
	if got[0].OrderNullifier != wantNullifier {
		t.Fatalf("nullifier = %s, want %s", got[0].OrderNullifier, wantNullifier)
	}
}

// quoteQty is exact decimal (never float): 10.20 * 3 = 30.6 (canonical).
func TestBuildSettlementTradesQuoteQtyDecimal(t *testing.T) {
	orders := []OrderCommitmentInput{
		order(aliceHash, "cosmos1alice", types.SideBuy, "10.20", "3", 1),
		order(bobHash, "cosmos1bob", types.SideSell, "10.20", "3", 2),
	}
	fills := []types.Fill{{
		TradeID: "t1", Market: "ATOM/USDC",
		MakerOrderHash: aliceHash, TakerOrderHash: bobHash,
		Price: "10.20", Qty: "3", MakerFee: "0", TakerFee: "0",
		Buyer: "cosmos1alice", Seller: "cosmos1bob",
	}}
	got, err := BuildSettlementTrades(fills, orders, atomUsdcRegistry(t))
	if err != nil {
		t.Fatalf("BuildSettlementTrades: %v", err)
	}
	if got[0].QuoteQty != "30.6" || got[0].BaseQty != "3" {
		t.Fatalf("quoteQty=%q baseQty=%q, want 30.6 / 3", got[0].QuoteQty, got[0].BaseQty)
	}
}

func TestBuildSettlementTradesErrors(t *testing.T) {
	reg := atomUsdcRegistry(t)
	base := func() ([]types.Fill, []OrderCommitmentInput) {
		orders := []OrderCommitmentInput{
			order(aliceHash, "cosmos1alice", types.SideBuy, "100", "20", 1),
			order(bobHash, "cosmos1bob", types.SideSell, "100", "20", 2),
		}
		fills := []types.Fill{{
			TradeID: "t1", Market: "ATOM/USDC",
			MakerOrderHash: aliceHash, TakerOrderHash: bobHash,
			Price: "100", Qty: "20", MakerFee: "10", TakerFee: "20",
			Buyer: "cosmos1alice", Seller: "cosmos1bob",
		}}
		return fills, orders
	}

	t.Run("missing order", func(t *testing.T) {
		fills, _ := base()
		if _, err := BuildSettlementTrades(fills, nil, reg); !errors.Is(err, ErrBuildSettlementTrades) {
			t.Fatalf("want ErrBuildSettlementTrades, got %v", err)
		}
	})
	t.Run("not a cross (both buy)", func(t *testing.T) {
		fills, orders := base()
		orders[1].Side = types.SideBuy
		if _, err := BuildSettlementTrades(fills, orders, reg); !errors.Is(err, ErrBuildSettlementTrades) {
			t.Fatalf("want error, got %v", err)
		}
	})
	t.Run("buyer mismatch", func(t *testing.T) {
		fills, orders := base()
		fills[0].Buyer = "cosmos1mallory"
		if _, err := BuildSettlementTrades(fills, orders, reg); !errors.Is(err, ErrBuildSettlementTrades) {
			t.Fatalf("want error, got %v", err)
		}
	})
	t.Run("unknown market", func(t *testing.T) {
		fills, orders := base()
		fills[0].Market = "NOPE/USDC"
		if _, err := BuildSettlementTrades(fills, orders, reg); !errors.Is(err, ErrBuildSettlementTrades) {
			t.Fatalf("want error, got %v", err)
		}
	})
	t.Run("nil registry", func(t *testing.T) {
		fills, orders := base()
		if _, err := BuildSettlementTrades(fills, orders, nil); !errors.Is(err, ErrBuildSettlementTrades) {
			t.Fatalf("want error, got %v", err)
		}
	})
}

// The wire JSON must use the camelCase keys the chain proto expects.
func TestSettlementTradeJSONRoundTrip(t *testing.T) {
	orders := []OrderCommitmentInput{
		order(aliceHash, "cosmos1alice", types.SideBuy, "100", "20", 1),
		order(bobHash, "cosmos1bob", types.SideSell, "100", "20", 2),
	}
	fills := []types.Fill{{
		TradeID: "t1", Market: "ATOM/USDC",
		MakerOrderHash: aliceHash, TakerOrderHash: bobHash,
		Price: "100", Qty: "20", MakerFee: "10", TakerFee: "20",
		Buyer: "cosmos1alice", Seller: "cosmos1bob",
	}}
	got, err := BuildSettlementTrades(fills, orders, atomUsdcRegistry(t))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	raw, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{
		`"tradeId"`, `"makerOrderId"`, `"takerOrderId"`, `"orderHash"`,
		`"orderNullifier"`, `"denom"`, `"side"`, `"baseQty"`, `"quoteQty"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("json missing key %s: %s", key, raw)
		}
	}
	var back types.SettlementTrade
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != got[0] {
		t.Fatalf("round-trip mismatch\n got=%+v\nwant=%+v", back, got[0])
	}
}
