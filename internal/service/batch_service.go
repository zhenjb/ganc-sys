package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	batchbuilder "github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/internal/prover"
	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// StateRollback is the narrow STATE-14 boundary the batch service uses to keep
// the in-memory off-chain pending state consistent across a batch submit. It is
// satisfied by *state.OffchainStateManager.
//
// Semantics: Snapshot() captures the current pending state; Rollback(snap)
// restores it. The batch service treats the snapshot taken right after the last
// accepted submit as the "committed baseline" and reverts to it whenever a submit
// is rejected, so a failed batch never leaves a pending balance stuck.
type StateRollback interface {
	Snapshot() appstate.Snapshot
	Rollback(appstate.Snapshot)
}

const (
	BatchBuildSourceManual  = "manual"
	BatchBuildSourcePending = "pending"
)

var ErrOffchainSettlementServiceRequired = errors.New("offchain settlement service is required for pending batch build source")
var ErrManualBatchInsufficientOffchainBalance = errors.New("insufficient off-chain balance")
var ErrManualBatchDepositNotFound = errors.New("deposit not found")
var ErrProofVerificationFailed = errors.New("proof verification failed")

// BatchService owns batch endpoint orchestration.
//
// P4 owns this service as integration glue.
// P3 owns the actual batch builder / off-chain settlement implementation.
// P1 owns the actual batch submit implementation behind relayer.Client.
type BatchService struct {
	batchRepository    *repository.BatchRepository
	depositRepository  *repository.DepositRepository
	withdrawRepository *repository.WithdrawRepository
	batchBuilder       batchbuilder.Builder
	relayerClient      relayer.Client

	buildSource               string
	offchainSettlementService *OffchainSettlementService

	// proofVerifier performs real ZK verification before a batch is submitted.
	// When nil, verification is skipped (e.g. local/mock prover mode where the
	// proof is not a real Groth16 proof). When set (remote gazk prover), an
	// invalid proof rejects the batch and currentStateRoot does not advance.
	proofVerifier prover.Verifier

	// expectedVerificationKeyID pins the circuit the submitted proof must target
	// (SYS-05). When set, a proofBundle whose verificationKeyId differs is rejected
	// before gazk is even called, so the ZK loop never closes against the wrong
	// circuit. Empty = no pin (backward compatible). Only enforced in real verify
	// mode (proofVerifier != nil).
	expectedVerificationKeyID string

	// stateRollback + committedSnapshot implement STATE-14 submit rollback for the
	// in-memory off-chain manager (manual / snapshot-builder mode). committedSnapshot
	// is the pending state as of the last accepted submit; on a rejected submit the
	// manager is restored to it so no balance is left "stuck". nil disables the
	// mechanism, which is correct for the DB-backed pending mode where the cursor /
	// reopen flow already owns consistency.
	stateRollback     StateRollback
	snapMu            sync.Mutex
	committedSnapshot appstate.Snapshot
	hasBaseline       bool
}

// SetProofVerifier injects a real ZK proof verifier. It is an optional
// dependency so existing call sites and mock-mode setups remain unchanged.
func (s *BatchService) SetProofVerifier(verifier prover.Verifier) {
	s.proofVerifier = verifier
}

// SetExpectedVerificationKeyID pins the verificationKeyId every submitted proof
// must carry (SYS-05). Empty disables the pin. Wired from the
// PROOF_VERIFICATION_KEY_ID env in cmd/api.
func (s *BatchService) SetExpectedVerificationKeyID(id string) {
	s.expectedVerificationKeyID = strings.TrimSpace(id)
}

