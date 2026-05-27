package batch_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zhenjb/ganc-sys/internal/batch"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// canonicalAlicePublicInputs là helper tái sử dụng cho mọi test STATE-10:
// chạy canonicalAlice (đã định nghĩa ở builder_test.go) → SettlementUpdate
// + BatchCommitments → trả về cùng input STATE-10 sẽ consume.
func canonicalAlicePublicInputs(t *testing.T) (types.SettlementUpdate, types.BatchCommitments) {
	t.Helper()

	in := canonicalAlice(t)
	upd, err := batch.NewSettlementUpdateBuilder().Build(in)
	if err != nil {
		t.Fatalf("SettlementUpdateBuilder.Build: %v", err)
	}
	com := batch.BuildCommitments(upd)
	return upd, com
}

func TestPublicInputs_Canonical_Order(t *testing.T) {
	upd, com := canonicalAlicePublicInputs(t)

	pi, err := batch.BuildPublicInputs(upd, com)
	if err != nil {
		t.Fatalf("BuildPublicInputs: %v", err)
	}

	if got, want := len(pi), batch.PublicInputCount; got != want {
		t.Fatalf("len(publicInputs) = %d, want %d", got, want)
	}

	cases := []struct {
		idx  int
		got  string
		want string
		name string
	}{
		{batch.PublicInputIdxOldStateRoot, pi[batch.PublicInputIdxOldStateRoot], upd.OldStateRoot, "oldStateRoot"},
		{batch.PublicInputIdxNewStateRoot, pi[batch.PublicInputIdxNewStateRoot], upd.NewStateRoot, "newStateRoot"},
		{batch.PublicInputIdxDepositsRoot, pi[batch.PublicInputIdxDepositsRoot], com.DepositsRoot, "depositsRoot"},
		{batch.PublicInputIdxWithdrawalsRoot, pi[batch.PublicInputIdxWithdrawalsRoot], com.WithdrawalsRoot, "withdrawalsRoot"},
		{batch.PublicInputIdxNullifiersRoot, pi[batch.PublicInputIdxNullifiersRoot], com.NullifiersRoot, "nullifiersRoot"},
		{batch.PublicInputIdxWithdrawOutputsRoot, pi[batch.PublicInputIdxWithdrawOutputsRoot], com.WithdrawOutputsRoot, "withdrawOutputsRoot"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("publicInputs[%d] (%s) = %q, want %q", c.idx, c.name, c.got, c.want)
		}
	}
}

func TestPublicInputs_BuilderMethod_EqualsHelper(t *testing.T) {
	upd, com := canonicalAlicePublicInputs(t)

	b := batch.NewPublicInputBuilder()
	a, err := b.Build(upd, com)
	if err != nil {
		t.Fatalf("Builder.Build: %v", err)
	}
	c, err := batch.BuildPublicInputs(upd, com)
	if err != nil {
		t.Fatalf("BuildPublicInputs: %v", err)
	}

	if len(a) != len(c) {
		t.Fatalf("length mismatch: builder=%d helper=%d", len(a), len(c))
	}
	for i := range a {
		if a[i] != c[i] {
			t.Fatalf("publicInputs[%d] mismatch: builder=%q helper=%q", i, a[i], c[i])
		}
	}
}

func TestPublicInputs_Deterministic(t *testing.T) {
	upd, com := canonicalAlicePublicInputs(t)

	a, err := batch.BuildPublicInputs(upd, com)
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}
	b, err := batch.BuildPublicInputs(upd, com)
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("publicInputs[%d] not deterministic: %q vs %q", i, a[i], b[i])
		}
	}
}

func TestPublicInputs_Labels(t *testing.T) {
	labels := batch.PublicInputLabels()
	if got, want := len(labels), batch.PublicInputCount; got != want {
		t.Fatalf("len(labels) = %d, want %d", got, want)
	}
	expected := []string{
		"oldStateRoot",
		"newStateRoot",
		"depositsRoot",
		"withdrawalsRoot",
		"nullifiersRoot",
		"withdrawOutputsRoot",
	}
	for i, want := range expected {
		if labels[i] != want {
			t.Fatalf("labels[%d] = %q, want %q", i, labels[i], want)
		}
	}

	// Defensive copy invariant: mutating returned slice không ảnh
	// hưởng global state.
	labels[0] = "tampered"
	again := batch.PublicInputLabels()
	if again[0] != "oldStateRoot" {
		t.Fatalf("PublicInputLabels returned a shared slice (got %q after mutation)", again[0])
	}
}

