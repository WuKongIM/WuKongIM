package plane

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

func TestPlannerCreatesBootstrapTaskForBrandNewSlot(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 4, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindBootstrap, decision.Task.Kind)
	require.Len(t, decision.Assignment.DesiredPeers, 3)
}

func TestPlannerBootstrapRotatesTargetNodeAcrossSlots(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 4, ReplicaN: 3})
	state := testState(aliveNode(1), aliveNode(2), aliveNode(3))

	wantTargets := []uint64{1, 2, 3, 1}
	for slotID := uint32(1); slotID <= 4; slotID++ {
		decision, err := planner.ReconcileSlot(context.Background(), state, slotID)

		require.NoError(t, err)
		require.NotNil(t, decision.Task)
		require.Equal(t, controllermeta.TaskKindBootstrap, decision.Task.Kind)
		require.Equal(t, wantTargets[slotID-1], decision.Task.TargetNode)
		require.Equal(t, decision.Assignment.PreferredLeader, decision.Task.TargetNode)
		state.Assignments[slotID] = decision.Assignment
	}
}

func TestPlannerBootstrapPersistsPreferredLeaderAndUsesAsTarget(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 4, ReplicaN: 3, LeaderSkewThreshold: 1})
	state := testState(aliveNode(1), aliveNode(2), aliveNode(3))

	decision, err := planner.ReconcileSlot(context.Background(), state, 2)

	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
	require.Equal(t, decision.Assignment.PreferredLeader, decision.Task.TargetNode)
}

func TestPlannerBootstrapBalancesPreferredLeaderLoad(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 6, ReplicaN: 3, LeaderSkewThreshold: 1})
	state := testState(aliveNode(1), aliveNode(2), aliveNode(3))
	loads := map[uint64]int{}

	for slotID := uint32(1); slotID <= 6; slotID++ {
		decision, err := planner.ReconcileSlot(context.Background(), state, slotID)

		require.NoError(t, err)
		require.NotZero(t, decision.Assignment.PreferredLeader)
		loads[decision.Assignment.PreferredLeader]++
		state.Assignments[slotID] = decision.Assignment
	}

	require.LessOrEqual(t, maxTestLoad(loads)-minTestLoad(loads, []uint64{1, 2, 3}), 1)
}

func TestPlannerBootstrapSkipsInactiveOrNonDataNodes(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		withMembershipNode(1, controllermeta.NodeStatusAlive, controllermeta.NodeRoleData, controllermeta.NodeJoinStateJoining),
		withMembershipNode(2, controllermeta.NodeStatusAlive, controllermeta.NodeRoleControllerVoter, controllermeta.NodeJoinStateActive),
		aliveNode(3), aliveNode(4), aliveNode(5),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, []uint64{3, 4, 5}, decision.Assignment.DesiredPeers)
}

func TestPlannerPrefersRepairBeforeRebalance(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 2, 4),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(2, []uint64{1, 2, 4}, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, uint32(1), decision.SlotID)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRepair, decision.Task.Kind)
}

func TestPlannerDoesNotBootstrapRemovedInitialSlotOutsidePhysicalSet(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 3, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withPhysicalSlots(1, 3),
		withAssignment(1, 1, 2, 3),
		withAssignment(3, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(3, []uint64{1, 2, 3}, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Zero(t, decision.SlotID)
	require.Nil(t, decision.Task)
}

func TestPlannerRepairsAddedSlotBeyondInitialSlotCount(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4),
		withPhysicalSlots(1, 2, 3),
		withAssignment(1, 1, 2, 4),
		withAssignment(2, 1, 2, 4),
		withAssignment(3, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 4}, true),
		withRuntimeView(2, []uint64{1, 2, 4}, true),
		withRuntimeView(3, []uint64{1, 2, 3}, true),
	)
	state.PauseRebalance = true

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, uint32(3), decision.SlotID)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRepair, decision.Task.Kind)
}

func TestPlannerRepairTargetSkipsInactiveOrNonDataNodes(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3),
		withMembershipNode(4, controllermeta.NodeStatusAlive, controllermeta.NodeRoleData, controllermeta.NodeJoinStateJoining),
		withMembershipNode(5, controllermeta.NodeStatusAlive, controllermeta.NodeRoleControllerVoter, controllermeta.NodeJoinStateActive),
		aliveNode(6),
		withAssignment(1, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRepair, decision.Task.Kind)
	require.Equal(t, uint64(6), decision.Task.TargetNode)
}

func TestPlannerRepairPreservesPreferredLeaderWhenStillDesired(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), deadNode(2), aliveNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)

	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, uint64(3), decision.Assignment.PreferredLeader)
	require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
}

func TestPlannerRepairReplacesPreferredLeaderWhenSourceRemoved(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), deadNode(2), aliveNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)

	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.NotEqual(t, uint64(2), decision.Assignment.PreferredLeader)
	require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
}

func TestPlannerRepairReplacesPreferredLeaderWhenStillDesiredButIneligible(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), drainingNode(2), deadNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)

	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.NotEqual(t, uint64(2), decision.Assignment.PreferredLeader)
	require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
}

func TestPlannerRepairReplacementPreferredLeaderSkipsIneligibleNextPeers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option stateOption
	}{
		{name: "draining", option: drainingNode(2)},
		{name: "joining", option: withMembershipNode(2, controllermeta.NodeStatusAlive, controllermeta.NodeRoleData, controllermeta.NodeJoinStateJoining)},
		{name: "controller-only", option: withMembershipNode(2, controllermeta.NodeStatusAlive, controllermeta.NodeRoleControllerVoter, controllermeta.NodeJoinStateActive)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
			state := testState(
				aliveNode(1), tc.option, aliveNode(4), deadNode(5), aliveNode(6),
				withAssignment(1, 1, 5, 2), withPreferredLeader(1, 5),
				withRuntimeView(1, []uint64{1, 5, 2}, true),
			)

			decision, err := planner.ReconcileSlot(context.Background(), state, 1)

			require.NoError(t, err)
			require.NotNil(t, decision.Task)
			require.Equal(t, controllermeta.TaskKindRepair, decision.Task.Kind)
			require.NotEqual(t, uint64(2), decision.Assignment.PreferredLeader)
			require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
			require.True(t, nodeSchedulableForData(state.Nodes[decision.Assignment.PreferredLeader]))
		})
	}
}

