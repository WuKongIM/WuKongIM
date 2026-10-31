package cluster

import (
	"context"
	"errors"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

type generationNodeFixture struct {
	PresenceGenerationNode
	claim        controller.PresenceGeneration
	owners       controller.PresenceOwnerSet
	claimRequest controller.ClaimPresenceGenerationRequest
	errorOnClaim error
	checks       int
}

func (n *generationNodeFixture) NodeID() uint64         { return 1 }
func (n *generationNodeFixture) PresenceBootID() uint64 { return 11 }
func (n *generationNodeFixture) EnsurePresenceOwnerSet(context.Context) (controller.PresenceOwnerSet, error) {
	return n.owners, nil
}
func (n *generationNodeFixture) GetPresenceGeneration(context.Context, uint16) (controller.PresenceGenerationSnapshot, error) {
	return controller.PresenceGenerationSnapshot{Generation: n.claim, OwnerSet: n.owners}, nil
}
func (n *generationNodeFixture) ClaimPresenceGeneration(_ context.Context, r controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	n.claimRequest = r
	if n.errorOnClaim != nil {
		return controller.PresenceGeneration{}, n.errorOnClaim
	}
	n.claim = controller.PresenceGeneration{Fence: r.Fence, OwnerSetEpoch: r.OwnerSetEpoch, ServingBoot: r.ServingBoot, Generation: 101, PreviousGeneration: r.ExpectedGeneration, AppliedRaftIndex: 101}
	return n.claim, nil
}
func (n *generationNodeFixture) CheckPresenceGeneration(c controller.PresenceGeneration) error {
	n.checks++
	if c != n.claim {
		return controller.ErrPresenceGenerationConflict
	}
	return nil
}

func TestPresenceGenerationAdapterPreservesCommittedCASAndOwnerVector(t *testing.T) {
	target := presence.RouteTarget{HashSlot: 37, SlotID: 3, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 2, RouteRevision: 55}
	node := &generationNodeFixture{claim: controller.PresenceGeneration{Generation: 90}, owners: controller.PresenceOwnerSet{Epoch: 7, Owners: []controller.PresenceOwner{{NodeID: 1, CapabilityGeneration: 11}, {NodeID: 2, CapabilityGeneration: 22}}}}
	adapter := NewPresenceGenerationAdapter(node, nil)
	claim, owners, err := adapter.ClaimSlotGeneration(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Generation != 101 || claim.PreviousGeneration != 90 || len(owners) != 2 || owners[1] != 2 {
		t.Fatalf("lost committed receipt/owner: %+v %+v", claim, owners)
	}
	if node.claimRequest.ExpectedGeneration != 90 || node.claimRequest.ServingBoot != 11 || node.claimRequest.OwnerSetEpoch != 7 {
		t.Fatalf("wrong CAS %+v", node.claimRequest)
	}
	if err = adapter.ValidateSlotGeneration(context.Background(), claim); err != nil || node.checks != 1 {
		t.Fatalf("local check: %v %d", err, node.checks)
	}
	stale := claim
	stale.Generation--
	if err = adapter.ValidateSlotGeneration(context.Background(), stale); !errors.Is(err, authority.ErrStaleGeneration) {
		t.Fatalf("stale became valid: %v", err)
	}
	node.errorOnClaim = controller.ErrPresenceGenerationConflict
	if _, _, err = adapter.ClaimSlotGeneration(context.Background(), target); err == nil {
		t.Fatal("CAS failure became successful claim")
	}
}
