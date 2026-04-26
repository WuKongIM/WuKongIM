package plane

import (
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestPlanNodeOnboardingRejectsInvalidTargets(t *testing.T) {
	tests := []struct {
		name string
		node *controllermeta.ClusterNode
		code string
	}{
		{name: "missing target", code: "target_not_active"},
		{name: "joining target", node: ptrOnboardingNode(onboardingNode(4, controllermeta.NodeStatusAlive, controllermeta.NodeRoleData, controllermeta.NodeJoinStateJoining)), code: "target_not_active"},
		{name: "dead target", node: ptrOnboardingNode(onboardingNode(4, controllermeta.NodeStatusDead, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive)), code: "target_not_alive"},
		{name: "draining target", node: ptrOnboardingNode(onboardingNode(4, controllermeta.NodeStatusDraining, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive)), code: "target_draining"},
		{name: "non data target", node: ptrOnboardingNode(onboardingNode(4, controllermeta.NodeStatusAlive, controllermeta.NodeRoleControllerVoter, controllermeta.NodeJoinStateActive)), code: "target_not_data"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := baseOnboardingInput(4)
			if tc.node == nil {
				delete(input.Nodes, 4)
			} else {
				input.Nodes[4] = *tc.node
			}

			plan := NewOnboardingPlanner(OnboardingPlannerConfig{ReplicaN: 3}).Plan(input)

			require.Empty(t, plan.Moves)
			requireBlockedReason(t, plan, tc.code, "node", 0, 4)
		})
	}
}

func TestPlanNodeOnboardingMovesTargetFromZeroSlotsTowardTargetMax(t *testing.T) {
	input := baseOnboardingInput(4)
	for slotID := uint32(1); slotID <= 3; slotID++ {
		input.Assignments[slotID] = onboardingAssignment(slotID, 1, 2, 3)
		input.Runtime[slotID] = onboardingRuntime(slotID, uint64(slotID), []uint64{1, 2, 3}, true)
	}

	plan := NewOnboardingPlanner(OnboardingPlannerConfig{ReplicaN: 3}).Plan(input)

	require.Equal(t, uint64(4), plan.TargetNodeID)
	require.Equal(t, 0, plan.Summary.CurrentTargetSlotCount)
	require.Equal(t, 3, plan.Summary.PlannedTargetSlotCount)
	require.Len(t, plan.Moves, 3)
	require.Empty(t, plan.BlockedReasons)

	seenSlots := map[uint32]struct{}{}
	for _, move := range plan.Moves {
		require.Equal(t, uint64(4), move.TargetNodeID)
		require.Equal(t, "replica_balance", move.Reason)
		require.NotContains(t, move.DesiredPeersBefore, uint64(4))
		require.Contains(t, move.DesiredPeersAfter, uint64(4))
		require.NotContains(t, move.DesiredPeersAfter, move.SourceNodeID)
		_, exists := seenSlots[move.SlotID]
		require.False(t, exists, "slot planned more than once")
		seenSlots[move.SlotID] = struct{}{}
	}
}

func TestPlanNodeOnboardingAlreadyBalancedReturnsBlockedReason(t *testing.T) {
	input := baseOnboardingInput(4)
	input.Assignments[1] = onboardingAssignment(1, 1, 2, 4)
	input.Assignments[2] = onboardingAssignment(2, 1, 3, 4)
	input.Runtime[1] = onboardingRuntime(1, 1, []uint64{1, 2, 4}, true)
	input.Runtime[2] = onboardingRuntime(2, 1, []uint64{1, 3, 4}, true)

	plan := NewOnboardingPlanner(OnboardingPlannerConfig{ReplicaN: 3}).Plan(input)

	require.Empty(t, plan.Moves)
	require.Equal(t, 2, plan.Summary.CurrentTargetSlotCount)
	require.Equal(t, 2, plan.Summary.PlannedTargetSlotCount)
	requireBlockedReason(t, plan, "already_balanced", "node", 0, 4)
}

func TestPlanNodeOnboardingRunningJobBlocksClusterPlanning(t *testing.T) {
	input := baseOnboardingInput(4)
	input.RunningJobExists = true
	input.Assignments[1] = onboardingAssignment(1, 1, 2, 3)
	input.Runtime[1] = onboardingRuntime(1, 1, []uint64{1, 2, 3}, true)

	plan := NewOnboardingPlanner(OnboardingPlannerConfig{ReplicaN: 3}).Plan(input)

	require.Empty(t, plan.Moves)
	requireBlockedReason(t, plan, "running_job_exists", "cluster", 0, 0)
}

