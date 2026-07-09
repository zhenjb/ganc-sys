package service

import (
	"context"

	"github.com/zhenjb/ganc-sys/internal/relayer"
	"github.com/zhenjb/ganc-sys/pkg/types"
)

// RelayerTradeSubmitter is the INT-T08 TradeSubmitter: it submits a proven trade
// batch to the chain via the relayer (MsgSubmitBatchProof, signed by the relayer
// key), replacing the INT-T06 LocalTradeSubmitter stub. It carries trades[] (in
// settlementUpdate) and the 8 public inputs (in proofBundle) unchanged, and maps
// the relayer result back to the (txHash, accepted, err) the trade settlement
// pipeline expects — a chain reject becomes (accepted=false, err) so INT-T06
// rolls back + re-enqueues.
type RelayerTradeSubmitter struct {
	client relayer.TradeClient
}

// NewRelayerTradeSubmitter wraps a relayer.TradeClient (LocalClient in local
// mode, CosmosClient against a real chain).
func NewRelayerTradeSubmitter(client relayer.TradeClient) *RelayerTradeSubmitter {
	return &RelayerTradeSubmitter{client: client}
}

var _ TradeSubmitter = (*RelayerTradeSubmitter)(nil)

func (s *RelayerTradeSubmitter) SubmitTrade(ctx context.Context, upd types.SettlementUpdate, com types.BatchCommitments, proof types.ProofBundle) (string, bool, error) {
	res, err := s.client.SubmitTradeBatch(ctx, relayer.SubmitBatchInput{
		SettlementUpdate: upd,
		BatchCommitments: com,
		ProofBundle:      proof,
	})
	// On a chain reject the relayer returns Accepted=false with an error; pass
	// both through so the settle loop treats it as a failure (rollback + requeue).
	return res.TxHash, res.Accepted, err
}
