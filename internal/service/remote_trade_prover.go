package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// RemoteTradeProver / RemoteTradeVerifierSubmitter are the Wave-2 trade seams
// (ZK-T10 integration): instead of the local 8-input stub, they call A's gazk
// prover over HTTP for a REAL Groth16 trade proof and verify it against the real
// vk (vkId gazk-trade-v1). They plug in via SetTradeSettlement without touching
// the settle orchestration (INT-T06).
//
// v0 root note (TRD-A1). gazk's trade circuit now BINDS the v0 (SHA-256) WIRE roots
// as its 8 public inputs — the SAME values this P4 pipeline computes and the chain
// re-derives — instead of the old field-native v1 (MiMC) roots. So the returned
// proofBundle.PublicInputs equal BuildPublicInputsWithTrades(upd, com) byte-exact, and
// the submitter re-binds P4's v0 roots against them (the exact consistency the chain
// verifier enforces) before the real crypto check. The prover passes the v0 state
// roots ([0]/[1]) + core sentinels ([2..5]) in the request so gazk binds P4's exact
// wire values; gazk re-derives [6]/[7] from orders[]/fills[] (byte-exact P3).
//
// The gazk trade circuit is the fixed canonical prototype (2 orders + 1 fill), so
// these seams handle the single-fill/two-order batch and return a clear error for
// anything else.

const defaultRemoteTradeTimeout = 120 * time.Second

// RemoteTradeProver calls gazk POST /prove {trade} and returns the real bundle.
type RemoteTradeProver struct {
	baseURL string
	client  *http.Client
}

// NewRemoteTradeProver builds a prover pointing at a gazk base URL (e.g.
// http://localhost:8090).
func NewRemoteTradeProver(baseURL string) *RemoteTradeProver {
	return &RemoteTradeProver{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: defaultRemoteTradeTimeout},
	}
}

var _ TradeProver = (*RemoteTradeProver)(nil)

func (p *RemoteTradeProver) ProveTrade(ctx context.Context, upd types.SettlementUpdate, com types.BatchCommitments, witness types.Witness, publicInputs []string) (types.ProofBundle, error) {
	tradeReq, err := buildGazkTradeRequest(witness, publicInputs)
	if err != nil {
		return types.ProofBundle{}, fmt.Errorf("remote trade prover: %w", err)
	}

	var resp gazkProveResponse
	if err := p.postJSON(ctx, "/prove", gazkProveRequest{Trade: tradeReq}, &resp); err != nil {
		return types.ProofBundle{}, fmt.Errorf("remote trade prover: %w", err)
	}
	if strings.TrimSpace(resp.ProofBundle.Proof) == "" {
		return types.ProofBundle{}, fmt.Errorf("remote trade prover: gazk returned empty proof")
	}

	return types.ProofBundle{
		Proof:             resp.ProofBundle.Proof,
		PublicInputs:      resp.ProofBundle.PublicInputs,
		VerificationKeyID: resp.ProofBundle.VerificationKeyID,
	}, nil
}

func (p *RemoteTradeProver) postJSON(ctx context.Context, path string, in, out any) error {
	return postGazkJSON(ctx, p.client, p.baseURL+path, in, out)
}

// RemoteTradeVerifierSubmitter verifies the real trade proof against gazk's vk
// (POST /verify-trade). It "submits" by confirming the proof cryptographically
// verifies — the same check B's on-chain verifier runs (ONCHAIN-T04). It moves no
// funds and touches no chain state.
type RemoteTradeVerifierSubmitter struct {
	baseURL string
	client  *http.Client
}

// NewRemoteTradeVerifierSubmitter builds a submitter pointing at a gazk base URL.
func NewRemoteTradeVerifierSubmitter(baseURL string) *RemoteTradeVerifierSubmitter {
	return &RemoteTradeVerifierSubmitter{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: defaultRemoteTradeTimeout},
	}
}

var _ TradeSubmitter = (*RemoteTradeVerifierSubmitter)(nil)

