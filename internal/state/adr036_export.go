package state

// Thin exported wrappers over the ADR-036 helpers, so tooling OUTSIDE this
// package (benchmark order signer, vector generators) can produce signatures
// that are byte-exact with what ADR036OrderSignatureVerifier expects.
//
// This file adds NO logic: both functions delegate verbatim to the unexported
// implementations in order_signature_adr036.go. It exists for the same reason
// p3/script-test/sign_order reuses state.MockOrderSignature — a signer that
// hand-rolls the sign-doc or the address derivation WILL silently drift from
// the verifier, and the resulting signatures would be rejected (or, worse,
// accepted for the wrong preimage).

// ADR036SignBytes returns the exact amino sign-doc bytes that a wallet's
// signArbitrary (ADR-036) signs for `canonical` on behalf of `owner`. The
// caller signs sha256(these bytes) with secp256k1.
func ADR036SignBytes(owner string, canonical []byte) []byte {
	return adr036SignBytes(owner, canonical)
}

// AccAddressFromPubKey derives the bech32 account address from a 33-byte
// compressed secp256k1 pubkey — the same derivation the verifier uses to bind
// a signature to order.Owner.
func AccAddressFromPubKey(compressed []byte) (string, error) {
	return accAddressFromPubKey(compressed)
}
