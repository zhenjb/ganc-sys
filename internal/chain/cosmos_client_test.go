package chain

import (
	"context"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/event"
)

// queueRunner replays a queue of canned outputs, one per Run call, so the deposit
// flow (broadcast, then optional query-by-hash) can be exercised without a chain.
type queueRunner struct {
	outputs [][]byte
	errs    []error

	callArgs [][]string
	calls    int
}

func (q *queueRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	idx := q.calls
	q.calls++
	q.callArgs = append(q.callArgs, append([]string{name}, args...))

	var out []byte
	if idx < len(q.outputs) {
		out = q.outputs[idx]
	}
	var err error
	if idx < len(q.errs) {
		err = q.errs[idx]
	}
	return out, err
}

func depositReq() DepositRequest {
	return DepositRequest{Owner: "cosmos1alice", Denom: "uusdc", Amount: "100"}
}

// blockModeOutput embeds the typed EventDeposit directly in the broadcast result,
// as obd does with --broadcast-mode block.
const blockModeOutput = `{
  "height": "42",
  "txhash": "DEADBEEF01",
  "code": 0,
  "raw_log": "",
  "events": [
    {
      "type": "ob.zkdex.v1.EventDeposit",
      "attributes": [
        {"key": "deposit_id", "value": "\"dep-7\""},
        {"key": "creator", "value": "\"cosmos1alice\""},
        {"key": "denom", "value": "\"uusdc\""},
        {"key": "amount", "value": "\"100\""}
      ]
    }
  ]
}`

func TestCosmosClientDepositParsesTxHashAndEvent(t *testing.T) {
	runner := &queueRunner{outputs: [][]byte{[]byte("gas estimate: 99\n" + blockModeOutput)}}
	client := NewCosmosClient(CosmosConfig{ChainID: "ganc-local", Node: "tcp://localhost:26657"}, runner)

	res, err := client.Deposit(context.Background(), depositReq())
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}

	if res.TxHash != "DEADBEEF01" {
		t.Fatalf("expected txHash=DEADBEEF01, got %q", res.TxHash)
	}
	if res.Height != 42 {
		t.Fatalf("expected height=42, got %d", res.Height)
	}
	if len(res.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(res.Events))
	}

	ev := res.Events[0]
	if ev.Type != event.TypeDeposit {
		t.Fatalf("expected event type %q, got %q", event.TypeDeposit, ev.Type)
	}
	if ev.Attributes["depositId"] != "dep-7" {
		t.Fatalf("expected depositId=dep-7 (unquoted), got %q", ev.Attributes["depositId"])
	}
	if ev.Attributes["creator"] != "cosmos1alice" {
		t.Fatalf("expected creator=cosmos1alice, got %q", ev.Attributes["creator"])
	}
	if ev.Attributes["denom"] != "uusdc" || ev.Attributes["amount"] != "100" {
		t.Fatalf("expected denom/amount uusdc/100, got %q/%q", ev.Attributes["denom"], ev.Attributes["amount"])
	}

	// Only one CLI call when the broadcast already contains the event.
	if runner.calls != 1 {
		t.Fatalf("expected 1 CLI call, got %d", runner.calls)
	}
}

func TestCosmosClientDepositPassesExpectedCLIArgs(t *testing.T) {
	runner := &queueRunner{outputs: [][]byte{[]byte(blockModeOutput)}}
	client := NewCosmosClient(CosmosConfig{
		Binary:  "obd",
		ChainID: "ganc-local",
		Node:    "tcp://localhost:26657",
		Fees:    "2000uusdc",
	}, runner)

	if _, err := client.Deposit(context.Background(), depositReq()); err != nil {
		t.Fatalf("deposit: %v", err)
	}

	got := strings.Join(runner.callArgs[0], " ")
	for _, want := range []string{
		"obd tx zkdex deposit uusdc 100",
		"--from cosmos1alice",
		"--chain-id ganc-local",
		"--keyring-backend test",
		"--broadcast-mode sync",
		"--node tcp://localhost:26657",
		"--fees 2000uusdc",
		"--output json",
		"-y",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected args to contain %q, got: %s", want, got)
		}
	}
}

