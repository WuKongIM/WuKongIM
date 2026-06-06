package clusternet

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

const defaultTransportQueueSize = 4096
const defaultTransportPoolSize = 16
const defaultTransportServiceConcurrency = 128
const defaultTransportAppendServiceConcurrency = 512
const defaultTransportServiceQueueSize = 4096
const defaultTransportServiceMaxQueueBytes = 64 << 20

// TransportServerConfig configures a transportv2-backed clusterv2 RPC server.
type TransportServerConfig struct {
	// NodeID is the local cluster node ID reported by transport observations.
	NodeID uint64
	// MaxPayload rejects inbound RPC payloads larger than this many bytes when positive.
	MaxPayload int
	// Service configures the bounded worker pool used for typed RPC services.
	// Append forward services use a higher default concurrency when Concurrency is unset.
	Service TransportServiceConfig
	// Limits bounds inbound frame sizes, scheduler queues, batching, and write timeouts.
	Limits transportv2.Limits
	// Observer receives transportv2 scheduler, peer, RPC, and service pressure observations.
	Observer transportv2.Observer
}

// TransportServiceConfig configures per-service transportv2 worker pools.
type TransportServiceConfig struct {
	// Concurrency is the worker count for each registered typed RPC service. Non-positive values use the clusterv2 default.
	Concurrency int
	// QueueSize is the queued request count for each typed RPC service. Non-positive values use the clusterv2 default.
	QueueSize int
	// MaxQueueBytes is the queued payload byte budget for each typed RPC service. Non-positive values use the clusterv2 default.
	MaxQueueBytes int64
	// Timeout bounds one handler invocation for each typed RPC service. Zero disables service-level handler timeouts.
	Timeout time.Duration
}

// TransportServer serves typed clusterv2 RPCs over pkg/transportv2.
type TransportServer struct {
	cfg    TransportServerConfig
	server *transportv2.Server
}

// NewTransportServer creates a TransportServer.
func NewTransportServer(cfg TransportServerConfig) *TransportServer {
	limits := normalizeTransportLimits(cfg.Limits, 0)
	server, err := transportv2.NewServer(transportv2.ServerConfig{
		NodeID:   transportv2.NodeID(cfg.NodeID),
		Limits:   limits,
		Observer: cfg.Observer,
	})
	if err != nil {
		panic(fmt.Sprintf("clusterv2/net: create transportv2 server: %v", err))
	}
	return &TransportServer{cfg: cfg, server: server}
}

// Register registers handler for serviceID.
func (s *TransportServer) Register(serviceID uint8, handler Handler) {
	if s == nil || s.server == nil {
		panic("clusterv2/net: nil transport server")
	}
	err := s.server.Handle(uint16(serviceID), func(ctx context.Context, payload []byte) ([]byte, error) {
		if s.cfg.MaxPayload > 0 && len(payload) > s.cfg.MaxPayload {
			return nil, fmt.Errorf("%w: payload too large", ErrInvalidFrame)
		}
		return handler.HandleRPC(ctx, payload)
	}, s.serviceOptions(serviceID))
	if err != nil {
		panic(fmt.Sprintf("clusterv2/net: register service %d: %v", serviceID, err))
	}
}

// Start starts the underlying TCP listener.
func (s *TransportServer) Start(addr string) error { return s.server.ListenAndServe(addr) }

// Stop stops the underlying transport server.
func (s *TransportServer) Stop() { s.server.Stop() }

// Addr returns the bound listener address.
func (s *TransportServer) Addr() string {
	if s == nil || s.server == nil {
		return ""
	}
	return s.server.Addr()
}

