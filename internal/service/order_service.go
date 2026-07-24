package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// OrderService is the P4 order/orderbook API boundary consumed by the HTTP
// handlers (INT-T01). It is an INTERFACE on purpose: INT-T01 ships a static
// MockOrderService so the frontend (P5) can start immediately, and INT-T02..T04
// will provide a real implementation (validate → reserve → insert book, live
// snapshots) WITHOUT changing the routes or the response shapes.
type OrderService interface {
	// ListMarkets returns the off-chain market registry (GET /api/markets).
	ListMarkets(ctx context.Context) types.MarketsResponse
	// CreateOrder accepts a SignedOrder (POST /api/order) and returns the order
	// with its status and lifecycle state.
	CreateOrder(ctx context.Context, order types.SignedOrder) (types.OrderResponse, error)
	// GetOrderbook returns a depth snapshot for a market
	// (GET /api/orderbook/{market}).
	GetOrderbook(ctx context.Context, market string) (types.OrderbookSnapshot, error)
	// CancelOrder cancels the resting order identified by id, on behalf of owner
	// (DELETE /api/order/{id}?owner=). It releases the remaining reserved
	// collateral and returns the order with status "cancelled".
	CancelOrder(ctx context.Context, id, owner string) (types.OrderResponse, error)
	// ListOpenOrders returns a user's resting (open/partial) orders across all
	// markets (GET /api/orders?owner=).
	ListOpenOrders(ctx context.Context, owner string) types.OpenOrdersResponse
	// ListTrades returns a market's fill history (GET /api/trades?market=).
	ListTrades(ctx context.Context, market string) types.TradesResponse
}

// Order API service errors. Handlers map these to HTTP status codes so the
// error shape is stable across the mock and the real implementation.
var (
	// ErrOrderFieldsRequired — a required order field is missing/empty.
	ErrOrderFieldsRequired = errors.New("order service: owner, market, side, price and qty are required")
	// ErrMarketNotFound — the requested market is not in the registry.
	ErrMarketNotFound = errors.New("order service: market not found")
	// ErrOrderNotFound — the order id to cancel is unknown (or already gone).
	// Handler maps to 404. Named distinctly from state.ErrOrderNotFound.
	ErrOrderNotFound = errors.New("order service: order not found")
	// ErrOrderForbidden — the caller is not the order's owner. Handler maps to 403.
	ErrOrderForbidden = errors.New("order service: order belongs to another owner")
)

// Machine-readable order rejection codes returned to P5 in the 400 body's
// "reason" field. bad_format/bad_signature/tick_violation/... are passed through
// verbatim from STATE-T03; these two are the P4-level codes the plan names.
const (
	// ReasonInsufficientBalance — available balance can't cover the collateral
	// (maps STATE-T03 ReasonInsufficientAvailable). Plan's required 400 code.
	ReasonInsufficientBalance = "insufficient_balance"
	// ReasonDuplicateOrder — an order with this hash is already resting.
	ReasonDuplicateOrder = "duplicate_order"
	// ReasonOwnerRequired — DELETE /api/order/{id} was called without ?owner=.
	ReasonOwnerRequired = "owner_required"
	// ReasonSelfTrade — the order would fill against the owner's own resting
	// order (wash trade). Blocked by Self-Trade Prevention (cancel-newest): the
	// just-submitted order is cancelled and its reserve released. HTTP 400.
	ReasonSelfTrade = "self_trade"
)

// OrderRejectedError is a client-input rejection (HTTP 400) carrying a stable
// machine reason code plus a human detail. The handler renders it as
// {"error": detail, "reason": code}. Distinct from a nil-verdict/internal error
// (HTTP 500) so validation failures never leak as 500s.
type OrderRejectedError struct {
	Reason string
	Detail string
}

func (e *OrderRejectedError) Error() string {
	if e.Detail != "" {
		return e.Reason + ": " + e.Detail
	}
	return e.Reason
}

