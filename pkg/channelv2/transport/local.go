package transport

import (
	"context"
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// LocalNetwork is an in-memory transport for v0 tests.
type LocalNetwork struct {
	mu       sync.RWMutex
	servers  map[ch.NodeID]Server
	DropPull map[ch.NodeID]bool
	DropAck  map[ch.NodeID]bool
}

// NewLocalNetwork creates an empty in-memory network.
func NewLocalNetwork() *LocalNetwork {
	return &LocalNetwork{servers: make(map[ch.NodeID]Server), DropPull: make(map[ch.NodeID]bool), DropAck: make(map[ch.NodeID]bool)}
}

// Register installs a node server.
func (n *LocalNetwork) Register(node ch.NodeID, server Server) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.servers[node] = server
}

// Client returns the network as a client.
func (n *LocalNetwork) Client() Client { return n }

// Pull calls a target node server.
func (n *LocalNetwork) Pull(ctx context.Context, node ch.NodeID, req PullRequest) (PullResponse, error) {
	n.mu.RLock()
	server := n.servers[node]
	drop := n.DropPull[node]
	n.mu.RUnlock()
	if drop {
		return PullResponse{}, ch.ErrNotReady
	}
	if server == nil {
		return PullResponse{}, ch.ErrChannelNotFound
	}
	return server.HandlePull(ctx, req)
}

// Ack calls a target node server.
func (n *LocalNetwork) Ack(ctx context.Context, node ch.NodeID, req AckRequest) error {
	n.mu.RLock()
	server := n.servers[node]
	drop := n.DropAck[node]
	n.mu.RUnlock()
	if drop {
		return ch.ErrNotReady
	}
	if server == nil {
		return ch.ErrChannelNotFound
	}
	return server.HandleAck(ctx, req)
}
