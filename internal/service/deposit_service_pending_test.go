package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/indexer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

type fakeDepositChain struct {
	tx  chain.TxResult
	err error
}

func (f fakeDepositChain) Deposit(_ context.Context, _ chain.DepositRequest) (chain.TxResult, error) {
	return f.tx, f.err
}

func newDepositService(cc chain.Client) *service.DepositService {
	repo := repository.NewDepositRepository(store.NewMemoryStore())
	return service.NewDepositService(repo, indexer.NewDepositIndexer(repo), cc)
}

// Nhóm 4 (e): when the deposit tx is confirmed broadcast (txHash present) but the
// EventDeposit was not available for a synchronous index, CreateDeposit returns the
// txHash + a "pending" status (NOT a null response) — the async poller indexes it.
func TestCreateDepositPendingWhenCommittedButNotIndexed(t *testing.T) {
	svc := newDepositService(fakeDepositChain{
		tx:  chain.TxResult{TxHash: "DEP01"},
		err: errors.New("deposit tx DEP01 committed but EventDeposit not found after polling"),
	})

	resp, err := svc.CreateDeposit(context.Background(), types.DepositRequestBody{
		Owner: "cosmos1a", Denom: "uusdc", Amount: "100",
	})
	if err != nil {
		t.Fatalf("want nil error (pending), got %v", err)
	}
	if resp.TxHash != "DEP01" {
		t.Fatalf("txHash = %q, want DEP01 (not null)", resp.TxHash)
	}
	if resp.State.DepositStatus != "pending" {
		t.Fatalf("depositStatus = %q, want pending", resp.State.DepositStatus)
	}
}

// A hard failure with NO txHash (broadcast never landed) still returns an error.
func TestCreateDepositErrorsWhenNoTxHash(t *testing.T) {
	svc := newDepositService(fakeDepositChain{err: errors.New("broadcast failed")})

	if _, err := svc.CreateDeposit(context.Background(), types.DepositRequestBody{
		Owner: "cosmos1a", Denom: "uusdc", Amount: "100",
	}); err == nil {
		t.Fatal("want error when broadcast failed with no txHash")
	}
}
