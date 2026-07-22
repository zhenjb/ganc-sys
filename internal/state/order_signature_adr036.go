package state

// Real order authentication — Cosmos ADR-036 arbitrary-message signing
// (secp256k1). This replaces the MVP MockOrderSignatureVerifier when
// ORDER_SIG_MODE=adr36. The wallet (Keplr `signArbitrary`) signs the order's
// CANONICAL bytes wrapped in the ADR-036 amino sign-doc; here we reconstruct the
// exact sign-doc, verify the secp256k1 signature, and — the crux — derive the
// bech32 address from the signing pubkey and assert it equals order.Owner.
//
// Byte-compatibility of adr036SignBytes with Keplr/cosmjs is locked by a
// cross-tool test vector (order_signature_adr036_test.go +
// testdata/adr036_order_vector.json), generated with cosmjs — the same library
// Keplr builds on.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcutil/bech32"
	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // ripemd160 is required for Cosmos address derivation.

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// AccountAddressPrefix is the bech32 human-readable prefix of the target chain's
// account addresses (Cosmos default).
const AccountAddressPrefix = "cosmos"

// ADR036OrderSignatureVerifier verifies a real Cosmos ADR-036 secp256k1 wallet
// signature over the order's canonical bytes and binds it to order.Owner.
type ADR036OrderSignatureVerifier struct{}

var _ OrderSignatureVerifier = ADR036OrderSignatureVerifier{}

func (ADR036OrderSignatureVerifier) Verify(order types.SignedOrder, canonical []byte) error {
	// 1. Decode transport fields (pubkey + signature live outside canonical).
	pubBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(order.PubKey))
	if err != nil || len(pubBytes) != 33 {
		return fmt.Errorf("%w: pubkey must be base64 33-byte compressed secp256k1", ErrOrderSignatureInvalid)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(order.Signature))
	if err != nil || len(sigBytes) != 64 {
		return fmt.Errorf("%w: signature must be base64 64-byte (r||s)", ErrOrderSignatureInvalid)
	}

	// 2. Parse the public key.
	pub, err := secp256k1.ParsePubKey(pubBytes)
	if err != nil {
		return fmt.Errorf("%w: parse pubkey: %v", ErrOrderSignatureInvalid, err)
	}

	// 3. Bind pubkey -> owner. THE authorization check: a valid signature only
	// proves *some* key signed; deriving the address from that key and asserting
	// it equals order.Owner is what proves the OWNER authorized this order.
	// Without this, an attacker signs a victim-owner order with their own key.
	derived, err := accAddressFromPubKey(pubBytes)
	if err != nil {
		return fmt.Errorf("%w: derive address: %v", ErrOrderSignatureInvalid, err)
	}
	if derived != strings.TrimSpace(order.Owner) {
		return fmt.Errorf("%w: pubkey derives %q, not owner %q", ErrOrderSignatureInvalid, derived, order.Owner)
	}

	// 4. Reconstruct ADR-036 sign bytes over the canonical order and verify,
	// rejecting high-S (malleability).
	signBytes := adr036SignBytes(order.Owner, canonical)
	digest := sha256.Sum256(signBytes)

	r, s, err := parseCompactSignature(sigBytes)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOrderSignatureInvalid, err)
	}
	if !ecdsa.NewSignature(r, s).Verify(digest[:], pub) {
		return fmt.Errorf("%w: signature does not verify against pubkey", ErrOrderSignatureInvalid)
	}
	return nil
}

// adr036SignBytes reconstructs, byte-for-byte, the amino sign-doc that Keplr's
// signArbitrary (ADR-036) signs: a sorted, compact JSON envelope whose single
// MsgSignData carries base64(canonical) and the signer address. encoding/json
// sorts map keys alphabetically — matching amino's sorted JSON. HTML escaping is
// disabled (cosmjs does not escape); the values here contain no <,>,& anyway.
func adr036SignBytes(owner string, canonical []byte) []byte {
	doc := map[string]any{
		"chain_id":       "",
		"account_number": "0",
		"sequence":       "0",
		"fee":            map[string]any{"gas": "0", "amount": []any{}},
		"memo":           "",
		"msgs": []any{
			map[string]any{
				"type": "sign/MsgSignData",
				"value": map[string]any{
					"signer": owner,
					"data":   base64.StdEncoding.EncodeToString(canonical),
				},
			},
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encode never fails for this map; the value is fully JSON-serializable.
	_ = enc.Encode(doc)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// accAddressFromPubKey derives the bech32 account address from a compressed
// secp256k1 pubkey: bech32(prefix, ripemd160(sha256(pubkey))).
func accAddressFromPubKey(compressed []byte) (string, error) {
	sha := sha256.Sum256(compressed)
	rmd := ripemd160.New()
	if _, err := rmd.Write(sha[:]); err != nil {
		return "", err
	}
	addr := rmd.Sum(nil) // 20 bytes
	conv, err := bech32.ConvertBits(addr, 8, 5, true)
	if err != nil {
		return "", err
	}
	return bech32.Encode(AccountAddressPrefix, conv)
}

// parseCompactSignature parses a 64-byte r||s signature, rejecting zero and
// non-low-S values (malleability guard, matching Cosmos).
func parseCompactSignature(sig []byte) (*secp256k1.ModNScalar, *secp256k1.ModNScalar, error) {
	var r, s secp256k1.ModNScalar
	if r.SetByteSlice(sig[:32]) {
		return nil, nil, fmt.Errorf("signature r >= curve order")
	}
	if s.SetByteSlice(sig[32:64]) {
		return nil, nil, fmt.Errorf("signature s >= curve order")
	}
	if r.IsZero() || s.IsZero() {
		return nil, nil, fmt.Errorf("signature r or s is zero")
	}
	if s.IsOverHalfOrder() {
		return nil, nil, fmt.Errorf("signature s is not low-S (malleable)")
	}
	return &r, &s, nil
}