func TestPlannerPreferredLeaderReplacementBalancesWithoutCurrentSlotContribution(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 3, ReplicaN: 3})
	current := controllermeta.SlotAssignment{
		SlotID:          1,
		DesiredPeers:    []uint64{1, 2, 5},
		PreferredLeader: 5,
	}
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), deadNode(5),
		func(state *PlannerState) { state.Assignments[1] = current },
		withRuntimeViewVoters(1, []uint64{1, 2, 5}, 1, true),
		withAssignment(2, 2, 3, 5), withPreferredLeader(2, 2),
	)

	preferred := planner.preferredLeaderForPeers(state, current, []uint64{1, 2, 3})

	require.Equal(t, uint64(1), preferred)
}

func TestPlannerRepairsInactiveOrNonDataExistingPeer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		role      controllermeta.NodeRole
		joinState controllermeta.NodeJoinState
	}{
		{
			name:      "joining data node",
			role:      controllermeta.NodeRoleData,
			joinState: controllermeta.NodeJoinStateJoining,
		},
		{
			name:      "controller voter node",
			role:      controllermeta.NodeRoleControllerVoter,
			joinState: controllermeta.NodeJoinStateActive,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
			state := testState(
				aliveNode(1), aliveNode(2),
				withMembershipNode(3, controllermeta.NodeStatusAlive, tc.role, tc.joinState),
				aliveNode(4),
				withAssignment(1, 1, 2, 3),
				withRuntimeView(1, []uint64{1, 2, 3}, true),
			)

			decision, err := planner.ReconcileSlot(context.Background(), state, 1)
			require.NoError(t, err)
			require.NotNil(t, decision.Task)
			require.Equal(t, controllermeta.TaskKindRepair, decision.Task.Kind)
			require.Equal(t, uint64(3), decision.Task.SourceNode)
			require.Equal(t, uint64(4), decision.Task.TargetNode)
			require.Equal(t, []uint64{1, 2, 4}, decision.Assignment.DesiredPeers)
		})
	}
}

func TestPlannerRebalanceSetsPreferredLeaderWhenOldPreferredRemoved(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 2, RebalanceSkewThreshold: 1})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withAssignment(1, 1, 2), withPreferredLeader(1, 1), withRuntimeView(1, []uint64{1, 2}, true),
		withAssignment(2, 1, 2), withPreferredLeader(2, 2), withRuntimeView(2, []uint64{1, 2}, true),
	)

	decision := planner.nextRebalanceDecision(state)

	require.NotZero(t, decision.SlotID)
	require.NotNil(t, decision.Task)
	require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
}

func TestPlannerDoesNotMigrateOnSuspectNode(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3, RebalanceSkewThreshold: 2})
	state := testState(
		aliveNode(1), aliveNode(2), suspectNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.Nil(t, decision.Task)
	require.False(t, decision.Degraded)
}

func TestPlannerSkipsRepairForMigratingSlot(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withMigratingSlot(1),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.Equal(t, uint32(1), decision.SlotID)
	require.Nil(t, decision.Task)
}

func TestPlannerRebalancesOnlyWhenSkewExceedsThreshold(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 4, ReplicaN: 3, RebalanceSkewThreshold: 2})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 2, 3),
		withAssignment(3, 1, 2, 3),
		withAssignment(4, 1, 2, 4),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(2, []uint64{1, 2, 3}, true),
		withRuntimeView(3, []uint64{1, 2, 3}, true),
		withRuntimeView(4, []uint64{1, 2, 4}, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRebalance, decision.Task.Kind)
	require.Equal(t, uint64(4), decision.Task.TargetNode)
}

func TestPlannerRebalanceSkipsInactiveOrNonDataNodes(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3, RebalanceSkewThreshold: 2})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withMembershipNode(4, controllermeta.NodeStatusAlive, controllermeta.NodeRoleData, controllermeta.NodeJoinStateJoining),
		withMembershipNode(5, controllermeta.NodeStatusAlive, controllermeta.NodeRoleControllerVoter, controllermeta.NodeJoinStateActive),
		aliveNode(6),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(2, []uint64{1, 2, 3}, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRebalance, decision.Task.Kind)
	require.Equal(t, uint64(6), decision.Task.TargetNode)
}

func TestPlannerSkipsMigratingSlotDuringRebalanceSelection(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3, RebalanceSkewThreshold: 2})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(2, []uint64{1, 2, 3}, true),
		withMigratingSlot(1),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, uint32(2), decision.SlotID)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRebalance, decision.Task.Kind)
}

func TestPlannerRebalancesWhenSkewMatchesThreshold(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3, RebalanceSkewThreshold: 2})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(2, []uint64{1, 2, 3}, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRebalance, decision.Task.Kind)
	require.Equal(t, uint64(4), decision.Task.TargetNode)
}

func TestPlannerDoesNotRebalanceOptimalSingleSkewDistribution(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 2, 4),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(2, []uint64{1, 2, 4}, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Zero(t, decision.SlotID)
	require.Nil(t, decision.Task)
}

func TestPlannerStopsAutomaticChangesAfterQuorumLoss(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), deadNode(2), deadNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withRuntimeView(1, []uint64{1}, false),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.Nil(t, decision.Task)
	require.True(t, decision.Degraded)
}

func TestPlannerSkipsDegradedSlotAndReturnsLaterRepairDecision(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4), deadNode(5),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 4, 5),
		withRuntimeView(1, []uint64{1, 2}, false),
		withRuntimeView(2, []uint64{1, 4, 5}, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, uint32(2), decision.SlotID)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRepair, decision.Task.Kind)
	require.False(t, decision.Degraded)
}

