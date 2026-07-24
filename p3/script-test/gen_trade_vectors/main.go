// gen_trade_vectors materializes the canonical trade scenario (Alice buy /
// Bob sell → one Fill) plus three failure vectors under
// testvectors/trade_alice_bob/, generated ENTIRELY from the STATE-T01..T10 code
// so every expected root/fill/reason is reproducible (never hand-authored).
//
// Run from repo root:
//
//	go run ./p3/script-test/gen_trade_vectors
//
// The checked-in files are the single source of truth for P1/P2/P4 cross-checks.
// MANIFEST.json binds every file + SHA-256; hand-editing breaks the determinism
// test (pkg/testvectors TestTradeVectors...).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/testvectors"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const (
	scenario      = "trade_alice_bob"
	vectorVersion = "v0"
	nowUnix       = int64(1_000_000)
)

func fixedMarket() types.Market {
	return types.Market{
		Market: "ATOM/USDC", BaseDenom: "uatom", QuoteDenom: "uusdc",
		TickSize: "0.1", LotSize: "1", MakerFeeBps: 50, TakerFeeBps: 100,
		Status: types.MarketActive,
	}
}

func main() {
	root, err := testvectors.FindRepoRoot()
	if err != nil {
		die("find repo root: %v", err)
	}
	outDir := filepath.Join(root, "testvectors", scenario)
	failDir := filepath.Join(outDir, testvectors.TradeFailureDir)
	if err := os.MkdirAll(failDir, 0o755); err != nil {
		die("mkdir: %v", err)
	}
	w := newWriter(outDir)

	market := fixedMarket()
	w.write(testvectors.TradeFileMarket, market)

	// --- Canonical scenario: Alice buys 20 @100, Bob sells 20 @100. ---
	markets := state.NewMarketRegistry()
	if err := markets.Register(market); err != nil {
		die("register market: %v", err)
	}
	mgr := state.NewOffchainStateManager()
	mustDeposit(mgr, "alice", "uusdc", "5000")
	mustDeposit(mgr, "bob", "uatom", "50")
	initial := []testvectors.TradeBalanceRecord{
		{Owner: "alice", Denom: "uusdc", Available: "5000", Reserved: "0"},
		{Owner: "bob", Denom: "uatom", Available: "50", Reserved: "0"},
	}
	w.write(testvectors.TradeFileInitialState, initial)

	nulls := state.NewInMemoryOrderNullifiers()
	book := state.NewOrderbook("ATOM/USDC", mgr, nulls)
	validator := state.NewOrderValidator(markets, mgr, nulls, nil)

	aliceOrder := signOrder(types.SignedOrder{Owner: "alice", Market: "ATOM/USDC", Side: types.SideBuy, Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1"})
	bobOrder := signOrder(types.SignedOrder{Owner: "bob", Market: "ATOM/USDC", Side: types.SideSell, Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1"})

	aliceRec := placeAndRecord(validator, book, aliceOrder)
	bobRec := placeAndRecord(validator, book, bobOrder)
	w.write(testvectors.TradeFileOrders, []testvectors.TradeOrderRecord{aliceRec, bobRec})

	// Pre-trade balances (post-reserve) + old root — this is the settlement baseline.
	preBalances := snapshotBalances(mgr, []ownerDenom{
		{"alice", "uusdc"}, {"alice", "uatom"}, {"bob", "uatom"}, {"bob", "uusdc"}, {state.FeeAccountOwner, "uusdc"},
	})
	oldRoot := mgr.Root()

	fills, _, err := state.NewMatchingEngine().Match(book, market)
	if err != nil {
		die("match: %v", err)
	}
	if len(fills) != 1 {
		die("canonical scenario must produce exactly 1 fill, got %d", len(fills))
	}
	w.write(testvectors.TradeFileFills, fills)

	// Order commitment inputs (both fully filled after matching).
	orderInputs := []state.OrderCommitmentInput{
		{OrderHash: aliceRec.OrderHash, Owner: "alice", Side: types.SideBuy, Price: "100", Qty: "20", Remaining: "0", Filled: true, Sequence: aliceRec.Sequence},
		{OrderHash: bobRec.OrderHash, Owner: "bob", Side: types.SideSell, Price: "100", Qty: "20", Remaining: "0", Filled: true, Sequence: bobRec.Sequence},
	}
	tc, err := state.BuildTradeCommitments(orderInputs, fills)
	if err != nil {
		die("trade commitments: %v", err)
	}

	// Apply → new root + post-trade balances.
	sides := map[string]types.OrderSide{aliceRec.OrderHash: types.SideBuy, bobRec.OrderHash: types.SideSell}
	if _, err := state.NewTradeApplier("").Apply(mgr, book, fills, market, sides); err != nil {
		die("apply: %v", err)
	}
	newRoot := mgr.Root()
	postBalances := snapshotBalances(mgr, []ownerDenom{
		{"alice", "uusdc"}, {"alice", "uatom"}, {"bob", "uatom"}, {"bob", "uusdc"}, {state.FeeAccountOwner, "uusdc"},
	})
	w.write(testvectors.TradeFileStateAfter, postBalances)

	// STATE-T08: assemble settlement update + commitments (trade-only batch).
	sub := batch.NewSettlementUpdateBuilder()
	upd, com, err := sub.BuildTradeBatch(batch.TradeBatchInputs{
		Core:   batch.SettlementInputs{OldStateRoot: oldRoot, NewStateRoot: newRoot},
		Fills:  fills,
		Orders: orderInputs,
	})
	if err != nil {
		die("build trade batch: %v", err)
	}
	if com.TradesRoot != tc.TradesRoot || com.OrdersRoot != tc.OrdersRoot {
		die("commitment mismatch between BuildTradeCommitments and BuildTradeBatch")
	}
	w.write(testvectors.TradeFileSettlementUpdate, upd)
	w.write(testvectors.TradeFileBatchCommitments, com)

	roots := testvectors.TradeRootsVector{
		OldStateRoot: oldRoot, NewStateRoot: newRoot,
		OrdersRoot: tc.OrdersRoot, TradesRoot: tc.TradesRoot,
		TradeBatchCommitment: upd.TradeBatchCommitment,
	}
	w.write(testvectors.TradeFileRoots, roots)

	// STATE-T09: witness (balances old/new avail+reserved).
	tw, err := batch.BuildTradeWitness(batch.TradeWitnessInputs{
		OldStateRoot: oldRoot, NewStateRoot: newRoot, OrdersRoot: tc.OrdersRoot, TradesRoot: tc.TradesRoot,
		Fills:    fills,
		Orders:   []batch.TradeWitnessOrderInput{{Order: aliceOrder, Filled: true, Remaining: "0"}, {Order: bobOrder, Filled: true, Remaining: "0"}},
		Balances: witnessBalances(preBalances, postBalances),
	})
	if err != nil {
		die("build witness: %v", err)
	}
	w.write(testvectors.TradeFileWitness, types.Witness{Trade: &tw})

	// STATE-T08: 8-input public inputs.
	pi, err := batch.BuildPublicInputsWithTrades(upd, com)
	if err != nil {
		die("public inputs: %v", err)
	}
	w.write(testvectors.TradeFilePublicInputs, testvectors.TradePublicInputsVector{
		Count: len(pi), Labels: batch.PublicInputLabelsWithTrades(), PublicInputs: pi,
		Note: "STATE-T08 extended layout: [0..5] core + [6]=tradesRoot, [7]=ordersRoot.",
	})

	// --- Failure vectors ---
	emitForgedSignature(w, failDir, market)
	emitNonCrossing(w, failDir, market)
	emitOverReserve(w, failDir, market)

	// MANIFEST.
	w.emitManifest(roots, vectorVersion)

	fmt.Println("canonical: oldRoot", oldRoot, "newRoot", newRoot)
	fmt.Println("ordersRoot", tc.OrdersRoot, "tradesRoot", tc.TradesRoot)
	fmt.Printf("wrote %d files into %s\n", len(w.files), outDir)
}

// ---- failure vectors ----

func emitForgedSignature(w *writer, dir string, market types.Market) {
	o := signOrder(types.SignedOrder{Owner: "alice", Market: "ATOM/USDC", Side: types.SideBuy, Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1"})
	o.Price = "101" // tamper AFTER signing → signature no longer matches canonical bytes
	accepted := false
	w.writeFailure(dir, testvectors.TradeFailForgedSignature, testvectors.TradeFailureVector{
		Name: "forged_signature", Stage: "validation",
		Description: "Order price tampered after signing; STATE-T03 rejects on signature check.",
		Market:      market,
		Funding:     []testvectors.TradeBalanceRecord{{Owner: "alice", Denom: "uusdc", Available: "5000", Reserved: "0"}},
		Orders:      []types.SignedOrder{o},
		Expected:    testvectors.TradeFailureExpect{Accepted: &accepted, Reason: string(state.ReasonBadSignature)},
		Note:        "Signature covers the pre-tamper canonical bytes; verify is on canonical bytes, not raw JSON.",
	})
}

func emitNonCrossing(w *writer, dir string, market types.Market) {
	buy := signOrder(types.SignedOrder{Owner: "alice", Market: "ATOM/USDC", Side: types.SideBuy, Price: "99", Qty: "20", Expiry: "2000000", Nonce: "1"})
	sell := signOrder(types.SignedOrder{Owner: "bob", Market: "ATOM/USDC", Side: types.SideSell, Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1"})
	zero := 0
	w.writeFailure(dir, testvectors.TradeFailNonCrossing, testvectors.TradeFailureVector{
		Name: "non_crossing", Stage: "matching",
		Description: "Best bid 99 < best ask 100 → STATE-T05 produces no fill.",
		Market:      market,
		Funding: []testvectors.TradeBalanceRecord{
			{Owner: "alice", Denom: "uusdc", Available: "5000", Reserved: "0"},
			{Owner: "bob", Denom: "uatom", Available: "50", Reserved: "0"},
		},
		Orders:   []types.SignedOrder{buy, sell},
		Expected: testvectors.TradeFailureExpect{FillCount: &zero},
	})
}

func emitOverReserve(w *writer, dir string, market types.Market) {
	// Alice can afford only 21 uusdc but a buy 20@100 needs 2020 reserve.
	o := signOrder(types.SignedOrder{Owner: "alice", Market: "ATOM/USDC", Side: types.SideBuy, Price: "100", Qty: "20", Expiry: "2000000", Nonce: "1"})
	accepted := false
	w.writeFailure(dir, testvectors.TradeFailOverReserve, testvectors.TradeFailureVector{
		Name: "over_reserve", Stage: "validation",
		Description: "Owner cannot afford the collateral (needs 2020 uusdc, has 21); STATE-T03 rejects, STATE-T02 Reserve would also reject.",
		Market:      market,
		Funding:     []testvectors.TradeBalanceRecord{{Owner: "alice", Denom: "uusdc", Available: "21", Reserved: "0"}},
		Orders:      []types.SignedOrder{o},
		Expected:    testvectors.TradeFailureExpect{Accepted: &accepted, Reason: string(state.ReasonInsufficientAvailable), Error: state.ErrInsufficientAvailable.Error()},
	})
}

// ---- helpers ----

type ownerDenom struct{ owner, denom string }

func signOrder(o types.SignedOrder) types.SignedOrder {
	sig, err := state.MockOrderSignature(o)
	if err != nil {
		die("sign order: %v", err)
	}
	o.Signature = sig
	return o
}

func placeAndRecord(v *state.OrderValidator, book *state.Orderbook, o types.SignedOrder) testvectors.TradeOrderRecord {
	verdict, err := v.Validate(o, nowUnix)
	if err != nil {
		die("validate: %v", err)
	}
	if !verdict.Accepted {
		die("canonical order rejected: %s", verdict.Reason)
	}
	ro, err := book.Insert(o, verdict)
	if err != nil {
		die("insert: %v", err)
	}
	return testvectors.TradeOrderRecord{
		Order: o, OrderHash: verdict.OrderHash, OrderNullifier: verdict.OrderNullifier,
		Sequence: ro.Sequence, Accepted: true, ReserveDenom: verdict.ReserveDenom, ReserveAmount: verdict.ReserveAmount,
	}
}

func snapshotBalances(m *state.OffchainStateManager, keys []ownerDenom) []testvectors.TradeBalanceRecord {
	out := make([]testvectors.TradeBalanceRecord, 0, len(keys))
	for _, k := range keys {
		acc := m.Account(k.owner, k.denom)
		reserved := acc.Reserved
		if reserved == "" {
			reserved = "0"
		}
		out = append(out, testvectors.TradeBalanceRecord{Owner: k.owner, Denom: k.denom, Available: acc.Balance, Reserved: reserved})
	}
	return out
}

func witnessBalances(pre, post []testvectors.TradeBalanceRecord) []batch.TradeWitnessBalanceInput {
	out := make([]batch.TradeWitnessBalanceInput, 0, len(pre))
	for i := range pre {
		out = append(out, batch.TradeWitnessBalanceInput{
			Owner: pre[i].Owner, Denom: pre[i].Denom,
			OldAvailable: pre[i].Available, OldReserved: pre[i].Reserved,
			NewAvailable: post[i].Available, NewReserved: post[i].Reserved,
		})
	}
	return out
}

func mustDeposit(m *state.OffchainStateManager, owner, denom, amount string) {
	if _, err := m.ApplyDeposit(types.DepositRecord{DepositID: "seed-" + owner + "-" + denom, Owner: owner, Denom: denom, Amount: amount}); err != nil {
		die("deposit: %v", err)
	}
}

// ---- writer + manifest ----

type manifestEntry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type tradeManifest struct {
	Scenario      string                       `json:"scenario"`
	Description   string                       `json:"description"`
	VectorVersion string                       `json:"vectorVersion"`
	HashAlgorithm string                       `json:"hashAlgorithm"`
	Generator     string                       `json:"generator"`
	Market        types.Market                 `json:"market"`
	Roots         testvectors.TradeRootsVector `json:"roots"`
	Files         []manifestEntry              `json:"files"`
	Note          string                       `json:"note,omitempty"`
}

type writer struct {
	outDir string
	files  []manifestEntry
}

func newWriter(dir string) *writer { return &writer{outDir: dir} }

func (w *writer) write(name string, v any) {
	w.writeAt(w.outDir, name, name, v)
}

func (w *writer) writeFailure(dir, name string, v any) {
	w.writeAt(dir, name, testvectors.TradeFailureDir+"/"+name, v)
}

func (w *writer) writeAt(dir, name, manifestName string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		die("marshal %s: %v", name, err)
	}
	content := append(b, '\n')
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		die("write %s: %v", name, err)
	}
	digest := sha256.Sum256(content)
	w.files = append(w.files, manifestEntry{Name: manifestName, SHA256: "0x" + hex.EncodeToString(digest[:])})
}

func (w *writer) emitManifest(roots testvectors.TradeRootsVector, version string) {
	files := append([]manifestEntry(nil), w.files...)
	sort.SliceStable(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	m := tradeManifest{
		Scenario:      scenario,
		Description:   "Canonical trade: Alice buy 20 ATOM @100, Bob sell 20 @100 → one Fill. Plus forged-signature / non-crossing / over-reserve failure vectors.",
		VectorVersion: version,
		HashAlgorithm: "sha256",
		Generator:     "p3/script-test/gen_trade_vectors",
		Market:        fixedMarket(),
		Roots:         roots,
		Files:         files,
		Note:          "STATE-T11. Do NOT hand-edit; run `go run ./p3/script-test/gen_trade_vectors` to regenerate. Roots generated from STATE-T05/T07 code.",
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		die("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.outDir, "MANIFEST.json"), append(b, '\n'), 0o644); err != nil {
		die("write manifest: %v", err)
	}
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen_trade_vectors: "+format+"\n", args...)
	os.Exit(1)
}
