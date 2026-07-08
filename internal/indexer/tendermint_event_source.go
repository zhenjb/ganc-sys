package indexer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/event"
)

const defaultTendermintTimeout = 10 * time.Second

// depositEventTypeMatch matches both the typed proto event name
// (ob.zkdex.v1.EventDeposit) and any "EventDeposit"-suffixed variant the chain
// might emit, so the source is resilient to event-name namespacing changes.
const depositEventTypeMatch = "EventDeposit"

// TendermintEventSource reads real DepositQueued/EventDeposit events from a
// CometBFT/Tendermint RPC endpoint via /tx_search.
//
// It is poll-based: each FetchDepositsSince call issues a tx_search query for
// deposit events at heights above the cursor and converts each matching tx into
// a chain.TxResult carrying the typed EventDeposit. The DepositIndexer then
// mirrors those into the deposit store and the off-chain settlement state.
type TendermintEventSource struct {
	rpcURL       string
	query        string
	httpClient   *http.Client
	legacyBase64 bool
}

func NewTendermintEventSource(rpcURL string) *TendermintEventSource {
	return &TendermintEventSource{
		rpcURL: strings.TrimRight(rpcURL, "/"),
		// Match any tx that emitted a deposit event. tx_search requires at least
		// one condition; EXISTS on the deposit_id attribute selects deposit txs.
		query:      "ob.zkdex.v1.EventDeposit.deposit_id EXISTS",
		httpClient: &http.Client{Timeout: defaultTendermintTimeout},
	}
}

// WithQuery overrides the tx_search condition (useful for tests or alternate
// event names). The height filter is appended automatically per fetch.
func (s *TendermintEventSource) WithQuery(query string) *TendermintEventSource {
	s.query = query
	return s
}

// WithLegacyBase64Attributes enables base64 decoding of event attribute
// keys/values, the encoding used by Tendermint <= 0.34.26. Modern CometBFT
// returns plain strings in /tx_search, which is the default. Decoding is explicit
// (not auto-detected) because plenty of plain values are also valid base64, so
// auto-detection would silently corrupt them.
func (s *TendermintEventSource) WithLegacyBase64Attributes() *TendermintEventSource {
	s.legacyBase64 = true
	return s
}

var _ DepositEventSource = (*TendermintEventSource)(nil)

func (s *TendermintEventSource) FetchDepositsSince(ctx context.Context, fromHeight int64) ([]chain.TxResult, int64, error) {
	if s.rpcURL == "" {
		return nil, fromHeight, fmt.Errorf("tendermint RPC URL is required")
	}

	query := s.query
	if fromHeight > 0 {
		query = fmt.Sprintf("%s AND tx.height > %d", s.query, fromHeight)
	}

	endpoint := s.rpcURL + "/tx_search?query=" + url.QueryEscape("\""+query+"\"") + "&order_by=" + url.QueryEscape("\"asc\"")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fromHeight, err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fromHeight, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fromHeight, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fromHeight, fmt.Errorf("tx_search failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	return parseTxSearchResponse(body, fromHeight, s.legacyBase64)
}

// --- tx_search response shapes (CometBFT JSON-RPC) ---

type txSearchEnvelope struct {
	Result txSearchResult  `json:"result"`
	Error  *txSearchRPCErr `json:"error"`
}

type txSearchRPCErr struct {
	Message string `json:"message"`
	Data    string `json:"data"`
}

type txSearchResult struct {
	Txs []txSearchTx `json:"txs"`
}

type txSearchTx struct {
	Hash     string          `json:"hash"`
	Height   string          `json:"height"`
	TxResult txSearchResult2 `json:"tx_result"`
}

type txSearchResult2 struct {
	Events []txSearchEvent `json:"events"`
}

type txSearchEvent struct {
	Type       string                   `json:"type"`
	Attributes []txSearchEventAttribute `json:"attributes"`
}

type txSearchEventAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// parseTxSearchResponse converts a CometBFT /tx_search payload into deposit
// chain.TxResults. Exported indirectly through the source so tests can assert
// parsing against a sample payload without a live RPC.
func parseTxSearchResponse(body []byte, fromHeight int64, legacyBase64 bool) ([]chain.TxResult, int64, error) {
	var env txSearchEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fromHeight, fmt.Errorf("decode tx_search: %w: %s", err, string(body))
	}
	if env.Error != nil {
		return nil, fromHeight, fmt.Errorf("tx_search rpc error: %s %s", env.Error.Message, env.Error.Data)
	}

	results := make([]chain.TxResult, 0, len(env.Result.Txs))
	nextHeight := fromHeight

	for _, tx := range env.Result.Txs {
		height, _ := strconv.ParseInt(strings.TrimSpace(tx.Height), 10, 64)

		depositEvent, ok := depositEventFromTxSearch(tx.TxResult.Events, legacyBase64)
		if !ok {
			continue
		}

		results = append(results, chain.TxResult{
			TxHash: tx.Hash,
			Height: height,
			Events: []event.Event{depositEvent},
		})

		if height > nextHeight {
			nextHeight = height
		}
	}

	return results, nextHeight, nil
}

func depositEventFromTxSearch(events []txSearchEvent, legacyBase64 bool) (event.Event, bool) {
	for _, ev := range events {
		if !strings.Contains(ev.Type, depositEventTypeMatch) {
			continue
		}

		attrs := map[string]string{}
		for _, a := range ev.Attributes {
			attrs[decodeAttrKey(a.Key, legacyBase64)] = decodeAttrValue(a.Value, legacyBase64)
		}

		depositID := firstNonEmpty(attrs["depositId"], attrs["deposit_id"])
		creator := firstNonEmpty(attrs["creator"], attrs["owner"])
		denom := attrs["denom"]
		amount := attrs["amount"]

		if depositID == "" {
			continue
		}

		return event.Event{
			Type: event.TypeDeposit,
			Attributes: map[string]string{
				"depositId": depositID,
				"creator":   creator,
				"denom":     denom,
				"amount":    amount,
			},
		}, true
	}

	return event.Event{}, false
}

// decodeAttrKey / decodeAttrValue normalize attribute encoding. When legacyBase64
// is set, keys/values are base64-decoded (Tendermint <= 0.34.26); otherwise they
// are taken as-is (modern CometBFT). Typed cosmos events additionally JSON-quote
// the value, which is always stripped.
func decodeAttrKey(key string, legacyBase64 bool) string {
	if legacyBase64 {
		key = base64Decode(key)
	}
	return strings.TrimSpace(key)
}

func decodeAttrValue(value string, legacyBase64 bool) string {
	value = strings.TrimSpace(value)
	if legacyBase64 {
		value = base64Decode(value)
	}
	return unquote(value)
}

func base64Decode(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return string(decoded)
	}
	return s
}

func unquote(value string) string {
	v := strings.TrimSpace(value)
	if len(v) >= 2 && strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
		if unq, err := strconv.Unquote(v); err == nil {
			return unq
		}
		return strings.Trim(v, "\"")
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