// DefaultMarkets is the MVP off-chain market registry seed, shared by the mock
// (INT-T01) and the real (INT-T02) order service so the market contract handed
// to P5 is identical in both modes. Keep in sync with the orderbook fixtures.
func DefaultMarkets() []types.Market {
	return []types.Market{
		{
			Market:      "ATOM/USDC",
			BaseDenom:   "uatom",
			QuoteDenom:  "uusdc",
			TickSize:    "0.1",
			LotSize:     "1",
			MakerFeeBps: 50,
			TakerFeeBps: 100,
			Status:      types.MarketActive,
		},
		{
			Market:      "OSMO/USDC",
			BaseDenom:   "uosmo",
			QuoteDenom:  "uusdc",
			TickSize:    "0.01",
			LotSize:     "1",
			MakerFeeBps: 50,
			TakerFeeBps: 100,
			Status:      types.MarketActive,
		},
	}
}

// MockOrderService is the INT-T01 static implementation. It serves a fixed
// market registry and orderbook fixture, and echoes any posted order back as
// "open". It performs NO signature/reserve/matching logic — those arrive with
// INT-T02..T05. The order id is derived from the canonical order bytes so it is
// deterministic and forward-compatible with the real orderHash (SHA-256 of
// CanonicalBytes, same definition as STATE-T03 OrderHash), but this service
// deliberately does not import P3 state (INT-T01 has no P3 dependency).
type MockOrderService struct {
	markets    []types.Market
	orderbooks map[string]types.OrderbookSnapshot
}

// NewMockOrderService builds the mock with a frozen ATOM/USDC + OSMO/USDC
// fixture. The shapes here are the contract handed to P5; keep them stable.
func NewMockOrderService() *MockOrderService {
	markets := DefaultMarkets()

	orderbooks := map[string]types.OrderbookSnapshot{
		"ATOM/USDC": {
			Market: "ATOM/USDC",
			// Bids: price-descending (best/highest first).
			Bids: []types.PriceLevel{
				{Price: "99.9", Qty: "12"},
				{Price: "99.8", Qty: "30"},
				{Price: "99.5", Qty: "75"},
			},
			// Asks: price-ascending (best/lowest first).
			Asks: []types.PriceLevel{
				{Price: "100.1", Qty: "8"},
				{Price: "100.2", Qty: "25"},
				{Price: "100.5", Qty: "60"},
			},
			BestBid: "99.9",
			BestAsk: "100.1",
		},
		"OSMO/USDC": {
			Market:  "OSMO/USDC",
			Bids:    []types.PriceLevel{{Price: "1.23", Qty: "500"}},
			Asks:    []types.PriceLevel{{Price: "1.25", Qty: "400"}},
			BestBid: "1.23",
			BestAsk: "1.25",
		},
	}

	return &MockOrderService{markets: markets, orderbooks: orderbooks}
}

// compile-time assertion that the mock satisfies the boundary.
var _ OrderService = (*MockOrderService)(nil)

func (s *MockOrderService) ListMarkets(_ context.Context) types.MarketsResponse {
	// Return a copy so a caller cannot mutate the fixture slice.
	out := make([]types.Market, len(s.markets))
	copy(out, s.markets)
	return types.MarketsResponse{Markets: out}
}

func (s *MockOrderService) CreateOrder(_ context.Context, order types.SignedOrder) (types.OrderResponse, error) {
	if strings.TrimSpace(order.Owner) == "" ||
		strings.TrimSpace(order.Market) == "" ||
		strings.TrimSpace(string(order.Side)) == "" ||
		strings.TrimSpace(order.Price) == "" ||
		strings.TrimSpace(order.Qty) == "" {
		return types.OrderResponse{}, ErrOrderFieldsRequired
	}

	orderHash := mockOrderHash(order)

	return types.OrderResponse{
		Order:  order,
		Status: types.OrderStatusOpen,
		State: types.OrderState{
			OrderID:   orderIDFromHash(orderHash),
			OrderHash: orderHash,
			Status:    types.OrderStatusOpen,
			Remaining: order.Qty,
			Filled:    "0",
		},
	}, nil
}

