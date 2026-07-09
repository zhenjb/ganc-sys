package state

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T04 — Orderbook structure.
//
// A per-market book of resting orders with strict PRICE-TIME priority. It is the
// deterministic data source the matching engine (STATE-T05) iterates: given the
// same sequence of inserts it always produces the same best-order ordering, so a
// replay (P2 proving) yields the identical fill sequence.
//
// Determinism rules (plan pitfalls):
//   - The match loop NEVER iterates a Go map (map order is randomized). Levels
//     are kept in a sorted key slice; orders within a level in a FIFO slice.
//   - Priority is the TOTAL order (price, sequence): price first, then the
//     internal monotonic receive sequence — no ties, no clock, no randomness.
//
// Price/qty are exact decimals (STATE-T03 decimal.go); collateral amounts are
// integer smallest-units. The book couples to reserved balance (STATE-T02) via
// an optional controller: Insert reserves, Cancel releases.

var (
	// ErrOrderNotAccepted is returned when Insert is given a verdict that did
	// not pass validation (STATE-T03). Sentinel — errors.Is.
	ErrOrderNotAccepted = errors.New("orderbook: order was not accepted by validation")
	// ErrWrongMarket is returned when an order's market does not match the book.
	ErrWrongMarket = errors.New("orderbook: order market does not match book")
	// ErrOrderExists is returned when inserting an orderHash already in the book.
	ErrOrderExists = errors.New("orderbook: order already in book")
	// ErrOrderNotFound is returned by Cancel/Reduce/RemainingQty for an unknown
	// orderHash. Sentinel — errors.Is.
	ErrOrderNotFound = errors.New("orderbook: order not found")
	// ErrOrderOwnerMismatch is returned by CancelOwned when the requester is not
	// the order's owner (INT-T03: block cancelling someone else's order).
	// Sentinel — errors.Is.
	ErrOrderOwnerMismatch = errors.New("orderbook: order owner mismatch")
	// ErrInvalidFill is returned when a reduce/fill qty is non-positive or
	// exceeds the order's remaining. Sentinel — errors.Is.
	ErrInvalidFill = errors.New("orderbook: invalid fill quantity")
)

// ReservationController is the STATE-T02 surface the book uses to lock collateral
// on insert and release it on cancel. *OffchainStateManager satisfies it. May be
// nil (book-only / structural use, e.g. inside the matching engine which manages
// reservations itself).
type ReservationController interface {
	Reserve(owner, denom, amount, orderHash string) (string, error)
	ReleaseOrder(orderHash string) (string, error)
}

// OrderNullifierMarker records an order's nullifier as consumed on cancel (so a
// cancelled order cannot be replayed — STATE-T03). *InMemoryOrderNullifiers
// satisfies it. May be nil.
type OrderNullifierMarker interface {
	MarkUsed(nullifier string)
}

// RestingOrder is an immutable view of a live order in the book. Best/Snapshot
// return copies so callers cannot mutate book state directly (use Reduce/Cancel).
type RestingOrder struct {
	OrderHash      string          `json:"orderHash"`
	OrderNullifier string          `json:"orderNullifier"`
	Owner          string          `json:"owner"`
	Side           types.OrderSide `json:"side"`
	Price          string          `json:"price"`
	Qty            string          `json:"qty"`
	Remaining      string          `json:"remaining"`
	Sequence       uint64          `json:"sequence"`
	ReserveDenom   string          `json:"reserveDenom"`
}

// restingOrder is the mutable internal record (Remaining changes on partial fill).
type restingOrder struct {
	view     RestingOrder
	priceKey string
}

type priceLevel struct {
	key    string
	price  decimal
	orders []*restingOrder // FIFO by sequence
}

// bookSide holds all resting orders of one side (bids or asks). Levels are kept
// in sortedKeys ASCENDING by price; the side determines which end is "best"
// (bids: highest price; asks: lowest price). Within a level, orders are FIFO
// (earliest sequence first).
type bookSide struct {
	side       types.OrderSide
	levels     map[string]*priceLevel
	sortedKeys []string
	index      map[string]*restingOrder // orderHash -> order
}

