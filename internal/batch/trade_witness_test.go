package batch

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func aliceOrder() types.SignedOrder {
	return types.SignedOrder{Owner: "alice", Market: "ATOM/USDC", Side: types.SideBuy, Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1", Signature: "0xsig-alice"}
}
func bobOrder() types.SignedOrder {
	return types.SignedOrder{Owner: "bob", Market: "ATOM/USDC", Side: types.SideSell, Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1", Signature: "0xsig-bob"}
}

// canonicalWitnessInputs builds the STATE-T06 canonical Alice/Bob scenario as
// trade witness inputs (balances taken from the T06 conservation numbers).
func canonicalWitnessInputs(t *testing.T) TradeWitnessInputs {
	t.Helper()
	aliceHash, err := state.OrderHash(aliceOrder())
	if err != nil {
		t.Fatalf("alice hash: %v", err)
	}
	bobHash, err := state.OrderHash(bobOrder())
	if err != nil {
		t.Fatalf("bob hash: %v", err)
	}
	fill := types.Fill{
		TradeID: "t1", Market: "ATOM/USDC", MakerOrderHash: aliceHash, TakerOrderHash: bobHash,
		Price: "100", Qty: "20", MakerFee: "10", TakerFee: "20", Buyer: "alice", Seller: "bob",
	}
	return TradeWitnessInputs{
		OldStateRoot: "0xaaaa", NewStateRoot: "0xbbbb", OrdersRoot: "0xor", TradesRoot: "0xtr",
		Fills: []types.Fill{fill},
		Orders: []TradeWitnessOrderInput{
			{Order: aliceOrder(), Filled: true, Remaining: "0"},
			{Order: bobOrder(), Filled: true, Remaining: "0"},
		},
		Balances: []TradeWitnessBalanceInput{
			{Owner: "alice", Denom: "uusdc", OldAvailable: "2980", OldReserved: "2020", NewAvailable: "2990", NewReserved: "0"},
			{Owner: "alice", Denom: "uatom", OldAvailable: "0", OldReserved: "0", NewAvailable: "20", NewReserved: "0"},
			{Owner: "bob", Denom: "uatom", OldAvailable: "30", OldReserved: "20", NewAvailable: "30", NewReserved: "0"},
			{Owner: "bob", Denom: "uusdc", OldAvailable: "0", OldReserved: "0", NewAvailable: "1980", NewReserved: "0"},
			{Owner: state.FeeAccountOwner, Denom: "uusdc", OldAvailable: "0", OldReserved: "0", NewAvailable: "30", NewReserved: "0"},
		},
	}
}

func TestTradeWitnessCanonical(t *testing.T) {
	tw, err := BuildTradeWitness(canonicalWitnessInputs(t))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(tw.Orders) != 2 || len(tw.Fills) != 1 || len(tw.Balances) != 5 {
		t.Fatalf("witness shape wrong: %d orders, %d fills, %d balances", len(tw.Orders), len(tw.Fills), len(tw.Balances))
	}
	// orderHash + nullifier derived (not trusted from caller).
	wantHash, _ := state.OrderHash(aliceOrder())
	wantNull, _ := state.OrderNullifierFor("alice", wantHash)
	found := false
	for _, o := range tw.Orders {
		if o.OrderHash == wantHash {
			found = true
			if o.OrderNullifier != wantNull {
				t.Fatalf("alice nullifier wrong: %s", o.OrderNullifier)
			}
			// reserved fields present on the balance for the order owner.
		}
	}
	if !found {
		t.Fatal("alice order not in witness")
	}
	if tw.OldStateRoot != "0xaaaa" || tw.NewStateRoot != "0xbbbb" || tw.OrdersRoot != "0xor" || tw.TradesRoot != "0xtr" {
		t.Fatalf("roots not carried: %+v", tw)
	}
}

// Value conservation self-check: a tampered new balance breaks per-denom
// conservation and is rejected.
func TestTradeWitnessRejectsNonConserving(t *testing.T) {
	in := canonicalWitnessInputs(t)
	in.Balances[4].NewAvailable = "31" // fee account credited 31 instead of 30
	if _, err := BuildTradeWitness(in); !errors.Is(err, ErrInvalidTradeWitness) {
		t.Fatalf("expected conservation rejection, got %v", err)
	}
}

// Missing feeAccount balance breaks conservation (fees leave the set).
func TestTradeWitnessRejectsMissingFeeAccount(t *testing.T) {
	in := canonicalWitnessInputs(t)
	in.Balances = in.Balances[:4] // drop feeAccount
	if _, err := BuildTradeWitness(in); !errors.Is(err, ErrInvalidTradeWitness) {
		t.Fatalf("expected rejection when feeAccount missing, got %v", err)
	}
}

// Reserved is mandatory: an empty reserved field is rejected (circuit needs it).
func TestTradeWitnessRequiresReserved(t *testing.T) {
	in := canonicalWitnessInputs(t)
	in.Balances[0].OldReserved = ""
	if _, err := BuildTradeWitness(in); !errors.Is(err, ErrInvalidTradeWitness) {
		t.Fatalf("expected rejection for empty reserved, got %v", err)
	}
}

// A fill referencing an order not in the witness is rejected.
func TestTradeWitnessRejectsUnknownOrderRef(t *testing.T) {
	in := canonicalWitnessInputs(t)
	in.Fills[0].MakerOrderHash = "0xunknown"
	if _, err := BuildTradeWitness(in); !errors.Is(err, ErrInvalidTradeWitness) {
		t.Fatalf("expected rejection for unknown maker order, got %v", err)
	}
}

// Buyer without a balance entry is rejected.
func TestTradeWitnessRejectsPartyWithoutBalance(t *testing.T) {
	in := canonicalWitnessInputs(t)
	in.Fills[0].Buyer = "charlie"
	if _, err := BuildTradeWitness(in); !errors.Is(err, ErrInvalidTradeWitness) {
		t.Fatalf("expected rejection for buyer without balance, got %v", err)
	}
}

// An order missing its signature is rejected (order.Validate).
func TestTradeWitnessRequiresSignature(t *testing.T) {
	in := canonicalWitnessInputs(t)
	in.Orders[0].Order.Signature = ""
	if _, err := BuildTradeWitness(in); !errors.Is(err, ErrInvalidTradeWitness) {
		t.Fatalf("expected rejection for missing signature, got %v", err)
	}
}

// BACKWARD COMPAT: a core witness (Trade nil) serializes without a "trade" key.
func TestCoreWitnessJSONUnchangedByTradeExtension(t *testing.T) {
	w := types.Witness{
		Accounts: []types.WitnessAccount{{Owner: "alice", UserSecret: "s", Nonce: "0", OldBalance: "100", NewBalance: "60"}},
	}
	j, _ := json.Marshal(w)
	if strings.Contains(string(j), "trade") {
		t.Fatalf("core witness JSON leaked trade key: %s", j)
	}
}

// The trade witness round-trips inside a full Witness.
func TestWitnessWithTradeRoundTrip(t *testing.T) {
	tw, err := BuildTradeWitness(canonicalWitnessInputs(t))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	w := types.Witness{Trade: &tw}
	j, _ := json.Marshal(w)
	if !strings.Contains(string(j), "\"trade\"") {
		t.Fatalf("trade witness not serialized: %s", j)
	}
	var back types.Witness
	if err := json.Unmarshal(j, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Trade == nil || len(back.Trade.Fills) != 1 || len(back.Trade.Balances) != 5 {
		t.Fatalf("round-trip lost trade witness: %+v", back.Trade)
	}
}
