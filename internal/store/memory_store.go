package store

import (
	"strconv"
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

type MemoryStore struct {
	mu sync.RWMutex

	appState types.AppState

	deposits     map[string]types.DepositRecord
	depositOrder []string

	withdrawRequests     map[string]types.WithdrawRequest
	withdrawRequestOrder []string
	nextWithdrawSeq      int

	withdrawRecords     map[string]types.WithdrawRecord
	withdrawRecordOrder []string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		appState: types.AppState{
			Mode:             "local",
			CurrentStateRoot: "0xrootA",
			UserBalances: map[string]string{
				"cosmos1alice/uusdc": "1000",
			},
			ModuleAccountBalance: map[string]string{
				"uusdc": "0",
			},
			LatestDeposit:          nil,
			LatestWithdrawRequest:  nil,
			LatestSettlement:       nil,
			LatestBatchCommitments: nil,
			LatestProof:            nil,
			LatestWithdrawRecords:  nil,
			ProofStatus:            "idle",
			DepositStatus:          "none",
			WithdrawStatus:         "none",
			BatchStatus:            "none",
		},
		deposits:             make(map[string]types.DepositRecord),
		depositOrder:         make([]string, 0),
		withdrawRequests:     make(map[string]types.WithdrawRequest),
		withdrawRequestOrder: make([]string, 0),
		nextWithdrawSeq:      1,
		withdrawRecords:      make(map[string]types.WithdrawRecord),
		withdrawRecordOrder:  make([]string, 0),
	}
}

func (s *MemoryStore) GetAppState() types.AppState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return cloneAppState(s.appState)
}

func (s *MemoryStore) SaveDeposit(record types.DepositRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.deposits[record.DepositID]; !exists {
		s.depositOrder = append(s.depositOrder, record.DepositID)
	}

	s.deposits[record.DepositID] = record

	s.appState.LatestDeposit = cloneDepositRecordPtr(record)
	s.appState.DepositStatus = "indexed"

	userKey := record.Owner + "/" + record.Denom
	debitBalance(s.appState.UserBalances, userKey, record.Amount)
	creditBalance(s.appState.ModuleAccountBalance, record.Denom, record.Amount)
}

// MarkDepositProcessed flips an indexed deposit's Processed flag to true once its
// batch has settled on-chain, so the deposit read model (list + latestDeposit)
// reflects the real on-chain status instead of the index-time false (Nhóm 4 (d)).
// Unknown ids are ignored.
func (s *MemoryStore) MarkDepositProcessed(depositID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.deposits[depositID]
	if !ok {
		return
	}
	record.Processed = true
	s.deposits[depositID] = record

	if s.appState.LatestDeposit != nil && s.appState.LatestDeposit.DepositID == depositID {
		s.appState.LatestDeposit.Processed = true
	}
}

func (s *MemoryStore) GetDeposit(depositID string) (types.DepositRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.deposits[depositID]
	return record, ok
}

func (s *MemoryStore) ListDeposits() []types.DepositRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]types.DepositRecord, 0, len(s.depositOrder))

	for _, depositID := range s.depositOrder {
		record, ok := s.deposits[depositID]
		if ok {
			records = append(records, record)
		}
	}

	return records
}

func (s *MemoryStore) NextWithdrawSequence() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	seq := s.nextWithdrawSeq
	s.nextWithdrawSeq++

	return seq
}

func (s *MemoryStore) SaveWithdrawRequest(request types.WithdrawRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.withdrawRequests[request.WithdrawID]; !exists {
		s.withdrawRequestOrder = append(s.withdrawRequestOrder, request.WithdrawID)
	}

	s.withdrawRequests[request.WithdrawID] = request

	s.appState.LatestWithdrawRequest = cloneWithdrawRequestPtr(request)
	s.appState.WithdrawStatus = "requested"
}

func (s *MemoryStore) GetWithdrawRequest(withdrawID string) (types.WithdrawRequest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	request, ok := s.withdrawRequests[withdrawID]
	return request, ok
}

func (s *MemoryStore) ListWithdrawRequests() []types.WithdrawRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	requests := make([]types.WithdrawRequest, 0, len(s.withdrawRequestOrder))

	for _, withdrawID := range s.withdrawRequestOrder {
		request, ok := s.withdrawRequests[withdrawID]
		if ok {
			requests = append(requests, request)
		}
	}

	return requests
}

