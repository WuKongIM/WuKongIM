package raft

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	defaultTickInterval         = 100 * time.Millisecond
	defaultMaxApplyBatchEntries = 128
	defaultMaxApplyBatchBytes   = uint64(4 << 20)
	defaultMaxApplyDelay        = 2 * time.Millisecond
	defaultWALSegmentSize       = uint64(64 << 20)
	defaultSnapshotCount        = uint64(10_000)
	defaultSnapshotCatchUp      = uint64(5_000)
	defaultSnapshotMinInterval  = 30 * time.Second
	electionTick                = 10
	heartbeatTick               = 1
)

// Peer identifies one ControllerV2 Raft voter.
type Peer struct {
	// NodeID is the non-zero stable node identity of the voter.
	NodeID uint64
	// Addr is the stable Controller address associated with the voter.
	Addr string
}

// Transport sends outbound ControllerV2 Raft messages to peer nodes.
//
// Implementations should enqueue messages and return without waiting for network I/O;
// the service calls Send inline and may call it before local leader persistence.
type Transport interface {
	Send([]raftpb.Message)
}

type stateMachine interface {
	Load(context.Context) error
	Reset()
	Restore(context.Context, state.ClusterState) error
	Snapshot(context.Context) state.ClusterState
	IsDegraded() bool
	ApplyBatch(context.Context, []fsm.AppliedCommand) (fsm.BatchApplyResult, error)
}

// Config configures one ControllerV2 Raft service instance.
type Config struct {
	// NodeID is the local ControllerV2 Raft node ID.
	NodeID uint64
	// Peers lists the ControllerV2 Raft voters used for bootstrap.
	Peers []Peer
	// AllowBootstrap permits this node to initialize a brand-new ControllerV2 Raft log.
	AllowBootstrap bool
	// RaftDir is the local directory used for ControllerV2 Raft WAL segments, snapshots, and metadata.
	RaftDir string
	// StateMachine applies committed ControllerV2 commands to cluster-state.json.
	StateMachine stateMachine
	// Transport delivers outbound Raft protocol messages to peer services.
	Transport Transport
	// TickInterval controls the wall-clock interval between Raft ticks; zero uses the default.
	TickInterval time.Duration
	// MaxApplyBatchEntries limits how many committed command entries one FSM batch may contain.
	MaxApplyBatchEntries int
	// MaxApplyBatchBytes limits the total encoded command bytes in one FSM apply batch.
	MaxApplyBatchBytes uint64
	// MaxApplyDelay bounds how long the apply scheduler may wait to coalesce more committed entries.
	MaxApplyDelay time.Duration
	// WALSegmentSize controls ControllerV2 WAL segment rollover size in bytes.
	WALSegmentSize uint64
	// SnapshotCount controls how many newly applied entries trigger a ControllerV2 snapshot.
	SnapshotCount uint64
	// SnapshotCatchUpEntries controls how many entries after the last snapshot remain available for follower catch-up.
	SnapshotCatchUpEntries uint64
	// SnapshotMinInterval prevents repeated snapshots more frequently than this interval.
	SnapshotMinInterval time.Duration
}

func (c Config) normalized() Config {
	if c.TickInterval == 0 {
		c.TickInterval = defaultTickInterval
	}
	if c.MaxApplyBatchEntries == 0 {
		c.MaxApplyBatchEntries = defaultMaxApplyBatchEntries
	}
	if c.MaxApplyBatchBytes == 0 {
		c.MaxApplyBatchBytes = defaultMaxApplyBatchBytes
	}
	if c.MaxApplyDelay == 0 {
		c.MaxApplyDelay = defaultMaxApplyDelay
	}
	if c.WALSegmentSize == 0 {
		c.WALSegmentSize = defaultWALSegmentSize
	}
	if c.SnapshotCount == 0 {
		c.SnapshotCount = defaultSnapshotCount
	}
	if c.SnapshotCatchUpEntries == 0 {
		c.SnapshotCatchUpEntries = defaultSnapshotCatchUp
	}
	if c.SnapshotMinInterval == 0 {
		c.SnapshotMinInterval = defaultSnapshotMinInterval
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
	if c.RaftDir == "" {
		return fmt.Errorf("%w: raft dir must not be empty", ErrInvalidConfig)
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
	if c.MaxApplyBatchEntries <= 0 {
		return fmt.Errorf("%w: max apply batch entries must be > 0", ErrInvalidConfig)
	}
	if c.MaxApplyBatchBytes == 0 {
		return fmt.Errorf("%w: max apply batch bytes must be > 0", ErrInvalidConfig)
	}
	if c.MaxApplyDelay <= 0 {
		return fmt.Errorf("%w: max apply delay must be > 0", ErrInvalidConfig)
	}
	if c.WALSegmentSize == 0 {
		return fmt.Errorf("%w: wal segment size must be > 0", ErrInvalidConfig)
	}
	if c.SnapshotCatchUpEntries > c.SnapshotCount {
		return fmt.Errorf("%w: snapshot catch-up entries must be <= snapshot count", ErrInvalidConfig)
	}
	if c.SnapshotMinInterval <= 0 {
		return fmt.Errorf("%w: snapshot min interval must be > 0", ErrInvalidConfig)
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
