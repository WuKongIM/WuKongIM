// Package peer manages outbound transport connections grouped by remote node.
package peer

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/conn"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

// Config configures outbound peer connection management.
type Config struct {
	// Discovery resolves remote node IDs to dialable addresses.
	Discovery core.Discovery
	// PoolSize is the number of connection slots maintained per peer.
	PoolSize int
	// Limits bounds each managed connection.
	Limits core.Limits
	// Dial creates a raw network connection to a resolved address.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Manager owns per-node pools of outbound connection slots.
type Manager struct {
	cfg Config

	mu      sync.Mutex
	stopped bool
	peers   map[core.NodeID]*peerState
}

type peerState struct {
	slots []*slotState
}

type slotState struct {
	conn *conn.Conn

	dialing bool
	ready   chan struct{}

	lastErr      error
	lastDialFail time.Time
}

// NewManager creates a manager with normalized pool and dial defaults.
func NewManager(cfg Config) *Manager {
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = 1
	}
	if cfg.Dial == nil {
		var dialer net.Dialer
		cfg.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		}
	}
	return &Manager{
		cfg:   cfg,
		peers: make(map[core.NodeID]*peerState),
	}
}

// Acquire returns the connection slot selected by shardKey for nodeID.
func (m *Manager) Acquire(ctx context.Context, nodeID core.NodeID, shardKey uint64) (*conn.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.cfg.Discovery == nil {
		return nil, fmt.Errorf("%w: discovery is required", core.ErrInvalidConfig)
	}
	slotIndex := int(shardKey % uint64(m.cfg.PoolSize))

	for {
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return nil, core.ErrStopped
		}
		slot := m.slotLocked(nodeID, slotIndex)
		if slot.conn != nil {
			c := slot.conn
			m.mu.Unlock()
			return c, nil
		}
		if m.inDialCooldownLocked(slot) {
			err := slot.lastErr
			m.mu.Unlock()
			return nil, err
		}
		if slot.dialing {
			ready := slot.ready
			m.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		slot.dialing = true
		slot.ready = make(chan struct{})
		ready := slot.ready
		m.mu.Unlock()

		c, err := m.dialConn(ctx, nodeID)

		m.mu.Lock()
		if err != nil {
			slot.lastErr = err
			slot.lastDialFail = time.Now()
			slot.dialing = false
			close(ready)
			m.mu.Unlock()
			return nil, err
		}
		if m.stopped {
			slot.dialing = false
			close(ready)
			m.mu.Unlock()
			c.Close(core.ErrStopped)
			return nil, core.ErrStopped
		}
		slot.conn = c
		slot.lastErr = nil
		slot.lastDialFail = time.Time{}
		slot.dialing = false
		close(ready)
		m.mu.Unlock()
		return c, nil
	}
}

// ClosePeer closes all slots for nodeID and removes the peer from the manager.
func (m *Manager) ClosePeer(nodeID core.NodeID) {
	var conns []*conn.Conn
	m.mu.Lock()
	if peer := m.peers[nodeID]; peer != nil {
		for _, slot := range peer.slots {
			if slot.conn != nil {
				conns = append(conns, slot.conn)
			}
		}
		delete(m.peers, nodeID)
	}
	m.mu.Unlock()

	for _, c := range conns {
		c.Close(core.ErrStopped)
	}
}

// Stop closes all managed connections and prevents later Acquire calls.
func (m *Manager) Stop() {
	var conns []*conn.Conn
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	for _, peer := range m.peers {
		for _, slot := range peer.slots {
			if slot.conn != nil {
				conns = append(conns, slot.conn)
			}
		}
	}
	m.peers = make(map[core.NodeID]*peerState)
	m.mu.Unlock()

	for _, c := range conns {
		c.Close(core.ErrStopped)
	}
}

// Stats returns a point-in-time snapshot of managed peers and live slots.
func (m *Manager) Stats() core.Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	stats := core.Stats{Peers: len(m.peers)}
	for _, peer := range m.peers {
		for _, slot := range peer.slots {
			if slot.conn != nil {
				stats.Connections++
			}
		}
	}
	return stats
}

func (m *Manager) slotLocked(nodeID core.NodeID, slotIndex int) *slotState {
	peer := m.peers[nodeID]
	if peer == nil {
		peer = &peerState{slots: make([]*slotState, m.cfg.PoolSize)}
		for i := range peer.slots {
			peer.slots[i] = &slotState{}
		}
		m.peers[nodeID] = peer
	}
	return peer.slots[slotIndex]
}

func (m *Manager) inDialCooldownLocked(slot *slotState) bool {
	if slot.lastErr == nil || m.cfg.Limits.DialFailureCooldown <= 0 || slot.lastDialFail.IsZero() {
		return false
	}
	return time.Since(slot.lastDialFail) < m.cfg.Limits.DialFailureCooldown
}

func (m *Manager) dialConn(ctx context.Context, nodeID core.NodeID) (*conn.Conn, error) {
	addr, err := m.cfg.Discovery.Resolve(nodeID)
	if err != nil {
		return nil, err
	}
	raw, err := m.cfg.Dial(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", core.ErrDialFailed, err)
	}
	c := conn.New(raw, conn.Config{Limits: m.cfg.Limits}, nil)
	c.Start()
	return c, nil
}
