package relayer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// CommandRunner abstracts process execution so the Cosmos relayer can be
// unit-tested without a live chain or the obd binary.
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
// already knows how to build, sign and broadcast the module messages.
type CosmosConfig struct {
	Binary         string // chain binary, e.g. "obd"
	ChainID        string // --chain-id
	Node           string // --node tcp://host:26657
	From           string // relayer key name/address used to sign MsgSubmitBatchProof
	KeyringBackend string // --keyring-backend (test|os|file)
	Home           string // --home (optional)
	Gas            string // --gas (default "auto")
	GasAdjustment  string // --gas-adjustment (default "1.5")
	GasPrices      string // --gas-prices (e.g. "0.025uusdc"); used if Fees empty
	Fees           string // --fees (e.g. "2000uusdc"); takes precedence over GasPrices
	BroadcastMode  string // --broadcast-mode (default "sync")

	// WaitForCommit, when true, makes SubmitBatch poll `query tx <hash>` after a
	// sync broadcast until the tx is included in a block, and reports Accepted
	// based on the committed (DeliverTx) code — not the CheckTx code. This is
	// REQUIRED when settling many batches back-to-back from a single signer: it
	// serializes submits to block cadence so the auto-derived account sequence
	// never collides ("account sequence mismatch"). It also upgrades Accepted
	// from "passed CheckTx" to "actually in a block and succeeded".
	WaitForCommit   bool
	ConfirmAttempts int           // max `query tx` polls (default 30)
	ConfirmInterval time.Duration // delay between polls (default 1s)

	// SeqRetryMax / SeqRetryDelay retry a broadcast that fails with "account
	// sequence mismatch" — a defense-in-depth net for any residual race (default
	// 5 attempts, 1s apart). On retry the CLI re-queries the now-advanced
	// sequence, so the resend succeeds.
	SeqRetryMax   int
	SeqRetryDelay time.Duration
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
	if c.ConfirmAttempts <= 0 {
		c.ConfirmAttempts = 30
	}
	if c.ConfirmInterval <= 0 {
		c.ConfirmInterval = 1 * time.Second
	}
	if c.SeqRetryMax <= 0 {
		c.SeqRetryMax = 5
	}
	if c.SeqRetryDelay <= 0 {
		c.SeqRetryDelay = 1 * time.Second
	}
	return c
}

// CosmosClient is a real relayer that submits MsgSubmitBatchProof and
// MsgClaimWithdraw to the on-chain x/zkdex module via the obd CLI.
//
// Money/state flow stays on-chain:
//   - SubmitBatchProof: relayer-signed; chain verifies + applies the settlement.
//   - ClaimWithdraw: must be signed by the withdrawal destination (the user),
//     so the destination address is used as --from (its key must exist in the
//     relayer keyring for dev/demo).
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

// chainProofBundle is the EXACT JSON the on-chain keeper unmarshals from the
// MsgSubmitBatchProof.proof_bundle bytes field (x/zkdex keeper
// agreementProofBundle). Only `proof` + `publicInputs` are read on-chain, and
// publicInputs must DeepEqual the 6 roots the chain derives from
// settlementUpdate + batchCommitments. We deliberately omit verificationKeyId
// (and any other ProofBundle field) the chain does not expect.
type chainProofBundle struct {
	Proof        string   `json:"proof"`
	PublicInputs []string `json:"publicInputs"`
}

// chainEmptyRootSentinel is the 32-byte all-zeros root the x/zkdex chain uses for
// the trade public inputs [6]=tradesRoot / [7]=ordersRoot of a batch with NO
// trades (its emptyPublicInputRootSentinel). The chain now derives a UNIFORM 8
// public inputs for EVERY batch (derivePublicInputs), so a core deposit/withdraw
// submit must carry [6]/[7] = this sentinel — NOT P3's SHA-256 empty-root — or the
// on-chain DeepEqual(bundle.publicInputs, derived8) rejects it.
const chainEmptyRootSentinel = "0x0000000000000000000000000000000000000000000000000000000000000000"

