package state

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// STATE-T05 — Matching engine.
//
// The heart of trading: from an orderbook it produces an ordered sequence of
// Fills with strict price-time matching, FULLY DETERMINISTICALLY. Same order set
// + same market config → byte-identical Fill sequence, so P2 can prove it.
//
// Hard rules (plan pitfalls):
//   - Fill price is the MAKER's price (the order that rested first — lower
//     sequence). Using the taker price would break value conservation and the
//     canonical vectors.
//   - No clock, no randomness anywhere. tradeId is derived from data
//     (market, maker/taker order hashes, fill index).
//
// The engine mutates the book (Reduce removes filled orders) but NEVER touches
// balances — turning Fills into balance transitions is STATE-T06. Reserved
// collateral of filled orders stays locked here; T06 consumes it.

const tradeIDDomain = "zkdex/tradeId/v0"

// TradeIDFor derives a deterministic trade id:
//
//	tradeId = SHA256( domain | market | makerOrderHash | takerOrderHash | fillIndex )
//
// fillIndex disambiguates fills within one matching run so the id is unique and
// reproducible without any clock or counter external to the data.
func TradeIDFor(market, makerOrderHash, takerOrderHash string, fillIndex uint64) string {
	var b strings.Builder
	b.WriteString(tradeIDDomain)
	b.WriteByte('|')
	b.WriteString(market)
	b.WriteByte('|')
	b.WriteString(makerOrderHash)
	b.WriteByte('|')
	b.WriteString(takerOrderHash)
	b.WriteByte('|')
	b.WriteString(strconv.FormatUint(fillIndex, 10))
	return hash.SHA256Hex([]byte(b.String()))
}

// StpMode is the Self-Trade Prevention policy applied when the best bid and best
// ask belong to the SAME owner (a would-be wash trade). A crossed book can never
// persist, so at least one of the two orders is always cancelled.
type StpMode string

const (
	// StpCancelNewest cancels the just-arrived (higher-sequence) order and keeps
	// the resting one. Default — protects standing liquidity.
	StpCancelNewest StpMode = "cancel-newest"
	// StpCancelOldest cancels the resting (lower-sequence) order and lets the new
	// order proceed (match other owners / rest).
	StpCancelOldest StpMode = "cancel-oldest"
	// StpCancelBoth cancels both crossing orders.
	StpCancelBoth StpMode = "cancel-both"
)

// ParseStpMode maps a config/env string to a StpMode. Empty or unrecognized
// values fall back to cancel-newest (the safe, most common default).
func ParseStpMode(s string) StpMode {
	switch StpMode(strings.ToLower(strings.TrimSpace(s))) {
	case StpCancelOldest:
		return StpCancelOldest
	case StpCancelBoth:
		return StpCancelBoth
	default:
		return StpCancelNewest
	}
}

// MatchingEngine runs price-time matching over an Orderbook, applying a
// Self-Trade Prevention policy. Stateless w.r.t. the book — safe to reuse across
// markets/batches.
type MatchingEngine struct {
	stp StpMode
}

// NewMatchingEngine returns a matching engine with the default STP policy
// (cancel-newest).
func NewMatchingEngine() *MatchingEngine { return &MatchingEngine{stp: StpCancelNewest} }

// NewMatchingEngineWithMode returns a matching engine with an explicit STP
// policy. An empty mode falls back to cancel-newest.
func NewMatchingEngineWithMode(mode StpMode) *MatchingEngine {
	if mode == "" {
		mode = StpCancelNewest
	}
	return &MatchingEngine{stp: mode}
}

