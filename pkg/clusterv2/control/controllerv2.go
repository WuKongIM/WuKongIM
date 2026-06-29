package control

import (
	"context"
	"fmt"
	"sync"
	"time"

	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
)

const defaultNodeHealthReportTTL = 30 * time.Second

// ControllerV2StateSource provides locally visible ControllerV2 state snapshots.
type ControllerV2StateSource interface {
	// Snapshot returns the latest locally visible ControllerV2 cluster state.
	Snapshot(context.Context) cv2.ClusterState
}

// ControllerV2Config wires a ControllerV2Adapter.
type ControllerV2Config struct {
	// Source returns ControllerV2 state snapshots.
	Source ControllerV2StateSource
}

// ControllerV2Adapter adapts ControllerV2 state snapshots to clusterv2 control snapshots.
type ControllerV2Adapter struct {
	mu       sync.RWMutex
	source   ControllerV2StateSource
	snapshot Snapshot
	watch    chan SnapshotEvent
	started  bool
}

// NewControllerV2Adapter creates a ControllerV2Adapter.
func NewControllerV2Adapter(cfg ControllerV2Config) *ControllerV2Adapter {
	return &ControllerV2Adapter{source: cfg.Source, watch: make(chan SnapshotEvent, 16)}
}

// SnapshotFromControllerV2 maps ControllerV2 durable state into the clusterv2 control model.
func SnapshotFromControllerV2(st cv2.ClusterState) (Snapshot, error) {
	if err := st.Validate(); err != nil {
		return Snapshot{}, err
	}
	st = st.Clone()
	st.Normalize()
	leaderID := uint64(0)
	if len(st.Controllers) > 0 {
		leaderID = st.Controllers[0].NodeID
	}
	snap := snapshotFromControllerState(st, leaderID, time.Now().UTC(), defaultNodeHealthReportTTL)
	if err := snap.Validate(); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

func snapshotFromControllerState(st cv2.ClusterState, leaderID uint64, now time.Time, healthTTL time.Duration) Snapshot {
	snap := Snapshot{ClusterID: st.ClusterID, Revision: st.Revision, ControllerID: leaderID, Nodes: make([]Node, 0, len(st.Nodes)), Slots: make([]SlotAssignment, 0, len(st.Slots)), Tasks: make([]ReconcileTask, 0, len(st.Tasks))}
	healthByNode := make(map[uint64]cv2.NodeHealthReport, len(st.NodeHealthReports))
	for _, report := range st.NodeHealthReports {
		healthByNode[report.NodeID] = report
	}
	for _, node := range st.Nodes {
		health, hasHealth := healthByNode[node.NodeID]
		snap.Nodes = append(snap.Nodes, Node{
			NodeID:         node.NodeID,
			Addr:           node.Addr,
			Roles:          mapControllerV2Roles(node.Roles),
			Status:         mapControllerV2Status(node.Status),
			Health:         BuildNodeHealth(health, hasHealth, now, healthTTL),
			JoinState:      mapControllerV2JoinState(node.JoinState),
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
			ParticipantProgress: mapControllerV2ParticipantProgress(task.ParticipantProgress),
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

// Start loads the initial ControllerV2 snapshot without emitting a watch event.
func (a *ControllerV2Adapter) Start(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if a == nil || a.source == nil {
		return fmt.Errorf("control: controllerv2 source is required")
	}
	snap, err := SnapshotFromControllerV2(a.source.Snapshot(ctx))
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
func (a *ControllerV2Adapter) Stop(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	a.started = false
	a.mu.Unlock()
	return nil
}

// Refresh loads a ControllerV2 snapshot and publishes an event when valid.
func (a *ControllerV2Adapter) Refresh(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	snap, err := SnapshotFromControllerV2(a.source.Snapshot(ctx))
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.snapshot = snap.Clone()
	a.mu.Unlock()
	select {
	case a.watch <- SnapshotEvent{Snapshot: snap.Clone()}:
	default:
	}
	return nil
}

// LocalSnapshot returns a deep copy of the latest adapted snapshot.
func (a *ControllerV2Adapter) LocalSnapshot(ctx context.Context) (Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return Snapshot{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.snapshot.Clone(), nil
}

// LeaderID returns the best-known ControllerV2 leader ID from the adapted snapshot.
func (a *ControllerV2Adapter) LeaderID() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.snapshot.ControllerID
}

// ReportNode is a best-effort no-op on the read-only snapshot adapter.
func (a *ControllerV2Adapter) ReportNode(ctx context.Context, report NodeReport) error {
	return ctxErr(ctx)
}

// ReportSlots is currently a best-effort no-op until ControllerV2 exposes report commands.
func (a *ControllerV2Adapter) ReportSlots(ctx context.Context, report SlotRuntimeReport) error {
	return ctxErr(ctx)
}

// CompleteTask is unsupported on the read-only ControllerV2 snapshot adapter.
func (a *ControllerV2Adapter) CompleteTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controllerv2 adapter cannot write task results")
}

// FailTask is unsupported on the read-only ControllerV2 snapshot adapter.
func (a *ControllerV2Adapter) FailTask(ctx context.Context, result TaskResult) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controllerv2 adapter cannot write task results")
}

// ReportTaskProgress is unsupported on the read-only ControllerV2 snapshot adapter.
func (a *ControllerV2Adapter) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controllerv2 adapter cannot write task progress")
}

// AdvanceSlotReplicaMovePhase is unsupported on the read-only ControllerV2 snapshot adapter.
func (a *ControllerV2Adapter) AdvanceSlotReplicaMovePhase(ctx context.Context, phase SlotReplicaMovePhaseAdvance) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controllerv2 adapter cannot write slot replica move phases")
}

// CommitSlotReplicaMove is unsupported on the read-only ControllerV2 snapshot adapter.
func (a *ControllerV2Adapter) CommitSlotReplicaMove(ctx context.Context, commit SlotReplicaMoveCommit) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	return fmt.Errorf("control: controllerv2 adapter cannot write slot replica move commits")
}

// RequestSlotLeaderTransfer is unsupported on the read-only ControllerV2 snapshot adapter.
func (a *ControllerV2Adapter) RequestSlotLeaderTransfer(ctx context.Context, req SlotLeaderTransferRequest) (SlotLeaderTransferResult, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotLeaderTransferResult{}, err
	}
	return SlotLeaderTransferResult{}, fmt.Errorf("control: controllerv2 adapter cannot write leader transfer intents")
}