func (s *MockOrderService) GetOrderbook(_ context.Context, market string) (types.OrderbookSnapshot, error) {
	book, ok := s.orderbooks[market]
	if !ok {
		return types.OrderbookSnapshot{}, ErrMarketNotFound
	}
	return book, nil
}

// CancelOrder (mock) echoes a "cancelled" response for any id so P5 can wire the
// cancel flow before the real book exists. It holds no state, so it cannot check
// ownership or a prior fill — the real service (INT-T03) does.
func (s *MockOrderService) CancelOrder(_ context.Context, id, owner string) (types.OrderResponse, error) {
	if strings.TrimSpace(id) == "" {
		return types.OrderResponse{}, ErrOrderFieldsRequired
	}
	orderHash := ""
	if strings.HasPrefix(id, "0x") {
		orderHash = id
	}
	return types.OrderResponse{
		Order:  types.SignedOrder{Owner: owner},
		Status: types.OrderStatusCancelled,
		State: types.OrderState{
			OrderID:   id,
			OrderHash: orderHash,
			Status:    types.OrderStatusCancelled,
			Remaining: "0",
			Filled:    "0",
		},
	}, nil
}

// ListOpenOrders (mock) returns a single static open order so P5 can render the
// order-management list before the real book exists. It ignores owner filtering
// (no state) — the real service filters properly.
func (s *MockOrderService) ListOpenOrders(_ context.Context, owner string) types.OpenOrdersResponse {
	if strings.TrimSpace(owner) == "" {
		return types.OpenOrdersResponse{OpenOrders: []types.OpenOrder{}}
	}
	sample := types.OpenOrder{
		OrderID:   "ord-000000000000mock",
		OrderHash: "0x" + strings.Repeat("00", 32),
		Owner:     owner,
		Market:    "ATOM/USDC",
		Side:      types.SideBuy,
		Price:     "99.9",
		Qty:       "12",
		Remaining: "12",
		Filled:    "0",
		Status:    types.OrderStatusOpen,
		Sequence:  1,
	}
	return types.OpenOrdersResponse{OpenOrders: []types.OpenOrder{sample}}
}

// ListTrades (mock) returns a single static fill so P5 can render the trades
// feed. The real service returns the live per-market history.
func (s *MockOrderService) ListTrades(_ context.Context, market string) types.TradesResponse {
	if strings.TrimSpace(market) == "" {
		return types.TradesResponse{Fills: []types.Fill{}}
	}
	sample := types.Fill{
		TradeID:        "0x" + strings.Repeat("11", 32),
		Market:         market,
		MakerOrderHash: "0x" + strings.Repeat("22", 32),
		TakerOrderHash: "0x" + strings.Repeat("33", 32),
		Price:          "100",
		Qty:            "5",
		MakerFee:       "2",
		TakerFee:       "5",
		Buyer:          "cosmos1alice",
		Seller:         "cosmos1bob",
	}
	return types.TradesResponse{Fills: []types.Fill{sample}}
}

// ---------------------------------------------------------------------------
// RealOrderService (INT-T02) — the real endpoint. It is pure GLUE over P3: it
// orchestrates STATE-T03 validate → STATE-T04 insert (which fuses STATE-T02
// reserve) and maps failures to HTTP codes. It implements NO trading logic of
// its own. Same OrderService interface as the mock, so main.go swaps it in
// behind the same routes with no handler/route/DTO change.
// ---------------------------------------------------------------------------

