// sign_order_adr036 prints a fully-signed SignedOrder as JSON for a backend
// running with ORDER_SIG_MODE=adr36 (the live default). It is the ADR-036
// sibling of p3/script-test/sign_order (which only covers ORDER_SIG_MODE=mock).
//
// A wallet is NOT required: ADR-036 verification is plain secp256k1 over a
// deterministic sign-doc, so a private key is all that is needed. Keplr is one
// producer of such signatures, not a precondition. This makes the whole
// benchmark suite scriptable end-to-end.
//
// It reuses the repo's own state.ADR036SignBytes / state.AccAddressFromPubKey /
// SignedOrder.CanonicalBytes so the bytes are exact by construction (no
// hand-rolled hashing — the same discipline as sign_order).
//
// The owner is DERIVED from the key: under adr36 the verifier rejects any order
// whose Owner is not the address of the signing pubkey, so an arbitrary owner
// string ("cosmos1alice") cannot work.
//
// Usage:
//
//	# key from the chain keyring (test backend):
//	PK=$(yes | obd keys export alice --unarmored-hex --unsafe --keyring-backend test | tail -1)
//	go run ./p3/script-test/sign_order_adr036 \
//	  -privkey-hex "$PK" -market ATOM/USDC -side buy -price 100 -qty 20 -nonce 1
//	# then: | curl -s -X POST localhost:8080/api/order -H 'content-type: application/json' --data @-
//
//	# print just the derived address (to fund it / cross-check):
//	go run ./p3/script-test/sign_order_adr036 -privkey-hex "$PK" -addr-only
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func main() {
	privHex := flag.String("privkey-hex", "", "secp256k1 private key, 32-byte hex (REQUIRED)")
	market := flag.String("market", "ATOM/USDC", "market id")
	side := flag.String("side", "buy", "buy | sell")
	price := flag.String("price", "", "limit price (decimal string)")
	qty := flag.String("qty", "", "base quantity (decimal string)")
	expiry := flag.String("expiry", "2000000", "expiry (block height or ts)")
	nonce := flag.String("nonce", "1", "per-account order nonce")
	wantOwner := flag.String("owner", "", "optional: assert the derived address equals this")
	addrOnly := flag.Bool("addr-only", false, "print the derived bech32 address and exit")
	flag.Parse()

	priv, pubCompressed, err := parseKey(*privHex)
	if err != nil {
		fatal("%v", err)
	}

	owner, err := state.AccAddressFromPubKey(pubCompressed)
	if err != nil {
		fatal("derive address: %v", err)
	}
	if *wantOwner != "" && strings.TrimSpace(*wantOwner) != owner {
		fatal("key derives %q but -owner says %q — the verifier would reject this order", owner, *wantOwner)
	}
	if *addrOnly {
		fmt.Println(owner)
		return
	}
	if *price == "" || *qty == "" {
		fatal("-price and -qty are required")
	}

	o := types.SignedOrder{
		Owner:  owner,
		Market: *market,
		Side:   types.OrderSide(*side),
		Price:  *price,
		Qty:    *qty,
		Expiry: *expiry,
		Nonce:  *nonce,
	}

	// Canonical bytes are the signed preimage (signature/pubkey are transport-only
	// and deliberately excluded from them).
	canonical, err := o.CanonicalBytes()
	if err != nil {
		fatal("canonical bytes: %v", err)
	}

	digest := sha256.Sum256(state.ADR036SignBytes(owner, canonical))

	// Sign, then emit compact r||s. ecdsa.SignCompact returns 65 bytes with a
	// leading recovery byte; the verifier wants the trailing 64. dcrd normalises
	// to low-S, which the verifier requires (malleability guard).
	compact := ecdsa.SignCompact(priv, digest[:], true)
	if len(compact) != 65 {
		fatal("unexpected compact signature length %d", len(compact))
	}
	o.Signature = base64.StdEncoding.EncodeToString(compact[1:])
	o.PubKey = base64.StdEncoding.EncodeToString(pubCompressed)

	out, err := json.Marshal(o)
	if err != nil {
		fatal("marshal: %v", err)
	}
	fmt.Println(string(out))
}

// parseKey accepts a 32-byte hex private key (with or without 0x) and returns
// the key plus its 33-byte compressed pubkey.
func parseKey(h string) (*secp256k1.PrivateKey, []byte, error) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(h), "0x"))
	if s == "" {
		return nil, nil, fmt.Errorf("-privkey-hex is required")
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, nil, fmt.Errorf("decode privkey hex: %w", err)
	}
	if len(raw) != 32 {
		return nil, nil, fmt.Errorf("privkey must be 32 bytes, got %d", len(raw))
	}
	priv := secp256k1.PrivKeyFromBytes(raw)
	return priv, priv.PubKey().SerializeCompressed(), nil
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "sign_order_adr036: "+f+"\n", a...)
	os.Exit(1)
}
