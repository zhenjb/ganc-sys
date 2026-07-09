package batch

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T09 — Trade witness builder.
//
// Builds the private witness the P2 prover needs to replay a trade batch: the
// orders (with signature + orderHash) to re-check auth and re-run matching, the
// Fills in matching order, and — crucially — the available AND reserved balances
// BEFORE and AFTER for every buyer/seller/feeAccount per denom. Reserved must be
// present or the circuit cannot prove the lock/consume of collateral (plan
// pitfall).
//
// Self-checks (fail fast, before the prover round-trip):
//   - orderHash re-derived from canonical order fields must match; orderNullifier
//     re-derived from (owner, orderHash) is attached.
//   - every fill references orders present in the witness; buyer/seller have a
//     balance entry.
//   - VALUE CONSERVATION per denom: sum(oldAvailable+oldReserved) ==
//     sum(newAvailable+newReserved). With buyer, seller and feeAccount all in the
//     set this is exactly in = out + fee — a wrong witness is rejected here.
//
// It does NOT re-derive the full state root (ComputeRoot needs the complete
// account set, which the trade witness does not carry); that binding is the
// circuit's job via the merkle paths (ZK-02). The conservation + hash re-derivation
// checks are the local guarantees.

// ErrInvalidTradeWitness is the sentinel for trade-witness validation failures.
var ErrInvalidTradeWitness = errors.New("batch: invalid trade witness inputs")

// TradeWitnessBalanceInput is the available/reserved old+new for one (owner,
// denom). All four amounts are required non-negative integer strings.
type TradeWitnessBalanceInput struct {
	Owner        string
	Denom        string
	OldAvailable string
	OldReserved  string
	NewAvailable string
	NewReserved  string
}

// TradeWitnessOrderInput carries an order plus its post-batch state. orderHash /
// orderNullifier are derived by the builder (never trusted from the caller).
type TradeWitnessOrderInput struct {
	Order      types.SignedOrder
	Filled     bool
	Remaining  string
	MerklePath []string
}

// TradeWitnessInputs is the batch-shaped input for the trade witness.
type TradeWitnessInputs struct {
	OldStateRoot string
	NewStateRoot string
	OrdersRoot   string
	TradesRoot   string
	Fills        []types.Fill
	Orders       []TradeWitnessOrderInput
	Balances     []TradeWitnessBalanceInput
}

// TradeWitnessBuilder produces a types.TradeWitness. Stateless.
type TradeWitnessBuilder struct{}

// NewTradeWitnessBuilder returns a builder.
func NewTradeWitnessBuilder() *TradeWitnessBuilder { return &TradeWitnessBuilder{} }

