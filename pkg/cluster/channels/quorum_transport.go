package channels

import (
	"context"
	"encoding/binary"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

var errInvalidQuorumExchangeFrame = errors.New("channels: invalid quorum exchange frame")

// QuorumExchangeServer is the bounded follower-side quorum exchange surface.
type QuorumExchangeServer interface {
	Handle(context.Context, ch.NodeID, replication.ExchangeBatch) (replication.ExchangeBatchResult, error)
}

// QuorumPeerLink carries the data-bearing quorum protocol over cluster RPC.
type QuorumPeerLink struct {
	local  ch.NodeID
	caller clusternet.Caller
}

// NewQuorumPeerLink creates one node-owned quorum peer transport.
func NewQuorumPeerLink(local ch.NodeID, caller clusternet.Caller) (*QuorumPeerLink, error) {
	if local == 0 || caller == nil {
		return nil, ch.ErrInvalidConfig
	}
	return &QuorumPeerLink{local: local, caller: caller}, nil
}

// Exchange sends one bounded batch and decodes its position-correlated result.
func (l *QuorumPeerLink) Exchange(ctx context.Context, node ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	if l == nil || ctx == nil || l.local == 0 || l.caller == nil || node == 0 || node == l.local {
		return replication.ExchangeBatchResult{}, ch.ErrInvalidConfig
	}
	payload, err := replication.EncodeExchangeBatch(batch)
	if err != nil {
		return replication.ExchangeBatchResult{}, err
	}
	frame := binary.AppendUvarint(nil, uint64(l.local))
	frame = append(frame, payload...)
	response, err := clusternet.CallOwnedPayload(ctx, l.caller, uint64(node), clusternet.RPCChannelQuorumExchange, frame)
	if err != nil {
		return replication.ExchangeBatchResult{}, err
	}
	return replication.DecodeExchangeBatchResult(response)
}

// RegisterQuorumExchangeHandlerOn registers the stable quorum exchange RPC.
func RegisterQuorumExchangeHandlerOn(registrar HandlerRegistrar, server QuorumExchangeServer) {
	registrar.Register(clusternet.RPCChannelQuorumExchange, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		origin, size := binary.Uvarint(payload)
		if size <= 0 || origin == 0 || size == len(payload) {
			return nil, errInvalidQuorumExchangeFrame
		}
		batch, err := replication.DecodeExchangeBatch(payload[size:])
		if err != nil {
			return nil, err
		}
		result, err := server.Handle(ctx, ch.NodeID(origin), batch)
		if err != nil {
			return nil, err
		}
		return replication.EncodeExchangeBatchResult(result)
	}))
}

var _ replication.PeerLink = (*QuorumPeerLink)(nil)
