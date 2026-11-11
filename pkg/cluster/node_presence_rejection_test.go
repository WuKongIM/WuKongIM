package cluster

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

// Every owner-side rejection keeps its compatible sentinel but also names the
// exact failed check, so a remote owner that rejects Begin can report why
// (#1097: all failures previously collapsed into one "not ready" value).
func TestPresenceOwnerCheckRejectionNamesFailedCheck(t *testing.T) {
	setup := func(t *testing.T) (*Node, controller.PresenceGeneration, func(controller.PresenceGeneration, []controller.PresenceOwner)) {
		n := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), presenceBoot: 123}
		n.started.Store(true)
		if err := n.EnablePresenceProtocol(1); err != nil {
			t.Fatal(err)
		}
		snap := nodeControlSnapshot()
		if err := n.router.UpdateControlSnapshot(snap); err != nil {
			t.Fatal(err)
		}
		n.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 7}})
		route, err := n.RouteHashSlot(3)
		if err != nil {
			t.Fatal(err)
		}
		claim := controller.PresenceGeneration{Fence: controller.PresenceFence{HashSlot: 3, SlotID: route.SlotID, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: route.ConfigEpoch}, OwnerSetEpoch: 10, ServingBoot: 123, Generation: 12, AppliedRaftIndex: 12}
		publish := func(g controller.PresenceGeneration, owners []controller.PresenceOwner) {
			s := snap
			s.Presence = &state.PresenceState{RosterRevision: 10, OwnerSet: controller.PresenceOwnerSet{Epoch: 10, Owners: owners}, Generations: []controller.PresenceGeneration{g}}
			s.PresenceRosterCurrent = true
			n.publishPresenceSnapshot(s)
		}
		return n, claim, publish
	}
	self := controller.PresenceOwner{NodeID: 1, Addr: "node-1", JoinState: controller.NodeJoinStateActive, CapabilityGeneration: 123}

	cases := []struct {
		name string
		run  func(t *testing.T) error
		want controller.PresenceRejectReason
	}{
		{"read view never published", func(t *testing.T) error {
			n, claim, _ := setup(t)
			return n.CheckPresenceOwnerGeneration(claim)
		}, controller.PresenceRejectReadViewNotReady},
		{"owner set epoch moved", func(t *testing.T) error {
			n, claim, publish := setup(t)
			publish(claim, []controller.PresenceOwner{self})
			claim.OwnerSetEpoch = 9
			return n.CheckPresenceOwnerGeneration(claim)
		}, controller.PresenceRejectOwnerSetEpochMismatch},
		{"published generation differs", func(t *testing.T) error {
			n, claim, publish := setup(t)
			publish(claim, []controller.PresenceOwner{self})
			claim.Generation, claim.AppliedRaftIndex = 13, 13
			return n.CheckPresenceOwnerGeneration(claim)
		}, controller.PresenceRejectGenerationMismatch},
		{"own boot absent from owner set", func(t *testing.T) error {
			n, claim, publish := setup(t)
			publish(claim, []controller.PresenceOwner{self})
			// The owner restarted with a new boot while the committed owner set
			// still advertises the previous boot.
			n.presenceBoot = 999
			return n.CheckPresenceOwnerGeneration(claim)
		}, controller.PresenceRejectOwnBootNotInOwnerSet},
		{"serving boot absent from owner set", func(t *testing.T) error {
			n, claim, publish := setup(t)
			claim.ServingBoot = 456
			publish(claim, []controller.PresenceOwner{self})
			return n.CheckPresenceOwnerGeneration(claim)
		}, controller.PresenceRejectServingBootNotInOwnerSet},
		{"fence route moved", func(t *testing.T) error {
			n, claim, publish := setup(t)
			claim.Fence.LeaderTerm = 8
			publish(claim, []controller.PresenceOwner{self})
			return n.CheckPresenceOwnerGeneration(claim)
		}, controller.PresenceRejectFenceRouteMismatch},
		{"generation retired", func(t *testing.T) error {
			n, claim, publish := setup(t)
			publish(claim, []controller.PresenceOwner{self})
			n.retirePresenceGeneration(claim.Fence.HashSlot, claim.Generation)
			return n.CheckPresenceOwnerGeneration(claim)
		}, controller.PresenceRejectGenerationRetired},
		{"serving check from a non-serving boot", func(t *testing.T) error {
			n, claim, publish := setup(t)
			publish(claim, []controller.PresenceOwner{self})
			claim.ServingBoot = 456
			return n.CheckPresenceGeneration(claim)
		}, controller.PresenceRejectServingBootMismatch},
		{"validate an empty generation", func(t *testing.T) error {
			n, _, _ := setup(t)
			return n.ValidatePresenceGeneration(t.Context(), controller.PresenceGeneration{})
		}, controller.PresenceRejectEmptyGeneration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			if !errors.Is(err, controller.ErrPresenceGenerationNotReady) {
				t.Fatalf("compatible sentinel lost: %v", err)
			}
			if got := controller.PresenceRejectReasonOf(err); got != tc.want {
				t.Fatalf("reason=%s want %s", got, tc.want)
			}
			if tc.want.String() == "" || tc.want.String() == controller.PresenceRejectUnknown.String() {
				t.Fatal("closed reason has no stable name")
			}
		})
	}
	if got := controller.PresenceRejectReasonOf(controller.ErrPresenceGenerationNotReady); got != controller.PresenceRejectUnknown {
		t.Fatalf("plain sentinel invented a reason: %s", got)
	}
	if got := controller.PresenceRejectReasonOf(errors.Join(errors.New("wrapped"), controller.PresenceRejection(controller.PresenceRejectCommittedCutMismatch))); got != controller.PresenceRejectCommittedCutMismatch {
		t.Fatalf("joined chain lost reason: %s", got)
	}
	if !errors.Is(controller.PresenceRejection(controller.PresenceRejectCommittedCutMismatch), controller.ErrPresenceGenerationConflict) {
		t.Fatal("committed-cut mismatch must stay a conflict")
	}
}
