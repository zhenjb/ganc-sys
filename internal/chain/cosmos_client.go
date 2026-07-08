package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/zhenjb/ganc-sys/internal/event"
)

// CommandRunner abstracts process execution so the Cosmos deposit client can be
// unit-tested without a live chain or the obd binary. It mirrors the relayer's
// runner so both chain-facing clients share the same shell-out contract.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs the configured chain binary as a child process.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// CosmosConfig holds the connection / signing parameters for the obd chain CLI.
//
// All values come from environment variables wired in cmd/api. The backend
// stays free of the heavy cosmos-sdk dependency by shelling out to obd, which
// already knows how to build, sign and broadcast MsgDeposit.
type CosmosConfig struct {
	Binary         string // chain binary, e.g. "obd"
	ChainID        string // --chain-id
	Node           string // --node tcp://host:26657
	KeyringBackend string // --keyring-backend (test|os|file)
	Home           string // --home (optional)
	Gas            string // --gas (default "auto")
	GasAdjustment  string // --gas-adjustment (default "1.5")
	GasPrices      string // --gas-prices (e.g. "0.025uusdc"); used if Fees empty
	Fees           string // --fees (e.g. "2000uusdc"); takes precedence over GasPrices
	BroadcastMode  string // --broadcast-mode (default "sync")

	// TxQueryRetries / TxQueryInterval control how long Deposit polls `query tx`
	// for the committed EventDeposit. --broadcast-mode sync returns before block
	// inclusion, so the chain-assigned depositId is only readable after commit.
	TxQueryRetries  int           // default 20
	TxQueryInterval time.Duration // default 1s
}

func (c CosmosConfig) withDefaults() CosmosConfig {
	if strings.TrimSpace(c.Binary) == "" {
		c.Binary = "obd"
	}
	if strings.TrimSpace(c.KeyringBackend) == "" {
		c.KeyringBackend = "test"
	}
	if strings.TrimSpace(c.Gas) == "" {
		c.Gas = "auto"
	}
	if strings.TrimSpace(c.GasAdjustment) == "" {
		c.GasAdjustment = "1.5"
	}
	if strings.TrimSpace(c.BroadcastMode) == "" {
		c.BroadcastMode = "sync"
	}
	if c.TxQueryRetries <= 0 {
		c.TxQueryRetries = 20
	}
	if c.TxQueryInterval <= 0 {
		c.TxQueryInterval = 1 * time.Second
	}
	return c
}

// CosmosClient is a real deposit chain client that builds, signs and broadcasts
// MsgDeposit to the on-chain x/zkdex module via the obd CLI.
//
//	obd tx zkdex deposit <denom> <amount> --from <owner> ...
//
// The signer (--from) is the deposit owner: a deposit moves the user's own
// funds into the module, so the owner's key must exist in the relayer keyring
// for dev/demo. The returned TxResult carries the real txHash and the typed
// EventDeposit so the deposit indexer is agnostic to LocalClient vs CosmosClient.
//
// When the broadcast output does not embed the EventDeposit (e.g. --broadcast-mode
// sync returns the CheckTx result before block inclusion), the client queries the
// tx by hash once to enrich the deposit event.
type CosmosClient struct {
	cfg    CosmosConfig
	runner CommandRunner
}

func NewCosmosClient(cfg CosmosConfig, runner CommandRunner) *CosmosClient {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &CosmosClient{
		cfg:    cfg.withDefaults(),
		runner: runner,
	}
}

var _ Client = (*CosmosClient)(nil)

