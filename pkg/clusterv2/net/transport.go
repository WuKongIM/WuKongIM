package clusternet

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// TransportServerConfig configures a transport-backed clusterv2 RPC server.
type TransportServerConfig struct {
	// MaxPayload rejects inbound RPC payloads larger than this many bytes when positive.
	MaxPayload int
	// Server configures the underlying transport server.
	Server transport.ServerConfig
}

// TransportServer serves typed clusterv2 RPCs over pkg/transport.
type TransportServer struct {
	cfg    TransportServerConfig
	mux    *transport.RPCMux
	server *transport.Server
}

// NewTransportServer creates a TransportServer.
func NewTransportServer(cfg TransportServerConfig) *TransportServer {
	mux := transport.NewRPCMux()
	server := transport.NewServerWithConfig(cfg.Server)
	server.HandleRPCMux(mux)
	return &TransportServer{cfg: cfg, mux: mux, server: server}
}

// Register registers handler for serviceID.
func (s *TransportServer) Register(serviceID uint8, handler Handler) {
	s.mux.Handle(serviceID, func(ctx context.Context, payload []byte) ([]byte, error) {
		if s.cfg.MaxPayload > 0 && len(payload) > s.cfg.MaxPayload {
			return nil, fmt.Errorf("%w: payload too large", ErrInvalidFrame)
		}
		return handler.HandleRPC(ctx, payload)
	})
}

// Start starts the underlying TCP listener.
func (s *TransportServer) Start(addr string) error { return s.server.Start(addr) }

// Stop stops the underlying transport server.
func (s *TransportServer) Stop() { s.server.Stop() }

// Addr returns the bound listener address.
func (s *TransportServer) Addr() string {
	if s == nil || s.server == nil || s.server.Listener() == nil {
		return ""
	}
	return s.server.Listener().Addr().String()
}

// TransportClientConfig configures a transport-backed clusterv2 RPC client.
type TransportClientConfig struct {
	// Discovery resolves node IDs to addresses.
	Discovery transport.Discovery
	// PoolSize is the number of outbound connections per peer.
	PoolSize int
	// DialTimeout bounds outbound dials.
	DialTimeout time.Duration
}

// TransportClient sends typed clusterv2 RPCs over pkg/transport.
type TransportClient struct {
	pool   *transport.Pool
	client *transport.Client
}

// NewTransportClient creates a TransportClient.
func NewTransportClient(cfg TransportClientConfig) *TransportClient {
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = 1
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	pool := transport.NewPool(transport.PoolConfig{Discovery: cfg.Discovery, Size: cfg.PoolSize, DialTimeout: cfg.DialTimeout})
	return &TransportClient{pool: pool, client: transport.NewClient(pool)}
}

// Call invokes serviceID on nodeID.
func (c *TransportClient) Call(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	return c.client.RPCService(ctx, nodeID, 0, serviceID, payload)
}

// Send sends serviceID to nodeID without waiting for a response.
func (c *TransportClient) Send(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) error {
	return c.client.SendService(ctx, nodeID, 0, serviceID, payload)
}

// Stop closes outbound transport connections.
func (c *TransportClient) Stop() {
	if c != nil && c.client != nil {
		c.client.Stop()
	}
}
