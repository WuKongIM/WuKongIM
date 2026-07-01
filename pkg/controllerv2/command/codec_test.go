package command

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/stretchr/testify/require"
)

func TestCommandEncodeDecodeRoundTrip(t *testing.T) {
	expectedRevision := uint64(7)
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	table, err := state.BuildInitialHashSlotTable(4, 16)
	require.NoError(t, err)

	commands := []Command{
		{
			Kind:     KindInitClusterState,
			IssuedAt: now,
			Init: &InitClusterState{
				ClusterID: "wk-command-test",
				Config: state.ClusterConfig{
					SlotCount:             4,
					HashSlotCount:         16,
					ReplicaCount:          3,
					DefaultCapacityWeight: 10,
				},
				Controllers: []state.ControllerVoter{{NodeID: 1, Addr: "n1", Role: state.ControllerRoleVoter}},
				Nodes: []state.Node{
					{NodeID: 1, Addr: "n1", Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10},
					{NodeID: 2, Addr: "n2", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10},
					{NodeID: 3, Addr: "n3", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 10},
				},
			},
			HashSlots: &table,
		},
		{
			Kind:             KindUpsertSlotAssignmentAndTask,
			IssuedAt:         now.Add(time.Minute),
			ExpectedRevision: &expectedRevision,
			Assignment:       &state.SlotAssignment{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 3, PreferredLeader: 2},
			Task: &state.ReconcileTask{
				TaskID:      "slot-2-bootstrap-3",
				SlotID:      2,
				Kind:        state.TaskKindBootstrap,
				Step:        state.TaskStepCreateSlot,
				TargetNode:  2,
				TargetPeers: []uint64{1, 2, 3},
				ConfigEpoch: 3,
				Attempt:     1,
				Status:      state.TaskStatusRunning,
				LastError:   "previous transient error",
			},
			TaskResult: &TaskResult{
				TaskID:      "slot-2-bootstrap-3",
				SlotID:      2,
				TaskKind:    state.TaskKindBootstrap,
				ConfigEpoch: 3,
				Attempt:     1,
				FinishedAt:  now,
			},
			HashSlots: &table,
		},
		{
			Kind:             KindUpsertSlotAssignmentAndTask,
			IssuedAt:         now.Add(90 * time.Second),
			ExpectedRevision: &expectedRevision,
			Assignment:       &state.SlotAssignment{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 2},
			Task: &state.ReconcileTask{
				TaskID:           "slot-2-leader-transfer-7-r7",
				SlotID:           2,
				Kind:             state.TaskKindLeaderTransfer,
				Step:             state.TaskStepTransferLeader,
				SourceNode:       1,
				TargetNode:       2,
				TargetPeers:      []uint64{1, 2, 3},
				CompletionPolicy: state.TaskCompletionPolicySingleObserver,
				ConfigEpoch:      7,
				Status:           state.TaskStatusPending,
			},
		},
		{
			Kind:     KindReportTaskProgress,
			IssuedAt: now.Add(2 * time.Minute),
			TaskProgress: &TaskProgress{
				TaskID:             "slot-2-bootstrap-3",
				SlotID:             2,
				TaskKind:           state.TaskKindBootstrap,
				ConfigEpoch:        3,
				TaskAttempt:        1,
				ParticipantNodeID:  2,
				ParticipantAttempt: 0,
				Status:             state.TaskParticipantStatusDone,
				FinishedAt:         now.Add(2 * time.Minute),
			},
		},
	}

	for _, want := range commands {
		data, err := Encode(want)
		require.NoError(t, err)
		require.Contains(t, string(data), `"version":1`)
		require.Contains(t, string(data), `"issued_at"`)

		got, err := Decode(data)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestCommandLeaderTransferTaskCodecRoundTrip(t *testing.T) {
	expectedRevision := uint64(9)
	now := time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC)
	want := Command{
		Kind:             KindUpsertSlotAssignmentAndTask,
		IssuedAt:         now,
		ExpectedRevision: &expectedRevision,
		Assignment:       &state.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 2},
		Task: &state.ReconcileTask{
			TaskID:           "slot-1-leader-transfer-7-r9",
			SlotID:           1,
			Kind:             state.TaskKindLeaderTransfer,
			Step:             state.TaskStepTransferLeader,
			SourceNode:       1,
			TargetNode:       2,
			TargetPeers:      []uint64{1, 2, 3},
			CompletionPolicy: state.TaskCompletionPolicySingleObserver,
			ConfigEpoch:      7,
			Status:           state.TaskStatusPending,
		},
	}

	data, err := Encode(want)
	require.NoError(t, err)
	got, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestControllerVoterPromotionCommandRoundTrip(t *testing.T) {
	expectedRevision := uint64(11)
	now := time.Date(2026, 5, 24, 14, 0, 0, 0, time.UTC)
	want := Command{
		Kind:             KindPromoteControllerVoter,
		IssuedAt:         now,
		ExpectedRevision: &expectedRevision,
		ControllerVoterPromotion: &ControllerVoterPromotion{
			TargetNodeID:           3,
			TargetAddr:             "n3",
			ExpectedPreviousVoters: []uint64{1, 2},
			ObservedConfigIndex:    42,
			ObservedVoters:         []uint64{1, 2, 3},
		},
	}

	data, err := Encode(want)
	require.NoError(t, err)
	require.Contains(t, string(data), `"controller_voter_promotion"`)

	got, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestControllerVoterPromotionCommandPreservesExplicitEmptyExpectedVoters(t *testing.T) {
	now := time.Date(2026, 5, 24, 15, 0, 0, 0, time.UTC)
	want := Command{
		Kind:     KindPromoteControllerVoter,
		IssuedAt: now,
		ControllerVoterPromotion: &ControllerVoterPromotion{
			TargetNodeID:           3,
			TargetAddr:             "n3",
			ExpectedPreviousVoters: []uint64{},
			ObservedConfigIndex:    42,
			ObservedVoters:         []uint64{1, 2, 3},
		},
	}

	data, err := Encode(want)
	require.NoError(t, err)
	requireJSONPathEqual(t, data, []string{"command", "controller_voter_promotion", "expected_previous_voters"}, []any{})

	got, err := Decode(data)
	require.NoError(t, err)
	require.NotNil(t, got.ControllerVoterPromotion.ExpectedPreviousVoters)
	require.Empty(t, got.ControllerVoterPromotion.ExpectedPreviousVoters)
}

func TestControllerVoterPromotionCommandPreservesNilExpectedVoters(t *testing.T) {
	now := time.Date(2026, 5, 24, 16, 0, 0, 0, time.UTC)
	want := Command{
		Kind:     KindPromoteControllerVoter,
		IssuedAt: now,
		ControllerVoterPromotion: &ControllerVoterPromotion{
			TargetNodeID:        3,
			TargetAddr:          "n3",
			ObservedConfigIndex: 42,
			ObservedVoters:      []uint64{1, 2, 3},
		},
	}

	data, err := Encode(want)
	require.NoError(t, err)
	requireJSONPathEqual(t, data, []string{"command", "controller_voter_promotion", "expected_previous_voters"}, nil)

	got, err := Decode(data)
	require.NoError(t, err)
	require.Nil(t, got.ControllerVoterPromotion.ExpectedPreviousVoters)
}

func TestCommandDecodeRejectsUnknownVersion(t *testing.T) {
	_, err := Decode([]byte(`{"version":2,"command":{"kind":"init_cluster_state"}}`))
	require.ErrorIs(t, err, ErrUnsupportedVersion)
}

func requireJSONPathEqual(t *testing.T, data []byte, path []string, want any) {
	t.Helper()
	var got any
	require.NoError(t, json.Unmarshal(data, &got))
	for _, key := range path {
		object, ok := got.(map[string]any)
		require.True(t, ok, "expected object while resolving %v", path)
		got, ok = object[key]
		require.True(t, ok, "missing JSON key %q while resolving %v", key, path)
	}
	require.Equal(t, want, got)
}
