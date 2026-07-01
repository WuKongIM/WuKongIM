package management

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ControllerRaftOperator exposes node-local Controller Raft status and compaction operations.
type ControllerRaftOperator interface {
	// ControllerRaftStatus returns one node's local Controller Raft status.
	ControllerRaftStatus(context.Context, uint64) (ControllerRaftStatus, error)
	// CompactControllerRaftLog forces one node's local Controller Raft log compaction.
	CompactControllerRaftLog(context.Context, uint64) (ControllerRaftCompactionResult, error)
}

// ErrControllerRaftOperatorUnavailable reports that Controller Raft operations are not wired.
var ErrControllerRaftOperatorUnavailable = errors.New("internalv2/usecase/management: controller raft operator unavailable")

// ControllerRaftCompaction describes Controller Raft log compaction state on one node.
type ControllerRaftCompaction struct {
	// Enabled reports whether Controller Raft snapshot compaction is enabled.
	Enabled bool
	// TriggerEntries is the applied-entry delta required before taking another snapshot.
	TriggerEntries uint64
	// CheckInterval is the minimum interval between compaction checks.
	CheckInterval time.Duration
	// LastSnapshotIndex is the latest snapshot index created by compaction.
	LastSnapshotIndex uint64
	// LastSnapshotAt records when the latest snapshot was created.
	LastSnapshotAt time.Time
	// LastCheckAt records the latest compaction check attempt.
	LastCheckAt time.Time
	// LastError is the latest compaction error, when present.
	LastError string
	// LastErrorAt records when LastError was observed.
	LastErrorAt time.Time
	// Degraded reports whether the latest compaction attempt failed.
	Degraded bool
}

// ControllerRaftRestore describes Controller metadata snapshot restore state.
type ControllerRaftRestore struct {
	// LastSnapshotIndex is the index of the latest restored snapshot.
	LastSnapshotIndex uint64
	// LastSnapshotTerm is the term of the latest restored snapshot.
	LastSnapshotTerm uint64
	// LastRestoredAt records when the latest snapshot restore succeeded.
	LastRestoredAt time.Time
	// LastError is the latest restore error, when present.
	LastError string
	// LastErrorAt records when LastError was observed.
	LastErrorAt time.Time
	// Failed reports whether the latest restore attempt failed.
	Failed bool
}

