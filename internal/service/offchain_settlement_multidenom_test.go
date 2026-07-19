package service

import "testing"

// INT-MULTIDENOM: the pending builder groups witness accounts by (owner, denom),
// so ONE owner holding several denoms yields several accounts instead of the old
// "owner appears with multiple denoms" rejection.
func TestBuildWitnessAccountsMultiDenomSameOwner(t *testing.T) {
	ordered := []pendingSettlementTransition{
		{kind: "deposit", ownerAddress: "alice", denom: "uusdc", balanceBefore: "0", balanceAfter: "500000", rootAfter: "r1"},
		{kind: "deposit", ownerAddress: "alice", denom: "uatom", balanceBefore: "0", balanceAfter: "5000", rootAfter: "r2"},
	}
	accounts, err := buildWitnessAccountsFromPendingTransitions(ordered, nil)
	if err != nil {
		t.Fatalf("same-owner multi-denom should not error, got %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("want 2 accounts (one per denom), got %d", len(accounts))
	}
	got := map[string]string{}
	for _, a := range accounts {
		got[a.Denom] = a.NewBalance
	}
	if got["uusdc"] != "500000" || got["uatom"] != "5000" {
		t.Fatalf("per-denom balances wrong: %+v", got)
	}
}

// INT-MULTIDENOM: a batch is capped to the circuit's account-cell budget. A
// pending chain touching more than `max` distinct (owner,denom) accounts is
// bounded to the first `max`; repeats of an already-counted account are free.
func TestBoundMaxDistinctAccountsPrefix(t *testing.T) {
	tx := func(owner, denom, rootAfter string) pendingSettlementTransition {
		return pendingSettlementTransition{kind: "deposit", ownerAddress: owner, denom: denom, rootAfter: rootAfter}
	}

	five := []pendingSettlementTransition{
		tx("alice", "uusdc", "r1"),
		tx("bob", "uatom", "r2"),
		tx("carol", "uosmo", "r3"),
		tx("dave", "uusdc", "r4"),
		tx("erin", "uatom", "r5"), // 5th distinct account → dropped when max=4
	}
	if got := boundMaxDistinctAccountsPrefix(five, 4); len(got) != 4 {
		t.Fatalf("cell budget 4: want 4, got %d", len(got))
	}

	// Same (owner,denom) repeating counts once — the whole prefix survives.
	repeats := []pendingSettlementTransition{
		tx("alice", "uusdc", "r1"),
		tx("alice", "uusdc", "r2"),
		tx("bob", "uatom", "r3"),
	}
	if got := boundMaxDistinctAccountsPrefix(repeats, 4); len(got) != 3 {
		t.Fatalf("repeat account should be free: want 3, got %d", len(got))
	}

	// A single (owner,denom) with two denoms counts as two accounts.
	twoDenom := []pendingSettlementTransition{
		tx("alice", "uusdc", "r1"),
		tx("alice", "uatom", "r2"),
	}
	if got := boundMaxDistinctAccountsPrefix(twoDenom, 4); len(got) != 2 {
		t.Fatalf("two denoms = two cells: want 2, got %d", len(got))
	}
}
