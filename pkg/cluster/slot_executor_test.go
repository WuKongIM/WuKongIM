package cluster

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestSlotExecutorExecuteUsesClusterScopedExecutionHook(t *testing.T) {
	clusterA := newObserverTestCluster(t, ObserverHooks{})
	clusterB := newObserverTestCluster(t, ObserverHooks{})
	sentinel := errors.New("slot executor hook failed")

	restore := clusterA.SetManagedSlotExecutionTestHook(func(slotID uint32, task controllermeta.ReconcileTask) error {
		if slotID != 1 {
			t.Fatalf("hook slotID = %d, want 1", slotID)
		}
		if task.SlotID != 1 {
			t.Fatalf("hook task slotID = %d, want 1", task.SlotID)
		}
		return sentinel
	})
	defer restore()

	state := assignmentTaskState{
		assignment: controllermeta.SlotAssignment{SlotID: 1},
		task:       controllermeta.ReconcileTask{SlotID: 1},
	}

	if err := newSlotExecutor(clusterA).Execute(context.Background(), state); !errors.Is(err, sentinel) {
		t.Fatalf("slotExecutor.Execute() error = %v, want %v", err, sentinel)
	}
	if err := newSlotExecutor(clusterB).Execute(context.Background(), state); err != nil {
		t.Fatalf("slotExecutor.Execute() error = %v, want nil", err)
	}
}

func TestSlotExecutorExecuteRepairAndRebalanceStepOrder(t *testing.T) {
	testCases := []struct {
		name string
		kind controllermeta.TaskKind
	}{
		{name: "repair", kind: controllermeta.TaskKindRepair},
		{name: "rebalance", kind: controllermeta.TaskKindRebalance},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &Cluster{}
			var calls []string
			executor := newSlotExecutorWithFuncs(cluster, slotExecutorFuncs{
				prepareSlot: func(slotID multiraft.SlotID, desiredPeers []uint64) {
					if slotID != 1 {
						t.Fatalf("prepareSlot() slotID = %d, want 1", slotID)
					}
					calls = append(calls, "PrepareSlot")
				},
				changeSlotConfig: func(_ context.Context, slotID multiraft.SlotID, change multiraft.ConfigChange) error {
					if slotID != 1 {
						t.Fatalf("changeSlotConfig() slotID = %d, want 1", slotID)
					}
					calls = append(calls, configChangeStepName(change.Type))
					return nil
				},
				waitForCatchUp: func(_ context.Context, slotID multiraft.SlotID, targetNode multiraft.NodeID) error {
					if slotID != 1 {
						t.Fatalf("waitForCatchUp() slotID = %d, want 1", slotID)
					}
					if targetNode != 2 {
						t.Fatalf("waitForCatchUp() targetNode = %d, want 2", targetNode)
					}
					calls = append(calls, "CatchUp")
					return nil
				},
				ensureLeaderMovedOffSource: func(_ context.Context, slotID multiraft.SlotID, sourceNode, targetNode multiraft.NodeID) error {
					if slotID != 1 {
						t.Fatalf("ensureLeaderMovedOffSource() slotID = %d, want 1", slotID)
					}
					if sourceNode != 3 || targetNode != 2 {
						t.Fatalf("ensureLeaderMovedOffSource() source=%d target=%d, want source=3 target=2", sourceNode, targetNode)
					}
					calls = append(calls, "MoveLeaderOffSource")
					return nil
				},
			})

			err := executor.Execute(context.Background(), assignmentTaskState{
				assignment: controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2}},
				task: controllermeta.ReconcileTask{
					SlotID:     1,
					Kind:       tc.kind,
					TargetNode: 2,
					SourceNode: 3,
				},
			})
			if err != nil {
				t.Fatalf("slotExecutor.Execute() error = %v, want nil", err)
			}
			want := []string{
				"PrepareSlot",
				"AddLearner",
				"CatchUp",
				"PromoteLearner",
				"CatchUp",
				"MoveLeaderOffSource",
				"RemoveVoter",
			}
			if !slices.Equal(calls, want) {
				t.Fatalf("slotExecutor.Execute() calls = %v, want %v", calls, want)
			}
		})
	}
}

