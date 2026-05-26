package tests

import (
	"context"
	"os"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/chain"
)

func TestRealChainRestQueryClient(t *testing.T) {
	if os.Getenv("RUN_CHAIN_REST_TESTS") != "1" {
		t.Skip("set RUN_CHAIN_REST_TESTS=1 to run real chain REST tests")
	}

	baseURL := os.Getenv("CHAIN_REST_URL")
	if baseURL == "" {
		baseURL = "http://localhost:1317"
	}

	client := chain.NewRestQueryClient(baseURL)
	ctx := context.Background()

	root, err := client.GetCurrentStateRoot(ctx)
	if err != nil {
		t.Fatalf("get current state root: %v", err)
	}

	if root == "" {
		t.Fatalf("expected current state root")
	}

	moduleAddress, err := client.GetModuleAccountAddress(ctx)
	if err != nil {
		t.Fatalf("get module account address: %v", err)
	}

	if moduleAddress == "" {
		t.Fatalf("expected module account address")
	}

	moduleBalance, err := client.GetModuleAccountBalance(ctx)
	if err != nil {
		t.Fatalf("get module account balance: %v", err)
	}

	if moduleBalance == nil {
		t.Fatalf("expected module balance map")
	}

	_, found, err := client.GetDepositRecord(ctx, "dep-1")
	if err != nil {
		t.Fatalf("get missing deposit record: %v", err)
	}

	if found {
		t.Fatalf("expected dep-1 to be missing on fresh chain")
	}

	processed, err := client.GetDepositProcessed(ctx, "dep-1")
	if err != nil {
		t.Fatalf("get deposit processed: %v", err)
	}

	if processed {
		t.Fatalf("expected dep-1 processed=false on fresh chain")
	}

	_, found, err = client.GetWithdrawRecord(ctx, "wd-1")
	if err != nil {
		t.Fatalf("get missing withdraw record: %v", err)
	}

	if found {
		t.Fatalf("expected wd-1 to be missing on fresh chain")
	}

	used, err := client.GetNullifierUsed(ctx, "0xmocknullifier")
	if err != nil {
		t.Fatalf("get nullifier used: %v", err)
	}

	if used {
		t.Fatalf("expected mock nullifier used=false on fresh chain")
	}
}
