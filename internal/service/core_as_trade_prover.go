package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/prover"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// TRD-UNIFY — retire the placeholder core circuit (gazk-balance-smoke-v1) and prove
// EVERY batch (deposit/withdraw AND trade) with the ONE real unified circuit
// gazk-trade-v1.
//
// A core (no-trade) batch is a trade batch with a ZERO fill. The unified circuit's
// per-cell transition constraint (NewBalance == OldBalance + DeltaIn − DeltaOut,
// range + non-negative) already IS the balance transition the smoke circuit stood
// in for; its price-crossing and conservation constraints are trivially satisfied
// by a zero fill (all comparisons become 0≤0 / 0==0). The 8 roots are bound as
// opaque v0 wire values (TRD-A1), with tradesRoot/ordersRoot set to the empty
// sentinel for a no-trade batch — exactly what the chain forces in
// derivePublicInputs and what the relayer's normalizeCoreSubmitToEight forces on
// submit, so the proof's public inputs match byte-exact end-to-end.
//
// CoreAsTradeProver implements prover.Client / prover.Verifier /
// prover.VerifierArtifactProvider so it drops into the SAME core proof path
// (ProofService, batch-submit verify, startup preflight) with NO orchestration
// change — only the proof it returns is now a real 8-input gazk-trade-v1 proof
// instead of a 6-input smoke proof. On-chain nothing changes: the chain already
// verifies gazk-trade-v1 for every batch.

const (
	// unifiedTradeVKID is the single on-chain vkId every batch now uses.
	unifiedTradeVKID = "gazk-trade-v1"

	// coreEmptyTradeRootSentinel MUST equal the chain's emptyPublicInputRootSentinel
	// and internal/relayer.chainEmptyRootSentinel: the chain forces publicInputs[6]/[7]
	// to this for a no-trade batch, so the core-as-trade proof MUST bind exactly this
	// value or on-chain verification fails.
	coreEmptyTradeRootSentinel = "0x0000000000000000000000000000000000000000000000000000000000000000"

	// maxCoreCells mirrors gazk's maxStateCells: the unified circuit binds at most
	// this many (owner,denom) state cells per batch.
	maxCoreCells = 4

	coreAsTradeTimeout = 120 * time.Second
)

// CoreAsTradeProver proves core (deposit/withdraw) batches with the unified
// gazk-trade-v1 circuit by shaping them as a zero-fill trade batch.
type CoreAsTradeProver struct {
	baseURL string
	client  *http.Client
}

// NewCoreAsTradeProver builds a prover pointing at a gazk base URL (e.g.
// http://localhost:8090).
func NewCoreAsTradeProver(baseURL string) *CoreAsTradeProver {
	return &CoreAsTradeProver{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  &http.Client{Timeout: coreAsTradeTimeout},
	}
}

var (
	_ prover.Client                   = (*CoreAsTradeProver)(nil)
	_ prover.Verifier                 = (*CoreAsTradeProver)(nil)
	_ prover.VerifierArtifactProvider = (*CoreAsTradeProver)(nil)
)

// GenerateProof shapes the core batch as a zero-fill trade batch and returns gazk's
// real gazk-trade-v1 proof (8 public inputs).
func (p *CoreAsTradeProver) GenerateProof(ctx context.Context, input prover.GenerateProofInput) (types.ProofBundle, error) {
	req, publicInputs, err := buildCoreAsTradeRequest(input)
	if err != nil {
		return types.ProofBundle{}, fmt.Errorf("core-as-trade prover: %w", err)
	}

	var resp gazkProveResponse
	if err := postGazkJSON(ctx, p.client, p.baseURL+"/prove", gazkProveRequest{Trade: req}, &resp); err != nil {
		return types.ProofBundle{}, fmt.Errorf("core-as-trade prover: %w", err)
	}
	if strings.TrimSpace(resp.ProofBundle.Proof) == "" {
		return types.ProofBundle{}, fmt.Errorf("core-as-trade prover: gazk returned empty proof")
	}
	if got := resp.ProofBundle.VerificationKeyID; got != unifiedTradeVKID {
		return types.ProofBundle{}, fmt.Errorf("core-as-trade prover: gazk returned vkId %q, want %q (wrong circuit?)", got, unifiedTradeVKID)
	}
	if len(resp.ProofBundle.PublicInputs) != batch.PublicInputCountWithTrades {
		return types.ProofBundle{}, fmt.Errorf("core-as-trade prover: gazk returned %d public inputs, want %d",
			len(resp.ProofBundle.PublicInputs), batch.PublicInputCountWithTrades)
	}
	// Fail closed: the proof must commit to the EXACT 8 inputs we derived, so the
	// chain's independently-derived vector matches byte-exact (no v0/v1 drift).
	for i := range publicInputs {
		if resp.ProofBundle.PublicInputs[i] != publicInputs[i] {
			return types.ProofBundle{}, fmt.Errorf("core-as-trade prover: gazk public input %d drift (%q != %q)",
				i, resp.ProofBundle.PublicInputs[i], publicInputs[i])
		}
	}

	return types.ProofBundle{
		Proof:             resp.ProofBundle.Proof,
		PublicInputs:      resp.ProofBundle.PublicInputs,
		VerificationKeyID: resp.ProofBundle.VerificationKeyID,
	}, nil
}

