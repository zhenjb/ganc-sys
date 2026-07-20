package tests

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/repository"
	appstate "github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/internal/service"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// Nhóm 3: withdraw_requests.status mirrors the settlement lifecycle
// (requested → included → committed) and batch_builds.status advances past 'built'
// (built → accepted) — the two columns used to freeze at their insert value.
func TestDBStatusLifecycleMirror(t *testing.T) {
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
	for _, tbl := range []string{"withdraw_requests", "batch_builds", "submit_batches"} {
		if _, err := pool.Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("clean %s: %v", tbl, err)
		}
	}

	// ---------- withdraw_requests.status ----------
	mgr := appstate.NewOffchainStateManager()
	offRepo := repository.NewOffchainSettlementRepository(pool)
	offSvc := service.NewOffchainSettlementService(mgr, offRepo)
	wdRepo := repository.NewWithdrawRepositoryWithDB(store.NewMemoryStore(), pool, "postgres", "postgres")

	if _, err := offSvc.ApplyIndexedDeposit(ctx, types.DepositRecord{
		DepositID: "d1", Owner: "cosmos1a", Denom: "uusdc", Amount: "100",
	}); err != nil {
		t.Fatalf("deposit: %v", err)
	}
	wr := types.WithdrawRequest{
		WithdrawID: "wd-1", Owner: "cosmos1a", Denom: "uusdc", Amount: "40",
		Destination: "cosmos1a", Nonce: "1", Signature: "0xsig",
	}
	if _, err := wdRepo.SaveWithdrawRequest(ctx, wr); err != nil {
		t.Fatalf("save withdraw request: %v", err)
	}
	if _, err := offSvc.ApplyWithdrawRequest(ctx, wr, "mock-user-secret"); err != nil {
		t.Fatalf("apply withdraw: %v", err)
	}
	assertStatus(t, ctx, pool, "withdraw_requests", "withdraw_id", "wd-1", "requested")

	if err := offRepo.MarkIncluded(ctx, "batch-1", nil, []string{"wd-1"}); err != nil {
		t.Fatalf("mark included: %v", err)
	}
	assertStatus(t, ctx, pool, "withdraw_requests", "withdraw_id", "wd-1", "included")

	if err := offRepo.MarkCommitted(ctx, "batch-1", "0xtx"); err != nil {
		t.Fatalf("mark committed: %v", err)
	}
	assertStatus(t, ctx, pool, "withdraw_requests", "withdraw_id", "wd-1", "committed")

	// ---------- batch_builds.status ----------
	batchRepo := repository.NewBatchRepositoryWithDB(store.NewMemoryStore(), pool, "postgres", "postgres")
	upd := types.SettlementUpdate{BatchID: "batch-2", OldStateRoot: "0x00", NewStateRoot: "0x01"}
	batchRepo.SaveBatchBuild(ctx, upd, types.BatchCommitments{}, types.Witness{})
	assertStatus(t, ctx, pool, "batch_builds", "batch_id", "batch-2", "built")

	batchRepo.SaveBatchSubmitResult(ctx, upd, types.BatchCommitments{}, "0xtx", true, "accepted", nil)
	assertStatus(t, ctx, pool, "batch_builds", "batch_id", "batch-2", "accepted")
}

func assertStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, idCol, id, want string) {
	t.Helper()
	var got string
	q := "SELECT status FROM " + table + " WHERE " + idCol + " = $1"
	if err := pool.QueryRow(ctx, q, id).Scan(&got); err != nil {
		t.Fatalf("query %s.%s=%s: %v", table, idCol, id, err)
	}
	if got != want {
		t.Fatalf("%s[%s].status = %q, want %q", table, id, got, want)
	}
}
