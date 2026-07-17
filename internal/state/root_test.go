package state

import (
	"encoding/json"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// INT-2SEQ / Phương án A: the root binds the settled TOTAL (available+reserved),
// so an account with locked collateral hashes identically to the same total held
// fully available. This is what keeps the off-chain root == chain current root
// while orders rest, fixing the two-sequencer oldStateRoot mismatch.
func TestComputeRootFoldsReservedIntoTotal(t *testing.T) {
	locked := []types.Account{{Owner: "alice", Denom: "uusdc", Balance: "60", Reserved: "40", Nonce: "0"}}
	free := []types.Account{{Owner: "alice", Denom: "uusdc", Balance: "100", Nonce: "0"}}
	if ComputeRoot(locked) != ComputeRoot(free) {
		t.Fatalf("reserved not folded into total:\n  locked root %s\n  free root   %s",
			ComputeRoot(locked), ComputeRoot(free))
	}
}

// Backward-compat: an account with no reservation must hash byte-identically to
// the pre-INT-2SEQ encoding (settledView copies it verbatim). This pins that core
// deposit/withdraw roots, the interop vector and the canonical Alice vector are
// unchanged — only states carrying a live reservation differ.
func TestComputeRootUnchangedWithoutReserved(t *testing.T) {
	accs := []types.Account{
		{Owner: "alice", Denom: "uusdc", Balance: "100", Nonce: "0"},
		{Owner: "bob", Denom: "uatom", Balance: "5000", Nonce: "0"},
	}
	// Legacy encoding: marshal the accounts verbatim, no settledView fold.
	canonical, _ := json.Marshal(accs)
	legacy := hash.SHA256HexString(stateDomainTag + "|" + string(canonical))
	if got := ComputeRoot(accs); got != legacy {
		t.Fatalf("reserved-free root diverged from legacy encoding:\n  got  %s\n  want %s", got, legacy)
	}
}

// A fill moves the settled total (Consume reduces reserved without crediting
// available), so the root MUST change — the counterpart to the fold invariance.
func TestComputeRootChangesWhenTotalChanges(t *testing.T) {
	before := []types.Account{{Owner: "alice", Denom: "uusdc", Balance: "60", Reserved: "40", Nonce: "0"}}
	afterFill := []types.Account{{Owner: "alice", Denom: "uusdc", Balance: "60", Reserved: "10", Nonce: "0"}} // consumed 30
	if ComputeRoot(before) == ComputeRoot(afterFill) {
		t.Fatal("root did not change when the settled total dropped after a fill")
	}
}
