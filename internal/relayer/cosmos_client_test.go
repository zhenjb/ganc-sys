package relayer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// fakeRunner is a deterministic CommandRunner stand-in so the Cosmos relayer can
// be unit-tested without a live chain or the obd binary. It captures the last
// invocation and replays a canned output/error.
type fakeRunner struct {
	out []byte
	err error

	gotName string
	gotArgs []string
	calls   int
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls++
	f.gotName = name
	f.gotArgs = append([]string(nil), args...)
	return f.out, f.err
}

// flagValue returns the argument immediately following the named flag.
func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func sampleSubmitInput() SubmitBatchInput {
	return SubmitBatchInput{
		SettlementUpdate: types.SettlementUpdate{
			BatchID:      "batch-1",
			OldStateRoot: "0xrootA",
			NewStateRoot: "0xrootB",
			Withdrawals: []types.SettlementWithdrawal{
				{
					WithdrawID:  "w-1",
					Owner:       "cosmos1alice",
					Denom:       "uusdc",
					Amount:      "40",
					Destination: "cosmos1alice",
					Nullifier:   "0xnull",
				},
			},
		},
		BatchCommitments: types.BatchCommitments{
			DepositsRoot:        "0xdepositsRoot",
			WithdrawalsRoot:     "0xwithdrawalsRoot",
			NullifiersRoot:      "0xnullifiersRoot",
			WithdrawOutputsRoot: "0xwithdrawOutputsRoot",
		},
		ProofBundle: types.ProofBundle{
			Proof: "0xrealproof",
			PublicInputs: []string{
				"0xrootA", "0xrootB", "0xdepositsRoot",
				"0xwithdrawalsRoot", "0xnullifiersRoot", "0xwithdrawOutputsRoot",
			},
			VerificationKeyID: "gazk-balance-smoke-v1",
		},
	}
}

func TestCosmosClientSubmitBatchBroadcastsAndParsesTxHash(t *testing.T) {
	runner := &fakeRunner{out: []byte(`gas estimate: 120000
{"txhash":"ABCDEF0123","code":0,"raw_log":""}`)}

	client := NewCosmosClient(CosmosConfig{
		Binary:  "obd",
		ChainID: "ganc-local",
		Node:    "tcp://localhost:26657",
		From:    "relayer",
	}, runner)

	result, err := client.SubmitBatch(context.Background(), sampleSubmitInput())
	if err != nil {
		t.Fatalf("submit batch: %v", err)
	}

	if result.TxHash != "ABCDEF0123" {
		t.Fatalf("expected txHash=ABCDEF0123, got %q", result.TxHash)
	}
	if !result.Accepted {
		t.Fatalf("expected accepted=true")
	}
	if result.ProofStatus != "accepted" {
		t.Fatalf("expected proofStatus=accepted, got %q", result.ProofStatus)
	}
	if len(result.WithdrawRecords) != 1 || result.WithdrawRecords[0].WithdrawID != "w-1" {
		t.Fatalf("expected one withdraw record w-1, got %+v", result.WithdrawRecords)
	}
	if result.WithdrawRecords[0].Claimed {
		t.Fatalf("freshly submitted withdraw record must not be claimed")
	}
}