// ControllerRaftPeer describes one follower from the Controller Raft leader's view.
type ControllerRaftPeer struct {
	// NodeID is the follower node ID.
	NodeID uint64
	// Match is the highest log index known to match on the follower.
	Match uint64
	// Next is the next log index the leader will send to the follower.
	Next uint64
	// State is the raft progress state.
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

// ControllerRaftStatus is a node-scoped Controller Raft status snapshot.
type ControllerRaftStatus struct {
	// NodeID is the node whose Controller Raft status was read.
	NodeID uint64
	// Role is leader, follower, candidate, or unknown.
	Role string
	// LeaderID is the Controller Raft leader known to the queried node.
	LeaderID uint64
	// Term is the queried node's current Controller Raft term.
	Term uint64
	// Health is the derived manager-facing status bucket.
	Health string
	// FirstIndex is the first available Controller Raft log index.
	FirstIndex uint64
	// LastIndex is the last available Controller Raft log index.
	LastIndex uint64
	// CommitIndex is the queried node's committed Controller Raft watermark.
	CommitIndex uint64
	// AppliedIndex is the queried node's applied Controller Raft watermark.
	AppliedIndex uint64
	// Voters is the Controller Raft voter set observed by the queried node.
	Voters []uint64
	// Learners is the Controller Raft learner set observed by the queried node.
	Learners []uint64
	// SnapshotIndex is the latest persisted Controller Raft snapshot index.
	SnapshotIndex uint64
	// SnapshotTerm is the latest persisted Controller Raft snapshot term.
	SnapshotTerm uint64
	// Compaction describes Controller Raft log compaction state.
	Compaction ControllerRaftCompaction
	// Restore describes Controller metadata snapshot restore state.
	Restore ControllerRaftRestore
	// Peers contains leader-side follower progress.
	Peers []ControllerRaftPeer
}

// ControllerRaftCompactionResult describes one node-local Controller Raft compaction attempt.
type ControllerRaftCompactionResult struct {
	// NodeID is the node that handled the attempt.
	NodeID uint64
	// AppliedIndex is the node-local applied index used as the compaction target.
	AppliedIndex uint64
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether this attempt created a new snapshot and compacted entries.
	Compacted bool
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string
	// Error is the per-node failure message when present.
	Error string
}

// ControllerRaftCompactNodeResult describes one Controller voter node compaction attempt.
type ControllerRaftCompactNodeResult struct {
	// NodeID is the Controller voter node that handled the attempt.
	NodeID uint64
	// Success reports whether the node accepted and completed the local attempt.
	Success bool
	// AppliedIndex is the node-local applied index used as the compaction target.
	AppliedIndex uint64
	// BeforeSnapshotIndex is the persisted snapshot index before the attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the persisted snapshot index after the attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether this attempt created a new snapshot and compacted entries.
	Compacted bool
	// SkippedReason explains why no new snapshot was created when Compacted is false.
	SkippedReason string
	// Error is the per-node failure message when Success is false.
	Error string
}

// ControllerRaftCompactionSummary is returned after control-plane-wide compaction fan-out.
type ControllerRaftCompactionSummary struct {
	// GeneratedAt records when the fan-out result was assembled.
	GeneratedAt time.Time
	// Total is the number of Controller voter nodes targeted.
	Total int
	// Succeeded is the number of nodes that completed the attempt.
	Succeeded int
	// Failed is the number of nodes that returned an error.
	Failed int
	// Items contains per-node results ordered by node id.
	Items []ControllerRaftCompactNodeResult
}

// ControllerRaftStatus returns one selected node's local Controller Raft status.
func (a *App) ControllerRaftStatus(ctx context.Context, nodeID uint64) (ControllerRaftStatus, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftStatus{}, err
	}
	if nodeID == 0 {
		return ControllerRaftStatus{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.controllerRaft == nil {
		return ControllerRaftStatus{}, ErrControllerRaftOperatorUnavailable
	}
	status, err := a.controllerRaft.ControllerRaftStatus(ctx, nodeID)
	if err != nil {
		return ControllerRaftStatus{}, err
	}
	if a.controllerRaftStatusObserver != nil {
		a.controllerRaftStatusObserver.ObserveControllerRaftStatus(status)
	}
	return status, nil
}

// CompactControllerRaftLog forces one selected node's local Controller Raft log compaction.
func (a *App) CompactControllerRaftLog(ctx context.Context, nodeID uint64) (ControllerRaftCompactionResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	if nodeID == 0 {
		return ControllerRaftCompactionResult{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.controllerRaft == nil {
		return ControllerRaftCompactionResult{}, ErrControllerRaftOperatorUnavailable
	}
	return a.controllerRaft.CompactControllerRaftLog(ctx, nodeID)
}

// CompactControllerRaftLogs forces local Controller Raft log compaction on every Controller voter.
func (a *App) CompactControllerRaftLogs(ctx context.Context) (ControllerRaftCompactionSummary, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftCompactionSummary{}, err
	}
	if a == nil || a.controllerRaft == nil || a.cluster == nil {
		return ControllerRaftCompactionSummary{}, ErrControllerRaftOperatorUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return ControllerRaftCompactionSummary{}, err
	}
	nodeIDs := controllerVoterNodeIDs(snapshot)
	summary := ControllerRaftCompactionSummary{GeneratedAt: a.now(), Total: len(nodeIDs), Items: make([]ControllerRaftCompactNodeResult, 0, len(nodeIDs))}
	for _, nodeID := range nodeIDs {
		result, err := a.controllerRaft.CompactControllerRaftLog(ctx, nodeID)
		item := ControllerRaftCompactNodeResult{
			NodeID:              nodeID,
			Success:             err == nil,
			AppliedIndex:        result.AppliedIndex,
			BeforeSnapshotIndex: result.BeforeSnapshotIndex,
			AfterSnapshotIndex:  result.AfterSnapshotIndex,
			Compacted:           result.Compacted,
			SkippedReason:       result.SkippedReason,
		}
		if err != nil {
			item.Error = err.Error()
			if item.Error == "" {
				item.Error = result.Error
			}
			summary.Failed++
		} else {
			summary.Succeeded++
		}
		summary.Items = append(summary.Items, item)
	}
	return summary, nil
}

func controllerVoterNodeIDs(snapshot control.Snapshot) []uint64 {
	ids := make([]uint64, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if hasControlRole(node.Roles, control.RoleController) {
			ids = append(ids, node.NodeID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func hasControlRole(roles []control.Role, role control.Role) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}
	return false
}