func newBookSide(side types.OrderSide) *bookSide {
	return &bookSide{
		side:   side,
		levels: make(map[string]*priceLevel),
		index:  make(map[string]*restingOrder),
	}
}

func (s *bookSide) insert(ro *restingOrder, price decimal) {
	level, ok := s.levels[ro.priceKey]
	if !ok {
		level = &priceLevel{key: ro.priceKey, price: price}
		s.levels[ro.priceKey] = level
		// Binary-insert the key so sortedKeys stays ascending by price.
		i := sort.Search(len(s.sortedKeys), func(i int) bool {
			return cmpDecimal(s.levels[s.sortedKeys[i]].price, price) >= 0
		})
		s.sortedKeys = append(s.sortedKeys, "")
		copy(s.sortedKeys[i+1:], s.sortedKeys[i:])
		s.sortedKeys[i] = ro.priceKey
	}
	level.orders = append(level.orders, ro)
	s.index[ro.view.OrderHash] = ro
}

// best returns the top-priority order of this side, or nil if empty.
func (s *bookSide) best() *restingOrder {
	if len(s.sortedKeys) == 0 {
		return nil
	}
	var key string
	if s.side == types.SideBuy {
		key = s.sortedKeys[len(s.sortedKeys)-1] // highest price
	} else {
		key = s.sortedKeys[0] // lowest price
	}
	level := s.levels[key]
	if len(level.orders) == 0 {
		return nil
	}
	return level.orders[0] // earliest sequence
}

func (s *bookSide) remove(orderHash string) bool {
	ro, ok := s.index[orderHash]
	if !ok {
		return false
	}
	level := s.levels[ro.priceKey]
	for i, o := range level.orders {
		if o.view.OrderHash == orderHash {
			level.orders = append(level.orders[:i], level.orders[i+1:]...)
			break
		}
	}
	if len(level.orders) == 0 {
		delete(s.levels, ro.priceKey)
		for i, k := range s.sortedKeys {
			if k == ro.priceKey {
				s.sortedKeys = append(s.sortedKeys[:i], s.sortedKeys[i+1:]...)
				break
			}
		}
	}
	delete(s.index, orderHash)
	return true
}

// ordered returns this side's resting orders in priority order (best first).
func (s *bookSide) ordered() []RestingOrder {
	out := make([]RestingOrder, 0, len(s.index))
	if s.side == types.SideBuy {
		for i := len(s.sortedKeys) - 1; i >= 0; i-- {
			for _, o := range s.levels[s.sortedKeys[i]].orders {
				out = append(out, o.view)
			}
		}
	} else {
		for _, k := range s.sortedKeys {
			for _, o := range s.levels[k].orders {
				out = append(out, o.view)
			}
		}
	}
	return out
}

// depthLevels aggregates this side into price levels in priority order (best
// first): one level per distinct price, Qty = sum of the resting orders'
// Remaining at that price. Empty levels are skipped. Used by GET /api/orderbook
// (INT-T02/INT-T04) to render depth without exposing individual orders.
func (s *bookSide) depthLevels() []types.PriceLevel {
	out := make([]types.PriceLevel, 0, len(s.sortedKeys))
	emit := func(level *priceLevel) {
		sum := decimal{mant: new(big.Int), scale: 0}
		for _, o := range level.orders {
			rem, err := parsePositiveDecimal(o.view.Remaining)
			if err != nil {
				continue // a resting order always has a positive remaining; skip defensively
			}
			sum = addDecimal(sum, rem)
		}
		if isZeroDecimal(sum) {
			return
		}
		out = append(out, types.PriceLevel{Price: level.price.String(), Qty: sum.String()})
	}
	if s.side == types.SideBuy {
		for i := len(s.sortedKeys) - 1; i >= 0; i-- { // highest price first
			emit(s.levels[s.sortedKeys[i]])
		}
	} else {
		for _, k := range s.sortedKeys { // lowest price first
			emit(s.levels[k])
		}
	}
	return out
}

// Orderbook is the per-market price-time book. Thread-safe.
type Orderbook struct {
	mu          sync.Mutex
	market      string
	bids        *bookSide
	asks        *bookSide
	seq         uint64
	reservation ReservationController
	nullifiers  OrderNullifierMarker
}