// TransportClientConfig configures a transportv2-backed clusterv2 RPC client.
type TransportClientConfig struct {
	// Discovery resolves node IDs to addresses.
	Discovery transportv2.Discovery
	// NodeID is the local cluster node ID reported by transport observations.
	NodeID uint64
	// PoolSize is the number of outbound connections per peer.
	PoolSize int
	// DialTimeout bounds outbound dials.
	DialTimeout time.Duration
	// QueueSize is the outbound frame queue size for each peer connection.
	// Non-positive values use the clusterv2 default, which is sized to absorb
	// short foreground RPC fanout bursts before returning transportv2 backpressure.
	QueueSize int
	// Limits bounds outbound frame sizes, scheduler queues, batching, and write timeouts.
	Limits transportv2.Limits
	// Observer receives transportv2 scheduler, peer, RPC, and service pressure observations.
	Observer transportv2.Observer
}

// TransportClient sends typed clusterv2 RPCs over pkg/transportv2.
type TransportClient struct {
	client   *transportv2.Client
	poolSize int
	limits   transportv2.Limits
}

// NewTransportClient creates a TransportClient.
func NewTransportClient(cfg TransportClientConfig) *TransportClient {
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = defaultTransportPoolSize
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	discovery := cfg.Discovery
	if discovery == nil {
		discovery = emptyTransportDiscovery{}
	}
	limits := normalizeTransportLimits(cfg.Limits, cfg.QueueSize)
	client, err := transportv2.NewClient(transportv2.ClientConfig{
		NodeID:      transportv2.NodeID(cfg.NodeID),
		Discovery:   discovery,
		PoolSize:    cfg.PoolSize,
		DialTimeout: cfg.DialTimeout,
		Limits:      limits,
		Observer:    cfg.Observer,
	})
	if err != nil {
		panic(fmt.Sprintf("clusterv2/net: create transportv2 client: %v", err))
	}
	return &TransportClient{client: client, poolSize: cfg.PoolSize, limits: limits}
}

// Call invokes serviceID on nodeID.
func (c *TransportClient) Call(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	return c.CallShard(ctx, nodeID, serviceID, serviceShardKey(serviceID), payload)
}

// CallShard invokes serviceID on nodeID using shardKey for connection selection.
func (c *TransportClient) CallShard(ctx context.Context, nodeID uint64, serviceID uint8, shardKey uint64, payload []byte) ([]byte, error) {
	return c.client.Call(ctx, transportv2.NodeID(nodeID), shardKey, servicePriority(serviceID), uint16(serviceID), payload)
}

// Send sends serviceID to nodeID without waiting for a response.
func (c *TransportClient) Send(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) error {
	return c.client.Notify(ctx, transportv2.NodeID(nodeID), serviceShardKey(serviceID), servicePriority(serviceID), uint16(serviceID), payload)
}

// PoolSize returns the configured outbound connection count per peer.
func (c *TransportClient) PoolSize() int {
	if c == nil {
		return 0
	}
	return c.poolSize
}

// Limits returns the effective transportv2 connection limits.
func (c *TransportClient) Limits() transportv2.Limits {
	if c == nil {
		return transportv2.Limits{}
	}
	return c.limits
}

// Stats returns a point-in-time transportv2 client stats snapshot.
func (c *TransportClient) Stats() transportv2.Stats {
	if c == nil || c.client == nil {
		return transportv2.Stats{}
	}
	return c.client.Stats()
}

// Stop closes outbound transport connections.
func (c *TransportClient) Stop() {
	if c != nil && c.client != nil {
		c.client.Stop()
	}
}

func serviceShardKey(serviceID uint8) uint64 {
	switch serviceID {
	case RPCChannelAppend, RPCChannelAppendBatch:
		return 0
	case RPCChannelPull, RPCChannelPullBatch:
		return 1
	case RPCChannelAck, RPCChannelPullHint, RPCChannelPullHintBatch, RPCChannelNotify:
		return 2
	default:
		return uint64(serviceID) + 3
	}
}