// RealOrderService wires the P3 order pipeline behind the order API. It holds
// the shared OffchainStateManager (deposits credit it, batches snapshot it) as
// both the balance source and the reservation controller, so an order reserves
// from the very state the rest of the backend settles.
type RealOrderService struct {
	markets    *state.MarketRegistry
	validator  *state.OrderValidator
	books      *state.BookSet
	manager    *state.OffchainStateManager
	nullifiers *state.InMemoryOrderNullifiers
	trades     TradeStore
	engine     *state.MatchingEngine
	queue      FillQueue
	builder    *batch.SettlementUpdateBuilder
	// orderRecords retains each placed order's data keyed by orderHash so trade
	// settlement (INT-T06) can rebuild its OrderCommitmentInput / witness after
	// matching has removed filled orders from the book. Accessed under matchMu.
	orderRecords   map[string]orderRecord
	tradeProver    TradeProver
	tradeSubmitter TradeSubmitter
	// committedRootSink keeps the CORE settlement cursor in lockstep after a trade
	// settles on-chain (DB-1). nil when off-chain settlement is disabled.
	committedRootSink CommittedRootSink
	// tradeBatchRecorder surfaces settled trade batches in GET /api/state's latest*
	// pointers (Nhóm 4 (c)). nil is a no-op.
	tradeBatchRecorder TradeBatchRecorder
	matchMu            sync.Mutex // single-writer: serializes matching + settle (INT-T05/T06)
	now                func() int64
}

// orderRecord is the retained data for one placed order (INT-T06 settlement).
type orderRecord struct {
	order    types.SignedOrder
	sequence uint64
}

// compile-time assertion that the real service satisfies the boundary.
var _ OrderService = (*RealOrderService)(nil)

// NewRealOrderService builds the real order service over a shared state manager.
// markets seeds the off-chain registry (use DefaultMarkets for parity with the
// mock). now supplies the validation reference time in unix seconds; nil uses
// the wall clock (this is the live submission boundary, not the deterministic
// replay path — P2 replays from recorded order data, not this clock). Returns an
// error if any market config is invalid.
func NewRealOrderService(manager *state.OffchainStateManager, markets []types.Market, now func() int64) (*RealOrderService, error) {
	if manager == nil {
		return nil, errors.New("order service: nil state manager")
	}
	registry := state.NewMarketRegistry()
	for _, m := range markets {
		if err := registry.Register(m); err != nil {
			return nil, fmt.Errorf("order service: seed market %q: %w", m.Market, err)
		}
	}
	nullifiers := state.NewInMemoryOrderNullifiers()
	// The BookSet shares the manager (reserve on insert / release on cancel) and
	// the nullifier marker across every market's book.
	books := state.NewBookSet(manager, nullifiers)
	// ORDER_SIG_MODE selects order-auth: "adr36" = real Cosmos ADR-036 secp256k1
	// (binds signer pubkey to Owner); default/"mock" = MVP binding (no wallet).
	validator := state.NewOrderValidator(registry, manager, nullifiers,
		state.NewOrderSignatureVerifier(os.Getenv("ORDER_SIG_MODE")))
	if now == nil {
		now = func() int64 { return time.Now().Unix() }
	}
	return &RealOrderService{
		markets:        registry,
		validator:      validator,
		books:          books,
		manager:        manager,
		nullifiers:     nullifiers,
		trades:         NewInMemoryTradeStore(),
		// STP_MODE selects the Self-Trade Prevention policy: "cancel-newest"
		// (default), "cancel-oldest", or "cancel-both". Empty/unknown → default.
		engine:         state.NewMatchingEngineWithMode(state.ParseStpMode(os.Getenv("STP_MODE"))),
		queue:          NewInMemoryFillQueue(),
		// INT-2SEQ: đường trade mint batchId dưới namespace "trade-" để không đụng
		// namespace "core-" của đường core (SnapshotBuilder) trên cùng chain.
		builder:        batch.NewSettlementUpdateBuilderWithPrefix("trade-"),
		orderRecords:   make(map[string]orderRecord),
		tradeProver:    NewLocalTradeProver(),
		tradeSubmitter: NewLocalTradeSubmitter(),
		now:            now,
	}, nil
}

