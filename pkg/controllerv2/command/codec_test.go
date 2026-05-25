package command

import (
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
			TaskResult: &TaskResult{TaskID: "slot-2-bootstrap-3", SlotID: 2, Err: "", FinishedAt: now},
			HashSlots:  &table,
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

func TestCommandDecodeRejectsUnknownVersion(t *testing.T) {
	_, err := Decode([]byte(`{"version":2,"command":{"kind":"init_cluster_state"}}`))
	require.ErrorIs(t, err, ErrUnsupportedVersion)
}
