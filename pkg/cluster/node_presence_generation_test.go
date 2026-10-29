package cluster

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestPresenceCapabilityRequiresExplicitBusinessWiring(t *testing.T) {
	n := &Node{cfg: Config{NodeID: 1}, presenceBoot: 123}
	if got := n.PresenceCapabilities(); got.PresenceProtocolVersion != 0 || got.ControllerStateVersion != 1 || got.BootID != 123 {
		t.Fatalf("unwired caps=%+v", got)
	}
	if err := n.EnablePresenceProtocol(2); err == nil {
		t.Fatal("unknown version enabled")
	}
	if err := n.EnablePresenceProtocol(1); err != nil {
		t.Fatal(err)
	}
	if n.PresenceCapabilities().PresenceProtocolVersion != 1 {
		t.Fatal("wired version missing")
	}
}

func TestPresenceLocalCheckRequiresCommittedGenerationAndActualSlot(t *testing.T) {
	n := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), presenceBoot: 123}
	n.started.Store(true)
	if err := n.EnablePresenceProtocol(1); err != nil {
		t.Fatal(err)
	}
	snap := nodeControlSnapshot()
	// The default is 256; existing larger legal configurations remain supported.
	snap.HashSlots.Count = 512
	snap.HashSlots.Ranges[0].To = 511
	if err := n.router.UpdateControlSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	n.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 7}})
	route, err := n.RouteHashSlot(300)
	if err != nil {
		t.Fatal(err)
	}
	claim := controller.PresenceGeneration{Fence: controller.PresenceFence{HashSlot: 300, SlotID: route.SlotID, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: route.ConfigEpoch}, OwnerSetEpoch: 10, ServingBoot: 123, Generation: 12, AppliedRaftIndex: 12}
	if err := n.CheckPresenceGeneration(claim); err == nil {
		t.Fatal("unpublished claim accepted")
	}
	status := multiraft.Status{SlotID: 1, NodeID: 1, LeaderID: 1, Term: 7, Role: multiraft.RoleLeader}
	n.slotStatusRuntime = slotStatusReaderFunc(func(multiraft.SlotID) (multiraft.Status, error) { return status, nil })
	snap.Presence = &state.PresenceState{OwnerSet: controller.PresenceOwnerSet{Epoch: 10, Owners: []controller.PresenceOwner{{NodeID: 1, Addr: "node-1", JoinState: controller.NodeJoinStateActive, CapabilityGeneration: 123}}}, Generations: []controller.PresenceGeneration{claim}}
	snap.PresenceRosterCurrent = true
	n.publishPresenceSnapshot(snap)
	if err := n.CheckPresenceGeneration(claim); err != nil {
		t.Fatalf("valid claim: %v", err)
	}
	if err := n.CheckPresenceOwnerGeneration(claim); err != nil {
		t.Fatalf("owner check: %v", err)
	}
	local, err := n.GetLocalPresenceGeneration(300)
	if err != nil || local.Generation != claim || local.OwnerSet.Epoch != 10 {
		t.Fatalf("local cut = %+v, %v", local, err)
	}
	local.OwnerSet.Owners[0].NodeID = 999
	local, err = n.GetLocalPresenceGeneration(300)
	if err != nil || local.OwnerSet.Owners[0].NodeID != 1 {
		t.Fatal("mutable local cut escaped")
	}
	status.Term = 8
	if err := n.CheckPresenceGeneration(claim); err == nil {
		t.Fatal("stale actual term accepted")
	}
	if err := n.CheckPresenceOwnerGeneration(claim); err != nil {
		t.Fatalf("owner check incorrectly requires local Slot: %v", err)
	}
	status.Term = 7
	stale := claim
	stale.Generation = 11
	if err := n.CheckPresenceGeneration(stale); err == nil {
		t.Fatal("old generation accepted")
	}
	snap.PresenceRosterCurrent = false
	n.publishPresenceSnapshot(snap)
	if err := n.CheckPresenceGeneration(claim); err == nil {
		t.Fatal("changed roster accepted")
	}
}
