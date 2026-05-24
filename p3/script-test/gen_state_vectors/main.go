// gen_state_vectors materializes the canonical Alice 100/40 vectors under
// testvectors/alice_100_40/ for P3 STATE-02/STATE-03/STATE-04.
//
// It is run from the repo root:
//
//	go run ./p3/script-test/gen_state_vectors
//
// The output files are checked in so other roles (P2 prover, P1 verifier,
// P4 backend) can consume them without re-running this program.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const (
	outDir    = "testvectors/alice_100_40"
	aliceAddr = "cosmos1alice"
	denom     = "uusdc"
)

type stateSnapshot struct {
	Root     string           `json:"root"`
	Accounts []types.Account  `json:"accounts"`
	Note     string           `json:"note,omitempty"`
}

func main() {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		die("mkdir: %v", err)
	}

	ls := state.NewLocalState()
	initial := stateSnapshot{
		Root:     ls.Root(),
		Accounts: ls.Snapshot(),
		Note:     "STATE-02 — initial local state, empty accounts. rootA.",
	}
	write("initial_state.json", initial)

	dep1 := types.DepositRecord{
		DepositID:     "dep-1",
		Owner:         aliceAddr,
		Denom:         denom,
		Amount:        "100",
		Processed:     false,
		CreatedHeight: 12345,
		TxHash:        canonicalTxHash("deposit", aliceAddr, denom, "100", "dep-1"),
	}
	write("deposit_dep_1.json", dep1)

	newRoot, err := ls.ApplyDeposit(dep1)
	if err != nil {
		die("apply deposit: %v", err)
	}
	after := stateSnapshot{
		Root:     newRoot,
		Accounts: ls.Snapshot(),
		Note:     "STATE-03 — after applying dep-1, Alice balance=100. rootB.",
	}
	write("state_after_deposit.json", after)

	// STATE-04 — build the canonical Alice withdraw request (40 uusdc).
	// Builder reads (but does not mutate) the post-deposit local state, so the
	// snapshot above (rootB, balance=100, nonce=0) is the input precondition.
	wb := state.NewWithdrawRequestBuilder(ls)
	wdReq, err := wb.Build(state.WithdrawIntent{
		Owner:       aliceAddr,
		Denom:       denom,
		Amount:      "40",
		Destination: aliceAddr,
	})
	if err != nil {
		die("build withdraw request: %v", err)
	}
	write("withdraw_request_wd_1.json", wdReq)

	// Re-snapshot to assert STATE-04 left the state unchanged.
	postBuildRoot := ls.Root()
	if postBuildRoot != newRoot {
		die("STATE-04 mutated root: rootB=%s, after-build=%s", newRoot, postBuildRoot)
	}

	// STATE-05 — apply the canonical Alice withdrawal (40 uusdc).
	// The nullifier here is a placeholder following the same composition
	// STATE-06 will use (`Hash(userSecret, nonce)`), but with a fixed test
	// secret. Once ZK-02 locks the hash scheme, regenerate this vector and
	// any downstream consumer (P2 witness, P1 verifier) re-reads it.
	nullifier := canonicalNullifier("alice_secret", wdReq.Nonce)
	rootC, err := ls.ApplyWithdrawal(wdReq, nullifier)
	if err != nil {
		die("apply withdrawal: %v", err)
	}
	afterWithdraw := stateSnapshot{
		Root:     rootC,
		Accounts: ls.Snapshot(),
		Note:     "STATE-05 — after applying wd-1, Alice balance=60, nonce=1. rootC. Nullifier is a placeholder until STATE-06/ZK-02 locks the hash scheme.",
	}
	write("state_after_withdrawal.json", afterWithdraw)

	// Sanity: post-condition required by the STATE-04 changenote — after
	// STATE-05, account.Nonce must equal request.Nonce.
	acc := ls.Account(aliceAddr, denom)
	if acc.Nonce != wdReq.Nonce {
		die("post-STATE-05 invariant violated: account.Nonce=%s, request.Nonce=%s", acc.Nonce, wdReq.Nonce)
	}
	if acc.Balance != "60" {
		die("post-STATE-05 balance: want 60, got %s", acc.Balance)
	}

	fmt.Println("rootA:", initial.Root)
	fmt.Println("rootB:", newRoot)
	fmt.Println("rootC:", rootC)
	fmt.Println("withdrawRequest:", wdReq.WithdrawID, "nonce:", wdReq.Nonce)
	fmt.Println("nullifier (placeholder):", nullifier)
	fmt.Println("wrote vectors into", outDir)
}

// canonicalNullifier mirrors the agreed nullifier composition
// (`Hash(userSecret, nonce)`) using SHA-256 as a placeholder. STATE-06
// will replace this with whatever circuit-friendly hash ZK-02 locks; the
// JSON shape ("0x"-prefixed hex) is what consumers depend on, not the
// digest value itself.
func canonicalNullifier(userSecret, nonce string) string {
	h := sha256.New()
	h.Write([]byte("zkdex/nullifier/v0|"))
	h.Write([]byte(userSecret))
	h.Write([]byte("|"))
	h.Write([]byte(nonce))
	return "0x" + strings.ToLower(hex.EncodeToString(h.Sum(nil)))
}

func write(name string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		die("marshal %s: %v", name, err)
	}
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		die("write %s: %v", path, err)
	}
}

// canonicalTxHash mirrors the recipe used by P4's chain.MockClient
// (`internal/chain/mock_client.go::mockTxHash`) so the static test vector
// matches what the mock would emit at runtime for the same input.
func canonicalTxHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte("|"))
	}
	return "0x" + strings.ToLower(hex.EncodeToString(h.Sum(nil)))[:32]
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen_state_vectors: "+format+"\n", args...)
	os.Exit(1)
}
