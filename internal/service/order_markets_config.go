package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// DEN-D1 — Market registry ra config.
//
// The live (real) order service seeds its MarketRegistry from a []types.Market.
// Before DEN-D1 that seed was the hard-coded DefaultMarkets(), whose denoms
// (uatom/uosmo/uusdc) could not change without recompiling — the root cause of
// the BE<->chain denom mismatch ("Tường A"): the chain funded ATOM/USDT while
// the backend insisted on uatom/uusdc, so trading collateral could never be
// deposited. This file resolves the seed from the environment so an operator can
// align the backend to whatever denoms the target chain actually funds, with
// DefaultMarkets() kept as the safe fallback (unchanged behaviour when unset).
//
// Resolution precedence (first non-empty wins):
//  1. ORDER_MARKETS_JSON — an inline JSON array of Market objects.
//  2. ORDER_MARKETS_FILE — path to a JSON file holding that array.
//  3. DefaultMarkets()   — the built-in MVP seed.
const (
	// EnvOrderMarketsJSON holds an inline JSON array of markets.
	EnvOrderMarketsJSON = "ORDER_MARKETS_JSON"
	// EnvOrderMarketsFile holds a path to a JSON file with the market array.
	EnvOrderMarketsFile = "ORDER_MARKETS_FILE"
)

// Source labels returned by MarketsFromEnv for the startup log line.
const (
	marketsSourceDefault = "default"
	marketsSourceEnvJSON = "env:" + EnvOrderMarketsJSON
)

// MarketsFromEnv resolves the market seed from the environment, falling back to
// DefaultMarkets() when neither variable is set. It returns the resolved
// markets, a human-readable source label (for startup logging), and an error if
// a *configured* source is malformed (bad JSON, missing file, empty array).
//
// It deliberately does NOT field-validate the markets: NewRealOrderService (via
// MarketRegistry.Register) is the single source of truth for tick/lot/fee/denom
// validation and will reject an invalid seed at construction. Keeping validation
// in one place avoids two drifting rule sets.
func MarketsFromEnv() ([]types.Market, string, error) {
	return resolveMarkets(os.Getenv(EnvOrderMarketsJSON), os.Getenv(EnvOrderMarketsFile), os.ReadFile)
}

// resolveMarkets is the env-independent core of MarketsFromEnv, injectable for
// tests (no process env or real filesystem required).
func resolveMarkets(
	inline, filePath string,
	readFile func(string) ([]byte, error),
) ([]types.Market, string, error) {
	switch {
	case strings.TrimSpace(inline) != "":
		markets, err := parseMarketsJSON([]byte(inline))
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", EnvOrderMarketsJSON, err)
		}
		return markets, marketsSourceEnvJSON, nil

	case strings.TrimSpace(filePath) != "":
		path := strings.TrimSpace(filePath)
		data, err := readFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("%s %q: %w", EnvOrderMarketsFile, path, err)
		}
		markets, err := parseMarketsJSON(data)
		if err != nil {
			return nil, "", fmt.Errorf("%s %q: %w", EnvOrderMarketsFile, path, err)
		}
		return markets, "file:" + path, nil

	default:
		return DefaultMarkets(), marketsSourceDefault, nil
	}
}

// parseMarketsJSON strictly decodes a JSON array of markets. Unknown fields are
// rejected so a mistyped key (e.g. "basedenom") fails loudly at startup instead
// of silently leaving a denom empty — precisely the class of denom bug DEN-D1
// exists to prevent.
func parseMarketsJSON(data []byte) ([]types.Market, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var markets []types.Market
	if err := dec.Decode(&markets); err != nil {
		return nil, fmt.Errorf("parse markets json: %w", err)
	}
	if dec.More() {
		return nil, errors.New("parse markets json: unexpected trailing data after JSON array")
	}
	if len(markets) == 0 {
		return nil, errors.New("market config must contain at least one market")
	}
	return markets, nil
}
