package raft

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	defaultTickInterval = 100 * time.Millisecond
	electionTick        = 10
	heartbeatTick       = 1
)

// Peer identifies one ControllerV2 Raft voter.
type Peer struct {
	// NodeID is the non-zero stable node identity of the voter.
	NodeID uint64
	// Addr is the stable Controller address associated with the voter.
	Addr string
}

// Transport sends outbound ControllerV2 Raft messages to peer nodes.
type Transport interface {
	Send(context.Context, []raftpb.Message) error
}

// Config configures one ControllerV2 Raft service instance.
type Config struct {
	// NodeID is the local ControllerV2 Raft node ID.
	NodeID uint64
	// Peers lists the ControllerV2 Raft voters used for bootstrap.
	Peers []Peer
	// AllowBootstrap permits this node to initialize a brand-new ControllerV2 Raft log.
	AllowBootstrap bool
	// Storage persists the ControllerV2 Raft hard state, log entries, snapshots, and applied index.
	Storage multiraft.Storage
	// StateMachine applies committed ControllerV2 commands to cluster-state.json.
	StateMachine *fsm.StateMachine
	// Transport delivers outbound Raft protocol messages to peer services.
	Transport Transport
	// TickInterval controls the wall-clock interval between Raft ticks; zero uses the default.
	TickInterval time.Duration
}

func (c Config) normalized() Config {
	if c.TickInterval == 0 {
		c.TickInterval = defaultTickInterval
	}
	c.Peers = normalizePeers(c.Peers)
	return c
}

func (c Config) validate() error {
	if c.NodeID == 0 {
		return fmt.Errorf("%w: node id must be > 0", ErrInvalidConfig)
	}
	if len(c.Peers) == 0 {
		return fmt.Errorf("%w: peers must not be empty", ErrInvalidConfig)
	}
	seen := make(map[uint64]struct{}, len(c.Peers))
	selfFound := false
	for _, peer := range c.Peers {
		if peer.NodeID == 0 {
			return fmt.Errorf("%w: peer node id must be > 0", ErrInvalidConfig)
		}
		if peer.Addr == "" {
			return fmt.Errorf("%w: peer addr must not be empty", ErrInvalidConfig)
		}
		if _, ok := seen[peer.NodeID]; ok {
			return fmt.Errorf("%w: duplicate peer %d", ErrInvalidConfig, peer.NodeID)
		}
		seen[peer.NodeID] = struct{}{}
		if peer.NodeID == c.NodeID {
			selfFound = true
		}
	}
	if !selfFound {
		return fmt.Errorf("%w: local node %d missing from peers", ErrInvalidConfig, c.NodeID)
	}
	if c.Storage == nil {
		return fmt.Errorf("%w: storage must not be nil", ErrInvalidConfig)
	}
	if c.StateMachine == nil {
		return fmt.Errorf("%w: state machine must not be nil", ErrInvalidConfig)
	}
	if c.Transport == nil {
		return fmt.Errorf("%w: transport must not be nil", ErrInvalidConfig)
	}
	if c.TickInterval <= 0 {
		return fmt.Errorf("%w: tick interval must be > 0", ErrInvalidConfig)
	}
	return nil
}

func normalizePeers(peers []Peer) []Peer {
	out := append([]Peer(nil), peers...)
	slices.SortFunc(out, func(left, right Peer) int {
		switch {
		case left.NodeID < right.NodeID:
			return -1
		case left.NodeID > right.NodeID:
			return 1
		default:
			return 0
		}
	})
	return out
}