// guardProofContract enforces, before the ZK verifier runs, that the submitted
// proof targets the expected circuit and that its public inputs bind to THIS
// settlement. It is defense-in-depth: it does not trust gazk to catch a
// mismatched verificationKeyId or a proof whose public inputs describe a
// different batch. Any failure is an ErrProofVerificationFailed so the caller
// rejects the batch without advancing state.
func (s *BatchService) guardProofContract(req types.SubmitBatchRequestBody) error {
	if s.expectedVerificationKeyID != "" &&
		strings.TrimSpace(req.ProofBundle.VerificationKeyID) != s.expectedVerificationKeyID {
		return fmt.Errorf(
			"%w: proofBundle.verificationKeyId %q does not match expected %q",
			ErrProofVerificationFailed,
			req.ProofBundle.VerificationKeyID,
			s.expectedVerificationKeyID,
		)
	}

	expectedInputs, err := expectedProofPublicInputs(req)
	if err != nil {
		return fmt.Errorf("%w: derive public inputs: %v", ErrProofVerificationFailed, err)
	}

	if len(req.ProofBundle.PublicInputs) != len(expectedInputs) {
		return fmt.Errorf(
			"%w: proofBundle.publicInputs length %d != expected %d",
			ErrProofVerificationFailed,
			len(req.ProofBundle.PublicInputs),
			len(expectedInputs),
		)
	}

	for i := range expectedInputs {
		if req.ProofBundle.PublicInputs[i] != expectedInputs[i] {
			return fmt.Errorf(
				"%w: proofBundle.publicInputs[%d]=%q does not bind to settlement (expected %q)",
				ErrProofVerificationFailed,
				i,
				req.ProofBundle.PublicInputs[i],
				expectedInputs[i],
			)
		}
	}

	return nil
}

// expectedProofPublicInputs derives the public-input vector the proof MUST commit
// to, matching whichever layout the proof carries (TRD-UNIFY):
//   - 6 inputs: the legacy core circuit ([0..5]).
//   - 8 inputs: the unified circuit gazk-trade-v1 — [0..5] plus [6]/[7]. For a
//     no-trade batch [6]/[7] are the all-zeros sentinel the chain forces in
//     derivePublicInputs (and the relayer forces in normalizeCoreSubmitToEight),
//     NOT the SHA-256 empty roots BuildPublicInputsWithTrades uses; a batch that
//     carries trades keeps its real tradesRoot/ordersRoot.
func expectedProofPublicInputs(req types.SubmitBatchRequestBody) ([]string, error) {
	if len(req.ProofBundle.PublicInputs) == batchbuilder.PublicInputCountWithTrades {
		full, err := batchbuilder.BuildPublicInputsWithTrades(req.SettlementUpdate, req.BatchCommitments)
		if err != nil {
			return nil, err
		}
		if len(req.SettlementUpdate.Trades) == 0 {
			full[batchbuilder.PublicInputIdxTradesRoot] = coreEmptyTradeRootSentinel
			full[batchbuilder.PublicInputIdxOrdersRoot] = coreEmptyTradeRootSentinel
		}
		return full, nil
	}
	return batchbuilder.BuildPublicInputs(req.SettlementUpdate, req.BatchCommitments)
}

// SetStateRollback wires the off-chain state manager so a rejected batch submit
// rolls the in-memory pending state back to the last accepted baseline. The
// current manager state is captured immediately as the initial baseline. Use
// this only in manual / snapshot-builder mode; leave it unset in DB-backed
// pending mode, where the offchain settlement cursor handles rollback.
func (s *BatchService) SetStateRollback(rollback StateRollback) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	s.stateRollback = rollback
	if rollback != nil {
		s.committedSnapshot = rollback.Snapshot()
		s.hasBaseline = true
	}
}

// commitStateBaseline advances the rollback baseline to the current pending
// state after an accepted submit.
func (s *BatchService) commitStateBaseline() {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if s.stateRollback == nil {
		return
	}
	s.committedSnapshot = s.stateRollback.Snapshot()
	s.hasBaseline = true
}

// rollbackStateToBaseline restores the pending state to the last accepted
// baseline after a rejected submit. No-op when no rollback target is configured.
func (s *BatchService) rollbackStateToBaseline() {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if s.stateRollback == nil || !s.hasBaseline {
		return
	}
	s.stateRollback.Rollback(s.committedSnapshot)
}

func NewBatchService(
	batchRepository *repository.BatchRepository,
	depositRepository *repository.DepositRepository,
	withdrawRepository *repository.WithdrawRepository,
	batchBuilder batchbuilder.Builder,
	relayerClient relayer.Client,
) *BatchService {
	return &BatchService{
		batchRepository:    batchRepository,
		depositRepository:  depositRepository,
		withdrawRepository: withdrawRepository,
		batchBuilder:       batchBuilder,
		relayerClient:      relayerClient,
		buildSource:        BatchBuildSourceManual,
	}
}

