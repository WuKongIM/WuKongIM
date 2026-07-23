//go:build e2e

package medium_recipient_hotpath

import (
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

func TestValidateMediumSlotConvergence(t *testing.T) {
	inventories := stableMediumSlotInventories()

	t.Run("accepts actual leaders that differ from preferred leaders", func(t *testing.T) {
		inventories := cloneMediumSlotInventories(inventories)
		for nodeID, inventory := range inventories {
			inventory.Items[0].Assignment.PreferredLeaderID = 2
			inventory.Items[0].NodeLog.LeaderID = 1
			inventories[nodeID] = inventory
		}

		snapshot, err := validateMediumSlotConvergence(inventories)
		if err != nil {
			t.Fatalf("validate convergence: %v", err)
		}
		if len(snapshot.Leaders) != mediumLogicalSlots || snapshot.Leaders[0] != 1 {
			t.Fatalf("leaders = %v, want %d leaders beginning with actual leader 1", snapshot.Leaders, mediumLogicalSlots)
		}
		if snapshot.Fingerprint == "" {
			t.Fatal("convergence fingerprint is empty")
		}
	})

	tests := []struct {
		name string
		edit func(map[uint64]mediumSlotsResponse)
		want string
	}{
		{
			name: "missing logical slot",
			edit: func(items map[uint64]mediumSlotsResponse) {
				inventory := items[1]
				inventory.Items = inventory.Items[:len(inventory.Items)-1]
				inventory.Total = len(inventory.Items)
				items[1] = inventory
			},
			want: "logical slots",
		},
		{
			name: "missing local raft observation",
			edit: func(items map[uint64]mediumSlotsResponse) {
				items[2].Items[3].NodeLog = nil
			},
			want: "Raft observation",
		},
		{
			name: "leader disagreement",
			edit: func(items map[uint64]mediumSlotsResponse) {
				items[3].Items[4].NodeLog.LeaderID = 1
			},
			want: "leader disagreement",
		},
		{
			name: "incomplete voter set",
			edit: func(items map[uint64]mediumSlotsResponse) {
				items[1].Items[5].NodeLog.CurrentVoters = []uint64{1, 2}
			},
			want: "current voters",
		},
		{
			name: "lost hash slot",
			edit: func(items map[uint64]mediumSlotsResponse) {
				hashSlots := items[1].Items[0].HashSlots
				hashSlots.Items = hashSlots.Items[1:]
				hashSlots.Count = len(hashSlots.Items)
			},
			want: "physical hash-slot coverage",
		},
		{
			name: "empty logical slot",
			edit: func(items map[uint64]mediumSlotsResponse) {
				first := items[1].Items[0].HashSlots
				second := items[1].Items[1].HashSlots
				second.Items = append(append([]uint16(nil), first.Items...), second.Items...)
				second.Count = len(second.Items)
				first.Items = nil
				first.Count = 0
			},
			want: "physical hash-slot inventory is empty",
		},
		{
			name: "active controller task",
			edit: func(items map[uint64]mediumSlotsResponse) {
				items[1].Items[6].Task = &suite.SlotTaskDTO{TaskID: "task-1"}
			},
			want: "active task",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneMediumSlotInventories(inventories)
			test.edit(candidate)
			_, err := validateMediumSlotConvergence(candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateMediumSlotStateMatchesStableProofRejectsTOCTOU(t *testing.T) {
	baseline, err := validateMediumSlotConvergence(stableMediumSlotInventories())
	if err != nil {
		t.Fatalf("validate baseline convergence: %v", err)
	}
	proof := suite.SlotLeaderConvergence{Fingerprint: baseline.Fingerprint}
	if err := validateMediumSlotStateMatchesStableProof(proof, baseline); err != nil {
		t.Fatalf("unchanged state rejected: %v", err)
	}

	tests := map[string]func(map[uint64]mediumSlotsResponse){
		"actual leader": func(inventories map[uint64]mediumSlotsResponse) {
			for nodeID, inventory := range inventories {
				inventory.Items[0].NodeLog.LeaderID = 3
				inventory.Items[0].NodeLog.Role = "follower"
				if nodeID == 3 {
					inventory.Items[0].NodeLog.Role = "leader"
				}
				inventories[nodeID] = inventory
			}
		},
		"preferred leader": func(inventories map[uint64]mediumSlotsResponse) {
			for nodeID, inventory := range inventories {
				inventory.Items[0].Assignment.PreferredLeaderID = 3
				inventories[nodeID] = inventory
			}
		},
		"config epoch": func(inventories map[uint64]mediumSlotsResponse) {
			for nodeID, inventory := range inventories {
				inventory.Items[0].Assignment.ConfigEpoch++
				inventories[nodeID] = inventory
			}
		},
		"balance version": func(inventories map[uint64]mediumSlotsResponse) {
			for nodeID, inventory := range inventories {
				inventory.Items[0].Assignment.BalanceVersion++
				inventories[nodeID] = inventory
			}
		},
		"slot IDs": func(inventories map[uint64]mediumSlotsResponse) {
			for nodeID, inventory := range inventories {
				for index := range inventory.Items {
					inventory.Items[index].SlotID += 100
				}
				inventories[nodeID] = inventory
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			inventories := stableMediumSlotInventories()
			mutate(inventories)
			current, err := validateMediumSlotConvergence(inventories)
			if err != nil {
				t.Fatalf("validate current convergence: %v", err)
			}
			if err := validateMediumSlotStateMatchesStableProof(proof, current); err == nil {
				t.Fatal("changed state matched the stable proof")
			}
		})
	}
}

func stableMediumSlotInventories() map[uint64]mediumSlotsResponse {
	items := make([]suite.SlotDTO, mediumLogicalSlots)
	nextHashSlot := 0
	for slotIndex := range items {
		hashSlotCount := mediumPhysicalHashSlots / mediumLogicalSlots
		if slotIndex < mediumPhysicalHashSlots%mediumLogicalSlots {
			hashSlotCount++
		}
		hashSlots := make([]uint16, hashSlotCount)
		for i := range hashSlots {
			hashSlots[i] = uint16(nextHashSlot)
			nextHashSlot++
		}
		leaderID := uint64(slotIndex%mediumReplicaCount + 1)
		items[slotIndex] = suite.SlotDTO{
			SlotID: uint32(slotIndex + 1),
			HashSlots: &suite.SlotHashSlotsDTO{
				Count: len(hashSlots),
				Items: hashSlots,
			},
			Assignment: suite.SlotAssignmentDTO{
				DesiredPeers:      []uint64{1, 2, 3},
				PreferredLeaderID: uint64((slotIndex+1)%mediumReplicaCount + 1),
				ConfigEpoch:       7,
				BalanceVersion:    9,
			},
			Runtime: suite.SlotRuntimeDTO{
				CurrentPeers:      []uint64{1, 2, 3},
				CurrentVoters:     []uint64{1, 2, 3},
				HealthyVoters:     3,
				HasQuorum:         true,
				PreferredLeaderID: uint64((slotIndex+1)%mediumReplicaCount + 1),
			},
			NodeLog: &suite.SlotNodeLogDTO{
				LeaderID:      leaderID,
				Role:          "follower",
				CurrentVoters: []uint64{1, 2, 3},
				CommitIndex:   10,
				AppliedIndex:  10,
			},
		}
	}

	inventories := make(map[uint64]mediumSlotsResponse, mediumReplicaCount)
	for nodeID := uint64(1); nodeID <= mediumReplicaCount; nodeID++ {
		nodeItems := cloneMediumSlots(items)
		for index := range nodeItems {
			nodeItems[index].NodeLog.NodeID = nodeID
			if nodeItems[index].NodeLog.LeaderID == nodeID {
				nodeItems[index].NodeLog.Role = "leader"
			}
		}
		inventories[nodeID] = mediumSlotsResponse{Total: len(nodeItems), Items: nodeItems}
	}
	return inventories
}

func cloneMediumSlotInventories(source map[uint64]mediumSlotsResponse) map[uint64]mediumSlotsResponse {
	cloned := make(map[uint64]mediumSlotsResponse, len(source))
	for nodeID, inventory := range source {
		cloned[nodeID] = mediumSlotsResponse{
			Total: inventory.Total,
			Items: cloneMediumSlots(inventory.Items),
		}
	}
	return cloned
}

func cloneMediumSlots(source []suite.SlotDTO) []suite.SlotDTO {
	cloned := make([]suite.SlotDTO, len(source))
	for index, item := range source {
		cloned[index] = item
		cloned[index].Assignment.DesiredPeers = append([]uint64(nil), item.Assignment.DesiredPeers...)
		cloned[index].Runtime.CurrentPeers = append([]uint64(nil), item.Runtime.CurrentPeers...)
		cloned[index].Runtime.CurrentVoters = append([]uint64(nil), item.Runtime.CurrentVoters...)
		if item.HashSlots != nil {
			hashSlots := *item.HashSlots
			hashSlots.Items = append([]uint16(nil), item.HashSlots.Items...)
			cloned[index].HashSlots = &hashSlots
		}
		if item.NodeLog != nil {
			nodeLog := *item.NodeLog
			nodeLog.CurrentVoters = append([]uint64(nil), item.NodeLog.CurrentVoters...)
			cloned[index].NodeLog = &nodeLog
		}
		if item.Task != nil {
			task := *item.Task
			cloned[index].Task = &task
		}
	}
	return cloned
}
