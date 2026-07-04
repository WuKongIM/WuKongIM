package testkit

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

const (
	// ClientNodeID is the default local client node used by Harness.
	ClientNodeID transport.NodeID = 1
	// ServerNodeID is the default local server node used by Harness.
	ServerNodeID transport.NodeID = 2
	// ServiceID is the default service registered by Harness.
	ServiceID uint16 = 7
)

// StaticDiscovery resolves a fixed set of node IDs to transport addresses.
type StaticDiscovery map[transport.NodeID]string

// Resolve returns the address registered for nodeID.
func (d StaticDiscovery) Resolve(nodeID transport.NodeID) (string, error) {
	addr, ok := d[nodeID]
	if !ok {
		return "", fmt.Errorf("%w: %d", transport.ErrNodeNotFound, nodeID)
	}
	return addr, nil
}

// Harness owns a local transport server/client pair for pressure tests.
type Harness struct {
	// Server is the local inbound transport endpoint using ServerNodeID.
	Server *transport.Server
	// Client is the local outbound transport endpoint using ClientNodeID.
	Client *transport.Client
}

// NewHarness creates a local server with service 7 and a client with pool size 4.
func NewHarness(tb testing.TB, handler transport.Handler) *Harness {
	tb.Helper()
	if handler == nil {
		handler = func(ctx context.Context, payload []byte) ([]byte, error) {
			return payload, nil
		}
	}

	server, err := transport.NewServer(transport.ServerConfig{
		NodeID: ServerNodeID,
		Limits: transport.DefaultLimits(),
	})
	if err != nil {
		tb.Fatalf("NewServer() error = %v", err)
	}

	if err := server.Handle(ServiceID, handler, transport.ServiceOptions{
		Concurrency:   4,
		QueueSize:     1024,
		MaxQueueBytes: 8 << 20,
	}); err != nil {
		server.Stop()
		tb.Fatalf("Handle() error = %v", err)
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		server.Stop()
		tb.Fatalf("ListenAndServe() error = %v", err)
	}

	client, err := transport.NewClient(transport.ClientConfig{
		NodeID:    ClientNodeID,
		Discovery: StaticDiscovery{ServerNodeID: server.Addr()},
		PoolSize:  4,
		Limits:    transport.DefaultLimits(),
	})
	if err != nil {
		server.Stop()
		tb.Fatalf("NewClient() error = %v", err)
	}

	return &Harness{
		Server: server,
		Client: client,
	}
}

// Close stops the harness client and server.
func (h *Harness) Close() {
	if h == nil {
		return
	}
	if h.Client != nil {
		h.Client.Stop()
	}
	if h.Server != nil {
		h.Server.Stop()
	}
}
