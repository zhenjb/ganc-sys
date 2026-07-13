// sign_order prints a fully-signed SignedOrder as JSON, ready to POST to
// /api/order on the live backend. It reuses the repo's own
// state.MockOrderSignature so the signature is byte-exact with what
// MockOrderSignatureVerifier expects (no hand-rolled hashing).
//
// Usage:
//
//	go run ./p3/script-test/sign_order \
//	  -owner cosmos1... -market ATOM/USDC -side buy -price 10 -qty 5
//	# then: | curl -s -X POST localhost:8080/api/order -H 'content-type: application/json' --data @-
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func main() {
	owner := flag.String("owner", "", "order owner (bech32 address)")
	market := flag.String("market", "ATOM/USDC", "market id, e.g. ATOM/USDC")
	side := flag.String("side", "buy", "buy | sell")
	price := flag.String("price", "", "limit price (decimal string)")
	qty := flag.String("qty", "", "base quantity (decimal string)")
	expiry := flag.String("expiry", "2000000", "expiry (block height or ts)")
	nonce := flag.String("nonce", "1", "per-account order nonce")
	flag.Parse()

	if *owner == "" || *price == "" || *qty == "" {
		fmt.Fprintln(os.Stderr, "sign_order: -owner, -price and -qty are required")
		os.Exit(2)
	}

	o := types.SignedOrder{
		Owner:  *owner,
		Market: *market,
		Side:   types.OrderSide(*side),
		Price:  *price,
		Qty:    *qty,
		Expiry: *expiry,
		Nonce:  *nonce,
	}

	sig, err := state.MockOrderSignature(o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign_order: %v\n", err)
		os.Exit(1)
	}
	o.Signature = sig

	out, err := json.Marshal(o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign_order: marshal: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
