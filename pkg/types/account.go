package types

// Account is the off-chain balance record for one (owner, denom) pair.
//
// Balance is the AVAILABLE (spendable) amount. The field is named "balance"
// for backward compatibility with the pre-trading core: before STATE-T02 an
// account only had a single spendable balance, so every deposit/withdraw path
// and every pinned state root treats `balance` as the available amount.
//
// Reserved (STATE-T02) is collateral locked behind resting trade orders. It is
// bound into the state root so a proof cannot spend already-locked funds
// (Reserved balance Agreement: "root bind cả available+reserved"). It uses
// `omitempty` and is kept as "" (not "0") whenever it is zero, so a
// non-trading account serializes byte-identically to the legacy shape and its
// state root is unchanged. As soon as a reservation exists the field appears
// and the root reflects it.
//
// Invariant: available (Balance) + Reserved is conserved by Reserve/Release
// (locking moves funds between the two buckets); it decreases only when a fill
// Consumes reserved collateral (the funds leave the account to the
// counterparty, applied by the matching engine — STATE-T05/T06).
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