func TestPlannerDoesNotReissueRetryingTaskBeforeNextRunAt(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withTask(controllermeta.ReconcileTask{
			SlotID:     1,
			Kind:       controllermeta.TaskKindRepair,
			Step:       controllermeta.TaskStepAddLearner,
			SourceNode: 3,
			TargetNode: 4,
			Status:     controllermeta.TaskStatusRetrying,
			NextRunAt:  time.Unix(200, 0),
		}),
	)

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Zero(t, decision.SlotID)
	require.Nil(t, decision.Task)

	state.Now = time.Unix(200, 0)
	decision, err = planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, uint32(1), decision.SlotID)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskStatusRetrying, decision.Task.Status)
}

func TestPlannerFailedTaskBlocksAutomaticRegeneration(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withTask(controllermeta.ReconcileTask{
			SlotID:     1,
			Kind:       controllermeta.TaskKindRepair,
			Step:       controllermeta.TaskStepAddLearner,
			SourceNode: 3,
			TargetNode: 4,
			Status:     controllermeta.TaskStatusFailed,
			LastError:  "operator action required",
		}),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.Nil(t, decision.Task)
	require.Equal(t, uint32(1), decision.Assignment.SlotID)
}

func TestPlannerDegradedSlotDoesNotReissueExistingTaskAndDoesNotBlockLaterPlanning(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4), deadNode(5),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 4, 5),
		withRuntimeView(1, []uint64{1, 2}, false),
		withRuntimeView(2, []uint64{1, 4, 5}, true),
		withTask(controllermeta.ReconcileTask{
			SlotID:     1,
			Kind:       controllermeta.TaskKindRepair,
			Step:       controllermeta.TaskStepAddLearner,
			SourceNode: 3,
			TargetNode: 4,
			Status:     controllermeta.TaskStatusPending,
		}),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.True(t, decision.Degraded)
	require.Nil(t, decision.Task)

	decision, err = planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Equal(t, uint32(2), decision.SlotID)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRepair, decision.Task.Kind)
}

func TestPlannerBootstrapUsesLeastLoadedAliveNodes(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 4, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4), aliveNode(5),
		withAssignment(10, 1, 2, 3),
		withAssignment(11, 1, 2, 3),
		withAssignment(12, 1, 2, 4),
	)

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, []uint64{3, 4, 5}, decision.Assignment.DesiredPeers)
}

func TestPlannerInitializesAndIncrementsConfigEpochOnMembershipChanges(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3})

	bootstrapDecision, err := planner.ReconcileSlot(context.Background(), testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
	), 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, bootstrapDecision.Assignment.ConfigEpoch)

	repairDecision, err := planner.ReconcileSlot(context.Background(), testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4),
		withAssignment(2, 1, 2, 3),
		withAssignmentConfigEpoch(2, 7),
		withRuntimeView(2, []uint64{1, 2, 3}, true),
	), 2)
	require.NoError(t, err)
	require.NotNil(t, repairDecision.Task)
	require.EqualValues(t, 8, repairDecision.Assignment.ConfigEpoch)
}

func TestPlannerLeaderRebalanceCreatesTransferTaskForSkew(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 3, ReplicaN: 3, LeaderSkewThreshold: 1})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2), withRuntimeViewVoters(1, []uint64{1, 2, 3}, 1, true),
		withAssignment(2, 1, 2, 3), withPreferredLeader(2, 2), withRuntimeViewVoters(2, []uint64{1, 2, 3}, 1, true),
		withAssignment(3, 1, 2, 3), withPreferredLeader(3, 3), withRuntimeViewVoters(3, []uint64{1, 2, 3}, 2, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)

	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindLeaderTransfer, decision.Task.Kind)
	require.Equal(t, controllermeta.TaskStepTransferLeader, decision.Task.Step)
	require.Equal(t, uint64(1), decision.Task.SourceNode)
	require.Equal(t, uint64(3), decision.Task.TargetNode)
	require.Equal(t, uint64(3), decision.Assignment.PreferredLeader)
}

func TestPlannerLeaderRebalanceTriesEligibleLowLoadVotersBeyondGlobalMin(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 5, ReplicaN: 3, LeaderSkewThreshold: 2})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 1), withRuntimeViewVoters(1, []uint64{1, 2, 3}, 1, true),
		withAssignment(2, 1, 2, 3), withPreferredLeader(2, 1), withRuntimeViewVoters(2, []uint64{1, 2, 3}, 1, true),
		withAssignment(3, 1, 2, 3), withPreferredLeader(3, 1), withRuntimeViewVoters(3, []uint64{1, 2, 3}, 1, true),
		withAssignment(4, 1, 2, 3), withPreferredLeader(4, 2), withRuntimeViewVoters(4, []uint64{1, 2, 3}, 2, true),
		withAssignment(5, 1, 2, 3), withPreferredLeader(5, 3), withRuntimeViewVoters(5, []uint64{1, 2, 3}, 3, true),
	)

	decision := planner.nextLeaderPlacementDecision(state)

	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindLeaderTransfer, decision.Task.Kind)
	require.Equal(t, uint64(1), decision.Task.SourceNode)
	require.Equal(t, uint64(2), decision.Task.TargetNode)
}

func TestPlannerLeaderRebalanceSkipsWhenAnyAssignedSlotMissingRuntimeView(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 3, ReplicaN: 3, LeaderSkewThreshold: 1})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2), withRuntimeViewVoters(1, []uint64{1, 2, 3}, 1, true),
		withAssignment(2, 1, 2, 3), withPreferredLeader(2, 2), withRuntimeViewVoters(2, []uint64{1, 2, 3}, 1, true),
		withAssignment(3, 1, 2, 3), withPreferredLeader(3, 3),
	)

	decision, err := planner.NextDecision(context.Background(), state)

	require.NoError(t, err)
	require.Nil(t, decision.Task)
}

func TestPlannerLeaderRebalanceSkipsSingleVoterSingleNode(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 1, LeaderSkewThreshold: 1})
	state := testState(
		aliveNode(1),
		withAssignment(1, 1), withPreferredLeader(1, 1), withRuntimeViewVoters(1, []uint64{1}, 1, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)

	require.NoError(t, err)
	require.Nil(t, decision.Task)
}