// SetTradeSettlement swaps the trade prove/submit backends (Wave 2: A's gazk
// trade prover + the real relayer trade submit, INT-T08). Call before starting
// the settlement sequencer.
func (s *RealOrderService) SetTradeSettlement(prover TradeProver, submitter TradeSubmitter) {
	s.matchMu.Lock()
	defer s.matchMu.Unlock()
	if prover != nil {
		s.tradeProver = prover
	}
	if submitter != nil {
		s.tradeSubmitter = submitter
	}
}

// SetCommittedRootSink wires the core settlement cursor so a trade settlement
// keeps it in lockstep with the on-chain root (DB-1 — see CommittedRootSink).
// nil (off-chain settlement disabled) leaves trade settlement unchanged.
func (s *RealOrderService) SetCommittedRootSink(sink CommittedRootSink) {
	s.matchMu.Lock()
	defer s.matchMu.Unlock()
	s.committedRootSink = sink
}

// SetTradeBatchRecorder wires the dashboard latest* pointers so settled trade
// batches surface in GET /api/state (Nhóm 4 (c)). nil is a no-op.
func (s *RealOrderService) SetTradeBatchRecorder(rec TradeBatchRecorder) {
	s.matchMu.Lock()
	defer s.matchMu.Unlock()
	s.tradeBatchRecorder = rec
}

// RecordFills appends fills to the permanent trade history (GET /api/trades)
// WITHOUT enqueuing them for settlement. Kept for tests/tooling that want to
// seed history; the live matching path uses matchAndCollectLocked, which records
// AND enqueues.
func (s *RealOrderService) RecordFills(fills []types.Fill) {
	s.trades.Record(fills)
}

// RunMatchingOnce matches every market once under the single-writer lock — the
// interval sequencer's tick (INT-T05). Returns the total fills produced. Markets
// are iterated in registry (sorted) order for determinism.
func (s *RealOrderService) RunMatchingOnce() (int, error) {
	s.matchMu.Lock()
	defer s.matchMu.Unlock()
	total := 0
	for _, m := range s.markets.List() {
		fills, _, err := s.matchAndCollectLocked(m)
		if err != nil {
			return total, fmt.Errorf("order service: match %s: %w", m.Market, err)
		}
		total += len(fills)
	}
	return total, nil
}

// DrainFills removes and returns all fills queued for settlement — the INT-T06
// trade batch pipeline read seam.
func (s *RealOrderService) DrainFills() []types.Fill { return s.queue.Drain() }

// PendingFillCount reports how many fills await settlement (observability/tests).
func (s *RealOrderService) PendingFillCount() int { return s.queue.Len() }

// matchAndCollectLocked runs the matching engine for one market and routes its
// fills into BOTH the permanent history (GET /api/trades) and the settlement
// queue (INT-T06). It MUST be called with matchMu held (single-writer
// determinism — the ZK proof must reproduce the exact fill sequence). It does
// NOT touch balances: reserved collateral of filled orders stays locked until
// on-chain settle (STATE-T06); matching only mutates the book via Reduce.
func (s *RealOrderService) matchAndCollectLocked(market types.Market) ([]types.Fill, []state.RestingOrder, error) {
	book := s.books.Book(market.Market)
	fills, stpCancelled, err := s.engine.Match(book, market)
	if err != nil {
		return nil, nil, err
	}
	if len(fills) > 0 {
		s.trades.Record(fills) // permanent history
		s.queue.Enqueue(fills) // settlement work-list (INT-T06)
	}
	// STP-cancelled orders already had their reserve released by book.Cancel
	// (BookSet is wired to the manager). Nothing to release here; the list is
	// returned so CreateOrder can surface a self_trade rejection for the caller.
	return fills, stpCancelled, nil
}

// orderStatusLocked reports an order's post-match fill state (called with matchMu
// held). A hash no longer in any book means it fully filled and was removed.
func (s *RealOrderService) orderStatusLocked(book *state.Orderbook, orderHash, qty string) (remaining, filled string, status types.OrderStatus) {
	rem, err := book.RemainingQty(orderHash)
	if err != nil {
		return "0", qty, types.OrderStatusFilled // gone from book => fully filled
	}
	f, serr := state.SubAmount(qty, rem)
	if serr != nil {
		f = "0" // defensive: book amounts are always valid decimals
	}
	if f == "0" {
		return rem, "0", types.OrderStatusOpen
	}
	return rem, f, types.OrderStatusPartial
}

