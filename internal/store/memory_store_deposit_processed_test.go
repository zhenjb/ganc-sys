package store_test

import (
	"testing"

	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// Nhóm 4 (d): a deposit's Processed flag flips true once its batch settles, in both
// the list read model and the latestDeposit pointer; unknown ids are a no-op.
func TestMarkDepositProcessed(t *testing.T) {
	s := store.NewMemoryStore()
	s.SaveDeposit(types.DepositRecord{DepositID: "dep-1", Owner: "cosmos1a", Denom: "uusdc", Amount: "100"})

	if depositProcessed(t, s, "dep-1") {
		t.Fatal("want processed=false at index time")
	}
	if st := s.GetAppState(); st.LatestDeposit == nil || st.LatestDeposit.Processed {
		t.Fatal("latestDeposit should be present and processed=false")
	}

	s.MarkDepositProcessed("dep-1")

	if !depositProcessed(t, s, "dep-1") {
		t.Fatal("want processed=true after MarkDepositProcessed")
	}
	if st := s.GetAppState(); st.LatestDeposit == nil || !st.LatestDeposit.Processed {
		t.Fatal("latestDeposit.Processed should be true after mark")
	}

	s.MarkDepositProcessed("dep-unknown") // no-op, must not panic
}

func depositProcessed(t *testing.T, s *store.MemoryStore, id string) bool {
	t.Helper()
	for _, d := range s.ListDeposits() {
		if d.DepositID == id {
			return d.Processed
		}
	}
	t.Fatalf("deposit %s not found", id)
	return false
}