// normalizeCoreSubmitToEight adapts a core (deposit/withdraw, no-trade) submit to
// the chain's uniform 8-input contract: it pads/forces publicInputs[6]/[7] to the
// all-zeros sentinel and clears the trade roots in batchCommitments (the chain
// requires them empty-or-sentinel when no trades are present). Returns a copy;
// the caller's slice/struct are not mutated.
//
// NOTE: the gazk core proof is still a 6-input Groth16 proof; padding to 8 makes
// the SUBMIT bundle match the chain's uniform layout, which the chain's stub
// verifier accepts. When a real on-chain verifier lands, the gazk core circuit
// must also expose 8 public inputs (or the chain must select a 6-input core
// verifier) — tracked as a Wave-2 item.
func normalizeCoreSubmitToEight(input SubmitBatchInput) (SubmitBatchInput, error) {
	pis := input.ProofBundle.PublicInputs
	switch len(pis) {
	case 6:
		out := append(append([]string(nil), pis...), chainEmptyRootSentinel, chainEmptyRootSentinel)
		input.ProofBundle.PublicInputs = out
	case 8:
		out := append([]string(nil), pis...)
		out[6] = chainEmptyRootSentinel
		out[7] = chainEmptyRootSentinel
		input.ProofBundle.PublicInputs = out
	default:
		return SubmitBatchInput{}, fmt.Errorf("proofBundle.publicInputs must contain 6 or 8 values, got %d", len(pis))
	}
	// The chain rejects a no-trade batch whose tradesRoot/ordersRoot is neither
	// empty nor the sentinel, so clear P3's SHA-256 empty-roots here.
	input.BatchCommitments.TradesRoot = ""
	input.BatchCommitments.OrdersRoot = ""
	return input, nil
}

// buildSubmitBatchProofFlags renders the three autocli flag values for
//
//	obd tx zkdex submit-batch-proof \
//	  --settlement-update <json> --batch-commitments <json> --proof-bundle <bytes>
//
// The JSON struct tags on types.SettlementUpdate / types.BatchCommitments match
// the x/zkdex proto json_names exactly, so the chain parses them without
// remapping. proofBundle is the raw bytes later written to a temp file and
// passed to the `binary` --proof-bundle flag. Kept pure so the on-chain
// contract can be asserted in unit tests. Emits the uniform 8-input layout the
// chain now derives for every batch.
func buildSubmitBatchProofFlags(input SubmitBatchInput) (settlementUpdate string, batchCommitments string, proofBundle []byte, err error) {
	if input.SettlementUpdate.BatchID == "" {
		return "", "", nil, fmt.Errorf("settlementUpdate.batchId is required")
	}
	if input.ProofBundle.Proof == "" {
		return "", "", nil, fmt.Errorf("proofBundle.proof is required")
	}
	normalized, nerr := normalizeCoreSubmitToEight(input)
	if nerr != nil {
		return "", "", nil, nerr
	}

	return buildSubmitFlags(normalized, 8)
}

// buildTradeSubmitBatchProofFlags renders the same three flags for a TRADE batch
// (INT-T08): identical command and JSON shape, but the proofBundle carries the 8
// public inputs ([0..5] core + [6]=tradesRoot + [7]=ordersRoot) and the
// settlementUpdate carries trades[]. Kept pure for on-chain-contract unit tests.
func buildTradeSubmitBatchProofFlags(input SubmitBatchInput) (settlementUpdate string, batchCommitments string, proofBundle []byte, err error) {
	return buildSubmitFlags(input, 8)
}

// buildSubmitFlags is the shared flag builder for submit-batch-proof. wantInputs
// is 6 for the core circuit and 8 for the trade circuit — the ONLY difference
// between the two payloads (the obd command, JSON tags and proof-bundle shape are
// identical, so a batch mixing deposits/withdrawals/trades needs no new message).
func buildSubmitFlags(input SubmitBatchInput, wantInputs int) (settlementUpdate string, batchCommitments string, proofBundle []byte, err error) {
	if input.SettlementUpdate.BatchID == "" {
		return "", "", nil, fmt.Errorf("settlementUpdate.batchId is required")
	}
	if input.ProofBundle.Proof == "" {
		return "", "", nil, fmt.Errorf("proofBundle.proof is required")
	}
	if len(input.ProofBundle.PublicInputs) != wantInputs {
		return "", "", nil, fmt.Errorf("proofBundle.publicInputs must contain %d values", wantInputs)
	}

	su, err := json.Marshal(input.SettlementUpdate)
	if err != nil {
		return "", "", nil, fmt.Errorf("marshal settlementUpdate: %w", err)
	}
	bc, err := json.Marshal(input.BatchCommitments)
	if err != nil {
		return "", "", nil, fmt.Errorf("marshal batchCommitments: %w", err)
	}
	pb, err := json.Marshal(chainProofBundle{
		Proof:        input.ProofBundle.Proof,
		PublicInputs: input.ProofBundle.PublicInputs,
	})
	if err != nil {
		return "", "", nil, fmt.Errorf("marshal proofBundle: %w", err)
	}
	return string(su), string(bc), pb, nil
}

