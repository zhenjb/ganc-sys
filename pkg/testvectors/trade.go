package testvectors

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T11 — trade test vector schemas + loaders.
//
// The canonical trade scenario (Alice buy / Bob sell, one Fill) and the failure
// vectors live under testvectors/<TradeScenarioName>/. They are the single
// source of truth for P1 (verifier), P2 (prover/circuit) and P4 (backend) to
// cross-check matching, commitments and reject reasons bit-for-bit. Every
// expected value is generated FROM the T05/T07 code (never hand-authored), so
// the cross-check test can re-derive and confirm.

// TradeScenarioName is the folder slug for the canonical trade scenario.
const TradeScenarioName = "trade_alice_bob"

// Canonical trade-vector file names.
const (
	TradeFileMarket           = "market.json"
	TradeFileInitialState     = "initial_state.json"
	TradeFileOrders           = "orders.json"
	TradeFileFills            = "fills.json"
	TradeFileRoots            = "roots.json"
	TradeFileStateAfter       = "state_after_trade.json"
	TradeFileSettlementUpdate = "settlement_update.json"
	TradeFileBatchCommitments = "batch_commitments.json"
	TradeFilePublicInputs     = "public_inputs.json"
	TradeFileWitness          = "witness.json"

	TradeFailureDir          = "failure_vectors"
	TradeFailForgedSignature = "forged_signature.json"
	TradeFailNonCrossing     = "non_crossing.json"
	TradeFailOverReserve     = "over_reserve.json"
)

// TradeBalanceRecord is one account's available/reserved balance for a denom.
type TradeBalanceRecord struct {
	Owner     string `json:"owner"`
	Denom     string `json:"denom"`
	Available string `json:"available"`
	Reserved  string `json:"reserved"`
}

// TradeOrderRecord is a canonical order plus its derived identifiers and the
// validation verdict (STATE-T03).
type TradeOrderRecord struct {
	Order          types.SignedOrder `json:"order"`
	OrderHash      string            `json:"orderHash"`
	OrderNullifier string            `json:"orderNullifier"`
	Sequence       uint64            `json:"sequence"`
	Accepted       bool              `json:"accepted"`
	ReserveDenom   string            `json:"reserveDenom"`
	ReserveAmount  string            `json:"reserveAmount"`
}

// TradeRootsVector holds the batch's four hex roots (STATE-T06/T07/T08).
type TradeRootsVector struct {
	OldStateRoot         string `json:"oldStateRoot"`
	NewStateRoot         string `json:"newStateRoot"`
	OrdersRoot           string `json:"ordersRoot"`
	TradesRoot           string `json:"tradesRoot"`
	TradeBatchCommitment string `json:"tradeBatchCommitment"`
}

// TradePublicInputsVector echoes public_inputs.json (8-input trade layout).
type TradePublicInputsVector struct {
	Count        int      `json:"count"`
	Labels       []string `json:"labels"`
	PublicInputs []string `json:"publicInputs"`
	Note         string   `json:"note,omitempty"`
}

// TradeFailureVector is one negative vector: an input that must be rejected /
// produce no fill, with the expected outcome recorded.
type TradeFailureVector struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Stage       string               `json:"stage"` // validation | matching | reserve
	Market      types.Market         `json:"market"`
	Funding     []TradeBalanceRecord `json:"funding"`
	Orders      []types.SignedOrder  `json:"orders"`
	Expected    TradeFailureExpect   `json:"expected"`
	Note        string               `json:"note,omitempty"`
}

// TradeFailureExpect is the expected outcome of a failure vector.
type TradeFailureExpect struct {
	Accepted  *bool  `json:"accepted,omitempty"`  // validation stage
	Reason    string `json:"reason,omitempty"`    // reject reason code
	FillCount *int   `json:"fillCount,omitempty"` // matching stage
	Error     string `json:"error,omitempty"`     // reserve stage sentinel
}

// TradeScenarioDir returns the absolute path to testvectors/<TradeScenarioName>.
func TradeScenarioDir() (string, error) {
	root, err := FindRepoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "testvectors", TradeScenarioName), nil
}

// LoadTradeVector reads and unmarshals a canonical trade-vector file into v.
func LoadTradeVector(name string, v any) error {
	dir, err := TradeScenarioDir()
	if err != nil {
		return err
	}
	return readTradeJSON(filepath.Join(dir, name), v)
}

// LoadTradeFailureVector reads a failure vector from the failure_vectors subdir.
func LoadTradeFailureVector(name string) (TradeFailureVector, error) {
	dir, err := TradeScenarioDir()
	if err != nil {
		return TradeFailureVector{}, err
	}
	var fv TradeFailureVector
	if err := readTradeJSON(filepath.Join(dir, TradeFailureDir, name), &fv); err != nil {
		return TradeFailureVector{}, err
	}
	return fv, nil
}

func readTradeJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("testvectors: read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("testvectors: parse %s: %w", path, err)
	}
	return nil
}