// RequestSlotReplicaMove is unsupported on the read-only ControllerV2 snapshot adapter.
func (a *ControllerV2Adapter) RequestSlotReplicaMove(ctx context.Context, req SlotReplicaMoveRequest) (SlotReplicaMoveResult, error) {
	if err := ctxErr(ctx); err != nil {
		return SlotReplicaMoveResult{}, err
	}
	return SlotReplicaMoveResult{}, fmt.Errorf("control: controllerv2 adapter cannot write slot replica move intents")
}

// Watch returns snapshot update events.
func (a *ControllerV2Adapter) Watch() <-chan SnapshotEvent { return a.watch }

func mapControllerV2Roles(in []cv2.NodeRole) []Role {
	out := make([]Role, 0, len(in))
	for _, role := range in {
		switch role {
		case cv2.NodeRoleControllerVoter:
			out = append(out, RoleController)
		case cv2.NodeRoleData:
			out = append(out, RoleData)
		}
	}
	return out
}

func mapControllerV2Status(status cv2.NodeStatus) NodeStatus {
	switch status {
	case cv2.NodeStatusAlive:
		return NodeAlive
	case cv2.NodeStatusSuspect:
		return NodeSuspect
	case cv2.NodeStatusDown:
		return NodeDown
	default:
		return NodeDown
	}
}

func mapControllerV2JoinState(state cv2.NodeJoinState) NodeJoinState {
	switch state {
	case cv2.NodeJoinStateActive:
		return NodeJoinStateActive
	case cv2.NodeJoinStateJoining:
		return NodeJoinStateJoining
	case cv2.NodeJoinStateLeaving:
		return NodeJoinStateLeaving
	case cv2.NodeJoinStateRemoved:
		return NodeJoinStateRemoved
	default:
		return NodeJoinStateRemoved
	}
}

func mapControllerV2ParticipantProgress(in []cv2.TaskParticipantProgress) []TaskParticipantProgress {
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

var _ Controller = (*ControllerV2Adapter)(nil)