// Verify checks the (trade-shaped) core proof against gazk's real vk via
// /verify-trade — the SAME crypto check the chain's verifier runs.
func (p *CoreAsTradeProver) Verify(ctx context.Context, input prover.VerifyProofInput) error {
	pis := input.ProofBundle.PublicInputs
	if len(pis) != batch.PublicInputCountWithTrades {
		return fmt.Errorf("core-as-trade verify: proof has %d public inputs, want %d", len(pis), batch.PublicInputCountWithTrades)
	}
	req := gazkVerifyRequest{
		SettlementUpdate: gazkSettlementUpdate{
			BatchID:      input.SettlementUpdate.BatchID,
			OldStateRoot: pis[0],
			NewStateRoot: pis[1],
		},
		ProofBundle: gazkProofBundle{
			Proof:             input.ProofBundle.Proof,
			PublicInputs:      pis,
			VerificationKeyID: input.ProofBundle.VerificationKeyID,
		},
		PublicInputs: pis,
	}
	var resp gazkVerifyResponse
	if err := postGazkJSON(ctx, p.client, p.baseURL+"/verify-trade", req, &resp); err != nil {
		return fmt.Errorf("core-as-trade verify: %w", err)
	}
	if !resp.Valid {
		return fmt.Errorf("core-as-trade verify: gazk rejected proof: %s", resp.Error)
	}
	return nil
}

// GetVerifierArtifact fetches gazk's TRADE verifier artifact (vkId gazk-trade-v1),
// so the startup preflight + per-proof artifact check pin the unified circuit.
func (p *CoreAsTradeProver) GetVerifierArtifact(ctx context.Context) (types.VerifierArtifact, error) {
	var artifact types.VerifierArtifact
	if err := getGazkJSON(ctx, p.client, p.baseURL+"/trade-verifier-artifact", &artifact); err != nil {
		return types.VerifierArtifact{}, fmt.Errorf("core-as-trade artifact: %w", err)
	}
	if err := prover.ValidateVerifierArtifact(artifact); err != nil {
		return types.VerifierArtifact{}, err
	}
	return artifact, nil
}

// ---------------------------------------------------------------------------
// core batch -> zero-fill trade request
// ---------------------------------------------------------------------------

// buildCoreAsTradeRequest turns a core (no-trade) batch into a zero-fill gazk trade
// request plus the 8 public inputs the proof must commit to.
func buildCoreAsTradeRequest(input prover.GenerateProofInput) (*gazkTradeProveRequest, []string, error) {
	upd := input.SettlementUpdate
	com := input.BatchCommitments
	if strings.TrimSpace(upd.OldStateRoot) == "" || strings.TrimSpace(upd.NewStateRoot) == "" {
		return nil, nil, fmt.Errorf("oldStateRoot/newStateRoot are required")
	}
	if len(upd.Trades) > 0 {
		return nil, nil, fmt.Errorf("core-as-trade path is for no-trade batches (got %d trades); trade batches use the trade prover", len(upd.Trades))
	}

	// 8 public inputs: [0..5] via the locked core builder, [6]/[7] = chain sentinel.
	core, err := batch.BuildPublicInputs(upd, com)
	if err != nil {
		return nil, nil, fmt.Errorf("build core public inputs: %w", err)
	}
	if len(core) != batch.PublicInputCount {
		return nil, nil, fmt.Errorf("core public inputs = %d, want %d", len(core), batch.PublicInputCount)
	}
	publicInputs := make([]string, batch.PublicInputCountWithTrades)
	copy(publicInputs, core)
	publicInputs[batch.PublicInputIdxTradesRoot] = coreEmptyTradeRootSentinel
	publicInputs[batch.PublicInputIdxOrdersRoot] = coreEmptyTradeRootSentinel

	cells, err := buildCoreCells(input.Witness.Accounts)
	if err != nil {
		return nil, nil, err
	}

	req := &gazkTradeProveRequest{
		Orders:     zeroTradeOrders(),
		Fills:      zeroTradeFills(),
		BidPrice:   "0",
		AskPrice:   "0",
		FillPrice:  "0",
		MakerIsBid: false,
		Conservation: gazkTradeConservation{
			Price: "0", Qty: "0", MakerFee: "0", TakerFee: "0",
			BuyerIsMaker:             false,
			BuyerReservedQuoteBefore: "0",
			SellerReservedBaseBefore: "0",
		},
		Cells:               cells,
		OldStateRoot:        publicInputs[0],
		NewStateRoot:        publicInputs[1],
		DepositsRoot:        publicInputs[2],
		WithdrawalsRoot:     publicInputs[3],
		NullifiersRoot:      publicInputs[4],
		WithdrawOutputsRoot: publicInputs[5],
		TradesRoot:          publicInputs[6], // sentinel
		OrdersRoot:          publicInputs[7], // sentinel
	}
	return req, publicInputs, nil
}

