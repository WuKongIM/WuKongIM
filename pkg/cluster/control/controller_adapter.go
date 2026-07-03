package control

import (
	"context"
	"fmt"
	"sync"
	"time"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

const defaultNodeHealthReportTTL = 30 * time.Second

// ControllerStateSource provides locally visible Controller state snapshots.
type ControllerStateSource interface {
	// Snapshot returns the latest locally visible Controller cluster state.
	Snapshot(context.Context) controller.ClusterState
}

// ControllerConfig wires a ControllerAdapter.
type ControllerConfig struct {
	// Source returns Controller state snapshots.
	Source ControllerStateSource
	// HealthReportTTL bounds how long adapted node health reports remain fresh.
	HealthReportTTL time.Duration
}

// ControllerAdapter adapts Controller state snapshots to cluster control snapshots.
type ControllerAdapter struct {
	mu        sync.RWMutex
	source    ControllerStateSource
	healthTTL time.Duration
	snapshot  Snapshot
	watch     chan SnapshotEvent
	publisher *snapshotWatchPublisher
	started   bool
}

// NewControllerAdapter creates a ControllerAdapter.
func NewControllerAdapter(cfg ControllerConfig) *ControllerAdapter {
	watch := make(chan SnapshotEvent, 16)
	return &ControllerAdapter{source: cfg.Source, healthTTL: normalizeNodeHealthReportTTL(cfg.HealthReportTTL), watch: watch, publisher: newSnapshotWatchPublisher(watch)}
}

// SnapshotFromController maps Controller durable state into the cluster control model.
func SnapshotFromController(st controller.ClusterState) (Snapshot, error) {
	return SnapshotFromControllerWithHealthTTL(st, defaultNodeHealthReportTTL)
}

// SnapshotFromControllerWithHealthTTL maps Controller state using an explicit node health TTL.
func SnapshotFromControllerWithHealthTTL(st controller.ClusterState, healthTTL time.Duration) (Snapshot, error) {
	if err := st.Validate(); err != nil {
		return Snapshot{}, err
	}
	st = st.Clone()
	st.Normalize()
	leaderID := uint64(0)
	if len(st.Controllers) > 0 {
		leaderID = st.Controllers[0].NodeID
	}
	snap := snapshotFromControllerState(st, leaderID, time.Now().UTC(), normalizeNodeHealthReportTTL(healthTTL))
	if err := snap.Validate(); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

func normalizeNodeHealthReportTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultNodeHealthReportTTL
	}
	return ttl
}

func snapshotFromControllerState(st controller.ClusterState, leaderID uint64, now time.Time, healthTTL time.Duration) Snapshot {
	snap := Snapshot{ClusterID: st.ClusterID, Revision: st.Revision, ControllerID: leaderID, Nodes: make([]Node, 0, len(st.Nodes)), Slots: make([]SlotAssignment, 0, len(st.Slots)), Tasks: make([]ReconcileTask, 0, len(st.Tasks))}
	healthByNode := make(map[uint64]controller.NodeHealthReport, len(st.NodeHealthReports))
	for _, report := range st.NodeHealthReports {
		healthByNode[report.NodeID] = report
	}
	for _, node := range st.Nodes {
		health, hasHealth := healthByNode[node.NodeID]
		snap.Nodes = append(snap.Nodes, Node{
			NodeID:         node.NodeID,
			Addr:           node.Addr,
			Roles:          mapControllerRoles(node.Roles),
			Status:         mapControllerStatus(node.Status),
			Health:         BuildNodeHealth(health, hasHealth, now, healthTTL),
			JoinState:      mapControllerJoinState(node.JoinState),
			CapacityWeight: node.CapacityWeight,
		})
	}
	for _, slot := range st.Slots {
		snap.Slots = append(snap.Slots, SlotAssignment{SlotID: slot.SlotID, DesiredPeers: append([]uint64(nil), slot.DesiredPeers...), ConfigEpoch: slot.ConfigEpoch, PreferredLeader: slot.PreferredLeader})
	}
	snap.HashSlots = HashSlotTable{Revision: st.Revision, Count: st.HashSlots.SlotCount, Ranges: make([]HashSlotRange, 0, len(st.HashSlots.Ranges))}
	for _, r := range st.HashSlots.Ranges {
		snap.HashSlots.Ranges = append(snap.HashSlots.Ranges, HashSlotRange{From: r.From, To: r.To, SlotID: r.SlotID})
	}
	for _, task := range st.Tasks {
		snap.Tasks = append(snap.Tasks, ReconcileTask{
			TaskID:              task.TaskID,
			SlotID:              task.SlotID,
			Kind:                TaskKind(task.Kind),
			Step:                TaskStep(task.Step),
			SourceNode:          task.SourceNode,
			TargetNode:          task.TargetNode,
			TargetPeers:         append([]uint64(nil), task.TargetPeers...),
			CompletionPolicy:    TaskCompletionPolicy(task.CompletionPolicy),
			ParticipantProgress: mapControllerParticipantProgress(task.ParticipantProgress),
			ConfigEpoch:         task.ConfigEpoch,
			Attempt:             task.Attempt,
			Status:              TaskStatus(task.Status),
			LastError:           task.LastError,
			PhaseIndex:          task.PhaseIndex,
			ObservedConfigIndex: task.ObservedConfigIndex,
			ObservedVoters:      append([]uint64(nil), task.ObservedVoters...),
			ObservedLearners:    append([]uint64(nil), task.ObservedLearners...),
		})
	}
	return snap
}

