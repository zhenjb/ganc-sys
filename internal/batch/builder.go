package batch

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// ErrInvalidSettlementInputs is the sentinel returned whenever the
// fields of SettlementInputs are missing, malformed, or mutually
// inconsistent. Callers chain with errors.Is.
//
// We intentionally use a single sentinel for the whole builder so P4
// (which exposes Build over /api/batch/build) can map any failure to
// HTTP 400 with the underlying message as the body, instead of having
// to enumerate ten different error types per validation rule.
var ErrInvalidSettlementInputs = errors.New("batch: invalid settlement inputs")

// SettlementInputs is the read-only bundle the off-chain caller hands
// to the builder. Every field is something STATE-01..07 has already
// produced for the canonical Alice 100/40 vector:
//
//   - OldStateRoot:        LocalState.Root() snapshot BEFORE ApplyWithdrawal
//                          (e.g. rootB — the post-deposit / pre-withdraw root).
//   - NewStateRoot:        LocalState.Root() snapshot AFTER  ApplyWithdrawal
//                          (e.g. rootC — what the on-chain currentStateRoot
//                          will advance to once MsgSubmitBatchProof is
//                          accepted).
//   - Deposit:             the on-chain DepositRecord (STATE-03 source).
//                          Owner/Denom/Amount must match what STATE-03 used
//                          to credit LocalState.
//   - Withdraw:            the WithdrawRequest produced by STATE-04
//                          (WithdrawRequestBuilder).
//   - Nullifier:           value returned by state.NullifierFor in STATE-06.
//                          Caller already passed this to ApplyWithdrawal so
//                          re-using it here is byte-identical.
//   - WithdrawAddressHash: value returned by state.WithdrawAddressHash in
//                          STATE-07. The builder re-derives it from
//                          Withdraw.Destination and rejects if mismatched —
//                          defense-in-depth, see Build() doc.
//
// The struct is by-value because the builder must not mutate anything
// the caller still owns; the resulting SettlementUpdate is the only
// shared artifact.
type SettlementInputs struct {
	OldStateRoot        string
	NewStateRoot        string
	Deposit             types.DepositRecord
	Withdraw            types.WithdrawRequest
	Nullifier           string
	WithdrawAddressHash string
}

// SettlementUpdateBuilder produces deterministic, sequentially-numbered
// SettlementUpdate records.
//
// This is STATE-08 of the P3 pipeline. The builder is the only place
// that assigns BatchID; everything else (roots, ids, amounts, hashes,
// address) is passed in by the caller after STATE-03..07 has produced
// it. Keeping batch-id sequencing here means the on-chain side and the
// off-chain side share a single source of monotonicity per process.
//
// Concurrency: Build is goroutine-safe; the only mutable state is the
// seq counter, guarded by mu. The builder holds no reference to
// LocalState so multiple LocalState mirrors (e.g. one per chain in a
// multi-chain future) can share a builder if desired — though current
// MVP runs a single mirror.
type SettlementUpdateBuilder struct {
	mu  sync.Mutex
	seq uint64
}

func NewSettlementUpdateBuilder() *SettlementUpdateBuilder {
	return &SettlementUpdateBuilder{}
}

