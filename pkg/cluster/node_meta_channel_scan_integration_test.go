//go:build integration

package cluster

import (
	"context"
	"reflect"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterSingleNodeScanChannelsSlotPagePaginatesMetadata(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	route := waitRouteKeyLeaderReady(t, node, "g1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := node.UpsertChannelMetadata(ctx, metadb.Channel{ChannelID: "g1", ChannelType: 2, SubscriberMutationVersion: 4}); err != nil {
		t.Fatalf("UpsertChannelMetadata(g1) error = %v", err)
	}
	if err := node.UpsertChannelMetadata(ctx, metadb.Channel{ChannelID: "g2", ChannelType: 2, Ban: 1}); err != nil {
		t.Fatalf("UpsertChannelMetadata(g2) error = %v", err)
	}

	page, cursor, done, err := node.ScanChannelsSlotPage(ctx, route.SlotID, metadb.ChannelCursor{}, 1)
	if err != nil {
		t.Fatalf("ScanChannelsSlotPage() error = %v", err)
	}
	if len(page) != 1 || page[0].ChannelID != "g1" || done {
		t.Fatalf("page1 = %#v cursor=%#v done=%t, want g1 and more", page, cursor, done)
	}
	page, _, done, err = node.ScanChannelsSlotPage(ctx, route.SlotID, cursor, 10)
	if err != nil {
		t.Fatalf("ScanChannelsSlotPage(page2) error = %v", err)
	}
	if len(page) != 1 || page[0].ChannelID != "g2" || page[0].Ban != 1 || !done {
		t.Fatalf("page2 = %#v done=%t, want g2 and done", page, done)
	}
}

func TestClusterSingleNodeScanChannelRuntimeMetaSlotPagePaginatesMetadata(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	route := waitRouteKeyLeaderReady(t, node, "g1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := defaultChannelRuntimeMetaStore{node: node}
	if err := store.UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2, Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1, Status: uint8(channelruntime.StatusActive)}); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(g1) error = %v", err)
	}
	if err := store.UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{ChannelID: "g2", ChannelType: 2, Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1, Status: uint8(channelruntime.StatusCreating)}); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(g2) error = %v", err)
	}

	page, cursor, done, err := node.ScanChannelRuntimeMetaSlotPage(ctx, route.SlotID, metadb.ChannelRuntimeMetaCursor{}, 1)
	if err != nil {
		t.Fatalf("ScanChannelRuntimeMetaSlotPage() error = %v", err)
	}
	if len(page) != 1 || page[0].ChannelID != "g1" || done {
		t.Fatalf("page1 = %#v cursor=%#v done=%t, want g1 and more", page, cursor, done)
	}
	repairPage, _, _, err := node.ListRepairScannerRuntimeMetaPage(ctx, route.SlotID, metadb.ChannelRuntimeMetaCursor{}, 1)
	if err != nil {
		t.Fatalf("ListRepairScannerRuntimeMetaPage() error = %v", err)
	}
	if len(repairPage) != 1 || repairPage[0].HashSlot != route.HashSlot || repairPage[0].Meta.ChannelID != "g1" {
		t.Fatalf("repair page = %#v, want g1 with hash slot %d", repairPage, route.HashSlot)
	}
	page, _, done, err = node.ScanChannelRuntimeMetaSlotPage(ctx, route.SlotID, cursor, 10)
	if err != nil {
		t.Fatalf("ScanChannelRuntimeMetaSlotPage(page2) error = %v", err)
	}
	if len(page) != 1 || page[0].ChannelID != "g2" || page[0].Status != uint8(channelruntime.StatusCreating) || !done {
		t.Fatalf("page2 = %#v done=%t, want g2 and done", page, done)
	}
}

func TestClusterFollowerReadsChannelRuntimeMetaFromSlotLeader(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)

	const channelID = "remote-runtime-meta"
	route := waitRouteKeyLeaderConverged(t, nodes, channelID)
	queryNode := firstNonLeaderNode(t, nodes, route.Leader)
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  1,
		ChannelEpoch: 3,
		LeaderEpoch:  2,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		Leader:       route.Leader,
		MinISR:       2,
		Status:       uint8(channelruntime.StatusActive),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := (defaultChannelRuntimeMetaStore{node: nodes[0]}).UpsertChannelRuntimeMeta(ctx, meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta() error = %v", err)
	}

	got, err := (defaultChannelRuntimeMetaStore{node: queryNode}).GetChannelRuntimeMeta(ctx, channelID, 1)
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta(follower=%d) error = %v", queryNode.NodeID(), err)
	}
	want := metadb.NormalizeChannelRuntimeMeta(meta)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetChannelRuntimeMeta(follower=%d) = %#v, want %#v", queryNode.NodeID(), got, want)
	}
}
