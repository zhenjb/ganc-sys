package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/cors"
	"github.com/zhenjb/ganc-sys/internal/api"
	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/chain"
	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/handler"
	"github.com/zhenjb/ganc-sys/internal/indexer"
	"github.com/zhenjb/ganc-sys/internal/prover"
	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/internal/store"
)

const BatchBuilderModeLocal = "local"
const BatchBuilderModeSnapshot = "snapshot"

// Relayer / chain-client mode selectors. "local" keeps the deterministic mock so
// the backend builds and runs end-to-end without a chain; "cosmos" wires the
// real obd-CLI clients. Switching is a config change only — no code edits.
const relayerModeLocal = "local"
const relayerModeCosmos = "cosmos"
const chainDepositModeLocal = "local"
const chainDepositModeCosmos = "cosmos"
const indexerModeMock = "mock"
const indexerModeChain = "chain"

func main() {
	port := getenv("PORT", "8080")

	memoryStore := store.NewMemoryStore()
	offchainStateManager := appstate.NewOffchainStateManager()

	chainQueryMode := getenv("CHAIN_QUERY_MODE", "local")
	chainRESTURL := getenv("CHAIN_REST_URL", "http://localhost:1317")
	chainQueryClient := chain.NewRestQueryClient(chainRESTURL)

	withdrawRequestStore := getenv("WITHDRAW_REQUEST_STORE", repository.WithdrawRequestStoreMemory)
	withdrawRecordStore := getenv("WITHDRAW_RECORD_STORE", repository.WithdrawRecordStoreMemory)
	batchBuildStore := getenv("BATCH_BUILD_STORE", repository.BatchBuildStoreMemory)
	proofBundleStore := getenv("PROOF_BUNDLE_STORE", repository.ProofBundleStoreMemory)
	submitBatchStore := getenv("SUBMIT_BATCH_STORE", repository.SubmitBatchStoreMemory)

	batchBuilderMode := getenv("BATCH_BUILDER_MODE", BatchBuilderModeLocal)
	batchBuildSource := getenv("BATCH_BUILD_SOURCE", service.BatchBuildSourceManual)
	offchainSettlementEnabled := getenv("OFFCHAIN_SETTLEMENT_ENABLED", "false") == "true"

	proverMode := getenv("PROVER_MODE", "local")
	proverURL := getenv("PROVER_URL", prover.DefaultRemoteProverURL)

	dbPool := openDatabaseIfNeeded(
		withdrawRequestStore,
		withdrawRecordStore,
		batchBuildStore,
		proofBundleStore,
		submitBatchStore,
		batchBuildSource,
		offchainSettlementEnabled,
	)
	if dbPool != nil {
		defer dbPool.Close()
	}

	var offchainSettlementService *service.OffchainSettlementService
	if dbPool != nil {
		offchainSettlementRepository := repository.NewOffchainSettlementRepository(dbPool)
		offchainSettlementService = service.NewOffchainSettlementService(
			offchainStateManager,
			offchainSettlementRepository,
		)
	}

	// Rebuild the in-memory off-chain state from the durable pending tables so a
	// restart does not regenerate already-consumed nonces/nullifiers and collide
	// with persisted unique constraints. Must run before serving any request.
	if offchainSettlementEnabled && offchainSettlementService != nil {
		if err := offchainSettlementService.RehydrateFromStore(context.Background()); err != nil {
			log.Printf("offchain settlement rehydrate failed: %v", err)
		}
	}

	var offchainSettlementHandler *handler.OffchainSettlementHandler
	if offchainSettlementService != nil {
		offchainSettlementHandler = handler.NewOffchainSettlementHandler(offchainSettlementService)
	}

	healthRepository := repository.NewHealthRepository()
	healthService := service.NewHealthService(healthRepository)
	healthHandler := handler.NewHealthHandler(healthService)

	stateRepository := repository.NewStateRepositoryWithChainQuery(
		memoryStore,
		chainQueryClient,
		chainQueryMode,
	)
	stateService := service.NewStateService(stateRepository)
	stateHandler := handler.NewStateHandler(stateService)

	relayerMode := getenv("RELAYER_MODE", relayerModeLocal)
	relayerClient := newRelayerClient(relayerMode)

	chainDepositMode := getenv("CHAIN_DEPOSIT_MODE", chainDepositModeLocal)
	chainClient := newChainClient(chainDepositMode)

	depositRepository := repository.NewDepositRepositoryWithChainQuery(
		memoryStore,
		chainQueryClient,
		chainQueryMode,
	)

	var depositIndexer *indexer.DepositIndexer
	if offchainSettlementEnabled {
		depositIndexer = indexer.NewDepositIndexerWithOffchainSettlement(
			depositRepository,
			offchainSettlementService,
		)
	} else {
		depositIndexer = indexer.NewDepositIndexer(depositRepository)
	}

	depositService := service.NewDepositService(depositRepository, depositIndexer, chainClient)
	depositHandler := handler.NewDepositHandler(depositService)

	// SYS-03: when running against a real chain, start the asynchronous deposit
	// poller. It reads DepositQueued/EventDeposit events from the Tendermint RPC
	// and mirrors them into the deposit store and off-chain settlement state via
	// the same DepositIndexer used by POST /api/deposit. Default mode is "mock":
	// deposits are only indexed synchronously from the deposit response, so the
	// backend still runs without a chain.
	indexerMode := getenv("INDEXER_MODE", indexerModeMock)
	if indexerMode == indexerModeChain {
		startDepositPoller(depositIndexer)
	}
	log.Printf("indexer mode=%s", indexerMode)

	withdrawRepository := repository.NewWithdrawRepositoryWithDB(
		memoryStore,
		dbPool,
		withdrawRequestStore,
		withdrawRecordStore,
	)
	withdrawService := service.NewWithdrawServiceWithOffchainSettlement(
		withdrawRepository,
		relayerClient,
		offchainSettlementEnabled,
		offchainSettlementService,
	)
	withdrawHandler := handler.NewWithdrawHandler(withdrawService)

	batchRepository := repository.NewBatchRepositoryWithDB(
		memoryStore,
		dbPool,
		batchBuildStore,
		submitBatchStore,
	)
	batchBuilder := newBatchBuilder(batchBuilderMode, offchainStateManager)
	batchService := service.NewBatchServiceWithOffchainSettlement(
		batchRepository,
		depositRepository,
		withdrawRepository,
		batchBuilder,
		relayerClient,
		batchBuildSource,
		offchainSettlementService,
	)
	// SYS-04 (STATE-14): in snapshot-builder + manual mode the batch is built from
	// the in-memory OffchainStateManager snapshot, so a rejected submit must roll
	// the manager's pending state back to the last accepted baseline. In the
	// DB-backed pending mode the offchain settlement cursor already owns rollback,
	// so the manager rollback is intentionally left unwired there.
	if batchBuilderMode == BatchBuilderModeSnapshot && batchBuildSource != service.BatchBuildSourcePending {
		batchService.SetStateRollback(offchainStateManager)
		log.Printf("batch submit state rollback enabled (snapshot builder, manual source)")
	}

	batchHandler := handler.NewBatchHandler(batchService)

	proverClient := newProverClient(proverMode, proverURL)
	proofRepository := repository.NewProofRepositoryWithDB(
		memoryStore,
		dbPool,
		proofBundleStore,
	)
	proofService := service.NewProofService(proverClient, proofRepository)
	proofHandler := handler.NewProofHandler(proofService)

	// Enable real ZK verification on batch submit when the prover client can
	// also verify (remote gazk) and it has not been explicitly disabled. This
	// makes /api/batch/submit reject batches whose Groth16 proof is invalid,
	// instead of relying on the mock relayer that accepts everything.
	proofVerifyEnabled := getenv("PROOF_VERIFY_ENABLED", "true") == "true"
	expectedArtifact := prover.ExpectedArtifact{
		VerificationKeyID: os.Getenv("PROOF_VERIFICATION_KEY_ID"),
		HashMode:          os.Getenv("PROOF_HASH_MODE"),
	}
	preflightStrict := getenv("PROOF_PREFLIGHT_STRICT", "false") == "true"
	if verifier, ok := proverClient.(prover.Verifier); ok && proofVerifyEnabled {
		batchService.SetProofVerifier(verifier)
		batchService.SetExpectedVerificationKeyID(expectedArtifact.VerificationKeyID)
		log.Printf("batch submit real ZK verification enabled via %s prover", proverMode)
		if expectedArtifact.IsZero() {
			log.Printf("warning: no expected verifier artifact pinned (set PROOF_VERIFICATION_KEY_ID/PROOF_HASH_MODE to lock the gazk circuit)")
		} else {
			log.Printf("expected verifier artifact: verificationKeyId=%q hashMode=%q",
				expectedArtifact.VerificationKeyID, expectedArtifact.HashMode)
		}
		// SYS-05 startup preflight: confirm gazk advertises the expected circuit
		// before the backend starts closing the ZK loop against it.
		preflightProverArtifact(proverClient, expectedArtifact, preflightStrict)
	} else {
		log.Printf("batch submit real ZK verification disabled (proverMode=%s, enabled=%v)", proverMode, proofVerifyEnabled)
	}

	chainQueryService := service.NewChainQueryService(chainQueryClient)
	chainQueryHandler := handler.NewChainQueryHandler(chainQueryService)

	router := api.NewRouter(api.RouterDeps{
		HealthHandler:             healthHandler,
		StateHandler:              stateHandler,
		DepositHandler:            depositHandler,
		WithdrawHandler:           withdrawHandler,
		BatchHandler:              batchHandler,
		ProofHandler:              proofHandler,
		ChainQueryHandler:         chainQueryHandler,
		OffchainSettlementHandler: offchainSettlementHandler,
	})

	addr := ":" + port
	log.Printf("ganc-sys backend API listening on http://localhost%s", addr)
	log.Printf("chain query mode=%s rest=%s", chainQueryMode, chainRESTURL)
	log.Printf("chain deposit mode=%s", chainDepositMode)
	log.Printf("relayer mode=%s", relayerMode)
	log.Printf("withdraw request store=%s", withdrawRequestStore)
	log.Printf("withdraw record store=%s", withdrawRecordStore)
	log.Printf("batch build store=%s", batchBuildStore)
	log.Printf("proof bundle store=%s", proofBundleStore)
	log.Printf("submit batch store=%s", submitBatchStore)
	log.Printf("batch builder mode=%s", batchBuilderMode)
	log.Printf("batch build source=%s", batchBuildSource)
	log.Printf("offchain settlement enabled=%v", offchainSettlementEnabled)
	log.Printf("prover mode=%s", proverMode)
	if proverMode == "remote" {
		log.Printf("prover url=%s", proverURL)
	}

	// CORS — allowed origins are configurable so the same binary serves a local
	// FE (http://localhost:3000) and a remote FE (e.g. a GitHub Codespaces
	// forwarded URL https://<name>-3000.app.github.dev). Set CORS_ALLOWED_ORIGINS
	// to a comma-separated list; a wildcard pattern like https://*.app.github.dev
	// is supported and still echoes the concrete origin so credentials work.
	//
	// The single literal "*" cannot be combined with credentials (CORS spec), so
	// when it is used we disable credentials automatically.
	allowedOrigins := corsAllowedOrigins()
	allowCredentials := true
	for _, origin := range allowedOrigins {
		if origin == "*" {
			allowCredentials = false
		}
	}
	c := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: allowCredentials,
	})
	log.Printf("CORS allowed origins=%v credentials=%v", allowedOrigins, allowCredentials)

	// 2. Bọc router.Routes() bằng middleware cors
	handlerWithCORS := c.Handler(router.Routes())

	// 3. Truyền handler đã có CORS vào đây
	if err := http.ListenAndServe(addr, handlerWithCORS); err != nil {
		log.Fatal(err)
	}
}