// NewOrderbook builds an empty book for a market. reservation/nullifiers may be
// nil (structural use); when set, Insert reserves collateral and Cancel releases
// it and marks the order nullifier consumed.
func NewOrderbook(market string, reservation ReservationController, nullifiers OrderNullifierMarker) *Orderbook {
	return &Orderbook{
		market:      strings.TrimSpace(market),
		bids:        newBookSide(types.SideBuy),
		asks:        newBookSide(types.SideSell),
		reservation: reservation,
		nullifiers:  nullifiers,
	}
}

// Market returns the book's market id.
func (b *Orderbook) Market() string { return b.market }

// Insert rests a validated order into the book with PRICE-TIME priority. It
// accepts ONLY an order whose verdict.Accepted is true (STATE-T03). When a
// reservation controller is set, it first locks the verdict's collateral
// (available -> reserved); if that fails the order is NOT added. Returns the
// resting-order view.
func (b *Orderbook) Insert(order types.SignedOrder, verdict OrderValidation) (RestingOrder, error) {
	if !verdict.Accepted {
		return RestingOrder{}, fmt.Errorf("%w: %s", ErrOrderNotAccepted, verdict.Reason)
	}
	if strings.TrimSpace(order.Market) != b.market {
		return RestingOrder{}, fmt.Errorf("%w: order market %q, book %q", ErrWrongMarket, order.Market, b.market)
	}
	if !order.Side.IsValid() {
		return RestingOrder{}, fmt.Errorf("%w: side %q", types.ErrInvalidOrder, order.Side)
	}
	price, err := parsePositiveDecimal(order.Price)
	if err != nil {
		return RestingOrder{}, fmt.Errorf("orderbook: price %q: %w", order.Price, err)
	}
	if _, err := parsePositiveDecimal(order.Qty); err != nil {
		return RestingOrder{}, fmt.Errorf("orderbook: qty %q: %w", order.Qty, err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.side(order.Side).index[verdict.OrderHash]; exists {
		return RestingOrder{}, fmt.Errorf("%w: %s", ErrOrderExists, verdict.OrderHash)
	}

	// Lock collateral before resting the order. If Reserve fails (e.g. someone
	// else drained available), the order is rejected and the book is untouched.
	if b.reservation != nil {
		if _, err := b.reservation.Reserve(order.Owner, verdict.ReserveDenom, verdict.ReserveAmount, verdict.OrderHash); err != nil {
			return RestingOrder{}, fmt.Errorf("orderbook: reserve for %s: %w", verdict.OrderHash, err)
		}
	}

	b.seq++
	ro := &restingOrder{
		priceKey: price.String(),
		view: RestingOrder{
			OrderHash:      verdict.OrderHash,
			OrderNullifier: verdict.OrderNullifier,
			Owner:          strings.TrimSpace(order.Owner),
			Side:           order.Side,
			Price:          order.Price,
			Qty:            order.Qty,
			Remaining:      order.Qty,
			Sequence:       b.seq,
			ReserveDenom:   verdict.ReserveDenom,
		},
	}
	b.side(order.Side).insert(ro, price)
	return ro.view, nil
}

// Cancel removes an order from the book and, when wired, releases its remaining
// reserved collateral (STATE-T02) and marks its nullifier consumed (STATE-T03)
// so it cannot be replayed. Returns ErrOrderNotFound for an unknown orderHash.
func (b *Orderbook) Cancel(orderHash string) error {
	orderHash = strings.TrimSpace(orderHash)

	b.mu.Lock()
	defer b.mu.Unlock()

	ro := b.lookupLocked(orderHash)
	if ro == nil {
		return fmt.Errorf("%w: %s", ErrOrderNotFound, orderHash)
	}
	nullifier := ro.view.OrderNullifier

	if !b.side(ro.view.Side).remove(orderHash) {
		return fmt.Errorf("%w: %s", ErrOrderNotFound, orderHash)
	}
	if b.reservation != nil {
		if _, err := b.reservation.ReleaseOrder(orderHash); err != nil {
			return fmt.Errorf("orderbook: release on cancel %s: %w", orderHash, err)
		}
	}
	if b.nullifiers != nil && nullifier != "" {
		b.nullifiers.MarkUsed(nullifier)
	}
	return nil
}

// CancelOwned is Cancel with an ownership guard (INT-T03). It cancels orderHash
// only if it belongs to owner, doing the lookup, ownership check and removal
// under ONE lock so a concurrent match/cancel cannot slip between the check and
// the removal (the plan's INT-T05 race pitfall). On success it releases the
// order's remaining reserved collateral (available += remaining reservation) and
// marks its nullifier consumed so it cannot be replayed, then returns the
// cancelled resting-order view (whose Remaining is the just-released qty).
// Returns ErrOrderNotFound for an unknown hash, ErrOrderOwnerMismatch if owner
// does not match.
func (b *Orderbook) CancelOwned(orderHash, owner string) (RestingOrder, error) {
	orderHash = strings.TrimSpace(orderHash)
	owner = strings.TrimSpace(owner)

	b.mu.Lock()
	defer b.mu.Unlock()

	ro := b.lookupLocked(orderHash)
	if ro == nil {
		return RestingOrder{}, fmt.Errorf("%w: %s", ErrOrderNotFound, orderHash)
	}
	if ro.view.Owner != owner {
		return RestingOrder{}, fmt.Errorf("%w: %s", ErrOrderOwnerMismatch, orderHash)
	}
	view := ro.view
	nullifier := ro.view.OrderNullifier

	if !b.side(ro.view.Side).remove(orderHash) {
		return RestingOrder{}, fmt.Errorf("%w: %s", ErrOrderNotFound, orderHash)
	}
	if b.reservation != nil {
		if _, err := b.reservation.ReleaseOrder(orderHash); err != nil {
			return RestingOrder{}, fmt.Errorf("orderbook: release on cancel %s: %w", orderHash, err)
		}
	}
	if b.nullifiers != nil && nullifier != "" {
		b.nullifiers.MarkUsed(nullifier)
	}
	return view, nil
}

// Reduce decrements an order's remaining quantity by fillQty (a partial fill).
// Priority is preserved (the order keeps its price/sequence slot). When the
// order becomes fully filled (remaining hits 0) it is removed from the book and
// removed=true is returned. The matching engine (STATE-T05) drives this; it does
// NOT release reservation here — filled collateral is consumed by STATE-T06.
// Returns ErrOrderNotFound / ErrInvalidFill on bad input.
func (b *Orderbook) Reduce(orderHash, fillQty string) (remaining string, removed bool, err error) {
	orderHash = strings.TrimSpace(orderHash)
	fill, derr := parsePositiveDecimal(fillQty)
	if derr != nil {
		return "", false, fmt.Errorf("%w: %q", ErrInvalidFill, fillQty)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	ro := b.lookupLocked(orderHash)
	if ro == nil {
		return "", false, fmt.Errorf("%w: %s", ErrOrderNotFound, orderHash)
	}
	rem, derr := parsePositiveDecimal(ro.view.Remaining)
	if derr != nil {
		return "", false, fmt.Errorf("orderbook: corrupt remaining %q: %w", ro.view.Remaining, derr)
	}
	if cmpDecimal(fill, rem) > 0 {
		return "", false, fmt.Errorf("%w: fill %s > remaining %s", ErrInvalidFill, fillQty, ro.view.Remaining)
	}

	newRem := subDecimal(rem, fill)
	if isZeroDecimal(newRem) {
		b.side(ro.view.Side).remove(orderHash)
		return "0", true, nil
	}
	ro.view.Remaining = newRem.String()
	return ro.view.Remaining, false, nil
}

// BestBid returns the highest-price, earliest bid, or ok=false if none.
func (b *Orderbook) BestBid() (RestingOrder, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ro := b.bids.best(); ro != nil {
		return ro.view, true
	}
	return RestingOrder{}, false
}

// BestAsk returns the lowest-price, earliest ask, or ok=false if none.
func (b *Orderbook) BestAsk() (RestingOrder, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ro := b.asks.best(); ro != nil {
		return ro.view, true
	}
	return RestingOrder{}, false
}

// RemainingQty returns the unfilled quantity of orderHash, or ErrOrderNotFound.
func (b *Orderbook) RemainingQty(orderHash string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ro := b.lookupLocked(strings.TrimSpace(orderHash)); ro != nil {
		return ro.view.Remaining, nil
	}
	return "", fmt.Errorf("%w: %s", ErrOrderNotFound, orderHash)
}

// BookSnapshot is a deterministic, priority-ordered view of the whole book.
type BookSnapshot struct {
	Market string         `json:"market"`
	Bids   []RestingOrder `json:"bids"` // highest price first, then earliest
	Asks   []RestingOrder `json:"asks"` // lowest price first, then earliest
}

// Snapshot returns the full book in priority order. Two books built from the
// same insert sequence produce byte-identical snapshots (DoD: reproducibility).
func (b *Orderbook) Snapshot() BookSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BookSnapshot{
		Market: b.market,
		Bids:   b.bids.ordered(),
		Asks:   b.asks.ordered(),
	}
}

// Depth returns the aggregated price-level view of the book in priority order
// (bids highest-first, asks lowest-first), with bestBid/bestAsk the top-of-book
// prices ("" when a side is empty). bids/asks are always non-nil (possibly
// empty) so they JSON-encode as [] not null. This is the read model behind
// GET /api/orderbook/{market} (INT-T02).
func (b *Orderbook) Depth() (bids, asks []types.PriceLevel, bestBid, bestAsk string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bids = b.bids.depthLevels()
	asks = b.asks.depthLevels()
	if len(bids) > 0 {
		bestBid = bids[0].Price
	}
	if len(asks) > 0 {
		bestAsk = asks[0].Price
	}
	return bids, asks, bestBid, bestAsk
}

// OrderbookSnapshot is a deep, restorable snapshot of a book's internal state
// (STATE-T10). Unlike BookSnapshot (a read-only display view), this captures the
// exact resting set + sequence counter so the book can be rolled back to it.
type OrderbookSnapshot struct {
	market string
	seq    uint64
	orders []RestingOrder // deep value copies, sorted by Sequence
}

// Market returns the snapshot's market id.
func (s OrderbookSnapshot) Market() string { return s.market }

// Len returns the number of resting orders in the snapshot.
func (s OrderbookSnapshot) Len() int { return len(s.orders) }

// Capture takes a deep snapshot of the book (STATE-T10 baseline). RestingOrder
// is a pure value type, so the copied slice is independent of subsequent book
// mutations (Reduce/Cancel).
func (b *Orderbook) Capture() OrderbookSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	orders := make([]RestingOrder, 0, len(b.bids.index)+len(b.asks.index))
	for _, ro := range b.bids.index {
		orders = append(orders, ro.view)
	}
	for _, ro := range b.asks.index {
		orders = append(orders, ro.view)
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i].Sequence < orders[j].Sequence })
	return OrderbookSnapshot{market: b.market, seq: b.seq, orders: orders}
}

