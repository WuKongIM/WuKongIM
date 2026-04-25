package plane

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

func TestControllerTickNoOpsWhenNotLeader(t *testing.T) {
	store := openControllerStore(t)
	require.NoError(t, store.Close())

	controller := NewController(store, ControllerConfig{
		Planner:  PlannerConfig{SlotCount: 1, ReplicaN: 3},
		IsLeader: func() bool { return false },
	})

	require.NoError(t, controller.Tick(context.Background()))
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

func deadNode(nodeID uint64) stateOption {
	return withNode(nodeID, controllermeta.NodeStatusDead)
}

func withNode(nodeID uint64, status controllermeta.NodeStatus) stateOption {
	return func(state *PlannerState) {
		state.Nodes[nodeID] = controllermeta.ClusterNode{
			NodeID:          nodeID,
			Addr:            "127.0.0.1:7000",
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

func openControllerStore(tb testing.TB) *controllermeta.Store {
	tb.Helper()

	store, err := controllermeta.Open(filepath.Join(tb.TempDir(), "controller-meta"))
	require.NoError(tb, err)

	tb.Cleanup(func() {
		require.NoError(tb, store.Close())
	})
	return store
}