// syncBroadcastOutput is what --broadcast-mode sync returns: the CheckTx result,
// with the txhash but no block events yet.
const syncBroadcastOutput = `{"height":"0","txhash":"SYNC55","code":0,"raw_log":"[]"}`

// queriedTxOutput is what `obd query tx <hash>` returns once the tx is in a
// block, with the deposit event under logs[].events[].
const queriedTxOutput = `{
  "height": "51",
  "txhash": "SYNC55",
  "code": 0,
  "logs": [
    {
      "events": [
        {
          "type": "ob.zkdex.v1.EventDeposit",
          "attributes": [
            {"key": "deposit_id", "value": "dep-99"},
            {"key": "creator", "value": "cosmos1alice"},
            {"key": "denom", "value": "uusdc"},
            {"key": "amount", "value": "100"}
          ]
        }
      ]
    }
  ]
}`

func TestCosmosClientDepositEnrichesViaQueryWhenSyncBroadcast(t *testing.T) {
	runner := &queueRunner{outputs: [][]byte{
		[]byte(syncBroadcastOutput), // broadcast: no events
		[]byte(queriedTxOutput),     // query tx: events present
	}}
	client := NewCosmosClient(CosmosConfig{ChainID: "ganc-local", Node: "tcp://localhost:26657"}, runner)

	res, err := client.Deposit(context.Background(), depositReq())
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}

	if res.TxHash != "SYNC55" {
		t.Fatalf("expected txHash=SYNC55, got %q", res.TxHash)
	}
	if res.Height != 51 {
		t.Fatalf("expected height enriched to 51, got %d", res.Height)
	}
	if res.Events[0].Attributes["depositId"] != "dep-99" {
		t.Fatalf("expected depositId=dep-99 from query, got %q", res.Events[0].Attributes["depositId"])
	}

	if runner.calls != 2 {
		t.Fatalf("expected 2 CLI calls (broadcast + query), got %d", runner.calls)
	}
	if !strings.Contains(strings.Join(runner.callArgs[1], " "), "query tx SYNC55") {
		t.Fatalf("expected second call to query tx, got: %v", runner.callArgs[1])
	}
}

func TestCosmosClientDepositRejectsOnNonZeroCode(t *testing.T) {
	runner := &queueRunner{outputs: [][]byte{
		[]byte(`{"txhash":"BAD01","code":11,"raw_log":"insufficient funds"}`),
	}}
	client := NewCosmosClient(CosmosConfig{}, runner)

	if _, err := client.Deposit(context.Background(), depositReq()); err == nil {
		t.Fatalf("expected error on non-zero code")
	} else if !strings.Contains(err.Error(), "insufficient funds") {
		t.Fatalf("expected chain reason in error, got %v", err)
	}
}

func TestCosmosClientDepositErrorsWhenEventNeverFound(t *testing.T) {
	// Broadcast has no events and the query also returns none -> hard error so the
	// caller never fabricates a depositId.
	runner := &queueRunner{outputs: [][]byte{
		[]byte(syncBroadcastOutput),
		[]byte(`{"txhash":"SYNC55","code":0,"logs":[]}`),
	}}
	client := NewCosmosClient(CosmosConfig{}, runner)

	if _, err := client.Deposit(context.Background(), depositReq()); err == nil {
		t.Fatalf("expected error when EventDeposit is never found")
	}
}

func TestCosmosClientDepositValidatesInput(t *testing.T) {
	runner := &queueRunner{outputs: [][]byte{[]byte(blockModeOutput)}}
	client := NewCosmosClient(CosmosConfig{}, runner)

	cases := []DepositRequest{
		{Owner: "", Denom: "uusdc", Amount: "100"},
		{Owner: "cosmos1alice", Denom: "", Amount: "100"},
		{Owner: "cosmos1alice", Denom: "uusdc", Amount: "0"},
		{Owner: "cosmos1alice", Denom: "uusdc", Amount: "-5"},
		{Owner: "cosmos1alice", Denom: "uusdc", Amount: "abc"},
	}
	for i, c := range cases {
		if _, err := client.Deposit(context.Background(), c); err == nil {
			t.Fatalf("case %d: expected validation error for %+v", i, c)
		}
	}
	if runner.calls != 0 {
		t.Fatalf("runner must not be invoked when validation fails, calls=%d", runner.calls)
	}
}
