package tests

import (
	"context"
	"os"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/chain"
	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/event"
	"github.com/zhenjb/ganc-sys/internal/indexer"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/internal/store"
)

func TestDBDepositIndexerAppliesDepositToOffchainSettlement(t *testing.T) {
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("set RUN_DB_TESTS=1 to run postgres tests")
	}

	ctx := context.Background()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer pool.Close()

	cleanOffchainSettlementTables(t, ctx, pool)

	memoryStore := store.NewMemoryStore()

	depositRepository := repository.NewDepositRepository(memoryStore)

	offchainManager := appstate.NewOffchainStateManager()
	offchainRepository := repository.NewOffchainSettlementRepository(pool)
	offchainService := service.NewOffchainSettlementService(
		offchainManager,
		offchainRepository,
	)

	depositIndexer := indexer.NewDepositIndexerWithOffchainSettlement(
		depositRepository,
		offchainService,
	)

	record, err := depositIndexer.IndexDepositFromTx(ctx, chain.TxResult{
		TxHash: "0xtx",
		Height: 123,
		Events: []event.Event{
			{
				Type: event.TypeDeposit,
				Attributes: map[string]string{
					"depositId": "dep-db-indexer-1",
					"creator":   "cosmos1alice",
					"denom":     "uusdc",
					"amount":    "100",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("index deposit from tx: %v", err)
	}

	if record.DepositID != "dep-db-indexer-1" {
		t.Fatalf("expected depositId=dep-db-indexer-1, got %q", record.DepositID)
	}

	account := offchainService.ManagerAccount("cosmos1alice", "uusdc")
	if account.Balance != "100" {
		t.Fatalf("expected manager balance=100, got %q", account.Balance)
	}

	deposits, withdrawals, err := offchainService.ListPendingSettlement(ctx)
	if err != nil {
		t.Fatalf("list pending settlement: %v", err)
	}

	if len(deposits) != 1 {
		t.Fatalf("expected one pending deposit, got %d", len(deposits))
	}

	if len(withdrawals) != 0 {
		t.Fatalf("expected no pending withdrawals, got %d", len(withdrawals))
	}

	if deposits[0].DepositID != "dep-db-indexer-1" {
		t.Fatalf("expected pending deposit dep-db-indexer-1, got %q", deposits[0].DepositID)
	}

	if deposits[0].BalanceBefore != "0" {
		t.Fatalf("expected balanceBefore=0, got %q", deposits[0].BalanceBefore)
	}

	if deposits[0].BalanceAfter != "100" {
		t.Fatalf("expected balanceAfter=100, got %q", deposits[0].BalanceAfter)
	}

	cursor, err := offchainService.Cursor(ctx)
	if err != nil {
		t.Fatalf("get cursor: %v", err)
	}

	if cursor.PendingRoot != deposits[0].RootAfter {
		t.Fatalf("expected pendingRoot=%q, got %q", deposits[0].RootAfter, cursor.PendingRoot)
	}
}