func TestCosmosClientSubmitBatchPassesExpectedCLIArgs(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"txhash":"AA","code":0,"raw_log":""}`)}

	client := NewCosmosClient(CosmosConfig{
		ChainID:        "ganc-local",
		Node:           "tcp://localhost:26657",
		From:           "relayer",
		KeyringBackend: "test",
		Home:           "/tmp/obd",
		Fees:           "2000uusdc",
	}, runner)

	if _, err := client.SubmitBatch(context.Background(), sampleSubmitInput()); err != nil {
		t.Fatalf("submit batch: %v", err)
	}

	if runner.gotName != "obd" {
		t.Fatalf("expected binary obd, got %q", runner.gotName)
	}

	args := strings.Join(runner.gotArgs, " ")
	for _, want := range []string{
		"tx zkdex submit-batch-proof",
		"--settlement-update",
		"--batch-commitments",
		"--proof-bundle",
		"--from relayer",
		"--chain-id ganc-local",
		"--keyring-backend test",
		"--node tcp://localhost:26657",
		"--home /tmp/obd",
		"--fees 2000uusdc",
		"--output json",
		"-y",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("expected args to contain %q, got: %s", want, args)
		}
	}

	// The three autocli flags must carry the payload (NOT a positional file, the
	// old — and chain-incompatible — shape).
	su, ok := flagValue(runner.gotArgs, "--settlement-update")
	if !ok || !strings.Contains(su, `"batchId":"batch-1"`) {
		t.Fatalf("--settlement-update should carry the settlement JSON, got %q", su)
	}
	bc, ok := flagValue(runner.gotArgs, "--batch-commitments")
	if !ok || !strings.Contains(bc, `"depositsRoot":"0xdepositsRoot"`) {
		t.Fatalf("--batch-commitments should carry the commitments JSON, got %q", bc)
	}
	pb, ok := flagValue(runner.gotArgs, "--proof-bundle")
	if !ok || strings.TrimSpace(pb) == "" {
		t.Fatalf("--proof-bundle should carry a temp file path, got %q", pb)
	}
}

func TestBuildSubmitBatchProofFlags(t *testing.T) {
	su, bc, pb, err := buildSubmitBatchProofFlags(sampleSubmitInput())
	if err != nil {
		t.Fatalf("build flags: %v", err)
	}

	var settlement types.SettlementUpdate
	if err := json.Unmarshal([]byte(su), &settlement); err != nil {
		t.Fatalf("settlement-update is not valid JSON: %v", err)
	}
	if settlement.BatchID != "batch-1" {
		t.Fatalf("expected batchId=batch-1, got %q", settlement.BatchID)
	}

	var commitments types.BatchCommitments
	if err := json.Unmarshal([]byte(bc), &commitments); err != nil {
		t.Fatalf("batch-commitments is not valid JSON: %v", err)
	}
	if commitments.DepositsRoot != "0xdepositsRoot" {
		t.Fatalf("expected depositsRoot=0xdepositsRoot, got %q", commitments.DepositsRoot)
	}

	// proof-bundle bytes must be EXACTLY {proof, publicInputs}: the on-chain
	// keeper (agreementProofBundle) reads only these and must NOT receive
	// verificationKeyId or other fields.
	var bundle map[string]json.RawMessage
	if err := json.Unmarshal(pb, &bundle); err != nil {
		t.Fatalf("proof-bundle is not valid JSON: %v", err)
	}
	if _, ok := bundle["proof"]; !ok {
		t.Fatalf("proof-bundle must contain proof")
	}
	if _, ok := bundle["publicInputs"]; !ok {
		t.Fatalf("proof-bundle must contain publicInputs")
	}
	if _, ok := bundle["verificationKeyId"]; ok {
		t.Fatalf("proof-bundle must NOT contain verificationKeyId (chain does not expect it)")
	}

	var decoded chainProofBundle
	if err := json.Unmarshal(pb, &decoded); err != nil {
		t.Fatalf("proof-bundle decode: %v", err)
	}
	if len(decoded.PublicInputs) != 6 {
		t.Fatalf("expected 6 public inputs, got %d", len(decoded.PublicInputs))
	}
}

func TestCosmosClientSubmitBatchRejectsOnNonZeroCode(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"txhash":"BB","code":5,"raw_log":"proof verification failed"}`)}

	client := NewCosmosClient(CosmosConfig{From: "relayer"}, runner)

	result, err := client.SubmitBatch(context.Background(), sampleSubmitInput())
	if err == nil {
		t.Fatalf("expected error on non-zero code")
	}
	if result.Accepted {
		t.Fatalf("expected accepted=false on rejection")
	}
	if result.ProofStatus != "rejected" {
		t.Fatalf("expected proofStatus=rejected, got %q", result.ProofStatus)
	}
	if !strings.Contains(err.Error(), "proof verification failed") {
		t.Fatalf("expected rejection reason in error, got %v", err)
	}
}