func TestPlanNodeOnboardingSkipsUnsafeSlotsWithSpecificReasons(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*OnboardingPlanInput)
		code  string
	}{
		{
			name: "no runtime",
			setup: func(input *OnboardingPlanInput) {
				input.Assignments[1] = onboardingAssignment(1, 1, 2, 3)
			},
			code: "no_runtime_view",
		},
		{
			name: "quorum lost",
			setup: func(input *OnboardingPlanInput) {
				input.Assignments[1] = onboardingAssignment(1, 1, 2, 3)
				input.Runtime[1] = onboardingRuntime(1, 1, []uint64{1, 2}, false)
			},
			code: "slot_quorum_lost",
		},
		{
			name: "task running",
			setup: func(input *OnboardingPlanInput) {
				input.Assignments[1] = onboardingAssignment(1, 1, 2, 3)
				input.Runtime[1] = onboardingRuntime(1, 1, []uint64{1, 2, 3}, true)
				input.Tasks[1] = controllermeta.ReconcileTask{SlotID: 1, Status: controllermeta.TaskStatusPending}
			},
			code: "slot_task_running",
		},
		{
			name: "task failed",
			setup: func(input *OnboardingPlanInput) {
				input.Assignments[1] = onboardingAssignment(1, 1, 2, 3)
				input.Runtime[1] = onboardingRuntime(1, 1, []uint64{1, 2, 3}, true)
				input.Tasks[1] = controllermeta.ReconcileTask{SlotID: 1, Status: controllermeta.TaskStatusFailed}
			},
			code: "slot_task_failed",
		},
		{
			name: "migrating",
			setup: func(input *OnboardingPlanInput) {
				input.Assignments[1] = onboardingAssignment(1, 1, 2, 3)
				input.Runtime[1] = onboardingRuntime(1, 1, []uint64{1, 2, 3}, true)
				input.MigratingSlots[1] = struct{}{}
			},
			code: "slot_hash_migration_active",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := baseOnboardingInput(4)
			tc.setup(&input)

			plan := NewOnboardingPlanner(OnboardingPlannerConfig{ReplicaN: 3}).Plan(input)

			require.Empty(t, plan.Moves)
			requireBlockedReason(t, plan, tc.code, "slot", 1, 0)
		})
	}
}

