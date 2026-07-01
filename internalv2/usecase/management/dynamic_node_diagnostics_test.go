package management

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestDynamicNodeDiagnosticsCombinesScaleInTasksAuditsAndSlots(t *testing.T) {
	generatedAt := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	var seenAuditReq ControllerTaskAuditListRequest
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID:   1,
			snapshot: dynamicNodeDiagnosticsLeavingSnapshot(),
		},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: scaleInRuntimeSummariesFor(88, 1, 2, 3, 4),
		},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				7: {SlotID: 7, LeaderID: 1, CurrentVoters: []uint64{1, 2, 4}},
				8: {SlotID: 8, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
		ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
			listSink: &seenAuditReq,
			items: []ControllerTaskAuditSnapshot{{
				TaskID:     "slot-7-replica-move-4-to-3-r88",
				Kind:       "slot_replica_move",
				Status:     "running",
				SlotID:     7,
				LeaderID:   1,
				SourceNode: 4,
				TargetNode: 3,
				StartedAt:  generatedAt.Add(-2 * time.Minute),
				EventCount: 4,
				LastReason: "waiting for source leadership transfer",
			}},
		},
		Now: func() time.Time { return generatedAt },
	})

	resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4, TaskLimit: 20, AuditLimit: 10, SlotLimit: 256})
	require.NoError(t, err)
	require.Equal(t, ControllerTaskAuditListRequest{NodeID: 4, Limit: 10}, seenAuditReq)
	require.Equal(t, uint64(4), resp.NodeID)
	require.Equal(t, uint64(88), resp.StateRevision)
	require.Equal(t, false, resp.Summary.SafeToRemove)
	require.Equal(t, 1, resp.Summary.ActiveTaskCount)
	require.Equal(t, 0, resp.Summary.FailedTaskCount)
	require.Equal(t, "waiting_leader_transfer", resp.Summary.SlotReplicaMoveState)
	require.Equal(t, int64(120), resp.Summary.OldestTaskAgeSeconds)
	require.Equal(t, "inspect_controller_task", resp.Summary.RecommendedNextAction)
	require.Len(t, resp.ActiveTasks, 1)
	require.Equal(t, "slot_replica_move", resp.ActiveTasks[0].Kind)
	require.Equal(t, "remove_voter", resp.ActiveTasks[0].Step)
	require.Equal(t, uint32(3), resp.ActiveTasks[0].PhaseIndex)
	require.Equal(t, uint64(101), resp.ActiveTasks[0].ObservedConfigIndex)
	require.Equal(t, []uint64{1, 2, 4}, resp.ActiveTasks[0].ObservedVoters)
	require.Len(t, resp.TaskAudits, 1)
	require.Equal(t, "waiting for source leadership transfer", resp.TaskAudits[0].LastReason)
	require.Len(t, resp.Slots, 1)
	require.Equal(t, uint32(7), resp.Slots[0].SlotID)
	require.Equal(t, "slot-7-replica-move-4-to-3-r88", resp.Slots[0].TaskID)
	require.Equal(t, "slot_replica_move", resp.Slots[0].TaskKind)
	require.Equal(t, "remove_voter", resp.Slots[0].TaskStep)
	require.Equal(t, "running", resp.Slots[0].TaskStatus)
	require.NotNil(t, resp.ScaleIn)
	require.Nil(t, resp.Onboarding)
	require.True(t, resp.Sources.ControlSnapshot.Available)
	require.True(t, resp.Sources.TaskAudit.Available)
	require.True(t, resp.Sources.SlotRuntime.Available)
}

func TestDynamicNodeDiagnosticsTreatsAuditUnavailableAsWarning(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: dynamicNodeDiagnosticsLeavingSnapshot()},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: scaleInRuntimeSummariesFor(88, 1, 2, 3, 4),
		},
		SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta: newChannelDrainMetaReader(),
		ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
			err: ErrControllerTaskAuditUnavailable,
		},
	})

	resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
	require.NoError(t, err)
	require.Empty(t, resp.TaskAudits)
	require.False(t, resp.Sources.TaskAudit.Available)
	require.False(t, resp.Summary.AuditAvailable)
	require.NotEmpty(t, resp.Warnings)
	require.Contains(t, resp.Warnings[0], "task audit unavailable")
}

