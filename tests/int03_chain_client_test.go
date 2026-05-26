package tests

import (
	"context"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/chain"
	"github.com/zhenjb/ganc-sys/internal/event"
)

func TestINT03LocalChainClientDepositSuccess(t *testing.T) {
	client := chain.NewLocalClient()

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

	if result.Height == 0 {
		t.Fatalf("expected height to be generated")
	}

	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}

	depositEvent := result.Events[0]

	if depositEvent.Type != event.TypeDeposit {
		t.Fatalf("expected event type %q, got %q", event.TypeDeposit, depositEvent.Type)
	}

	if depositEvent.Attributes["depositId"] != "dep-1" {
		t.Fatalf("expected depositId=dep-1, got %q", depositEvent.Attributes["depositId"])
	}

	if depositEvent.Attributes["creator"] != "cosmos1alice" {
		t.Fatalf("expected creator=cosmos1alice, got %q", depositEvent.Attributes["creator"])
	}

	if depositEvent.Attributes["denom"] != "uusdc" {
		t.Fatalf("expected denom=uusdc, got %q", depositEvent.Attributes["denom"])
	}

	if depositEvent.Attributes["amount"] != "100" {
		t.Fatalf("expected amount=100, got %q", depositEvent.Attributes["amount"])
	}
}

func TestINT03LocalChainClientDepositRejectsInvalidAmount(t *testing.T) {
	client := chain.NewLocalClient()

	_, err := client.Deposit(context.Background(), chain.DepositRequest{
		Owner:  "cosmos1alice",
		Denom:  "uusdc",
		Amount: "abc",
	})
	if err == nil {
		t.Fatalf("expected error for invalid amount")
	}
}

func TestINT03LocalChainClientDepositRejectsMissingFields(t *testing.T) {
	client := chain.NewLocalClient()

	_, err := client.Deposit(context.Background(), chain.DepositRequest{
		Owner:  "cosmos1alice",
		Denom:  "uusdc",
		Amount: "",
	})
	if err == nil {
		t.Fatalf("expected error for missing amount")
	}
}