func (s *RemoteTradeVerifierSubmitter) SubmitTrade(ctx context.Context, upd types.SettlementUpdate, com types.BatchCommitments, proof types.ProofBundle) (string, bool, error) {
	if proof.Proof == "" || len(proof.PublicInputs) == 0 {
		return "", false, fmt.Errorf("remote trade submitter: proofBundle is required")
	}

	// TRD-A1 reconciliation: gazk now binds P4's v0 wire roots, so the proof's public
	// inputs MUST equal the batch's independently-rebuilt v0 vector (the exact check
	// the chain verifier runs). A mismatch means a v0/v1 or layout drift — fail closed.
	expected, berr := batch.BuildPublicInputsWithTrades(upd, com)
	if berr != nil {
		return "", false, fmt.Errorf("remote trade submitter: rebuild public inputs: %w", berr)
	}
	if len(proof.PublicInputs) != len(expected) {
		return "", false, fmt.Errorf("remote trade submitter: proof has %d public inputs, want %d", len(proof.PublicInputs), len(expected))
	}
	for i := range expected {
		if proof.PublicInputs[i] != expected[i] {
			return "", false, fmt.Errorf("remote trade submitter: public input %d mismatch (%q != v0 %q)", i, proof.PublicInputs[i], expected[i])
		}
	}

	req := gazkVerifyRequest{
		SettlementUpdate: gazkSettlementUpdate{
			BatchID:      upd.BatchID,
			OldStateRoot: proof.PublicInputs[0],
			NewStateRoot: proof.PublicInputs[1],
		},
		ProofBundle: gazkProofBundle{
			Proof:             proof.Proof,
			PublicInputs:      proof.PublicInputs,
			VerificationKeyID: proof.VerificationKeyID,
		},
		PublicInputs: proof.PublicInputs,
	}

	var resp gazkVerifyResponse
	if err := postGazkJSON(ctx, s.client, s.baseURL+"/verify-trade", req, &resp); err != nil {
		return "", false, fmt.Errorf("remote trade submitter: %w", err)
	}
	if !resp.Valid {
		return "", false, fmt.Errorf("remote trade submitter: gazk rejected proof: %s", resp.Error)
	}

	// Deterministic pseudo tx hash from the verified bundle (no chain here).
	txHash := localTradeHash("remote-trade-verified", map[string]any{
		"batchId":      upd.BatchID,
		"proof":        proof.Proof,
		"publicInputs": proof.PublicInputs,
	})
	return txHash, true, nil
}

// ---------------------------------------------------------------------------
// witness → gazk trade request mapping (canonical single-fill batch).
// ---------------------------------------------------------------------------

