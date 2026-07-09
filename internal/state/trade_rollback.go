package state

// STATE-T10 — Trade rollback.
//
// When a batch is rejected on-chain or prove-fails, the off-chain state must
// return to the last settled baseline — the state captured BEFORE the trade
// batch was matched/applied. This fixes the core gap where FailBatch only marked
// status without restoring balances/reserved or reopening orders.
//
// Mechanism: exact SNAPSHOT restoration, not manual inverse arithmetic. A
// TradeBaseline captured before matching holds:
//   - the OffchainStateManager snapshot (balances + reserved + reservation
//     registry + root — deep copy),
//   - the orderbook snapshot (resting set + remaining + sequence),
//   - the batch's order nullifiers (to release).
//
// Rolling back restores all three to the baseline, which realizes every step the
// plan lists — un-fill fills, return reserved→available for consumed collateral,
// refund fees from the fee account, reopen partially/ fully filled orders, and
// recompute the root to baseline — WITHOUT any arithmetic that could drift or
// double-spend. It is idempotent: restoring to the same immutable baseline any
// number of times yields the same state.

// OrderNullifierReleaser releases a marked order nullifier so the order can be
// used again after its fill/cancel is rolled back. *InMemoryOrderNullifiers
// satisfies it.
type OrderNullifierReleaser interface {
	Unmark(nullifier string)
}

// TradeBaseline is an immutable snapshot of everything a trade batch touches,
// captured before the batch is matched/applied. Mint only via
// CaptureTradeBaseline.
type TradeBaseline struct {
	manager    Snapshot
	book       OrderbookSnapshot
	nullifiers []string
}

// Root returns the baseline (pre-batch) pending root.
func (b TradeBaseline) Root() string { return b.manager.Root() }

// CaptureTradeBaseline snapshots the manager + orderbook at the last-settled
// baseline (call AFTER orders are placed/reserved but BEFORE matching/apply).
// orderNullifiers are the batch's order nullifiers that a later fill/settlement
// may mark used; they are released on rollback.
func CaptureTradeBaseline(m *OffchainStateManager, book *Orderbook, orderNullifiers []string) TradeBaseline {
	return TradeBaseline{
		manager:    m.Snapshot(),
		book:       book.Capture(),
		nullifiers: append([]string(nil), orderNullifiers...),
	}
}

// TradeRollback reverts a trade batch to its baseline.
type TradeRollback struct {
	releaser OrderNullifierReleaser
}

// NewTradeRollback builds a rollback helper. releaser may be nil (no nullifier
// release — e.g. when the batch never marked any).
func NewTradeRollback(releaser OrderNullifierReleaser) *TradeRollback {
	return &TradeRollback{releaser: releaser}
}

// Rollback restores manager + orderbook to the baseline and releases the
// batch's order nullifiers. Returns the restored (baseline) root. Idempotent:
// calling it again with the same baseline is safe and yields the same state.
//
// Order of operations does not matter for correctness (each restore is
// independent and absolute), but manager first keeps the reserved/reservation
// state consistent with the reopened orders.
func (tr *TradeRollback) Rollback(m *OffchainStateManager, book *Orderbook, baseline TradeBaseline) string {
	m.Rollback(baseline.manager)
	book.Restore(baseline.book)
	if tr.releaser != nil {
		for _, n := range baseline.nullifiers {
			tr.releaser.Unmark(n)
		}
	}
	return m.Root()
}
