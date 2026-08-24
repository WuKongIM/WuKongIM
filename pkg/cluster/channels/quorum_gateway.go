package channels

import (
	"context"
	"sync/atomic"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
)

type quorumExchangeTarget struct {
	server QuorumExchangeServer
}

// QuorumExchangeGateway keeps the registered RPC stable while restore swaps
// the node-owned replication runtime.
type QuorumExchangeGateway struct {
	current atomic.Pointer[quorumExchangeTarget]
}

// NewQuorumExchangeGateway creates one stable endpoint.
func NewQuorumExchangeGateway(server QuorumExchangeServer) *QuorumExchangeGateway {
	gateway := &QuorumExchangeGateway{}
	gateway.Replace(server)
	return gateway
}

// Replace publishes a fully constructed server for subsequent calls.
func (g *QuorumExchangeGateway) Replace(server QuorumExchangeServer) {
	if g == nil || server == nil {
		return
	}
	g.current.Store(&quorumExchangeTarget{server: server})
}

// Clear fails new calls closed while an owning runtime is absent.
func (g *QuorumExchangeGateway) Clear() {
	if g != nil {
		g.current.Store(nil)
	}
}

// Handle forwards one bounded exchange to the current runtime.
func (g *QuorumExchangeGateway) Handle(ctx context.Context, from ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	if g == nil {
		return replication.ExchangeBatchResult{}, ch.ErrNotReady
	}
	target := g.current.Load()
	if target == nil || target.server == nil {
		return replication.ExchangeBatchResult{}, ch.ErrNotReady
	}
	return target.server.Handle(ctx, from, batch)
}

var _ QuorumExchangeServer = (*QuorumExchangeGateway)(nil)