func TestDynamicNodeDiagnosticsCopiesScaleInSummaryFields(t *testing.T) {
	snapshot := dynamicNodeDiagnosticsLeavingSnapshot()
	snapshot.Nodes[3].Health.Freshness = control.NodeHealthStale
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			snapshot: snapshot,
			nodeID:   1,
		},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: map[uint64]NodeRuntimeSummary{
				1: {NodeID: 1, ControlRevision: 88, Draining: true},
				2: {NodeID: 2, ControlRevision: 88, Draining: true},
				3: {NodeID: 3, ControlRevision: 88, Draining: true},
				4: {NodeID: 4, ControlRevision: 88, Draining: true},
			},
		},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				7: {SlotID: 7, LeaderID: 4, CurrentVoters: []uint64{1, 2, 4}},
			},
		},
		ChannelRuntimeMeta:  newChannelDrainMetaReader(),
		ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{items: nil},
	})

	resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
	require.NoError(t, err)
	require.Contains(t, resp.Summary.BlockedReasons, "blocked_by_tasks")
	require.Contains(t, resp.Summary.BlockedReasons, "target_health_stale")
	require.Equal(t, 1, resp.Summary.SlotLeaderCount)
}

func TestDynamicNodeDiagnosticsUsesSingleControlSnapshotForLeaving(t *testing.T) {
	snapshot := dynamicNodeDiagnosticsLeavingSnapshot()
	reader := &dynamicNodeDiagnosticsCountingSnapshotReader{snapshot: snapshot, nodeID: 1}
	app := New(Options{
		Cluster: reader,
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: scaleInRuntimeSummariesFor(snapshot.Revision, 1, 2, 3, 4),
		},
		SlotRuntimeStatus:   scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta:  newChannelDrainMetaReader(),
		ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{items: nil},
	})

	_, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
	require.NoError(t, err)
	require.Equal(t, 1, reader.calls)
}

func TestDynamicNodeDiagnosticsRuntimeUnknownUsesScaleInFlags(t *testing.T) {
	snapshot := dynamicNodeDiagnosticsLeavingSnapshot()
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: map[uint64]NodeRuntimeSummary{
				1: {NodeID: 1, Unknown: true},
				2: {NodeID: 2, ControlRevision: 88, Draining: true},
				3: {NodeID: 3, ControlRevision: 88, Draining: true},
				4: {NodeID: 4, ControlRevision: 88, Draining: true},
			},
		},
		SlotRuntimeStatus:   scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta:  newChannelDrainMetaReader(),
		ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{items: nil},
	})

	req := DynamicNodeDiagnosticsRequest{NodeID: 4}
	resp, err := app.DynamicNodeDiagnostics(context.Background(), req)
	require.NoError(t, err)
	require.True(t, resp.Summary.RuntimeUnknown)
}

func TestDynamicNodeDiagnosticsSlotRuntimeSourceAvailableWithoutRelatedSlots(t *testing.T) {
	snapshot := dynamicNodeDiagnosticsReadyToRemoveSnapshot()
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: scaleInRuntimeSummariesFor(snapshot.Revision, 1, 2, 3, 4),
		},
		SlotRuntimeStatus:   scaleInSafeSlotRuntimeReader{},
		ChannelRuntimeMeta:  newChannelDrainMetaReader(),
		ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{items: nil},
	})

	resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
	require.NoError(t, err)
	require.True(t, resp.Sources.SlotRuntime.Available)
}

