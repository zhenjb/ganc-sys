// state14_manager_demo trình diễn OffchainStateManager qua canonical
// Alice 100/40 flow + rollback path. Chạy từ repo root:
//
//	go run ./p3/script-test/state14_manager_demo
//
// Mục đích:
//   - Smoke-check OffchainStateManager khớp Agreements (root advance,
//     idempotency, snapshot immutability, rollback restore).
//   - Mô phỏng cách P4 sẽ wire manager (INT-05 indexer, INT-06 withdraw
//     API, INT-09 batch submit fail).
//   - KHÔNG phải unit test — chỉ là tài liệu thực thi được, in ra
//     pretty trace để dev đọc và verify bằng mắt.
package main

import (
	"fmt"
	"os"

	"github.com/zhenjb/ganc-sys/internal/state"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

const (
	aliceAddr   = "cosmos1alice"
	denom       = "uusdc"
	aliceSecret = "mock-user-secret"
)

func main() {
	fmt.Println("== STATE-14 OffchainStateManager demo ==")
	fmt.Println()

	runHappyPath()
	fmt.Println()
	runRollbackOnSubmitFail()
}

// runHappyPath mô phỏng luồng accept: indexer credit deposit, withdraw
// API debit balance, batch builder snapshot → submit thành công → state
// giữ nguyên không cần rollback.
func runHappyPath() {
	fmt.Println("-- Happy path (deposit -> withdraw -> batch -> submit accept) --")
	m := state.NewOffchainStateManager()
	fmt.Printf("[init]               root=%s gen=%d\n", short(m.Root()), m.Generation())

	// INT-05 indexer apply deposit dep-1 amount=100.
	if _, err := m.ApplyDeposit(types.DepositRecord{
		DepositID: "dep-1", Owner: aliceAddr, Denom: denom, Amount: "100",
	}); err != nil {
		die("ApplyDeposit", err)
	}
	fmt.Printf("[after dep-1=100]   root=%s gen=%d balance=%s nonce=%s\n",
		short(m.Root()), m.Generation(),
		m.Account(aliceAddr, denom).Balance,
		m.Account(aliceAddr, denom).Nonce)

	// INT-06 withdraw API: derive nullifier, debit pending balance.
	nullifier, err := state.NullifierFor(aliceSecret, "1")
	if err != nil {
		die("NullifierFor", err)
	}
	req := types.WithdrawRequest{
		WithdrawID:  "wd-1",
		Owner:       aliceAddr,
		Denom:       denom,
		Amount:      "40",
		Destination: aliceAddr,
		Nonce:       "1",
	}
	if _, err := m.ApplyWithdrawRequest(req, nullifier); err != nil {
		die("ApplyWithdrawRequest", err)
	}
	fmt.Printf("[after wd-1=40]     root=%s gen=%d balance=%s nonce=%s nullifier=%s\n",
		short(m.Root()), m.Generation(),
		m.Account(aliceAddr, denom).Balance,
		m.Account(aliceAddr, denom).Nonce,
		short(nullifier))

	// P3 batch builder snapshot — đây sẽ là input pre-batch state khi
	// P4 wire xong.
	snap := m.Snapshot()
	fmt.Printf("[snapshot for batch] root=%s gen=%d (immutable)\n", short(snap.Root()), snap.Generation())

	// INT-09 submit accept → giữ nguyên state. Không cần rollback.
	fmt.Printf("[submit accept]      manager state unchanged. final balance=%s\n",
		m.Account(aliceAddr, denom).Balance)
}

// runRollbackOnSubmitFail mô phỏng luồng reject: indexer apply deposit
// → withdraw API apply → batch submit fail → INT-09 rollback về snapshot
// trước batch (=trước withdraw). Withdraw được "hoàn lại" pending
// balance, nullifier bị xoá để user có thể submit lại.
func runRollbackOnSubmitFail() {
	fmt.Println("-- Rollback path (deposit -> withdraw -> batch -> submit FAIL -> rollback) --")
	m := state.NewOffchainStateManager()

	if _, err := m.ApplyDeposit(types.DepositRecord{
		DepositID: "dep-1", Owner: aliceAddr, Denom: denom, Amount: "100",
	}); err != nil {
		die("ApplyDeposit", err)
	}
	fmt.Printf("[after deposit]      balance=%s\n", m.Account(aliceAddr, denom).Balance)

	// Chụp snapshot TRƯỚC khi withdraw được apply (= pre-batch state).
	preBatchSnap := m.Snapshot()
	fmt.Printf("[pre-batch snapshot] root=%s\n", short(preBatchSnap.Root()))

	nullifier, err := state.NullifierFor(aliceSecret, "1")
	if err != nil {
		die("NullifierFor", err)
	}
	req := types.WithdrawRequest{
		WithdrawID:  "wd-1",
		Owner:       aliceAddr,
		Denom:       denom,
		Amount:      "40",
		Destination: aliceAddr,
		Nonce:       "1",
	}
	if _, err := m.ApplyWithdrawRequest(req, nullifier); err != nil {
		die("ApplyWithdrawRequest", err)
	}
	fmt.Printf("[after withdraw]     balance=%s nonce=%s nullifier_consumed=%v\n",
		m.Account(aliceAddr, denom).Balance,
		m.Account(aliceAddr, denom).Nonce,
		m.IsNullifierApplied(nullifier))

	// SubmitBatchProof fail (proof invalid hoặc chain reject) →
	// rollback.
	fmt.Println("[submit FAIL]        rolling back to pre-batch snapshot...")
	m.Rollback(preBatchSnap)

	fmt.Printf("[after rollback]     root=%s balance=%s nonce=%s nullifier_consumed=%v gen=%d\n",
		short(m.Root()),
		m.Account(aliceAddr, denom).Balance,
		m.Account(aliceAddr, denom).Nonce,
		m.IsNullifierApplied(nullifier),
		m.Generation())

	// Sanity: balance restore về 100, nullifier bị clear, deposit vẫn còn.
	expect("balance after rollback", m.Account(aliceAddr, denom).Balance, "100")
	expect("nonce after rollback", m.Account(aliceAddr, denom).Nonce, "0")
	if m.IsNullifierApplied(nullifier) {
		die("rollback should clear nullifier", nil)
	}
	if !m.IsDepositApplied("dep-1") {
		die("pre-snapshot deposit must persist through rollback", nil)
	}
	fmt.Println("[ok]                 rollback restored pre-batch state correctly")
}

func short(s string) string {
	if len(s) <= 18 {
		return s
	}
	return s[:10] + "…" + s[len(s)-6:]
}

func expect(label, got, want string) {
	if got != want {
		fmt.Fprintf(os.Stderr, "FAIL %s: got %q, want %q\n", label, got, want)
		os.Exit(1)
	}
}

func die(label string, err error) {
	fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", label, err)
	os.Exit(1)
}