func (s *MemoryStore) SaveBatchBuild(
	settlementUpdate types.SettlementUpdate,
	batchCommitments types.BatchCommitments,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.appState.LatestSettlement = cloneSettlementUpdatePtr(settlementUpdate)
	s.appState.LatestBatchCommitments = cloneBatchCommitmentsPtr(batchCommitments)
	s.appState.BatchStatus = "built"
	s.appState.ProofStatus = "idle"
	s.appState.WithdrawStatus = "batchBuilt"
}

// SaveLatestTradeBatch records the most recent TRADE batch's settlement,
// commitments and proof so GET /api/state's latest* pointers reflect trade batches
// too — not only the core deposit/withdraw path (Nhóm 4 (c)). It touches ONLY the
// latest* pointers, NOT CurrentStateRoot or the *Status fields, which stay owned by
// the core pipeline (the state root itself is sourced from chain REST).
func (s *MemoryStore) SaveLatestTradeBatch(
	settlementUpdate types.SettlementUpdate,
	batchCommitments types.BatchCommitments,
	proofBundle types.ProofBundle,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.appState.LatestSettlement = cloneSettlementUpdatePtr(settlementUpdate)
	s.appState.LatestBatchCommitments = cloneBatchCommitmentsPtr(batchCommitments)
	s.appState.LatestProof = cloneProofBundlePtr(proofBundle)
}

func (s *MemoryStore) SaveProofBundle(proofBundle types.ProofBundle) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.appState.LatestProof = cloneProofBundlePtr(proofBundle)
	s.appState.ProofStatus = "ready"
}

func (s *MemoryStore) SaveBatchSubmitted(
	settlementUpdate types.SettlementUpdate,
	batchCommitments types.BatchCommitments,
	withdrawRecords []types.WithdrawRecord,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, record := range withdrawRecords {
		if _, exists := s.withdrawRecords[record.WithdrawID]; !exists {
			s.withdrawRecordOrder = append(s.withdrawRecordOrder, record.WithdrawID)
		}

		s.withdrawRecords[record.WithdrawID] = record
	}

	s.appState.CurrentStateRoot = settlementUpdate.NewStateRoot
	s.appState.LatestSettlement = cloneSettlementUpdatePtr(settlementUpdate)
	s.appState.LatestBatchCommitments = cloneBatchCommitmentsPtr(batchCommitments)
	s.appState.LatestWithdrawRecords = cloneWithdrawRecords(withdrawRecords)
	s.appState.DepositStatus = "processed"
	s.appState.ProofStatus = "accepted"
	s.appState.WithdrawStatus = "readyToClaim"
	s.appState.BatchStatus = "accepted"
}

func (s *MemoryStore) SaveWithdrawRecord(record types.WithdrawRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.withdrawRecords[record.WithdrawID]; !exists {
		s.withdrawRecordOrder = append(s.withdrawRecordOrder, record.WithdrawID)
	}

	s.withdrawRecords[record.WithdrawID] = record
}

func (s *MemoryStore) GetWithdrawRecord(withdrawID string) (types.WithdrawRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.withdrawRecords[withdrawID]
	return record, ok
}

func (s *MemoryStore) ClaimWithdrawRecord(withdrawID string) (types.WithdrawRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.withdrawRecords[withdrawID]
	if !ok {
		return types.WithdrawRecord{}, false
	}

	record.Claimed = true
	s.withdrawRecords[withdrawID] = record

	for i := range s.appState.LatestWithdrawRecords {
		if s.appState.LatestWithdrawRecords[i].WithdrawID == withdrawID {
			s.appState.LatestWithdrawRecords[i] = record
			break
		}
	}

	userKey := record.Destination + "/" + record.Denom
	creditBalance(s.appState.UserBalances, userKey, record.Amount)
	debitBalance(s.appState.ModuleAccountBalance, record.Denom, record.Amount)

	s.appState.WithdrawStatus = "claimed"

	return record, true
}

func (s *MemoryStore) ListWithdrawRecords() []types.WithdrawRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]types.WithdrawRecord, 0, len(s.withdrawRecordOrder))

	for _, withdrawID := range s.withdrawRecordOrder {
		record, ok := s.withdrawRecords[withdrawID]
		if ok {
			records = append(records, record)
		}
	}

	return records
}

