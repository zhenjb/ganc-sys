package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// failReadFile is a readFile that fails if ever called — used to prove a source
// with higher precedence short-circuits before the filesystem is touched.
func failReadFile(string) ([]byte, error) {
	return nil, errors.New("readFile must not be called")
}

// validMarketsJSON is a single-market config using denoms different from
// DefaultMarkets(), so a test can prove the config (not the hard-coded seed)
// reached the registry.
const validMarketsJSON = `[{"market":"ATOM/USDT","baseDenom":"uatom","quoteDenom":"uusdt","tickSize":"0.1","lotSize":"1","makerFeeBps":50,"takerFeeBps":100,"status":"active"}]`

func TestResolveMarketsDefault(t *testing.T) {
	markets, source, err := resolveMarkets("", "", failReadFile)
	if err != nil {
		t.Fatalf("resolveMarkets default: %v", err)
	}
	if source != marketsSourceDefault {
		t.Fatalf("source = %q, want %q", source, marketsSourceDefault)
	}
	if len(markets) != len(DefaultMarkets()) {
		t.Fatalf("markets = %d, want default %d", len(markets), len(DefaultMarkets()))
	}
}

func TestResolveMarketsInlineJSON(t *testing.T) {
	markets, source, err := resolveMarkets(validMarketsJSON, "", failReadFile)
	if err != nil {
		t.Fatalf("resolveMarkets inline: %v", err)
	}
	if source != marketsSourceEnvJSON {
		t.Fatalf("source = %q, want %q", source, marketsSourceEnvJSON)
	}
	if len(markets) != 1 || markets[0].Market != "ATOM/USDT" || markets[0].QuoteDenom != "uusdt" {
		t.Fatalf("unexpected markets: %+v", markets)
	}
}

// Inline JSON must win over a file source, and the file must not be read at all.
func TestResolveMarketsInlineWinsOverFile(t *testing.T) {
	markets, source, err := resolveMarkets(validMarketsJSON, "/some/path.json", failReadFile)
	if err != nil {
		t.Fatalf("resolveMarkets precedence: %v", err)
	}
	if source != marketsSourceEnvJSON {
		t.Fatalf("source = %q, want inline to win", source)
	}
	if markets[0].QuoteDenom != "uusdt" {
		t.Fatalf("quoteDenom = %q, want uusdt", markets[0].QuoteDenom)
	}
}

func TestResolveMarketsFile(t *testing.T) {
	read := func(path string) ([]byte, error) {
		if path != "/cfg/markets.json" {
			t.Fatalf("readFile path = %q", path)
		}
		return []byte(validMarketsJSON), nil
	}
	markets, source, err := resolveMarkets("", "/cfg/markets.json", read)
	if err != nil {
		t.Fatalf("resolveMarkets file: %v", err)
	}
	if source != "file:/cfg/markets.json" {
		t.Fatalf("source = %q", source)
	}
	if markets[0].BaseDenom != "uatom" {
		t.Fatalf("baseDenom = %q", markets[0].BaseDenom)
	}
}

func TestResolveMarketsFileReadError(t *testing.T) {
	read := func(string) ([]byte, error) { return nil, errors.New("no such file") }
	_, _, err := resolveMarkets("", "/missing.json", read)
	if err == nil || !strings.Contains(err.Error(), EnvOrderMarketsFile) {
		t.Fatalf("want file read error mentioning %s, got %v", EnvOrderMarketsFile, err)
	}
}

func TestParseMarketsJSONErrors(t *testing.T) {
	cases := map[string]string{
		"malformed":     `[{"market":`,
		"empty array":   `[]`,
		"unknown field": `[{"market":"X/Y","baseDenom":"a","quoteDenom":"b","tickSize":"1","lotSize":"1","status":"active","bogus":1}]`,
		"trailing data": validMarketsJSON + `garbage`,
		"not an array":  `{"market":"X/Y"}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMarketsJSON([]byte(in)); err == nil {
				t.Fatalf("parseMarketsJSON(%q) = nil error, want error", in)
			}
		})
	}
}

// End-to-end: a config seed flows through NewRealOrderService and is what
// GET /api/markets serves — proving DEN-D1 changes the live registry, not just
// a parser.
func TestConfigMarketsReachRealOrderService(t *testing.T) {
	markets, _, err := resolveMarkets(validMarketsJSON, "", failReadFile)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	svc, err := NewRealOrderService(state.NewOffchainStateManager(), markets, nil)
	if err != nil {
		t.Fatalf("NewRealOrderService: %v", err)
	}
	got := svc.ListMarkets(context.Background())
	if len(got.Markets) != 1 || got.Markets[0].QuoteDenom != "uusdt" {
		t.Fatalf("ListMarkets = %+v, want configured ATOM/USDT (uusdt)", got.Markets)
	}
}

// MarketsFromEnv reads the real process environment (inline JSON path).
func TestMarketsFromEnvInline(t *testing.T) {
	t.Setenv(EnvOrderMarketsFile, "")
	t.Setenv(EnvOrderMarketsJSON, validMarketsJSON)
	markets, source, err := MarketsFromEnv()
	if err != nil {
		t.Fatalf("MarketsFromEnv: %v", err)
	}
	if source != marketsSourceEnvJSON || markets[0].QuoteDenom != "uusdt" {
		t.Fatalf("source=%q markets=%+v", source, markets)
	}
}

// MarketsFromEnv reads a real file on disk (file path branch + os.ReadFile).
func TestMarketsFromEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "markets.json")
	if err := os.WriteFile(path, []byte(validMarketsJSON), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	t.Setenv(EnvOrderMarketsJSON, "")
	t.Setenv(EnvOrderMarketsFile, path)
	markets, source, err := MarketsFromEnv()
	if err != nil {
		t.Fatalf("MarketsFromEnv: %v", err)
	}
	if source != "file:"+path || markets[0].BaseDenom != "uatom" {
		t.Fatalf("source=%q markets=%+v", source, markets)
	}
}

// DEN-D2 anti-drift guard: the documented sample config
// (docs/matching_orderbook/markets.sample.json) must stay byte-equivalent to the
// canonical DefaultMarkets(). If someone edits one denom without the other, this
// fails — preventing a stray/mismatched denom from creeping back into the docs.
func TestSampleConfigMatchesDefaultMarkets(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "matching_orderbook", "markets.sample.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample config %s: %v", path, err)
	}
	got, err := parseMarketsJSON(data)
	if err != nil {
		t.Fatalf("parse sample config: %v", err)
	}
	if want := DefaultMarkets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("markets.sample.json drifted from DefaultMarkets()\n got=%+v\nwant=%+v", got, want)
	}
}

// A config that parses but is field-invalid must be rejected by the registry
// (single source of truth), so a bad seed is fatal at startup, not silent.
func TestInvalidMarketConfigRejectedByService(t *testing.T) {
	bad := []types.Market{{
		Market:     "ATOM/USDT",
		BaseDenom:  "uatom",
		QuoteDenom: "uusdt",
		TickSize:   "0", // not a positive decimal → invalid
		LotSize:    "1",
		Status:     types.MarketActive,
	}}
	if _, err := NewRealOrderService(state.NewOffchainStateManager(), bad, nil); err == nil {
		t.Fatal("NewRealOrderService accepted an invalid market seed, want error")
	}
}
