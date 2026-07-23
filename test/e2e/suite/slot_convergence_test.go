//go:build e2e

package suite

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSlotLeaderConvergenceTrackerRequiresUnchangedFingerprint(t *testing.T) {
	tracker := newSlotLeaderConvergenceTracker(2 * time.Second)
	start := time.Unix(100, 0)

	stableFor, ready := tracker.Observe(start, "slot-1:leader-2")
	require.Zero(t, stableFor)
	require.False(t, ready)

	stableFor, ready = tracker.Observe(start.Add(time.Second), "slot-1:leader-2")
	require.Equal(t, time.Second, stableFor)
	require.False(t, ready)

	stableFor, ready = tracker.Observe(start.Add(1500*time.Millisecond), "slot-1:leader-3")
	require.Zero(t, stableFor)
	require.False(t, ready)

	stableFor, ready = tracker.Observe(start.Add(3500*time.Millisecond), "slot-1:leader-3")
	require.Equal(t, 2*time.Second, stableFor)
	require.True(t, ready)
}

func TestValidateSlotLeaderConvergenceRejectsCrossNodeLeaderDisagreement(t *testing.T) {
	inventories := validSlotLeaderInventories()
	inventories.byVoter[3].Items[0].NodeLog.LeaderID = 3
	inventories.byVoter[3].Items[0].NodeLog.Role = "leader"

	_, err := validateSlotLeaderConvergence(inventories, []uint64{1, 2, 3})
	require.ErrorContains(t, err, "actual leader disagreement")
}

func TestValidateSlotLeaderConvergenceAcceptsAgreedRaftState(t *testing.T) {
	snapshot, err := validateSlotLeaderConvergence(validSlotLeaderInventories(), []uint64{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, []ActualSlotLeader{{SlotID: 1, LeaderID: 2}}, snapshot.Leaders)
	require.NotEmpty(t, snapshot.Fingerprint)
}

func TestValidateSlotLeaderConvergenceAcceptsNonVoterNodeAndPreferredLeaderMismatch(t *testing.T) {
	inventories := validSlotLeaderInventories()
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		inventories.full[nodeID].Items[0].Assignment.PreferredLeaderID = 1
		inventories.byVoter[nodeID].Items[0].Assignment.PreferredLeaderID = 1
	}
	inventories.full[4] = managerSlotsResponse{
		Total: 1,
		Items: []SlotDTO{inventories.full[1].Items[0]},
	}
	inventories.byVoter[4] = managerSlotsResponse{}

	snapshot, err := validateSlotLeaderConvergence(inventories, []uint64{1, 2, 3, 4})
	require.NoError(t, err)
	require.Equal(t, []ActualSlotLeader{{SlotID: 1, LeaderID: 2}}, snapshot.Leaders)
}

func TestValidateSlotLeaderConvergenceRejectsLostQuorum(t *testing.T) {
	inventories := validSlotLeaderInventories()
	inventories.byVoter[1].Items[0].Runtime.HasQuorum = false

	_, err := validateSlotLeaderConvergence(inventories, []uint64{1, 2, 3})
	require.ErrorContains(t, err, "runtime voters/quorum")
}

func TestValidateSlotLeaderConvergenceRejectsInvalidDesiredPeers(t *testing.T) {
	for name, peers := range map[string][]uint64{
		"duplicate": {1, 2, 2},
		"unknown":   {1, 2, 4},
		"zero":      {0, 1, 2},
	} {
		t.Run(name, func(t *testing.T) {
			inventories := validSlotLeaderInventories()
			inventories.full[1].Items[0].Assignment.DesiredPeers = peers

			_, err := validateSlotLeaderConvergence(inventories, []uint64{1, 2, 3})
			require.Error(t, err)
		})
	}
}

func TestValidateSlotLeaderConvergenceRejectsUnknownOnlyControlSlot(t *testing.T) {
	inventories := validSlotLeaderInventories()
	unknownSlot := inventories.full[1].Items[0]
	unknownSlot.SlotID = 2
	unknownSlot.Assignment.DesiredPeers = []uint64{4, 5, 6}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		inventory := inventories.full[nodeID]
		inventory.Total = 2
		inventory.Items = append(inventory.Items, unknownSlot)
		inventories.full[nodeID] = inventory
	}

	_, err := validateSlotLeaderConvergence(inventories, []uint64{1, 2, 3})
	require.ErrorContains(t, err, "unknown node")
}

func TestValidateSlotLeaderConvergenceRejectsRaftAndRuntimeVoterMismatch(t *testing.T) {
	tests := map[string]func(*SlotDTO){
		"Raft": func(item *SlotDTO) {
			item.NodeLog.CurrentVoters = []uint64{1, 2}
		},
		"runtime": func(item *SlotDTO) {
			item.Runtime.CurrentVoters = []uint64{1, 2}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			inventories := validSlotLeaderInventories()
			mutate(&inventories.byVoter[1].Items[0])

			_, err := validateSlotLeaderConvergence(inventories, []uint64{1, 2, 3})
			require.ErrorContains(t, err, "voters")
		})
	}
}

func TestValidateSlotLeaderConvergenceFingerprintIncludesVoters(t *testing.T) {
	first, err := validateSlotLeaderConvergence(
		slotLeaderInventoriesForPeers([]uint64{1, 2, 3}, 2, []uint64{1, 2, 3, 4}),
		[]uint64{1, 2, 3, 4},
	)
	require.NoError(t, err)
	second, err := validateSlotLeaderConvergence(
		slotLeaderInventoriesForPeers([]uint64{1, 2, 4}, 2, []uint64{1, 2, 3, 4}),
		[]uint64{1, 2, 3, 4},
	)
	require.NoError(t, err)
	require.NotEqual(t, first.Fingerprint, second.Fingerprint)
}

func validSlotLeaderInventories() slotLeaderInventorySet {
	return slotLeaderInventoriesForPeers([]uint64{1, 2, 3}, 2, []uint64{1, 2, 3})
}

func slotLeaderInventoriesForPeers(peers []uint64, leaderID uint64, nodeIDs []uint64) slotLeaderInventorySet {
	inventories := slotLeaderInventorySet{
		full:    make(map[uint64]managerSlotsResponse, len(nodeIDs)),
		byVoter: make(map[uint64]managerSlotsResponse, len(nodeIDs)),
	}
	reference := SlotDTO{
		SlotID: 1,
		Assignment: SlotAssignmentDTO{
			DesiredPeers:   append([]uint64(nil), peers...),
			ConfigEpoch:    4,
			BalanceVersion: 7,
		},
	}
	for _, nodeID := range nodeIDs {
		inventories.full[nodeID] = managerSlotsResponse{
			Total: 1,
			Items: []SlotDTO{reference},
		}
		if !containsUint64Value(peers, nodeID) {
			inventories.byVoter[nodeID] = managerSlotsResponse{}
			continue
		}
		role := "follower"
		if nodeID == leaderID {
			role = "leader"
		}
		inventories.byVoter[nodeID] = managerSlotsResponse{
			Total: 1,
			Items: []SlotDTO{{
				SlotID:     reference.SlotID,
				Assignment: reference.Assignment,
				Runtime: SlotRuntimeDTO{
					CurrentVoters: append([]uint64(nil), peers...),
					HasQuorum:     true,
				},
				NodeLog: &SlotNodeLogDTO{
					NodeID:        nodeID,
					LeaderID:      leaderID,
					Role:          role,
					CurrentVoters: append([]uint64(nil), peers...),
					CommitIndex:   10,
					AppliedIndex:  10,
				},
			}},
		}
	}
	return inventories
}