func TestDynamicNodeDiagnosticSlotReplicaMoveState(t *testing.T) {
	tests := []struct {
		name  string
		tasks []control.ReconcileTask
		want  string
	}{
		{
			name: "no slot replica move",
			tasks: []control.ReconcileTask{
				{Kind: control.TaskKindLeaderTransfer, Status: control.TaskStatusRunning},
			},
			want: "no_active_move",
		},
		{
			name: "failed slot_replica_move",
			tasks: []control.ReconcileTask{{
				Kind:   control.TaskKindSlotReplicaMove,
				Status: control.TaskStatusFailed,
			}},
			want: "task_failed",
		},
		{
			name: "phase observation missing",
			tasks: []control.ReconcileTask{{
				Kind:                control.TaskKindSlotReplicaMove,
				Status:              control.TaskStatusRunning,
				Step:                control.TaskStepRemoveVoter,
				ObservedConfigIndex: 0,
			}},
			want: "phase_observation_missing",
		},
		{
			name: "waiting leader transfer",
			tasks: []control.ReconcileTask{{
				Kind:                control.TaskKindSlotReplicaMove,
				Status:              control.TaskStatusRunning,
				Step:                control.TaskStepRemoveVoter,
				ObservedConfigIndex: 9,
				ObservedVoters:      []uint64{1, 2, 4},
			}},
			want: "waiting_leader_transfer",
		},
		{
			name: "waiting learner catchup promote learner",
			tasks: []control.ReconcileTask{{
				Kind:                control.TaskKindSlotReplicaMove,
				Status:              control.TaskStatusRunning,
				Step:                control.TaskStepPromoteLearner,
				ObservedConfigIndex: 9,
				ObservedLearners:    []uint64{2},
			}},
			want: "waiting_learner_catchup",
		},
		{
			name: "unknown active step",
			tasks: []control.ReconcileTask{{
				Kind:                control.TaskKindSlotReplicaMove,
				Status:              control.TaskStatusRunning,
				Step:                control.TaskStepCommitAssignment,
				ObservedConfigIndex: 9,
			}},
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinStateTaskReplicaMoveState(tt.tasks)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDynamicNodeDiagnosticsReturnsNotFoundForMissingNode(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: dynamicNodeDiagnosticsLeavingSnapshot()},
	})

	_, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 99})
	require.ErrorIs(t, err, ErrDynamicNodeDiagnosticsNotFound)
}

func TestDynamicNodeDiagnosticsBoundsLimits(t *testing.T) {
	snapshot := dynamicNodeDiagnosticsLargeSnapshot()
	var seenAuditReq ControllerTaskAuditListRequest
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: scaleInRuntimeSummariesFor(snapshot.Revision, 1, 2, 3, 4),
		},
		ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
			listSink: &seenAuditReq,
			items:    dynamicNodeDiagnosticsAuditItems(15, 4),
		},
	})

	resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
	require.NoError(t, err)
	require.Len(t, resp.ActiveTasks, DefaultDynamicNodeDiagnosticTaskLimit)
	require.Len(t, resp.TaskAudits, DefaultDynamicNodeDiagnosticAuditLimit)
	require.Len(t, resp.Slots, DefaultDynamicNodeDiagnosticSlotLimit)
	require.Equal(t, ControllerTaskAuditListRequest{NodeID: 4, Limit: DefaultDynamicNodeDiagnosticAuditLimit}, seenAuditReq)

	_, err = app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 0})
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)

	for _, req := range []DynamicNodeDiagnosticsRequest{
		{NodeID: 4, TaskLimit: -1},
		{NodeID: 4, TaskLimit: MaxDynamicNodeDiagnosticTaskLimit + 1},
		{NodeID: 4, AuditLimit: -1},
		{NodeID: 4, AuditLimit: MaxDynamicNodeDiagnosticAuditLimit + 1},
		{NodeID: 4, SlotLimit: -1},
		{NodeID: 4, SlotLimit: MaxDynamicNodeDiagnosticSlotLimit + 1},
	} {
		_, err := app.DynamicNodeDiagnostics(context.Background(), req)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}

	_, err = (*App)(nil).DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
	require.ErrorIs(t, err, ErrNodeScaleInUnavailable)

	_, err = New(Options{}).DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
	require.ErrorIs(t, err, ErrNodeScaleInUnavailable)
}

