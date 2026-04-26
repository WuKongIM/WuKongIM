package plane

import (
	"strings"
	"testing"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestOnboardingPlanFingerprintIsStableForIdenticalInput(t *testing.T) {
	input := sampleFingerprintInput()

	first := OnboardingPlanFingerprint(input)
	second := OnboardingPlanFingerprint(input)

	require.NotEmpty(t, first)
	require.Equal(t, first, second)
}

func TestOnboardingPlanFingerprintChangesWhenAssignmentEpochChanges(t *testing.T) {
	input := sampleFingerprintInput()
	changed := sampleFingerprintInput()
	assignment := changed.Assignments[2]
	assignment.ConfigEpoch++
	changed.Assignments[2] = assignment

	require.NotEqual(t, OnboardingPlanFingerprint(input), OnboardingPlanFingerprint(changed))
}

func TestOnboardingPlanFingerprintPreservesMoveOrder(t *testing.T) {
	input := sampleFingerprintInput()
	reordered := sampleFingerprintInput()
	reordered.Plan.Moves[0], reordered.Plan.Moves[1] = reordered.Plan.Moves[1], reordered.Plan.Moves[0]

	require.NotEqual(t, OnboardingPlanFingerprint(input), OnboardingPlanFingerprint(reordered))
}

func TestOnboardingPlanFingerprintCanonicalJSONSortsKeysAndOmitsWhitespace(t *testing.T) {
	input := sampleFingerprintInput()
	input.Plan.Moves = input.Plan.Moves[:1]
	delete(input.Assignments, 3)
	delete(input.Runtime, 3)

	canonical := OnboardingPlanCanonicalJSON(input)

	require.NotContains(t, canonical, " ")
	require.NotContains(t, canonical, "\n")
	require.NotContains(t, canonical, "\t")
	require.True(t, strings.HasPrefix(canonical, `{"moves":[{"assignment":{"balance_version":2,"config_epoch":7},"current_leader_id":1`))
	require.Contains(t, canonical, `"reason":"replica_balance"`)
	require.Contains(t, canonical, `"runtime":{"current_peers":[1,2,3],"has_quorum":true,"leader_id":1,"observed_config_epoch":7}`)
	require.True(t, strings.HasSuffix(canonical, `"target":{"join_state":2,"node_id":4,"role":1,"status":1}}`))
}

func sampleFingerprintInput() OnboardingPlanFingerprintInput {
	plan := controllermeta.NodeOnboardingPlan{
		TargetNodeID: 4,
		Moves: []controllermeta.NodeOnboardingPlanMove{
			{
				SlotID:                 2,
				SourceNodeID:           1,
				TargetNodeID:           4,
				Reason:                 "replica_balance",
				DesiredPeersBefore:     []uint64{1, 2, 3},
				DesiredPeersAfter:      []uint64{2, 3, 4},
				CurrentLeaderID:        1,
				LeaderTransferRequired: true,
			},
			{
				SlotID:             3,
				SourceNodeID:       2,
				TargetNodeID:       4,
				Reason:             "replica_balance",
				DesiredPeersBefore: []uint64{1, 2, 3},
				DesiredPeersAfter:  []uint64{1, 3, 4},
				CurrentLeaderID:    1,
			},
		},
	}

	return OnboardingPlanFingerprintInput{
		TargetNode: onboardingNode(4, controllermeta.NodeStatusAlive, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive),
		Plan:       plan,
		Assignments: map[uint32]controllermeta.SlotAssignment{
			2: {SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, BalanceVersion: 2},
			3: {SlotID: 3, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, BalanceVersion: 3},
		},
		Runtime: map[uint32]controllermeta.SlotRuntimeView{
			2: {SlotID: 2, CurrentPeers: []uint64{1, 2, 3}, LeaderID: 1, HasQuorum: true, ObservedConfigEpoch: 7},
			3: {SlotID: 3, CurrentPeers: []uint64{1, 2, 3}, LeaderID: 1, HasQuorum: true, ObservedConfigEpoch: 8},
		},
		Tasks:          map[uint32]controllermeta.ReconcileTask{},
		MigratingSlots: map[uint32]struct{}{},
	}
}