func TestPublicInputs_RejectEmptyRoot(t *testing.T) {
	upd, com := canonicalAlicePublicInputs(t)

	cases := []struct {
		name   string
		mutate func(*types.SettlementUpdate, *types.BatchCommitments)
	}{
		{"oldStateRoot empty", func(u *types.SettlementUpdate, _ *types.BatchCommitments) { u.OldStateRoot = "" }},
		{"newStateRoot empty", func(u *types.SettlementUpdate, _ *types.BatchCommitments) { u.NewStateRoot = "" }},
		{"depositsRoot empty", func(_ *types.SettlementUpdate, c *types.BatchCommitments) { c.DepositsRoot = "" }},
		{"withdrawalsRoot empty", func(_ *types.SettlementUpdate, c *types.BatchCommitments) { c.WithdrawalsRoot = "" }},
		{"nullifiersRoot empty", func(_ *types.SettlementUpdate, c *types.BatchCommitments) { c.NullifiersRoot = "" }},
		{"withdrawOutputsRoot empty", func(_ *types.SettlementUpdate, c *types.BatchCommitments) { c.WithdrawOutputsRoot = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := upd
			c := com
			tc.mutate(&u, &c)
			pi, err := batch.BuildPublicInputs(u, c)
			if err == nil {
				t.Fatalf("expected ErrInvalidPublicInputs, got nil (pi=%v)", pi)
			}
			if !errors.Is(err, batch.ErrInvalidPublicInputs) {
				t.Fatalf("expected ErrInvalidPublicInputs, got %v", err)
			}
		})
	}
}

func TestPublicInputs_RejectMissingHexPrefix(t *testing.T) {
	upd, com := canonicalAlicePublicInputs(t)
	upd.NewStateRoot = strings.TrimPrefix(upd.NewStateRoot, "0x")

	_, err := batch.BuildPublicInputs(upd, com)
	if err == nil {
		t.Fatal("expected error for missing 0x prefix")
	}
	if !errors.Is(err, batch.ErrInvalidPublicInputs) {
		t.Fatalf("expected ErrInvalidPublicInputs, got %v", err)
	}
}

func TestPublicInputs_RejectNoOpBatch(t *testing.T) {
	upd, com := canonicalAlicePublicInputs(t)
	upd.NewStateRoot = upd.OldStateRoot

	_, err := batch.BuildPublicInputs(upd, com)
	if err == nil {
		t.Fatal("expected error when oldStateRoot == newStateRoot")
	}
	if !errors.Is(err, batch.ErrInvalidPublicInputs) {
		t.Fatalf("expected ErrInvalidPublicInputs, got %v", err)
	}
	if !strings.Contains(err.Error(), "no-op") {
		t.Fatalf("expected message to mention no-op, got %v", err)
	}
}

func TestPublicInputs_MatchProverClientOrder(t *testing.T) {
	// Đây là regression test cross-package: thứ tự public input của
	// STATE-10 PHẢI khớp với thứ tự P4 prover client hiện đang inline
	// (internal/prover/client.go). Khi P4 refactor sang dùng
	// batch.BuildPublicInputs, test này vẫn đảm bảo contract không drift.
	upd, com := canonicalAlicePublicInputs(t)

	pi, err := batch.BuildPublicInputs(upd, com)
	if err != nil {
		t.Fatalf("BuildPublicInputs: %v", err)
	}

	want := []string{
		upd.OldStateRoot,
		upd.NewStateRoot,
		com.DepositsRoot,
		com.WithdrawalsRoot,
		com.NullifiersRoot,
		com.WithdrawOutputsRoot,
	}
	if len(pi) != len(want) {
		t.Fatalf("len mismatch: got=%d want=%d", len(pi), len(want))
	}
	for i := range want {
		if pi[i] != want[i] {
			t.Fatalf("publicInputs[%d] = %q, want %q (prover-client layout)", i, pi[i], want[i])
		}
	}
}
