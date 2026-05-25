package store

import (
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

type MemoryStore struct {
	mu sync.RWMutex

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
		deposits:             make(map[string]types.DepositRecord),
		depositOrder:         make([]string, 0),
		withdrawRequests:     make(map[string]types.WithdrawRequest),
		withdrawRequestOrder: make([]string, 0),
		nextWithdrawSeq:      1,
		withdrawRecords:      make(map[string]types.WithdrawRecord),
		withdrawRecordOrder:  make([]string, 0),
	}
}

func (s *MemoryStore) SaveDeposit(record types.DepositRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.deposits[record.DepositID]; !exists {
		s.depositOrder = append(s.depositOrder, record.DepositID)
	}

	s.deposits[record.DepositID] = record
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
