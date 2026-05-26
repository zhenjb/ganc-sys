package batch_test

import (
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/pkg/hash"
)

func TestCommitments_AllRootsHexPrefixed(t *testing.T) {
	in := canonicalAlice(t)
	upd, err := batch.NewSettlementUpdateBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c := batch.BuildCommitments(upd)

	roots := map[string]string{
		"depositsRoot":        c.DepositsRoot,
		"withdrawalsRoot":     c.WithdrawalsRoot,
		"nullifiersRoot":      c.NullifiersRoot,
		"withdrawOutputsRoot": c.WithdrawOutputsRoot,
	}
	for label, r := range roots {
		if !hash.IsHexPrefixed(r) {
			t.Fatalf("%s missing 0x prefix: %s", label, r)
		}
		if len(hash.StripHex(r)) != 64 { // SHA-256 → 32 bytes → 64 hex chars
			t.Fatalf("%s wrong length: %s", label, r)
		}
	}
}

func TestCommitments_Deterministic(t *testing.T) {
	in := canonicalAlice(t)
	upd1, err := batch.NewSettlementUpdateBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build1: %v", err)
	}
	upd2, err := batch.NewSettlementUpdateBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build2: %v", err)
	}
	c1 := batch.BuildCommitments(upd1)
	c2 := batch.BuildCommitments(upd2)
	if c1 != c2 {
		t.Fatalf("commitments not deterministic:\n  c1=%+v\n  c2=%+v", c1, c2)
	}
}

func TestCommitments_DistinctAcrossRoots(t *testing.T) {
	in := canonicalAlice(t)
	upd, err := batch.NewSettlementUpdateBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c := batch.BuildCommitments(upd)

	// 4 root cùng entry data nhưng khác domain tag phải khác nhau (anti-collision).
	roots := []string{c.DepositsRoot, c.WithdrawalsRoot, c.NullifiersRoot, c.WithdrawOutputsRoot}
	for i := 0; i < len(roots); i++ {
		for j := i + 1; j < len(roots); j++ {
			if roots[i] == roots[j] {
				t.Fatalf("root %d == root %d (domain separation broken): %s", i, j, roots[i])
			}
		}
	}
}

func TestCommitments_ChangesWhenWithdrawalChanges(t *testing.T) {
	in := canonicalAlice(t)
	upd, err := batch.NewSettlementUpdateBuilder().Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := batch.BuildCommitments(upd)

	// Mutate amount của withdrawal[0] → cả withdrawalsRoot và
	// withdrawOutputsRoot phải đổi; nullifiersRoot và depositsRoot KHÔNG.
	upd.Withdrawals[0].Amount = "39"
	c2 := batch.BuildCommitments(upd)

	if c2.WithdrawalsRoot == base.WithdrawalsRoot {
		t.Fatal("withdrawalsRoot should change when withdrawal amount mutates")
	}
	if c2.WithdrawOutputsRoot == base.WithdrawOutputsRoot {
		t.Fatal("withdrawOutputsRoot should change when withdrawal amount mutates")
	}
	if c2.NullifiersRoot != base.NullifiersRoot {
		t.Fatal("nullifiersRoot should NOT change when only amount mutates")
	}
	if c2.DepositsRoot != base.DepositsRoot {
		t.Fatal("depositsRoot should NOT change when withdrawal amount mutates")
	}
}

func TestCommitments_DomainTagsExposed(t *testing.T) {
	d, w, n, o := batch.CommitmentDomainTags()
	for label, tag := range map[string]string{
		"deposits":        d,
		"withdrawals":     w,
		"nullifiers":      n,
		"withdrawOutputs": o,
	} {
		if !strings.HasPrefix(tag, "zkdex/batch/") {
			t.Fatalf("%s domain tag must be namespaced under zkdex/batch/: %q", label, tag)
		}
	}
}