func TestSlotExecutorExecuteBootstrapTransfersLeaderToTarget(t *testing.T) {
	cluster := &Cluster{}
	var calls []string
	executor := newSlotExecutorWithFuncs(cluster, slotExecutorFuncs{
		prepareSlot: func(slotID multiraft.SlotID, desiredPeers []uint64) {
			if slotID != 1 {
				t.Fatalf("prepareSlot() slotID = %d, want 1", slotID)
			}
			calls = append(calls, "PrepareSlot")
		},
		waitForLeader: func(_ context.Context, slotID multiraft.SlotID) error {
			if slotID != 1 {
				t.Fatalf("waitForLeader() slotID = %d, want 1", slotID)
			}
			calls = append(calls, "WaitForLeader")
			return nil
		},
		ensureBootstrapLeader: func(_ context.Context, slotID multiraft.SlotID, targetNode multiraft.NodeID) error {
			if slotID != 1 {
				t.Fatalf("ensureBootstrapLeader() slotID = %d, want 1", slotID)
			}
			if targetNode != 3 {
				t.Fatalf("ensureBootstrapLeader() targetNode = %d, want 3", targetNode)
			}
			calls = append(calls, "EnsureBootstrapLeader")
			return nil
		},
	})

	err := executor.Execute(context.Background(), assignmentTaskState{
		assignment: controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}},
		task: controllermeta.ReconcileTask{
			SlotID:     1,
			Kind:       controllermeta.TaskKindBootstrap,
			TargetNode: 3,
		},
	})
	if err != nil {
		t.Fatalf("slotExecutor.Execute() error = %v, want nil", err)
	}
	want := []string{"PrepareSlot", "WaitForLeader", "EnsureBootstrapLeader"}
	if !slices.Equal(calls, want) {
		t.Fatalf("slotExecutor.Execute() calls = %v, want %v", calls, want)
	}
}

func TestSlotExecutorExecuteLeaderTransferOnlyTransfersLeadership(t *testing.T) {
	cluster := &Cluster{}
	var transferred bool
	executor := newSlotExecutorWithFuncs(cluster, slotExecutorFuncs{
		prepareSlot: func(multiraft.SlotID, []uint64) {
			t.Fatal("prepareSlot should not be called for leader transfer")
		},
		changeSlotConfig: func(context.Context, multiraft.SlotID, multiraft.ConfigChange) error {
			t.Fatal("changeSlotConfig should not be called for leader transfer")
			return nil
		},
		transferLeadershipFrom: func(_ context.Context, slotID multiraft.SlotID, expectedLeader, target multiraft.NodeID) error {
			if slotID != 1 || expectedLeader != 1 || target != 2 {
				t.Fatalf("transferLeadershipFrom() slot=%d expected=%d target=%d, want slot=1 expected=1 target=2", slotID, expectedLeader, target)
			}
			transferred = true
			return nil
		},
		waitForSpecificLeader: func(_ context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
			if slotID != 1 || target != 2 {
				t.Fatalf("waitForSpecificLeader() slot=%d target=%d, want slot=1 target=2", slotID, target)
			}
			return nil
		},
	})

	err := executor.Execute(context.Background(), assignmentTaskState{
		assignment:     controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}},
		runtimeView:    controllermeta.SlotRuntimeView{SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
		hasRuntimeView: true,
		nodes: map[uint64]controllermeta.ClusterNode{
			1: leaderTransferTestNode(1),
			2: leaderTransferTestNode(2),
		},
		task: controllermeta.ReconcileTask{
			SlotID:     1,
			Kind:       controllermeta.TaskKindLeaderTransfer,
			Step:       controllermeta.TaskStepTransferLeader,
			TargetNode: 2,
		},
	})
	if err != nil {
		t.Fatalf("slotExecutor.Execute() error = %v, want nil", err)
	}
	if !transferred {
		t.Fatal("transferLeadership was not called")
	}
}

func TestSlotManagerTransferLeadershipFromRejectsChangedObservedLeader(t *testing.T) {
	cluster := newObserverTestCluster(t, ObserverHooks{})
	cluster.cfg.NodeID = 1
	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			t.Fatalf("leader hook slotID = %d, want 1", slotID)
		}
		return 3, nil, true
	})
	defer restoreLeader()

	err := cluster.managedSlots().transferLeadershipFrom(context.Background(), 1, 1, 2)

	if !errors.Is(err, ErrLeaderTransferSafetyCheck) {
		t.Fatalf("transferLeadershipFrom() error = %v, want %v", err, ErrLeaderTransferSafetyCheck)
	}
}

func TestSlotExecutorExecuteLeaderTransferAlreadyLeaderNoOp(t *testing.T) {
	cluster := &Cluster{}
	executor := newSlotExecutorWithFuncs(cluster, slotExecutorFuncs{
		prepareSlot: func(multiraft.SlotID, []uint64) {
			t.Fatal("prepareSlot should not be called for leader transfer")
		},
		transferLeadership: func(context.Context, multiraft.SlotID, multiraft.NodeID) error {
			t.Fatal("transferLeadership should not be called when target is already leader")
			return nil
		},
		waitForSpecificLeader: func(context.Context, multiraft.SlotID, multiraft.NodeID) error {
			t.Fatal("waitForSpecificLeader should not be called when target is already leader")
			return nil
		},
	})

	err := executor.Execute(context.Background(), leaderTransferTestState(2, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
		2: leaderTransferTestNode(2),
	}))
	if err != nil {
		t.Fatalf("slotExecutor.Execute() error = %v, want nil", err)
	}
}