func newBatchBuilder(
	mode string,
	offchainStateManager *appstate.OffchainStateManager,
) batch.Builder {
	switch mode {
	case BatchBuilderModeSnapshot:
		return batch.NewSnapshotBuilder(offchainStateManager)
	case BatchBuilderModeLocal:
		return batch.NewLocalBuilder()
	default:
		log.Printf("unknown BATCH_BUILDER_MODE=%q, falling back to local", mode)
		return batch.NewLocalBuilder()
	}
}

// newRelayerClient selects the settlement/claim relayer. The real CosmosClient
// shells out to the obd CLI to broadcast MsgSubmitBatchProof / MsgClaimWithdraw;
// the LocalClient is a deterministic placeholder that preserves the REST shape.
func newRelayerClient(mode string) relayer.Client {
	switch mode {
	case relayerModeCosmos:
		cfg := relayer.CosmosConfig{
			Binary:         getenv("CHAIN_BINARY", "obd"),
			ChainID:        os.Getenv("CHAIN_ID"),
			Node:           os.Getenv("CHAIN_NODE"),
			From:           os.Getenv("RELAYER_FROM"),
			KeyringBackend: os.Getenv("CHAIN_KEYRING_BACKEND"),
			Home:           os.Getenv("CHAIN_HOME"),
			Gas:            os.Getenv("CHAIN_GAS"),
			GasAdjustment:  os.Getenv("CHAIN_GAS_ADJUSTMENT"),
			GasPrices:      os.Getenv("CHAIN_GAS_PRICES"),
			Fees:           os.Getenv("CHAIN_FEES"),
			BroadcastMode:  os.Getenv("CHAIN_BROADCAST_MODE"),
		}
		log.Printf("relayer cosmos mode: binary=%s chainID=%s node=%s from=%s",
			cfg.Binary, cfg.ChainID, cfg.Node, cfg.From)
		return relayer.NewCosmosClient(cfg, nil)
	case relayerModeLocal:
		return relayer.NewLocalClient()
	default:
		log.Printf("unknown RELAYER_MODE=%q, falling back to local", mode)
		return relayer.NewLocalClient()
	}
}

