package state

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

type adr036Order struct {
	Owner, Market, Side, Price, Qty, Expiry, Nonce string
}

type adr036Case struct {
	Order           adr036Order `json:"order"`
	Canonical       string      `json:"canonical"`
	PubkeyBase64    string      `json:"pubkeyBase64"`
	SignatureBase64 string      `json:"signatureBase64"`
	SignBytesHex    string      `json:"signBytesHex"`
}

type adr036Vectors struct {
	Valid         adr036Case `json:"valid"`
	Impersonation adr036Case `json:"impersonation"`
}

func loadADR036Vectors(t *testing.T) adr036Vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/adr036_order_vector.json")
	if err != nil {
		t.Fatalf("read vector: %v", err)
	}
	var v adr036Vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse vector: %v", err)
	}
	return v
}

func (c adr036Case) order() types.SignedOrder {
	return types.SignedOrder{
		Owner:     c.Order.Owner,
		Market:    c.Order.Market,
		Side:      types.OrderSide(c.Order.Side),
		Price:     c.Order.Price,
		Qty:       c.Order.Qty,
		Expiry:    c.Order.Expiry,
		Nonce:     c.Order.Nonce,
		Signature: c.SignatureBase64,
		PubKey:    c.PubkeyBase64,
	}
}

// adr036SignBytes must be byte-identical to cosmjs/Keplr serializeSignDoc — this
// is the cross-tool byte-compat lock (Keplr ↔ cosmjs ↔ Go).
func TestADR036SignBytesMatchesKeplr(t *testing.T) {
	v := loadADR036Vectors(t)
	o := v.Valid.order()
	canonical, err := o.CanonicalBytes()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if string(canonical) != v.Valid.Canonical {
		t.Fatalf("canonical mismatch:\n got=%q\nwant=%q", string(canonical), v.Valid.Canonical)
	}
	got := hex.EncodeToString(adr036SignBytes(o.Owner, canonical))
	if got != v.Valid.SignBytesHex {
		t.Fatalf("adr036 signBytes mismatch (BE envelope != Keplr):\n got=%s\nwant=%s", got, v.Valid.SignBytesHex)
	}
}

func TestADR036VerifyAcceptsKeplrSignature(t *testing.T) {
	v := loadADR036Vectors(t)
	o := v.Valid.order()
	canonical, _ := o.CanonicalBytes()
	if err := (ADR036OrderSignatureVerifier{}).Verify(o, canonical); err != nil {
		t.Fatalf("verify valid signature: %v", err)
	}
}

// The crux: a VALID secp256k1 signature under key1, but the order claims owner =
// address of key2. Must reject via the pubkey→owner binding.
func TestADR036VerifyRejectsImpersonation(t *testing.T) {
	v := loadADR036Vectors(t)
	o := v.Impersonation.order()
	canonical, _ := o.CanonicalBytes()
	if err := (ADR036OrderSignatureVerifier{}).Verify(o, canonical); err == nil {
		t.Fatal("expected reject: signing pubkey does not derive the claimed owner")
	}
}

func TestADR036VerifyRejectsTamperedSignature(t *testing.T) {
	v := loadADR036Vectors(t)
	o := v.Valid.order()
	sig, _ := base64.StdEncoding.DecodeString(o.Signature)
	sig[63] ^= 0x01 // flip a bit in s
	o.Signature = base64.StdEncoding.EncodeToString(sig)
	canonical, _ := o.CanonicalBytes()
	if err := (ADR036OrderSignatureVerifier{}).Verify(o, canonical); err == nil {
		t.Fatal("expected reject on tampered signature")
	}
}

func TestADR036VerifyRejectsTamperedField(t *testing.T) {
	v := loadADR036Vectors(t)
	o := v.Valid.order()
	o.Price = "101" // changes canonical → signature no longer matches
	canonical, _ := o.CanonicalBytes()
	if err := (ADR036OrderSignatureVerifier{}).Verify(o, canonical); err == nil {
		t.Fatal("expected reject when a signed field is tampered")
	}
}

func TestNewOrderSignatureVerifierSelectsMode(t *testing.T) {
	if _, ok := NewOrderSignatureVerifier("").(MockOrderSignatureVerifier); !ok {
		t.Fatal(`mode "" should select mock`)
	}
	if _, ok := NewOrderSignatureVerifier("mock").(MockOrderSignatureVerifier); !ok {
		t.Fatal(`mode "mock" should select mock`)
	}
	if _, ok := NewOrderSignatureVerifier("adr36").(ADR036OrderSignatureVerifier); !ok {
		t.Fatal(`mode "adr36" should select ADR-036`)
	}
}
