//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"
)

func TestClusterUserChannelMembershipFacadeUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "membership-channel"
	channelRoute := waitRouteKeyLeaderReady(t, node, channelID)
	uid := findRouteKeyWithDifferentHashSlot(t, node, channelRoute.HashSlot, "membership-user")
	uidRoute := waitRouteKeyLeaderReady(t, node, uid)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := node.UpsertUserChannelMemberships(ctx, channelID, 2, []string{uid}, 0, 123); err != nil {
		t.Fatalf("UpsertUserChannelMemberships() error = %v", err)
	}

	got, err := node.defaultSlotMetaDB.ForHashSlot(uidRoute.HashSlot).GetUserChannelMembership(ctx, uid, channelID, 2)
	if err != nil {
		t.Fatalf("GetUserChannelMembership(uid hash slot): %v", err)
	}
	if got.UID != uid || got.ChannelID != channelID || got.ChannelType != 2 || got.UpdatedAt != 123 {
		t.Fatalf("membership = %#v, want uid/channel row with updated_at=123", got)
	}
	_, err = node.defaultSlotMetaDB.ForHashSlot(channelRoute.HashSlot).GetUserChannelMembership(ctx, uid, channelID, 2)
	if err == nil {
		t.Fatalf("GetUserChannelMembership(channel hash slot) error = nil, want missing")
	}

	if err := node.DeleteUserChannelMemberships(ctx, channelID, 2, []string{uid}, 456); err != nil {
		t.Fatalf("DeleteUserChannelMemberships() error = %v", err)
	}
	_, err = node.defaultSlotMetaDB.ForHashSlot(uidRoute.HashSlot).GetUserChannelMembership(ctx, uid, channelID, 2)
	if err == nil {
		t.Fatalf("GetUserChannelMembership(after delete) error = nil, want missing")
	}
}
