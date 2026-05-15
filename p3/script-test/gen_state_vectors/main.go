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

	fmt.Println("rootA:", initial.Root)
	fmt.Println("rootB:", newRoot)
	fmt.Println("withdrawRequest:", wdReq.WithdrawID, "nonce:", wdReq.Nonce)
	fmt.Println("wrote vectors into", outDir)
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