// newChainClient selects the deposit chain client. The real CosmosClient builds,
// signs and broadcasts MsgDeposit via the obd CLI and returns the on-chain
// txHash + EventDeposit; the LocalClient simulates the same typed event so the
// deposit indexer is agnostic to the source.
func newChainClient(mode string) chain.Client {
	switch mode {
	case chainDepositModeCosmos:
		cfg := chain.CosmosConfig{
			Binary:         getenv("CHAIN_BINARY", "obd"),
			ChainID:        os.Getenv("CHAIN_ID"),
			Node:           os.Getenv("CHAIN_NODE"),
			KeyringBackend: os.Getenv("CHAIN_KEYRING_BACKEND"),
			Home:           os.Getenv("CHAIN_HOME"),
			Gas:            os.Getenv("CHAIN_GAS"),
			GasAdjustment:  os.Getenv("CHAIN_GAS_ADJUSTMENT"),
			GasPrices:      os.Getenv("CHAIN_GAS_PRICES"),
			Fees:           os.Getenv("CHAIN_FEES"),
			BroadcastMode:  os.Getenv("CHAIN_BROADCAST_MODE"),
		}
		log.Printf("chain deposit cosmos mode: binary=%s chainID=%s node=%s",
			cfg.Binary, cfg.ChainID, cfg.Node)
		return chain.NewCosmosClient(cfg, nil)
	case chainDepositModeLocal:
		return chain.NewLocalClient()
	default:
		log.Printf("unknown CHAIN_DEPOSIT_MODE=%q, falling back to local", mode)
		return chain.NewLocalClient()
	}
}