// buildCoreCells maps each participating (owner, denom) account's old->new balance
// to a state cell. The net change is expressed as DeltaIn (credit) or DeltaOut
// (debit) so the circuit's NewBalance == OldBalance + DeltaIn - DeltaOut holds
// exactly. A deposit is a net credit, a withdrawal a net debit.
func buildCoreCells(accounts []types.WitnessAccount) ([]gazkTradeCell, error) {
	if len(accounts) == 0 {
		return nil, fmt.Errorf("witness has no accounts")
	}
	if len(accounts) > maxCoreCells {
		return nil, fmt.Errorf(
			"core batch touches %d accounts; the unified circuit binds at most %d state cells (split the batch)",
			len(accounts), maxCoreCells,
		)
	}
	cells := make([]gazkTradeCell, 0, len(accounts))
	for i, a := range accounts {
		old, ok := new(big.Int).SetString(nonEmpty(a.OldBalance), 10)
		if !ok {
			return nil, fmt.Errorf("accounts[%d] oldBalance %q invalid", i, a.OldBalance)
		}
		newBal, ok := new(big.Int).SetString(nonEmpty(a.NewBalance), 10)
		if !ok {
			return nil, fmt.Errorf("accounts[%d] newBalance %q invalid", i, a.NewBalance)
		}
		net := new(big.Int).Sub(newBal, old)
		deltaIn, deltaOut := "0", "0"
		if net.Sign() >= 0 {
			deltaIn = net.String()
		} else {
			deltaOut = new(big.Int).Neg(net).String()
		}
		denom := strings.TrimSpace(a.Denom)
		if denom == "" {
			// denom feeds only gazk's UNBOUND internal v1 root (the bound roots are the
			// supplied v0 ones), so a placeholder keeps the request valid when a legacy
			// witness carries no denom.
			denom = "uunknown"
		}
		cells = append(cells, gazkTradeCell{
			Owner:      strings.TrimSpace(a.Owner),
			Denom:      denom,
			OldBalance: old.String(),
			DeltaIn:    deltaIn,
			DeltaOut:   deltaOut,
		})
	}
	return cells, nil
}

// zeroTradeOrders returns the two placeholder orders the fixed circuit shape
// requires (protoOrderCount). Because the request supplies ordersRoot, gazk never
// hashes these orders and they enter no constraint — their values are irrelevant.
func zeroTradeOrders() []gazkTradeOrder {
	return []gazkTradeOrder{
		{OrderHash: "0x00", Owner: "zkdex/core", Side: "buy", Price: "0", Qty: "0", Remaining: "0", Filled: false, Sequence: 1},
		{OrderHash: "0x01", Owner: "zkdex/core", Side: "sell", Price: "0", Qty: "0", Remaining: "0", Filled: false, Sequence: 2},
	}
}

// zeroTradeFills returns the single zero fill (protoFillCount). Because the request
// supplies tradesRoot, gazk never hashes it; its zeros satisfy price-crossing and
// conservation trivially.
func zeroTradeFills() []gazkTradeFill {
	return []gazkTradeFill{{
		TradeID: "core-nofill", Market: "n/a",
		MakerOrderHash: "0x00", TakerOrderHash: "0x01",
		Price: "0", Qty: "0", MakerFee: "0", TakerFee: "0",
		Buyer: "zkdex/core", Seller: "zkdex/core",
	}}
}

func getGazkJSON(ctx context.Context, client *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var er gazkErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		return fmt.Errorf("GET %s status %d: %s", url, resp.StatusCode, er.Error)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