var _ TradeClient = (*CosmosClient)(nil)

func (c *CosmosClient) SubmitBatch(ctx context.Context, input SubmitBatchInput) (SubmitBatchResult, error) {
	settlementUpdate, batchCommitments, proofBundle, err := buildSubmitBatchProofFlags(input)
	if err != nil {
		return SubmitBatchResult{}, err
	}
	return c.submitProofFlags(ctx, settlementUpdate, batchCommitments, proofBundle, input.SettlementUpdate.Withdrawals)
}

// SubmitTradeBatch signs and broadcasts a trade batch through the same
// submit-batch-proof tx (INT-T08). trades[] rides inside settlementUpdate and the
// proofBundle carries the 8 public inputs; the chain verifies the proof and
// commits the new root (no bank transfer). A CheckTx/DeliverTx rejection is
// surfaced as (Accepted=false, error) so INT-T06 rolls back and re-enqueues.
func (c *CosmosClient) SubmitTradeBatch(ctx context.Context, input SubmitBatchInput) (SubmitBatchResult, error) {
	settlementUpdate, batchCommitments, proofBundle, err := buildTradeSubmitBatchProofFlags(input)
	if err != nil {
		return SubmitBatchResult{}, err
	}
	return c.submitProofFlags(ctx, settlementUpdate, batchCommitments, proofBundle, input.SettlementUpdate.Withdrawals)
}

// submitProofFlags writes the proof-bundle temp file, broadcasts the
// submit-batch-proof tx, optionally waits for block commit, and maps the result.
// Shared by SubmitBatch (6-input core) and SubmitTradeBatch (8-input trade) so
// signing / sequence-retry / commit-wait behave identically for both.
func (c *CosmosClient) submitProofFlags(ctx context.Context, settlementUpdate, batchCommitments string, proofBundle []byte, withdrawals []types.SettlementWithdrawal) (SubmitBatchResult, error) {
	// The on-chain `--proof-bundle` flag is a `binary` value: cosmos autocli
	// reads the bytes from a file path. Write the {proof, publicInputs} JSON to a
	// temp file and pass its path (the settlement/commitments go inline as JSON).
	file, err := os.CreateTemp("", "zkdex-proof-bundle-*.json")
	if err != nil {
		return SubmitBatchResult{}, fmt.Errorf("create temp proof-bundle file: %w", err)
	}
	defer os.Remove(file.Name())

	if _, err := file.Write(proofBundle); err != nil {
		file.Close()
		return SubmitBatchResult{}, fmt.Errorf("write temp proof-bundle file: %w", err)
	}
	if err := file.Close(); err != nil {
		return SubmitBatchResult{}, fmt.Errorf("close temp proof-bundle file: %w", err)
	}

	args := append(
		[]string{
			"tx", "zkdex", "submit-batch-proof",
			"--settlement-update", settlementUpdate,
			"--batch-commitments", batchCommitments,
			"--proof-bundle", file.Name(),
		},
		c.commonTxArgs(c.cfg.From)...,
	)

	tx, err := c.broadcast(ctx, args)
	if err != nil {
		return SubmitBatchResult{}, fmt.Errorf("submit-batch-proof failed: %w", err)
	}
	// CheckTx-level rejection (e.g. invalid proof caught in ante/handler).
	if tx.Code != 0 {
		return SubmitBatchResult{
			TxHash:      tx.TxHash,
			Accepted:    false,
			ProofStatus: "rejected",
		}, fmt.Errorf("submit-batch-proof rejected by chain (code=%d): %s", tx.Code, tx.RawLog)
	}

	// Wait for the tx to be included in a block so (a) the account sequence has
	// advanced before the next submit (no "account sequence mismatch" when
	// draining batches back-to-back) and (b) Accepted reflects the real
	// DeliverTx result, not just CheckTx.
	if c.cfg.WaitForCommit {
		committed, waitErr := c.waitForTxCommit(ctx, tx.TxHash)
		if waitErr != nil {
			return SubmitBatchResult{TxHash: tx.TxHash}, fmt.Errorf(
				"submit-batch-proof broadcast (tx=%s) but not confirmed in a block: %w", tx.TxHash, waitErr)
		}
		if committed.Code != 0 {
			return SubmitBatchResult{
				TxHash:      committed.TxHash,
				Accepted:    false,
				ProofStatus: "rejected",
			}, fmt.Errorf("submit-batch-proof rejected in block (code=%d): %s", committed.Code, committed.RawLog)
		}
		tx = committed
	}

	withdrawRecords := make([]types.WithdrawRecord, 0, len(withdrawals))
	for _, w := range withdrawals {
		withdrawRecords = append(withdrawRecords, types.WithdrawRecord{
			WithdrawID:  w.WithdrawID,
			Owner:       w.Owner,
			Denom:       w.Denom,
			Amount:      w.Amount,
			Destination: w.Destination,
			Nullifier:   w.Nullifier,
			Claimed:     false,
		})
	}

	return SubmitBatchResult{
		TxHash:          tx.TxHash,
		Accepted:        true,
		ProofStatus:     "accepted",
		WithdrawRecords: withdrawRecords,
	}, nil
}