func NewBatchServiceWithOffchainSettlement(
	batchRepository *repository.BatchRepository,
	depositRepository *repository.DepositRepository,
	withdrawRepository *repository.WithdrawRepository,
	batchBuilder batchbuilder.Builder,
	relayerClient relayer.Client,
	buildSource string,
	offchainSettlementService *OffchainSettlementService,
) *BatchService {
	if buildSource == "" {
		buildSource = BatchBuildSourceManual
	}

	return &BatchService{
		batchRepository:           batchRepository,
		depositRepository:         depositRepository,
		withdrawRepository:        withdrawRepository,
		batchBuilder:              batchBuilder,
		relayerClient:             relayerClient,
		buildSource:               buildSource,
		offchainSettlementService: offchainSettlementService,
	}
}

func (s *BatchService) BuildBatch(ctx context.Context, req types.BuildBatchRequestBody) (types.BuildBatchResponse, error) {
	if s.buildSource == BatchBuildSourcePending {
		return s.buildPendingBatch(ctx)
	}

	return s.buildManualBatch(ctx, req)
}

func (s *BatchService) buildManualBatch(ctx context.Context, req types.BuildBatchRequestBody) (types.BuildBatchResponse, error) {
	if len(req.DepositIDs) == 0 && len(req.WithdrawIDs) == 0 {
		return types.BuildBatchResponse{}, errors.New("depositIds and withdrawIds are required")
	}

	deposits := make([]types.DepositRecord, 0, len(req.DepositIDs))
	for _, depositID := range req.DepositIDs {
		deposit, err := s.depositRepository.GetDeposit(ctx, depositID)
		if err != nil {
			return types.BuildBatchResponse{}, normalizeManualBatchBuildError(err)
		}

		deposits = append(deposits, deposit)
	}

	withdrawRequests := make([]types.WithdrawRequest, 0, len(req.WithdrawIDs))
	for _, withdrawID := range req.WithdrawIDs {
		withdrawReq, err := s.withdrawRepository.GetWithdrawRequest(ctx, withdrawID)
		if err != nil {
			return types.BuildBatchResponse{}, normalizeManualBatchBuildError(err)
		}

		withdrawRequests = append(withdrawRequests, withdrawReq)
	}

	output, err := s.batchBuilder.Build(ctx, batchbuilder.BuildInput{
		OldStateRoot:     "0xrootA",
		Deposits:         deposits,
		WithdrawRequests: withdrawRequests,
	})
	if err != nil {
		return types.BuildBatchResponse{}, normalizeManualBatchBuildError(err)
	}

	s.batchRepository.SaveBatchBuild(
		ctx,
		output.SettlementUpdate,
		output.BatchCommitments,
		output.Witness,
	)

	return types.BuildBatchResponse{
		SettlementUpdate: output.SettlementUpdate,
		BatchCommitments: output.BatchCommitments,
		Witness:          output.Witness,
		State: types.PartialState{
			BatchStatus:    "built",
			ProofStatus:    "idle",
			WithdrawStatus: "batchBuilt",
		},
	}, nil
}

func (s *BatchService) buildPendingBatch(ctx context.Context) (types.BuildBatchResponse, error) {
	if s.offchainSettlementService == nil {
		return types.BuildBatchResponse{}, ErrOffchainSettlementServiceRequired
	}

	output, err := s.offchainSettlementService.BuildPendingBatch(ctx, nil)
	if err != nil {
		return types.BuildBatchResponse{}, err
	}

	s.batchRepository.SaveBatchBuild(
		ctx,
		output.SettlementUpdate,
		output.BatchCommitments,
		output.Witness,
	)

	return types.BuildBatchResponse{
		SettlementUpdate: output.SettlementUpdate,
		BatchCommitments: output.BatchCommitments,
		Witness:          output.Witness,
		State: types.PartialState{
			BatchStatus:    "built",
			ProofStatus:    "idle",
			WithdrawStatus: "batchBuilt",
		},
	}, nil
}

