package state

import (
	"errors"
	"sync"
	"testing"

	"github.com/zhenjb/ganc-sys/pkg/types"
)

// Test fixtures riêng cho manager — không phụ thuộc helper của
// local_state_test (giữ test độc lập, dễ chạy đơn lẻ).
const (
	mgrAlice  = "cosmos1alice"
	mgrBob    = "cosmos1bob"
	mgrDenom  = "uusdc"
	mgrSecret = "mock-user-secret"
)

func mgrAliceDeposit(id, amount string) types.DepositRecord {
	return types.DepositRecord{
		DepositID:     id,
		Owner:         mgrAlice,
		Denom:         mgrDenom,
		Amount:        amount,
		Processed:     false,
		CreatedHeight: 42,
	}
}

func mgrAliceWithdraw(t *testing.T, id, amount, nonce string) (types.WithdrawRequest, string) {
	t.Helper()
	nullifier, err := NullifierFor(mgrSecret, nonce)
	if err != nil {
		t.Fatalf("NullifierFor: %v", err)
	}
	return types.WithdrawRequest{
		WithdrawID:  id,
		Owner:       mgrAlice,
		Denom:       mgrDenom,
		Amount:      amount,
		Destination: mgrAlice,
		Nonce:       nonce,
	}, nullifier
}

// --- Smoke / API contract -------------------------------------------------

func TestOffchainStateManager_NewIsEmptyDeterministic(t *testing.T) {
	a := NewOffchainStateManager()
	b := NewOffchainStateManager()
	if a.Root() != b.Root() {
		t.Fatalf("initial root not deterministic: a=%s b=%s", a.Root(), b.Root())
	}
	if a.Root() == "" || a.Root()[:2] != "0x" {
		t.Fatalf("root must be 0x-prefixed hex, got %q", a.Root())
	}
	if a.Generation() != 0 {
		t.Fatalf("fresh manager gen = %d, want 0", a.Generation())
	}
	if a.IsDepositApplied("dep-1") {
		t.Fatalf("fresh manager should not have any deposit applied")
	}
}

func TestOffchainStateManager_RootMatchesFreshLocalState(t *testing.T) {
	// Manager Root() bằng LocalState empty Root() => khớp với genesis
	// state hôm chain mới deploy (ONCHAIN-03).
	m := NewOffchainStateManager()
	ls := NewLocalState()
	if m.Root() != ls.Root() {
		t.Fatalf("manager root %s != fresh LocalState root %s", m.Root(), ls.Root())
	}
}

// --- ApplyDeposit ---------------------------------------------------------

func TestOffchainStateManager_ApplyDeposit_CreditsBalanceAndAdvancesRoot(t *testing.T) {
	m := NewOffchainStateManager()
	rootBefore := m.Root()

	rootAfter, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100"))
	if err != nil {
		t.Fatalf("ApplyDeposit: %v", err)
	}
	if rootAfter == rootBefore {
		t.Fatalf("root must advance after deposit, got same: %s", rootAfter)
	}
	if rootAfter != m.Root() {
		t.Fatalf("returned root must equal Root(): %s vs %s", rootAfter, m.Root())
	}
	if got := m.Account(mgrAlice, mgrDenom); got.Balance != "100" {
		t.Fatalf("balance = %s, want 100", got.Balance)
	}
	if !m.IsDepositApplied("dep-1") {
		t.Fatalf("deposit dep-1 should be marked applied")
	}
	if m.Generation() != 1 {
		t.Fatalf("generation = %d, want 1", m.Generation())
	}
}

func TestOffchainStateManager_ApplyDeposit_IdempotentByDepositID(t *testing.T) {
	m := NewOffchainStateManager()
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("first ApplyDeposit: %v", err)
	}
	_, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100"))
	if !errors.Is(err, ErrDepositAlreadyApplied) {
		t.Fatalf("replay ApplyDeposit err = %v, want ErrDepositAlreadyApplied", err)
	}
	// Balance vẫn = 100, không double-credit.
	if got := m.Account(mgrAlice, mgrDenom); got.Balance != "100" {
		t.Fatalf("balance after replay = %s, want 100 (no double credit)", got.Balance)
	}
	if m.Generation() != 1 {
		t.Fatalf("generation must not advance on idempotent reject, got %d", m.Generation())
	}
}

