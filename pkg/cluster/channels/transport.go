package channels

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	wktransport "github.com/WuKongIM/WuKongIM/pkg/transport"
)

const (
	legacyCodecProbeInterval = time.Minute
	codecPeerStateCacheLimit = int64(1024)
	codecPeerLegacyBit       = uint64(1)
)

// HandlerRegistrar registers cluster typed RPC handlers.
type HandlerRegistrar interface {
	// Register registers handler for serviceID.
	Register(serviceID uint8, handler clusternet.Handler)
}

// TransportClient implements Channel transport over cluster typed RPC.
type TransportClient struct {
	caller clusternet.Caller
	// legacyCodecPeers stores bounded per-peer codec observations and coordinates one ordinary v6 probe at a time.
	legacyCodecPeers sync.Map
	// codecPeerCount tracks live legacyCodecPeers entries for bounded churn cleanup.
	codecPeerCount atomic.Int64
}

type codecPeerState struct {
	// nextRequest assigns monotonic request generations within this peer state.
	nextRequest atomic.Uint64
	// observation packs the latest applied request generation and legacy bit.
	observation atomic.Uint64
	// legacyUntilNanos is the wall-clock deadline for the bounded v5 compatibility window.
	legacyUntilNanos atomic.Int64
	// legacyMu serializes deadline publication with legacy observation updates.
	legacyMu sync.Mutex
	// probeOwner holds the request generation owning the one permitted ordinary v6 probe.
	probeOwner atomic.Uint64
	// inFlight prevents churn cleanup from removing state used by active calls.
	inFlight atomic.Int64
	// retired fences acquisitions while churn cleanup removes the map entry.
	retired atomic.Bool
}

func (s *codecPeerState) selectVersion() (uint8, uint64) {
	if s.observation.Load()&codecPeerLegacyBit == 0 {
		return codecVersion, s.beginV6Request()
	}
	until := time.Unix(0, s.legacyUntilNanos.Load())
	if time.Now().Before(until) {
		return legacyCodecVersionV5, 0
	}
	request := s.beginV6Request()
	if s.probeOwner.CompareAndSwap(0, request) {
		return codecVersion, request
	}
	return legacyCodecVersionV5, 0
}

func (s *codecPeerState) beginV6Request() uint64 {
	return s.nextRequest.Add(1)
}

func (s *codecPeerState) applyCurrent(request uint64) {
	next := request << 1
	applied := false
	for {
		current := s.observation.Load()
		if current>>1 > request {
			break
		}
		if s.observation.CompareAndSwap(current, next) {
			applied = true
			break
		}
	}
	s.releaseProbe(request, applied)
}

func (s *codecPeerState) applyLegacy(request uint64, until time.Time) {
	s.legacyMu.Lock()
	applied := false
	next := request<<1 | codecPeerLegacyBit
	for {
		current := s.observation.Load()
		if current>>1 > request {
			break
		}
		s.legacyUntilNanos.Store(until.UnixNano())
		if s.observation.CompareAndSwap(current, next) {
			applied = true
			break
		}
	}
	s.legacyMu.Unlock()
	s.releaseProbe(request, applied)
}

func (s *codecPeerState) releaseProbe(request uint64, observationApplied bool) {
	for {
		owner := s.probeOwner.Load()
		if owner == 0 {
			return
		}
		if observationApplied {
			if owner > request {
				return
			}
		} else if owner != request {
			return
		}
		if s.probeOwner.CompareAndSwap(owner, 0) {
			return
		}
	}
}

// NewTransportClient creates a Channel transport client.
func NewTransportClient(caller clusternet.Caller) *TransportClient {
	return &TransportClient{caller: caller}
}

// Pull sends a Channel pull request to node.
func (c *TransportClient) Pull(ctx context.Context, node ch.NodeID, req channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	resp, err := c.callVersioned(ctx, uint64(node), clusternet.RPCChannelPull, req.NeedMeta, func(version uint8) ([]byte, error) {
		return encodePullRequestVersion(req, version)
	})
	if err != nil {
		return channeltransport.PullResponse{}, err
	}
	return decodePullResponse(resp)
}