// Restore replaces the book's resting set + sequence with the snapshot,
// rebuilding price-time priority. It does NOT touch the reservation controller
// (collateral is restored separately by the manager rollback) — re-inserting
// here must not re-reserve. Idempotent: restoring the same snapshot twice yields
// the same book.
func (b *Orderbook) Restore(snap OrderbookSnapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.bids = newBookSide(types.SideBuy)
	b.asks = newBookSide(types.SideSell)
	b.seq = snap.seq
	// snap.orders is sorted by Sequence, so appending rebuilds FIFO order within
	// each price level correctly.
	for _, v := range snap.orders {
		price, err := parsePositiveDecimal(v.Price)
		if err != nil {
			continue // a captured order always had a valid price; skip defensively
		}
		ro := &restingOrder{view: v, priceKey: price.String()}
		b.side(v.Side).insert(ro, price)
	}
}

func (b *Orderbook) side(side types.OrderSide) *bookSide {
	if side == types.SideBuy {
		return b.bids
	}
	return b.asks
}

func (b *Orderbook) lookupLocked(orderHash string) *restingOrder {
	if ro, ok := b.bids.index[orderHash]; ok {
		return ro
	}
	if ro, ok := b.asks.index[orderHash]; ok {
		return ro
	}
	return nil
}