func TestOffchainStateManager_ApplyDeposit_InvalidRecord(t *testing.T) {
	m := NewOffchainStateManager()
	_, err := m.ApplyDeposit(types.DepositRecord{
		DepositID: "",
		Owner:     mgrAlice,
		Denom:     mgrDenom,
		Amount:    "100",
	})
	if !errors.Is(err, ErrInvalidDepositRecord) {
		t.Fatalf("err = %v, want ErrInvalidDepositRecord", err)
	}
	if m.Generation() != 0 {
		t.Fatalf("failed apply must not advance generation, got %d", m.Generation())
	}
}

// --- ApplyWithdrawRequest -------------------------------------------------

func TestOffchainStateManager_ApplyWithdrawRequest_DebitsAndAdvancesRoot(t *testing.T) {
	m := NewOffchainStateManager()
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	rootAfterDeposit := m.Root()

	req, nullifier := mgrAliceWithdraw(t, "wd-1", "40", "1")
	rootAfterWithdraw, err := m.ApplyWithdrawRequest(req, nullifier)
	if err != nil {
		t.Fatalf("ApplyWithdrawRequest: %v", err)
	}
	if rootAfterWithdraw == rootAfterDeposit {
		t.Fatalf("root must advance after withdraw, got same: %s", rootAfterWithdraw)
	}
	if got := m.Account(mgrAlice, mgrDenom); got.Balance != "60" || got.Nonce != "1" {
		t.Fatalf("after withdraw: balance=%s nonce=%s, want 60/1", got.Balance, got.Nonce)
	}
	if !m.IsNullifierApplied(nullifier) {
		t.Fatalf("nullifier should be consumed")
	}
}

func TestOffchainStateManager_ApplyWithdrawRequest_RejectsInsufficientBalance(t *testing.T) {
	m := NewOffchainStateManager()
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	preRoot := m.Root()
	preGen := m.Generation()

	req, nullifier := mgrAliceWithdraw(t, "wd-1", "500", "1")
	_, err := m.ApplyWithdrawRequest(req, nullifier)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("err = %v, want ErrInsufficientBalance", err)
	}
	// State KHÔNG bị mutate trên lỗi.
	if m.Root() != preRoot {
		t.Fatalf("root must not advance on failed withdraw, got %s", m.Root())
	}
	if m.Generation() != preGen {
		t.Fatalf("generation must not advance on failed withdraw, got %d", m.Generation())
	}
	if m.IsNullifierApplied(nullifier) {
		t.Fatalf("nullifier must NOT be consumed on rejected withdraw")
	}
}

func TestOffchainStateManager_ApplyWithdrawRequest_RejectsReplayedNullifier(t *testing.T) {
	m := NewOffchainStateManager()
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	req, nullifier := mgrAliceWithdraw(t, "wd-1", "40", "1")
	if _, err := m.ApplyWithdrawRequest(req, nullifier); err != nil {
		t.Fatalf("first ApplyWithdrawRequest: %v", err)
	}

	// Replay cùng nullifier → reject.
	_, err := m.ApplyWithdrawRequest(req, nullifier)
	if !errors.Is(err, ErrWithdrawAlreadyApplied) {
		t.Fatalf("replay err = %v, want ErrWithdrawAlreadyApplied", err)
	}
}

func TestOffchainStateManager_ApplyWithdrawRequest_RejectsNonceMismatch(t *testing.T) {
	m := NewOffchainStateManager()
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	// Withdraw với nonce 2 trong khi account đang ở nonce 0 → mismatch.
	req, nullifier := mgrAliceWithdraw(t, "wd-1", "40", "2")
	_, err := m.ApplyWithdrawRequest(req, nullifier)
	if !errors.Is(err, ErrNonceMismatch) {
		t.Fatalf("err = %v, want ErrNonceMismatch", err)
	}
}

// --- Snapshot -------------------------------------------------------------

func TestOffchainStateManager_Snapshot_ReflectsCurrentState(t *testing.T) {
	m := NewOffchainStateManager()
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	req, nullifier := mgrAliceWithdraw(t, "wd-1", "40", "1")
	if _, err := m.ApplyWithdrawRequest(req, nullifier); err != nil {
		t.Fatalf("seed withdraw: %v", err)
	}

	snap := m.Snapshot()
	if snap.Root() != m.Root() {
		t.Fatalf("snapshot root %s != manager root %s", snap.Root(), m.Root())
	}
	if acc := snap.Account(mgrAlice, mgrDenom); acc.Balance != "60" || acc.Nonce != "1" {
		t.Fatalf("snapshot account = %+v, want balance=60 nonce=1", acc)
	}
	if !snap.IsDepositApplied("dep-1") {
		t.Fatalf("snapshot should mark dep-1 applied")
	}
	if !snap.IsNullifierApplied(nullifier) {
		t.Fatalf("snapshot should mark nullifier applied")
	}
	if got := snap.Accounts(); len(got) != 1 || got[0].Owner != mgrAlice {
		t.Fatalf("snapshot.Accounts() = %+v, want 1 entry for alice", got)
	}
}

