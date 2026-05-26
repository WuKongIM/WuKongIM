package channels

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
)

// TransportClient implements ChannelV2 transport over clusterv2 typed RPC.
type TransportClient struct {
	caller clusternet.Caller
}

// NewTransportClient creates a ChannelV2 transport client.
func NewTransportClient(caller clusternet.Caller) *TransportClient {
	return &TransportClient{caller: caller}
}

// Pull sends a ChannelV2 pull request to node.
func (c *TransportClient) Pull(ctx context.Context, node ch.NodeID, req channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	payload, err := EncodePullRequest(req)
	if err != nil {
		return channeltransport.PullResponse{}, err
	}
	resp, err := c.caller.Call(ctx, uint64(node), clusternet.RPCChannelPull, payload)
	if err != nil {
		return channeltransport.PullResponse{}, err
	}
	return decodePullResponse(resp)
}

// Ack sends a ChannelV2 acknowledgement to node.
func (c *TransportClient) Ack(ctx context.Context, node ch.NodeID, req channeltransport.AckRequest) error {
	payload, err := encodeAckRequest(req)
	if err != nil {
		return err
	}
	_, err = c.caller.Call(ctx, uint64(node), clusternet.RPCChannelAck, payload)
	return err
}

// PullHint sends a ChannelV2 pull hint to node.
func (c *TransportClient) PullHint(ctx context.Context, node ch.NodeID, req channeltransport.PullHintRequest) error {
	payload, err := encodePullHintRequest(req)
	if err != nil {
		return err
	}
	_, err = c.caller.Call(ctx, uint64(node), clusternet.RPCChannelPullHint, payload)
	return err
}

// Notify sends a legacy ChannelV2 notify request to node.
func (c *TransportClient) Notify(ctx context.Context, node ch.NodeID, req channeltransport.NotifyRequest) error {
	payload, err := encodeNotifyRequest(req)
	if err != nil {
		return err
	}
	_, err = c.caller.Call(ctx, uint64(node), clusternet.RPCChannelNotify, payload)
	return err
}

// ForwardAppend sends a client append request to node.
func (c *TransportClient) ForwardAppend(ctx context.Context, node ch.NodeID, req ch.AppendRequest) (ch.AppendResult, error) {
	payload, err := encodeAppendRequest(req)
	if err != nil {
		return ch.AppendResult{}, err
	}
	resp, err := c.caller.Call(ctx, uint64(node), clusternet.RPCChannelAppend, payload)
	if err != nil {
		return ch.AppendResult{}, err
	}
	return decodeAppendResponse(resp)
}

// ForwardAppendBatch sends a client append batch request to node.
func (c *TransportClient) ForwardAppendBatch(ctx context.Context, node ch.NodeID, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	payload, err := encodeAppendBatchRequest(req)
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	resp, err := c.caller.Call(ctx, uint64(node), clusternet.RPCChannelAppendBatch, payload)
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	return decodeAppendBatchResponse(resp)
}

// RegisterHandlers registers ChannelV2 replication handlers on network for nodeID.
func RegisterHandlers(network *clusternet.LocalNetwork, nodeID uint64, server channeltransport.Server) {
	network.Register(nodeID, clusternet.RPCChannelPull, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodePullRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := server.HandlePull(ctx, req)
		if err != nil {
			return nil, err
		}
		return encodePullResponse(resp)
	}))
	network.Register(nodeID, clusternet.RPCChannelAck, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeAckRequest(payload)
		if err != nil {
			return nil, err
		}
		return nil, server.HandleAck(ctx, req)
	}))
	network.Register(nodeID, clusternet.RPCChannelPullHint, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodePullHintRequest(payload)
		if err != nil {
			return nil, err
		}
		return nil, server.HandlePullHint(ctx, req)
	}))
	network.Register(nodeID, clusternet.RPCChannelNotify, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeNotifyRequest(payload)
		if err != nil {
			return nil, err
		}
		return nil, server.HandleNotify(ctx, req)
	}))
}

// RegisterServiceHandlers registers ChannelV2 replication and append-forward handlers on network.
func RegisterServiceHandlers(network *clusternet.LocalNetwork, nodeID uint64, service *Service) {
	RegisterHandlers(network, nodeID, service.Server())
	network.Register(nodeID, clusternet.RPCChannelAppend, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeAppendRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := service.Append(ctx, req)
		if err != nil {
			return nil, err
		}
		return encodeAppendResponse(resp)
	}))
	network.Register(nodeID, clusternet.RPCChannelAppendBatch, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeAppendBatchRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := service.AppendBatch(ctx, req)
		if err != nil {
			return nil, err
		}
		return encodeAppendBatchResponse(resp)
	}))
}

var _ channeltransport.Client = (*TransportClient)(nil)
var _ ForwardClient = (*TransportClient)(nil)