// Build validates the inputs and assembles the trade witness.
func (b *TradeWitnessBuilder) Build(in TradeWitnessInputs) (types.TradeWitness, error) {
	if err := validateRoot(in.OldStateRoot, "trade witness oldStateRoot"); err != nil {
		return types.TradeWitness{}, wrapTradeWitnessErr(err)
	}
	if err := validateRoot(in.NewStateRoot, "trade witness newStateRoot"); err != nil {
		return types.TradeWitness{}, wrapTradeWitnessErr(err)
	}
	if in.OldStateRoot == in.NewStateRoot {
		return types.TradeWitness{}, fmt.Errorf("%w: oldStateRoot == newStateRoot", ErrInvalidTradeWitness)
	}
	if err := validateHex(in.OrdersRoot, "trade witness ordersRoot"); err != nil {
		return types.TradeWitness{}, wrapTradeWitnessErr(err)
	}
	if err := validateHex(in.TradesRoot, "trade witness tradesRoot"); err != nil {
		return types.TradeWitness{}, wrapTradeWitnessErr(err)
	}
	if len(in.Fills) == 0 {
		return types.TradeWitness{}, fmt.Errorf("%w: no fills", ErrInvalidTradeWitness)
	}
	if _, err := validateFills(in.Fills); err != nil {
		return types.TradeWitness{}, err
	}

	// --- Orders: derive orderHash + nullifier, index by hash. ---
	orders := make([]types.TradeWitnessOrder, 0, len(in.Orders))
	orderByHash := make(map[string]struct{}, len(in.Orders))
	for i, oi := range in.Orders {
		if err := oi.Order.Validate(); err != nil {
			return types.TradeWitness{}, fmt.Errorf("%w: orders[%d]: %v", ErrInvalidTradeWitness, i, err)
		}
		orderHash, err := state.OrderHash(oi.Order)
		if err != nil {
			return types.TradeWitness{}, fmt.Errorf("%w: orders[%d] orderHash: %v", ErrInvalidTradeWitness, i, err)
		}
		nullifier, err := state.OrderNullifierFor(oi.Order.Owner, orderHash)
		if err != nil {
			return types.TradeWitness{}, fmt.Errorf("%w: orders[%d] nullifier: %v", ErrInvalidTradeWitness, i, err)
		}
		if _, dup := orderByHash[orderHash]; dup {
			return types.TradeWitness{}, fmt.Errorf("%w: orders[%d] duplicate orderHash %s", ErrInvalidTradeWitness, i, orderHash)
		}
		orderByHash[orderHash] = struct{}{}
		orders = append(orders, types.TradeWitnessOrder{
			OrderHash:      orderHash,
			OrderNullifier: nullifier,
			Owner:          strings.TrimSpace(oi.Order.Owner),
			Market:         strings.TrimSpace(oi.Order.Market),
			Side:           oi.Order.Side,
			Price:          oi.Order.Price,
			Qty:            oi.Order.Qty,
			Expiry:         oi.Order.Expiry,
			Nonce:          oi.Order.Nonce,
			Signature:      oi.Order.Signature,
			Filled:         oi.Filled,
			Remaining:      oi.Remaining,
			MerklePath:     append([]string(nil), oi.MerklePath...),
		})
	}

	// --- Balances: parse, index owners, conservation per denom. ---
	balances := make([]types.TradeWitnessBalance, 0, len(in.Balances))
	ownerSet := make(map[string]struct{}, len(in.Balances))
	oldByDenom := map[string]*big.Int{}
	newByDenom := map[string]*big.Int{}
	for i, bi := range in.Balances {
		owner := strings.TrimSpace(bi.Owner)
		denom := strings.TrimSpace(bi.Denom)
		if owner == "" || denom == "" {
			return types.TradeWitness{}, fmt.Errorf("%w: balances[%d] owner/denom empty", ErrInvalidTradeWitness, i)
		}
		oa, err := parseNonNegative(bi.OldAvailable)
		if err != nil {
			return types.TradeWitness{}, fmt.Errorf("%w: balances[%d].oldAvailable %q: %v", ErrInvalidTradeWitness, i, bi.OldAvailable, err)
		}
		or, err := parseNonNegative(bi.OldReserved)
		if err != nil {
			return types.TradeWitness{}, fmt.Errorf("%w: balances[%d].oldReserved %q: %v", ErrInvalidTradeWitness, i, bi.OldReserved, err)
		}
		na, err := parseNonNegative(bi.NewAvailable)
		if err != nil {
			return types.TradeWitness{}, fmt.Errorf("%w: balances[%d].newAvailable %q: %v", ErrInvalidTradeWitness, i, bi.NewAvailable, err)
		}
		nr, err := parseNonNegative(bi.NewReserved)
		if err != nil {
			return types.TradeWitness{}, fmt.Errorf("%w: balances[%d].newReserved %q: %v", ErrInvalidTradeWitness, i, bi.NewReserved, err)
		}
		ownerSet[owner] = struct{}{}

		addTo(oldByDenom, denom, new(big.Int).Add(oa, or))
		addTo(newByDenom, denom, new(big.Int).Add(na, nr))

		balances = append(balances, types.TradeWitnessBalance{
			Owner:        owner,
			Denom:        denom,
			OldAvailable: oa.String(),
			OldReserved:  or.String(),
			NewAvailable: na.String(),
			NewReserved:  nr.String(),
		})
	}

	// Conservation: per denom, total (available+reserved) before == after.
	for denom, oldTotal := range oldByDenom {
		newTotal, ok := newByDenom[denom]
		if !ok || oldTotal.Cmp(newTotal) != 0 {
			return types.TradeWitness{}, fmt.Errorf(
				"%w: value not conserved for denom %q: old total %s != new total %v (feeAccount balances missing?)",
				ErrInvalidTradeWitness, denom, oldTotal, newByDenom[denom])
		}
	}

	// --- Cross-refs: each fill's orders/parties are represented. ---
	for i, f := range in.Fills {
		if _, ok := orderByHash[f.MakerOrderHash]; !ok {
			return types.TradeWitness{}, fmt.Errorf("%w: fills[%d] makerOrderHash %s not in witness orders", ErrInvalidTradeWitness, i, f.MakerOrderHash)
		}
		if _, ok := orderByHash[f.TakerOrderHash]; !ok {
			return types.TradeWitness{}, fmt.Errorf("%w: fills[%d] takerOrderHash %s not in witness orders", ErrInvalidTradeWitness, i, f.TakerOrderHash)
		}
		if _, ok := ownerSet[f.Buyer]; !ok {
			return types.TradeWitness{}, fmt.Errorf("%w: fills[%d] buyer %s has no balance entry", ErrInvalidTradeWitness, i, f.Buyer)
		}
		if _, ok := ownerSet[f.Seller]; !ok {
			return types.TradeWitness{}, fmt.Errorf("%w: fills[%d] seller %s has no balance entry", ErrInvalidTradeWitness, i, f.Seller)
		}
	}

	return types.TradeWitness{
		OldStateRoot: in.OldStateRoot,
		NewStateRoot: in.NewStateRoot,
		OrdersRoot:   in.OrdersRoot,
		TradesRoot:   in.TradesRoot,
		Balances:     balances,
		Orders:       orders,
		Fills:        append([]types.Fill(nil), in.Fills...),
	}, nil
}

// BuildTradeWitness is the stateless top-level helper.
func BuildTradeWitness(in TradeWitnessInputs) (types.TradeWitness, error) {
	return (&TradeWitnessBuilder{}).Build(in)
}

func addTo(m map[string]*big.Int, key string, v *big.Int) {
	if cur, ok := m[key]; ok {
		cur.Add(cur, v)
		return
	}
	m[key] = v
}

func wrapTradeWitnessErr(cause error) error {
	return fmt.Errorf("%w: %s", ErrInvalidTradeWitness, cause.Error())
}