// PullBatch sends grouped Channel pull requests to node.
func (c *TransportClient) PullBatch(ctx context.Context, node ch.NodeID, req channeltransport.PullBatchRequest) (channeltransport.PullBatchResponse, error) {
	resp, err := c.callVersioned(ctx, uint64(node), clusternet.RPCChannelPullBatch, pullBatchNeedsMeta(req), func(version uint8) ([]byte, error) {
		return encodePullBatchRequestVersion(req, version)
	})
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

// Ack sends a Channel acknowledgement to node.
func (c *TransportClient) Ack(ctx context.Context, node ch.NodeID, req channeltransport.AckRequest) error {
	resp, err := c.callVersioned(ctx, uint64(node), clusternet.RPCChannelAck, false, func(version uint8) ([]byte, error) {
		return encodeAckRequestVersion(req, version)
	})
	if err != nil {
		return err
	}
	return decodeRPCResult(resp, kindAck, nil)
}

// PullHint sends a Channel pull hint to node.
func (c *TransportClient) PullHint(ctx context.Context, node ch.NodeID, req channeltransport.PullHintRequest) error {
	resp, err := c.callVersioned(ctx, uint64(node), clusternet.RPCChannelPullHint, false, func(version uint8) ([]byte, error) {
		return encodePullHintRequestVersion(req, version)
	})
	if err != nil {
		return err
	}
	return decodeRPCResult(resp, kindPullHint, nil)
}

// PullHintBatch sends grouped Channel pull hints to node.
func (c *TransportClient) PullHintBatch(ctx context.Context, node ch.NodeID, req channeltransport.PullHintBatchRequest) (channeltransport.PullHintBatchResponse, error) {
	resp, err := c.callVersioned(ctx, uint64(node), clusternet.RPCChannelPullHintBatch, false, func(version uint8) ([]byte, error) {
		return encodePullHintBatchRequestVersion(req, version)
	})
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

// Notify sends a legacy Channel notify request to node.
func (c *TransportClient) Notify(ctx context.Context, node ch.NodeID, req channeltransport.NotifyRequest) error {
	resp, err := c.callVersioned(ctx, uint64(node), clusternet.RPCChannelNotify, false, func(version uint8) ([]byte, error) {
		return encodeNotifyRequestVersion(req, version)
	})
	if err != nil {
		return err
	}
	return decodeRPCResult(resp, kindNotify, nil)
}

// ForwardAppend sends a client append request to node.
func (c *TransportClient) ForwardAppend(ctx context.Context, node ch.NodeID, req ch.AppendRequest) (ch.AppendResult, error) {
	resp, err := c.callShardVersioned(ctx, uint64(node), clusternet.RPCChannelAppend, channelForwardShardKey(req.ChannelID), func(version uint8) ([]byte, error) {
		return encodeAppendRequestVersion(req, version)
	})
	if err != nil {
		return ch.AppendResult{}, err
	}
	return decodeAppendResponse(resp)
}

// ForwardAppendBatch sends a client append batch request to node.
func (c *TransportClient) ForwardAppendBatch(ctx context.Context, node ch.NodeID, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	resp, err := c.callShardVersioned(ctx, uint64(node), clusternet.RPCChannelAppendBatch, channelForwardShardKey(req.ChannelID), func(version uint8) ([]byte, error) {
		return encodeAppendBatchRequestVersion(req, version)
	})
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	return decodeAppendBatchResponse(resp)
}

// ForwardLastVisible sends a last-visible message read to node.
func (c *TransportClient) ForwardLastVisible(ctx context.Context, node ch.NodeID, req LastVisibleRequest) (LastVisibleResponse, error) {
	resp, err := c.callShardVersioned(ctx, uint64(node), clusternet.RPCChannelLastVisible, channelForwardShardKey(req.ChannelID), func(version uint8) ([]byte, error) {
		return encodeLastVisibleRequestVersion(req, version)
	})
	if err != nil {
		return LastVisibleResponse{}, err
	}
	return decodeLastVisibleResponse(resp)
}

func (c *TransportClient) call(ctx context.Context, node uint64, serviceID uint8, payload []byte) ([]byte, error) {
	return clusternet.CallOwnedPayload(ctx, c.caller, node, serviceID, payload)
}

func (c *TransportClient) callShard(ctx context.Context, node uint64, serviceID uint8, shardKey uint64, payload []byte) ([]byte, error) {
	return clusternet.CallShardOwnedPayload(ctx, c.caller, node, serviceID, shardKey, payload)
}

type codecRequestEncoder func(version uint8) ([]byte, error)

func (c *TransportClient) callVersioned(ctx context.Context, node uint64, serviceID uint8, requireCurrent bool, encode codecRequestEncoder) ([]byte, error) {
	return c.callCompatible(node, requireCurrent, encode, func(payload []byte) ([]byte, error) {
		return c.call(ctx, node, serviceID, payload)
	})
}

func (c *TransportClient) callShardVersioned(ctx context.Context, node uint64, serviceID uint8, shardKey uint64, encode codecRequestEncoder) ([]byte, error) {
	return c.callCompatible(node, false, encode, func(payload []byte) ([]byte, error) {
		return c.callShard(ctx, node, serviceID, shardKey, payload)
	})
}

func (c *TransportClient) callCompatible(node uint64, requireCurrent bool, encode codecRequestEncoder, call func([]byte) ([]byte, error)) ([]byte, error) {
	state := c.acquireCodecPeerState(node)
	defer c.releaseCodecPeerState(node, state)

	version := codecVersion
	var requestGeneration uint64
	if requireCurrent {
		requestGeneration = state.beginV6Request()
	} else {
		version, requestGeneration = state.selectVersion()
	}
	payload, err := encode(version)
	if err != nil {
		state.releaseProbe(requestGeneration, false)
		return nil, err
	}
	response, err := call(payload)
	if version != codecVersion {
		return response, err
	}
	if err == nil {
		if requireCurrent && (len(response) == 0 || response[0] != codecVersion) {
			// Authority reads require both a v6 request and a v6 response. A
			// legacy success frame omits retention and write-fence fields.
			state.applyLegacy(requestGeneration, time.Now().Add(legacyCodecProbeInterval))
			return nil, errInvalidCodecFrame
		}
		state.applyCurrent(requestGeneration)
		return response, nil
	}
	if !isLegacyCodecVersionRejection(err) {
		state.releaseProbe(requestGeneration, false)
		return response, err
	}

	state.applyLegacy(requestGeneration, time.Now().Add(legacyCodecProbeInterval))
	if requireCurrent {
		// v5 cannot carry retention or write-fence metadata. Returning a v5
		// NeedMeta response as authority would clear safety fields, so fail
		// closed until the peer can answer with the current codec.
		return response, err
	}

	legacyPayload, encodeErr := encode(legacyCodecVersionV5)
	if encodeErr != nil {
		return nil, encodeErr
	}
	return call(legacyPayload)
}

func pullBatchNeedsMeta(req channeltransport.PullBatchRequest) bool {
	for _, item := range req.Items {
		if item.NeedMeta {
			return true
		}
	}
	return false
}

func validateAuthorityCodec(payload []byte, needMeta bool) error {
	if needMeta && (len(payload) == 0 || payload[0] != codecVersion) {
		return errInvalidCodecFrame
	}
	return nil
}

func (c *TransportClient) cacheLegacyCodecPeer(node uint64, until time.Time) {
	state := c.acquireCodecPeerState(node)
	defer c.releaseCodecPeerState(node, state)
	state.applyLegacy(state.beginV6Request(), until)
}

func (c *TransportClient) acquireCodecPeerState(node uint64) *codecPeerState {
	for {
		if value, ok := c.legacyCodecPeers.Load(node); ok {
			state := value.(*codecPeerState)
			state.inFlight.Add(1)
			if !state.retired.Load() {
				return state
			}
			state.inFlight.Add(-1)
			continue
		}

		state := &codecPeerState{}
		state.inFlight.Store(1)
		actual, loaded := c.legacyCodecPeers.LoadOrStore(node, state)
		if loaded {
			_ = actual
			continue
		}
		if c.codecPeerCount.Add(1) > codecPeerStateCacheLimit {
			c.trimCodecPeerStates(node)
		}
		return state
	}
}

func (c *TransportClient) releaseCodecPeerState(node uint64, state *codecPeerState) {
	if state.inFlight.Add(-1) == 0 && c.codecPeerCount.Load() > codecPeerStateCacheLimit {
		c.trimCodecPeerStates(node)
	}
}

func (c *TransportClient) trimCodecPeerStates(skipNode uint64) {
	if c.codecPeerCount.Load() <= codecPeerStateCacheLimit {
		return
	}
	c.legacyCodecPeers.Range(func(key, value any) bool {
		if c.codecPeerCount.Load() <= codecPeerStateCacheLimit {
			return false
		}
		node, ok := key.(uint64)
		if !ok || node == skipNode {
			return true
		}
		state, ok := value.(*codecPeerState)
		if !ok || state.probeOwner.Load() != 0 || state.inFlight.Load() != 0 || !state.retired.CompareAndSwap(false, true) {
			return true
		}
		if state.inFlight.Load() != 0 {
			state.retired.Store(false)
			return true
		}
		if c.legacyCodecPeers.CompareAndDelete(node, state) {
			c.codecPeerCount.Add(-1)
		}
		return true
	})
}

func isLegacyCodecVersionRejection(err error) bool {
	if errors.Is(err, errInvalidCodecFrame) {
		return true
	}
	var remoteErr wktransport.RemoteError
	return errors.As(err, &remoteErr) &&
		remoteErr.Code == "remote_error" &&
		remoteErr.Message == errInvalidCodecFrame.Error()
}

func channelForwardShardKey(id ch.ChannelID) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte{id.Type})
	_, _ = h.Write([]byte(id.ID))
	return h.Sum64()
}

