package cluster

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// The controlled Controller observes the real public Node seam synchronously
// at submission. No sleep or speculative timing creates the retirement proof.
func TestPresenceClaimRetiresOnlyItsObservedPredecessorBeforeSubmit(t *testing.T) {
	n, backend, snapshot, previous, sibling := presenceRetirementFixture(t)
	failure := errors.New("controlled committed result unknown")
	backend.claim = func(req controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
		if err := n.CheckPresenceGeneration(previous); err == nil {
			t.Error("old admission still open inside Controller submission")
		}
		if err := n.CheckPresenceOwnerGeneration(previous); err == nil {
			t.Error("old local owner gate still open inside Controller submission")
		}
		if err := n.CheckPresenceGeneration(sibling); err != nil {
			t.Errorf("unrelated warm slot closed: %v", err)
		}
		return controller.PresenceGeneration{}, failure
	}
	req := controller.ClaimPresenceGenerationRequest{Fence: previous.Fence, OwnerSetEpoch: previous.OwnerSetEpoch, ServingBoot: previous.ServingBoot, ExpectedGeneration: previous.Generation}
	if _, err := n.ClaimPresenceGeneration(context.Background(), req); !errors.Is(err, failure) {
		t.Fatalf("original claim failure lost: %v", err)
	}
	if err := n.CheckPresenceGeneration(previous); err == nil {
		t.Error("failed claim reopened old admission")
	}
	// Republishing the same committed snapshot cannot resurrect the retired G.
	n.publishPresenceSnapshot(snapshot)
	if err := n.CheckPresenceGeneration(previous); err == nil {
		t.Error("same snapshot resurrected retired generation")
	}
	next := previous
	next.PreviousGeneration = previous.Generation
	next.Generation = 20
	next.AppliedRaftIndex = 20
	snapshot.Revision++
	snapshot.Presence.Generations[0] = next
	n.publishPresenceSnapshot(snapshot)
	if err := n.CheckPresenceGeneration(next); err != nil {
		t.Fatalf("larger committed generation did not reopen: %v", err)
	}
	if err := n.CheckPresenceGeneration(sibling); err != nil {
		t.Fatalf("sibling gate changed: %v", err)
	}
}

func TestPresenceClaimInvalidCASCannotPoisonCurrentGate(t *testing.T) {
	for _, expected := range []uint64{0, 11, math.MaxUint64} {
		n, backend, _, current, _ := presenceRetirementFixture(t)
		calls := 0
		backend.claim = func(controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
			calls++
			return controller.PresenceGeneration{}, controller.ErrPresenceGenerationConflict
		}
		req := controller.ClaimPresenceGenerationRequest{Fence: current.Fence, OwnerSetEpoch: current.OwnerSetEpoch, ServingBoot: current.ServingBoot, ExpectedGeneration: expected}
		if _, err := n.ClaimPresenceGeneration(context.Background(), req); err == nil {
			t.Fatalf("invalid CAS %d accepted", expected)
		}
		if calls != 0 {
			t.Errorf("unproved predecessor %d submitted", expected)
		}
		if err := n.CheckPresenceGeneration(current); err != nil {
			t.Errorf("invalid CAS %d poisoned valid gate: %v", expected, err)
		}
	}
}

func TestPresenceClaimChangedOwnerEpochRetiresOldGeneration(t *testing.T) {
	n, backend, snapshot, current, _ := presenceRetirementFixture(t)
	snapshot.Revision++
	snapshot.Presence.OwnerSet.Epoch = 19
	n.publishPresenceSnapshot(snapshot)
	called := false
	backend.claim = func(req controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
		called = true
		if req.OwnerSetEpoch != 19 {
			t.Errorf("new M lost: %d", req.OwnerSetEpoch)
		}
		return controller.PresenceGeneration{}, controller.ErrPresenceGenerationNotReady
	}
	req := controller.ClaimPresenceGenerationRequest{Fence: current.Fence, OwnerSetEpoch: 19, ServingBoot: current.ServingBoot, ExpectedGeneration: current.Generation}
	if _, err := n.ClaimPresenceGeneration(context.Background(), req); err == nil {
		t.Fatal("controlled failure accepted")
	}
	if !called {
		t.Fatal("observed predecessor with new M rejected before controller")
	}
	snapshot.Revision++
	snapshot.Presence.OwnerSet.Epoch = current.OwnerSetEpoch
	n.publishPresenceSnapshot(snapshot)
	if err := n.CheckPresenceGeneration(current); err == nil {
		t.Fatal("retired old G reopened after owner-set cut changed")
	}
}

type retirementController struct {
	*control.StaticController
	get   func(uint16) (controller.PresenceGenerationSnapshot, error)
	claim func(controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error)
}

func (c *retirementController) EnsurePresenceOwnerSet(context.Context) (controller.PresenceOwnerSet, error) {
	return controller.PresenceOwnerSet{}, controller.ErrPresenceGenerationNotReady
}
func (c *retirementController) ClaimPresenceGeneration(_ context.Context, r controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	return c.claim(r)
}
func (c *retirementController) GetPresenceGeneration(_ context.Context, slot uint16) (controller.PresenceGenerationSnapshot, error) {
	if c.get != nil {
		return c.get(slot)
	}
	return controller.PresenceGenerationSnapshot{}, controller.ErrPresenceGenerationNotReady
}

