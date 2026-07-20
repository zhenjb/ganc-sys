package relayer

import (
	"context"
	"testing"
	"time"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// seqRunner replays a sequence of canned outputs (one per Run call), so a claim's
// broadcast (CheckTx) and its follow-up `query tx` (DeliverTx) can differ — needed
// to test the wait-for-commit path (Nhóm 4 (a)). The last output repeats.
type seqRunner struct {
	outs  [][]byte
	calls int
}

func (r *seqRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	i := r.calls
	r.calls++
	if i >= len(r.outs) {
		i = len(r.outs) - 1
	}
	return r.outs[i], nil
}

func claimInput() ClaimWithdrawInput {
	return ClaimWithdrawInput{WithdrawRecord: types.WithdrawRecord{
		WithdrawID: "w-1", Owner: "cosmos1alice", Denom: "uusdc", Amount: "40", Destination: "cosmos1bob",
	}}
}

func commitCfg() CosmosConfig {
	return CosmosConfig{
		ChainID: "ganc-local", From: "relayer",
		WaitForCommit: true, ConfirmAttempts: 1, ConfirmInterval: time.Millisecond,
	}
}

// Nhóm 4 (a): with WaitForCommit, a claim whose CheckTx passes (code 0) but which
// FAILS in DeliverTx (in-block code != 0) must return an error and NOT mark the
// record claimed — the old code (CheckTx-only) reported it as success.
func TestClaimWithdrawRejectedInBlock(t *testing.T) {
	runner := &seqRunner{outs: [][]byte{
		[]byte(`{"txhash":"CLAIM01","code":0,"raw_log":""}`),                       // broadcast: CheckTx ok
		[]byte(`{"txhash":"CLAIM01","code":11,"raw_log":"withdraw already claimed"}`), // in-block: fail
	}}
	client := NewCosmosClient(commitCfg(), runner)

	res, err := client.ClaimWithdraw(context.Background(), claimInput())
	if err == nil {
		t.Fatal("expected error when claim fails in-block, got nil")
	}
	if res.WithdrawRecord.Claimed {
		t.Fatal("record must NOT be marked claimed when the in-block tx failed")
	}
	if runner.calls < 2 {
		t.Fatalf("expected broadcast + waitForTxCommit (>=2 calls), got %d", runner.calls)
	}
}

// Nhóm 4 (a): CheckTx ok + DeliverTx ok (code 0 in-block) → success, claimed=true.
func TestClaimWithdrawCommittedOK(t *testing.T) {
	runner := &seqRunner{outs: [][]byte{
		[]byte(`{"txhash":"CLAIM01","code":0,"raw_log":""}`),
		[]byte(`{"txhash":"CLAIM01","code":0,"raw_log":""}`),
	}}
	client := NewCosmosClient(commitCfg(), runner)

	res, err := client.ClaimWithdraw(context.Background(), claimInput())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if res.TxHash != "CLAIM01" || !res.WithdrawRecord.Claimed {
		t.Fatalf("want committed claim (CLAIM01, claimed), got %+v", res)
	}
	if runner.calls < 2 {
		t.Fatalf("expected the wait-for-commit query to run, calls=%d", runner.calls)
	}
}
