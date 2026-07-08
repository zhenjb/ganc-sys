package state

import (
	"errors"
	"math/big"
	"strings"
)

// Exact decimal arithmetic for trading amounts (STATE-T03). Prices, quantities
// and tick/lot sizes are decimal strings (Encoding Agreement — never float:
// float drift silently corrupts hashes and fee/collateral math). A decimal is
// represented as (mantissa, scale) meaning value = mantissa * 10^(-scale), with
// mantissa a non-negative big.Int. All operations are exact and deterministic.

// ErrInvalidDecimal is returned when a decimal string is malformed or negative.
// Sentinel — callers chain with errors.Is.
var ErrInvalidDecimal = errors.New("state: invalid decimal")

type decimal struct {
	mant  *big.Int // non-negative
	scale int      // number of fractional digits (>= 0)
}

var bigTen = big.NewInt(10)

// parseDecimal parses a non-negative decimal string ("10", "10.5", "0.001",
// ".5"). Rejects empty, negative, non-numeric, or multi-dot strings.
func parseDecimal(s string) (decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return decimal{}, ErrInvalidDecimal
	}
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		return decimal{}, ErrInvalidDecimal // sign not allowed (non-negative only)
	}

	intPart, fracPart := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot+1:]
		if strings.IndexByte(fracPart, '.') >= 0 {
			return decimal{}, ErrInvalidDecimal // more than one dot
		}
	}
	if intPart == "" {
		intPart = "0"
	}
	if intPart == "" && fracPart == "" {
		return decimal{}, ErrInvalidDecimal
	}
	if !isAllDigits(intPart) || !isAllDigits(fracPart) {
		return decimal{}, ErrInvalidDecimal
	}

	mant, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return decimal{}, ErrInvalidDecimal
	}
	return decimal{mant: mant, scale: len(fracPart)}, nil
}

// parsePositiveDecimal is parseDecimal that additionally rejects zero.
func parsePositiveDecimal(s string) (decimal, error) {
	d, err := parseDecimal(s)
	if err != nil {
		return decimal{}, err
	}
	if d.mant.Sign() == 0 {
		return decimal{}, ErrInvalidDecimal
	}
	return d, nil
}

func isAllDigits(s string) bool {
	// empty is allowed (e.g. no fractional part); caller guards the all-empty case.
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// pow10 returns 10^n as a big.Int (n >= 0).
func pow10(n int) *big.Int {
	return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil)
}

// alignedMantissas returns a's and b's mantissas both scaled to the larger of
// the two scales, so they represent the same fixed-point grid and can be
// compared / divided exactly.
func alignedMantissas(a, b decimal) (am, bm *big.Int) {
	common := a.scale
	if b.scale > common {
		common = b.scale
	}
	am = new(big.Int).Mul(a.mant, pow10(common-a.scale))
	bm = new(big.Int).Mul(b.mant, pow10(common-b.scale))
	return am, bm
}

// isMultipleOf reports whether value is an exact positive-integer multiple of
// unit (value mod unit == 0). unit must be non-zero. Used for tick/lot checks:
// price must be a multiple of tickSize, qty a multiple of lotSize.
func isMultipleOf(value, unit decimal) bool {
	if unit.mant.Sign() == 0 {
		return false
	}
	vm, um := alignedMantissas(value, unit)
	if um.Sign() == 0 {
		return false
	}
	return new(big.Int).Rem(vm, um).Sign() == 0
}

// mulDecimal returns the exact product a*b as a decimal.
func mulDecimal(a, b decimal) decimal {
	return decimal{
		mant:  new(big.Int).Mul(a.mant, b.mant),
		scale: a.scale + b.scale,
	}
}

// ceilToInt returns the smallest integer >= the decimal value, as a big.Int.
// Used to convert a decimal collateral/notional into a whole smallest-unit
// amount to reserve. Ceil (round up) is deliberate: over-reserving is safe —
// the exact amount is consumed on fill and the remainder released.
func (d decimal) ceilToInt() *big.Int {
	if d.scale == 0 {
		return new(big.Int).Set(d.mant)
	}
	div := pow10(d.scale)
	q := new(big.Int)
	r := new(big.Int)
	q.DivMod(d.mant, div, r) // d.mant >= 0, so DivMod == Euclidean == truncation
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// ceilDivBig returns ceil(a / b) for a >= 0, b > 0.
func ceilDivBig(a, b *big.Int) *big.Int {
	num := new(big.Int).Add(a, new(big.Int).Sub(b, big.NewInt(1)))
	return num.Div(num, b)
}