func TestPlannerActualLeaderLoadsIgnoresSlotsThatCannotTransfer(t *testing.T) {
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withAssignment(1, 1, 2, 3), withRuntimeViewVoters(1, []uint64{1, 2, 3}, 1, true),
		withAssignment(2, 1, 2, 3), withRuntimeViewVoters(2, []uint64{1, 2, 3}, 1, true), withMigratingSlot(2),
		withAssignment(3, 1, 2, 3), withRuntimeViewVoters(3, []uint64{1, 2, 3}, 1, true), withLockedSlot(3),
		withAssignment(4, 1), withRuntimeViewVoters(4, []uint64{1}, 1, true),
	)

	loads := actualLeaderLoads(state)

	require.Equal(t, 1, loads[1])
}

func TestPlannerLeaderRebalanceSkipsMissingCurrentVoters(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3, LeaderSkewThreshold: 1})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2), withRuntimeView(1, []uint64{1, 2, 3}, true),
		withAssignment(2, 1, 2, 3), withPreferredLeader(2, 3), withRuntimeViewVoters(2, []uint64{1, 2, 3}, 1, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)

	require.NoError(t, err)
	require.Nil(t, decision.Task)
}

func TestPlannerLeaderRebalanceSkipsWhenTargetNotInCurrentVoters(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 3, ReplicaN: 3, LeaderSkewThreshold: 1})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 3), withRuntimeViewVoters(1, []uint64{1, 2}, 1, true),
		withAssignment(2, 1, 2, 3), withPreferredLeader(2, 2), withRuntimeViewVoters(2, []uint64{1, 2, 3}, 2, true),
		withAssignment(3, 1, 2, 3), withPreferredLeader(3, 3), withRuntimeViewVoters(3, []uint64{1, 2}, 1, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)

	require.NoError(t, err)
	require.Nil(t, decision.Task)
}

func TestPlannerLeaderRebalanceSkipsIneligibleTargetNodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option stateOption
	}{
		{name: "missing", option: func(*PlannerState) {}},
		{name: "dead", option: deadNode(2)},
		{name: "suspect", option: suspectNode(2)},
		{name: "draining", option: drainingNode(2)},
		{name: "joining", option: withMembershipNode(2, controllermeta.NodeStatusAlive, controllermeta.NodeRoleData, controllermeta.NodeJoinStateJoining)},
		{name: "controller-only", option: withMembershipNode(2, controllermeta.NodeStatusAlive, controllermeta.NodeRoleControllerVoter, controllermeta.NodeJoinStateActive)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3, LeaderSkewThreshold: 1})
			state := testState(
				aliveNode(1), tc.option, aliveNode(3),
				withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2), withRuntimeViewVoters(1, []uint64{1, 2, 3}, 1, true),
				withAssignment(2, 1, 2, 3), withPreferredLeader(2, 3), withRuntimeViewVoters(2, []uint64{1, 2, 3}, 3, true),
			)

			decision := planner.nextLeaderPlacementDecision(state)

			require.Nil(t, decision.Task)
		})
	}
}

func TestPlannerLeaderRebalanceSkipsCooldown(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3, LeaderSkewThreshold: 1})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2), withLeaderTransferCooldown(1, time.Unix(130, 0)), withRuntimeViewVoters(1, []uint64{1, 2, 3}, 1, true),
		withAssignment(2, 1, 2, 3), withPreferredLeader(2, 3), withRuntimeViewVoters(2, []uint64{1, 2, 3}, 3, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)

	require.NoError(t, err)
	require.Nil(t, decision.Task)
}

func TestPlannerLeaderRebalanceSkipsBalancedButWrongPreferenceWhenTransferWouldExceedThreshold(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 3, ReplicaN: 3, LeaderSkewThreshold: 1})
	state := testState(
		aliveNode(1), aliveNode(2), aliveNode(3),
		withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2), withRuntimeViewVoters(1, []uint64{1, 2, 3}, 1, true),
		withAssignment(2, 1, 2, 3), withPreferredLeader(2, 2), withRuntimeViewVoters(2, []uint64{1, 2, 3}, 2, true),
		withAssignment(3, 1, 2, 3), withPreferredLeader(3, 3), withRuntimeViewVoters(3, []uint64{1, 2, 3}, 3, true),
	)

	decision, err := planner.NextDecision(context.Background(), state)

	require.NoError(t, err)
	require.Nil(t, decision.Task)
}

func TestControllerTickNoOpsWhenNotLeader(t *testing.T) {
	store := openControllerStore(t)
	require.NoError(t, store.Close())

	controller := NewController(store, ControllerConfig{
		Planner:  PlannerConfig{SlotCount: 1, ReplicaN: 3},
		IsLeader: func() bool { return false },
	})

	require.NoError(t, controller.Tick(context.Background()))
}

func TestControllerTickReturnsClosedWhenNil(t *testing.T) {
	var controller *Controller

	require.ErrorIs(t, controller.Tick(context.Background()), controllermeta.ErrClosed)
}

func TestControllerTickProposesDecisionWhenProposerConfigured(t *testing.T) {
	store := openControllerStore(t)
	ctx := context.Background()
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		require.NoError(t, store.UpsertNode(ctx, controllermeta.ClusterNode{
			NodeID:          nodeID,
			Addr:            fmt.Sprintf("127.0.0.1:700%d", nodeID),
			Role:            controllermeta.NodeRoleData,
			JoinState:       controllermeta.NodeJoinStateActive,
			Status:          controllermeta.NodeStatusAlive,
			LastHeartbeatAt: time.Unix(100, 0),
			CapacityWeight:  1,
		}))
	}

	var proposed []Command
	controller := NewController(store, ControllerConfig{
		Planner: PlannerConfig{SlotCount: 1, ReplicaN: 3},
		Now:     func() time.Time { return time.Unix(100, 0) },
		Propose: func(_ context.Context, cmd Command) error {
			proposed = append(proposed, cmd)
			return nil
		},
	})

	require.NoError(t, controller.Tick(ctx))
	require.Len(t, proposed, 1)
	require.Equal(t, CommandKindAssignmentTaskUpdate, proposed[0].Kind)
	require.NotNil(t, proposed[0].Assignment)
	require.Equal(t, uint32(1), proposed[0].Assignment.SlotID)
	require.NotNil(t, proposed[0].Task)
	require.Equal(t, controllermeta.TaskKindBootstrap, proposed[0].Task.Kind)
	require.Equal(t, controllermeta.TaskStatusPending, proposed[0].Task.Status)
	require.Equal(t, time.Unix(100, 0), proposed[0].Task.NextRunAt)

	_, err := store.GetTask(ctx, 1)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

