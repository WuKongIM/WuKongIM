package management

import (
	"context"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
)

const (
	ControllerRaftHealthHealthy              = "healthy"
	ControllerRaftHealthAppendCatchup        = "append_catchup"
	ControllerRaftHealthSnapshotRequired     = "snapshot_required"
	ControllerRaftHealthSnapshotTransferring = "snapshot_transferring"
	ControllerRaftHealthRestoreFailed        = "restore_failed"
	ControllerRaftHealthCompactionDegraded   = "compaction_degraded"
	ControllerRaftHealthUnknown              = "unknown"
)

// ControllerRaftStatusResponse is the manager-facing full Controller Raft status.
type ControllerRaftStatusResponse struct {
	// NodeID is the node whose local Controller Raft status was read.
	NodeID uint64
	// Role is leader, follower, candidate, or unknown.
	Role string
	// LeaderID is the leader known to the queried node.
	LeaderID uint64
	// Term is the queried node's current Raft term.
	Term uint64
	// Health is the derived manager-facing status bucket.
	Health string
	// FirstIndex is the first available local Controller Raft log index.
	FirstIndex uint64
	// LastIndex is the last available local Controller Raft log index.
	LastIndex uint64
	// CommitIndex is the queried node's durable committed index watermark.
	CommitIndex uint64
	// AppliedIndex is the queried node's durable applied index watermark.
	AppliedIndex uint64
	// SnapshotIndex is the latest persisted Controller Raft snapshot index.
	SnapshotIndex uint64
	// SnapshotTerm is the latest persisted Controller Raft snapshot term.
	SnapshotTerm uint64
	// Compaction describes local Controller Raft log compaction state.
	Compaction ControllerRaftCompactionStatus
	// Restore describes local Controller metadata snapshot restore state.
	Restore ControllerRaftRestoreStatus
	// Peers contains leader-side follower progress.
	Peers []ControllerRaftPeerProgress
}

// ControllerRaftCompactionStatus describes local Controller Raft log compaction state.
type ControllerRaftCompactionStatus struct {
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

// ControllerRaftRestoreStatus describes Controller metadata snapshot restore state.
type ControllerRaftRestoreStatus struct {
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

// ControllerRaftPeerProgress describes one follower from the leader's view.
type ControllerRaftPeerProgress struct {
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
	// NeedsSnapshot reports whether the follower has fallen behind the local first index.
	NeedsSnapshot bool
	// SnapshotTransferring reports whether raft is currently transferring a snapshot.
	SnapshotTransferring bool
}

// GetControllerRaftStatus returns one node-local Controller Raft status snapshot.
func (a *App) GetControllerRaftStatus(ctx context.Context, nodeID uint64) (ControllerRaftStatusResponse, error) {
	if a == nil || a.cluster == nil {
		return ControllerRaftStatusResponse{}, nil
	}
	status, err := a.cluster.ControllerRaftStatusOnNode(ctx, nodeID)
	if err != nil {
		return ControllerRaftStatusResponse{}, err
	}
	return controllerRaftStatusResponse(status), nil
}

func controllerRaftStatusResponse(status raftcluster.ControllerRaftStatus) ControllerRaftStatusResponse {
	out := ControllerRaftStatusResponse{
		NodeID:        status.NodeID,
		Role:          status.Role,
		LeaderID:      status.LeaderID,
		Term:          status.Term,
		Health:        controllerRaftHealth(status),
		FirstIndex:    status.FirstIndex,
		LastIndex:     status.LastIndex,
		CommitIndex:   status.CommitIndex,
		AppliedIndex:  status.AppliedIndex,
		SnapshotIndex: status.SnapshotIndex,
		SnapshotTerm:  status.SnapshotTerm,
		Compaction: ControllerRaftCompactionStatus{
			Enabled:           status.Compaction.Enabled,
			TriggerEntries:    status.Compaction.TriggerEntries,
			CheckInterval:     status.Compaction.CheckInterval,
			LastSnapshotIndex: status.Compaction.LastSnapshotIndex,
			LastSnapshotAt:    status.Compaction.LastSnapshotAt,
			LastCheckAt:       status.Compaction.LastCheckAt,
			LastError:         status.Compaction.LastError,
			LastErrorAt:       status.Compaction.LastErrorAt,
			Degraded:          status.Compaction.Degraded,
		},
		Restore: ControllerRaftRestoreStatus{
			LastSnapshotIndex: status.Restore.LastSnapshotIndex,
			LastSnapshotTerm:  status.Restore.LastSnapshotTerm,
			LastRestoredAt:    status.Restore.LastRestoredAt,
			LastError:         status.Restore.LastError,
			LastErrorAt:       status.Restore.LastErrorAt,
			Failed:            status.Restore.Failed,
		},
		Peers: make([]ControllerRaftPeerProgress, 0, len(status.Peers)),
	}
	for _, peer := range status.Peers {
		out.Peers = append(out.Peers, ControllerRaftPeerProgress{
			NodeID:               peer.NodeID,
			Match:                peer.Match,
			Next:                 peer.Next,
			State:                peer.State,
			PendingSnapshot:      peer.PendingSnapshot,
			RecentActive:         peer.RecentActive,
			NeedsSnapshot:        peer.NeedsSnapshot,
			SnapshotTransferring: peer.SnapshotTransferring,
		})
	}
	return out
}

func controllerRaftHealth(status raftcluster.ControllerRaftStatus) string {
	if status.NodeID == 0 || status.Role == "" || status.Role == "unknown" {
		return ControllerRaftHealthUnknown
	}
	if status.Restore.Failed {
		return ControllerRaftHealthRestoreFailed
	}
	if status.Compaction.Degraded {
		return ControllerRaftHealthCompactionDegraded
	}
	transferring := false
	required := false
	appendCatchup := false
	for _, peer := range status.Peers {
		if peer.SnapshotTransferring {
			transferring = true
		}
		if peer.NeedsSnapshot {
			required = true
		}
		if peer.Match < status.CommitIndex && !peer.NeedsSnapshot && !peer.SnapshotTransferring {
			appendCatchup = true
		}
	}
	switch {
	case transferring:
		return ControllerRaftHealthSnapshotTransferring
	case required:
		return ControllerRaftHealthSnapshotRequired
	case appendCatchup:
		return ControllerRaftHealthAppendCatchup
	default:
		return ControllerRaftHealthHealthy
	}
}
