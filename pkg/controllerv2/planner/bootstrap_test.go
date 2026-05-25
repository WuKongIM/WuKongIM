package planner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/stretchr/testify/require"
)

func TestBootstrapPlannerBlocksWhenInsufficientDataNodes(t *testing.T) {
	st := testPlannerState()
	st.Config.ReplicaCount = 3
	st.Nodes[2].JoinState = state.NodeJoinStateJoining

	decision, err := NewBootstrapPlanner().Next(context.Background(), testPlannerView(st))

	require.NoError(t, err)
	require.Equal(t, DecisionKindBlocked, decision.Kind)
	require.NotEmpty(t, decision.Reason)
	require.Empty(t, decision.Command.Kind)
}

func TestBootstrapPlannerReturnsNoneWhenAllSlotsAssignedAndInsufficientDataNodes(t *testing.T) {
	st := testPlannerState()
	st.Config.ReplicaCount = 3
	st.Nodes[2].Status = state.NodeStatusDown
	st.Slots = []state.SlotAssignment{
		testAssignment(1, []uint64{1, 2, 3}, 1, 1),
		testAssignment(2, []uint64{1, 2, 3}, 1, 2),
		testAssignment(3, []uint64{1, 2, 3}, 1, 3),
		testAssignment(4, []uint64{1, 2, 3}, 1, 1),
	}

	decision, err := NewBootstrapPlanner().Next(context.Background(), testPlannerView(st))

	require.NoError(t, err)
	require.Equal(t, DecisionKindNone, decision.Kind)
	require.Equal(t, reasonNoMissingSlot, decision.Reason)
	require.Empty(t, decision.Command.Kind)
}

func TestBootstrapPlannerPicksLowestMissingSlot(t *testing.T) {
	st := testPlannerState()
	st.Slots = []state.SlotAssignment{
		testAssignment(1, []uint64{1, 2}, 1, 1),
		testAssignment(3, []uint64{2, 3}, 1, 2),
	}

	decision, err := NewBootstrapPlanner().Next(context.Background(), testPlannerView(st))

	require.NoError(t, err)
	require.Equal(t, DecisionKindCommand, decision.Kind)
	require.Equal(t, uint32(2), decision.Command.Assignment.SlotID)
}

func TestBootstrapPlannerSkipsSlotWithActiveTask(t *testing.T) {
	st := testPlannerState()
	st.Tasks = []state.ReconcileTask{
		testBootstrapTask(1, []uint64{1, 2}, 1, 1),
	}

	decision, err := NewBootstrapPlanner().Next(context.Background(), testPlannerView(st))

	require.NoError(t, err)
	require.Equal(t, DecisionKindCommand, decision.Kind)
	require.Equal(t, uint32(2), decision.Command.Assignment.SlotID)
}

func TestBootstrapPlannerSpreadsReplicasAcrossEqualWeightNodes(t *testing.T) {
	st := testPlannerState()
	st.Nodes = append(st.Nodes, state.Node{NodeID: 4, Name: "n4", Addr: "n4", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 100})
	st.Slots = []state.SlotAssignment{
		testAssignment(1, []uint64{1, 2}, 1, 1),
		testAssignment(2, []uint64{1, 3}, 1, 1),
	}

	decision, err := NewBootstrapPlanner().Next(context.Background(), testPlannerView(st))

	require.NoError(t, err)
	require.Equal(t, DecisionKindCommand, decision.Kind)
	require.Equal(t, uint32(3), decision.Command.Assignment.SlotID)
	require.Equal(t, []uint64{2, 4}, decision.Command.Assignment.DesiredPeers)
}

func TestBootstrapPlannerUsesCapacityWeight(t *testing.T) {
	st := testPlannerState()
	st.Nodes[0].CapacityWeight = 100
	st.Nodes[1].CapacityWeight = 1
	st.Nodes[2].CapacityWeight = 1
	st.Slots = []state.SlotAssignment{
		testAssignment(1, []uint64{1, 2}, 1, 1),
		testAssignment(2, []uint64{1, 3}, 1, 1),
	}

	decision, err := NewBootstrapPlanner().Next(context.Background(), testPlannerView(st))

	require.NoError(t, err)
	require.Equal(t, DecisionKindCommand, decision.Kind)
	require.Equal(t, uint32(3), decision.Command.Assignment.SlotID)
	require.Equal(t, []uint64{1, 3}, decision.Command.Assignment.DesiredPeers)
}