func (s *MemoryStore) GetBalanceSnapshot() types.BalanceSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return types.BalanceSnapshot{
		UserBalances:         cloneStringMap(s.appState.UserBalances),
		ModuleAccountBalance: cloneStringMap(s.appState.ModuleAccountBalance),
	}
}

func cloneAppState(state types.AppState) types.AppState {
	return types.AppState{
		Mode:                   state.Mode,
		CurrentStateRoot:       state.CurrentStateRoot,
		UserBalances:           cloneStringMap(state.UserBalances),
		ModuleAccountBalance:   cloneStringMap(state.ModuleAccountBalance),
		LatestDeposit:          cloneDepositRecordPtrValue(state.LatestDeposit),
		LatestWithdrawRequest:  cloneWithdrawRequestPtrValue(state.LatestWithdrawRequest),
		LatestSettlement:       cloneSettlementUpdatePtrValue(state.LatestSettlement),
		LatestBatchCommitments: cloneBatchCommitmentsPtrValue(state.LatestBatchCommitments),
		LatestProof:            cloneProofBundlePtrValue(state.LatestProof),
		LatestWithdrawRecords:  cloneWithdrawRecords(state.LatestWithdrawRecords),
		ProofStatus:            state.ProofStatus,
		DepositStatus:          state.DepositStatus,
		WithdrawStatus:         state.WithdrawStatus,
		BatchStatus:            state.BatchStatus,
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}

	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}

	return dst
}

func cloneDepositRecordPtr(record types.DepositRecord) *types.DepositRecord {
	cloned := record
	return &cloned
}

func cloneDepositRecordPtrValue(record *types.DepositRecord) *types.DepositRecord {
	if record == nil {
		return nil
	}

	cloned := *record
	return &cloned
}

func cloneWithdrawRequestPtr(request types.WithdrawRequest) *types.WithdrawRequest {
	cloned := request
	return &cloned
}

func cloneWithdrawRequestPtrValue(request *types.WithdrawRequest) *types.WithdrawRequest {
	if request == nil {
		return nil
	}

	cloned := *request
	return &cloned
}

func cloneSettlementUpdatePtr(update types.SettlementUpdate) *types.SettlementUpdate {
	cloned := update
	cloned.Deposits = append([]types.SettlementDeposit(nil), update.Deposits...)
	cloned.Withdrawals = append([]types.SettlementWithdrawal(nil), update.Withdrawals...)

	return &cloned
}

func cloneSettlementUpdatePtrValue(update *types.SettlementUpdate) *types.SettlementUpdate {
	if update == nil {
		return nil
	}

	return cloneSettlementUpdatePtr(*update)
}

func cloneBatchCommitmentsPtr(commitments types.BatchCommitments) *types.BatchCommitments {
	cloned := commitments
	return &cloned
}

func cloneBatchCommitmentsPtrValue(commitments *types.BatchCommitments) *types.BatchCommitments {
	if commitments == nil {
		return nil
	}

	cloned := *commitments
	return &cloned
}

func cloneProofBundlePtr(proof types.ProofBundle) *types.ProofBundle {
	cloned := proof
	cloned.PublicInputs = append([]string(nil), proof.PublicInputs...)

	return &cloned
}

func cloneProofBundlePtrValue(proof *types.ProofBundle) *types.ProofBundle {
	if proof == nil {
		return nil
	}

	return cloneProofBundlePtr(*proof)
}

func cloneWithdrawRecords(records []types.WithdrawRecord) []types.WithdrawRecord {
	if records == nil {
		return nil
	}

	return append([]types.WithdrawRecord(nil), records...)
}

func creditBalance(balances map[string]string, key string, amount string) {
	current := parseInt64OrZero(balances[key])
	delta := parseInt64OrZero(amount)
	balances[key] = strconv.FormatInt(current+delta, 10)
}

func debitBalance(balances map[string]string, key string, amount string) {
	current := parseInt64OrZero(balances[key])
	delta := parseInt64OrZero(amount)
	balances[key] = strconv.FormatInt(current-delta, 10)
}

func parseInt64OrZero(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}

	return parsed
}