func buildGazkTradeRequest(witness types.Witness, publicInputs []string) (*gazkTradeProveRequest, error) {
	tw := witness.Trade
	if tw == nil {
		return nil, fmt.Errorf("trade witness is nil")
	}
	if len(publicInputs) < 8 {
		return nil, fmt.Errorf("expected 8 v0 public inputs, got %d", len(publicInputs))
	}
	if len(tw.Fills) != 1 {
		return nil, fmt.Errorf("gazk trade circuit is the canonical prototype (1 fill), got %d", len(tw.Fills))
	}
	if len(tw.Orders) != 2 {
		return nil, fmt.Errorf("gazk trade circuit is the canonical prototype (2 orders), got %d", len(tw.Orders))
	}

	fill := tw.Fills[0]

	// Identify buy/sell orders.
	var buyOrder, sellOrder *types.TradeWitnessOrder
	for i := range tw.Orders {
		o := &tw.Orders[i]
		switch o.Side {
		case types.SideBuy:
			buyOrder = o
		case types.SideSell:
			sellOrder = o
		}
	}
	if buyOrder == nil || sellOrder == nil {
		return nil, fmt.Errorf("expected one buy and one sell order")
	}

	buyerIsMaker := fill.MakerOrderHash == buyOrder.OrderHash

	// Quote denom = the denom the BUYER reserved (>0); base denom = the SELLER's.
	quoteDenom, buyerReservedQuote := reservedDenom(tw.Balances, fill.Buyer)
	baseDenom, sellerReservedBase := reservedDenom(tw.Balances, fill.Seller)
	if quoteDenom == "" || baseDenom == "" {
		return nil, fmt.Errorf("could not infer quote/base denom from reserved balances")
	}

	// Economics for the flat state cells (self-consistent v1 root; not P4's v0 root).
	notional, err := mulWholeDecimal(fill.Price, fill.Qty)
	if err != nil {
		return nil, fmt.Errorf("notional %s*%s: %w", fill.Price, fill.Qty, err)
	}
	makerFee, ok1 := new(big.Int).SetString(strings.TrimSpace(fill.MakerFee), 10)
	takerFee, ok2 := new(big.Int).SetString(strings.TrimSpace(fill.TakerFee), 10)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("bad fee (%q/%q)", fill.MakerFee, fill.TakerFee)
	}
	buyerFee, sellerFee := takerFee, makerFee
	if buyerIsMaker {
		buyerFee, sellerFee = makerFee, takerFee
	}
	buyerQuoteOut := new(big.Int).Add(notional, buyerFee)
	sellerQuoteIn := new(big.Int).Sub(notional, sellerFee)
	feeTotal := new(big.Int).Add(makerFee, takerFee)
	sellerOldQuote := availOf(tw.Balances, fill.Seller, quoteDenom)
	feeOldQuote := "0" // fee account starts at 0 in the canonical batch

	orders := make([]gazkTradeOrder, 0, 2)
	for i := range tw.Orders {
		o := tw.Orders[i]
		orders = append(orders, gazkTradeOrder{
			OrderHash: o.OrderHash, Owner: o.Owner, Side: string(o.Side),
			Price: o.Price, Qty: o.Qty, Remaining: o.Remaining, Filled: o.Filled,
			Sequence: uint64(i + 1), // deterministic; gazk v1 root is self-consistent
		})
	}

	req := &gazkTradeProveRequest{
		Orders: orders,
		Fills: []gazkTradeFill{{
			TradeID: fill.TradeID, Market: fill.Market,
			MakerOrderHash: fill.MakerOrderHash, TakerOrderHash: fill.TakerOrderHash,
			Price: fill.Price, Qty: fill.Qty, MakerFee: fill.MakerFee, TakerFee: fill.TakerFee,
			Buyer: fill.Buyer, Seller: fill.Seller,
		}},
		BidPrice: buyOrder.Price, AskPrice: sellOrder.Price, FillPrice: fill.Price,
		MakerIsBid: buyerIsMaker,
		Conservation: gazkTradeConservation{
			Price: fill.Price, Qty: fill.Qty, MakerFee: fill.MakerFee, TakerFee: fill.TakerFee,
			BuyerIsMaker:             buyerIsMaker,
			BuyerReservedQuoteBefore: buyerReservedQuote,
			SellerReservedBaseBefore: sellerReservedBase,
		},
		Cells: []gazkTradeCell{
			{Owner: fill.Buyer, Denom: quoteDenom, OldBalance: buyerReservedQuote, DeltaIn: "0", DeltaOut: buyerQuoteOut.String()},
			{Owner: fill.Seller, Denom: quoteDenom, OldBalance: sellerOldQuote, DeltaIn: sellerQuoteIn.String(), DeltaOut: "0"},
			{Owner: state.FeeAccountOwner, Denom: quoteDenom, OldBalance: feeOldQuote, DeltaIn: feeTotal.String(), DeltaOut: "0"},
		},
		// TRD-A1: bind P4's exact v0 wire roots. P4 is the authoritative root deriver
		// (it owns the leaf sort order — Sequence — which the witness does not carry),
		// so it supplies ALL 8 roots and gazk binds them verbatim. The proof then commits
		// the same 8 values the chain re-derives independently (byte-exact reconciliation).
		OldStateRoot:        publicInputs[0],
		NewStateRoot:        publicInputs[1],
		DepositsRoot:        publicInputs[2],
		WithdrawalsRoot:     publicInputs[3],
		NullifiersRoot:      publicInputs[4],
		WithdrawOutputsRoot: publicInputs[5],
		TradesRoot:          publicInputs[6],
		OrdersRoot:          publicInputs[7],
	}
	return req, nil
}

// reservedDenom returns the (denom, oldReserved) with the largest positive old
// reserved for owner — the collateral denom (quote for a buyer, base for a seller).
func reservedDenom(balances []types.TradeWitnessBalance, owner string) (string, string) {
	best, bestVal := "", big.NewInt(0)
	for _, b := range balances {
		if b.Owner != owner {
			continue
		}
		v, ok := new(big.Int).SetString(strings.TrimSpace(nonEmpty(b.OldReserved)), 10)
		if !ok {
			continue
		}
		if v.Sign() > 0 && v.Cmp(bestVal) > 0 {
			best, bestVal = b.Denom, v
		}
	}
	if best == "" {
		return "", ""
	}
	return best, bestVal.String()
}

func availOf(balances []types.TradeWitnessBalance, owner, denom string) string {
	for _, b := range balances {
		if b.Owner == owner && b.Denom == denom {
			return nonEmpty(b.OldAvailable)
		}
	}
	return "0"
}

func nonEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}

// mulWholeDecimal returns a*b as a whole-integer string. Supports one operand
// with a fractional part as long as the product is whole (settlement notional).
func mulWholeDecimal(a, b string) (*big.Int, error) {
	am, as, err := decimalParts(a)
	if err != nil {
		return nil, err
	}
	bm, bs, err := decimalParts(b)
	if err != nil {
		return nil, err
	}
	prod := new(big.Int).Mul(am, bm)
	scale := as + bs
	if scale == 0 {
		return prod, nil
	}
	div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	q, r := new(big.Int).QuoRem(prod, div, new(big.Int))
	if r.Sign() != 0 {
		return nil, fmt.Errorf("product not whole units")
	}
	return q, nil
}