func servicePriority(serviceID uint8) transportv2.Priority {
	switch serviceID {
	case MsgSlotRaft, MsgSlotRaftBatch, RPCControlRaft:
		return transportv2.PriorityRaft
	case RPCChannelPull, RPCChannelPullBatch:
		return transportv2.PriorityBulk
	case RPCChannelAck, RPCChannelPullHint, RPCChannelPullHintBatch, RPCChannelNotify,
		RPCSlotForwardPropose, RPCControlStateSync, RPCControlReportNode, RPCControlReportSlots:
		return transportv2.PriorityControl
	default:
		return transportv2.PriorityRPC
	}
}

func (s *TransportServer) serviceOptions(serviceID uint8) transportv2.ServiceOptions {
	cfg := s.cfg.Service
	if isAppendForwardService(serviceID) && cfg.Concurrency <= 0 {
		cfg.Concurrency = defaultTransportAppendServiceConcurrency
	}
	return normalizeTransportServiceOptions(cfg, s.cfg.MaxPayload)
}

func isAppendForwardService(serviceID uint8) bool {
	switch serviceID {
	case RPCChannelAppend, RPCChannelAppendBatch:
		return true
	default:
		return false
	}
}

func normalizeTransportLimits(limits transportv2.Limits, queueSize int) transportv2.Limits {
	if limits == (transportv2.Limits{}) {
		limits = transportv2.DefaultLimits()
		limits.MaxQueuedItemsPerConn = defaultTransportQueueSize
	} else if queueSize <= 0 && limits.MaxQueuedItemsPerConn <= 0 {
		limits.MaxQueuedItemsPerConn = defaultTransportQueueSize
	}
	if queueSize > 0 {
		limits.MaxQueuedItemsPerConn = queueSize
	}
	if limits.MaxQueuedBytesPerConn <= 0 {
		limits.MaxQueuedBytesPerConn = transportv2.DefaultLimits().MaxQueuedBytesPerConn
	}
	if limits.MaxFrameBodyBytes <= 0 {
		limits.MaxFrameBodyBytes = transportv2.DefaultLimits().MaxFrameBodyBytes
	}
	if limits.MaxBatchBytes <= 0 {
		limits.MaxBatchBytes = transportv2.DefaultLimits().MaxBatchBytes
	}
	if limits.MaxBatchFrames <= 0 {
		limits.MaxBatchFrames = transportv2.DefaultLimits().MaxBatchFrames
	}
	if limits.DialFailureCooldown < 0 || limits.WriteTimeout < 0 || limits.ReadIdleTimeout < 0 {
		panic("clusterv2/net: negative transportv2 timeout")
	}
	if limits.DialFailureCooldown == 0 {
		limits.DialFailureCooldown = transportv2.DefaultLimits().DialFailureCooldown
	}
	if limits.WriteTimeout == 0 {
		limits.WriteTimeout = transportv2.DefaultLimits().WriteTimeout
	}
	if limits.MaxBatchBytes > limits.MaxFrameBodyBytes {
		limits.MaxBatchBytes = limits.MaxFrameBodyBytes
	}
	return limits
}

func normalizeTransportServiceOptions(cfg TransportServiceConfig, maxPayload int) transportv2.ServiceOptions {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = defaultTransportServiceConcurrency
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultTransportServiceQueueSize
	}
	if cfg.MaxQueueBytes <= 0 {
		cfg.MaxQueueBytes = defaultTransportServiceMaxQueueBytes
	}
	return transportv2.ServiceOptions{
		Concurrency:   cfg.Concurrency,
		QueueSize:     cfg.QueueSize,
		MaxQueueBytes: cfg.MaxQueueBytes,
		Timeout:       cfg.Timeout,
		MaxPayload:    maxPayload,
	}
}

type emptyTransportDiscovery struct{}

func (emptyTransportDiscovery) Resolve(nodeID transportv2.NodeID) (string, error) {
	return "", fmt.Errorf("%w: node %d", ErrNodeNotFound, nodeID)
}
