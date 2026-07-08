package indexer

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/chain"
)

// DepositEventSource is the boundary the deposit poller reads real on-chain
// deposit events from. Implementations subscribe to or poll the chain and return
// the deposit transactions discovered since the supplied height (exclusive).
//
// Returning chain.TxResult (the same type LocalClient/CosmosClient emit on the
// deposit path) keeps the downstream DepositIndexer agnostic to whether an event
// arrived synchronously from a deposit broadcast or asynchronously from the
// chain's event stream.
type DepositEventSource interface {
	// FetchDepositsSince returns deposit txs at heights strictly greater than
	// fromHeight, together with the height the caller should resume from next
	// (the max height observed, or fromHeight when nothing new was found).
	FetchDepositsSince(ctx context.Context, fromHeight int64) ([]chain.TxResult, int64, error)
}