// startDepositPoller builds a Tendermint-backed deposit poller and runs it in a
// background goroutine for the lifetime of the process. It is best-effort: a
// flaky RPC logs and retries on the next tick without affecting the HTTP server.
func startDepositPoller(depositIndexer *indexer.DepositIndexer) {
	rpcURL := getenv("CHAIN_RPC_URL", "http://localhost:26657")

	source := indexer.NewTendermintEventSource(rpcURL)
	if os.Getenv("INDEXER_LEGACY_BASE64") == "true" {
		source = source.WithLegacyBase64Attributes()
	}

	var startHeight int64
	if raw := os.Getenv("INDEXER_START_HEIGHT"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			startHeight = parsed
		}
	}

	var interval time.Duration
	if raw := os.Getenv("INDEXER_POLL_INTERVAL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			interval = parsed
		}
	}

	poller := indexer.NewDepositPoller(source, depositIndexer, startHeight, interval)
	log.Printf("deposit poller starting: rpc=%s startHeight=%d", rpcURL, startHeight)
	go poller.Run(context.Background())
}

func newProverClient(mode string, remoteURL string) prover.Client {
	switch mode {
	case "remote":
		return prover.NewRemoteClient(remoteURL)
	case "local":
		return prover.NewLocalClient()
	default:
		log.Printf("unknown PROVER_MODE=%q, falling back to local", mode)
		return prover.NewLocalClient()
	}
}

