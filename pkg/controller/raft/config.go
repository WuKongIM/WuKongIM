package raft

import (
	"fmt"
	"slices"

	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type Peer struct {
	NodeID uint64
	Addr   string
}

type LeaderChangeObserver func(from, to uint64)

type CommittedCommandObserver func(slotcontroller.Command)

type Config struct {
	NodeID             uint64
	Peers              []Peer
	AllowBootstrap     bool
	LogDB              *raftstorage.DB
	StateMachine       *slotcontroller.StateMachine
	Server             *transport.Server
	RPCMux             *transport.RPCMux
	Pool               *transport.Pool
	Logger             wklog.Logger
	OnLeaderChange     LeaderChangeObserver
	OnCommittedCommand CommittedCommandObserver
}

func (c Config) validateCore() error {
	if c.NodeID == 0 {
		return fmt.Errorf("%w: node id must be > 0", ErrInvalidConfig)
	}
	if c.LogDB == nil {
		return fmt.Errorf("%w: log db must not be nil", ErrInvalidConfig)
	}
	if c.StateMachine == nil {
		return fmt.Errorf("%w: state machine must not be nil", ErrInvalidConfig)
	}
	if c.Server == nil {
		return fmt.Errorf("%w: server must not be nil", ErrInvalidConfig)
	}
	if c.RPCMux == nil {
		return fmt.Errorf("%w: rpc mux must not be nil", ErrInvalidConfig)
	}
	if c.Pool == nil {
		return fmt.Errorf("%w: pool must not be nil", ErrInvalidConfig)
	}
	return nil
}

func (c Config) validateBootstrapPeers() error {
	if len(c.Peers) == 0 {
		return fmt.Errorf("%w: peers must not be empty", ErrInvalidConfig)
	}
	seen := make(map[uint64]struct{}, len(c.Peers))
	selfFound := false
	for _, peer := range c.Peers {
		if peer.NodeID == 0 || peer.Addr == "" {
			return fmt.Errorf("%w: peer node id and addr must be set", ErrInvalidConfig)
		}
		if _, exists := seen[peer.NodeID]; exists {
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