// Match repeatedly crosses the book's best bid and ask while
// bestBid.price >= bestAsk.price, emitting one Fill per crossing and reducing
// both orders by the fill quantity (removing any order that reaches zero). It
// returns the ordered Fills. A non-crossing book yields an empty slice.
//
// market supplies the fee bps and must match the book's market. The engine
// mutates book via Reduce; callers that need the pre-match book (rollback) must
// snapshot beforehand — book rollback integration is STATE-T08/T10.
func (e *MatchingEngine) Match(book *Orderbook, market types.Market) ([]types.Fill, []RestingOrder, error) {
	if strings.TrimSpace(market.Market) != book.Market() {
		return nil, nil, fmt.Errorf("matching: market %q does not match book %q", market.Market, book.Market())
	}

	fills := make([]types.Fill, 0)
	// stpCancelled collects orders removed by Self-Trade Prevention (STP) so the
	// caller can reflect them in the API response. book.Cancel already released
	// each cancelled order's reserved collateral (BookSet is wired to the manager).
	var stpCancelled []RestingOrder
	var fillIndex uint64

	for {
		bid, hasBid := book.BestBid()
		ask, hasAsk := book.BestAsk()
		if !hasBid || !hasAsk {
			break // one side empty — nothing left to cross
		}

		bidPrice, err := parsePositiveDecimal(bid.Price)
		if err != nil {
			return nil, nil, fmt.Errorf("matching: bid price %q: %w", bid.Price, err)
		}
		askPrice, err := parsePositiveDecimal(ask.Price)
		if err != nil {
			return nil, nil, fmt.Errorf("matching: ask price %q: %w", ask.Price, err)
		}
		// Cross condition: highest bid must reach the lowest ask.
		if cmpDecimal(bidPrice, askPrice) < 0 {
			break
		}

		// Self-Trade Prevention: a resting order must never fill against another
		// order from the SAME owner (wash trade). A crossed book cannot persist,
		// so the configured StpMode decides which order(s) to cancel. book.Cancel
		// releases the cancelled order's reserved collateral (BookSet is wired to
		// the manager). No fill is emitted; the loop re-evaluates the top of book.
		if strings.TrimSpace(bid.Owner) == strings.TrimSpace(ask.Owner) {
			newer, older := bid, ask
			if ask.Sequence > bid.Sequence {
				newer, older = ask, bid
			}
			cancelOne := func(o RestingOrder) error {
				if err := book.Cancel(o.OrderHash); err != nil {
					return fmt.Errorf("matching: STP cancel %s: %w", o.OrderHash, err)
				}
				stpCancelled = append(stpCancelled, o)
				return nil
			}
			switch e.stp {
			case StpCancelOldest:
				if err := cancelOne(older); err != nil {
					return nil, nil, err
				}
			case StpCancelBoth:
				if err := cancelOne(newer); err != nil {
					return nil, nil, err
				}
				if err := cancelOne(older); err != nil {
					return nil, nil, err
				}
			default: // StpCancelNewest
				if err := cancelOne(newer); err != nil {
					return nil, nil, err
				}
			}
			continue
		}

		bidRem, err := parsePositiveDecimal(bid.Remaining)
		if err != nil {
			return nil, nil, fmt.Errorf("matching: bid remaining %q: %w", bid.Remaining, err)
		}
		askRem, err := parsePositiveDecimal(ask.Remaining)
		if err != nil {
			return nil, nil, fmt.Errorf("matching: ask remaining %q: %w", ask.Remaining, err)
		}
		fillDec := minDecimal(bidRem, askRem)
		fillQty := fillDec.String()

		// Maker = the order that rested first (smaller sequence). Its price is
		// the trade price. Sequences are globally unique in a book, so there is
		// never a tie.
		makerIsBid := bid.Sequence < ask.Sequence
		var makerPrice decimal
		var makerOrderHash, takerOrderHash string
		if makerIsBid {
			makerPrice = bidPrice
			makerOrderHash, takerOrderHash = bid.OrderHash, ask.OrderHash
		} else {
			makerPrice = askPrice
			makerOrderHash, takerOrderHash = ask.OrderHash, bid.OrderHash
		}

		// Fees on the trade notional (maker price * fill qty), in quote units.
		notional := mulDecimal(makerPrice, fillDec)
		makerFee := floorFeeUnits(notional, market.MakerFeeBps)
		takerFee := floorFeeUnits(notional, market.TakerFeeBps)

		fills = append(fills, types.Fill{
			TradeID:        TradeIDFor(book.Market(), makerOrderHash, takerOrderHash, fillIndex),
			Market:         book.Market(),
			MakerOrderHash: makerOrderHash,
			TakerOrderHash: takerOrderHash,
			Price:          makerPrice.String(),
			Qty:            fillQty,
			MakerFee:       makerFee.String(),
			TakerFee:       takerFee.String(),
			Buyer:          bid.Owner,
			Seller:         ask.Owner,
		})
		fillIndex++

		// Reduce both sides; the fully-filled side(s) leave the book.
		if _, _, err := book.Reduce(bid.OrderHash, fillQty); err != nil {
			return nil, nil, fmt.Errorf("matching: reduce bid %s: %w", bid.OrderHash, err)
		}
		if _, _, err := book.Reduce(ask.OrderHash, fillQty); err != nil {
			return nil, nil, fmt.Errorf("matching: reduce ask %s: %w", ask.OrderHash, err)
		}
	}

	return fills, stpCancelled, nil
}

// minDecimal returns the smaller of a and b (a if equal).
func minDecimal(a, b decimal) decimal {
	if cmpDecimal(a, b) <= 0 {
		return a
	}
	return b
}

// floorFeeUnits returns floor(notional * bps / 10000) in whole smallest-units.
// Floor (round down) is the conventional fee rounding — a trade never charges
// more than the exact fee. Exact integer math on the decimal mantissa, no float.
func floorFeeUnits(notional decimal, bps int64) *big.Int {
	if bps <= 0 {
		return big.NewInt(0)
	}
	num := new(big.Int).Mul(notional.mant, big.NewInt(bps))
	den := new(big.Int).Mul(pow10(notional.scale), big.NewInt(10000))
	return num.Div(num, den) // floor for non-negative operands
}
