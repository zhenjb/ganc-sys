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

// AGR-2b: alice BUY (maker) crosses bob SELL (taker) → TWO records, buyer first
// then seller, each carrying its own order's owner/hash/nullifier/side.
func TestBuildSettlementTradesTwoPerFill(t *testing.T) {
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
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (buyer+seller)", len(got))
	}

	nullA, _ := OrderNullifierFor("cosmos1alice", aliceHash)
	nullB, _ := OrderNullifierFor("cosmos1bob", bobHash)
	wantBuy := types.SettlementTrade{
		TradeID: "0xc42239-buy", Market: "ATOM/USDC",
		MakerOrderID: aliceHash, TakerOrderID: bobHash,
		OrderHash: aliceHash, OrderNullifier: nullA,
		Owner: "cosmos1alice", Denom: "uatom", Side: "buy",
		Amount: "20", Price: "100", BaseQty: "20", QuoteQty: "2000",
		MakerFee: "10", TakerFee: "20",
	}
	wantSell := types.SettlementTrade{
		TradeID: "0xc42239-sell", Market: "ATOM/USDC",
		MakerOrderID: aliceHash, TakerOrderID: bobHash,
		OrderHash: bobHash, OrderNullifier: nullB,
		Owner: "cosmos1bob", Denom: "uatom", Side: "sell",
		Amount: "20", Price: "100", BaseQty: "20", QuoteQty: "2000",
		MakerFee: "10", TakerFee: "20",
	}
	if got[0] != wantBuy {
		t.Fatalf("buyer record mismatch\n got=%+v\nwant=%+v", got[0], wantBuy)
	}
	if got[1] != wantSell {
		t.Fatalf("seller record mismatch\n got=%+v\nwant=%+v", got[1], wantSell)
	}
	// The two nullifiers must differ (both orders individually replay-protected).
	if nullA == nullB {
		t.Fatal("buyer and seller nullifier must differ")
	}
}

// The buyer may be the TAKER: the mapper must key each record off the side, not
// the maker/taker role.
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
	if got[0].Owner != "cosmos1bob" || got[0].OrderHash != bobHash || got[0].Side != "buy" || got[0].TradeID != "t1-buy" {
		t.Fatalf("buyer record wrong: %+v", got[0])
	}
	if got[1].Owner != "cosmos1alice" || got[1].OrderHash != aliceHash || got[1].Side != "sell" || got[1].TradeID != "t1-sell" {
		t.Fatalf("seller record wrong: %+v", got[1])
	}
}

// quoteQty is exact decimal (never float): 10.20 * 3 = 30.6 (canonical) on both records.
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
	for i, rec := range got {
		if rec.QuoteQty != "30.6" || rec.BaseQty != "3" {
			t.Fatalf("record[%d] quoteQty=%q baseQty=%q, want 30.6 / 3", i, rec.QuoteQty, rec.BaseQty)
		}
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
	t.Run("seller mismatch", func(t *testing.T) {
		fills, orders := base()
		fills[0].Seller = "cosmos1mallory"
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

// The wire JSON must use the camelCase keys the chain proto expects, for both
// the buyer and seller record.
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
	for _, rec := range got {
		raw, err := json.Marshal(rec)
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
		if back != rec {
			t.Fatalf("round-trip mismatch\n got=%+v\nwant=%+v", back, rec)
		}
	}
}
