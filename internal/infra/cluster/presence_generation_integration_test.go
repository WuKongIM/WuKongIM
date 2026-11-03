//go:build integration

package cluster

import (
	"context"
	"errors"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

type pendingGenerationFixture struct {
	claim presence.GenerationClaim
	route presence.Route
}

func (f *pendingGenerationFixture) RegisterRoute(_ context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	f.claim = presence.GenerationClaim{Target: target, OwnerSetEpoch: 7, ServingBoot: 11, Generation: f.claim.Generation + 100, PreviousGeneration: f.claim.Generation, AppliedRaftIndex: f.claim.Generation + 100}
	f.route = route
	claim := f.claim
	return presence.RegisterResult{PendingToken: "slot:1", PendingClaim: &claim}, nil
}
func (f *pendingGenerationFixture) CommitRouteWithReceipt(_ context.Context, claim presence.GenerationClaim, _ presence.PendingRouteToken) (presence.ActivationReceipt, error) {
	if !claim.Equal(f.claim) {
		return presence.ActivationReceipt{}, authority.ErrStaleGeneration
	}
	return presence.ActivationReceipt{Claim: claim, Owner: presence.OwnerCut{NodeID: f.route.OwnerNodeID, BootID: f.route.OwnerBootID, Cut: 1}, Identity: f.route.Identity(), OwnerSeq: f.route.OwnerSeq}, nil
}
func (*pendingGenerationFixture) AbortRouteWithClaim(context.Context, presence.GenerationClaim, presence.PendingRouteToken) error {
	return nil
}
func (*pendingGenerationFixture) TouchRoutes(context.Context, presence.GenerationClaim, []presence.Route) error {
	return nil
}
func (*pendingGenerationFixture) UnregisterRoute(context.Context, presence.GenerationClaim, presence.RouteIdentity, uint64) error {
	return nil
}

func TestFullPendingTokenDoesNotAliasAcrossSameFenceGenerations(t *testing.T) {
	node := &fakePresenceCluster{nodeID: 1, route: clusterpkg.Route{HashSlot: 7, SlotID: 11, Leader: 1, LeaderTerm: 2, ConfigEpoch: 3}}
	port := &pendingGenerationFixture{}
	client := NewPresenceAuthorityClient(node, nil)
	client.SetGenerationAuthority(port)
	firstRoute := presence.Route{UID: "u", OwnerNodeID: 1, OwnerBootID: 11, SessionID: 1, OwnerSeq: 1}
	secondRoute := firstRoute
	secondRoute.SessionID = 2
	secondRoute.OwnerSeq = 2
	first, err := client.RegisterRoute(context.Background(), firstRoute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.RegisterRoute(context.Background(), secondRoute)
	if err != nil {
		t.Fatal(err)
	}
	if first.PendingToken == second.PendingToken {
		t.Fatal("same-fence new generation overwrote an older pending token")
	}
	if _, err = client.CommitRouteWithReceipt(context.Background(), first.PendingToken); !errors.Is(err, authority.ErrStaleGeneration) {
		t.Fatalf("old token committed successor candidate: %v", err)
	}
	receipt, err := client.CommitRouteWithReceipt(context.Background(), second.PendingToken)
	if err != nil || receipt.Identity != secondRoute.Identity() {
		t.Fatalf("current candidate lost: %+v %v", receipt, err)
	}
}