// ---------------------------------------------------------------------------
// BookSet — per-market registry of orderbooks (P4 holds one set for all
// markets). Thread-safe get-or-create.
// ---------------------------------------------------------------------------

// BookSet maps market id -> *Orderbook, sharing one reservation controller and
// nullifier marker across all books.
type BookSet struct {
	mu          sync.Mutex
	books       map[string]*Orderbook
	reservation ReservationController
	nullifiers  OrderNullifierMarker
}

// NewBookSet builds an empty set. reservation/nullifiers are passed to every
// book it creates.
func NewBookSet(reservation ReservationController, nullifiers OrderNullifierMarker) *BookSet {
	return &BookSet{
		books:       make(map[string]*Orderbook),
		reservation: reservation,
		nullifiers:  nullifiers,
	}
}

// Book returns the orderbook for market, creating it on first use.
func (s *BookSet) Book(market string) *Orderbook {
	market = strings.TrimSpace(market)
	s.mu.Lock()
	defer s.mu.Unlock()
	if bk, ok := s.books[market]; ok {
		return bk
	}
	bk := NewOrderbook(market, s.reservation, s.nullifiers)
	s.books[market] = bk
	return bk
}

// CancelOwned locates orderHash across every book in the set and cancels it if
// owner matches, returning the cancelled view and the market it was in. The
// DELETE /api/order/{id} route carries no market, so P4 cancels by hash alone
// (INT-T03). It snapshots the book list under the set lock, then delegates to
// each book's own lock — so a per-book cancel never runs while holding the set
// lock. Returns ErrOrderNotFound if no book holds the order, or whatever the
// owning book returns (e.g. ErrOrderOwnerMismatch).
func (s *BookSet) CancelOwned(orderHash, owner string) (RestingOrder, string, error) {
	s.mu.Lock()
	books := make([]*Orderbook, 0, len(s.books))
	markets := make([]string, 0, len(s.books))
	for m, bk := range s.books {
		books = append(books, bk)
		markets = append(markets, m)
	}
	s.mu.Unlock()

	for i, bk := range books {
		view, err := bk.CancelOwned(orderHash, owner)
		if err != nil {
			if errors.Is(err, ErrOrderNotFound) {
				continue // not in this market's book; keep looking
			}
			return RestingOrder{}, markets[i], err // owner mismatch / release error
		}
		return view, markets[i], nil
	}
	return RestingOrder{}, "", fmt.Errorf("%w: %s", ErrOrderNotFound, orderHash)
}

