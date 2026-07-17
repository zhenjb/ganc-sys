package types

// Account is the off-chain balance record for one (owner, denom) pair.
//
// Balance is the AVAILABLE (spendable) amount. The field is named "balance"
// for backward compatibility with the pre-trading core: before STATE-T02 an
// account only had a single spendable balance, so every deposit/withdraw path
// and every pinned state root treats `balance` as the available amount.
//
// Reserved (STATE-T02) is collateral locked behind resting trade orders. It uses
// `omitempty` and is kept as "" (not "0") whenever it is zero, so a non-trading
// account serializes byte-identically to the legacy shape.
//
// INT-2SEQ / Phương án A — Reserved is NOT committed to the state root on its
// own. The root binds each account's SETTLED total (available + Reserved) via
// state.settledView, not the split between the two buckets. Rationale: a resting
// order is off-chain bookkeeping that never settles on-chain by itself, so making
// the root depend on it desynchronised the off-chain root from the chain's
// current root the moment any order was placed (the two-sequencer oldStateRoot
// mismatch). Folding Reserved into the total makes Reserve/Release root-invariant
// while deposits/withdrawals/fills — which move the total — still advance the
// root. (This reverses the earlier Agreement "root bind cả available+reserved".)
//
// Invariant: available (Balance) + Reserved is conserved by Reserve/Release
// (locking moves funds between the two buckets); it decreases only when a fill
// Consumes reserved collateral (the funds leave the account to the
// counterparty, applied by the matching engine — STATE-T05/T06). Because the
// root binds this total, Reserve/Release leave it unchanged and Consume moves it.
//
// All amounts are non-negative base-10 integer strings in the denom's smallest
// unit (never float — Encoding Agreement).
type Account struct {
	Owner    string `json:"owner"`
	Denom    string `json:"denom"`
	Balance  string `json:"balance"`            // available (spendable) amount
	Reserved string `json:"reserved,omitempty"` // locked collateral; "" when zero
	Nonce    string `json:"nonce"`
}