// Start loads the initial Controller snapshot without emitting a watch event.
func (a *ControllerAdapter) Start(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if a == nil || a.source == nil {
		return fmt.Errorf("control: controller source is required")
	}
	snap, err := SnapshotFromControllerWithHealthTTL(a.source.Snapshot(ctx), a.healthTTL)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.snapshot = snap.Clone()
	a.started = true
	a.mu.Unlock()
	return nil
}

// Stop marks the adapter stopped.
func (a *ControllerAdapter) Stop(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	a.started = false
	a.mu.Unlock()
	return nil
}

// Refresh loads a Controller snapshot and publishes an event when valid.
func (a *ControllerAdapter) Refresh(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	snap, err := SnapshotFromControllerWithHealthTTL(a.source.Snapshot(ctx), a.healthTTL)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.snapshot = snap.Clone()
	a.mu.Unlock()
	a.publisher.publish(snap)
	return nil
}

// LocalSnapshot returns a deep copy of the latest adapted snapshot.
func (a *ControllerAdapter) LocalSnapshot(ctx context.Context) (Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return Snapshot{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.snapshot.Clone(), nil
}

// LeaderID returns the best-known Controller leader ID from the adapted snapshot.
func (a *ControllerAdapter) LeaderID() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.snapshot.ControllerID
}

// ReportNode is a best-effort no-op on the read-only snapshot adapter.
func (a *ControllerAdapter) ReportNode(ctx context.Context, report NodeReport) error {
	return ctxErr(ctx)
}

// ReportSlots is currently a best-effort no-op until Controller exposes report commands.
func (a *ControllerAdapter) ReportSlots(ctx context.Context, report SlotRuntimeReport) error {
	return ctxErr(ctx)
}

// CompleteTask is unsupported on the read-only Controller snapshot adapter.
func (a *ControllerAdapter) CompleteTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controller adapter cannot write task results")
}

// FailTask is unsupported on the read-only Controller snapshot adapter.
func (a *ControllerAdapter) FailTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controller adapter cannot write task results")
}

// ReportTaskProgress is unsupported on the read-only Controller snapshot adapter.
func (a *ControllerAdapter) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controller adapter cannot write task progress")
}

// AdvanceSlotReplicaMovePhase is unsupported on the read-only Controller snapshot adapter.
func (a *ControllerAdapter) AdvanceSlotReplicaMovePhase(ctx context.Context, phase SlotReplicaMovePhaseAdvance) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controller adapter cannot write slot replica move phases")
}

// CommitSlotReplicaMove is unsupported on the read-only Controller snapshot adapter.
func (a *ControllerAdapter) CommitSlotReplicaMove(ctx context.Context, commit SlotReplicaMoveCommit) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controller adapter cannot write slot replica move commits")
}

// RequestSlotLeaderTransfer is unsupported on the read-only Controller snapshot adapter.
func (a *ControllerAdapter) RequestSlotLeaderTransfer(ctx context.Context, req SlotLeaderTransferRequest) (SlotLeaderTransferResult, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLeaderTransferResult{}, err
	}
	return SlotLeaderTransferResult{}, fmt.Errorf("control: controller adapter cannot write leader transfer intents")
}

// RequestSlotReplicaMove is unsupported on the read-only Controller snapshot adapter.
func (a *ControllerAdapter) RequestSlotReplicaMove(ctx context.Context, req SlotReplicaMoveRequest) (SlotReplicaMoveResult, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotReplicaMoveResult{}, err
	}
	return SlotReplicaMoveResult{}, fmt.Errorf("control: controller adapter cannot write slot replica move intents")
}

// Watch returns snapshot update events.
func (a *ControllerAdapter) Watch() <-chan SnapshotEvent { return a.watch }

func mapControllerRoles(in []controller.NodeRole) []Role {
	out := make([]Role, 0, len(in))
	for _, role := range in {
		switch role {
		case controller.NodeRoleControllerVoter:
			out = append(out, RoleController)
		case controller.NodeRoleData:
			out = append(out, RoleData)
		}
	}
	return out
}

func mapControllerStatus(status controller.NodeStatus) NodeStatus {
	switch status {
	case controller.NodeStatusAlive:
		return NodeAlive
	case controller.NodeStatusSuspect:
		return NodeSuspect
	case controller.NodeStatusDown:
		return NodeDown
	default:
		return NodeDown
	}
}

func mapControllerJoinState(state controller.NodeJoinState) NodeJoinState {
	switch state {
	case controller.NodeJoinStateActive:
		return NodeJoinStateActive
	case controller.NodeJoinStateJoining:
		return NodeJoinStateJoining
	case controller.NodeJoinStateLeaving:
		return NodeJoinStateLeaving
	case controller.NodeJoinStateRemoved:
		return NodeJoinStateRemoved
	default:
		return NodeJoinStateRemoved
	}
}

func mapControllerParticipantProgress(in []controller.TaskParticipantProgress) []TaskParticipantProgress {
	out := make([]TaskParticipantProgress, 0, len(in))
	for _, progress := range in {
		out = append(out, TaskParticipantProgress{
			NodeID:    progress.NodeID,
			Attempt:   progress.Attempt,
			Status:    TaskParticipantStatus(progress.Status),
			LastError: progress.LastError,
		})
	}
	return out
}

var _ Controller = (*ControllerAdapter)(nil)