func (s *BatchService) SubmitBatch(ctx context.Context, req types.SubmitBatchRequestBody) (types.SubmitBatchResponse, error) {
	// Real ZK verification gate. Mirrors the on-chain x/zkdex invariant:
	// currentStateRoot advances only after proof verification succeeds.
	// On failure the batch is rejected before the relayer runs, so there is no
	// root update, no nullifier write, and no withdraw record creation (the
	// "invalid proof => no state change" invariant). Off-chain pending state is
	// left untouched; rolling it back is an explicit P3 operation, not part of
	// verification.
	if s.proofVerifier != nil {
		// SYS-05: pin the circuit + bind public inputs to this settlement BEFORE
		// calling gazk. Rejects a proof for the wrong verificationKeyId or one
		// whose public inputs describe a different batch.
		if guardErr := s.guardProofContract(req); guardErr != nil {
			s.rollbackStateToBaseline()
			return types.SubmitBatchResponse{}, guardErr
		}

		if verifyErr := s.proofVerifier.Verify(ctx, prover.VerifyProofInput{
			SettlementUpdate: req.SettlementUpdate,
			BatchCommitments: req.BatchCommitments,
			ProofBundle:      req.ProofBundle,
		}); verifyErr != nil {
			// STATE-14: invalid proof => batch rejected => restore pending state.
			s.rollbackStateToBaseline()
			return types.SubmitBatchResponse{}, fmt.Errorf("%w: %v", ErrProofVerificationFailed, verifyErr)
		}
	}

	result, err := s.relayerClient.SubmitBatch(ctx, relayer.SubmitBatchInput{
		SettlementUpdate: req.SettlementUpdate,
		BatchCommitments: req.BatchCommitments,
		ProofBundle:      req.ProofBundle,
	})
	if err != nil {
		// Relayer / chain error => batch not applied => restore pending state.
		s.rollbackStateToBaseline()
		return types.SubmitBatchResponse{}, err
	}

	// STATE-14: keep the in-memory off-chain baseline in lockstep with the chain.
	// Accepted advances the baseline to the new pending state; a rejection rolls
	// the pending state back so no balance is left stuck in an unsettled batch.
	if result.Accepted {
		s.commitStateBaseline()
	} else {
		s.rollbackStateToBaseline()
	}

	s.batchRepository.SaveBatchSubmitResult(
		ctx,
		req.SettlementUpdate,
		req.BatchCommitments,
		result.TxHash,
		result.Accepted,
		result.ProofStatus,
		result.WithdrawRecords,
	)

	if s.buildSource == BatchBuildSourcePending && s.offchainSettlementService != nil {
		if result.Accepted {
			if err := s.offchainSettlementService.CommitBatch(
				ctx,
				req.SettlementUpdate.BatchID,
				result.TxHash,
				req.SettlementUpdate.NewStateRoot,
			); err != nil {
				return types.SubmitBatchResponse{}, err
			}
		} else {
			if err := s.offchainSettlementService.FailBatch(
				ctx,
				req.SettlementUpdate.BatchID,
				result.ProofStatus,
			); err != nil {
				return types.SubmitBatchResponse{}, err
			}
		}
	}

	return types.SubmitBatchResponse{
		TxHash:           result.TxHash,
		Accepted:         result.Accepted,
		ProofStatus:      result.ProofStatus,
		SettlementUpdate: req.SettlementUpdate,
		BatchCommitments: req.BatchCommitments,
		WithdrawRecords:  result.WithdrawRecords,
		State: types.PartialState{
			CurrentStateRoot: req.SettlementUpdate.NewStateRoot,
			DepositStatus:    "processed",
			ProofStatus:      result.ProofStatus,
			WithdrawStatus:   "readyToClaim",
			BatchStatus:      "accepted",
		},
	}, nil
}

func normalizeManualBatchBuildError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, batchbuilder.ErrInsufficientOffchainBalance) {
		return ErrManualBatchInsufficientOffchainBalance
	}

	if errors.Is(err, repository.ErrDepositNotFound) ||
		strings.Contains(err.Error(), "deposit record not found") {
		return ErrManualBatchDepositNotFound
	}

	return err
}
