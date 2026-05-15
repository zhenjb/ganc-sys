package state

import (
	"errors"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

const aliceDest = "cosmos1alice"

func seededState(t *testing.T, amount string) *LocalState {
	t.Helper()
	ls := NewLocalState()
	if _, err := ls.ApplyDeposit(types.DepositRecord{
		DepositID: "dep-1",
		Owner:     aliceAddr,
		Denom:     "uusdc",
		Amount:    amount,
	}); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	return ls
}

func TestBuildWithdrawRequest_Canonical(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)

	req, err := b.Build(WithdrawIntent{
		Owner:       aliceAddr,
		Denom:       "uusdc",
		Amount:      "40",
		Destination: aliceDest,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if req.WithdrawID != "wd-1" {
		t.Fatalf("withdrawId: want wd-1, got %s", req.WithdrawID)
	}
	if req.Owner != aliceAddr || req.Denom != "uusdc" || req.Destination != aliceDest {
		t.Fatalf("identity fields mismatched: %+v", req)
	}
	if req.Amount != "40" {
		t.Fatalf("amount: want 40, got %s", req.Amount)
	}
	if req.Nonce != "1" {
		t.Fatalf("nonce must be Account.Nonce+1 (post-increment): want 1, got %s", req.Nonce)
	}
	if req.Signature != "" {
		t.Fatalf("signature must be left empty for builder; got %q", req.Signature)
	}

	// STATE-04 must NOT mutate the local state.
	acc := ls.Account(aliceAddr, "uusdc")
	if acc.Balance != "100" {
		t.Fatalf("balance must not change in STATE-04: want 100, got %s", acc.Balance)
	}
	if acc.Nonce != "0" {
		t.Fatalf("nonce must not change in STATE-04: want 0, got %s", acc.Nonce)
	}
}

func TestBuildWithdrawRequest_SequentialIDs(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)

	r1, err := b.Build(WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Amount: "10", Destination: aliceDest})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := b.Build(WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Amount: "20", Destination: aliceDest})
	if err != nil {
		t.Fatal(err)
	}
	if r1.WithdrawID != "wd-1" || r2.WithdrawID != "wd-2" {
		t.Fatalf("ids must be sequential: got %s, %s", r1.WithdrawID, r2.WithdrawID)
	}
	// Two builds without STATE-05 in between → both pin the same next-nonce.
	// Documented behavior; STATE-05 sequencing is the caller's responsibility.
	if r1.Nonce != "1" || r2.Nonce != "1" {
		t.Fatalf("both should pin Account.Nonce+1 since STATE-05 hasn't run: got %s, %s", r1.Nonce, r2.Nonce)
	}
}

func TestBuildWithdrawRequest_InvalidIntent(t *testing.T) {
	ls := seededState(t, "100")
	cases := []struct {
		name   string
		intent WithdrawIntent
	}{
		{"empty owner", WithdrawIntent{Denom: "uusdc", Amount: "40", Destination: aliceDest}},
		{"empty denom", WithdrawIntent{Owner: aliceAddr, Amount: "40", Destination: aliceDest}},
		{"empty destination", WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Amount: "40"}},
		{"empty amount", WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Destination: aliceDest}},
		{"zero amount", WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Amount: "0", Destination: aliceDest}},
		{"negative amount", WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Amount: "-5", Destination: aliceDest}},
		{"non-numeric amount", WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Amount: "abc", Destination: aliceDest}},
		{"whitespace owner", WithdrawIntent{Owner: "   ", Denom: "uusdc", Amount: "40", Destination: aliceDest}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := NewWithdrawRequestBuilder(ls)
			_, err := b.Build(c.intent)
			if !errors.Is(err, ErrInvalidWithdrawIntent) {
				t.Fatalf("want ErrInvalidWithdrawIntent, got %v", err)
			}
		})
	}
}

func TestBuildWithdrawRequest_InsufficientBalance(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)

	_, err := b.Build(WithdrawIntent{
		Owner:       aliceAddr,
		Denom:       "uusdc",
		Amount:      "200",
		Destination: aliceDest,
	})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("want ErrInsufficientBalance, got %v", err)
	}
}

// Boundary: amount exactly equal to balance must succeed — full-balance
// withdrawal is a legitimate user action, not a failure case.
func TestBuildWithdrawRequest_ExactBalanceSucceeds(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)

	req, err := b.Build(WithdrawIntent{
		Owner:       aliceAddr,
		Denom:       "uusdc",
		Amount:      "100",
		Destination: aliceDest,
	})
	if err != nil {
		t.Fatalf("withdraw of exact balance must succeed: %v", err)
	}
	if req.Amount != "100" || req.Nonce != "1" {
		t.Fatalf("unexpected request: %+v", req)
	}
}

// Off-by-one: amount = balance + 1 must fail with ErrInsufficientBalance.
// Guards against a regression like `bal.Cmp(amount) <= 0` (which would also
// reject the exact-balance case above) or `<`-vs-`<=` swap.
func TestBuildWithdrawRequest_OneOverBalanceFails(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)

	_, err := b.Build(WithdrawIntent{
		Owner:       aliceAddr,
		Denom:       "uusdc",
		Amount:      "101",
		Destination: aliceDest,
	})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("amount = balance+1 must trip ErrInsufficientBalance, got %v", err)
	}
}

