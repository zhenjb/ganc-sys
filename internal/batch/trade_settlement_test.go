package batch

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func sampleTradeOrders() []state.OrderCommitmentInput {
	return []state.OrderCommitmentInput{
		{OrderHash: "o-alice", Owner: "alice", Side: types.SideBuy, Price: "100", Qty: "20", Remaining: "0", Filled: true, Sequence: 1},
		{OrderHash: "o-bob", Owner: "bob", Side: types.SideSell, Price: "100", Qty: "20", Remaining: "0", Filled: true, Sequence: 2},
	}
}

func sampleTradeFills() []types.Fill {
	return []types.Fill{
		{TradeID: "t1", Market: "ATOM/USDC", MakerOrderHash: "o-alice", TakerOrderHash: "o-bob",
			Price: "100", Qty: "20", MakerFee: "10", TakerFee: "20", Buyer: "alice", Seller: "bob"},
	}
}

func coreInputsWithDepositAndWithdraw(t *testing.T) SettlementInputs {
	t.Helper()
	dest := "cosmos1destination"
	dh, err := state.WithdrawAddressHash(dest)
	if err != nil {
		t.Fatalf("destination hash: %v", err)
	}
	return SettlementInputs{
		OldStateRoot: "0xaaaa",
		NewStateRoot: "0xbbbb",
		Deposits: []types.DepositRecord{
			{DepositID: "dep-1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "100"},
		},
		Withdrawals: []WithdrawalInput{
			{
				Request:         types.WithdrawRequest{WithdrawID: "wd-1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "10", Destination: dest, Nonce: "1", Signature: "0xsig"},
				Nullifier:       "0xnullifier1",
				DestinationHash: dh,
			},
		},
	}
}

// A single batch can carry deposits, withdrawals AND trades.
func TestBuildTradeBatchMixed(t *testing.T) {
	b := NewSettlementUpdateBuilder()
	upd, com, err := b.BuildTradeBatch(TradeBatchInputs{
		Core:   coreInputsWithDepositAndWithdraw(t),
		Fills:  sampleTradeFills(),
		Orders: sampleTradeOrders(),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if len(upd.Deposits) != 1 || len(upd.Withdrawals) != 1 || len(upd.Trades) != 1 {
		t.Fatalf("mixed batch shape wrong: %d dep, %d wd, %d trades", len(upd.Deposits), len(upd.Withdrawals), len(upd.Trades))
	}
	if upd.TradeBatchCommitment == "" {
		t.Fatal("tradeBatchCommitment must be set")
	}
	// Core 4 roots + appended trade roots all present.
	for _, r := range []struct{ name, val string }{
		{"deposits", com.DepositsRoot}, {"withdrawals", com.WithdrawalsRoot},
		{"nullifiers", com.NullifiersRoot}, {"withdrawOutputs", com.WithdrawOutputsRoot},
		{"trades", com.TradesRoot}, {"orders", com.OrdersRoot},
	} {
		if r.val == "" {
			t.Fatalf("commitment %s root empty", r.name)
		}
	}
	// TradeBatchCommitment binds the two roots deterministically.
	if upd.TradeBatchCommitment != TradeBatchCommitment(com.TradesRoot, com.OrdersRoot) {
		t.Fatal("tradeBatchCommitment does not bind tradesRoot+ordersRoot")
	}
}

// A trade-only batch (no deposits/withdrawals) is valid.
func TestBuildTradeBatchTradeOnly(t *testing.T) {
	b := NewSettlementUpdateBuilder()
	upd, com, err := b.BuildTradeBatch(TradeBatchInputs{
		Core:   SettlementInputs{OldStateRoot: "0xaaaa", NewStateRoot: "0xbbbb"},
		Fills:  sampleTradeFills(),
		Orders: sampleTradeOrders(),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(upd.Deposits) != 0 || len(upd.Withdrawals) != 0 {
		t.Fatal("trade-only batch should have empty core slices")
	}
	if len(upd.Trades) != 1 || upd.BatchID == "" {
		t.Fatalf("trade-only batch shape wrong: %+v", upd)
	}
	if com.TradesRoot == "" || com.OrdersRoot == "" {
		t.Fatal("trade roots must be set")
	}
	// Core roots equal the empty-batch commitments.
	empty := BuildCommitments(types.SettlementUpdate{})
	if com.DepositsRoot != empty.DepositsRoot || com.WithdrawalsRoot != empty.WithdrawalsRoot {
		t.Fatal("trade-only core roots should equal empty-batch roots")
	}
}

// Empty batch (no core, no trades) is rejected.
func TestBuildTradeBatchEmptyRejected(t *testing.T) {
	b := NewSettlementUpdateBuilder()
	_, _, err := b.BuildTradeBatch(TradeBatchInputs{
		Core: SettlementInputs{OldStateRoot: "0xaaaa", NewStateRoot: "0xbbbb"},
	})
	if !errors.Is(err, ErrInvalidSettlementInputs) {
		t.Fatalf("empty batch should be rejected, got %v", err)
	}
}

// Duplicate order nullifier (same owner+orderHash) is rejected.
func TestBuildTradeBatchDuplicateNullifier(t *testing.T) {
	b := NewSettlementUpdateBuilder()
	orders := []state.OrderCommitmentInput{
		{OrderHash: "o", Owner: "alice", Side: types.SideBuy, Price: "1", Qty: "1", Sequence: 1},
		{OrderHash: "o", Owner: "alice", Side: types.SideBuy, Price: "1", Qty: "1", Sequence: 2},
	}
	_, _, err := b.BuildTradeBatch(TradeBatchInputs{
		Core:   SettlementInputs{OldStateRoot: "0xaaaa", NewStateRoot: "0xbbbb"},
		Fills:  sampleTradeFills(),
		Orders: orders,
	})
	if !errors.Is(err, ErrInvalidSettlementInputs) {
		t.Fatalf("duplicate nullifier should be rejected, got %v", err)
	}
}

// A fill missing a required field is rejected.
func TestBuildTradeBatchInvalidFill(t *testing.T) {
	b := NewSettlementUpdateBuilder()
	bad := sampleTradeFills()
	bad[0].Buyer = "" // required
	_, _, err := b.BuildTradeBatch(TradeBatchInputs{
		Core:   SettlementInputs{OldStateRoot: "0xaaaa", NewStateRoot: "0xbbbb"},
		Fills:  bad,
		Orders: sampleTradeOrders(),
	})
	if !errors.Is(err, ErrInvalidSettlementInputs) {
		t.Fatalf("invalid fill should be rejected, got %v", err)
	}
}

// BACKWARD COMPAT: a core (no-trade) SettlementUpdate + BatchCommitments must
// serialize byte-identically to the pre-trade schema (no new keys).
func TestCoreBatchJSONUnchangedByTradeExtension(t *testing.T) {
	b := NewSettlementUpdateBuilder()
	upd, err := b.Build(coreInputsWithDepositAndWithdraw(t))
	if err != nil {
		t.Fatalf("core build: %v", err)
	}
	updJSON, _ := json.Marshal(upd)
	if strings.Contains(string(updJSON), "trades") || strings.Contains(string(updJSON), "tradeBatchCommitment") {
		t.Fatalf("core SettlementUpdate JSON leaked trade keys: %s", updJSON)
	}
	comJSON, _ := json.Marshal(BuildCommitments(upd))
	if strings.Contains(string(comJSON), "tradesRoot") || strings.Contains(string(comJSON), "ordersRoot") {
		t.Fatalf("core BatchCommitments JSON leaked trade roots: %s", comJSON)
	}
}

// The extended public-input vector appends [6]=tradesRoot,[7]=ordersRoot while
// the core 6-input Build is unchanged.
func TestBuildPublicInputsWithTrades(t *testing.T) {
	b := NewSettlementUpdateBuilder()
	upd, com, err := b.BuildTradeBatch(TradeBatchInputs{
		Core:   coreInputsWithDepositAndWithdraw(t),
		Fills:  sampleTradeFills(),
		Orders: sampleTradeOrders(),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Core builder unchanged: 6 inputs.
	core, err := BuildPublicInputs(upd, com)
	if err != nil {
		t.Fatalf("core public inputs: %v", err)
	}
	if len(core) != PublicInputCount || PublicInputCount != 6 {
		t.Fatalf("core public inputs must stay 6, got %d", len(core))
	}

	// Extended: 8 inputs, [6]/[7] = the trade roots.
	ext, err := BuildPublicInputsWithTrades(upd, com)
	if err != nil {
		t.Fatalf("extended public inputs: %v", err)
	}
	if len(ext) != PublicInputCountWithTrades {
		t.Fatalf("extended public inputs must be 8, got %d", len(ext))
	}
	for i := 0; i < PublicInputCount; i++ {
		if ext[i] != core[i] {
			t.Fatalf("extended[%d] diverged from core", i)
		}
	}
	if ext[PublicInputIdxTradesRoot] != com.TradesRoot || ext[PublicInputIdxOrdersRoot] != com.OrdersRoot {
		t.Fatal("extended [6]/[7] must be tradesRoot/ordersRoot")
	}
}

// A CORE batch (no trades) still yields a valid 8-input vector using the empty
// sentinels for [6]/[7].
func TestBuildPublicInputsWithTradesCoreUsesSentinels(t *testing.T) {
	b := NewSettlementUpdateBuilder()
	upd, err := b.Build(coreInputsWithDepositAndWithdraw(t))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	com := BuildCommitments(upd) // no trade roots

	ext, err := BuildPublicInputsWithTrades(upd, com)
	if err != nil {
		t.Fatalf("extended: %v", err)
	}
	if ext[PublicInputIdxTradesRoot] != state.EmptyTradesRoot() {
		t.Fatalf("core batch [6] must be empty trades sentinel")
	}
	if ext[PublicInputIdxOrdersRoot] != state.EmptyOrdersRoot() {
		t.Fatalf("core batch [7] must be empty orders sentinel")
	}
}
