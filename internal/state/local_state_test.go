package state

import (
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

const aliceAddr = "cosmos1alice"

func aliceDeposit(id, amount string) types.DepositRecord {
	return types.DepositRecord{
		DepositID:     id,
		Owner:         aliceAddr,
		Denom:         "uusdc",
		Amount:        amount,
		Processed:     false,
		CreatedHeight: 12345,
	}
}

func TestNewLocalState_EmptyAccountsDeterministicRoot(t *testing.T) {
	a := NewLocalState()
	b := NewLocalState()
	if a.Root() != b.Root() {
		t.Fatalf("initial root not deterministic: a=%s b=%s", a.Root(), b.Root())
	}
	if a.Root() == "" || a.Root()[:2] != "0x" {
		t.Fatalf("root must be 0x-prefixed hex, got %q", a.Root())
	}
	if len(a.Snapshot()) != 0 {
		t.Fatalf("expected empty snapshot, got %d entries", len(a.Snapshot()))
	}
}

func TestApplyDeposit_CreditsBalanceAndAdvancesRoot(t *testing.T) {
	ls := NewLocalState()
	rootA := ls.Root()

	rootB, err := ls.ApplyDeposit(aliceDeposit("dep-1", "100"))
	if err != nil {
		t.Fatalf("ApplyDeposit: %v", err)
	}
	if rootB == rootA {
		t.Fatalf("root must advance after deposit, got same: %s", rootB)
	}
	if rootB != ls.Root() {
		t.Fatalf("returned root must equal LocalState.Root(): %s vs %s", rootB, ls.Root())
	}

	acc := ls.Account(aliceAddr, "uusdc")
	if acc.Balance != "100" {
		t.Fatalf("balance after deposit: want 100, got %s", acc.Balance)
	}
	if acc.Nonce != "0" {
		t.Fatalf("nonce must not change on deposit: want 0, got %s", acc.Nonce)
	}
}

func TestApplyDeposit_Idempotent(t *testing.T) {
	ls := NewLocalState()
	if _, err := ls.ApplyDeposit(aliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	_, err := ls.ApplyDeposit(aliceDeposit("dep-1", "100"))
	if !errors.Is(err, ErrDepositAlreadyApplied) {
		t.Fatalf("expected ErrDepositAlreadyApplied, got %v", err)
	}
	acc := ls.Account(aliceAddr, "uusdc")
	if acc.Balance != "100" {
		t.Fatalf("balance must remain 100 after rejected replay, got %s", acc.Balance)
	}
}

func TestApplyDeposit_AccumulatesAcrossDepositIDs(t *testing.T) {
	ls := NewLocalState()
	if _, err := ls.ApplyDeposit(aliceDeposit("dep-1", "100")); err != nil {
		t.Fatal(err)
	}
	root2, err := ls.ApplyDeposit(aliceDeposit("dep-2", "40"))
	if err != nil {
		t.Fatal(err)
	}
	if ls.Account(aliceAddr, "uusdc").Balance != "140" {
		t.Fatalf("balance want 140, got %s", ls.Account(aliceAddr, "uusdc").Balance)
	}
	if root2 == "" {
		t.Fatal("root must not be empty")
	}
}

func TestApplyDeposit_Invalid(t *testing.T) {
	cases := []struct {
		name string
		dep  types.DepositRecord
	}{
		{"empty id", types.DepositRecord{Owner: aliceAddr, Denom: "uusdc", Amount: "100"}},
		{"empty owner", types.DepositRecord{DepositID: "x", Denom: "uusdc", Amount: "100"}},
		{"empty denom", types.DepositRecord{DepositID: "x", Owner: aliceAddr, Amount: "100"}},
		{"zero amount", types.DepositRecord{DepositID: "x", Owner: aliceAddr, Denom: "uusdc", Amount: "0"}},
		{"negative amount", types.DepositRecord{DepositID: "x", Owner: aliceAddr, Denom: "uusdc", Amount: "-5"}},
		{"non-numeric amount", types.DepositRecord{DepositID: "x", Owner: aliceAddr, Denom: "uusdc", Amount: "abc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ls := NewLocalState()
			if _, err := ls.ApplyDeposit(c.dep); !errors.Is(err, ErrInvalidDepositRecord) {
				t.Fatalf("want ErrInvalidDepositRecord, got %v", err)
			}
		})
	}
}

func TestComputeRoot_OrderIndependent(t *testing.T) {
	a := NewLocalState()
	b := NewLocalState()
	_, _ = a.ApplyDeposit(types.DepositRecord{DepositID: "d1", Owner: "alice", Denom: "uusdc", Amount: "100"})
	_, _ = a.ApplyDeposit(types.DepositRecord{DepositID: "d2", Owner: "bob", Denom: "uusdc", Amount: "50"})

	_, _ = b.ApplyDeposit(types.DepositRecord{DepositID: "d2", Owner: "bob", Denom: "uusdc", Amount: "50"})
	_, _ = b.ApplyDeposit(types.DepositRecord{DepositID: "d1", Owner: "alice", Denom: "uusdc", Amount: "100"})

	if a.Root() != b.Root() {
		t.Fatalf("root must depend only on final state, not order: a=%s b=%s", a.Root(), b.Root())
	}
}