// A failed Build must not advance the withdrawId counter — the next valid
// Build should still return wd-1, not wd-2. This is the "no side effects on
// failure" property that P4's retry-on-422 flow relies on.
func TestBuildWithdrawRequest_FailedBuildDoesNotConsumeID(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)

	if _, err := b.Build(WithdrawIntent{
		Owner: aliceAddr, Denom: "uusdc", Amount: "999", Destination: aliceDest,
	}); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("setup: want ErrInsufficientBalance, got %v", err)
	}
	if b.Seq() != 0 {
		t.Fatalf("seq must not advance on failure: got %d", b.Seq())
	}

	req, err := b.Build(WithdrawIntent{
		Owner: aliceAddr, Denom: "uusdc", Amount: "40", Destination: aliceDest,
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if req.WithdrawID != "wd-1" {
		t.Fatalf("first successful build after failure must be wd-1, got %s", req.WithdrawID)
	}
}

// A failed Build must not leak into LocalState — balance, nonce, and root
// must be byte-identical pre/post.
func TestBuildWithdrawRequest_FailedBuildLeavesStateClean(t *testing.T) {
	ls := seededState(t, "100")
	rootBefore := ls.Root()
	accBefore := ls.Account(aliceAddr, "uusdc")

	b := NewWithdrawRequestBuilder(ls)
	if _, err := b.Build(WithdrawIntent{
		Owner: aliceAddr, Denom: "uusdc", Amount: "200", Destination: aliceDest,
	}); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("setup: want ErrInsufficientBalance, got %v", err)
	}

	if ls.Root() != rootBefore {
		t.Fatalf("root changed after failed Build: before=%s after=%s", rootBefore, ls.Root())
	}
	accAfter := ls.Account(aliceAddr, "uusdc")
	if accAfter.Balance != accBefore.Balance {
		t.Fatalf("balance changed after failed Build: before=%s after=%s", accBefore.Balance, accAfter.Balance)
	}
	if accAfter.Nonce != accBefore.Nonce {
		t.Fatalf("nonce changed after failed Build: before=%s after=%s", accBefore.Nonce, accAfter.Nonce)
	}
}

// Cross-denom isolation: balance for uusdc must not satisfy a withdraw in
// a different denom. Prevents a class of bugs where a builder or storage
// layer collapses on owner alone.
func TestBuildWithdrawRequest_InsufficientBalance_WrongDenom(t *testing.T) {
	ls := seededState(t, "100") // funds Alice in uusdc only
	b := NewWithdrawRequestBuilder(ls)

	_, err := b.Build(WithdrawIntent{
		Owner:       aliceAddr,
		Denom:       "uatom",
		Amount:      "1",
		Destination: aliceDest,
	})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("withdraw in unfunded denom must fail: got %v", err)
	}
}

// Very large amount (beyond int64) must still be compared correctly via
// big.Int and rejected when balance is small.
func TestBuildWithdrawRequest_HugeAmountAgainstSmallBalance(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)

	huge := "99999999999999999999999999999999" // > int64 max
	_, err := b.Build(WithdrawIntent{
		Owner: aliceAddr, Denom: "uusdc", Amount: huge, Destination: aliceDest,
	})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("huge amount must be rejected via big.Int compare, got %v", err)
	}
}

func TestBuildWithdrawRequest_UnknownAccountIsZeroBalance(t *testing.T) {
	ls := NewLocalState() // no deposit
	b := NewWithdrawRequestBuilder(ls)

	_, err := b.Build(WithdrawIntent{
		Owner:       "cosmos1nobody",
		Denom:       "uusdc",
		Amount:      "1",
		Destination: aliceDest,
	})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("unknown account should be treated as zero balance → ErrInsufficientBalance, got %v", err)
	}
}

func TestBuildWithdrawRequest_TrimsWhitespace(t *testing.T) {
	ls := seededState(t, "100")
	b := NewWithdrawRequestBuilder(ls)

	req, err := b.Build(WithdrawIntent{
		Owner:       "  " + aliceAddr + "  ",
		Denom:       " uusdc ",
		Amount:      " 40 ",
		Destination: " " + aliceDest + " ",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if req.Owner != aliceAddr || req.Denom != "uusdc" || req.Destination != aliceDest || req.Amount != "40" {
		t.Fatalf("whitespace not trimmed: %+v", req)
	}
}

func TestBuildWithdrawRequest_DoesNotAffectRoot(t *testing.T) {
	ls := seededState(t, "100")
	rootBefore := ls.Root()
	b := NewWithdrawRequestBuilder(ls)
	if _, err := b.Build(WithdrawIntent{Owner: aliceAddr, Denom: "uusdc", Amount: "40", Destination: aliceDest}); err != nil {
		t.Fatal(err)
	}
	if ls.Root() != rootBefore {
		t.Fatalf("root must not change in STATE-04: before=%s after=%s", rootBefore, ls.Root())
	}
}