func (c *CosmosClient) Deposit(ctx context.Context, req DepositRequest) (TxResult, error) {
	owner := strings.TrimSpace(req.Owner)
	denom := strings.TrimSpace(req.Denom)
	amount := strings.TrimSpace(req.Amount)

	if owner == "" || denom == "" || amount == "" {
		return TxResult{}, fmt.Errorf("owner, denom and amount are required")
	}
	if v, err := strconv.ParseInt(amount, 10, 64); err != nil || v <= 0 {
		return TxResult{}, fmt.Errorf("amount must be a positive integer string")
	}

	args := append(
		[]string{"tx", "zkdex", "deposit", denom, amount},
		c.commonTxArgs(owner)...,
	)

	out, runErr := c.runner.Run(ctx, c.cfg.Binary, args...)
	tx, parseErr := parseTxResult(out)
	if runErr != nil && tx.TxHash == "" {
		return TxResult{}, fmt.Errorf("deposit failed: %w: %s", runErr, strings.TrimSpace(string(out)))
	}
	if parseErr != nil {
		return TxResult{}, fmt.Errorf("deposit: %w", parseErr)
	}
	if tx.Code != 0 {
		return TxResult{}, fmt.Errorf("deposit rejected by chain (code=%d): %s", tx.Code, tx.RawLog)
	}

	depositEvent, ok := extractDepositEvent(tx, owner, denom, amount)

	// --broadcast-mode sync returns the CheckTx result BEFORE the tx is in a
	// block, so the typed EventDeposit (which carries the chain-assigned
	// depositId = "dep-<creator>-<height>-<rand>") is absent from the broadcast
	// output. Poll `query tx` until the committed tx surfaces the event.
	if !ok {
		for attempt := 0; attempt < c.cfg.TxQueryRetries; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return TxResult{}, ctx.Err()
				case <-time.After(c.cfg.TxQueryInterval):
				}
			}
			queried, qErr := c.queryTx(ctx, tx.TxHash)
			if qErr != nil {
				continue // not committed / queryable yet
			}
			if queried.Code != 0 {
				return TxResult{}, fmt.Errorf("deposit rejected by chain (code=%d): %s", queried.Code, queried.RawLog)
			}
			if ev, found := extractDepositEvent(queried, owner, denom, amount); found {
				depositEvent = ev
				ok = true
				if queried.Height != 0 {
					tx.Height = queried.Height
				}
				break
			}
		}
	}

	if !ok {
		return TxResult{}, fmt.Errorf("deposit tx %s committed but EventDeposit not found after polling (depositId unavailable)", tx.TxHash)
	}

	return TxResult{
		TxHash: tx.TxHash,
		Height: tx.Height,
		Events: []event.Event{depositEvent},
	}, nil
}

func (c *CosmosClient) queryTx(ctx context.Context, txHash string) (txResult, error) {
	args := []string{"query", "tx", txHash, "--output", "json"}
	if strings.TrimSpace(c.cfg.Node) != "" {
		args = append(args, "--node", c.cfg.Node)
	}
	if strings.TrimSpace(c.cfg.Home) != "" {
		args = append(args, "--home", c.cfg.Home)
	}

	out, err := c.runner.Run(ctx, c.cfg.Binary, args...)
	if err != nil {
		return txResult{}, err
	}
	return parseTxResult(out)
}

// commonTxArgs builds the shared obd tx flags for the given signer.
func (c *CosmosClient) commonTxArgs(signer string) []string {
	args := []string{
		"--from", signer,
		"--chain-id", c.cfg.ChainID,
		"--keyring-backend", c.cfg.KeyringBackend,
		"--gas", c.cfg.Gas,
		"--gas-adjustment", c.cfg.GasAdjustment,
		"--broadcast-mode", c.cfg.BroadcastMode,
		"--output", "json",
		"-y",
	}
	if strings.TrimSpace(c.cfg.Node) != "" {
		args = append(args, "--node", c.cfg.Node)
	}
	if strings.TrimSpace(c.cfg.Home) != "" {
		args = append(args, "--home", c.cfg.Home)
	}
	if strings.TrimSpace(c.cfg.Fees) != "" {
		args = append(args, "--fees", c.cfg.Fees)
	} else if strings.TrimSpace(c.cfg.GasPrices) != "" {
		args = append(args, "--gas-prices", c.cfg.GasPrices)
	}
	return args
}