func TestBootstrapPlannerTreatsZeroCapacityWeightAsDefaultEligible(t *testing.T) {
	st := testPlannerState()
	st.Config.ReplicaCount = 3
	st.Nodes[2].CapacityWeight = 0

	decision, err := NewBootstrapPlanner().Next(context.Background(), testPlannerView(st))

	require.NoError(t, err)
	require.Equal(t, DecisionKindCommand, decision.Kind)
	require.Equal(t, uint32(1), decision.Command.Assignment.SlotID)
	require.Equal(t, []uint64{1, 2, 3}, decision.Command.Assignment.DesiredPeers)
}

func TestBootstrapPlannerSpreadsPreferredLeader(t *testing.T) {
	st := testPlannerState()
	st.Config.ReplicaCount = 3
	st.Slots = []state.SlotAssignment{
		testAssignment(1, []uint64{1, 2, 3}, 1, 1),
		testAssignment(2, []uint64{1, 2, 3}, 1, 2),
	}

	decision, err := NewBootstrapPlanner().Next(context.Background(), testPlannerView(st))

	require.NoError(t, err)
	require.Equal(t, DecisionKindCommand, decision.Kind)
	require.Equal(t, uint32(3), decision.Command.Assignment.SlotID)
	require.Equal(t, []uint64{1, 2, 3}, decision.Command.Assignment.DesiredPeers)
	require.Equal(t, uint64(3), decision.Command.Assignment.PreferredLeader)
}

func TestBootstrapPlannerCommandContainsExpectedRevisionAssignmentAndTask(t *testing.T) {
	st := testPlannerState()
	st.Revision = 42
	now := time.Date(2026, 5, 24, 14, 30, 0, 0, time.UTC)

	decision, err := NewBootstrapPlanner().Next(context.Background(), View{State: st, Now: now})

	require.NoError(t, err)
	require.Equal(t, DecisionKindCommand, decision.Kind)
	require.Equal(t, command.KindUpsertSlotAssignmentAndTask, decision.Command.Kind)
	require.Equal(t, now, decision.Command.IssuedAt)
	require.NotNil(t, decision.Command.ExpectedRevision)
	require.Equal(t, uint64(42), *decision.Command.ExpectedRevision)
	require.Equal(t, &state.SlotAssignment{
		SlotID:          1,
		DesiredPeers:    []uint64{2, 3},
		ConfigEpoch:     1,
		PreferredLeader: 2,
	}, decision.Command.Assignment)
	require.Equal(t, &state.ReconcileTask{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		Kind:        state.TaskKindBootstrap,
		Step:        state.TaskStepCreateSlot,
		TargetNode:  2,
		TargetPeers: []uint64{2, 3},
		ConfigEpoch: 1,
		Status:      state.TaskStatusPending,
	}, decision.Command.Task)
}

func testPlannerView(st state.ClusterState) View {
	return View{
		State: st,
		Now:   time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
	}
}

func testPlannerState() state.ClusterState {
	table, _ := state.BuildInitialHashSlotTable(4, 16)
	return state.ClusterState{
		SchemaVersion: state.CurrentSchemaVersion,
		ClusterID:     "wk-planner-test",
		Revision:      1,
		UpdatedAt:     time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		Config: state.ClusterConfig{
			SlotCount:             4,
			HashSlotCount:         16,
			ReplicaCount:          2,
			DefaultCapacityWeight: 100,
		},
		Controllers: []state.ControllerVoter{
			{NodeID: 1, Addr: "n1", Role: state.ControllerRoleVoter},
		},
		Nodes: []state.Node{
			{NodeID: 1, Name: "n1", Addr: "n1", Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 100},
			{NodeID: 2, Name: "n2", Addr: "n2", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 100},
			{NodeID: 3, Name: "n3", Addr: "n3", Roles: []state.NodeRole{state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 100},
		},
		Slots:     []state.SlotAssignment{},
		HashSlots: table,
		Tasks:     []state.ReconcileTask{},
	}
}

func testAssignment(slotID uint32, peers []uint64, epoch uint64, leader uint64) state.SlotAssignment {
	return state.SlotAssignment{
		SlotID:          slotID,
		DesiredPeers:    append([]uint64(nil), peers...),
		ConfigEpoch:     epoch,
		PreferredLeader: leader,
	}
}

func testBootstrapTask(slotID uint32, peers []uint64, epoch uint64, target uint64) state.ReconcileTask {
	return state.ReconcileTask{
		TaskID:      fmt.Sprintf("slot-%d-bootstrap-%d", slotID, epoch),
		SlotID:      slotID,
		Kind:        state.TaskKindBootstrap,
		Step:        state.TaskStepCreateSlot,
		TargetNode:  target,
		TargetPeers: append([]uint64(nil), peers...),
		ConfigEpoch: epoch,
		Status:      state.TaskStatusPending,
	}
}
