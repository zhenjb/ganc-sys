package store

import (
	"sync"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

type MemoryStore struct {
	mu sync.RWMutex

	deposits     map[string]types.DepositRecord
	depositOrder []string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		deposits:     make(map[string]types.DepositRecord),
		depositOrder: make([]string, 0),
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