func presenceRetirementFixture(t *testing.T) (*Node, *retirementController, control.Snapshot, controller.PresenceGeneration, controller.PresenceGeneration) {
	t.Helper()
	snapshot := nodeControlSnapshot()
	snapshot.HashSlots.Count = 256
	snapshot.HashSlots.Ranges[0].To = 255
	n := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), presenceBoot: 123}
	n.started.Store(true)
	if err := n.EnablePresenceProtocol(1); err != nil {
		t.Fatal(err)
	}
	if err := n.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	n.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 7}})
	n.slotStatusRuntime = slotStatusReaderFunc(func(multiraft.SlotID) (multiraft.Status, error) {
		return multiraft.Status{SlotID: 1, NodeID: 1, LeaderID: 1, Term: 7, Role: multiraft.RoleLeader}, nil
	})
	current := controller.PresenceGeneration{Fence: controller.PresenceFence{HashSlot: 37, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 1}, OwnerSetEpoch: 10, ServingBoot: 123, Generation: 12, PreviousGeneration: 8, AppliedRaftIndex: 12}
	sibling := current
	sibling.Fence.HashSlot = 38
	sibling.Generation = 13
	sibling.AppliedRaftIndex = 13
	snapshot.Presence = &state.PresenceState{RosterRevision: 10, OwnerSet: controller.PresenceOwnerSet{Epoch: 10, Owners: []controller.PresenceOwner{{NodeID: 1, Addr: "127.0.0.1:1001", JoinState: controller.NodeJoinStateActive, CapabilityGeneration: 123}}}, Generations: []controller.PresenceGeneration{current, sibling}}
	snapshot.PresenceRosterCurrent = true
	n.controlSnapshot = snapshot.Clone()
	n.publishPresenceSnapshot(snapshot)
	backend := &retirementController{StaticController: control.NewStaticController(snapshot)}
	n.control = backend
	if err := n.CheckPresenceGeneration(current); err != nil {
		t.Fatalf("fixture did not open current generation: %v", err)
	}
	return n, backend, snapshot, current, sibling
}

func TestPresenceCommittedGetPublishesBeforeLaggingMirror(t *testing.T) {
	n, backend, snapshot, current, _ := presenceRetirementFixture(t)
	next := current
	next.OwnerSetEpoch = 19
	next.PreviousGeneration = current.Generation
	next.Generation = 20
	next.AppliedRaftIndex = 20
	owners := snapshot.Presence.OwnerSet.Clone()
	owners.Epoch = 19
	backend.get = func(slot uint16) (controller.PresenceGenerationSnapshot, error) {
		if slot == 39 {
			return controller.PresenceGenerationSnapshot{RosterRevision: 10, Revision: 30, OwnerSet: owners}, nil
		}
		return controller.PresenceGenerationSnapshot{RosterRevision: 10, Revision: 30, OwnerSet: owners, Generation: next}, nil
	}
	if _, err := n.GetPresenceGeneration(context.Background(), 37); err != nil {
		t.Fatal(err)
	}
	got, err := n.GetLocalPresenceGeneration(37)
	if err != nil || got.Generation != next {
		t.Fatalf("fresh same-cut Get not published before mirror: %+v, %v", got, err)
	}
	n.publishPresenceSnapshot(snapshot)
	if err := n.CheckPresenceGeneration(next); err != nil {
		t.Fatalf("lagging mirror replaced authenticated cut: %v", err)
	}
	if _, err := n.GetPresenceGeneration(context.Background(), 39); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("unclaimed slot reached controller with proven M")
	backend.claim = func(controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
		return controller.PresenceGeneration{}, sentinel
	}
	fence := current.Fence
	fence.HashSlot = 39
	if _, err := n.ClaimPresenceGeneration(context.Background(), controller.ClaimPresenceGenerationRequest{Fence: fence, OwnerSetEpoch: 19, ServingBoot: 123}); !errors.Is(err, sentinel) {
		t.Fatalf("explicit unclaimed cut not admitted: %v", err)
	}
	if _, err := n.GetLocalPresenceGeneration(39); err == nil {
		t.Fatal("unclaimed slot became a serving proof")
	}
}

func TestPresenceClaimExactRetryRetiresOnlyProvenPrevious(t *testing.T) {
	n, backend, _, current, _ := presenceRetirementFixture(t)
	backend.claim = func(controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
		if err := n.CheckPresenceGeneration(current); err != nil {
			t.Errorf("retry retired already committed successor: %v", err)
		}
		return current, nil
	}
	req := controller.ClaimPresenceGenerationRequest{Fence: current.Fence, OwnerSetEpoch: current.OwnerSetEpoch, ServingBoot: current.ServingBoot, ExpectedGeneration: current.PreviousGeneration}
	got, err := n.ClaimPresenceGeneration(context.Background(), req)
	if err != nil || got != current {
		t.Fatalf("exact committed retry lost: %+v, %v", got, err)
	}
}