func (s *RealOrderService) ListMarkets(_ context.Context) types.MarketsResponse {
	return types.MarketsResponse{Markets: s.markets.List()}
}

// CreateOrder runs validate → insert(=reserve+rest) atomically from the caller's
// view. Validation rejections and an insufficient/duplicate insert become an
// *OrderRejectedError (HTTP 400); a genuine internal fault becomes a plain error
// (HTTP 500). On the (currently impossible) path where collateral was reserved
// but resting failed, it compensates by releasing the reservation so no balance
// is orphaned — honoring the plan's atomicity requirement and future-proofing
// against a non-atomic Insert.
func (s *RealOrderService) CreateOrder(_ context.Context, order types.SignedOrder) (types.OrderResponse, error) {
	verdict, err := s.validator.Validate(order, s.now())
	if err != nil {
		return types.OrderResponse{}, fmt.Errorf("order service: validate: %w", err)
	}
	if !verdict.Accepted {
		return types.OrderResponse{}, rejectionFromVerdict(verdict)
	}
	// Validation already confirmed the market exists and is active.
	market, _ := s.markets.Get(order.Market)

	// Single-writer section (INT-T05): insert (reserve+rest) THEN immediately run
	// matching, so the new order crosses at most once and the fill sequence is
	// produced deterministically. The interval sequencer shares matchMu, so an
	// insert-time match and a tick match never interleave.
	s.matchMu.Lock()
	book := s.books.Book(order.Market)
	resting, err := book.Insert(order, verdict)
	if err != nil {
		// Compensating release: return collateral if it was reserved but the order
		// did not come to rest (defensive — Insert is atomic today).
		if _, reserved := s.manager.Reservation(verdict.OrderHash); reserved {
			_, _ = s.manager.ReleaseOrder(verdict.OrderHash)
		}
		s.matchMu.Unlock()
		switch {
		case errors.Is(err, state.ErrInsufficientAvailable):
			return types.OrderResponse{}, &OrderRejectedError{Reason: ReasonInsufficientBalance, Detail: err.Error()}
		case errors.Is(err, state.ErrOrderExists), errors.Is(err, state.ErrReservationExists):
			return types.OrderResponse{}, &OrderRejectedError{Reason: ReasonDuplicateOrder, Detail: err.Error()}
		default:
			return types.OrderResponse{}, fmt.Errorf("order service: insert: %w", err)
		}
	}
	// Retain the order for settlement (INT-T06): after matching removes a filled
	// order from the book, its data is only available here.
	s.orderRecords[verdict.OrderHash] = orderRecord{order: order, sequence: resting.Sequence}

	_, stpCancelled, err := s.matchAndCollectLocked(market)
	if err != nil {
		s.matchMu.Unlock()
		return types.OrderResponse{}, fmt.Errorf("order service: match: %w", err)
	}
	// Self-Trade Prevention: if THIS order was cancelled because it would have
	// filled against the owner's own resting order, surface it distinctly instead
	// of letting orderStatusLocked mis-read "gone from book" as "fully filled".
	// Its reserve was already released by book.Cancel. A zero-fill self-cross is a
	// clean rejection; a partial fill against OTHER owners before the self-cross is
	// reported as cancelled with Filled>0 (those fills settle normally).
	for _, v := range stpCancelled {
		if v.OrderHash != verdict.OrderHash {
			continue
		}
		s.matchMu.Unlock()
		filledSelf, ferr := state.SubAmount(order.Qty, v.Remaining)
		if ferr != nil {
			filledSelf = "0"
		}
		if filledSelf == "0" {
			return types.OrderResponse{}, &OrderRejectedError{
				Reason: ReasonSelfTrade,
				Detail: "order would self-trade with your own resting order",
			}
		}
		return types.OrderResponse{
			Order:  order,
			Status: types.OrderStatusCancelled,
			State: types.OrderState{
				OrderID:   orderIDFromHash(verdict.OrderHash),
				OrderHash: verdict.OrderHash,
				Status:    types.OrderStatusCancelled,
				Remaining: "0",
				Filled:    filledSelf,
			},
		}, nil
	}
	// Reflect any immediate fill in the response (open / partial / filled).
	remaining, filled, status := s.orderStatusLocked(book, verdict.OrderHash, order.Qty)
	s.matchMu.Unlock()

	return types.OrderResponse{
		Order:  order,
		Status: status,
		State: types.OrderState{
			OrderID:   orderIDFromHash(verdict.OrderHash),
			OrderHash: verdict.OrderHash,
			Status:    status,
			Remaining: remaining,
			Filled:    filled,
		},
	}, nil
}