// preflightProverArtifact fetches gazk's verifier artifact at startup and checks
// it matches the expected (pinned) verificationKeyId / hashMode (SYS-05). This
// fails fast on a misconfigured prover instead of discovering the mismatch only
// when a real proof is submitted. In strict mode a mismatch / unreachable prover
// aborts startup; otherwise it logs loudly and continues (so the backend still
// boots when gazk is down and the mock relayer is in use).
func preflightProverArtifact(proverClient prover.Client, expected prover.ExpectedArtifact, strict bool) {
	provider, ok := proverClient.(prover.VerifierArtifactProvider)
	if !ok {
		log.Printf("prover preflight skipped: client does not expose a verifier artifact")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	artifact, err := provider.GetVerifierArtifact(ctx)
	if err != nil {
		failPreflight(strict, "prover preflight: cannot fetch verifier artifact: %v", err)
		return
	}

	if err := prover.ValidateArtifactMatchesExpected(artifact, expected); err != nil {
		failPreflight(strict, "prover preflight: verifier artifact mismatch: %v", err)
		return
	}

	log.Printf("prover preflight OK: verificationKeyId=%q hashMode=%q curve=%q backend=%q",
		artifact.VerificationKeyID, artifact.HashMode, artifact.Curve, artifact.Backend)
}

func failPreflight(strict bool, format string, args ...any) {
	if strict {
		log.Fatalf(format, args...)
	}
	log.Printf(format+" (continuing; set PROOF_PREFLIGHT_STRICT=true to fail fast)", args...)
}

// corsAllowedOrigins reads the comma-separated CORS_ALLOWED_ORIGINS env var and
// returns the list of allowed browser origins. Empty/unset falls back to the
// local dev FE origin so existing local setups are unchanged.
func corsAllowedOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if raw == "" {
		return []string{"http://localhost:3000"}
	}

	out := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		if origin := strings.TrimSpace(part); origin != "" {
			out = append(out, origin)
		}
	}
	if len(out) == 0 {
		return []string{"http://localhost:3000"}
	}
	return out
}

func openDatabaseIfNeeded(
	withdrawRequestStore string,
	withdrawRecordStore string,
	batchBuildStore string,
	proofBundleStore string,
	submitBatchStore string,
	batchBuildSource string,
	offchainSettlementEnabled bool,
) *pgxpool.Pool {
	needsDB :=
		withdrawRequestStore == repository.WithdrawRequestStorePostgres ||
			withdrawRecordStore == repository.WithdrawRecordStorePostgres ||
			batchBuildStore == repository.BatchBuildStorePostgres ||
			proofBundleStore == repository.ProofBundleStorePostgres ||
			submitBatchStore == repository.SubmitBatchStorePostgres ||
			batchBuildSource == service.BatchBuildSourcePending ||
			offchainSettlementEnabled

	if !needsDB {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	return pool
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
