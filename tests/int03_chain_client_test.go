package tests

import (
	"context"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/chain"
)

func TestINT03MockChainClientDepositSuccess(t *testing.T) {
	client := chain.NewMockClient()

	result, err := client.Deposit(context.Background(), chain.DepositRequest{
		Owner:  "cosmos1alice",
		Denom:  "uusdc",
		Amount: "100",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.TxHash == "" {
		t.Fatalf("expected txHash to be generated")
	}

	if result.DepositRecord.DepositID != "dep-1" {
		t.Fatalf("expected depositId=dep-1, got %q", result.DepositRecord.DepositID)
	}

	if result.DepositRecord.Owner != "cosmos1alice" {
		t.Fatalf("expected owner=cosmos1alice, got %q", result.DepositRecord.Owner)
	}

	if result.DepositRecord.Amount != "100" {
		t.Fatalf("expected amount=100, got %q", result.DepositRecord.Amount)
	}

	if result.DepositRecord.Processed {
		t.Fatalf("expected processed=false")
	}
}

func TestINT03MockChainClientDepositRejectsInvalidAmount(t *testing.T) {
	client := chain.NewMockClient()

	_, err := client.Deposit(context.Background(), chain.DepositRequest{
		Owner:  "cosmos1alice",
		Denom:  "uusdc",
		Amount: "abc",
	})
	if err == nil {
		t.Fatalf("expected error for invalid amount")
	}
}

func TestINT03MockChainClientDepositRejectsMissingFields(t *testing.T) {
	client := chain.NewMockClient()

	_, err := client.Deposit(context.Background(), chain.DepositRequest{
		Owner:  "cosmos1alice",
		Denom:  "uusdc",
		Amount: "",
	})
	if err == nil {
		t.Fatalf("expected error for missing amount")
	}
}