func TestControllerSnapshotUsesHashSlotTableAsPhysicalSlotSet(t *testing.T) {
	store := openControllerStore(t)
	ctx := context.Background()
	table := hashslot.NewHashSlotTable(8, 3)
	require.NoError(t, store.SaveHashSlotTable(ctx, table))

	controller := NewController(store, ControllerConfig{
		Planner: PlannerConfig{SlotCount: 2, ReplicaN: 3},
	})

	state, err := controller.snapshot(ctx)
	require.NoError(t, err)
	require.Contains(t, state.PhysicalSlots, uint32(1))
	require.Contains(t, state.PhysicalSlots, uint32(2))
	require.Contains(t, state.PhysicalSlots, uint32(3))
	require.Len(t, state.PhysicalSlots, 3)
}

func TestStateMachineTransitionsNodeStatusFromSuspectToDeadToAlive(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{
		SuspectTimeout: time.Second,
		DeadTimeout:    2 * time.Second,
	})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeHeartbeat,
		Report: &AgentReport{
			NodeID:     1,
			Addr:       "127.0.0.1:7000",
			ObservedAt: time.Unix(0, 0),
		},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind:    CommandKindEvaluateTimeouts,
		Advance: &TaskAdvance{Now: time.Unix(2, 0)},
	}))

	node, err := store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusSuspect, node.Status)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind:    CommandKindEvaluateTimeouts,
		Advance: &TaskAdvance{Now: time.Unix(3, 0)},
	}))
	node, err = store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusDead, node.Status)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeHeartbeat,
		Report: &AgentReport{
			NodeID:     1,
			Addr:       "127.0.0.1:7000",
			ObservedAt: time.Unix(4, 0),
		},
	}))
	node, err = store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusAlive, node.Status)
}

func TestStateMachineDefaultConfigAppliesHeartbeatTimeouts(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeHeartbeat,
		Report: &AgentReport{
			NodeID:     1,
			Addr:       "127.0.0.1:7000",
			ObservedAt: time.Unix(0, 0),
		},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind:    CommandKindEvaluateTimeouts,
		Advance: &TaskAdvance{Now: time.Unix(2, 0)},
	}))

	node, err := store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusAlive, node.Status)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind:    CommandKindEvaluateTimeouts,
		Advance: &TaskAdvance{Now: time.Unix(3, 0)},
	}))
	node, err = store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusSuspect, node.Status)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind:    CommandKindEvaluateTimeouts,
		Advance: &TaskAdvance{Now: time.Unix(10, 0)},
	}))
	node, err = store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusSuspect, node.Status)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind:    CommandKindEvaluateTimeouts,
		Advance: &TaskAdvance{Now: time.Unix(11, 0)},
	}))
	node, err = store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, NodeStatusDead, node.Status)
}

func TestStateMachineApplyNodeStatusUpdateAppliesExpectedTransition(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, store.UpsertNode(ctx, controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1, 0),
		CapacityWeight:  1,
	}))
	require.NoError(t, store.UpsertNode(ctx, controllermeta.ClusterNode{
		NodeID:          2,
		Addr:            "127.0.0.1:7002",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(2, 0),
		CapacityWeight:  1,
	}))

	alive := statusPtr(controllermeta.NodeStatusAlive)
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeStatusUpdate,
		NodeStatusUpdate: &NodeStatusUpdate{
			Transitions: []NodeStatusTransition{{
				NodeID:         1,
				NewStatus:      controllermeta.NodeStatusSuspect,
				ExpectedStatus: alive,
				EvaluatedAt:    time.Unix(10, 0),
			}},
		},
	}))

	node, err := store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, controllermeta.NodeStatusSuspect, node.Status)
	require.Equal(t, time.Unix(10, 0), node.LastHeartbeatAt)

	untouched, err := store.GetNode(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, controllermeta.NodeStatusAlive, untouched.Status)
}

func TestStateMachineApplyNodeStatusUpdateIgnoresMismatchedPriorStatus(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, store.UpsertNode(ctx, controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusDead,
		LastHeartbeatAt: time.Unix(1, 0),
		CapacityWeight:  1,
	}))

	alive := statusPtr(controllermeta.NodeStatusAlive)
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeStatusUpdate,
		NodeStatusUpdate: &NodeStatusUpdate{
			Transitions: []NodeStatusTransition{{
				NodeID:         1,
				NewStatus:      controllermeta.NodeStatusSuspect,
				ExpectedStatus: alive,
				EvaluatedAt:    time.Unix(10, 0),
			}},
		},
	}))

	node, err := store.GetNode(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, controllermeta.NodeStatusDead, node.Status)
	require.Equal(t, time.Unix(1, 0), node.LastHeartbeatAt)
}

func TestStateMachineRequiresResumeToLeaveDraining(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorMarkNodeDraining, NodeID: 2},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeHeartbeat,
		Report: &AgentReport{
			NodeID:     2,
			Addr:       "127.0.0.1:7001",
			ObservedAt: time.Unix(10, 0),
		},
	}))

	node, err := store.GetNode(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, NodeStatusDraining, node.Status)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorResumeNode, NodeID: 2},
	}))
	node, err = store.GetNode(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, NodeStatusAlive, node.Status)
}

