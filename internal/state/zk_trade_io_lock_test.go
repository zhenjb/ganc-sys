package state

import (
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ZK-T01 contract lock. These assertions pin the LOCKED trade I/O encoding
// documented in docs/matching_orderbook/zk_trade_io.md so it cannot drift
// silently: any change to a domain tag, the canonical field order, or the hash
// preimage breaks this test, forcing a coordinated version bump + vector
// regeneration across A (circuit) / P1 (verifier) / P3 / P4. The hex values here
// are the published cross-check example (§8) reproduced independently by plain
// sha256sum.

func TestZKTradeIODomainTagsLocked(t *testing.T) {
	if got := types.OrderCanonicalVersion; got != "zkdex/order/v0" {
		t.Fatalf("order canonical version = %q, want zkdex/order/v0 (bump = breaking contract change)", got)
	}
	if got := OrderNullifierDomainTag(); got != "zkdex/orderNullifier/v0" {
		t.Fatalf("orderNullifier domain = %q, want zkdex/orderNullifier/v0", got)
	}
	orders, trades := TradeCommitmentDomainTags()
	if orders != "zkdex/batch/ordersRoot/v0" {
		t.Fatalf("ordersRoot domain = %q, want zkdex/batch/ordersRoot/v0", orders)
	}
	if trades != "zkdex/batch/tradesRoot/v0" {
		t.Fatalf("tradesRoot domain = %q, want zkdex/batch/tradesRoot/v0", trades)
	}
}

// The canonical example (zk_trade_io.md §8) must derive to the exact published
// values — the bit-exact cross-check the DoD requires.
func TestZKTradeIOExampleVectorLocked(t *testing.T) {
	aliceBuy := types.SignedOrder{
		Owner: "alice", Market: "ATOM/USDC", Side: types.SideBuy,
		Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1",
	}

	orderHash, err := OrderHash(aliceBuy)
	if err != nil {
		t.Fatalf("OrderHash: %v", err)
	}
	const wantHash = "0x60ab102de75186520e5bc75fee0b76583567323946d50dddafcd0b55334dbb8a"
	if orderHash != wantHash {
		t.Fatalf("orderHash = %s, want %s (canonical encoding changed — bump zkdex/order/v0)", orderHash, wantHash)
	}

	nullifier, err := OrderNullifierFor(aliceBuy.Owner, orderHash)
	if err != nil {
		t.Fatalf("OrderNullifierFor: %v", err)
	}
	const wantNullifier = "0xeb02fbee136b33065013a41019599dd6390423e59721cfc7f8c05acd60466ca7"
	if nullifier != wantNullifier {
		t.Fatalf("orderNullifier = %s, want %s", nullifier, wantNullifier)
	}
}

// ordersRoot / tradesRoot of the canonical batch must match the published roots.
func TestZKTradeIORootsLocked(t *testing.T) {
	orders := []OrderCommitmentInput{
		{OrderHash: "0x60ab102de75186520e5bc75fee0b76583567323946d50dddafcd0b55334dbb8a",
			Owner: "alice", Side: types.SideBuy, Price: "100", Qty: "20", Remaining: "0", Filled: true, Sequence: 1},
		{OrderHash: "0xbeeae7ff0bad0b8e19594892537e52e77a17cfd0b6c92ebfe105911c1897749f",
			Owner: "bob", Side: types.SideSell, Price: "100", Qty: "20", Remaining: "0", Filled: true, Sequence: 2},
	}
	fills := []types.Fill{{
		TradeID:        "0xc0b11f3a4fbf7ca3ba85c2b90a7c4c6337da9001e14dd2b5bf0d149a8c06cd14",
		Market:         "ATOM/USDC",
		MakerOrderHash: "0x60ab102de75186520e5bc75fee0b76583567323946d50dddafcd0b55334dbb8a",
		TakerOrderHash: "0xbeeae7ff0bad0b8e19594892537e52e77a17cfd0b6c92ebfe105911c1897749f",
		Price:          "100", Qty: "20", MakerFee: "10", TakerFee: "20", Buyer: "alice", Seller: "bob",
	}}

	tc, err := BuildTradeCommitments(orders, fills)
	if err != nil {
		t.Fatalf("BuildTradeCommitments: %v", err)
	}
	const wantOrders = "0x798b004fcf98281e699899cf89b853a5849bf8e8fdbf474b7300e770cd4ab3ea"
	const wantTrades = "0xe0485ea21f6ea846a9de81a4153afa85ca471933f6910285258c0c2051410dfb"
	if tc.OrdersRoot != wantOrders {
		t.Fatalf("ordersRoot = %s, want %s", tc.OrdersRoot, wantOrders)
	}
	if tc.TradesRoot != wantTrades {
		t.Fatalf("tradesRoot = %s, want %s", tc.TradesRoot, wantTrades)
	}
}
