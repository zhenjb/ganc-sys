package tests

import (
	"context"
	"os"
	"testing"

	appdb "github.com/zhenjb/ganc-sys/internal/db"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/store"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

func TestDBWithdrawRequestsRepository(t *testing.T) {
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("set RUN_DB_TESTS=1 to run postgres tests")
	}

	ctx := context.Background()

	pool, err := appdb.Open(ctx, appdb.DatabaseURLFromEnv())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, "DELETE FROM withdraw_requests"); err != nil {
		t.Fatalf("clean withdraw_requests: %v", err)
	}

	if _, err := pool.Exec(ctx, "ALTER SEQUENCE withdraw_request_seq RESTART WITH 1"); err != nil {
		t.Fatalf("reset withdraw_request_seq: %v", err)
	}

	repo := repository.NewWithdrawRepositoryWithDB(
		store.NewMemoryStore(),
		pool,
		repository.WithdrawRequestStorePostgres,
	)

	created := repo.CreateWithdrawRequest(ctx, types.WithdrawRequestBody{
		Owner:       "cosmos1alice",
		Denom:       "uusdc",
		Amount:      "40",
		Destination: "cosmos1alice",
	})

	if created.WithdrawID != "wd-1" {
		t.Fatalf("expected withdrawId=wd-1, got %q", created.WithdrawID)
	}

	if created.Owner != "cosmos1alice" {
		t.Fatalf("expected owner=cosmos1alice, got %q", created.Owner)
	}

	if created.Denom != "uusdc" {
		t.Fatalf("expected denom=uusdc, got %q", created.Denom)
	}

	if created.Amount != "40" {
		t.Fatalf("expected amount=40, got %q", created.Amount)
	}

	if created.Destination != "cosmos1alice" {
		t.Fatalf("expected destination=cosmos1alice, got %q", created.Destination)
	}

	if created.Nonce != "1" {
		t.Fatalf("expected nonce=1, got %q", created.Nonce)
	}

	if created.Signature == "" {
		t.Fatalf("expected signature")
	}

	got, err := repo.GetWithdrawRequest(ctx, created.WithdrawID)
	if err != nil {
		t.Fatalf("get withdraw request: %v", err)
	}

	if got.WithdrawID != created.WithdrawID {
		t.Fatalf("expected withdrawId=%q, got %q", created.WithdrawID, got.WithdrawID)
	}

	list := repo.ListWithdrawRequests(ctx)
	if len(list) != 1 {
		t.Fatalf("expected one withdraw request, got %d", len(list))
	}

	if list[0].WithdrawID != created.WithdrawID {
		t.Fatalf("expected listed withdrawId=%q, got %q", created.WithdrawID, list[0].WithdrawID)
	}

	_, err = repo.GetWithdrawRequest(ctx, "unknown")
	if err == nil {
		t.Fatalf("expected not found error")
	}

	if err != repository.ErrWithdrawRequestNotFound {
		t.Fatalf("expected ErrWithdrawRequestNotFound, got %v", err)
	}
}
