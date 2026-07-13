package types

// SettlementTrade is one entry in SettlementUpdate.Trades[], shaped to match the
// on-chain x/zkdex `Trade` message (ganc-trade) field-for-field with camelCase
// JSON so it deserializes byte-compatibly on the wire (TRD-D1, AGR-2).
//
// A Fill (STATE-T05) describes a match between TWO orders (maker + taker); a
// SettlementTrade is the richer, chain-facing projection the relayer submits: it
// carries both order ids for linkage plus one order's identity fields
// (owner / orderHash / orderNullifier / side / denom) that the chain needs to
// mark the order nullifier used and account the trade. It is built by
// state.BuildSettlementTrades from a Fill + the batch's orders + the market.
//
// Chain contract (x/zkdex/types.Trade.ValidateBasic):
//   - tradeId / market / makerOrderId / takerOrderId: non-empty
//   - orderHash / orderNullifier: 0x-prefixed 32-byte hex
//   - owner: non-empty; denom: a valid sdk denom
//   - side: "buy" | "sell"
//   - amount / price / baseQty / quoteQty: positive decimal strings
//   - makerFee / takerFee: non-negative decimal strings
type SettlementTrade struct {
	TradeID        string `json:"tradeId"`
	Market         string `json:"market"`
	MakerOrderID   string `json:"makerOrderId"`
	TakerOrderID   string `json:"takerOrderId"`
	OrderHash      string `json:"orderHash"`
	OrderNullifier string `json:"orderNullifier"`
	Owner          string `json:"owner"`
	Denom          string `json:"denom"`
	Side           string `json:"side"`
	Amount         string `json:"amount"`
	Price          string `json:"price"`
	BaseQty        string `json:"baseQty"`
	QuoteQty       string `json:"quoteQty"`
	MakerFee       string `json:"makerFee"`
	TakerFee       string `json:"takerFee"`
}
