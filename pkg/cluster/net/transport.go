package clusternet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

const defaultTransportQueueSize = 4096
const defaultTransportPoolSize = 16
const defaultTransportServiceConcurrency = 128
const defaultTransportForegroundWriteServiceConcurrency = 512
const orderedRaftServiceConcurrency = 1
const defaultTransportServiceQueueSize = 4096
const defaultTransportServiceMaxQueueBytes = 64 << 20

// TransportServerConfig configures a transport-backed cluster RPC server.
type TransportServerConfig struct {
	// NodeID is the local cluster node ID reported by transport observations.
	NodeID uint64
	// MaxPayload rejects inbound RPC payloads larger than this many bytes when positive.
	MaxPayload int
	// Service configures the bounded worker pool used for typed RPC services.
	// Foreground channel write services use a higher default concurrency when Concurrency is unset.
	Service TransportServiceConfig
	// Limits bounds inbound frame sizes, scheduler queues, batching, and write timeouts.
	Limits transport.Limits
	// Observer receives transport scheduler, peer, RPC, and service pressure observations.
	Observer transport.Observer
}

// TransportServiceConfig configures per-service transport worker pools.
type TransportServiceConfig struct {
	// Concurrency is the worker count for each registered typed RPC service. Non-positive values use the cluster default.
	// Raft protocol services always use one worker because peer message order is part of the protocol contract.
	Concurrency int
	// QueueSize is the queued request count for each typed RPC service. Non-positive values use the cluster default.
	QueueSize int
	// MaxQueueBytes is the queued payload byte budget for each typed RPC service. Non-positive values use the cluster default.
	MaxQueueBytes int64
	// Timeout bounds one handler invocation for each typed RPC service. Zero disables service-level handler timeouts.
	Timeout time.Duration
}

// TransportServer serves typed cluster RPCs over pkg/transport.
type TransportServer struct {
	cfg    TransportServerConfig
	server *transport.Server
}

// NewTransportServer creates a TransportServer.
func NewTransportServer(cfg TransportServerConfig) *TransportServer {
	limits := normalizeTransportLimits(cfg.Limits, 0)
	server, err := transport.NewServer(transport.ServerConfig{
		NodeID:   transport.NodeID(cfg.NodeID),
		Limits:   limits,
		Observer: cfg.Observer,
	})
	if err != nil {
		panic(fmt.Sprintf("cluster/net: create transport server: %v", err))
	}
	return &TransportServer{cfg: cfg, server: server}
}

