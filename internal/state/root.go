package state

import (
	"encoding/json"
	"strings"

	"github.com/zhenjb/ganc-sys/pkg/hash"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const stateDomainTag = "zkdex/state/v1"

// ComputeRoot is a deterministic, MVP-simplified state root.
//
// Encoding (locked for STATE-01..03 — P2 may replace with a circuit-friendly
// hash in ZK-02; the contract is "same input -> same root"):
//   sha256( "zkdex/state/v1" || canonicalJSON([accounts sorted by (owner,denom)]) )
//
// Output is hex-prefixed (`0x...`) per agreements.
func ComputeRoot(accounts []types.Account) string {
	canonical, _ := json.Marshal(accounts)
	var buf strings.Builder
	buf.Grow(len(stateDomainTag) + 1 + len(canonical))
	buf.WriteString(stateDomainTag)
	buf.WriteByte('|')
	buf.Write(canonical)
	return hash.SHA256HexString(buf.String())
}
