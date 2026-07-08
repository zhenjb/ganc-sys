package indexer

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/event"
)

// modern CometBFT tx_search: plain-string attributes, values JSON-quoted for
// typed events.
const txSearchModernPayload = `{
  "jsonrpc": "2.0",
  "id": -1,
  "result": {
    "total_count": "2",
    "txs": [
      {
        "hash": "TX_AAA",
        "height": "10",
        "tx_result": {
          "events": [
            {"type": "tx", "attributes": [{"key": "fee", "value": "0uusdc"}]},
            {
              "type": "ob.zkdex.v1.EventDeposit",
              "attributes": [
                {"key": "deposit_id", "value": "\"dep-10\""},
                {"key": "creator", "value": "\"cosmos1alice\""},
                {"key": "denom", "value": "\"uusdc\""},
                {"key": "amount", "value": "\"100\""}
              ]
            }
          ]
        }
      },
      {
        "hash": "TX_BBB",
        "height": "12",
        "tx_result": {
          "events": [
            {
              "type": "ob.zkdex.v1.EventDeposit",
              "attributes": [
                {"key": "deposit_id", "value": "\"dep-12\""},
                {"key": "creator", "value": "\"cosmos1bob\""},
                {"key": "denom", "value": "\"uusdc\""},
                {"key": "amount", "value": "\"40\""}
              ]
            }
          ]
        }
      }
    ]
  }
}`

func TestTendermintSourceParsesModernPayload(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		fmt.Fprint(w, txSearchModernPayload)
	}))
	defer srv.Close()

	source := NewTendermintEventSource(srv.URL)
	txs, next, err := source.FetchDepositsSince(context.Background(), 5)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	// Height filter must be encoded into the tx_search query.
	if !strings.Contains(gotQuery, "tx.height > 5") {
		t.Fatalf("expected height filter in query, got %q", gotQuery)
	}

	if len(txs) != 2 {
		t.Fatalf("expected 2 deposit txs, got %d", len(txs))
	}
	if next != 12 {
		t.Fatalf("expected nextHeight=12, got %d", next)
	}

	first := txs[0]
	if first.TxHash != "TX_AAA" || first.Height != 10 {
		t.Fatalf("unexpected first tx: %+v", first)
	}
	if first.Events[0].Type != event.TypeDeposit {
		t.Fatalf("expected typed deposit event, got %q", first.Events[0].Type)
	}
	attrs := first.Events[0].Attributes
	if attrs["depositId"] != "dep-10" || attrs["creator"] != "cosmos1alice" ||
		attrs["denom"] != "uusdc" || attrs["amount"] != "100" {
		t.Fatalf("unexpected deposit attrs: %+v", attrs)
	}
}

func TestTendermintSourceParsesLegacyBase64Payload(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

	payload := fmt.Sprintf(`{
      "result": {
        "txs": [
          {
            "hash": "TX_LEGACY",
            "height": "7",
            "tx_result": {
              "events": [
                {
                  "type": "ob.zkdex.v1.EventDeposit",
                  "attributes": [
                    {"key": "%s", "value": "%s"},
                    {"key": "%s", "value": "%s"},
                    {"key": "%s", "value": "%s"},
                    {"key": "%s", "value": "%s"}
                  ]
                }
              ]
            }
          }
        ]
      }
    }`,
		b64("deposit_id"), b64("dep-7"),
		b64("creator"), b64("cosmos1alice"),
		b64("denom"), b64("uusdc"),
		b64("amount"), b64("100"),
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	source := NewTendermintEventSource(srv.URL).WithLegacyBase64Attributes()
	txs, next, err := source.FetchDepositsSince(context.Background(), 0)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(txs) != 1 || next != 7 {
		t.Fatalf("expected 1 tx at height 7, got %d txs next=%d", len(txs), next)
	}
	attrs := txs[0].Events[0].Attributes
	if attrs["depositId"] != "dep-7" || attrs["creator"] != "cosmos1alice" {
		t.Fatalf("legacy base64 decode failed: %+v", attrs)
	}
}

func TestTendermintSourceReturnsRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"parse error","data":"bad query"}}`)
	}))
	defer srv.Close()

	source := NewTendermintEventSource(srv.URL)
	if _, _, err := source.FetchDepositsSince(context.Background(), 0); err == nil {
		t.Fatalf("expected RPC error to surface")
	}
}

func TestTendermintSourceEmptyResultKeepsCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"result":{"total_count":"0","txs":[]}}`)
	}))
	defer srv.Close()

	source := NewTendermintEventSource(srv.URL)
	txs, next, err := source.FetchDepositsSince(context.Background(), 99)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(txs) != 0 {
		t.Fatalf("expected no txs, got %d", len(txs))
	}
	if next != 99 {
		t.Fatalf("expected cursor unchanged at 99, got %d", next)
	}
}
