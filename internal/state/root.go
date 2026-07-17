package state

import (
	"encoding/json"
	"math/big"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const stateDomainTag = "zkdex/state/v1"

// ComputeRoot is a deterministic, MVP-simplified state root.
//
// Encoding (locked for STATE-01..03 — P2 may replace with a circuit-friendly
// hash in ZK-02; the contract is "same input -> same root"):
//
//	sha256( "zkdex/state/v1" || canonicalJSON([settledView(accounts) sorted by (owner,denom)]) )
//
// Output is hex-prefixed (`0x...`) per agreements.
//
// INT-2SEQ / Phương án A: the root commits each account's SETTLED total
// (available + reserved) via settledView — it does NOT distinguish which part is
// locked behind a resting order. Reserving/releasing collateral moves funds
// between the two buckets without changing the total, so it leaves the root
// unchanged; only deposits, withdrawals and fills (which move the total) advance
// it. This keeps the off-chain settled root equal to the chain's current root
// even while orders are open — see settledView.
func ComputeRoot(accounts []types.Account) string {
	canonical, _ := json.Marshal(settledView(accounts))
	var buf strings.Builder
	buf.Grow(len(stateDomainTag) + 1 + len(canonical))
	buf.WriteString(stateDomainTag)
	buf.WriteByte('|')
	buf.Write(canonical)
	return hash.SHA256HexString(buf.String())
}

// settledView returns the settled projection of an account set used for the
// state root (INT-2SEQ / Phương án A). Reserved collateral is off-chain
// bookkeeping (an open order that never settles on-chain on its own), so the
// chain-committed root must not depend on it: each account's reserved amount is
// folded into its balance (total = available + reserved) and reserved is cleared
// to "". Because Reserve/Release conserve total, this makes the root invariant
// across order placement/cancellation, while deposit/withdraw/fill (which change
// the total) still advance it.
//
// An account with no reserved collateral (Reserved == "") is copied verbatim, so
// its canonical JSON — and therefore the root — is byte-identical to the
// pre-INT-2SEQ encoding. Core deposit/withdraw states, the interop vector and the
// canonical Alice vector are unaffected; only states with live reservations differ.
func settledView(accounts []types.Account) []types.Account {
	out := make([]types.Account, len(accounts))
	for i, acc := range accounts {
		out[i] = acc
		reserved := strings.TrimSpace(acc.Reserved)
		if reserved == "" {
			continue // no reservation → identical to the legacy encoding
		}
		bal, okB := new(big.Int).SetString(strings.TrimSpace(acc.Balance), 10)
		res, okR := new(big.Int).SetString(reserved, 10)
		if !okB || !okR {
			// A malformed amount is a corruption bug the balance primitives already
			// guard; keep the account verbatim rather than silently zeroing it, so
			// the root stays deterministic for the (bad) input instead of hiding it.
			continue
		}
		out[i].Balance = new(big.Int).Add(bal, res).String()
		out[i].Reserved = ""
	}
	return out
}
