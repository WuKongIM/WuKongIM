package cluster

import (
	"context"
	"time"

	controllerraft "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// ControllerRaftStatus is a manager-facing read-only status snapshot for one Controller Raft node.
type ControllerRaftStatus struct {
	// NodeID is the node whose local Controller Raft status was read.
	NodeID uint64
	// Role is leader, follower, candidate, or unknown.
	Role string
	// LeaderID is the leader known to the queried node.
	LeaderID uint64
	// Term is the queried node's current Raft term.
	Term uint64
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

// ControllerRaftStatusOnNode returns one node's local Controller Raft status.
func (c *Cluster) ControllerRaftStatusOnNode(ctx context.Context, nodeID uint64) (ControllerRaftStatus, error) {
	if c == nil {
		return ControllerRaftStatus{}, ErrNotStarted
	}
	if c.IsLocal(multiraft.NodeID(nodeID)) {
		return c.localControllerRaftStatus(ctx, nodeID)
	}
	return c.remoteControllerRaftStatus(ctx, nodeID)
}

func (c *Cluster) localControllerRaftStatus(ctx context.Context, nodeID uint64) (ControllerRaftStatus, error) {
	if c == nil || c.controllerHost == nil || c.controllerHost.service == nil || c.controllerHost.raftDB == nil {
		return ControllerRaftStatus{}, ErrNotStarted
	}
	status := controllerRaftStatusFromService(c.controllerHost.service.Status())
	if nodeID != 0 {
		status.NodeID = nodeID
	}

	storage := c.controllerHost.raftDB.ForController()
	return controllerRaftStatusWithDurableIndexes(ctx, status, storage)
}

func controllerRaftStatusWithDurableIndexes(ctx context.Context, status ControllerRaftStatus, storage multiraft.Storage) (ControllerRaftStatus, error) {
	state, err := storage.InitialState(ctx)
	if err != nil {
		return ControllerRaftStatus{}, err
	}
	first, err := storage.FirstIndex(ctx)
	if err != nil {
		return ControllerRaftStatus{}, err
	}
	last, err := storage.LastIndex(ctx)
	if err != nil {
		return ControllerRaftStatus{}, err
	}
	var snapshotTerm uint64
	snapshotIndex := uint64(0)
	if first > 1 {
		snapshotIndex = first - 1
		snapshotTerm, err = storage.Term(ctx, snapshotIndex)
		if err != nil {
			return ControllerRaftStatus{}, err
		}
	}

	status.FirstIndex = first
	status.LastIndex = last
	status.CommitIndex = state.HardState.Commit
	status.AppliedIndex = state.AppliedIndex
	status.SnapshotIndex = snapshotIndex
	status.SnapshotTerm = snapshotTerm
	deriveControllerRaftPeerStatus(&status)
	return status, nil
}

func (c *Cluster) remoteControllerRaftStatus(ctx context.Context, nodeID uint64) (ControllerRaftStatus, error) {
	body, err := encodeControllerRequest(controllerRPCRequest{Kind: controllerRPCControllerRaftStatus})
	if err != nil {
		return ControllerRaftStatus{}, err
	}
	respBody, err := c.controllerRPCService(ctx, multiraft.NodeID(nodeID), body)
	if err != nil {
		return ControllerRaftStatus{}, err
	}
	resp, err := decodeControllerResponse(controllerRPCControllerRaftStatus, respBody)
	if err != nil {
		return ControllerRaftStatus{}, err
	}
	if resp.ControllerRaftStatus == nil {
		return ControllerRaftStatus{}, ErrInvalidConfig
	}
	return *resp.ControllerRaftStatus, nil
}

func controllerRaftStatusFromService(st controllerraft.Status) ControllerRaftStatus {
	out := ControllerRaftStatus{
		NodeID:       st.NodeID,
		Role:         st.Role,
		LeaderID:     st.LeaderID,
		Term:         st.Term,
		CommitIndex:  st.CommitIndex,
		AppliedIndex: st.AppliedIndex,
		Compaction: ControllerRaftCompactionStatus{
			Enabled:           st.Compaction.Enabled,
			TriggerEntries:    st.Compaction.TriggerEntries,
			CheckInterval:     st.Compaction.CheckInterval,
			LastSnapshotIndex: st.Compaction.LastSnapshotIndex,
			LastSnapshotAt:    st.Compaction.LastSnapshotAt,
			LastCheckAt:       st.Compaction.LastCheckAt,
			LastError:         st.Compaction.LastError,
			LastErrorAt:       st.Compaction.LastErrorAt,
			Degraded:          st.Compaction.Degraded,
		},
		Restore: ControllerRaftRestoreStatus{
			LastSnapshotIndex: st.Restore.LastSnapshotIndex,
			LastSnapshotTerm:  st.Restore.LastSnapshotTerm,
			LastRestoredAt:    st.Restore.LastRestoredAt,
			LastError:         st.Restore.LastError,
			LastErrorAt:       st.Restore.LastErrorAt,
			Failed:            st.Restore.Failed,
		},
		Peers: make([]ControllerRaftPeerProgress, 0, len(st.Peers)),
	}
	for _, peer := range st.Peers {
		out.Peers = append(out.Peers, ControllerRaftPeerProgress{
			NodeID:               peer.NodeID,
			Match:                peer.Match,
			Next:                 peer.Next,
			State:                peer.State,
			PendingSnapshot:      peer.PendingSnapshot,
			RecentActive:         peer.RecentActive,
			SnapshotTransferring: peer.SnapshotTransferring,
		})
	}
	return out
}

func deriveControllerRaftPeerStatus(status *ControllerRaftStatus) {
	if status == nil {
		return
	}
	for i := range status.Peers {
		peer := &status.Peers[i]
		peer.NeedsSnapshot = status.FirstIndex > 0 && peer.Next < status.FirstIndex && peer.PendingSnapshot == 0
		peer.SnapshotTransferring = peer.SnapshotTransferring || peer.PendingSnapshot > 0
	}
}
