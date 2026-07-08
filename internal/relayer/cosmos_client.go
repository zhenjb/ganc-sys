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

// buildSubmitBatchProofFlags renders the three autocli flag values for
//
//	obd tx zkdex submit-batch-proof \
//	  --settlement-update <json> --batch-commitments <json> --proof-bundle <bytes>
//
// The JSON struct tags on types.SettlementUpdate / types.BatchCommitments match
// the x/zkdex proto json_names exactly, so the chain parses them without
// remapping. proofBundle is the raw bytes later written to a temp file and
// passed to the `binary` --proof-bundle flag. Kept pure so the on-chain
// contract can be asserted in unit tests.
func buildSubmitBatchProofFlags(input SubmitBatchInput) (settlementUpdate string, batchCommitments string, proofBundle []byte, err error) {
	if input.SettlementUpdate.BatchID == "" {
		return "", "", nil, fmt.Errorf("settlementUpdate.batchId is required")
	}
	if input.ProofBundle.Proof == "" {
		return "", "", nil, fmt.Errorf("proofBundle.proof is required")
	}
	if len(input.ProofBundle.PublicInputs) != 6 {
		return "", "", nil, fmt.Errorf("proofBundle.publicInputs must contain 6 values")
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

func (c *CosmosClient) SubmitBatch(ctx context.Context, input SubmitBatchInput) (SubmitBatchResult, error) {
	settlementUpdate, batchCommitments, proofBundle, err := buildSubmitBatchProofFlags(input)
	if err != nil {
		return SubmitBatchResult{}, err
	}

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

	withdrawRecords := make([]types.WithdrawRecord, 0, len(input.SettlementUpdate.Withdrawals))
	for _, w := range input.SettlementUpdate.Withdrawals {
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

	out, runErr := c.runner.Run(ctx, c.cfg.Binary, args...)
	tx, parseErr := parseTxOutput(out)
	if runErr != nil && tx.TxHash == "" {
		return ClaimWithdrawResult{}, fmt.Errorf("claim-withdraw failed: %w: %s", runErr, strings.TrimSpace(string(out)))
	}
	if parseErr != nil {
		return ClaimWithdrawResult{}, fmt.Errorf("claim-withdraw: %w", parseErr)
	}
	if tx.Code != 0 {
		return ClaimWithdrawResult{}, fmt.Errorf("claim-withdraw rejected by chain (code=%d): %s", tx.Code, tx.RawLog)
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
