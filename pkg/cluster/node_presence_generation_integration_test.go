//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// Real Controller and Slot Raft plus transport prove the public Node seam.
// Explicit capability enablement is the fixture's business-adapter declaration;
// full application wiring remains a separate cross-layer acceptance.
func TestPresenceGenerationAcrossRealClusterAndRemoteOwner(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	for _, node := range nodes {
		node.cfg.Slots.HashSlotCount = 256
		if err := node.EnablePresenceProtocol(1); err != nil {
			t.Fatal(err)
		}
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	owners, err := nodes[0].EnsurePresenceOwnerSet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners.Owners) != 3 || owners.Epoch == 0 {
		t.Fatalf("owners=%+v", owners)
	}
	route := waitRouteKeyLeaderReady(t, nodes[0], "presence-committed-seam")
	var authority, remote *Node
	for _, node := range nodes {
		if node.NodeID() == route.Leader {
			authority = node
		} else if remote == nil {
			remote = node
		}
	}
	if authority == nil || remote == nil {
		t.Fatal("missing cluster participants")
	}
	// The caller uses the same committed Get as the production generation adapter;
	// Ensure on another node does not prove this authority has observed its mirror.
	cut, err := authority.GetPresenceGeneration(ctx, route.HashSlot)
	if err != nil || cut.OwnerSet.Epoch != owners.Epoch {
		t.Fatalf("authority committed cut: %+v/%v", cut, err)
	}
	claim, err := authority.ClaimPresenceGeneration(ctx, controller.ClaimPresenceGenerationRequest{Fence: controller.PresenceFence{HashSlot: route.HashSlot, SlotID: route.SlotID, LeaderNodeID: route.Leader, LeaderTerm: route.LeaderTerm, ConfigEpoch: route.ConfigEpoch}, OwnerSetEpoch: owners.Epoch, ServingBoot: authority.PresenceBootID()})
	if err != nil {
		t.Fatalf("claim:%v", err)
	}
	if err := remote.ValidatePresenceGeneration(ctx, claim); err != nil {
		t.Fatalf("remote Begin check:%v", err)
	}
	if err := remote.CheckPresenceOwnerGeneration(claim); err != nil {
		t.Fatalf("remote page check:%v", err)
	}
	if err := remote.CheckPresenceGeneration(claim); err == nil {
		t.Fatal("non-authority accepted local serving claim")
	}
	if err := authority.ValidatePresenceGeneration(ctx, claim); err != nil {
		t.Fatalf("authority fresh publication:%v", err)
	}
	if err := authority.CheckPresenceGeneration(claim); err != nil {
		t.Fatalf("authority check:%v", err)
	}
	successor, err := authority.ClaimPresenceGeneration(ctx, controller.ClaimPresenceGenerationRequest{Fence: claim.Fence, OwnerSetEpoch: owners.Epoch, ServingBoot: authority.PresenceBootID(), ExpectedGeneration: claim.Generation})
	if err != nil || successor.Generation <= claim.Generation {
		t.Fatalf("successor=%+v/%v", successor, err)
	}
	if err := remote.ValidatePresenceGeneration(ctx, claim); err == nil {
		t.Fatal("old committed claim reused after successor")
	}
	if err := remote.ValidatePresenceGeneration(ctx, successor); err != nil {
		t.Fatalf("new owner Begin:%v", err)
	}
}
