package state

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T07 — Order nullifier + commitments.
//
// The extended proof public inputs add [6]=tradesRoot and [7]=ordersRoot. D
// computes these commitments DETERMINISTICALLY so P2 can bind them in-circuit
// and P1 can verify them on-chain. orderNullifier = Hash(owner, orderHash)
// (STATE-T03) prevents reusing a filled/cancelled order; ordersRoot binds every
// order's nullifier + filled status, tradesRoot binds the Fill sequence.
//
// Encoding mirrors internal/batch/commitments.go EXACTLY so a trade root is
// derived the same way as the core deposit/withdrawal roots:
//
//	root = SHA256( domain | for each entry: f1|f2|...|fn; )
//
// fields joined by '|', entries terminated by ';'. MVP placeholder hash is
// SHA-256; when ZK-T01 locks the circuit hash (MiMC/Poseidon) bump the version
// tags in lockstep and regenerate the trade vectors (plan pitfall: P3 and the
// P2 circuit MUST use the same hash + domain separation or the roots diverge).

const (
	ordersRootDomainTag = "zkdex/batch/ordersRoot/v0"
	tradesRootDomainTag = "zkdex/batch/tradesRoot/v0"
)

// ErrDuplicateOrderNullifier is returned when two orders in a batch derive the
// same orderNullifier (same owner+orderHash) — a replay that must be caught
// before committing. Sentinel — errors.Is.
var ErrDuplicateOrderNullifier = errors.New("state: duplicate order nullifier in batch")

// OrderCommitmentInput is one order's post-batch state fed into ordersRoot. The
// orderNullifier is derived internally from (Owner, OrderHash) so callers cannot
// supply an inconsistent value.
type OrderCommitmentInput struct {
	OrderHash string          `json:"orderHash"`
	Owner     string          `json:"owner"`
	Side      types.OrderSide `json:"side"`
	Price     string          `json:"price"`
	Qty       string          `json:"qty"`
	Remaining string          `json:"remaining"`
	Filled    bool            `json:"filled"`
	Sequence  uint64          `json:"sequence"` // book receive order (T04) — deterministic leaf ordering
}

// TradeCommitments is the STATE-T07 deliverable: the two extended-public-input
// roots plus the batch's consumed order-nullifier list.
type TradeCommitments struct {
	OrdersRoot      string   `json:"ordersRoot"`
	TradesRoot      string   `json:"tradesRoot"`
	OrderNullifiers []string `json:"orderNullifiers"` // sorted, unique
}

// BuildTradeCommitments derives ordersRoot, tradesRoot and the sorted unique
// order-nullifier list for a batch. Orders are committed in a fixed order
// (Sequence, then OrderHash) regardless of input order; fills are committed in
// the given matching order (do NOT reorder — the sequence is the proof). Returns
// ErrDuplicateOrderNullifier if two orders share a nullifier.
func BuildTradeCommitments(orders []OrderCommitmentInput, fills []types.Fill) (TradeCommitments, error) {
	ordersRoot, nullifiers, err := ordersRootAndNullifiers(orders)
	if err != nil {
		return TradeCommitments{}, err
	}
	return TradeCommitments{
		OrdersRoot:      ordersRoot,
		TradesRoot:      TradesRoot(fills),
		OrderNullifiers: nullifiers,
	}, nil
}

// OrdersRoot computes the commitment over a batch's orders (deterministic leaf
// order). Exposed for callers that only need the root.
func OrdersRoot(orders []OrderCommitmentInput) (string, error) {
	root, _, err := ordersRootAndNullifiers(orders)
	return root, err
}

func ordersRootAndNullifiers(orders []OrderCommitmentInput) (string, []string, error) {
	// Copy + sort by (Sequence, OrderHash) so the leaf order is fixed and
	// independent of how the caller passes the slice.
	sorted := make([]OrderCommitmentInput, len(orders))
	copy(sorted, orders)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Sequence != sorted[j].Sequence {
			return sorted[i].Sequence < sorted[j].Sequence
		}
		return sorted[i].OrderHash < sorted[j].OrderHash
	})

	seen := make(map[string]struct{}, len(sorted))
	nullifiers := make([]string, 0, len(sorted))

	var b strings.Builder
	b.WriteString(ordersRootDomainTag)
	b.WriteByte('|')
	for _, o := range sorted {
		nullifier, err := OrderNullifierFor(o.Owner, o.OrderHash)
		if err != nil {
			return "", nil, err
		}
		if _, dup := seen[nullifier]; dup {
			return "", nil, fmt.Errorf("%w: %s", ErrDuplicateOrderNullifier, nullifier)
		}
		seen[nullifier] = struct{}{}
		nullifiers = append(nullifiers, nullifier)

		b.WriteString(o.OrderHash)
		b.WriteByte('|')
		b.WriteString(strings.TrimSpace(o.Owner))
		b.WriteByte('|')
		b.WriteString(nullifier)
		b.WriteByte('|')
		b.WriteString(string(o.Side))
		b.WriteByte('|')
		b.WriteString(o.Price)
		b.WriteByte('|')
		b.WriteString(o.Qty)
		b.WriteByte('|')
		b.WriteString(o.Remaining)
		b.WriteByte('|')
		b.WriteString(strconv.FormatBool(o.Filled))
		b.WriteByte(';')
	}

	sort.Strings(nullifiers)
	return hash.SHA256Hex([]byte(b.String())), nullifiers, nil
}

// TradesRoot computes the commitment over a batch's Fills, IN THE GIVEN ORDER
// (the deterministic matching order from STATE-T05). Reordering the fills
// changes the root by design.
func TradesRoot(fills []types.Fill) string {
	var b strings.Builder
	b.WriteString(tradesRootDomainTag)
	b.WriteByte('|')
	for _, f := range fills {
		b.WriteString(f.TradeID)
		b.WriteByte('|')
		b.WriteString(f.Market)
		b.WriteByte('|')
		b.WriteString(f.MakerOrderHash)
		b.WriteByte('|')
		b.WriteString(f.TakerOrderHash)
		b.WriteByte('|')
		b.WriteString(f.Price)
		b.WriteByte('|')
		b.WriteString(f.Qty)
		b.WriteByte('|')
		b.WriteString(f.MakerFee)
		b.WriteByte('|')
		b.WriteString(f.TakerFee)
		b.WriteByte('|')
		b.WriteString(f.Buyer)
		b.WriteByte('|')
		b.WriteString(f.Seller)
		b.WriteByte(';')
	}
	return hash.SHA256Hex([]byte(b.String()))
}

// EmptyOrdersRoot / EmptyTradesRoot are the roots of an empty batch. They are
// the sentinel values public inputs [6]/[7] carry when a batch has no trades, so
// a core (deposit/withdraw-only) proof still verifies against the fixed 8-input
// layout (ZK append-not-reorder agreement).
func EmptyOrdersRoot() string {
	root, _, _ := ordersRootAndNullifiers(nil)
	return root
}

func EmptyTradesRoot() string {
	return TradesRoot(nil)
}

// TradeCommitmentDomainTags exposes the domain tags for cross-role assertion (P2
// circuit, P1 verifier) that the off-chain derivation has not silently bumped.
func TradeCommitmentDomainTags() (ordersRoot, tradesRoot string) {
	return ordersRootDomainTag, tradesRootDomainTag
}