func TestDynamicNodeDiagnosticsRecommendedActions(t *testing.T) {
	t.Run("inspect_controller_task", func(t *testing.T) {
		app := New(Options{
			Cluster: fakeNodeSnapshotReader{snapshot: dynamicNodeDiagnosticsLeavingSnapshot()},
			RuntimeSummary: fakeNodeRuntimeSummaryReader{
				summaries: scaleInRuntimeSummariesFor(88, 1, 2, 3, 4),
			},
			SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
			ChannelRuntimeMeta: newChannelDrainMetaReader(),
			ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
				items: dynamicNodeDiagnosticsAuditItems(1, 4),
			},
		})

		resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
		require.NoError(t, err)
		require.Equal(t, "inspect_controller_task", resp.Summary.RecommendedNextAction)
	})

	t.Run("inspect_controller_task_when_failed_tasks", func(t *testing.T) {
		snapshot := dynamicNodeDiagnosticsLeavingSnapshot()
		snapshot.Tasks[0].Status = control.TaskStatusFailed
		app := New(Options{
			Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
			RuntimeSummary: fakeNodeRuntimeSummaryReader{
				summaries: scaleInRuntimeSummariesFor(88, 1, 2, 3, 4),
			},
			SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
			ChannelRuntimeMeta: newChannelDrainMetaReader(),
			ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
				items: nil,
			},
		})

		resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
		require.NoError(t, err)
		require.Equal(t, 0, resp.Summary.ActiveTaskCount)
		require.Equal(t, 1, resp.Summary.FailedTaskCount)
		require.Equal(t, "inspect_controller_task", resp.Summary.RecommendedNextAction)
	})

	t.Run("wait_control_revision", func(t *testing.T) {
		snapshot := dynamicNodeDiagnosticsReadyToRemoveSnapshot()
		snapshot.Revision = 91
		snapshot.Nodes[3].Health.ObservedControlRevision = 90
		app := New(Options{
			Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
			RuntimeSummary: fakeNodeRuntimeSummaryReader{
				summaries: map[uint64]NodeRuntimeSummary{
					1: {NodeID: 1, ControlRevision: 91, Draining: true},
					2: {NodeID: 2, ControlRevision: 91, Draining: true},
					3: {NodeID: 3, ControlRevision: 91, Draining: true},
					4: {NodeID: 4, ControlRevision: 90, Draining: true},
				},
			},
			SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
			ChannelRuntimeMeta: newChannelDrainMetaReader(),
			ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
				items: nil,
			},
		})

		resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
		require.NoError(t, err)
		require.Equal(t, uint64(1), resp.Summary.ControlRevisionGap)
		require.Equal(t, "wait_control_revision", resp.Summary.RecommendedNextAction)
	})

	t.Run("inspect_slot_runtime", func(t *testing.T) {
		snapshot := dynamicNodeDiagnosticsReadyToRemoveSnapshot()
		snapshot.Slots = []control.SlotAssignment{{SlotID: 9, DesiredPeers: []uint64{1, 2}, PreferredLeader: 1, ConfigEpoch: 5}}
		app := New(Options{
			Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
			RuntimeSummary: fakeNodeRuntimeSummaryReader{
				summaries: scaleInRuntimeSummariesFor(snapshot.Revision, 1, 2, 3, 4),
			},
			ChannelRuntimeMeta: newChannelDrainMetaReader(),
			ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
				items: nil,
			},
		})

		resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
		require.NoError(t, err)
		require.True(t, resp.Summary.SlotRuntimeUnknown)
		require.Equal(t, "inspect_slot_runtime", resp.Summary.RecommendedNextAction)
	})

	t.Run("inspect_runtime", func(t *testing.T) {
		snapshot := dynamicNodeDiagnosticsReadyToRemoveSnapshot()
		app := New(Options{
			Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
			RuntimeSummary: fakeNodeRuntimeSummaryReader{
				summaries: map[uint64]NodeRuntimeSummary{
					1: {NodeID: 1, ControlRevision: snapshot.Revision, Draining: true},
					2: {NodeID: 2, ControlRevision: snapshot.Revision, Draining: true},
					3: {NodeID: 3, ControlRevision: snapshot.Revision, Draining: true},
				},
				errs: map[uint64]error{4: errors.New("runtime unavailable")},
			},
			SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
			ChannelRuntimeMeta: newChannelDrainMetaReader(),
			ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
				items: nil,
			},
		})

		resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
		require.NoError(t, err)
		require.True(t, resp.Summary.RuntimeUnknown)
		require.Equal(t, "inspect_runtime", resp.Summary.RecommendedNextAction)
	})

	t.Run("advance_slot_drain", func(t *testing.T) {
		snapshot := dynamicNodeDiagnosticsReadyToRemoveSnapshot()
		snapshot.Slots = []control.SlotAssignment{{SlotID: 9, DesiredPeers: []uint64{1, 2, 4}, PreferredLeader: 1, ConfigEpoch: 5}}
		app := New(Options{
			Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
			RuntimeSummary: fakeNodeRuntimeSummaryReader{
				summaries: scaleInRuntimeSummariesFor(snapshot.Revision, 1, 2, 3, 4),
			},
			SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
				statuses: map[uint32]SlotRuntimeStatus{
					9: {SlotID: 9, LeaderID: 1, CurrentVoters: []uint64{1, 2, 4}},
				},
			},
			ChannelRuntimeMeta: newChannelDrainMetaReader(),
			ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
				items: nil,
			},
		})

		resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
		require.NoError(t, err)
		require.True(t, resp.ScaleIn.BlockedBySlots)
		require.Zero(t, resp.Summary.ActiveTaskCount)
		require.Zero(t, resp.Summary.FailedTaskCount)
		require.Equal(t, "advance_slot_drain", resp.Summary.RecommendedNextAction)
	})

	t.Run("ready_to_remove", func(t *testing.T) {
		snapshot := dynamicNodeDiagnosticsReadyToRemoveSnapshot()
		app := New(Options{
			Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
			RuntimeSummary: fakeNodeRuntimeSummaryReader{
				summaries: scaleInRuntimeSummariesFor(snapshot.Revision, 1, 2, 3, 4),
			},
			SlotRuntimeStatus:  scaleInSafeSlotRuntimeReader{},
			ChannelRuntimeMeta: newChannelDrainMetaReader(),
			ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
				items: nil,
			},
		})

		resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
		require.NoError(t, err)
		require.True(t, resp.Summary.SafeToRemove)
		require.Equal(t, "ready_to_remove", resp.Summary.RecommendedNextAction)
	})

	t.Run("no_action", func(t *testing.T) {
		snapshot := control.Snapshot{
			Revision: 77,
			Nodes: []control.Node{
				scaleInHealthNode(1, []control.Role{control.RoleController, control.RoleData}, control.NodeJoinStateActive, 77),
				scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, 77),
			},
			Slots: []control.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1, 4}, PreferredLeader: 1, ConfigEpoch: 7},
			},
		}
		app := New(Options{
			Cluster: fakeNodeSnapshotReader{snapshot: snapshot},
			RuntimeSummary: fakeNodeRuntimeSummaryReader{
				summaries: scaleInRuntimeSummariesFor(snapshot.Revision, 1, 4),
			},
			SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
				statuses: map[uint32]SlotRuntimeStatus{
					1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 4}},
				},
			},
			ControllerTaskAudit: &dynamicNodeDiagnosticsAuditReader{
				items: nil,
			},
		})

		resp, err := app.DynamicNodeDiagnostics(context.Background(), DynamicNodeDiagnosticsRequest{NodeID: 4})
		require.NoError(t, err)
		require.Nil(t, resp.ScaleIn)
		require.NotNil(t, resp.Onboarding)
		require.Equal(t, "no_action", resp.Summary.RecommendedNextAction)
	})
}

func dynamicNodeDiagnosticsLeavingSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 88,
		Nodes: []control.Node{
			scaleInHealthNode(1, []control.Role{control.RoleController, control.RoleData}, control.NodeJoinStateActive, 88),
			scaleInHealthNode(2, []control.Role{control.RoleData}, control.NodeJoinStateActive, 88),
			scaleInHealthNode(3, []control.Role{control.RoleData}, control.NodeJoinStateActive, 88),
			scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateLeaving, 88),
		},
		Slots: []control.SlotAssignment{
			{SlotID: 7, DesiredPeers: []uint64{1, 2, 4}, PreferredLeader: 1, ConfigEpoch: 12},
			{SlotID: 8, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 2, ConfigEpoch: 13},
		},
		Tasks: []control.ReconcileTask{{
			TaskID:              "slot-7-replica-move-4-to-3-r88",
			SlotID:              7,
			Kind:                control.TaskKindSlotReplicaMove,
			Step:                control.TaskStepRemoveVoter,
			Status:              control.TaskStatusRunning,
			SourceNode:          4,
			TargetNode:          3,
			TargetPeers:         []uint64{1, 2, 3},
			CompletionPolicy:    control.TaskCompletionPolicyAllTargetPeers,
			ConfigEpoch:         12,
			Attempt:             2,
			PhaseIndex:          3,
			ObservedConfigIndex: 101,
			ObservedVoters:      []uint64{1, 2, 4},
			ParticipantProgress: []control.TaskParticipantProgress{
				{NodeID: 1, Attempt: 1, Status: control.TaskParticipantStatusDone},
				{NodeID: 4, Attempt: 1, Status: control.TaskParticipantStatusPending},
			},
		}},
	}
}