func TestStateMachineApplyNodeJoin(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()
	joinedAt := time.Unix(100, 0)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeJoin,
		NodeJoin: &NodeJoinRequest{
			NodeID:         7,
			Name:           "worker-7",
			Addr:           "127.0.0.1:7007",
			CapacityWeight: 5,
			JoinedAt:       joinedAt,
		},
	}))

	node, err := store.GetNode(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, uint64(7), node.NodeID)
	require.Equal(t, "worker-7", node.Name)
	require.Equal(t, "127.0.0.1:7007", node.Addr)
	require.Equal(t, controllermeta.NodeRoleData, node.Role)
	require.Equal(t, controllermeta.NodeJoinStateJoining, node.JoinState)
	require.Equal(t, controllermeta.NodeStatusAlive, node.Status)
	require.Equal(t, joinedAt, node.JoinedAt)
	require.Equal(t, joinedAt, node.LastHeartbeatAt)
	require.Equal(t, 5, node.CapacityWeight)

	node.Status = controllermeta.NodeStatusDraining
	require.NoError(t, store.UpsertNode(ctx, node))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeJoin,
		NodeJoin: &NodeJoinRequest{
			NodeID:         7,
			Name:           "worker-7-renamed",
			Addr:           "127.0.0.1:7007",
			CapacityWeight: 8,
			JoinedAt:       time.Unix(200, 0),
		},
	}))

	node, err = store.GetNode(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, "worker-7-renamed", node.Name)
	require.Equal(t, 8, node.CapacityWeight)
	require.Equal(t, controllermeta.NodeStatusDraining, node.Status)
	require.Equal(t, joinedAt, node.JoinedAt)
	require.Equal(t, joinedAt, node.LastHeartbeatAt)

	err = sm.Apply(ctx, Command{
		Kind: CommandKindNodeJoin,
		NodeJoin: &NodeJoinRequest{
			NodeID:   7,
			Addr:     "127.0.0.1:7008",
			JoinedAt: time.Unix(300, 0),
		},
	})
	require.NoError(t, err)
	node, err = store.GetNode(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:7007", node.Addr)
	require.Equal(t, "worker-7-renamed", node.Name)
	require.Equal(t, 8, node.CapacityWeight)

	err = sm.Apply(ctx, Command{
		Kind: CommandKindNodeJoin,
		NodeJoin: &NodeJoinRequest{
			NodeID:   8,
			Addr:     "127.0.0.1:7007",
			JoinedAt: time.Unix(300, 0),
		},
	})
	require.NoError(t, err)
	_, err = store.GetNode(ctx, 8)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

func TestStateMachineNodeHeartbeatKeepsJoiningUntilFullSync(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeJoin,
		NodeJoin: &NodeJoinRequest{
			NodeID:         9,
			Name:           "worker-9",
			Addr:           "127.0.0.1:7009",
			CapacityWeight: 2,
			JoinedAt:       time.Unix(10, 0),
		},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeHeartbeat,
		Report: &AgentReport{
			NodeID:         9,
			Addr:           "127.0.0.1:7009",
			ObservedAt:     time.Unix(20, 0),
			CapacityWeight: 3,
		},
	}))

	node, err := store.GetNode(ctx, 9)
	require.NoError(t, err)
	require.Equal(t, controllermeta.NodeStatusAlive, node.Status)
	require.Equal(t, controllermeta.NodeJoinStateJoining, node.JoinState)
	require.Equal(t, time.Unix(20, 0), node.LastHeartbeatAt)
	require.Equal(t, 3, node.CapacityWeight)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeHeartbeat,
		Report: &AgentReport{
			NodeID:     10,
			Addr:       "127.0.0.1:7010",
			ObservedAt: time.Unix(30, 0),
		},
	}))
	legacyNode, err := store.GetNode(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, controllermeta.NodeRoleData, legacyNode.Role)
	require.Equal(t, controllermeta.NodeJoinStateActive, legacyNode.JoinState)
	require.Equal(t, controllermeta.NodeStatusAlive, legacyNode.Status)
}

func TestStateMachineNodeJoinActivate(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeJoin,
		NodeJoin: &NodeJoinRequest{
			NodeID:   11,
			Name:     "worker-11",
			Addr:     "127.0.0.1:7011",
			JoinedAt: time.Unix(10, 0),
		},
	}))
	node, err := store.GetNode(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, controllermeta.NodeJoinStateJoining, node.JoinState)

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindNodeJoinActivate,
		NodeJoinActivate: &NodeJoinActivateRequest{
			NodeID:      11,
			ActivatedAt: time.Unix(20, 0),
		},
	}))
	node, err = store.GetNode(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, controllermeta.NodeJoinStateActive, node.JoinState)
	require.Equal(t, controllermeta.NodeStatusAlive, node.Status)

	err = sm.Apply(ctx, Command{
		Kind: CommandKindNodeJoinActivate,
		NodeJoinActivate: &NodeJoinActivateRequest{
			NodeID:      12,
			ActivatedAt: time.Unix(20, 0),
		},
	})
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

func statusPtr(status controllermeta.NodeStatus) *controllermeta.NodeStatus {
	return &status
}

func TestStateMachineResumeNodeClearsRepairTasksForThatNode(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorMarkNodeDraining, NodeID: 2},
	}))
	require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: 2,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusPending,
		NextRunAt:  time.Unix(10, 0),
	}))
	require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
		SlotID:     2,
		Kind:       controllermeta.TaskKindRepair,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: 3,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusPending,
		NextRunAt:  time.Unix(11, 0),
	}))
	require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
		SlotID:     3,
		Kind:       controllermeta.TaskKindRebalance,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: 2,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusPending,
		NextRunAt:  time.Unix(12, 0),
	}))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorResumeNode, NodeID: 2},
	}))

	node, err := store.GetNode(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, NodeStatusAlive, node.Status)

	_, err = store.GetTask(ctx, 1)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)

	task, err := store.GetTask(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint32(2), task.SlotID)

	task, err = store.GetTask(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, controllermeta.TaskKindRebalance, task.Kind)
}

