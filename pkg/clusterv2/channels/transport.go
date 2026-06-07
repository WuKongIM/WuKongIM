package channels

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
)

// HandlerRegistrar registers clusterv2 typed RPC handlers.
type HandlerRegistrar interface {
	// Register registers handler for serviceID.
	Register(serviceID uint8, handler clusternet.Handler)
}

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

// PullBatch sends grouped ChannelV2 pull requests to node.
func (c *TransportClient) PullBatch(ctx context.Context, node ch.NodeID, req channeltransport.PullBatchRequest) (channeltransport.PullBatchResponse, error) {
	payload, err := encodePullBatchRequest(req)
	if err != nil {
		return channeltransport.PullBatchResponse{}, err
	}
	resp, err := c.caller.Call(ctx, uint64(node), clusternet.RPCChannelPullBatch, payload)
	if err != nil {
		return channeltransport.PullBatchResponse{}, err
	}
	decoded, err := decodePullBatchResponse(resp)
	if err != nil {
		return channeltransport.PullBatchResponse{}, err
	}
	if len(decoded.Items) != len(req.Items) {
		return channeltransport.PullBatchResponse{}, fmt.Errorf("channels: pull batch response items = %d, want %d", len(decoded.Items), len(req.Items))
	}
	return decoded, nil
}

// Ack sends a ChannelV2 acknowledgement to node.
func (c *TransportClient) Ack(ctx context.Context, node ch.NodeID, req channeltransport.AckRequest) error {
	payload, err := encodeAckRequest(req)
	if err != nil {
		return err
	}
	resp, err := c.caller.Call(ctx, uint64(node), clusternet.RPCChannelAck, payload)
	if err != nil {
		return err
	}
	return decodeRPCResult(resp, kindAck, nil)
}

// PullHint sends a ChannelV2 pull hint to node.
func (c *TransportClient) PullHint(ctx context.Context, node ch.NodeID, req channeltransport.PullHintRequest) error {
	payload, err := encodePullHintRequest(req)
	if err != nil {
		return err
	}
	resp, err := c.caller.Call(ctx, uint64(node), clusternet.RPCChannelPullHint, payload)
	if err != nil {
		return err
	}
	return decodeRPCResult(resp, kindPullHint, nil)
}

// PullHintBatch sends grouped ChannelV2 pull hints to node.
func (c *TransportClient) PullHintBatch(ctx context.Context, node ch.NodeID, req channeltransport.PullHintBatchRequest) (channeltransport.PullHintBatchResponse, error) {
	payload, err := encodePullHintBatchRequest(req)
	if err != nil {
		return channeltransport.PullHintBatchResponse{}, err
	}
	resp, err := c.caller.Call(ctx, uint64(node), clusternet.RPCChannelPullHintBatch, payload)
	if err != nil {
		return channeltransport.PullHintBatchResponse{}, err
	}
	decoded, err := decodePullHintBatchResponse(resp)
	if err != nil {
		return channeltransport.PullHintBatchResponse{}, err
	}
	if len(decoded.Items) != len(req.Items) {
		return channeltransport.PullHintBatchResponse{}, fmt.Errorf("channels: pull hint batch response items = %d, want %d", len(decoded.Items), len(req.Items))
	}
	return decoded, nil
}

// Notify sends a legacy ChannelV2 notify request to node.
func (c *TransportClient) Notify(ctx context.Context, node ch.NodeID, req channeltransport.NotifyRequest) error {
	payload, err := encodeNotifyRequest(req)
	if err != nil {
		return err
	}
	resp, err := c.caller.Call(ctx, uint64(node), clusternet.RPCChannelNotify, payload)
	if err != nil {
		return err
	}
	return decodeRPCResult(resp, kindNotify, nil)
}

// ForwardAppend sends a client append request to node.
func (c *TransportClient) ForwardAppend(ctx context.Context, node ch.NodeID, req ch.AppendRequest) (ch.AppendResult, error) {
	payload, err := encodeAppendRequest(req)
	if err != nil {
		return ch.AppendResult{}, err
	}
	resp, err := c.callShard(ctx, uint64(node), clusternet.RPCChannelAppend, channelForwardShardKey(req.ChannelID), payload)
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
	resp, err := c.callShard(ctx, uint64(node), clusternet.RPCChannelAppendBatch, channelForwardShardKey(req.ChannelID), payload)
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	return decodeAppendBatchResponse(resp)
}

// ForwardLastVisible sends a last-visible message read to node.
func (c *TransportClient) ForwardLastVisible(ctx context.Context, node ch.NodeID, req LastVisibleRequest) (LastVisibleResponse, error) {
	payload, err := encodeLastVisibleRequest(req)
	if err != nil {
		return LastVisibleResponse{}, err
	}
	resp, err := c.callShard(ctx, uint64(node), clusternet.RPCChannelLastVisible, channelForwardShardKey(req.ChannelID), payload)
	if err != nil {
		return LastVisibleResponse{}, err
	}
	return decodeLastVisibleResponse(resp)
}

func (c *TransportClient) callShard(ctx context.Context, node uint64, serviceID uint8, shardKey uint64, payload []byte) ([]byte, error) {
	if caller, ok := c.caller.(clusternet.ShardCaller); ok {
		return caller.CallShard(ctx, node, serviceID, shardKey, payload)
	}
	return c.caller.Call(ctx, node, serviceID, payload)
}