func dynamicNodeDiagnosticsReadyToRemoveSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 90,
		Nodes: []control.Node{
			scaleInHealthNode(1, []control.Role{control.RoleController, control.RoleData}, control.NodeJoinStateActive, 90),
			scaleInHealthNode(2, []control.Role{control.RoleData}, control.NodeJoinStateActive, 90),
			scaleInHealthNode(3, []control.Role{control.RoleData}, control.NodeJoinStateActive, 90),
			scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateLeaving, 90),
		},
		Slots: []control.SlotAssignment{
			{SlotID: 7, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 1, ConfigEpoch: 12},
		},
	}
}

func dynamicNodeDiagnosticsLargeSnapshot() control.Snapshot {
	snapshot := control.Snapshot{
		Revision: 99,
		Nodes: []control.Node{
			scaleInHealthNode(1, []control.Role{control.RoleController, control.RoleData}, control.NodeJoinStateActive, 99),
			scaleInHealthNode(2, []control.Role{control.RoleData}, control.NodeJoinStateActive, 99),
			scaleInHealthNode(3, []control.Role{control.RoleData}, control.NodeJoinStateActive, 99),
			scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateLeaving, 99),
		},
		Slots: make([]control.SlotAssignment, 0, 300),
		Tasks: make([]control.ReconcileTask, 0, 25),
	}
	for slotID := uint32(1); slotID <= 300; slotID++ {
		snapshot.Slots = append(snapshot.Slots, control.SlotAssignment{
			SlotID:          slotID,
			DesiredPeers:    []uint64{1, 2, 4},
			PreferredLeader: 1,
			ConfigEpoch:     uint64(slotID),
		})
		if slotID <= 25 {
			snapshot.Tasks = append(snapshot.Tasks, control.ReconcileTask{
				TaskID:      fmt.Sprintf("slot-%03d-replica-move-4-to-3", slotID),
				SlotID:      slotID,
				Kind:        control.TaskKindSlotReplicaMove,
				Step:        control.TaskStepPromoteLearner,
				Status:      control.TaskStatusPending,
				SourceNode:  4,
				TargetNode:  3,
				TargetPeers: []uint64{1, 2, 3},
				ConfigEpoch: uint64(slotID),
			})
		}
	}
	return snapshot
}

func dynamicNodeDiagnosticsAuditItems(count int, nodeID uint64) []ControllerTaskAuditSnapshot {
	items := make([]ControllerTaskAuditSnapshot, 0, count)
	for i := 0; i < count; i++ {
		items = append(items, ControllerTaskAuditSnapshot{
			TaskID:     fmt.Sprintf("audit-%02d", i),
			Kind:       "slot_replica_move",
			Status:     "running",
			SlotID:     uint32(i + 1),
			SourceNode: nodeID,
			TargetNode: 3,
			StartedAt:  time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second),
		})
	}
	return items
}

type dynamicNodeDiagnosticsAuditReader struct {
	listSink *ControllerTaskAuditListRequest
	items    []ControllerTaskAuditSnapshot
	err      error
}

func (r *dynamicNodeDiagnosticsAuditReader) ListControllerTaskAudits(_ context.Context, req ControllerTaskAuditListRequest) (ControllerTaskAuditListResponse, error) {
	if r.listSink != nil {
		*r.listSink = req
	}
	if r.err != nil {
		return ControllerTaskAuditListResponse{}, r.err
	}
	limit := req.Limit
	if limit <= 0 || limit > len(r.items) {
		limit = len(r.items)
	}
	items := append([]ControllerTaskAuditSnapshot(nil), r.items[:limit]...)
	return ControllerTaskAuditListResponse{
		Total:     len(r.items),
		Limit:     req.Limit,
		Truncated: len(r.items) > limit,
		Items:     items,
	}, nil
}

func (r *dynamicNodeDiagnosticsAuditReader) ControllerTaskAuditEvents(_ context.Context, taskID string) (ControllerTaskAuditEventsResponse, error) {
	if r.err != nil {
		return ControllerTaskAuditEventsResponse{}, r.err
	}
	return ControllerTaskAuditEventsResponse{
		Task: ControllerTaskAuditSnapshot{TaskID: taskID},
	}, nil
}

type dynamicNodeDiagnosticsCountingSnapshotReader struct {
	nodeID   uint64
	snapshot control.Snapshot
	err      error
	calls    int
}

func (r *dynamicNodeDiagnosticsCountingSnapshotReader) NodeID() uint64 {
	return r.nodeID
}

func (r *dynamicNodeDiagnosticsCountingSnapshotReader) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	r.calls++
	return r.snapshot.Clone(), r.err
}
