package types

import (
	"errors"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// STATE-T01 — Trade structs (shared trading schema).
//
// This file is the single source of truth for the trading data types consumed
// by every role: P2 (ZK circuit), P3 (this backend — state/batch), P4 (API /
// relayer) and P5 (frontend). D owns the schema; every other member binds to
// these shapes so canonical hashes and public inputs never diverge.
//
// Encoding (locked in Agreements → "Encoding"):
//   - amount / price / qty / fee     = decimal STRING (never float — float
//                                      drift silently corrupts both the order
//                                      hash and fee arithmetic).
//   - roots / hashes / nullifiers    = "0x"-prefixed lowercase hex STRING.
//   - trade denoms come from the market (base/quote); nothing is hardcoded.
//
// Determinism is the whole point: the same logical order must always serialize
// to the same CanonicalBytes(), because those bytes are exactly what the wallet
// signs (Order auth Agreement → Cosmos ADR-036 arbitrary sign) and what the
// orderHash is derived from. A stable, versioned field order guarantees P3/P4/P5
// (and a TS wallet) can all reproduce the identical byte string.
// ---------------------------------------------------------------------------

// OrderSide is the direction of a SignedOrder. Buy locks quote-denom
// collateral, sell locks base-denom collateral (Reserved balance Agreement).
type OrderSide string

const (
	SideBuy  OrderSide = "buy"
	SideSell OrderSide = "sell"
)

// IsValid reports whether s is one of the two locked sides.
func (s OrderSide) IsValid() bool {
	return s == SideBuy || s == SideSell
}

// MarketStatus is the trading state of a Market in the off-chain registry.
type MarketStatus string

const (
	// MarketActive — orders are accepted and matched.
	MarketActive MarketStatus = "active"
	// MarketHalted — market exists but temporarily rejects new orders.
	MarketHalted MarketStatus = "halted"
	// MarketInactive — market is not tradable (delisted / not yet opened).
	MarketInactive MarketStatus = "inactive"
)

// IsValid reports whether m is a known market status.
func (m MarketStatus) IsValid() bool {
	switch m {
	case MarketActive, MarketHalted, MarketInactive:
		return true
	default:
		return false
	}
}

// OrderCanonicalVersion is the version tag written as the first line of every
// SignedOrder.CanonicalBytes(). It is part of the signed/hashed preimage: if
// the canonical field set or ordering ever changes, bump this to "v1" so old
// signatures/hashes cannot be replayed against the new layout, and regenerate
// the sample vector. NEVER silently change the layout without bumping this —
// every root already built at P1/P3 would break.
const OrderCanonicalVersion = "zkdex/order/v0"

// ErrInvalidOrder is the sentinel returned by SignedOrder.Validate and
// CanonicalBytes when the order is structurally malformed. Callers chain with
// errors.Is(err, ErrInvalidOrder).
var ErrInvalidOrder = errors.New("types: invalid signed order")

// SignedOrder is a limit order authored and signed off-chain by a user's
// wallet (it is NOT an on-chain tx). Price and Qty are decimal strings quoted
// in the market's quote/base denoms respectively.
//
//	Side   ∈ {buy, sell}
//	Price  decimal string (quote per base), a multiple of Market.TickSize
//	Qty    decimal string (base amount),    a multiple of Market.LotSize
//	Expiry unix-seconds string; the order is invalid once now > Expiry
//	Nonce  per-owner monotonic string used for replay protection
//
// Signature is the wallet signature over CanonicalBytes() and is deliberately
// NOT part of the canonical preimage (a signature cannot cover itself).
type SignedOrder struct {
	Owner     string    `json:"owner"`
	Market    string    `json:"market"`
	Side      OrderSide `json:"side"`
	Price     string    `json:"price"`
	Qty       string    `json:"qty"`
	Expiry    string    `json:"expiry"`
	Nonce     string    `json:"nonce"`
	Signature string    `json:"signature"`

	// PubKey is the base64 compressed secp256k1 public key of the signer,
	// supplied only for real ADR-036 verification (ORDER_SIG_MODE=adr36). Like
	// Signature it is transport-only and is NOT part of CanonicalBytes() — the
	// canonical preimage cannot cover the key/signature that signs it.
	PubKey string `json:"pubkey,omitempty"`
}

// canonicalOrderFields lists, in fixed order, the (label, value) pairs that make
// up the canonical preimage. The order and labels are frozen by
// OrderCanonicalVersion. Signature is intentionally absent.
func (o SignedOrder) canonicalOrderFields() []struct{ label, value string } {
	return []struct{ label, value string }{
		{"owner", strings.TrimSpace(o.Owner)},
		{"market", strings.TrimSpace(o.Market)},
		{"side", strings.TrimSpace(string(o.Side))},
		{"price", strings.TrimSpace(o.Price)},
		{"qty", strings.TrimSpace(o.Qty)},
		{"expiry", strings.TrimSpace(o.Expiry)},
		{"nonce", strings.TrimSpace(o.Nonce)},
	}
}

// CanonicalString returns the deterministic, versioned string representation of
// the order used for signing and hashing. Format (newline-delimited, one
// labelled field per line, version first):
//
//	zkdex/order/v0
//	owner:<owner>
//	market:<market>
//	side:<side>
//	price:<price>
//	qty:<qty>
//	expiry:<expiry>
//	nonce:<nonce>
//
// Each value is TrimSpace'd. The format is intentionally trivial to reproduce
// byte-for-byte in any language (TS wallet, Go backend, circuit witness
// builder). Signature is excluded.
//
// It returns ErrInvalidOrder if any canonical field is empty or contains a
// newline (a newline could forge field boundaries and let one order's preimage
// masquerade as another's). Semantic checks (valid side, tick/lot alignment,
// signature) are NOT done here — that is STATE-T03 (ValidateOrder). This method
// only guarantees the bytes are well-formed and stable.
func (o SignedOrder) CanonicalString() (string, error) {
	fields := o.canonicalOrderFields()
	var b strings.Builder
	b.WriteString(OrderCanonicalVersion)
	for _, f := range fields {
		if f.value == "" {
			return "", fmt.Errorf("%w: field %q is empty", ErrInvalidOrder, f.label)
		}
		if strings.ContainsAny(f.value, "\r\n") {
			return "", fmt.Errorf("%w: field %q contains a newline", ErrInvalidOrder, f.label)
		}
		b.WriteByte('\n')
		b.WriteString(f.label)
		b.WriteByte(':')
		b.WriteString(f.value)
	}
	return b.String(), nil
}

// CanonicalBytes returns CanonicalString as bytes — the exact preimage that is
// signed (ADR-036) and hashed (orderHash = H(CanonicalBytes)). Same logical
// order → identical bytes.
func (o SignedOrder) CanonicalBytes() ([]byte, error) {
	s, err := o.CanonicalString()
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// Validate performs structural (not cryptographic) validation of the order:
// canonical fields present and newline-free, and Side is a known enum. It does
// NOT verify the signature, market existence, tick/lot alignment or expiry —
// those belong to STATE-T03. Returns ErrInvalidOrder on failure.
func (o SignedOrder) Validate() error {
	if _, err := o.CanonicalString(); err != nil {
		return err
	}
	if !o.Side.IsValid() {
		return fmt.Errorf("%w: side %q must be buy or sell", ErrInvalidOrder, o.Side)
	}
	if strings.TrimSpace(o.Signature) == "" {
		return fmt.Errorf("%w: signature is empty", ErrInvalidOrder)
	}
	return nil
}

// Fill is a single matched trade produced by the matching engine (STATE-T05).
// Price is the maker's price (price-time priority: the resting order sets the
// price). Amounts and fees are decimal strings; order hashes are hex.
type Fill struct {
	TradeID        string `json:"tradeId"`
	Market         string `json:"market"`
	MakerOrderHash string `json:"makerOrderHash"` // hex
	TakerOrderHash string `json:"takerOrderHash"` // hex
	Price          string `json:"price"`          // maker price, decimal string
	Qty            string `json:"qty"`            // base qty filled, decimal string
	MakerFee       string `json:"makerFee"`       // amount string
	TakerFee       string `json:"takerFee"`       // amount string
	Buyer          string `json:"buyer"`
	Seller         string `json:"seller"`
}

// Market is one entry in the off-chain market registry (MVP; an on-chain
// registry is future work). TickSize/LotSize are decimal strings; fees are
// integer basis points (1 bps = 0.01%).
type Market struct {
	Market      string       `json:"market"`
	BaseDenom   string       `json:"baseDenom"`
	QuoteDenom  string       `json:"quoteDenom"`
	TickSize    string       `json:"tickSize"` // decimal string (min price increment)
	LotSize     string       `json:"lotSize"`  // decimal string (min qty increment)
	MakerFeeBps int64        `json:"makerFeeBps"`
	TakerFeeBps int64        `json:"takerFeeBps"`
	Status      MarketStatus `json:"status"`
}

// Reservation records collateral locked (available → reserved) for a resting
// order, keyed to its OrderHash so the exact amount can be released when the
// order is cancelled/expires or consumed when it fills (STATE-T02).
type Reservation struct {
	Owner     string `json:"owner"`
	Denom     string `json:"denom"`
	Amount    string `json:"amount"`    // amount string
	OrderHash string `json:"orderHash"` // hex
}
