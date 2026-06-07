package clusterv2

import (
	"context"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterV2SingleNodeChannelMetadataFacadeDeletesAndRemovesSubscribers(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	route := waitRouteKeyLeaderReady(t, node, "g1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := node.UpsertChannelMetadata(ctx, metadb.Channel{ChannelID: "g1", ChannelType: 2, SubscriberMutationVersion: 4}); err != nil {
		t.Fatalf("UpsertChannelMetadata() error = %v", err)
	}
	if err := node.AddChannelSubscribers(ctx, "g1", 2, []string{"u1", "u2"}, 5); err != nil {
		t.Fatalf("AddChannelSubscribers() error = %v", err)
	}
	if err := node.RemoveChannelSubscribers(ctx, "g1", 2, []string{"u1"}, 6); err != nil {
		t.Fatalf("RemoveChannelSubscribers() error = %v", err)
	}
	channel, err := node.GetChannelMetadata(ctx, "g1", 2)
	if err != nil {
		t.Fatalf("GetChannelMetadata() error = %v", err)
	}
	if channel.SubscriberMutationVersion != 6 {
		t.Fatalf("SubscriberMutationVersion = %d, want 6", channel.SubscriberMutationVersion)
	}
	uids, _, done, err := node.ListChannelSubscribersPage(ctx, "g1", 2, "", 10)
	if err != nil {
		t.Fatalf("ListChannelSubscribersPage() error = %v", err)
	}
	if len(uids) != 1 || uids[0] != "u2" || !done {
		t.Fatalf("uids = %#v done=%t, want only u2 and done", uids, done)
	}
	if err := node.DeleteChannelMetadata(ctx, "g1", 2); err != nil {
		t.Fatalf("DeleteChannelMetadata() error = %v", err)
	}
	_, err = node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannel(ctx, "g1", 2)
	if err == nil {
		t.Fatalf("GetChannel() error = nil, want deleted channel missing")
	}
}

func TestClusterV2SingleNodeUserMetadataFacadePersistsByUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	route := waitRouteKeyLeaderReady(t, node, "u1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := node.CreateUserMetadata(ctx, metadb.User{UID: "u1"}); err != nil {
		t.Fatalf("CreateUserMetadata() error = %v", err)
	}
	if err := node.UpsertDeviceMetadata(ctx, metadb.Device{UID: "u1", DeviceFlag: 1, Token: "token-1", DeviceLevel: 2}); err != nil {
		t.Fatalf("UpsertDeviceMetadata() error = %v", err)
	}

	gotUser, err := node.GetUserMetadata(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUserMetadata() error = %v", err)
	}
	if gotUser.UID != "u1" {
		t.Fatalf("user = %#v, want u1", gotUser)
	}
	gotDevice, err := node.GetDeviceMetadata(ctx, "u1", 1)
	if err != nil {
		t.Fatalf("GetDeviceMetadata() error = %v", err)
	}
	if gotDevice.Token != "token-1" || gotDevice.DeviceLevel != 2 {
		t.Fatalf("device = %#v, want token-1 level 2", gotDevice)
	}

	if _, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetUser(ctx, "u1"); err != nil {
		t.Fatalf("GetUser(uid hash slot) error = %v", err)
	}
}