// txResult is the subset of an obd tx/query JSON response we rely on, including
// the event carriers (top-level events and per-log events) used to locate the
// typed EventDeposit emitted by x/zkdex.
type txResult struct {
	TxHash string      `json:"txhash"`
	Code   int         `json:"code"`
	RawLog string      `json:"raw_log"`
	Height int64       `json:"-"`
	Events []txEvent   `json:"events"`
	Logs   []txABCILog `json:"logs"`
}

type txABCILog struct {
	Events []txEvent `json:"events"`
}

type txEvent struct {
	Type       string             `json:"type"`
	Attributes []txEventAttribute `json:"attributes"`
}

type txEventAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// heightEnvelope decodes the height separately because obd renders it as a JSON
// string ("123") in tx responses, which would fail an int64 unmarshal.
type heightEnvelope struct {
	Height string `json:"height"`
}

// parseTxResult extracts the broadcast/query result. The obd CLI may print
// warnings (e.g. gas estimate) before the JSON body, so we scan for the JSON
// object boundaries.
func parseTxResult(out []byte) (txResult, error) {
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return txResult{}, fmt.Errorf("empty CLI output")
	}

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < 0 || end < start {
		return txResult{}, fmt.Errorf("no JSON object in CLI output: %s", raw)
	}
	body := raw[start : end+1]

	var tx txResult
	if err := json.Unmarshal([]byte(body), &tx); err != nil {
		return txResult{}, fmt.Errorf("decode CLI output: %w: %s", err, raw)
	}
	if tx.TxHash == "" {
		return txResult{}, fmt.Errorf("CLI output missing txhash: %s", raw)
	}

	var h heightEnvelope
	if err := json.Unmarshal([]byte(body), &h); err == nil {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(h.Height), 10, 64); err == nil {
			tx.Height = parsed
		}
	}

	return tx, nil
}

// extractDepositEvent locates the typed EventDeposit in either the top-level
// events array or the per-log events, normalizes the camelCase/snake_case +
// JSON-quoted attribute values, and returns the indexer-friendly event.
//
// The on-chain creator/denom/amount are preferred; the request values are used
// only as a fallback when an attribute is absent from the event payload.
func extractDepositEvent(tx txResult, owner, denom, amount string) (event.Event, bool) {
	all := make([]txEvent, 0, len(tx.Events))
	all = append(all, tx.Events...)
	for _, lg := range tx.Logs {
		all = append(all, lg.Events...)
	}

	for _, ev := range all {
		if !strings.Contains(ev.Type, "EventDeposit") {
			continue
		}

		attrs := map[string]string{}
		for _, a := range ev.Attributes {
			attrs[normalizeAttrKey(a.Key)] = unquoteAttrValue(a.Value)
		}

		depositID := firstNonEmpty(attrs["depositId"], attrs["deposit_id"])
		if depositID == "" {
			continue
		}

		return event.Event{
			Type: event.TypeDeposit,
			Attributes: map[string]string{
				"depositId": depositID,
				"creator":   firstNonEmpty(attrs["creator"], attrs["owner"], owner),
				"denom":     firstNonEmpty(attrs["denom"], denom),
				"amount":    firstNonEmpty(attrs["amount"], amount),
			},
		}, true
	}

	return event.Event{}, false
}

func normalizeAttrKey(key string) string {
	return strings.TrimSpace(key)
}

// unquoteAttrValue strips the surrounding double quotes that cosmos-sdk typed
// events add when JSON-encoding attribute values ("\"dep-1\"" -> "dep-1").
func unquoteAttrValue(value string) string {
	v := strings.TrimSpace(value)
	if len(v) >= 2 && strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
		if unquoted, err := strconv.Unquote(v); err == nil {
			return unquoted
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
