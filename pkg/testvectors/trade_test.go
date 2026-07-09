package testvectors_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/testvectors"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// rebuildBook registers the market, funds accounts, validates + inserts each
// order, and returns the book + manager so a caller can re-run matching. This is
// the SAME pipeline (STATE-T02..T05) the generator used — the point is to
// re-derive, not trust the checked-in values.
func rebuildBook(t *testing.T, market types.Market, funding []testvectors.TradeBalanceRecord, orders []types.SignedOrder) (*state.Orderbook, *state.OffchainStateManager, []state.OrderValidation) {
	t.Helper()
	markets := state.NewMarketRegistry()
	if err := markets.Register(market); err != nil {
		t.Fatalf("register market: %v", err)
	}
	mgr := state.NewOffchainStateManager()
	for i, f := range funding {
		if _, err := mgr.ApplyDeposit(types.DepositRecord{DepositID: "f" + itoa(i), Owner: f.Owner, Denom: f.Denom, Amount: f.Available}); err != nil {
			t.Fatalf("fund: %v", err)
		}
	}
	nulls := state.NewInMemoryOrderNullifiers()
	book := state.NewOrderbook(market.Market, mgr, nulls)
	v := state.NewOrderValidator(markets, mgr, nulls, nil)

	verdicts := make([]state.OrderValidation, 0, len(orders))
	for _, o := range orders {
		verdict, err := v.Validate(o, 1_000_000)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		verdicts = append(verdicts, verdict)
		if verdict.Accepted {
			if _, err := book.Insert(o, verdict); err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
	}
	return book, mgr, verdicts
}

func itoa(i int) string { return strconv.Itoa(i) }

// The canonical vector's fills + roots must be reproducible from the recorded
// orders/market via the live STATE-T05/T07 code (pitfall: no hand-authored roots).
func TestTradeVectorsCanonicalReproducible(t *testing.T) {
	var market types.Market
	mustLoad(t, testvectors.TradeFileMarket, &market)
	var initial []testvectors.TradeBalanceRecord
	mustLoad(t, testvectors.TradeFileInitialState, &initial)
	var orderRecs []testvectors.TradeOrderRecord
	mustLoad(t, testvectors.TradeFileOrders, &orderRecs)
	var wantFills []types.Fill
	mustLoad(t, testvectors.TradeFileFills, &wantFills)
	var wantRoots testvectors.TradeRootsVector
	mustLoad(t, testvectors.TradeFileRoots, &wantRoots)

	orders := make([]types.SignedOrder, len(orderRecs))
	for i, r := range orderRecs {
		orders[i] = r.Order
	}

	// Re-run validation + matching from scratch.
	book, _, verdicts := rebuildBook(t, market, initial, orders)
	for i, v := range verdicts {
		if !v.Accepted {
			t.Fatalf("order %d unexpectedly rejected: %s", i, v.Reason)
		}
		if v.OrderHash != orderRecs[i].OrderHash {
			t.Fatalf("order %d hash mismatch: re-derived %s, vector %s", i, v.OrderHash, orderRecs[i].OrderHash)
		}
	}

	fills, err := state.NewMatchingEngine().Match(book, market)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if gotJSON, wantJSON := mustJSON(t, fills), mustJSON(t, wantFills); gotJSON != wantJSON {
		t.Fatalf("re-derived fills differ from vector:\n got: %s\nwant: %s", gotJSON, wantJSON)
	}

	// Re-derive ordersRoot/tradesRoot from the (post-fill) orders + fills.
	orderInputs := make([]state.OrderCommitmentInput, len(orderRecs))
	for i, r := range orderRecs {
		orderInputs[i] = state.OrderCommitmentInput{
			OrderHash: r.OrderHash, Owner: r.Order.Owner, Side: r.Order.Side,
			Price: r.Order.Price, Qty: r.Order.Qty, Remaining: "0", Filled: true, Sequence: r.Sequence,
		}
	}
	tc, err := state.BuildTradeCommitments(orderInputs, fills)
	if err != nil {
		t.Fatalf("commitments: %v", err)
	}
	if tc.TradesRoot != wantRoots.TradesRoot {
		t.Fatalf("tradesRoot mismatch: re-derived %s, vector %s", tc.TradesRoot, wantRoots.TradesRoot)
	}
	if tc.OrdersRoot != wantRoots.OrdersRoot {
		t.Fatalf("ordersRoot mismatch: re-derived %s, vector %s", tc.OrdersRoot, wantRoots.OrdersRoot)
	}
}

// Failure vectors must produce their recorded reject reason / no-fill outcome.
func TestTradeVectorsFailures(t *testing.T) {
	t.Run("forged_signature", func(t *testing.T) {
		fv := mustLoadFailure(t, testvectors.TradeFailForgedSignature)
		assertValidationReject(t, fv)
	})
	t.Run("over_reserve", func(t *testing.T) {
		fv := mustLoadFailure(t, testvectors.TradeFailOverReserve)
		assertValidationReject(t, fv)
	})
	t.Run("non_crossing", func(t *testing.T) {
		fv := mustLoadFailure(t, testvectors.TradeFailNonCrossing)
		book, _, verdicts := rebuildBook(t, fv.Market, fv.Funding, fv.Orders)
		for i, v := range verdicts {
			if !v.Accepted {
				t.Fatalf("non-crossing order %d should validate: %s", i, v.Reason)
			}
		}
		fills, err := state.NewMatchingEngine().Match(book, fv.Market)
		if err != nil {
			t.Fatalf("match: %v", err)
		}
		if fv.Expected.FillCount == nil || len(fills) != *fv.Expected.FillCount {
			t.Fatalf("non-crossing expected %v fills, got %d", fv.Expected.FillCount, len(fills))
		}
	})
}

func assertValidationReject(t *testing.T, fv testvectors.TradeFailureVector) {
	t.Helper()
	markets := state.NewMarketRegistry()
	if err := markets.Register(fv.Market); err != nil {
		t.Fatalf("register: %v", err)
	}
	mgr := state.NewOffchainStateManager()
	for i, f := range fv.Funding {
		if _, err := mgr.ApplyDeposit(types.DepositRecord{DepositID: "f" + itoa(i), Owner: f.Owner, Denom: f.Denom, Amount: f.Available}); err != nil {
			t.Fatalf("fund: %v", err)
		}
	}
	v := state.NewOrderValidator(markets, mgr, nil, nil)
	verdict, err := v.Validate(fv.Orders[0], 1_000_000)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if verdict.Accepted {
		t.Fatalf("%s: expected rejection, got accepted", fv.Name)
	}
	if string(verdict.Reason) != fv.Expected.Reason {
		t.Fatalf("%s: reason %q, want %q", fv.Name, verdict.Reason, fv.Expected.Reason)
	}
}

// MANIFEST SHA-256 must match every checked-in file (no silent hand-edits).
func TestTradeVectorsManifestIntegrity(t *testing.T) {
	dir, err := testvectors.TradeScenarioDir()
	if err != nil {
		t.Fatalf("scenario dir: %v", err)
	}
	var m struct {
		Files []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	mustLoad(t, "MANIFEST.json", &m)
	if len(m.Files) == 0 {
		t.Fatal("manifest lists no files")
	}
	for _, f := range m.Files {
		b, err := os.ReadFile(filepath.Join(dir, f.Name))
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		sum := sha256.Sum256(b)
		if got := "0x" + hex.EncodeToString(sum[:]); got != f.SHA256 {
			t.Fatalf("%s sha256 %s != manifest %s (regenerate vectors)", f.Name, got, f.SHA256)
		}
	}
}

// --- helpers ---

func mustLoad(t *testing.T, name string, v any) {
	t.Helper()
	if err := testvectors.LoadTradeVector(name, v); err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
}

func mustLoadFailure(t *testing.T, name string) testvectors.TradeFailureVector {
	t.Helper()
	fv, err := testvectors.LoadTradeFailureVector(name)
	if err != nil {
		t.Fatalf("load failure %s: %v", name, err)
	}
	return fv
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

