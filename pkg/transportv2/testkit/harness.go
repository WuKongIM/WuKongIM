package testkit

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

const (
	// ClientNodeID is the default local client node used by Harness.
	ClientNodeID transportv2.NodeID = 1
	// ServerNodeID is the default local server node used by Harness.
	ServerNodeID transportv2.NodeID = 2
	// ServiceID is the default service registered by Harness.
	ServiceID uint16 = 7
)

// StaticDiscovery resolves a fixed set of node IDs to transport addresses.
type StaticDiscovery map[transportv2.NodeID]string

// Resolve returns the address registered for nodeID.
func (d StaticDiscovery) Resolve(nodeID transportv2.NodeID) (string, error) {
	addr, ok := d[nodeID]
	if !ok {
		return "", fmt.Errorf("%w: %d", transportv2.ErrNodeNotFound, nodeID)
	}
	return addr, nil
}

// Harness owns a local transportv2 server/client pair for pressure tests.
type Harness struct {
	// Server is the local inbound transport endpoint using ServerNodeID.
	Server *transportv2.Server
	// Client is the local outbound transport endpoint using ClientNodeID.
	Client *transportv2.Client
}

// NewHarness creates a local server with service 7 and a client with pool size 4.
func NewHarness(tb testing.TB, handler transportv2.Handler) *Harness {
	tb.Helper()
	if handler == nil {
		handler = func(ctx context.Context, payload []byte) ([]byte, error) {
			return payload, nil
		}
	}

	server, err := transportv2.NewServer(transportv2.ServerConfig{
		NodeID: ServerNodeID,
		Limits: transportv2.DefaultLimits(),
	})
	if err != nil {
		tb.Fatalf("NewServer() error = %v", err)
	}

	if err := server.Handle(ServiceID, handler, transportv2.ServiceOptions{
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

	client, err := transportv2.NewClient(transportv2.ClientConfig{
		NodeID:    ClientNodeID,
		Discovery: StaticDiscovery{ServerNodeID: server.Addr()},
		PoolSize:  4,
		Limits:    transportv2.DefaultLimits(),
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
