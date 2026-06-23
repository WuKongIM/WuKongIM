package clusterv2

import (
	"context"
	"fmt"
	"reflect"
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

func TestClusterV2SingleNodePluginBindingFacadePersistsByUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	route := waitRouteKeyLeaderReady(t, node, "bot")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	binding := metadb.PluginUserBinding{UID: "bot", PluginNo: "receive-plugin", CreatedAtMS: 10, UpdatedAtMS: 20}
	if err := node.BindPluginUser(ctx, binding); err != nil {
		t.Fatalf("BindPluginUser() error = %v", err)
	}
	got, err := node.ListPluginBindingsByUID(ctx, "bot")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID() error = %v", err)
	}
	if len(got) != 1 || got[0] != binding {
		t.Fatalf("bindings = %#v, want %#v", got, []metadb.PluginUserBinding{binding})
	}
	direct, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListPluginBindingsByUID(ctx, "bot")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID(uid hash slot) error = %v", err)
	}
	if len(direct) != 1 || direct[0] != binding {
		t.Fatalf("direct bindings = %#v, want %#v", direct, []metadb.PluginUserBinding{binding})
	}
	if err := node.UnbindPluginUser(ctx, "bot", "receive-plugin"); err != nil {
		t.Fatalf("UnbindPluginUser() error = %v", err)
	}
	got, err = node.ListPluginBindingsByUID(ctx, "bot")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID(after unbind) error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("bindings after unbind = %#v, want empty", got)
	}
}

func TestClusterV2PluginBindingPluginNoScanRoutesToSlotLeader(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)

	pluginNo := "receive-plugin"
	uids := []string{
		pluginBindingUIDForHashSlot(t, nodes[0], 0),
		pluginBindingUIDForHashSlot(t, nodes[0], 1),
		pluginBindingUIDForHashSlot(t, nodes[0], 2),
	}
	leaderRoute := waitRouteKeyLeaderReady(t, nodes[0], uids[0])
	queryNode := firstNonLeaderNode(t, nodes, leaderRoute.Leader)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i, uid := range uids {
		binding := metadb.PluginUserBinding{
			UID:         uid,
			PluginNo:    pluginNo,
			CreatedAtMS: int64(10 + i),
			UpdatedAtMS: int64(20 + i),
		}
		if err := nodes[0].BindPluginUser(ctx, binding); err != nil {
			t.Fatalf("BindPluginUser(%s) error = %v", uid, err)
		}
	}
	if err := nodes[0].BindPluginUser(ctx, metadb.PluginUserBinding{UID: "noise-user", PluginNo: "other-plugin", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("BindPluginUser(noise) error = %v", err)
	}

	page1, cursor, hasMore, err := queryNode.ListPluginBindingsByPluginNo(ctx, pluginNo, "", 2)
	if err != nil {
		t.Fatalf("ListPluginBindingsByPluginNo(page1) error = %v", err)
	}
	if !hasMore || cursor == "" {
		t.Fatalf("page1 cursor=%q hasMore=%t, want cursor and hasMore", cursor, hasMore)
	}
	if got := pluginBindingUIDs(page1); !reflect.DeepEqual(got, uids[:2]) {
		t.Fatalf("page1 uids = %#v, want %#v", got, uids[:2])
	}

	page2, cursor, hasMore, err := queryNode.ListPluginBindingsByPluginNo(ctx, pluginNo, cursor, 2)
	if err != nil {
		t.Fatalf("ListPluginBindingsByPluginNo(page2) error = %v", err)
	}
	if hasMore || cursor != "" {
		t.Fatalf("page2 cursor=%q hasMore=%t, want final empty cursor", cursor, hasMore)
	}
	if got := pluginBindingUIDs(page2); !reflect.DeepEqual(got, uids[2:]) {
		t.Fatalf("page2 uids = %#v, want %#v", got, uids[2:])
	}
}

func pluginBindingUIDForHashSlot(t testing.TB, node *Node, want uint16) string {
	t.Helper()
	for i := 0; i < 10000; i++ {
		uid := fmt.Sprintf("plugin-binding-%02d-%04d", want, i)
		route := waitRouteKeyLeaderReady(t, node, uid)
		if route.HashSlot == want {
			return uid
		}
	}
	t.Fatalf("could not find plugin binding uid for hash slot %d", want)
	return ""
}

func pluginBindingUIDs(bindings []metadb.PluginUserBinding) []string {
	uids := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		uids = append(uids, binding.UID)
	}
	return uids
}
