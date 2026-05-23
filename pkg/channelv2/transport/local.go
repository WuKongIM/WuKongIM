package transport

import (
	"context"
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// LocalNetwork is an in-memory transport for v0 tests.
type LocalNetwork struct {
	mu sync.RWMutex
	// servers maps node ids to in-memory RPC handlers.
	servers map[ch.NodeID]Server
	// DropPull marks target nodes whose pull RPCs should fail; use SetDropPull during concurrent tests.
	DropPull map[ch.NodeID]bool
	// DropAck marks target nodes whose ACK RPCs should fail; use SetDropAck during concurrent tests.
	DropAck map[ch.NodeID]bool
	// DropPullHint marks target nodes whose pull hint RPCs should fail; use SetDropPullHint during concurrent tests.
	DropPullHint map[ch.NodeID]bool
	// DropNotify marks target nodes whose legacy notify RPCs should fail.
	//
	// Deprecated: use DropPullHint or SetDropPullHint for new pull hint tests.
	DropNotify map[ch.NodeID]bool
	// droppedPulls counts pull RPCs dropped by target node.
	droppedPulls map[ch.NodeID]int
	// droppedAcks counts ACK RPCs dropped by target node.
	droppedAcks map[ch.NodeID]int
	// droppedPullHints counts pull hint RPCs dropped by target node.
	droppedPullHints map[ch.NodeID]int
}

// NewLocalNetwork creates an empty in-memory network.
func NewLocalNetwork() *LocalNetwork {
	return &LocalNetwork{
		servers:          make(map[ch.NodeID]Server),
		DropPull:         make(map[ch.NodeID]bool),
		DropAck:          make(map[ch.NodeID]bool),
		DropPullHint:     make(map[ch.NodeID]bool),
		DropNotify:       make(map[ch.NodeID]bool),
		droppedPulls:     make(map[ch.NodeID]int),
		droppedAcks:      make(map[ch.NodeID]int),
		droppedPullHints: make(map[ch.NodeID]int),
	}
}

// Register installs a node server.
func (n *LocalNetwork) Register(node ch.NodeID, server Server) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.servers[node] = server
}

// SetDropPull toggles pull RPC drops for calls targeting node.
func (n *LocalNetwork) SetDropPull(node ch.NodeID, drop bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if drop {
		n.DropPull[node] = true
		return
	}
	delete(n.DropPull, node)
}

// SetDropAck toggles ACK RPC drops for calls targeting node.
func (n *LocalNetwork) SetDropAck(node ch.NodeID, drop bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if drop {
		n.DropAck[node] = true
		return
	}
	delete(n.DropAck, node)
}

// SetDropPullHint toggles pull hint RPC drops for calls targeting node.
func (n *LocalNetwork) SetDropPullHint(node ch.NodeID, drop bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if drop {
		n.DropPullHint[node] = true
		return
	}
	delete(n.DropPullHint, node)
}

// SetDropNotify toggles legacy notify RPC drops for calls targeting node.
func (n *LocalNetwork) SetDropNotify(node ch.NodeID, drop bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if drop {
		n.DropNotify[node] = true
		return
	}
	delete(n.DropNotify, node)
}

// DroppedPulls returns how many pull RPCs were dropped for calls targeting node.
func (n *LocalNetwork) DroppedPulls(node ch.NodeID) int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.droppedPulls[node]
}

// DroppedAcks returns how many ACK RPCs were dropped for calls targeting node.
func (n *LocalNetwork) DroppedAcks(node ch.NodeID) int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.droppedAcks[node]
}

// DroppedPullHints returns how many pull hint RPCs were dropped for calls targeting node.
func (n *LocalNetwork) DroppedPullHints(node ch.NodeID) int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.droppedPullHints[node]
}

// Client returns the network as a client.
func (n *LocalNetwork) Client() Client { return n }

// Pull calls a target node server.
func (n *LocalNetwork) Pull(ctx context.Context, node ch.NodeID, req PullRequest) (PullResponse, error) {
	n.mu.Lock()
	server := n.servers[node]
	drop := n.DropPull[node]
	if drop {
		n.droppedPulls[node]++
	}
	n.mu.Unlock()
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
	n.mu.Lock()
	server := n.servers[node]
	drop := n.DropAck[node]
	if drop {
		n.droppedAcks[node]++
	}
	n.mu.Unlock()
	if drop {
		return ch.ErrNotReady
	}
	if server == nil {
		return ch.ErrChannelNotFound
	}
	return server.HandleAck(ctx, req)
}

// PullHint calls a target node server with a best-effort replication nudge.
func (n *LocalNetwork) PullHint(ctx context.Context, node ch.NodeID, req PullHintRequest) error {
	n.mu.Lock()
	server := n.servers[node]
	drop := n.DropPullHint[node]
	if drop {
		n.droppedPullHints[node]++
	}
	n.mu.Unlock()
	if drop {
		return ch.ErrNotReady
	}
	if server == nil {
		return ch.ErrChannelNotFound
	}
	return server.HandlePullHint(ctx, req)
}

// Notify calls a target node server through the legacy notification path.
func (n *LocalNetwork) Notify(ctx context.Context, node ch.NodeID, req NotifyRequest) error {
	n.mu.Lock()
	server := n.servers[node]
	drop := n.DropPullHint[node] || n.DropNotify[node]
	if drop {
		n.droppedPullHints[node]++
	}
	n.mu.Unlock()
	if drop {
		return ch.ErrNotReady
	}
	if server == nil {
		return ch.ErrChannelNotFound
	}
	return server.HandleNotify(ctx, req)
}
