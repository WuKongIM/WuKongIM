// Package peer manages outbound transport connections grouped by remote node.
package peer

import (
	"context"
	"errors"
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
	// slots are the connection slots for this peer generation.
	slots []*slotState
	// closed marks this peer generation terminal after ClosePeer or Stop.
	closed bool
}

type slotState struct {
	// conn is the installed connection for this slot, when live.
	conn *conn.Conn

	// dialing is true while the slot has an in-flight owner dial.
	dialing bool
	// ready is closed when the current owner dial reaches a terminal state.
	ready chan struct{}
	// readyClosed prevents double close when ClosePeer races with owner dial return.
	readyClosed bool
	// closed marks this slot generation terminal after ClosePeer or Stop.
	closed bool

	// lastErr is the reusable non-context dial failure returned during cooldown.
	lastErr error
	// lastDialFail records when lastErr occurred for cooldown enforcement.
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
		peer, slot := m.slotLocked(nodeID, slotIndex)
		if slot.conn != nil {
			if connDone(slot.conn) {
				slot.conn = nil
			} else {
				c := slot.conn
				m.mu.Unlock()
				return c, nil
			}
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
		slot.readyClosed = false
		m.mu.Unlock()

		c, err := m.dialConn(ctx, nodeID)

		m.mu.Lock()
		installed := m.slotInstalledLocked(nodeID, slotIndex, peer, slot)
		if err != nil {
			if installed {
				if !isContextError(err) {
					slot.lastErr = err
					slot.lastDialFail = time.Now()
				}
				slot.dialing = false
				m.notifyReadyLocked(slot)
			}
			m.mu.Unlock()
			return nil, err
		}
		if !installed {
			m.mu.Unlock()
			c.Close(core.ErrStopped)
			return nil, core.ErrStopped
		}
		slot.conn = c
		slot.lastErr = nil
		slot.lastDialFail = time.Time{}
		slot.dialing = false
		m.notifyReadyLocked(slot)
		m.mu.Unlock()
		return c, nil
	}
}

// ClosePeer closes all slots for nodeID and removes the peer from the manager.
func (m *Manager) ClosePeer(nodeID core.NodeID) {
	var conns []*conn.Conn
	m.mu.Lock()
	if peer := m.peers[nodeID]; peer != nil {
		peer.closed = true
		for _, slot := range peer.slots {
			if slot.conn != nil {
				conns = append(conns, slot.conn)
				slot.conn = nil
			}
			slot.closed = true
			slot.dialing = false
			m.notifyReadyLocked(slot)
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
		peer.closed = true
		for _, slot := range peer.slots {
			if slot.conn != nil {
				conns = append(conns, slot.conn)
				slot.conn = nil
			}
			slot.closed = true
			slot.dialing = false
			m.notifyReadyLocked(slot)
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
			if slot.conn != nil && !connDone(slot.conn) {
				stats.Connections++
			}
		}
	}
	return stats
}

func (m *Manager) slotLocked(nodeID core.NodeID, slotIndex int) (*peerState, *slotState) {
	peer := m.peers[nodeID]
	if peer == nil {
		peer = &peerState{slots: make([]*slotState, m.cfg.PoolSize)}
		for i := range peer.slots {
			peer.slots[i] = &slotState{}
		}
		m.peers[nodeID] = peer
	}
	return peer, peer.slots[slotIndex]
}

func (m *Manager) inDialCooldownLocked(slot *slotState) bool {
	if slot.lastErr == nil || m.cfg.Limits.DialFailureCooldown <= 0 || slot.lastDialFail.IsZero() {
		return false
	}
	return time.Since(slot.lastDialFail) < m.cfg.Limits.DialFailureCooldown
}

func (m *Manager) slotInstalledLocked(nodeID core.NodeID, slotIndex int, peer *peerState, slot *slotState) bool {
	if m.stopped || peer.closed || slot.closed {
		return false
	}
	return m.peers[nodeID] == peer && peer.slots[slotIndex] == slot
}

func (m *Manager) notifyReadyLocked(slot *slotState) {
	if slot.ready == nil || slot.readyClosed {
		return
	}
	close(slot.ready)
	slot.readyClosed = true
}

func (m *Manager) dialConn(ctx context.Context, nodeID core.NodeID) (*conn.Conn, error) {
	addr, err := m.cfg.Discovery.Resolve(nodeID)
	if err != nil {
		return nil, err
	}
	raw, err := m.cfg.Dial(ctx, "tcp", addr)
	if err != nil {
		if isContextError(err) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", core.ErrDialFailed, err)
	}
	c := conn.New(raw, conn.Config{Limits: m.cfg.Limits}, nil)
	c.Start()
	return c, nil
}

func connDone(c *conn.Conn) bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
