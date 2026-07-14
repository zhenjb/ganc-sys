package main

import (
	"fmt"

	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/service"
)

// TRD-V1.0 — decouple the trade PROVER from the trade SUBMITTER.
//
// Before TRD-V1 the two were coupled in one if/else (main.go): TRADE_PROVER_MODE=
// remote forced RemoteTradeProver + RemoteTradeVerifierSubmitter (real proof, but
// only VERIFIED against gazk — never submitted to the chain); anything else used the
// stub prover + RelayerTradeSubmitter (chain submit, but a fake proof). There was NO
// way to get a REAL gazk proof SUBMITTED to the chain — exactly what a live trade
// settle (TRD-V1) needs. resolveTradeWiring picks prover and submitter INDEPENDENTLY
// so `TRADE_PROVER_MODE=remote TRADE_SUBMIT_MODE=chain` yields RemoteTradeProver +
// RelayerTradeSubmitter, while the two legacy combinations stay the defaults.

// Trade submit mode selectors (TRD-V1.0).
const (
	// tradeSubmitModeChain submits the proven batch to the chain via the relayer
	// (MsgSubmitBatchProof). This is what TRD-V1 pairs with a real gazk proof.
	tradeSubmitModeChain = "chain"
	// tradeSubmitModeGazkVerify verifies the proof against gazk's vk without any
	// chain submit (the ZK-T10 prove→verify loop).
	tradeSubmitModeGazkVerify = "gazk-verify"
	// tradeSubmitModeLocal keeps the Wave-1 in-process stub submitter.
	tradeSubmitModeLocal = "local"
)

// tradeWiringConfig is the resolved configuration for the trade settlement seams.
type tradeWiringConfig struct {
	proverMode  string              // TRADE_PROVER_MODE: local | remote
	submitMode  string              // TRADE_SUBMIT_MODE: "" (auto) | chain | gazk-verify | local
	gazkURL     string              // GAZK_TRADE_URL
	tradeClient relayer.TradeClient // nil when the relayer has no trade client
}

// tradeWiring is the resolved prover/submitter pair (nil = keep the service default)
// plus a human description and any non-fatal warnings for the operator log.
type tradeWiring struct {
	prover     service.TradeProver
	submitter  service.TradeSubmitter
	proverDesc string
	submitDesc string
	warnings   []string
}

// resolveTradeWiring maps env config to a (prover, submitter) pair. Nil means "keep
// the RealOrderService default" (LocalTradeProver / LocalTradeSubmitter). It never
// panics on a missing trade client: TRADE_SUBMIT_MODE=chain without one falls back
// to the stub submitter with a warning rather than dropping trade submit silently.
func resolveTradeWiring(cfg tradeWiringConfig) tradeWiring {
	w := tradeWiring{}

	// Prover: remote gazk vs the in-process stub (nil keeps the default stub).
	switch cfg.proverMode {
	case tradeProverModeRemote:
		w.prover = service.NewRemoteTradeProver(cfg.gazkURL)
		w.proverDesc = "remote-gazk"
	default:
		w.proverDesc = "local-stub"
	}

	// Submit mode defaults preserve the two legacy combinations when unset:
	// remote prover → gazk-verify (ZK-T10 loop); local prover → chain (INT-T08).
	submitMode := cfg.submitMode
	if submitMode == "" {
		if cfg.proverMode == tradeProverModeRemote {
			submitMode = tradeSubmitModeGazkVerify
		} else {
			submitMode = tradeSubmitModeChain
		}
	}

	switch submitMode {
	case tradeSubmitModeChain:
		if cfg.tradeClient != nil {
			w.submitter = service.NewRelayerTradeSubmitter(cfg.tradeClient)
			w.submitDesc = "chain-relayer"
		} else {
			w.warnings = append(w.warnings,
				"TRADE_SUBMIT_MODE=chain but the relayer exposes no trade client; keeping the stub submitter")
			w.submitDesc = "local-stub (no trade client)"
		}
	case tradeSubmitModeGazkVerify:
		w.submitter = service.NewRemoteTradeVerifierSubmitter(cfg.gazkURL)
		w.submitDesc = "gazk-verify"
	case tradeSubmitModeLocal:
		w.submitDesc = "local-stub"
	default:
		w.warnings = append(w.warnings, fmt.Sprintf("unknown TRADE_SUBMIT_MODE=%q; keeping the default submitter", submitMode))
		w.submitDesc = fmt.Sprintf("local-stub (unknown mode %q)", submitMode)
	}

	return w
}
