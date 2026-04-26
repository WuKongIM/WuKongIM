package plane

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

func TestPlannerSkipsAutomaticRebalanceWhenPaused(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 3, RebalanceSkewThreshold: 2})
	state := rebalanceSkewState()
	state.PauseRebalance = true

	decision, err := planner.NextDecision(context.Background(), state)
	require.NoError(t, err)
	require.Zero(t, decision.SlotID)
	require.Nil(t, decision.Task)
}

func TestPlannerLockedSlotBlocksNewTaskButAllowsExistingTask(t *testing.T) {
	planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
	state := testState(
		aliveNode(1), aliveNode(2), deadNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
	)
	state.LockedSlots = map[uint32]struct{}{1: {}}

	decision, err := planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.Equal(t, uint32(1), decision.SlotID)
	require.Nil(t, decision.Task)

	state.Tasks[1] = controllermeta.ReconcileTask{
		SlotID: 1,
		Kind:   controllermeta.TaskKindRebalance,
		Step:   controllermeta.TaskStepAddLearner,
		Status: controllermeta.TaskStatusPending,
	}
	decision, err = planner.ReconcileSlot(context.Background(), state, 1)
	require.NoError(t, err)
	require.NotNil(t, decision.Task)
	require.Equal(t, controllermeta.TaskKindRebalance, decision.Task.Kind)
}

func rebalanceSkewState() PlannerState {
	return testState(
		aliveNode(1), aliveNode(2), aliveNode(3), aliveNode(4),
		withAssignment(1, 1, 2, 3),
		withAssignment(2, 1, 2, 3),
		withRuntimeView(1, []uint64{1, 2, 3}, true),
		withRuntimeView(2, []uint64{1, 2, 3}, true),
	)
}