func TestCosmosClientSubmitBatchValidatesInput(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"txhash":"AA","code":0}`)}
	client := NewCosmosClient(CosmosConfig{From: "relayer"}, runner)

	bad := sampleSubmitInput()
	bad.ProofBundle.PublicInputs = []string{"0x1"} // wrong arity

	if _, err := client.SubmitBatch(context.Background(), bad); err == nil {
		t.Fatalf("expected validation error for wrong public-input arity")
	}
	if runner.calls != 0 {
		t.Fatalf("runner must not be invoked when validation fails, calls=%d", runner.calls)
	}
}

func TestCosmosClientClaimWithdrawSignsWithDestination(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"txhash":"CLAIM01","code":0,"raw_log":""}`)}

	client := NewCosmosClient(CosmosConfig{ChainID: "ganc-local", From: "relayer"}, runner)

	result, err := client.ClaimWithdraw(context.Background(), ClaimWithdrawInput{
		WithdrawRecord: types.WithdrawRecord{
			WithdrawID:  "w-1",
			Owner:       "cosmos1alice",
			Denom:       "uusdc",
			Amount:      "40",
			Destination: "cosmos1bob",
		},
	})
	if err != nil {
		t.Fatalf("claim withdraw: %v", err)
	}

	if result.TxHash != "CLAIM01" {
		t.Fatalf("expected txHash=CLAIM01, got %q", result.TxHash)
	}
	if !result.WithdrawRecord.Claimed {
		t.Fatalf("claimed record must be marked claimed=true")
	}

	args := strings.Join(runner.gotArgs, " ")
	if !strings.Contains(args, "tx zkdex claim-withdraw w-1") {
		t.Fatalf("expected claim-withdraw w-1 in args, got: %s", args)
	}
	// The claim MUST be signed by the destination (user), not the relayer key.
	if !strings.Contains(args, "--from cosmos1bob") {
		t.Fatalf("expected --from cosmos1bob (destination signer), got: %s", args)
	}
}

func TestCosmosClientClaimWithdrawRequiresDestination(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"txhash":"AA","code":0}`)}
	client := NewCosmosClient(CosmosConfig{From: "relayer"}, runner)

	if _, err := client.ClaimWithdraw(context.Background(), ClaimWithdrawInput{
		WithdrawRecord: types.WithdrawRecord{WithdrawID: "w-1"},
	}); err == nil {
		t.Fatalf("expected error when destination is empty")
	}
	if runner.calls != 0 {
		t.Fatalf("runner must not be invoked without a signer, calls=%d", runner.calls)
	}
}

func TestCosmosClientClaimWithdrawRejectsAlreadyClaimed(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"txhash":"AA","code":0}`)}
	client := NewCosmosClient(CosmosConfig{From: "relayer"}, runner)

	if _, err := client.ClaimWithdraw(context.Background(), ClaimWithdrawInput{
		WithdrawRecord: types.WithdrawRecord{
			WithdrawID:  "w-1",
			Destination: "cosmos1bob",
			Claimed:     true,
		},
	}); err == nil {
		t.Fatalf("expected error when withdraw already claimed")
	}
}

// scriptedRunner returns per-call canned outputs, distinguishing `tx ...`
// broadcasts from `query tx ...` confirmations so wait-for-commit and
// sequence-mismatch retry can be exercised deterministically.
type runnerResp struct {
	out []byte
	err error
}

type scriptedRunner struct {
	txResponses    []runnerResp
	queryResponses []runnerResp
	txCalls        int
	queryCalls     int
}

func (r *scriptedRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	isQuery := len(args) >= 2 && args[0] == "query" && args[1] == "tx"
	pick := func(resps []runnerResp, idx int) runnerResp {
		if idx < len(resps) {
			return resps[idx]
		}
		return resps[len(resps)-1] // repeat last once exhausted
	}
	if isQuery {
		resp := pick(r.queryResponses, r.queryCalls)
		r.queryCalls++
		return resp.out, resp.err
	}
	resp := pick(r.txResponses, r.txCalls)
	r.txCalls++
	return resp.out, resp.err
}

