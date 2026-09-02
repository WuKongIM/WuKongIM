package state

import "testing"

func validSlotReplicaMoveState() ClusterState {
	value := testState()
	value.Nodes = append(value.Nodes, Node{
		NodeID: 4, Name: "n4", Addr: "n4", Roles: []NodeRole{NodeRoleData},
		JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 100,
	})
	value.Slots = []SlotAssignment{{
		SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 1,
	}}
	value.Tasks = []ReconcileTask{{
		TaskID: "slot-1-move-7", SlotID: 1, Kind: TaskKindSlotReplicaMove,
		Step: TaskStepOpenLearner, SourceNode: 3, TargetNode: 4,
		TargetPeers: []uint64{1, 2, 4}, CompletionPolicy: TaskCompletionPolicySingleObserver,
		ConfigEpoch: 7, Status: TaskStatusPending,
	}}
	return value
}

func TestSlotReplicaMoveAcceptsEveryFencedPhase(t *testing.T) {
	for _, step := range []TaskStep{
		TaskStepOpenLearner, TaskStepAddLearner, TaskStepPromoteLearner,
		TaskStepRemoveVoter, TaskStepCommitAssignment,
	} {
		value := validSlotReplicaMoveState()
		value.Tasks[0].Step = step
		value.Tasks[0].ObservedVoters = []uint64{1, 2, 3}
		value.Tasks[0].ObservedLearners = []uint64{4}
		if err := value.Validate(); err != nil {
			t.Fatalf("valid replica-move step %q: %v", step, err)
		}
	}
	if got := replacePeer([]uint64{3, 1, 2}, 3, 4); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 4 {
		t.Fatalf("replacePeer() = %v", got)
	}
	if got := replacePeer([]uint64{3, 1, 2}, 9, 4); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("replacePeer(missing source) = %v", got)
	}
	if hasDuplicateUint64([]uint64{1, 2, 3}) || !hasDuplicateUint64([]uint64{1, 0}) || !hasDuplicateUint64([]uint64{1, 2, 1}) {
		t.Fatal("hasDuplicateUint64() did not enforce non-zero unique observed members")
	}
}

func TestSlotReplicaMoveRejectsUnsafeMembershipTransitions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ClusterState)
	}{
		{name: "wrong step", mutate: func(s *ClusterState) { s.Tasks[0].Step = TaskStepCreateSlot }},
		{name: "missing assignment", mutate: func(s *ClusterState) { s.Slots = nil }},
		{name: "missing source", mutate: func(s *ClusterState) { s.Tasks[0].SourceNode = 0 }},
		{name: "missing target", mutate: func(s *ClusterState) { s.Tasks[0].TargetNode = 0 }},
		{name: "same source and target", mutate: func(s *ClusterState) { s.Tasks[0].TargetNode = s.Tasks[0].SourceNode }},
		{name: "epoch mismatch", mutate: func(s *ClusterState) { s.Tasks[0].ConfigEpoch++ }},
		{name: "source outside assignment", mutate: func(s *ClusterState) { s.Tasks[0].SourceNode = 4; s.Tasks[0].TargetNode = 3 }},
		{name: "target already assigned", mutate: func(s *ClusterState) { s.Tasks[0].TargetNode = 2 }},
		{name: "target node missing", mutate: func(s *ClusterState) { s.Tasks[0].TargetNode = 9; s.Tasks[0].TargetPeers = []uint64{1, 2, 9} }},
		{name: "target node leaving", mutate: func(s *ClusterState) { s.Nodes[3].JoinState = NodeJoinStateLeaving }},
		{name: "target lacks data role", mutate: func(s *ClusterState) { s.Nodes[3].Roles = []NodeRole{NodeRoleControllerVoter} }},
		{name: "target peers mismatch", mutate: func(s *ClusterState) { s.Tasks[0].TargetPeers = []uint64{1, 3, 4} }},
		{name: "wrong completion policy", mutate: func(s *ClusterState) { s.Tasks[0].CompletionPolicy = TaskCompletionPolicyAllTargetPeers }},
		{name: "participant progress present", mutate: func(s *ClusterState) {
			s.Tasks[0].ParticipantProgress = []TaskParticipantProgress{{NodeID: 4, Status: TaskParticipantStatusPending}}
		}},
		{name: "duplicate observed voter", mutate: func(s *ClusterState) { s.Tasks[0].ObservedVoters = []uint64{1, 2, 1} }},
		{name: "zero observed learner", mutate: func(s *ClusterState) { s.Tasks[0].ObservedLearners = []uint64{0} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validSlotReplicaMoveState()
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}