// RegisterHandlersOn registers Channel replication handlers on registrar.
func RegisterHandlersOn(registrar HandlerRegistrar, server channeltransport.Server) {
	registrar.Register(clusternet.RPCChannelPull, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodePullRequest(payload)
		if err != nil {
			return nil, err
		}
		if err := validateAuthorityCodec(payload, req.NeedMeta); err != nil {
			return nil, err
		}
		resp, err := server.HandlePull(ctx, req)
		return encodeRPCResultVersion(responseCodecVersion(payload), kindPullResponse, resp, err)
	}))
	registrar.Register(clusternet.RPCChannelPullBatch, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodePullBatchRequest(payload)
		if err != nil {
			return nil, err
		}
		if err := validateAuthorityCodec(payload, pullBatchNeedsMeta(req)); err != nil {
			return nil, err
		}
		resp, err := handlePullBatch(ctx, server, req)
		return encodeRPCResultVersion(responseCodecVersion(payload), kindPullBatchResponse, resp, err)
	}))
	registrar.Register(clusternet.RPCChannelAck, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeAckRequest(payload)
		if err != nil {
			return nil, err
		}
		return encodeRPCResultVersion(responseCodecVersion(payload), kindAck, nil, server.HandleAck(ctx, req))
	}))
	registrar.Register(clusternet.RPCChannelPullHint, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodePullHintRequest(payload)
		if err != nil {
			return nil, err
		}
		return encodeRPCResultVersion(responseCodecVersion(payload), kindPullHint, nil, server.HandlePullHint(ctx, req))
	}))
	registrar.Register(clusternet.RPCChannelPullHintBatch, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodePullHintBatchRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := handlePullHintBatch(ctx, server, req)
		return encodeRPCResultVersion(responseCodecVersion(payload), kindPullHintBatchResponse, resp, err)
	}))
	registrar.Register(clusternet.RPCChannelNotify, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeNotifyRequest(payload)
		if err != nil {
			return nil, err
		}
		return encodeRPCResultVersion(responseCodecVersion(payload), kindNotify, nil, server.HandleNotify(ctx, req))
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

// RegisterHandlers registers Channel replication handlers on network for nodeID.
func RegisterHandlers(network *clusternet.LocalNetwork, nodeID uint64, server channeltransport.Server) {
	RegisterHandlersOn(localNetworkRegistrar{network: network, nodeID: nodeID}, server)
}

// RegisterServiceHandlersOn registers Channel replication and append-forward handlers on registrar.
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
		return encodeRPCResultVersion(responseCodecVersion(payload), kindAppendResponse, resp, err)
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
		return encodeRPCResultVersion(responseCodecVersion(payload), kindAppendBatchResponse, resp, err)
	}))
	registrar.Register(clusternet.RPCChannelLastVisible, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := decodeLastVisibleRequest(payload)
		if err != nil {
			return nil, err
		}
		resp, err := service.handleForwardLastVisible(ctx, req)
		return encodeRPCResultVersion(responseCodecVersion(payload), kindLastVisibleResponse, resp, err)
	}))
}

// RegisterServiceHandlers registers Channel replication and append-forward handlers on network.
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