func TestStateMachineIgnoresStaleRepairProposalAfterResume(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorMarkNodeDraining, NodeID: 2},
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorResumeNode, NodeID: 2},
	}))

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 3, 4},
		ConfigEpoch:  2,
	}
	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: 2,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusPending,
		NextRunAt:  time.Unix(20, 0),
	}
	require.NoError(t, sm.Apply(ctx, Command{
		Kind:       CommandKindAssignmentTaskUpdate,
		Assignment: &assignment,
		Task:       &task,
	}))

	_, err := store.GetAssignment(ctx, 1)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)

	_, err = store.GetTask(ctx, 1)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

func TestStateMachineResumeNodeRestoresAssignmentFromPendingRepair(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{})
	ctx := context.Background()

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorMarkNodeDraining, NodeID: 4},
	}))
	require.NoError(t, store.UpsertAssignment(ctx, controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 2, 3},
		ConfigEpoch:  2,
	}))
	require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: 4,
		TargetNode: 3,
		Status:     controllermeta.TaskStatusPending,
		NextRunAt:  time.Unix(21, 0),
	}))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindOperatorRequest,
		Op:   &OperatorRequest{Kind: OperatorResumeNode, NodeID: 4},
	}))

	assignment, err := store.GetAssignment(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 4}, assignment.DesiredPeers)
	require.EqualValues(t, 3, assignment.ConfigEpoch)

	_, err = store.GetTask(ctx, 1)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

func TestStateMachineLeaderTransferSuccessDeletesTaskAndSetsCooldown(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{LeaderTransferCooldown: 30 * time.Second})
	ctx := context.Background()

	require.NoError(t, store.UpsertAssignment(ctx, controllermeta.SlotAssignment{
		SlotID:          1,
		DesiredPeers:    []uint64{1, 2, 3},
		PreferredLeader: 2,
	}))
	require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindLeaderTransfer,
		Step:       controllermeta.TaskStepTransferLeader,
		SourceNode: 1,
		TargetNode: 2,
		Status:     controllermeta.TaskStatusPending,
	}))

	now := time.Unix(100, 0)
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindTaskResult,
		Advance: &TaskAdvance{
			SlotID: 1,
			Now:    now,
		},
	}))

	_, err := store.GetTask(ctx, 1)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
	assignment, err := store.GetAssignment(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, now.Add(30*time.Second).UnixNano(), assignment.LeaderTransferCooldownUntil.UnixNano())
}

func TestStateMachineLeaderTransferFailureExhaustionDeletesTaskAndSetsFailureCooldown(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{
		MaxTaskAttempts:               3,
		RetryBackoffBase:              time.Second,
		LeaderTransferFailureCooldown: time.Minute,
	})
	ctx := context.Background()

	require.NoError(t, store.UpsertAssignment(ctx, controllermeta.SlotAssignment{
		SlotID:          1,
		DesiredPeers:    []uint64{1, 2, 3},
		PreferredLeader: 2,
	}))
	require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindLeaderTransfer,
		Step:       controllermeta.TaskStepTransferLeader,
		TargetNode: 2,
		Attempt:    2,
		Status:     controllermeta.TaskStatusRetrying,
		NextRunAt:  time.Unix(99, 0),
		LastError:  "previous",
	}))

	now := time.Unix(100, 0)
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindTaskResult,
		Advance: &TaskAdvance{
			SlotID:  1,
			Attempt: 2,
			Now:     now,
			Err:     errors.New("current voters unknown"),
		},
	}))

	_, err := store.GetTask(ctx, 1)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
	assignment, err := store.GetAssignment(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Minute).UnixNano(), assignment.LeaderTransferCooldownUntil.UnixNano())
}

func TestStateMachineLeaderTransferMissingAssignmentDeletesTask(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{
		MaxTaskAttempts:  3,
		RetryBackoffBase: time.Second,
	})
	ctx := context.Background()

	require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindLeaderTransfer,
		Step:      controllermeta.TaskStepTransferLeader,
		Attempt:   2,
		Status:    controllermeta.TaskStatusRetrying,
		NextRunAt: time.Unix(99, 0),
	}))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindTaskResult,
		Advance: &TaskAdvance{
			SlotID:  1,
			Attempt: 2,
			Now:     time.Unix(100, 0),
			Err:     errors.New("transfer rejected"),
		},
	}))

	_, err := store.GetTask(ctx, 1)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

func TestStateMachineMarksTaskFailedAfterRetryExhaustion(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{
		MaxTaskAttempts:  3,
		RetryBackoffBase: time.Second,
	})
	ctx := context.Background()

	require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Attempt:   2,
		NextRunAt: time.Unix(10, 0),
		LastError: "previous failure",
		Status:    controllermeta.TaskStatusRetrying,
	}))
	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindTaskResult,
		Advance: &TaskAdvance{
			SlotID:  1,
			Attempt: 2,
			Now:     time.Unix(11, 0),
			Err:     errors.New("learner catch-up timeout"),
		},
	}))

	task, err := store.GetTask(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, TaskStatusFailed, task.Status)
	require.EqualValues(t, 3, task.Attempt)
	require.Contains(t, task.LastError, "learner catch-up timeout")
}

func TestStateMachineIgnoresDuplicateTaskResultForAdvancedAttempt(t *testing.T) {
	store := openControllerStore(t)
	sm := NewStateMachine(store, StateMachineConfig{
		MaxTaskAttempts:  3,
		RetryBackoffBase: time.Second,
	})
	ctx := context.Background()

	original := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Attempt:   2,
		NextRunAt: time.Unix(10, 0),
		LastError: "previous failure",
		Status:    controllermeta.TaskStatusRetrying,
	}
	require.NoError(t, store.UpsertTask(ctx, original))

	require.NoError(t, sm.Apply(ctx, Command{
		Kind: CommandKindTaskResult,
		Advance: &TaskAdvance{
			SlotID:  1,
			Attempt: 1,
			Now:     time.Unix(11, 0),
			Err:     errors.New("stale duplicate failure"),
		},
	}))

	task, err := store.GetTask(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, original.Attempt, task.Attempt)
	require.Equal(t, original.Status, task.Status)
	require.Equal(t, original.NextRunAt, task.NextRunAt)
	require.Equal(t, original.LastError, task.LastError)
}