// Seq returns the number of SettlementUpdate records built so far
// (testing/debug; not part of the STATE-08 contract).
func (b *SettlementUpdateBuilder) Seq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Build validates SettlementInputs and assembles a canonical
// SettlementUpdate.
//
// Validation pipeline (any failure returns ErrInvalidSettlementInputs
// wrapped with a human-readable cause; no partial mutation of state):
//
//  1. Roots are non-empty, hex-prefixed (0x...), and strictly different.
//     A no-op batch (oldRoot == newRoot) would still consume a batchId
//     and a deposit/nullifier; reject early.
//  2. DepositRecord identity fields (depositId, owner, denom) non-empty
//     and Amount is a positive integer string.
//  3. WithdrawRequest identity fields (withdrawId, owner, denom,
//     destination) non-empty and Amount is a positive integer string,
//     Nonce is a non-negative integer string.
//  4. Nullifier is non-empty and hex-prefixed.
//  5. WithdrawAddressHash is non-empty and hex-prefixed.
//  6. Deposit.Denom == Withdraw.Denom. MVP batches one deposit + one
//     withdraw of the same denom; the circuit (ZK-04) is written for
//     that shape. Mixed-denom batches are future work.
//  7. Re-derive WithdrawAddressHash from Withdraw.Destination via
//     state.WithdrawAddressHash and assert equality with the supplied
//     hash. This catches the exact tampering scenario the STATE-07
//     changenote calls out: if the off-chain pipeline drifts
//     destination and hash, the prover would build a proof that is
//     valid against a different address than the chain expects.
//     Failing here saves a prover round-trip.
//
// Post-conditions on success:
//   - Returned SettlementUpdate has BatchID = "batch-N" where N is the
//     post-increment seq counter (first batch is "batch-1").
//   - All amount fields are normalized through big.Int (so "01" and
//     "1" produce identical bytes downstream — same canonicalization
//     STATE-06 NullifierFor applies to nonce).
//   - Owner/denom/destination preserved verbatim from the input
//     records (those were already trimmed by STATE-03/STATE-04
//     respectively; trimming again here would be a silent rewrite).
//
// The function is the sole place BatchID is minted; no other path in
// the P3 codebase should assign batch ids.
func (b *SettlementUpdateBuilder) Build(in SettlementInputs) (types.SettlementUpdate, error) {
	if err := validateRoot(in.OldStateRoot, "oldStateRoot"); err != nil {
		return types.SettlementUpdate{}, err
	}
	if err := validateRoot(in.NewStateRoot, "newStateRoot"); err != nil {
		return types.SettlementUpdate{}, err
	}
	if in.OldStateRoot == in.NewStateRoot {
		return types.SettlementUpdate{}, fmt.Errorf("%w: oldStateRoot == newStateRoot (no-op batch)", ErrInvalidSettlementInputs)
	}

	depAmt, err := validateDeposit(in.Deposit)
	if err != nil {
		return types.SettlementUpdate{}, err
	}
	wdAmt, err := validateWithdraw(in.Withdraw)
	if err != nil {
		return types.SettlementUpdate{}, err
	}

	if err := validateHex(in.Nullifier, "nullifier"); err != nil {
		return types.SettlementUpdate{}, err
	}
	if err := validateHex(in.WithdrawAddressHash, "withdrawAddressHash"); err != nil {
		return types.SettlementUpdate{}, err
	}

	if in.Deposit.Denom != in.Withdraw.Denom {
		return types.SettlementUpdate{}, fmt.Errorf(
			"%w: deposit.denom=%q != withdraw.denom=%q (mixed-denom batches not supported in MVP)",
			ErrInvalidSettlementInputs, in.Deposit.Denom, in.Withdraw.Denom,
		)
	}

	rederived, err := state.WithdrawAddressHash(in.Withdraw.Destination)
	if err != nil {
		return types.SettlementUpdate{}, fmt.Errorf(
			"%w: cannot re-derive withdrawAddressHash from destination %q: %v",
			ErrInvalidSettlementInputs, in.Withdraw.Destination, err,
		)
	}
	if rederived != in.WithdrawAddressHash {
		return types.SettlementUpdate{}, fmt.Errorf(
			"%w: withdrawAddressHash mismatch: supplied=%s, re-derived(destination=%q)=%s",
			ErrInvalidSettlementInputs, in.WithdrawAddressHash, in.Withdraw.Destination, rederived,
		)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++

	return types.SettlementUpdate{
		BatchID:             "batch-" + strconv.FormatUint(b.seq, 10),
		OldStateRoot:        in.OldStateRoot,
		NewStateRoot:        in.NewStateRoot,
		DepositID:           in.Deposit.DepositID,
		DepositAmount:       depAmt.String(),
		WithdrawID:          in.Withdraw.WithdrawID,
		WithdrawAmount:      wdAmt.String(),
		WithdrawAddress:     in.Withdraw.Destination,
		WithdrawAddressHash: in.WithdrawAddressHash,
		Nullifier:           in.Nullifier,
	}, nil
}

func validateRoot(root, label string) error {
	r := strings.TrimSpace(root)
	if r == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidSettlementInputs, label)
	}
	if !hash.IsHexPrefixed(r) {
		return fmt.Errorf("%w: %s %q missing 0x prefix", ErrInvalidSettlementInputs, label, r)
	}
	if len(hash.StripHex(r)) == 0 {
		return fmt.Errorf("%w: %s %q is empty after stripping 0x prefix", ErrInvalidSettlementInputs, label, r)
	}
	return nil
}

func validateHex(value, label string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidSettlementInputs, label)
	}
	if !hash.IsHexPrefixed(v) {
		return fmt.Errorf("%w: %s %q missing 0x prefix", ErrInvalidSettlementInputs, label, v)
	}
	if len(hash.StripHex(v)) == 0 {
		return fmt.Errorf("%w: %s %q is empty after stripping 0x prefix", ErrInvalidSettlementInputs, label, v)
	}
	return nil
}

func validateDeposit(d types.DepositRecord) (*big.Int, error) {
	if strings.TrimSpace(d.DepositID) == "" {
		return nil, fmt.Errorf("%w: deposit.depositId is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(d.Owner) == "" {
		return nil, fmt.Errorf("%w: deposit.owner is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(d.Denom) == "" {
		return nil, fmt.Errorf("%w: deposit.denom is empty", ErrInvalidSettlementInputs)
	}
	amt, err := parsePositive(d.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: deposit.amount %q invalid: %v", ErrInvalidSettlementInputs, d.Amount, err)
	}
	return amt, nil
}

func validateWithdraw(w types.WithdrawRequest) (*big.Int, error) {
	if strings.TrimSpace(w.WithdrawID) == "" {
		return nil, fmt.Errorf("%w: withdraw.withdrawId is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(w.Owner) == "" {
		return nil, fmt.Errorf("%w: withdraw.owner is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(w.Denom) == "" {
		return nil, fmt.Errorf("%w: withdraw.denom is empty", ErrInvalidSettlementInputs)
	}
	if strings.TrimSpace(w.Destination) == "" {
		return nil, fmt.Errorf("%w: withdraw.destination is empty", ErrInvalidSettlementInputs)
	}
	amt, err := parsePositive(w.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: withdraw.amount %q invalid: %v", ErrInvalidSettlementInputs, w.Amount, err)
	}
	if _, err := parseNonNegative(w.Nonce); err != nil {
		return nil, fmt.Errorf("%w: withdraw.nonce %q invalid: %v", ErrInvalidSettlementInputs, w.Nonce, err)
	}
	return amt, nil
}

func parsePositive(amount string) (*big.Int, error) {
	v, err := parseNonNegative(amount)
	if err != nil {
		return nil, err
	}
	if v.Sign() == 0 {
		return nil, errors.New("must be > 0")
	}
	return v, nil
}

func parseNonNegative(amount string) (*big.Int, error) {
	s := strings.TrimSpace(amount)
	if s == "" {
		return nil, errors.New("empty")
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, errors.New("not a base-10 integer")
	}
	if v.Sign() < 0 {
		return nil, errors.New("negative")
	}
	return v, nil
}