func TestCosmosClientSubmitBatchWaitsForCommit(t *testing.T) {
	runner := &scriptedRunner{
		txResponses: []runnerResp{
			{out: []byte(`{"txhash":"HASH1","code":0,"raw_log":""}`)}, // CheckTx passed (mempool)
		},
		queryResponses: []runnerResp{
			{err: errors.New("tx (HASH1) not found")},                  // still pending
			{out: []byte(`{"txhash":"HASH1","code":0,"raw_log":""}`)},  // now in a block
		},
	}

	client := NewCosmosClient(CosmosConfig{
		From:            "relayer",
		WaitForCommit:   true,
		ConfirmInterval: time.Millisecond,
	}, runner)

	result, err := client.SubmitBatch(context.Background(), sampleSubmitInput())
	if err != nil {
		t.Fatalf("submit batch: %v", err)
	}
	if !result.Accepted || result.TxHash != "HASH1" {
		t.Fatalf("expected accepted HASH1, got %+v", result)
	}
	if runner.queryCalls != 2 {
		t.Fatalf("expected 2 confirmation polls (pending then committed), got %d", runner.queryCalls)
	}
}

func TestCosmosClientSubmitBatchRetriesOnSequenceMismatch(t *testing.T) {
	runner := &scriptedRunner{
		txResponses: []runnerResp{
			// First broadcast fails with the observed account-sequence error.
			{out: []byte(`account sequence mismatch, expected 5, got 4: incorrect account sequence`), err: errors.New("exit status 1")},
			// Retry re-queries the advanced sequence and succeeds.
			{out: []byte(`{"txhash":"HASH2","code":0,"raw_log":""}`)},
		},
	}

	client := NewCosmosClient(CosmosConfig{
		From:          "relayer",
		WaitForCommit: false,
		SeqRetryDelay: time.Millisecond,
	}, runner)

	result, err := client.SubmitBatch(context.Background(), sampleSubmitInput())
	if err != nil {
		t.Fatalf("submit batch should recover from sequence mismatch: %v", err)
	}
	if !result.Accepted || result.TxHash != "HASH2" {
		t.Fatalf("expected accepted HASH2 after retry, got %+v", result)
	}
	if runner.txCalls != 2 {
		t.Fatalf("expected one retry (2 tx calls), got %d", runner.txCalls)
	}
}

func TestCosmosClientSubmitBatchRejectedInBlock(t *testing.T) {
	// CheckTx passes (code 0) but DeliverTx fails in the block (non-zero code).
	// Wait-for-commit must surface this as a rejection, not a false accept.
	runner := &scriptedRunner{
		txResponses:    []runnerResp{{out: []byte(`{"txhash":"H3","code":0}`)}},
		queryResponses: []runnerResp{{out: []byte(`{"txhash":"H3","code":11,"raw_log":"out of gas"}`)}},
	}

	client := NewCosmosClient(CosmosConfig{
		From:            "relayer",
		WaitForCommit:   true,
		ConfirmInterval: time.Millisecond,
	}, runner)

	result, err := client.SubmitBatch(context.Background(), sampleSubmitInput())
	if err == nil {
		t.Fatalf("expected rejection when the committed tx code is non-zero")
	}
	if result.Accepted || result.ProofStatus != "rejected" {
		t.Fatalf("expected accepted=false/rejected, got %+v", result)
	}
	if !strings.Contains(err.Error(), "out of gas") {
		t.Fatalf("expected block error reason, got %v", err)
	}
}

func TestCosmosConfigWithDefaults(t *testing.T) {
	cfg := CosmosConfig{}.withDefaults()
	if cfg.Binary != "obd" {
		t.Fatalf("expected default binary obd, got %q", cfg.Binary)
	}
	if cfg.KeyringBackend != "test" {
		t.Fatalf("expected default keyring test, got %q", cfg.KeyringBackend)
	}
	if cfg.Gas != "auto" {
		t.Fatalf("expected default gas auto, got %q", cfg.Gas)
	}
	if cfg.BroadcastMode != "sync" {
		t.Fatalf("expected default broadcast sync, got %q", cfg.BroadcastMode)
	}
}
