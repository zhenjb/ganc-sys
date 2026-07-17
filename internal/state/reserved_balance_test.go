package state

import (
	"errors"
	"math/big"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// avail+reserved must be conserved by Reserve/Release. Helper reads both buckets
// of an account off the manager and returns their sum.
func totalHeld(t *testing.T, m *OffchainStateManager, owner, denom string) *big.Int {
	t.Helper()
	acc := m.Account(owner, denom)
	avail, ok := new(big.Int).SetString(acc.Balance, 10)
	if !ok {
		t.Fatalf("bad balance %q", acc.Balance)
	}
	reserved := big.NewInt(0)
	if acc.Reserved != "" {
		reserved, ok = new(big.Int).SetString(acc.Reserved, 10)
		if !ok {
			t.Fatalf("bad reserved %q", acc.Reserved)
		}
	}
	return new(big.Int).Add(avail, reserved)
}

func seedAccount(t *testing.T, m *OffchainStateManager, owner, denom, amount string) {
	t.Helper()
	if _, err := m.ApplyDeposit(types.DepositRecord{
		DepositID: "seed-" + owner + "-" + denom + "-" + amount,
		Owner:     owner,
		Denom:     denom,
		Amount:    amount,
	}); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
}

// --- AccountState primitives -------------------------------------------------

func TestAccountStateReserveReleaseConserveTotal(t *testing.T) {
	as := NewAccountState()
	if _, err := as.Credit("alice", "uusdc", "100"); err != nil {
		t.Fatalf("credit: %v", err)
	}

	acc, err := as.Reserve("alice", "uusdc", "40")
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if acc.Balance != "60" || acc.Reserved != "40" {
		t.Fatalf("after reserve: got balance=%q reserved=%q, want 60/40", acc.Balance, acc.Reserved)
	}

	acc, err = as.Release("alice", "uusdc", "25")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if acc.Balance != "85" || acc.Reserved != "15" {
		t.Fatalf("after release: got balance=%q reserved=%q, want 85/15", acc.Balance, acc.Reserved)
	}

	// Release the rest — reserved must normalize to "" (legacy-root compatible).
	acc, err = as.Release("alice", "uusdc", "15")
	if err != nil {
		t.Fatalf("release rest: %v", err)
	}
	if acc.Balance != "100" || acc.Reserved != "" {
		t.Fatalf("after full release: got balance=%q reserved=%q, want 100/\"\"", acc.Balance, acc.Reserved)
	}
}

func TestAccountStateReserveRejectsOverAvailable(t *testing.T) {
	as := NewAccountState()
	if _, err := as.Credit("alice", "uusdc", "30"); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if _, err := as.Reserve("alice", "uusdc", "31"); !errors.Is(err, ErrInsufficientAvailable) {
		t.Fatalf("expected ErrInsufficientAvailable, got %v", err)
	}
	// State must be untouched after a rejected reserve.
	acc := as.GetOrZero("alice", "uusdc")
	if acc.Balance != "30" || acc.Reserved != "" {
		t.Fatalf("state mutated by rejected reserve: %+v", acc)
	}
}

func TestAccountStateReleaseAndConsumeRejectOverReserved(t *testing.T) {
	as := NewAccountState()
	if _, err := as.Credit("alice", "uusdc", "100"); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if _, err := as.Reserve("alice", "uusdc", "40"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := as.Release("alice", "uusdc", "41"); !errors.Is(err, ErrInsufficientReserved) {
		t.Fatalf("release over reserved: expected ErrInsufficientReserved, got %v", err)
	}
	if _, err := as.Consume("alice", "uusdc", "41"); !errors.Is(err, ErrInsufficientReserved) {
		t.Fatalf("consume over reserved: expected ErrInsufficientReserved, got %v", err)
	}
}

// Consume is the fill path: it removes from reserved and does NOT credit
// available, so the account total drops by the consumed amount.
func TestAccountStateConsumeReducesTotal(t *testing.T) {
	as := NewAccountState()
	if _, err := as.Credit("alice", "uusdc", "100"); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if _, err := as.Reserve("alice", "uusdc", "40"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	acc, err := as.Consume("alice", "uusdc", "30")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if acc.Balance != "60" || acc.Reserved != "10" {
		t.Fatalf("after consume: got balance=%q reserved=%q, want 60/10", acc.Balance, acc.Reserved)
	}
}

// --- Buy/sell denom binding --------------------------------------------------

func TestReserveDenomForSide(t *testing.T) {
	mkt := types.Market{Market: "ATOM/USDC", BaseDenom: "uatom", QuoteDenom: "uusdc", Status: types.MarketActive}

	buyDenom, err := ReserveDenomForSide(mkt, types.SideBuy)
	if err != nil || buyDenom != "uusdc" {
		t.Fatalf("buy should lock quote (uusdc), got %q err=%v", buyDenom, err)
	}
	sellDenom, err := ReserveDenomForSide(mkt, types.SideSell)
	if err != nil || sellDenom != "uatom" {
		t.Fatalf("sell should lock base (uatom), got %q err=%v", sellDenom, err)
	}
	if _, err := ReserveDenomForSide(mkt, types.OrderSide("hold")); err == nil {
		t.Fatal("unknown side should error")
	}
}

// Full buy/sell reservation through the manager: a buyer locks quote, a seller
// locks base; both conserve their account total.
func TestManagerReserveBuyLocksQuoteSellLocksBase(t *testing.T) {
	m := NewOffchainStateManager()
	mkt := types.Market{Market: "ATOM/USDC", BaseDenom: "uatom", QuoteDenom: "uusdc", Status: types.MarketActive}

	seedAccount(t, m, "buyer", "uusdc", "1000")
	seedAccount(t, m, "seller", "uatom", "50")

	buyDenom, _ := ReserveDenomForSide(mkt, types.SideBuy)
	if _, err := m.Reserve("buyer", buyDenom, "210", "0xbuy"); err != nil {
		t.Fatalf("buyer reserve: %v", err)
	}
	sellDenom, _ := ReserveDenomForSide(mkt, types.SideSell)
	if _, err := m.Reserve("seller", sellDenom, "20", "0xsell"); err != nil {
		t.Fatalf("seller reserve: %v", err)
	}

	buyer := m.Account("buyer", "uusdc")
	if buyer.Balance != "790" || buyer.Reserved != "210" {
		t.Fatalf("buyer: got %q/%q want 790/210", buyer.Balance, buyer.Reserved)
	}
	seller := m.Account("seller", "uatom")
	if seller.Balance != "30" || seller.Reserved != "20" {
		t.Fatalf("seller: got %q/%q want 30/20", seller.Balance, seller.Reserved)
	}
	if got := totalHeld(t, m, "buyer", "uusdc"); got.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("buyer total not conserved: %s", got)
	}
	if got := totalHeld(t, m, "seller", "uatom"); got.Cmp(big.NewInt(50)) != 0 {
		t.Fatalf("seller total not conserved: %s", got)
	}
}

// --- Root binding ------------------------------------------------------------

// INT-2SEQ / Phương án A: the settled state root must NOT change when reserving
// or releasing collateral. Reserved funds are off-chain bookkeeping (a resting
// order that never settles on-chain on its own), so the chain-committed root
// binds only the settled TOTAL (available + reserved) — which Reserve/Release
// conserve. This is what keeps the off-chain root equal to the chain's current
// root while orders are open (the fix for the two-sequencer oldStateRoot
// mismatch). A fill (Consume) moves the total and MUST change the root — see
// TestConsumeChangesSettledRoot.
func TestReserveDoesNotChangeSettledRoot(t *testing.T) {
	m := NewOffchainStateManager()
	seedAccount(t, m, "alice", "uusdc", "100")

	rootBefore := m.Root()

	if _, err := m.Reserve("alice", "uusdc", "40", "0xorder"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if m.Root() != rootBefore {
		t.Fatalf("settled root changed on reserve: got %q want %q (reserved must fold into the settled total)", m.Root(), rootBefore)
	}

	if _, err := m.ReleaseOrder("0xorder"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if m.Root() != rootBefore {
		t.Fatalf("root not restored after full release: got %q want %q", m.Root(), rootBefore)
	}
}

// A fill draws locked collateral out of the account (Consume reduces reserved
// without crediting available), so the settled total drops and the root MUST
// advance — the counterpart to TestReserveDoesNotChangeSettledRoot. Together they
// pin Phương án A: reserve/release are root-invariant, settlement is not.
func TestConsumeChangesSettledRoot(t *testing.T) {
	m := NewOffchainStateManager()
	seedAccount(t, m, "alice", "uusdc", "100")

	if _, err := m.Reserve("alice", "uusdc", "40", "0xorder"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	rootReserved := m.Root()

	if _, err := m.ConsumeOrder("0xorder", "40"); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if m.Root() == rootReserved {
		t.Fatal("settled root did not change after a fill consumed locked collateral")
	}
}

// --- Order-keyed registry ----------------------------------------------------

func TestReserveRejectsDuplicateOrderHash(t *testing.T) {
	m := NewOffchainStateManager()
	seedAccount(t, m, "alice", "uusdc", "100")
	if _, err := m.Reserve("alice", "uusdc", "40", "0xorder"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := m.Reserve("alice", "uusdc", "10", "0xorder"); !errors.Is(err, ErrReservationExists) {
		t.Fatalf("expected ErrReservationExists, got %v", err)
	}
}

func TestReleaseAndConsumeUnknownOrderHash(t *testing.T) {
	m := NewOffchainStateManager()
	if _, err := m.ReleaseOrder("0xnope"); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("release unknown: expected ErrReservationNotFound, got %v", err)
	}
	if _, err := m.ConsumeOrder("0xnope", "1"); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("consume unknown: expected ErrReservationNotFound, got %v", err)
	}
}

// Partial fill: consuming part of a reservation leaves the rest locked and the
// reservation live; consuming the remainder drops it.
func TestConsumeOrderPartialThenFull(t *testing.T) {
	m := NewOffchainStateManager()
	seedAccount(t, m, "alice", "uusdc", "100")
	if _, err := m.Reserve("alice", "uusdc", "40", "0xorder"); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	if _, err := m.ConsumeOrder("0xorder", "15"); err != nil {
		t.Fatalf("partial consume: %v", err)
	}
	r, ok := m.Reservation("0xorder")
	if !ok || r.Amount != "25" {
		t.Fatalf("after partial consume: reservation=%+v ok=%v, want amount 25", r, ok)
	}
	acc := m.Account("alice", "uusdc")
	if acc.Balance != "60" || acc.Reserved != "25" {
		t.Fatalf("after partial consume: got %q/%q want 60/25", acc.Balance, acc.Reserved)
	}

	// Over-consume the remaining 25 must fail without mutating.
	if _, err := m.ConsumeOrder("0xorder", "26"); !errors.Is(err, ErrInsufficientReserved) {
		t.Fatalf("over-consume: expected ErrInsufficientReserved, got %v", err)
	}

	if _, err := m.ConsumeOrder("0xorder", "25"); err != nil {
		t.Fatalf("final consume: %v", err)
	}
	if _, ok := m.Reservation("0xorder"); ok {
		t.Fatal("reservation should be gone after full consume")
	}
	acc = m.Account("alice", "uusdc")
	if acc.Balance != "60" || acc.Reserved != "" {
		t.Fatalf("after full consume: got %q/%q want 60/\"\"", acc.Balance, acc.Reserved)
	}
}

// --- Rollback ----------------------------------------------------------------

// Rollback must restore BOTH the reserved buckets and the reservation registry
// (STATE-T10 depends on this: a rejected trade batch reopens locked collateral).
func TestRollbackRestoresReservations(t *testing.T) {
	m := NewOffchainStateManager()
	seedAccount(t, m, "alice", "uusdc", "100")

	snap := m.Snapshot() // pre-reserve
	if _, err := m.Reserve("alice", "uusdc", "40", "0xorder"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, ok := m.Reservation("0xorder"); !ok {
		t.Fatal("reservation should exist before rollback")
	}

	m.Rollback(snap)

	if _, ok := m.Reservation("0xorder"); ok {
		t.Fatal("reservation should be gone after rollback")
	}
	acc := m.Account("alice", "uusdc")
	if acc.Balance != "100" || acc.Reserved != "" {
		t.Fatalf("after rollback: got %q/%q want 100/\"\"", acc.Balance, acc.Reserved)
	}
	if m.Root() != snap.Root() {
		t.Fatalf("root not restored after rollback: got %q want %q", m.Root(), snap.Root())
	}
}

// A snapshot taken WHILE a reservation is live must carry it, and mutating the
// manager afterwards must not change the snapshot (deep-copy).
func TestSnapshotCapturesReservationsImmutably(t *testing.T) {
	m := NewOffchainStateManager()
	seedAccount(t, m, "alice", "uusdc", "100")
	if _, err := m.Reserve("alice", "uusdc", "40", "0xorder"); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	snap := m.Snapshot()
	if r, ok := snap.Reservation("0xorder"); !ok || r.Amount != "40" {
		t.Fatalf("snapshot missing reservation: %+v ok=%v", r, ok)
	}

	// Consume after snapshot — snapshot must be unaffected.
	if _, err := m.ConsumeOrder("0xorder", "40"); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if r, ok := snap.Reservation("0xorder"); !ok || r.Amount != "40" {
		t.Fatalf("snapshot reservation mutated: %+v ok=%v", r, ok)
	}
	if len(snap.Reservations()) != 1 {
		t.Fatalf("snapshot should list 1 reservation, got %d", len(snap.Reservations()))
	}
}