func TestSlotExecutorExecuteLeaderTransferRejectsUnsafeRuntimeView(t *testing.T) {
	testCases := []struct {
		name  string
		state assignmentTaskState
	}{
		{
			name: "missing runtime view",
			state: func() assignmentTaskState {
				state := leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
					2: leaderTransferTestNode(2),
				})
				state.hasRuntimeView = false
				return state
			}(),
		},
		{
			name: "unknown leader",
			state: leaderTransferTestState(0, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
				2: leaderTransferTestNode(2),
			}),
		},
		{
			name: "empty current voters",
			state: leaderTransferTestState(1, []uint64{1, 2, 3}, nil, map[uint64]controllermeta.ClusterNode{
				2: leaderTransferTestNode(2),
			}),
		},
		{
			name: "leader not current voter",
			state: leaderTransferTestState(4, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
				2: leaderTransferTestNode(2),
			}),
		},
		{
			name: "runtime view slot mismatch",
			state: func() assignmentTaskState {
				state := leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
					2: leaderTransferTestNode(2),
				})
				state.runtimeView.SlotID = 2
				return state
			}(),
		},
		{
			name: "runtime view config epoch stale",
			state: func() assignmentTaskState {
				state := leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
					1: leaderTransferTestNode(1),
					2: leaderTransferTestNode(2),
				})
				state.assignment.ConfigEpoch = 2
				state.runtimeView.ObservedConfigEpoch = 1
				return state
			}(),
		},
		{
			name: "runtime view config epoch zero is stale when assignment epoch known",
			state: func() assignmentTaskState {
				state := leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
					1: leaderTransferTestNode(1),
					2: leaderTransferTestNode(2),
				})
				state.assignment.ConfigEpoch = 2
				state.runtimeView.ObservedConfigEpoch = 0
				return state
			}(),
		},
		{
			name: "observed leader dead",
			state: leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
				1: leaderTransferTestNodeWithStatus(1, controllermeta.NodeStatusDead),
				2: leaderTransferTestNode(2),
			}),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor := newSlotExecutorWithFuncs(&Cluster{}, leaderTransferRejectingFuncs(t))

			err := executor.Execute(context.Background(), tc.state)
			if !errors.Is(err, ErrLeaderTransferSafetyCheck) {
				t.Fatalf("slotExecutor.Execute() error = %v, want %v", err, ErrLeaderTransferSafetyCheck)
			}
		})
	}
}

func TestSlotExecutorExecuteLeaderTransferRejectsUnsafeTarget(t *testing.T) {
	testCases := []struct {
		name  string
		state assignmentTaskState
	}{
		{
			name: "target not in desired peers",
			state: leaderTransferTestState(1, []uint64{1, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
				2: leaderTransferTestNode(2),
			}),
		},
		{
			name: "target not in current voters",
			state: leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 3}, map[uint64]controllermeta.ClusterNode{
				2: leaderTransferTestNode(2),
			}),
		},
		{
			name:  "target missing",
			state: leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, nil),
		},
		{
			name: "target dead",
			state: leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
				2: leaderTransferTestNodeWithStatus(2, controllermeta.NodeStatusDead),
			}),
		},
		{
			name: "target suspect",
			state: leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
				2: leaderTransferTestNodeWithStatus(2, controllermeta.NodeStatusSuspect),
			}),
		},
		{
			name: "target draining",
			state: leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
				2: leaderTransferTestNodeWithStatus(2, controllermeta.NodeStatusDraining),
			}),
		},
		{
			name: "target joining",
			state: leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
				2: {
					NodeID:    2,
					Role:      controllermeta.NodeRoleData,
					JoinState: controllermeta.NodeJoinStateJoining,
					Status:    controllermeta.NodeStatusAlive,
				},
			}),
		},
		{
			name: "target controller only",
			state: leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
				2: {
					NodeID:    2,
					Role:      controllermeta.NodeRoleControllerVoter,
					JoinState: controllermeta.NodeJoinStateActive,
					Status:    controllermeta.NodeStatusAlive,
				},
			}),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			executor := newSlotExecutorWithFuncs(&Cluster{}, leaderTransferRejectingFuncs(t))

			err := executor.Execute(context.Background(), tc.state)
			if !errors.Is(err, ErrLeaderTransferSafetyCheck) {
				t.Fatalf("slotExecutor.Execute() error = %v, want %v", err, ErrLeaderTransferSafetyCheck)
			}
		})
	}
}