type stateOption func(*PlannerState)

func testState(options ...stateOption) PlannerState {
	state := PlannerState{
		Now:         time.Unix(100, 0),
		Nodes:       make(map[uint64]controllermeta.ClusterNode),
		Assignments: make(map[uint32]controllermeta.SlotAssignment),
		Runtime:     make(map[uint32]controllermeta.SlotRuntimeView),
		Tasks:       make(map[uint32]controllermeta.ReconcileTask),
	}
	for _, option := range options {
		option(&state)
	}
	return state
}

func aliveNode(nodeID uint64) stateOption {
	return withNode(nodeID, controllermeta.NodeStatusAlive)
}

func suspectNode(nodeID uint64) stateOption {
	return withNode(nodeID, controllermeta.NodeStatusSuspect)
}

func drainingNode(nodeID uint64) stateOption {
	return withNode(nodeID, controllermeta.NodeStatusDraining)
}

func deadNode(nodeID uint64) stateOption {
	return withNode(nodeID, controllermeta.NodeStatusDead)
}

func withNode(nodeID uint64, status controllermeta.NodeStatus) stateOption {
	return withMembershipNode(nodeID, status, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive)
}

func withMembershipNode(nodeID uint64, status controllermeta.NodeStatus, role controllermeta.NodeRole, joinState controllermeta.NodeJoinState) stateOption {
	return func(state *PlannerState) {
		state.Nodes[nodeID] = controllermeta.ClusterNode{
			NodeID:          nodeID,
			Addr:            "127.0.0.1:7000",
			Role:            role,
			JoinState:       joinState,
			Status:          status,
			LastHeartbeatAt: state.Now,
			CapacityWeight:  1,
		}
	}
}

func withAssignment(slotID uint32, peers ...uint64) stateOption {
	return func(state *PlannerState) {
		state.Assignments[slotID] = controllermeta.SlotAssignment{
			SlotID:       slotID,
			DesiredPeers: append([]uint64(nil), peers...),
			ConfigEpoch:  1,
		}
	}
}

func withPreferredLeader(slotID uint32, preferredLeader uint64) stateOption {
	return func(state *PlannerState) {
		assignment := state.Assignments[slotID]
		assignment.SlotID = slotID
		assignment.PreferredLeader = preferredLeader
		state.Assignments[slotID] = assignment
	}
}

func withLeaderTransferCooldown(slotID uint32, until time.Time) stateOption {
	return func(state *PlannerState) {
		assignment := state.Assignments[slotID]
		assignment.SlotID = slotID
		assignment.LeaderTransferCooldownUntil = until
		state.Assignments[slotID] = assignment
	}
}

func withAssignmentConfigEpoch(slotID uint32, epoch uint64) stateOption {
	return func(state *PlannerState) {
		assignment := state.Assignments[slotID]
		assignment.SlotID = slotID
		assignment.ConfigEpoch = epoch
		state.Assignments[slotID] = assignment
	}
}

func withRuntimeView(slotID uint32, peers []uint64, hasQuorum bool) stateOption {
	return func(state *PlannerState) {
		state.Runtime[slotID] = controllermeta.SlotRuntimeView{
			SlotID:              slotID,
			CurrentPeers:        append([]uint64(nil), peers...),
			LeaderID:            peers[0],
			HealthyVoters:       uint32(len(peers)),
			HasQuorum:           hasQuorum,
			ObservedConfigEpoch: 1,
			LastReportAt:        state.Now,
		}
	}
}

func withRuntimeViewVoters(slotID uint32, voters []uint64, leaderID uint64, hasQuorum bool) stateOption {
	return func(state *PlannerState) {
		state.Runtime[slotID] = controllermeta.SlotRuntimeView{
			SlotID:              slotID,
			CurrentPeers:        append([]uint64(nil), voters...),
			CurrentVoters:       append([]uint64(nil), voters...),
			LeaderID:            leaderID,
			HealthyVoters:       uint32(len(voters)),
			HasQuorum:           hasQuorum,
			ObservedConfigEpoch: 1,
			LastReportAt:        state.Now,
		}
	}
}

func withTask(task controllermeta.ReconcileTask) stateOption {
	return func(state *PlannerState) {
		state.Tasks[task.SlotID] = task
	}
}

func withMigratingSlot(slotID uint32) stateOption {
	return func(state *PlannerState) {
		if state.MigratingSlots == nil {
			state.MigratingSlots = make(map[uint32]struct{})
		}
		state.MigratingSlots[slotID] = struct{}{}
	}
}

func withLockedSlot(slotID uint32) stateOption {
	return func(state *PlannerState) {
		if state.LockedSlots == nil {
			state.LockedSlots = make(map[uint32]struct{})
		}
		state.LockedSlots[slotID] = struct{}{}
	}
}

func withPhysicalSlots(slotIDs ...uint32) stateOption {
	return func(state *PlannerState) {
		if state.PhysicalSlots == nil {
			state.PhysicalSlots = make(map[uint32]struct{})
		}
		for _, slotID := range slotIDs {
			state.PhysicalSlots[slotID] = struct{}{}
		}
	}
}

func maxTestLoad(loads map[uint64]int) int {
	max := 0
	for _, load := range loads {
		if load > max {
			max = load
		}
	}
	return max
}

func minTestLoad(loads map[uint64]int, nodeIDs []uint64) int {
	if len(nodeIDs) == 0 {
		return 0
	}
	min := loads[nodeIDs[0]]
	for _, nodeID := range nodeIDs[1:] {
		if loads[nodeID] < min {
			min = loads[nodeID]
		}
	}
	return min
}

func openControllerStore(tb testing.TB) *controllermeta.Store {
	tb.Helper()

	store, err := controllermeta.Open(filepath.Join(tb.TempDir(), "controller-meta"))
	require.NoError(tb, err)

	tb.Cleanup(func() {
		require.NoError(tb, store.Close())
	})
	return store
}