func decimalParts(s string) (*big.Int, int, error) {
	s = strings.TrimSpace(s)
	intPart, fracPart := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, fracPart = s[:dot], s[dot+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	m, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return nil, 0, fmt.Errorf("invalid decimal %q", s)
	}
	return m, len(fracPart), nil
}

func postGazkJSON(ctx context.Context, client *http.Client, url string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var er gazkErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		return fmt.Errorf("POST %s status %d: %s", url, resp.StatusCode, er.Error)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// gazk HTTP JSON shapes (mirror gazk/contract; separate module → local structs).
// ---------------------------------------------------------------------------

type gazkProveRequest struct {
	Trade *gazkTradeProveRequest `json:"trade"`
}

type gazkTradeProveRequest struct {
	Orders              []gazkTradeOrder      `json:"orders"`
	Fills               []gazkTradeFill       `json:"fills"`
	BidPrice            string                `json:"bidPrice"`
	AskPrice            string                `json:"askPrice"`
	FillPrice           string                `json:"fillPrice"`
	MakerIsBid          bool                  `json:"makerIsBid"`
	Conservation        gazkTradeConservation `json:"conservation"`
	Cells               []gazkTradeCell       `json:"cells"`
	OldStateRoot        string                `json:"oldStateRoot,omitempty"`
	NewStateRoot        string                `json:"newStateRoot,omitempty"`
	DepositsRoot        string                `json:"depositsRoot,omitempty"`
	WithdrawalsRoot     string                `json:"withdrawalsRoot,omitempty"`
	NullifiersRoot      string                `json:"nullifiersRoot,omitempty"`
	WithdrawOutputsRoot string                `json:"withdrawOutputsRoot,omitempty"`
	TradesRoot          string                `json:"tradesRoot,omitempty"`
	OrdersRoot          string                `json:"ordersRoot,omitempty"`
}

type gazkTradeOrder struct {
	OrderHash string `json:"orderHash"`
	Owner     string `json:"owner"`
	Side      string `json:"side"`
	Price     string `json:"price"`
	Qty       string `json:"qty"`
	Remaining string `json:"remaining"`
	Filled    bool   `json:"filled"`
	Sequence  uint64 `json:"sequence"`
}

type gazkTradeFill struct {
	TradeID        string `json:"tradeId"`
	Market         string `json:"market"`
	MakerOrderHash string `json:"makerOrderHash"`
	TakerOrderHash string `json:"takerOrderHash"`
	Price          string `json:"price"`
	Qty            string `json:"qty"`
	MakerFee       string `json:"makerFee"`
	TakerFee       string `json:"takerFee"`
	Buyer          string `json:"buyer"`
	Seller         string `json:"seller"`
}

type gazkTradeConservation struct {
	Price                    string `json:"price"`
	Qty                      string `json:"qty"`
	MakerFee                 string `json:"makerFee"`
	TakerFee                 string `json:"takerFee"`
	BuyerIsMaker             bool   `json:"buyerIsMaker"`
	BuyerReservedQuoteBefore string `json:"buyerReservedQuoteBefore"`
	SellerReservedBaseBefore string `json:"sellerReservedBaseBefore"`
}

type gazkTradeCell struct {
	Owner      string `json:"owner"`
	Denom      string `json:"denom"`
	OldBalance string `json:"oldBalance"`
	DeltaIn    string `json:"deltaIn"`
	DeltaOut   string `json:"deltaOut"`
}

type gazkProveResponse struct {
	ProofBundle gazkProofBundle `json:"proofBundle"`
}

type gazkProofBundle struct {
	Proof             string   `json:"proof"`
	PublicInputs      []string `json:"publicInputs"`
	VerificationKeyID string   `json:"verificationKeyId"`
}

type gazkSettlementUpdate struct {
	BatchID      string `json:"batchId"`
	OldStateRoot string `json:"oldStateRoot"`
	NewStateRoot string `json:"newStateRoot"`
}

type gazkVerifyRequest struct {
	SettlementUpdate gazkSettlementUpdate `json:"settlementUpdate"`
	ProofBundle      gazkProofBundle      `json:"proofBundle"`
	PublicInputs     []string             `json:"publicInputs"`
}

type gazkVerifyResponse struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

type gazkErrorResponse struct {
	Error string `json:"error"`
}