func TestOffchainStateManager_Snapshot_ImmutableAfterFurtherMutation(t *testing.T) {
	// Snapshot phải là deep-copy: mutate manager sau snapshot KHÔNG
	// được ảnh hưởng snapshot đã trả ra.
	m := NewOffchainStateManager()
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	snap := m.Snapshot()
	rootAtSnapshot := snap.Root()

	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-2", "50")); err != nil {
		t.Fatalf("post-snapshot deposit: %v", err)
	}

	if snap.Root() != rootAtSnapshot {
		t.Fatalf("snapshot root mutated after manager update: %s != %s", snap.Root(), rootAtSnapshot)
	}
	if acc := snap.Account(mgrAlice, mgrDenom); acc.Balance != "100" {
		t.Fatalf("snapshot account balance mutated: %s, want 100", acc.Balance)
	}
	if snap.IsDepositApplied("dep-2") {
		t.Fatalf("snapshot must NOT see post-snapshot deposit dep-2")
	}
}

func TestOffchainStateManager_Snapshot_ZeroValueSafe(t *testing.T) {
	// Snapshot{} (chưa init qua manager) vẫn dùng được — trả về default
	// account cho mọi (owner, denom).
	var s Snapshot
	if s.Root() != "" {
		t.Errorf("zero snapshot root = %q, want empty", s.Root())
	}
	if acc := s.Account(mgrAlice, mgrDenom); acc.Balance != "0" || acc.Nonce != "0" {
		t.Errorf("zero snapshot account = %+v, want balance=0 nonce=0", acc)
	}
	if s.IsDepositApplied("anything") || s.IsNullifierApplied("anything") {
		t.Errorf("zero snapshot must not report any applied id")
	}
}

// --- Rollback -------------------------------------------------------------

func TestOffchainStateManager_Rollback_RestoresPreBatchState(t *testing.T) {
	// Use case INT-09: chụp snapshot trước batch, submit fail → rollback.
	m := NewOffchainStateManager()
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	preBatchSnap := m.Snapshot()
	preBatchRoot := preBatchSnap.Root()

	// Apply withdraw (manager state advance).
	req, nullifier := mgrAliceWithdraw(t, "wd-1", "40", "1")
	if _, err := m.ApplyWithdrawRequest(req, nullifier); err != nil {
		t.Fatalf("apply withdraw: %v", err)
	}
	if m.Root() == preBatchRoot {
		t.Fatalf("root should differ after withdraw, got same %s", m.Root())
	}

	// Submit fail → rollback.
	m.Rollback(preBatchSnap)

	if m.Root() != preBatchRoot {
		t.Fatalf("after rollback root = %s, want %s", m.Root(), preBatchRoot)
	}
	if acc := m.Account(mgrAlice, mgrDenom); acc.Balance != "100" || acc.Nonce != "0" {
		t.Fatalf("after rollback account = %+v, want balance=100 nonce=0", acc)
	}
	if m.IsNullifierApplied(nullifier) {
		t.Fatalf("nullifier must be discarded after rollback")
	}
	// Deposit dep-1 đã có TRƯỚC snapshot → vẫn còn.
	if !m.IsDepositApplied("dep-1") {
		t.Fatalf("pre-snapshot deposit must persist through rollback")
	}
}

func TestOffchainStateManager_Rollback_SnapshotReusableAfterRollback(t *testing.T) {
	// Rollback dùng deep-copy → snapshot không bị consume, có thể
	// rollback lần 2 (idempotent rollback).
	m := NewOffchainStateManager()
	snapEmpty := m.Snapshot()

	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	m.Rollback(snapEmpty)
	if m.Generation() == 0 {
		t.Fatalf("generation should advance after rollback")
	}
	if got := m.Account(mgrAlice, mgrDenom); got.Balance != "0" {
		t.Fatalf("after first rollback balance = %s, want 0", got.Balance)
	}

	// Apply lần 2 và rollback dùng CÙNG snapshot → vẫn về 0.
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("second deposit: %v", err)
	}
	m.Rollback(snapEmpty)
	if got := m.Account(mgrAlice, mgrDenom); got.Balance != "0" {
		t.Fatalf("after second rollback balance = %s, want 0", got.Balance)
	}
}