func TestPlanNodeOnboardingSortsCandidatesBySourceLeaderPriority(t *testing.T) {
	input := onboardingInputWithNodes(10, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	input.Assignments[1] = onboardingAssignment(1, 1, 2, 3)
	input.Assignments[2] = onboardingAssignment(2, 2, 5, 6)
	input.Assignments[3] = onboardingAssignment(3, 2, 5, 7)
	input.Runtime[1] = onboardingRuntime(1, 1, []uint64{1, 2, 3}, true)

	plan := NewOnboardingPlanner(OnboardingPlannerConfig{ReplicaN: 3}).Plan(input)

	require.NotEmpty(t, plan.Moves)
	require.Equal(t, uint32(1), plan.Moves[0].SlotID)
	require.Equal(t, uint64(1), plan.Moves[0].SourceNodeID, "leader source should beat a busier non-leader source")
}

func TestPlanNodeOnboardingSortsCandidatesBySourceLoad(t *testing.T) {
	input := onboardingInputWithNodes(10, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	input.Assignments[1] = onboardingAssignment(1, 1, 3, 4)
	input.Assignments[2] = onboardingAssignment(2, 2, 3, 4)
	input.Assignments[3] = onboardingAssignment(3, 2, 5, 6)
	input.Assignments[4] = onboardingAssignment(4, 2, 5, 7)
	input.Assignments[5] = onboardingAssignment(5, 1, 8, 9)
	input.Runtime[1] = onboardingRuntime(1, 1, []uint64{1, 3, 4}, true)
	input.Runtime[2] = onboardingRuntime(2, 2, []uint64{2, 3, 4}, true)

	plan := NewOnboardingPlanner(OnboardingPlannerConfig{ReplicaN: 3}).Plan(input)

	require.NotEmpty(t, plan.Moves)
	require.Equal(t, uint32(2), plan.Moves[0].SlotID)
	require.Equal(t, uint64(2), plan.Moves[0].SourceNodeID)
}

func TestPlanNodeOnboardingSortsCandidatesByBalanceVersionThenSlotID(t *testing.T) {
	input := onboardingInputWithNodes(5, 1, 2, 3, 4)
	input.Assignments[1] = onboardingAssignmentWithBalance(1, 5, 1, 2, 3)
	input.Assignments[2] = onboardingAssignmentWithBalance(2, 1, 2, 3, 4)
	input.Assignments[3] = onboardingAssignmentWithBalance(3, 1, 3, 4, 1)
	input.Assignments[4] = onboardingAssignmentWithBalance(4, 0, 4, 1, 2)
	input.Runtime[1] = onboardingRuntime(1, 1, []uint64{1, 2, 3}, true)
	input.Runtime[2] = onboardingRuntime(2, 2, []uint64{2, 3, 4}, true)
	input.Runtime[3] = onboardingRuntime(3, 3, []uint64{3, 4, 1}, true)
	input.Runtime[4] = onboardingRuntime(4, 4, []uint64{4, 1, 2}, true)

	plan := NewOnboardingPlanner(OnboardingPlannerConfig{ReplicaN: 3}).Plan(input)

	require.Len(t, plan.Moves, 3)
	require.Equal(t, []uint32{4, 2, 3}, []uint32{plan.Moves[0].SlotID, plan.Moves[1].SlotID, plan.Moves[2].SlotID})
}

func TestPlanNodeOnboardingMarksOnlyNeededSourceLeaderMovesForLeaderTransfer(t *testing.T) {
	input := onboardingInputWithNodes(5, 1, 2, 3, 4)
	for slotID := uint32(1); slotID <= 4; slotID++ {
		input.Assignments[slotID] = onboardingAssignment(slotID, 1, 2, 3)
		input.Runtime[slotID] = onboardingRuntime(slotID, uint64(slotID%3+1), []uint64{1, 2, 3}, true)
	}

	plan := NewOnboardingPlanner(OnboardingPlannerConfig{ReplicaN: 3}).Plan(input)

	require.Len(t, plan.Moves, 3)
	require.Equal(t, 0, plan.Summary.CurrentTargetLeaderCount)
	require.Equal(t, 1, plan.Summary.PlannedLeaderGain)

	marked := 0
	for _, move := range plan.Moves {
		if move.LeaderTransferRequired {
			marked++
			require.Equal(t, move.CurrentLeaderID, move.SourceNodeID)
		}
	}
	require.Equal(t, 1, marked)
}

func baseOnboardingInput(target uint64) OnboardingPlanInput {
	return onboardingInputWithNodes(target, 1, 2, 3)
}

func onboardingInputWithNodes(target uint64, sourceNodes ...uint64) OnboardingPlanInput {
	input := OnboardingPlanInput{
		TargetNodeID:   target,
		Nodes:          make(map[uint64]controllermeta.ClusterNode),
		Assignments:    make(map[uint32]controllermeta.SlotAssignment),
		Runtime:        make(map[uint32]controllermeta.SlotRuntimeView),
		Tasks:          make(map[uint32]controllermeta.ReconcileTask),
		MigratingSlots: make(map[uint32]struct{}),
		Now:            time.Unix(100, 0).UTC(),
	}
	input.Nodes[target] = onboardingNode(target, controllermeta.NodeStatusAlive, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive)
	for _, nodeID := range sourceNodes {
		input.Nodes[nodeID] = onboardingNode(nodeID, controllermeta.NodeStatusAlive, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive)
	}
	return input
}

func onboardingNode(nodeID uint64, status controllermeta.NodeStatus, role controllermeta.NodeRole, joinState controllermeta.NodeJoinState) controllermeta.ClusterNode {
	return controllermeta.ClusterNode{
		NodeID:          nodeID,
		Addr:            "127.0.0.1:7000",
		Role:            role,
		JoinState:       joinState,
		Status:          status,
		LastHeartbeatAt: time.Unix(100, 0).UTC(),
		CapacityWeight:  1,
	}
}

func ptrOnboardingNode(node controllermeta.ClusterNode) *controllermeta.ClusterNode {
	return &node
}

func onboardingAssignment(slotID uint32, peers ...uint64) controllermeta.SlotAssignment {
	return onboardingAssignmentWithBalance(slotID, 0, peers...)
}

func onboardingAssignmentWithBalance(slotID uint32, balanceVersion uint64, peers ...uint64) controllermeta.SlotAssignment {
	return controllermeta.SlotAssignment{
		SlotID:         slotID,
		DesiredPeers:   append([]uint64(nil), peers...),
		ConfigEpoch:    1,
		BalanceVersion: balanceVersion,
	}
}

func onboardingRuntime(slotID uint32, leaderID uint64, peers []uint64, hasQuorum bool) controllermeta.SlotRuntimeView {
	return controllermeta.SlotRuntimeView{
		SlotID:              slotID,
		CurrentPeers:        append([]uint64(nil), peers...),
		LeaderID:            leaderID,
		HealthyVoters:       uint32(len(peers)),
		HasQuorum:           hasQuorum,
		ObservedConfigEpoch: 1,
		LastReportAt:        time.Unix(100, 0).UTC(),
	}
}

func requireBlockedReason(t *testing.T, plan controllermeta.NodeOnboardingPlan, code, scope string, slotID uint32, nodeID uint64) {
	t.Helper()
	for _, reason := range plan.BlockedReasons {
		if reason.Code == code && reason.Scope == scope && reason.SlotID == slotID && reason.NodeID == nodeID {
			return
		}
	}
	require.Failf(t, "blocked reason not found", "code=%s scope=%s slot=%d node=%d reasons=%+v", code, scope, slotID, nodeID, plan.BlockedReasons)
}