func (c *CosmosClient) ClaimWithdraw(ctx context.Context, input ClaimWithdrawInput) (ClaimWithdrawResult, error) {
	if input.WithdrawRecord.WithdrawID == "" {
		return ClaimWithdrawResult{}, fmt.Errorf("withdrawRecord is required")
	}
	if input.WithdrawRecord.Claimed {
		return ClaimWithdrawResult{}, fmt.Errorf("withdraw already claimed")
	}

	// ClaimWithdraw must be signed by the destination (the user). Use the
	// destination address as --from; its key must be present in the keyring.
	signer := strings.TrimSpace(input.WithdrawRecord.Destination)
	if signer == "" {
		return ClaimWithdrawResult{}, fmt.Errorf("withdrawRecord.destination is required to sign claim")
	}

	args := append(
		[]string{"tx", "zkdex", "claim-withdraw", input.WithdrawRecord.WithdrawID},
		c.commonTxArgs(signer)...,
	)

	tx, err := c.broadcast(ctx, args)
	if err != nil {
		return ClaimWithdrawResult{}, fmt.Errorf("claim-withdraw failed: %w", err)
	}
	// CheckTx-level rejection (ante/handler).
	if tx.Code != 0 {
		return ClaimWithdrawResult{}, fmt.Errorf("claim-withdraw rejected by chain (code=%d): %s", tx.Code, tx.RawLog)
	}

	// Nhóm 4 (a): --broadcast-mode sync returns after CheckTx, BEFORE the module→
	// destination transfer runs in DeliverTx. Wait for the tx to be committed in a
	// block and check the IN-BLOCK code, so a claim that fails on-chain is never
	// reported as success. Mirrors SubmitBatchProof. (Was: only the CheckTx code was
	// checked — an immediate balance read after the response saw pre-transfer state,
	// which looked like "claimed but funds not moved".)
	if c.cfg.WaitForCommit {
		committed, waitErr := c.waitForTxCommit(ctx, tx.TxHash)
		if waitErr != nil {
			return ClaimWithdrawResult{TxHash: tx.TxHash}, fmt.Errorf(
				"claim-withdraw broadcast (tx=%s) but not confirmed in a block: %w", tx.TxHash, waitErr)
		}
		if committed.Code != 0 {
			return ClaimWithdrawResult{TxHash: committed.TxHash}, fmt.Errorf(
				"claim-withdraw rejected in block (code=%d): %s", committed.Code, committed.RawLog)
		}
		tx = committed
	}

	claimed := input.WithdrawRecord
	claimed.Claimed = true

	return ClaimWithdrawResult{
		TxHash:         tx.TxHash,
		WithdrawRecord: claimed,
	}, nil
}