// Register registers handler for serviceID.
func (s *TransportServer) Register(serviceID uint8, handler Handler) {
	if s == nil || s.server == nil {
		panic("cluster/net: nil transport server")
	}
	err := s.server.Handle(uint16(serviceID), func(ctx context.Context, payload []byte) ([]byte, error) {
		if s.cfg.MaxPayload > 0 && len(payload) > s.cfg.MaxPayload {
			return nil, fmt.Errorf("%w: payload too large", ErrInvalidFrame)
		}
		return handler.HandleRPC(ctx, payload)
	}, s.serviceOptions(serviceID))
	if err != nil {
		panic(fmt.Sprintf("cluster/net: register service %d: %v", serviceID, err))
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

// TransportClientConfig configures a transport-backed cluster RPC client.
type TransportClientConfig struct {
	// Discovery resolves node IDs to addresses.
	Discovery transport.Discovery
	// NodeID is the local cluster node ID reported by transport observations.
	NodeID uint64
	// PoolSize is the number of outbound connections per peer.
	PoolSize int
	// DialTimeout bounds outbound dials.
	DialTimeout time.Duration
	// QueueSize is the outbound frame queue size for each peer connection.
	// Non-positive values use the cluster default, which is sized to absorb
	// short foreground RPC fanout bursts before returning transport backpressure.
	QueueSize int
	// Limits bounds outbound frame sizes, scheduler queues, batching, and write timeouts.
	Limits transport.Limits
	// Observer receives transport scheduler, peer, RPC, and service pressure observations.
	Observer transport.Observer
}

// TransportClient sends typed cluster RPCs over pkg/transport.
type TransportClient struct {
	client   *transport.Client
	poolSize int
	limits   transport.Limits
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
	client, err := transport.NewClient(transport.ClientConfig{
		NodeID:      transport.NodeID(cfg.NodeID),
		Discovery:   discovery,
		PoolSize:    cfg.PoolSize,
		DialTimeout: cfg.DialTimeout,
		Limits:      limits,
		Observer:    cfg.Observer,
	})
	if err != nil {
		panic(fmt.Sprintf("cluster/net: create transport client: %v", err))
	}
	return &TransportClient{client: client, poolSize: cfg.PoolSize, limits: limits}
}

// Call invokes serviceID on nodeID.
func (c *TransportClient) Call(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	return c.CallShard(ctx, nodeID, serviceID, serviceShardKey(serviceID), payload)
}

// CallOwned invokes serviceID on nodeID and transfers payload ownership.
func (c *TransportClient) CallOwned(ctx context.Context, nodeID uint64, serviceID uint8, payload transport.OwnedBuffer) ([]byte, error) {
	return c.CallShardOwned(ctx, nodeID, serviceID, serviceShardKey(serviceID), payload)
}

// CallShard invokes serviceID on nodeID using shardKey for connection selection.
func (c *TransportClient) CallShard(ctx context.Context, nodeID uint64, serviceID uint8, shardKey uint64, payload []byte) ([]byte, error) {
	// gofail: var wkClusterNetCallShardFault string
	// if err := gofailClusterNetServiceFault(wkClusterNetCallShardFault, serviceID); err != nil { return nil, err }
	response, err := c.client.Call(ctx, transport.NodeID(nodeID), shardKey, servicePriority(serviceID), uint16(serviceID), payload)
	return response, translateTransportCallError(nodeID, serviceID, err)
}

// CallShardOwned invokes serviceID on nodeID using shardKey and transfers payload ownership.
func (c *TransportClient) CallShardOwned(ctx context.Context, nodeID uint64, serviceID uint8, shardKey uint64, payload transport.OwnedBuffer) ([]byte, error) {
	// gofail: var wkClusterNetCallShardOwnedFault string
	// if err := gofailClusterNetServiceFault(wkClusterNetCallShardOwnedFault, serviceID); err != nil { return nil, err }
	response, err := c.client.CallOwned(ctx, transport.NodeID(nodeID), shardKey, servicePriority(serviceID), uint16(serviceID), payload)
	return response, translateTransportCallError(nodeID, serviceID, err)
}

// Send sends serviceID to nodeID without waiting for a response.
func (c *TransportClient) Send(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) error {
	// gofail: var wkClusterNetSendFault string
	// if err := gofailClusterNetServiceFault(wkClusterNetSendFault, serviceID); err != nil { return err }
	return c.client.Notify(ctx, transport.NodeID(nodeID), serviceShardKey(serviceID), servicePriority(serviceID), uint16(serviceID), payload)
}

// SendOwned sends serviceID to nodeID and transfers payload ownership.
func (c *TransportClient) SendOwned(ctx context.Context, nodeID uint64, serviceID uint8, payload transport.OwnedBuffer) error {
	// gofail: var wkClusterNetSendOwnedFault string
	// if err := gofailClusterNetServiceFault(wkClusterNetSendOwnedFault, serviceID); err != nil { return err }
	return c.client.NotifyOwned(ctx, transport.NodeID(nodeID), serviceShardKey(serviceID), servicePriority(serviceID), uint16(serviceID), payload)
}

// PoolSize returns the configured outbound connection count per peer.
func (c *TransportClient) PoolSize() int {
	if c == nil {
		return 0
	}
	return c.poolSize
}

// Limits returns the effective transport connection limits.
func (c *TransportClient) Limits() transport.Limits {
	if c == nil {
		return transport.Limits{}
	}
	return c.limits
}

// Stats returns a point-in-time transport client stats snapshot.
func (c *TransportClient) Stats() transport.Stats {
	if c == nil || c.client == nil {
		return transport.Stats{}
	}
	return c.client.Stats()
}

// Stop closes outbound transport connections.
func (c *TransportClient) Stop() {
	if c != nil && c.client != nil {
		c.client.Stop()
	}
}

func translateTransportCallError(nodeID uint64, serviceID uint8, err error) error {
	if err == nil {
		return nil
	}
	var remoteErr transport.RemoteError
	if !errors.As(err, &remoteErr) {
		return err
	}
	if remoteErr.Code == transport.RemoteErrorCodeServiceNotFound ||
		(remoteErr.Code == transport.RemoteErrorCodeGeneric &&
			remoteErr.Message == fmt.Sprintf("transport: service %d not found", serviceID)) {
		return fmt.Errorf("%w: node %d service %d", ErrServiceNotFound, nodeID, serviceID)
	}
	return err
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

func servicePriority(serviceID uint8) transport.Priority {
	switch serviceID {
	case MsgSlotRaft, MsgSlotRaftBatch, RPCControlRaft:
		return transport.PriorityRaft
	case RPCChannelPull, RPCChannelPullBatch:
		return transport.PriorityBulk
	case RPCChannelAck, RPCChannelPullHint, RPCChannelPullHintBatch, RPCChannelNotify,
		RPCSlotForwardPropose, RPCControlStateSync, RPCControlReportNode, RPCControlReportSlots, RPCControlTaskResult, RPCControlWrite:
		return transport.PriorityControl
	default:
		return transport.PriorityRPC
	}
}

var errGofailClusterNetFault = errors.New("cluster/net: gofail injected service fault")

func gofailClusterNetServiceFault(raw string, serviceID uint8) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	alias, message, ok := strings.Cut(raw, ":")
	if !ok {
		return fmt.Errorf("%w: %s", errGofailClusterNetFault, raw)
	}
	alias = strings.TrimSpace(alias)
	message = strings.TrimSpace(message)
	if alias == "" {
		return nil
	}
	if alias != "all" && alias != transportServiceFailpointAlias(serviceID) {
		return nil
	}
	if message == "" {
		message = "injected"
	}
	return fmt.Errorf("%w: %s", errGofailClusterNetFault, message)
}

func transportServiceFailpointAlias(serviceID uint8) string {
	if serviceID == RPCControlWrite {
		return "control_write"
	}
	alias := transportServiceAlias(serviceID)
	if alias == "unknown service" {
		return ""
	}
	return strings.ReplaceAll(strings.ToLower(alias), " ", "_")
}

func (s *TransportServer) serviceOptions(serviceID uint8) transport.ServiceOptions {
	cfg := s.cfg.Service
	if isOrderedRaftService(serviceID) {
		// Each Raft sender uses one stable peer connection. Serial execution on
		// the receiver preserves that connection's Append-before-Heartbeat order;
		// concurrent handlers could otherwise commit beyond the follower log.
		cfg.Concurrency = orderedRaftServiceConcurrency
	} else if isForegroundChannelMutationService(serviceID) && cfg.Concurrency <= 0 {
		cfg.Concurrency = defaultTransportForegroundWriteServiceConcurrency
	}
	opts := normalizeTransportServiceOptions(cfg, s.cfg.MaxPayload)
	opts.Alias = transportServiceAlias(serviceID)
	return opts
}

func isOrderedRaftService(serviceID uint8) bool {
	switch serviceID {
	case MsgSlotRaft, MsgSlotRaftBatch, RPCControlRaft:
		return true
	default:
		return false
	}
}

func isForegroundChannelMutationService(serviceID uint8) bool {
	switch serviceID {
	case RPCChannelAppend, RPCChannelAppendBatch, RPCChannelAuthoritySend, RPCMessageEventAppend:
		return true
	default:
		return false
	}
}

func normalizeTransportLimits(limits transport.Limits, queueSize int) transport.Limits {
	if limits == (transport.Limits{}) {
		limits = transport.DefaultLimits()
		limits.MaxQueuedItemsPerConn = defaultTransportQueueSize
	} else if queueSize <= 0 && limits.MaxQueuedItemsPerConn <= 0 {
		limits.MaxQueuedItemsPerConn = defaultTransportQueueSize
	}
	if queueSize > 0 {
		limits.MaxQueuedItemsPerConn = queueSize
	}
	if limits.MaxQueuedBytesPerConn <= 0 {
		limits.MaxQueuedBytesPerConn = transport.DefaultLimits().MaxQueuedBytesPerConn
	}
	if limits.MaxFrameBodyBytes <= 0 {
		limits.MaxFrameBodyBytes = transport.DefaultLimits().MaxFrameBodyBytes
	}
	if limits.MaxBatchBytes <= 0 {
		limits.MaxBatchBytes = transport.DefaultLimits().MaxBatchBytes
	}
	if limits.MaxBatchFrames <= 0 {
		limits.MaxBatchFrames = transport.DefaultLimits().MaxBatchFrames
	}
	if limits.DialFailureCooldown < 0 || limits.WriteTimeout < 0 || limits.ReadIdleTimeout < 0 {
		panic("cluster/net: negative transport timeout")
	}
	if limits.DialFailureCooldown == 0 {
		limits.DialFailureCooldown = transport.DefaultLimits().DialFailureCooldown
	}
	if limits.WriteTimeout == 0 {
		limits.WriteTimeout = transport.DefaultLimits().WriteTimeout
	}
	if limits.MaxBatchBytes > limits.MaxFrameBodyBytes {
		limits.MaxBatchBytes = limits.MaxFrameBodyBytes
	}
	return limits
}

func normalizeTransportServiceOptions(cfg TransportServiceConfig, maxPayload int) transport.ServiceOptions {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = defaultTransportServiceConcurrency
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultTransportServiceQueueSize
	}
	if cfg.MaxQueueBytes <= 0 {
		cfg.MaxQueueBytes = defaultTransportServiceMaxQueueBytes
	}
	return transport.ServiceOptions{
		Concurrency:   cfg.Concurrency,
		QueueSize:     cfg.QueueSize,
		MaxQueueBytes: cfg.MaxQueueBytes,
		Timeout:       cfg.Timeout,
		MaxPayload:    maxPayload,
	}
}

type emptyTransportDiscovery struct{}

func (emptyTransportDiscovery) Resolve(nodeID transport.NodeID) (string, error) {
	return "", fmt.Errorf("%w: node %d", ErrNodeNotFound, nodeID)
}