// OwnedRestingOrder pairs a resting order with the market whose book holds it
// (RestingOrder has no market field). Returned by OrdersByOwner for the
// GET /api/orders?owner= read model (INT-T04).
type OwnedRestingOrder struct {
	Market string
	Order  RestingOrder
}

// OrdersByOwner returns every resting order owned by `owner` across all markets,
// deterministically ordered (by market, then price-time priority within each
// book). Each book is read via its own Snapshot() (a consistent copy under the
// book lock), so the result never observes a book mid-mutation (INT-T04 pitfall).
func (s *BookSet) OrdersByOwner(owner string) []OwnedRestingOrder {
	owner = strings.TrimSpace(owner)

	s.mu.Lock()
	books := make(map[string]*Orderbook, len(s.books))
	for m, bk := range s.books {
		books[m] = bk
	}
	s.mu.Unlock()

	markets := make([]string, 0, len(books))
	for m := range books {
		markets = append(markets, m)
	}
	sort.Strings(markets)

	out := make([]OwnedRestingOrder, 0)
	for _, m := range markets {
		snap := books[m].Snapshot()
		for _, ro := range snap.Bids {
			if ro.Owner == owner {
				out = append(out, OwnedRestingOrder{Market: m, Order: ro})
			}
		}
		for _, ro := range snap.Asks {
			if ro.Owner == owner {
				out = append(out, OwnedRestingOrder{Market: m, Order: ro})
			}
		}
	}
	return out
}

// Markets returns the sorted list of markets that have a book.
func (s *BookSet) Markets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.books))
	for m := range s.books {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