func TestOffchainStateManager_Rollback_AdvancesGeneration(t *testing.T) {
	m := NewOffchainStateManager()
	snap := m.Snapshot()
	g0 := m.Generation()
	m.Rollback(snap)
	if m.Generation() != g0+1 {
		t.Fatalf("rollback must advance generation: got %d, want %d", m.Generation(), g0+1)
	}
}

// --- NewLocalStateFromSnapshot -------------------------------------------

func TestNewLocalStateFromSnapshot_RoundTrip(t *testing.T) {
	// Snapshot đầy đủ → LocalState mới có cùng balance/root/deposit/nullifier.
	m := NewOffchainStateManager()
	if _, err := m.ApplyDeposit(mgrAliceDeposit("dep-1", "100")); err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
	req, nullifier := mgrAliceWithdraw(t, "wd-1", "40", "1")
	if _, err := m.ApplyWithdrawRequest(req, nullifier); err != nil {
		t.Fatalf("seed withdraw: %v", err)
	}
	snap := m.Snapshot()

	ls := NewLocalStateFromSnapshot(snap)
	if ls.Root() != snap.Root() {
		t.Fatalf("seeded LocalState root = %s, want %s", ls.Root(), snap.Root())
	}
	if acc := ls.Account(mgrAlice, mgrDenom); acc.Balance != "60" || acc.Nonce != "1" {
		t.Fatalf("seeded LocalState account = %+v, want balance=60 nonce=1", acc)
	}
	if !ls.IsDepositApplied("dep-1") {
		t.Fatalf("seeded LocalState should mirror snapshot deposits")
	}
	if !ls.IsNullifierApplied(nullifier) {
		t.Fatalf("seeded LocalState should mirror snapshot nullifiers")
	}

	// Mutate ls → snapshot không đổi.
	if _, err := ls.ApplyDeposit(mgrAliceDeposit("dep-2", "50")); err != nil {
		t.Fatalf("post-seed deposit: %v", err)
	}
	if snap.IsDepositApplied("dep-2") {
		t.Fatalf("snapshot must not see ls-only mutation dep-2")
	}
}

func TestNewLocalStateFromSnapshot_EmptySnapshot(t *testing.T) {
	// Snapshot zero-value → ls behave như fresh.
	ls := NewLocalStateFromSnapshot(Snapshot{})
	if ls.Root() != NewLocalState().Root() {
		t.Fatalf("empty-snapshot LocalState root != fresh LocalState root")
	}
}

// --- Concurrency ----------------------------------------------------------

func TestOffchainStateManager_ConcurrentApplyDeposit(t *testing.T) {
	m := NewOffchainStateManager()
	const n = 32

	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			d := mgrAliceDeposit(depID(i), "10")
			if _, err := m.ApplyDeposit(d); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent ApplyDeposit error: %v", err)
	}

	// Tổng balance = 32 * 10 = 320.
	if got := m.Account(mgrAlice, mgrDenom).Balance; got != "320" {
		t.Errorf("after %d concurrent deposits balance = %s, want 320", n, got)
	}
	if m.Generation() != n {
		t.Errorf("generation = %d, want %d", m.Generation(), n)
	}
}

func TestOffchainStateManager_ConcurrentSnapshotWhileApplying(t *testing.T) {
	// Snapshot song song với Apply: race detector phải sạch, snapshot
	// được trả ra là consistent (root khớp với accounts tại thời điểm
	// chụp).
	m := NewOffchainStateManager()
	const writers = 8
	const readers = 8
	const opsPerWriter = 20

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	for w := 0; w < writers; w++ {
		w := w
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerWriter; i++ {
				m.ApplyDeposit(types.DepositRecord{
					DepositID: depID(w*100 + i),
					Owner:     mgrBob,
					Denom:     mgrDenom,
					Amount:    "1",
				})
			}
		}()
	}
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerWriter; i++ {
				snap := m.Snapshot()
				_ = snap.Root()
				_ = snap.Account(mgrBob, mgrDenom)
			}
		}()
	}
	wg.Wait()

	// Tất cả deposit unique → balance = writers * opsPerWriter.
	wantBal := writers * opsPerWriter
	gotBal := m.Account(mgrBob, mgrDenom).Balance
	if gotBal != itoa(wantBal) {
		t.Errorf("final balance = %s, want %d", gotBal, wantBal)
	}
}

// --- Helpers --------------------------------------------------------------

func depID(i int) string {
	return "dep-" + itoa(i)
}

func itoa(i int) string {
	// avoid strconv import collisions; manager tests share package state_test
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