// broadcast runs the tx CLI, retrying on an "account sequence mismatch" — a
// transient error when a previous tx has not yet committed and advanced the
// signer's on-chain sequence. On retry the CLI re-queries the (now advanced)
// sequence, so the resend succeeds. Non-sequence failures return immediately.
func (c *CosmosClient) broadcast(ctx context.Context, args []string) (txOutput, error) {
	attempts := c.cfg.SeqRetryMax
	if attempts < 1 {
		attempts = 1
	}

	var lastOut []byte
	for i := 0; i < attempts; i++ {
		out, runErr := c.runner.Run(ctx, c.cfg.Binary, args...)
		lastOut = out
		tx, parseErr := parseTxOutput(out)

		// Hard failure with no txhash (CLI exited non-zero, e.g. CheckTx reject).
		if runErr != nil && tx.TxHash == "" {
			if isSequenceMismatch(string(out)) && i < attempts-1 {
				if !sleepCtx(ctx, c.cfg.SeqRetryDelay) {
					return txOutput{}, ctx.Err()
				}
				continue
			}
			return txOutput{}, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(out)))
		}
		if parseErr != nil {
			return txOutput{}, parseErr
		}
		// A non-zero code carrying a sequence mismatch is also retryable.
		if tx.Code != 0 && isSequenceMismatch(tx.RawLog) && i < attempts-1 {
			if !sleepCtx(ctx, c.cfg.SeqRetryDelay) {
				return txOutput{}, ctx.Err()
			}
			continue
		}
		return tx, nil
	}
	return txOutput{}, fmt.Errorf("broadcast exhausted %d retries: %s", attempts, strings.TrimSpace(string(lastOut)))
}

// waitForTxCommit polls `query tx <hash>` until the tx is found in a block or the
// attempt budget is exhausted. A "not found" (tx still pending) surfaces as a
// CLI error; we retry until it lands.
func (c *CosmosClient) waitForTxCommit(ctx context.Context, txHash string) (txOutput, error) {
	attempts := c.cfg.ConfirmAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		out, err := c.runner.Run(ctx, c.cfg.Binary, c.queryTxArgs(txHash)...)
		if err == nil {
			if tx, parseErr := parseTxOutput(out); parseErr == nil {
				return tx, nil // committed (code may be 0 or non-zero)
			} else {
				lastErr = parseErr
			}
		} else {
			lastErr = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		if i < attempts-1 && !sleepCtx(ctx, c.cfg.ConfirmInterval) {
			return txOutput{}, ctx.Err()
		}
	}
	return txOutput{}, fmt.Errorf("tx %s not committed after %d polls: %v", txHash, attempts, lastErr)
}

// queryTxArgs builds `obd query tx <hash> --output json [--node ..] [--home ..]`.
func (c *CosmosClient) queryTxArgs(txHash string) []string {
	args := []string{"query", "tx", txHash, "--output", "json"}
	if strings.TrimSpace(c.cfg.Node) != "" {
		args = append(args, "--node", c.cfg.Node)
	}
	if strings.TrimSpace(c.cfg.Home) != "" {
		args = append(args, "--home", c.cfg.Home)
	}
	return args
}

func isSequenceMismatch(s string) bool {
	return strings.Contains(s, "account sequence mismatch")
}

// sleepCtx sleeps for d, returning false if the context is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
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

// txOutput is the subset of `obd tx ... --output json` we rely on.
type txOutput struct {
	TxHash string `json:"txhash"`
	Code   int    `json:"code"`
	RawLog string `json:"raw_log"`
}

// parseTxOutput extracts the broadcast result. The obd CLI may print warnings
// (e.g. gas estimate) before the JSON body, so we scan for the JSON object.
func parseTxOutput(out []byte) (txOutput, error) {
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return txOutput{}, fmt.Errorf("empty CLI output")
	}

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < 0 || end < start {
		return txOutput{}, fmt.Errorf("no JSON object in CLI output: %s", raw)
	}

	var tx txOutput
	if err := json.Unmarshal([]byte(raw[start:end+1]), &tx); err != nil {
		return txOutput{}, fmt.Errorf("decode CLI output: %w: %s", err, raw)
	}
	if tx.TxHash == "" {
		return txOutput{}, fmt.Errorf("CLI output missing txhash: %s", raw)
	}
	return tx, nil
}
