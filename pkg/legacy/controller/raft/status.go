package raft

import (
	"slices"
	"time"

	etcdraft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/tracker"
)

const (
	// RoleUnknown reports that the Controller Raft role is not yet known.
	RoleUnknown = "unknown"
	// RoleLeader reports that the local Controller Raft node is leader.
	RoleLeader = "leader"
	// RoleFollower reports that the local Controller Raft node is follower.
	RoleFollower = "follower"
	// RoleCandidate reports that the local Controller Raft node is candidate.
	RoleCandidate = "candidate"
)

// Status is a goroutine-safe snapshot of one Controller Raft service.
type Status struct {
	// NodeID is the local Controller Raft node ID.
	NodeID uint64
	// Role is leader, follower, candidate, or unknown.
	Role string
	// LeaderID is the leader known to this node.
	LeaderID uint64
	// Term is the local Raft term.
	Term uint64
	// CommitIndex is the volatile commit index reported by etcd raft.
	CommitIndex uint64
	// AppliedIndex is the volatile applied index reported by etcd raft.
	AppliedIndex uint64
	// Compaction describes local Controller Raft log compaction state.
	Compaction LogCompactionStatus
	// Restore describes the latest Controller metadata snapshot restore.
	Restore SnapshotRestoreStatus
	// Peers contains leader-side follower progress. Followers return an empty list.
	Peers []PeerProgress
}

// LogCompactionStatus describes local Controller Raft snapshot compaction state.
type LogCompactionStatus struct {
	// Enabled reports whether local Controller Raft snapshot compaction is enabled.
	Enabled bool
	// TriggerEntries is the applied-entry delta required before taking another snapshot.
	TriggerEntries uint64
	// CheckInterval is the minimum interval between compaction checks.
	CheckInterval time.Duration
	// LastSnapshotIndex is the latest local snapshot index created by compaction.
	LastSnapshotIndex uint64
	// LastSnapshotAt records when the latest local snapshot was created.
	LastSnapshotAt time.Time
	// LastCheckAt records the latest compaction check attempt.
	LastCheckAt time.Time
	// LastError is the latest compaction error, if any.
	LastError string
	// LastErrorAt records when LastError was observed.
	LastErrorAt time.Time
	// Degraded reports whether the latest compaction attempt failed and has not yet been cleared.
	Degraded bool
}

// SnapshotRestoreStatus describes Controller metadata snapshot restore state.
type SnapshotRestoreStatus struct {
	// LastSnapshotIndex is the index of the latest restored snapshot.
	LastSnapshotIndex uint64
	// LastSnapshotTerm is the term of the latest restored snapshot.
	LastSnapshotTerm uint64
	// LastRestoredAt records when the latest snapshot restore succeeded.
	LastRestoredAt time.Time
	// LastError is the latest snapshot restore error, if any.
	LastError string
	// LastErrorAt records when LastError was observed.
	LastErrorAt time.Time
	// Failed reports whether the latest restore attempt failed.
	Failed bool
}

// PeerProgress describes one follower from the leader's view.
type PeerProgress struct {
	// NodeID is the follower Controller Raft node ID.
	NodeID uint64
	// Match is the highest log index known to match on the follower.
	Match uint64
	// Next is the next log index the leader will send to the follower.
	Next uint64
	// State is the etcd raft progress state.
	State string
	// PendingSnapshot is the snapshot index currently pending for the follower.
	PendingSnapshot uint64
	// RecentActive reports whether the follower was recently active.
	RecentActive bool
	// SnapshotTransferring reports whether raft is currently transferring a snapshot.
	SnapshotTransferring bool
}

func initialStatus(cfg Config) Status {
	compaction := NormalizeLogCompactionConfig(cfg.LogCompaction)
	return Status{
		NodeID: cfg.NodeID,
		Role:   RoleUnknown,
		Compaction: LogCompactionStatus{
			Enabled:        compaction.Enabled,
			TriggerEntries: compaction.TriggerEntries,
			CheckInterval:  compaction.CheckInterval,
		},
	}
}

func cloneStatus(st Status) Status {
	st.Peers = append([]PeerProgress(nil), st.Peers...)
	return st
}

func raftRoleName(state etcdraft.StateType) string {
	switch state {
	case etcdraft.StateLeader:
		return RoleLeader
	case etcdraft.StateFollower:
		return RoleFollower
	case etcdraft.StateCandidate, etcdraft.StatePreCandidate:
		return RoleCandidate
	default:
		return RoleUnknown
	}
}

func peerProgressFromRaft(self uint64, progress map[uint64]tracker.Progress) []PeerProgress {
	if len(progress) == 0 {
		return nil
	}
	peers := make([]PeerProgress, 0, len(progress))
	for nodeID, pr := range progress {
		if nodeID == self {
			continue
		}
		state := pr.State.String()
		peers = append(peers, PeerProgress{
			NodeID:               nodeID,
			Match:                pr.Match,
			Next:                 pr.Next,
			State:                state,
			PendingSnapshot:      pr.PendingSnapshot,
			RecentActive:         pr.RecentActive,
			SnapshotTransferring: pr.PendingSnapshot > 0 || pr.State == tracker.StateSnapshot,
		})
	}
	slices.SortFunc(peers, func(left, right PeerProgress) int {
		switch {
		case left.NodeID < right.NodeID:
			return -1
		case left.NodeID > right.NodeID:
			return 1
		default:
			return 0
		}
	})
	return peers
}