func channelForwardShardKey(id ch.ChannelID) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte{id.Type})
	_, _ = h.Write([]byte(id.ID))
	return h.Sum64()
}

// RegisterHandlersOn registers ChannelV2 replication handlers on registrar.
func RegisterHandlersOn(registrar HandlerRegistrar, server channeltransport.Server) {
	registrar.Register(clusternet.RPCChannelPull, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodePullRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := server.HandlePull(ctx, req)
		return encodeRPCResult(kindPullResponse, resp, err)
	}))
	registrar.Register(clusternet.RPCChannelPullBatch, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodePullBatchRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := handlePullBatch(ctx, server, req)
		return encodeRPCResult(kindPullBatchResponse, resp, err)
	}))
	registrar.Register(clusternet.RPCChannelAck, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeAckRequest(payload)
		if err != nil {
			return nil, err
		}
		return encodeRPCResult(kindAck, nil, server.HandleAck(ctx, req))
	}))
	registrar.Register(clusternet.RPCChannelPullHint, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodePullHintRequest(payload)
		if err != nil {
			return nil, err
		}
		return encodeRPCResult(kindPullHint, nil, server.HandlePullHint(ctx, req))
	}))
	registrar.Register(clusternet.RPCChannelPullHintBatch, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodePullHintBatchRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := handlePullHintBatch(ctx, server, req)
		return encodeRPCResult(kindPullHintBatchResponse, resp, err)
	}))
	registrar.Register(clusternet.RPCChannelNotify, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeNotifyRequest(payload)
		if err != nil {
			return nil, err
		}
		return encodeRPCResult(kindNotify, nil, server.HandleNotify(ctx, req))
	}))
}

func handlePullBatch(ctx context.Context, server channeltransport.Server, req channeltransport.PullBatchRequest) (channeltransport.PullBatchResponse, error) {
	if batch, ok := server.(channeltransport.BatchServer); ok {
		return batch.HandlePullBatch(ctx, req)
	}
	resp := channeltransport.PullBatchResponse{Items: make([]channeltransport.PullBatchItemResult, len(req.Items))}
	for i, item := range req.Items {
		pull, err := server.HandlePull(ctx, item)
		resp.Items[i] = channeltransport.PullBatchItemResult{Response: pull, Err: err}
	}
	return resp, nil
}

func handlePullHintBatch(ctx context.Context, server channeltransport.Server, req channeltransport.PullHintBatchRequest) (channeltransport.PullHintBatchResponse, error) {
	if batch, ok := server.(channeltransport.BatchServer); ok {
		return batch.HandlePullHintBatch(ctx, req)
	}
	resp := channeltransport.PullHintBatchResponse{Items: make([]channeltransport.PullHintBatchItemResult, len(req.Items))}
	for i, item := range req.Items {
		resp.Items[i] = channeltransport.PullHintBatchItemResult{Err: server.HandlePullHint(ctx, item)}
	}
	return resp, nil
}

// RegisterHandlers registers ChannelV2 replication handlers on network for nodeID.
func RegisterHandlers(network *clusternet.LocalNetwork, nodeID uint64, server channeltransport.Server) {
	RegisterHandlersOn(localNetworkRegistrar{network: network, nodeID: nodeID}, server)
}

// RegisterServiceHandlersOn registers ChannelV2 replication and append-forward handlers on registrar.
func RegisterServiceHandlersOn(registrar HandlerRegistrar, service *Service) {
	RegisterHandlersOn(registrar, service.Server())
	registrar.Register(clusternet.RPCChannelAppend, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		started := time.Now()
		req, err := decodeAppendRequest(payload)
		if err != nil {
			service.observeAppendStage(appendStageForwardAppendRemote, err, time.Since(started))
			return nil, err
		}
		resp, err := service.Append(ctx, req)
		service.observeAppendStage(appendStageForwardAppendRemote, err, time.Since(started))
		return encodeRPCResult(kindAppendResponse, resp, err)
	}))
	registrar.Register(clusternet.RPCChannelAppendBatch, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		started := time.Now()
		req, err := decodeAppendBatchRequest(payload)
		if err != nil {
			service.observeAppendStage(appendStageForwardAppendRemote, err, time.Since(started))
			return nil, err
		}
		resp, err := service.AppendBatch(ctx, req)
		service.observeAppendStage(appendStageForwardAppendRemote, err, time.Since(started))
		return encodeRPCResult(kindAppendBatchResponse, resp, err)
	}))
	registrar.Register(clusternet.RPCChannelLastVisible, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeLastVisibleRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := service.handleForwardLastVisible(ctx, req)
		return encodeRPCResult(kindLastVisibleResponse, resp, err)
	}))
}

// RegisterServiceHandlers registers ChannelV2 replication and append-forward handlers on network.
func RegisterServiceHandlers(network *clusternet.LocalNetwork, nodeID uint64, service *Service) {
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: nodeID}, service)
}

type localNetworkRegistrar struct {
	network *clusternet.LocalNetwork
	nodeID  uint64
}

func (r localNetworkRegistrar) Register(serviceID uint8, handler clusternet.Handler) {
	r.network.Register(r.nodeID, serviceID, handler)
}

var _ channeltransport.Client = (*TransportClient)(nil)
var _ channeltransport.BatchClient = (*TransportClient)(nil)
var _ ForwardClient = (*TransportClient)(nil)
