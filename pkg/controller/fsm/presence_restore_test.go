package fsm

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

// A snapshot install into an existing history must not drop the known high-water
// marks. This cannot detect an operator replacing every disk with an old copy.
func TestPresenceSnapshotRestoreRejectsKnownHighWaterRollback(t *testing.T) {
	ctx := context.Background()
	sm, _ := initializedStateMachine(t, 1)
	current := sm.Snapshot(ctx)
	current.Presence = &state.PresenceState{OwnerSet: state.PresenceOwnerSet{Epoch: 2, Owners: []state.PresenceOwner{{NodeID: 1, Addr: "node-1", JoinState: state.NodeJoinStateActive, CapabilityGeneration: 7}}}, Generations: []state.PresenceGeneration{{Fence: state.PresenceFence{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1}, OwnerSetEpoch: 2, ServingBoot: 7, Generation: 3, AppliedRaftIndex: 3}}}
	current.AppliedRaftIndex = 3
	if err := sm.Restore(ctx, current); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"missing", "lower", "mutated_same"} {
		t.Run(variant, func(t *testing.T) {
			older := current.Clone()
			older.AppliedRaftIndex = 4
			switch variant {
			case "missing":
				older.Presence = nil
			case "lower":
				older.Presence.Generations[0].Generation = 2
				older.Presence.Generations[0].AppliedRaftIndex = 2
			case "mutated_same":
				older.Presence.Generations[0].ServingBoot = 8
			}
			if err := sm.Restore(ctx, older); err == nil {
				t.Fatal("rollback accepted")
			}
			got := sm.Snapshot(ctx)
			if got.Presence == nil || got.Presence.Generations[0] != current.Presence.Generations[0] {
				t.Fatal("failed restore changed published high-water")
			}
		})
	}
}