func (s *RealOrderService) GetOrderbook(_ context.Context, market string) (types.OrderbookSnapshot, error) {
	if _, ok := s.markets.Get(market); !ok {
		return types.OrderbookSnapshot{}, ErrMarketNotFound
	}
	bids, asks, bestBid, bestAsk := s.books.Book(market).Depth()
	return types.OrderbookSnapshot{
		Market:  market,
		Bids:    bids,
		Asks:    asks,
		BestBid: bestBid,
		BestAsk: bestAsk,
	}, nil
}

// ListOpenOrders returns owner's resting orders across all markets (INT-T04),
// each tagged with its market and fill progress. An empty owner yields an empty
// list (no order has an empty owner). Reads consistent per-book snapshots.
func (s *RealOrderService) ListOpenOrders(_ context.Context, owner string) types.OpenOrdersResponse {
	owned := s.books.OrdersByOwner(owner)
	out := make([]types.OpenOrder, 0, len(owned))
	for _, o := range owned {
		ro := o.Order
		filled, err := state.SubAmount(ro.Qty, ro.Remaining)
		if err != nil {
			filled = "0" // defensive: book amounts are always valid decimals
		}
		status := types.OrderStatusOpen
		if filled != "0" {
			status = types.OrderStatusPartial
		}
		out = append(out, types.OpenOrder{
			OrderID:   orderIDFromHash(ro.OrderHash),
			OrderHash: ro.OrderHash,
			Owner:     ro.Owner,
			Market:    o.Market,
			Side:      ro.Side,
			Price:     ro.Price,
			Qty:       ro.Qty,
			Remaining: ro.Remaining,
			Filled:    filled,
			Status:    status,
			Sequence:  ro.Sequence,
		})
	}
	return types.OpenOrdersResponse{OpenOrders: out}
}

// ListTrades returns a market's fill history (INT-T04), populated by the INT-T05
// matching trigger via RecordFills. Unknown/empty market yields an empty list.
func (s *RealOrderService) ListTrades(_ context.Context, market string) types.TradesResponse {
	fills := s.trades.ByMarket(market)
	if fills == nil {
		fills = []types.Fill{}
	}
	return types.TradesResponse{Fills: fills}
}

