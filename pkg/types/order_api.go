package types

// ---------------------------------------------------------------------------
// INT-T01 — Order API contract DTOs (P4 → P5).
//
// These are the REST envelope shapes the frontend (P5) binds to. They are
// FROZEN as of the mock (INT-T01): field names and JSON types must not change
// when INT-T02..04 replace the mock handlers with real P3 logic, otherwise the
// FE breaks. Encoding follows the trade schema (STATE-T01):
//   - price / qty / amounts = decimal STRING (never float)
//   - orderHash / hashes     = "0x"-prefixed lowercase hex STRING
//
// The mock and the future real handlers share these types so the swap is a
// handler-body change only, not a contract change.
// ---------------------------------------------------------------------------

// OrderStatus is the lifecycle state of an order as seen by the API. The full
// enum is frozen now so FE can render every state before the real pipeline
// (INT-T02/T05) produces them.
type OrderStatus string

const (
	// OrderStatusOpen — resting in the book, no fills yet.
	OrderStatusOpen OrderStatus = "open"
	// OrderStatusPartial — partially filled, remainder still resting.
	OrderStatusPartial OrderStatus = "partial"
	// OrderStatusFilled — fully filled, no remainder.
	OrderStatusFilled OrderStatus = "filled"
	// OrderStatusCancelled — cancelled by the owner (INT-T03).
	OrderStatusCancelled OrderStatus = "cancelled"
	// OrderStatusRejected — failed validation/reserve (INT-T02).
	OrderStatusRejected OrderStatus = "rejected"
)

// MarketsResponse is the body of GET /api/markets.
type MarketsResponse struct {
	Markets []Market `json:"markets"`
}

// OrderState is the lightweight per-order lifecycle snapshot returned alongside
// an order. Remaining/Filled are decimal strings in base units.
type OrderState struct {
	OrderID   string      `json:"orderId"`
	OrderHash string      `json:"orderHash"` // hex (0x…)
	Status    OrderStatus `json:"status"`
	Remaining string      `json:"remaining"` // base qty still open, decimal string
	Filled    string      `json:"filled"`    // base qty already filled, decimal string
}

// OrderResponse is the body of POST /api/order (and DELETE /api/order/{id} in
// INT-T03): the echoed order, its top-level status, and the order state.
type OrderResponse struct {
	Order  SignedOrder `json:"order"`
	Status OrderStatus `json:"status"`
	State  OrderState  `json:"state"`
}

// PriceLevel is one aggregated depth level in an orderbook side: the total
// resting base quantity at a given price. Both are decimal strings.
type PriceLevel struct {
	Price string `json:"price"`
	Qty   string `json:"qty"`
}

// OrderbookSnapshot is the body of GET /api/orderbook/{market}. Bids are sorted
// price-descending, asks price-ascending. BestBid/BestAsk are the top-of-book
// prices, or "" when that side is empty.
type OrderbookSnapshot struct {
	Market  string       `json:"market"`
	Bids    []PriceLevel `json:"bids"`
	Asks    []PriceLevel `json:"asks"`
	BestBid string       `json:"bestBid"`
	BestAsk string       `json:"bestAsk"`
}