func TestSlotExecutorExecuteLeaderTransferRejectsNonLeaderExecutor(t *testing.T) {
	cluster := &Cluster{cfg: Config{NodeID: 2}}
	executor := newSlotExecutorWithFuncs(cluster, leaderTransferRejectingFuncs(t))
	state := leaderTransferTestState(1, []uint64{1, 2, 3}, []uint64{1, 2, 3}, map[uint64]controllermeta.ClusterNode{
		1: leaderTransferTestNode(1),
		3: leaderTransferTestNode(3),
	})
	state.task.TargetNode = 3

	err := executor.Execute(context.Background(), state)
	if !errors.Is(err, ErrLeaderTransferSafetyCheck) {
		t.Fatalf("slotExecutor.Execute() error = %v, want %v", err, ErrLeaderTransferSafetyCheck)
	}
}

func TestSlotExecutorExecuteStopsOnStepError(t *testing.T) {
	cluster := &Cluster{}
	sentinel := errors.New("promote failed")
	var calls []string
	executor := newSlotExecutorWithFuncs(cluster, slotExecutorFuncs{
		prepareSlot: func(_ multiraft.SlotID, _ []uint64) {
			calls = append(calls, "PrepareSlot")
		},
		changeSlotConfig: func(_ context.Context, _ multiraft.SlotID, change multiraft.ConfigChange) error {
			step := configChangeStepName(change.Type)
			calls = append(calls, step)
			if change.Type == multiraft.PromoteLearner {
				return sentinel
			}
			return nil
		},
		waitForCatchUp: func(_ context.Context, _ multiraft.SlotID, _ multiraft.NodeID) error {
			calls = append(calls, "CatchUp")
			return nil
		},
		ensureLeaderMovedOffSource: func(_ context.Context, _ multiraft.SlotID, _, _ multiraft.NodeID) error {
			calls = append(calls, "MoveLeaderOffSource")
			return nil
		},
	})

	err := executor.Execute(context.Background(), assignmentTaskState{
		assignment: controllermeta.SlotAssignment{SlotID: 1},
		task: controllermeta.ReconcileTask{
			SlotID:     1,
			Kind:       controllermeta.TaskKindRepair,
			TargetNode: 2,
			SourceNode: 3,
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("slotExecutor.Execute() error = %v, want %v", err, sentinel)
	}
	want := []string{
		"PrepareSlot",
		"AddLearner",
		"CatchUp",
		"PromoteLearner",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("slotExecutor.Execute() calls = %v, want %v", calls, want)
	}
}

func configChangeStepName(change multiraft.ChangeType) string {
	switch change {
	case multiraft.AddLearner:
		return "AddLearner"
	case multiraft.PromoteLearner:
		return "PromoteLearner"
	case multiraft.RemoveVoter:
		return "RemoveVoter"
	default:
		return fmt.Sprintf("UnknownChangeType(%d)", change)
	}
}

func leaderTransferTestState(leaderID uint64, desiredPeers, currentVoters []uint64, nodes map[uint64]controllermeta.ClusterNode) assignmentTaskState {
	return assignmentTaskState{
		assignment: controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: desiredPeers},
		runtimeView: controllermeta.SlotRuntimeView{
			SlotID:        1,
			LeaderID:      leaderID,
			CurrentVoters: currentVoters,
		},
		hasRuntimeView: true,
		nodes:          nodes,
		task: controllermeta.ReconcileTask{
			SlotID:     1,
			Kind:       controllermeta.TaskKindLeaderTransfer,
			Step:       controllermeta.TaskStepTransferLeader,
			TargetNode: 2,
		},
	}
}

func leaderTransferTestNode(nodeID uint64) controllermeta.ClusterNode {
	return leaderTransferTestNodeWithStatus(nodeID, controllermeta.NodeStatusAlive)
}

func leaderTransferTestNodeWithStatus(nodeID uint64, status controllermeta.NodeStatus) controllermeta.ClusterNode {
	return controllermeta.ClusterNode{
		NodeID:    nodeID,
		Role:      controllermeta.NodeRoleData,
		JoinState: controllermeta.NodeJoinStateActive,
		Status:    status,
	}
}

func leaderTransferRejectingFuncs(t *testing.T) slotExecutorFuncs {
	t.Helper()
	return slotExecutorFuncs{
		prepareSlot: func(multiraft.SlotID, []uint64) {
			t.Fatal("prepareSlot should not be called for rejected leader transfer")
		},
		transferLeadership: func(context.Context, multiraft.SlotID, multiraft.NodeID) error {
			t.Fatal("transferLeadership should not be called for rejected leader transfer")
			return nil
		},
		waitForSpecificLeader: func(context.Context, multiraft.SlotID, multiraft.NodeID) error {
			t.Fatal("waitForSpecificLeader should not be called for rejected leader transfer")
			return nil
		},
	}
}