// CancelOrder cancels a resting order on behalf of owner (INT-T03), the inverse
// of CreateOrder. It resolves id → orderHash, then cancels across the book set
// with an ownership guard: only the owner may cancel, only an open remainder is
// released (a filled portion is immutable — the reservation was already drawn
// down by any fills), and the order is removed from the book + marked used so it
// cannot be replayed. Returns the order with status "cancelled" and the released
// quantity reflected in State.Filled. Maps to 404 (unknown id) / 403 (not owner).
func (s *RealOrderService) CancelOrder(_ context.Context, id, owner string) (types.OrderResponse, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return types.OrderResponse{}, &OrderRejectedError{Reason: ReasonOwnerRequired, Detail: "owner is required to cancel an order"}
	}
	orderHash, err := s.resolveOrderHash(id)
	if err != nil {
		return types.OrderResponse{}, err
	}

	view, market, err := s.books.CancelOwned(orderHash, owner)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrOrderNotFound):
			return types.OrderResponse{}, ErrOrderNotFound
		case errors.Is(err, state.ErrOrderOwnerMismatch):
			return types.OrderResponse{}, ErrOrderForbidden
		default:
			return types.OrderResponse{}, fmt.Errorf("order service: cancel: %w", err)
		}
	}

	// filled = original qty − the remaining (just-released) qty. Whole-number
	// decimal subtraction, never float.
	filled, ferr := state.SubAmount(view.Qty, view.Remaining)
	if ferr != nil {
		filled = "0" // defensive: view amounts are always valid decimals
	}

	return types.OrderResponse{
		Order: types.SignedOrder{
			Owner:  view.Owner,
			Market: market,
			Side:   view.Side,
			Price:  view.Price,
			Qty:    view.Qty,
		},
		Status: types.OrderStatusCancelled,
		State: types.OrderState{
			OrderID:   orderIDFromHash(view.OrderHash),
			OrderHash: view.OrderHash,
			Status:    types.OrderStatusCancelled,
			Remaining: "0", // nothing rests after cancel
			Filled:    filled,
		},
	}, nil
}

// resolveOrderHash maps the DELETE {id} path value to a book orderHash. The
// canonical id is the full orderHash ("0x"+64 hex); the short "ord-<16hex>"
// display id is resolved best-effort by scanning resting orders. An empty or
// unresolvable id is ErrOrderNotFound.
func (s *RealOrderService) resolveOrderHash(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ErrOrderNotFound
	}
	if strings.HasPrefix(id, "0x") {
		return id, nil
	}
	if strings.HasPrefix(id, "ord-") {
		for _, m := range s.books.Markets() {
			snap := s.books.Book(m).Snapshot()
			for _, ro := range snap.Bids {
				if orderIDFromHash(ro.OrderHash) == id {
					return ro.OrderHash, nil
				}
			}
			for _, ro := range snap.Asks {
				if orderIDFromHash(ro.OrderHash) == id {
					return ro.OrderHash, nil
				}
			}
		}
		return "", ErrOrderNotFound
	}
	return id, nil // best-effort: treat as a raw hash
}

// rejectionFromVerdict maps a STATE-T03 reject verdict to an *OrderRejectedError.
// The insufficient-available reason is renamed to the plan's "insufficient_balance"
// code; every other reason (bad_format/bad_signature/tick_violation/…) passes
// through verbatim so P5 can branch on it.
func rejectionFromVerdict(v state.OrderValidation) *OrderRejectedError {
	reason := string(v.Reason)
	if v.Reason == state.ReasonInsufficientAvailable {
		reason = ReasonInsufficientBalance
	}
	return &OrderRejectedError{Reason: reason, Detail: v.Detail}
}

// orderIDFromHash derives the API order id from the orderHash ("0x"+64 hex):
// "ord-" + the first 16 hex chars. Deterministic and identical to the mock's id
// scheme, so ids are stable across the mock→real swap.
func orderIDFromHash(orderHash string) string {
	h := strings.TrimPrefix(orderHash, "0x")
	if len(h) > 16 {
		h = h[:16]
	}
	return "ord-" + h
}

// mockOrderHash returns "0x"+hex(SHA-256(CanonicalBytes)). It matches the
// STATE-T03 OrderHash definition so the mock id lines up with the real hash once
// INT-T02 wires P3, but is computed locally to keep INT-T01 free of any P3
// import. Malformed orders (empty canonical fields) fall back to hashing the
// raw owner|market|nonce join so the mock still returns a stable id.
func mockOrderHash(order types.SignedOrder) string {
	preimage, err := order.CanonicalBytes()
	if err != nil {
		preimage = []byte(order.Owner + "|" + order.Market + "|" + order.Nonce)
	}
	sum := sha256.Sum256(preimage)
	return "0x" + hex.EncodeToString(sum[:])
}
