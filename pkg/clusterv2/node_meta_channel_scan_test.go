package clusterv2

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterV2SingleNodeScanChannelsSlotPagePaginatesMetadata(t *testing.T) {
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

func TestClusterV2SingleNodeScanChannelRuntimeMetaSlotPagePaginatesMetadata(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	route := waitRouteKeyLeaderReady(t, node, "g1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := defaultChannelRuntimeMetaStore{node: node}
	if err := store.UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2, Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1, Status: uint8(channel.StatusActive)}); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(g1) error = %v", err)
	}
	if err := store.UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{ChannelID: "g2", ChannelType: 2, Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1, Status: uint8(channel.StatusCreating)}); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta(g2) error = %v", err)
	}

	page, cursor, done, err := node.ScanChannelRuntimeMetaSlotPage(ctx, route.SlotID, metadb.ChannelRuntimeMetaCursor{}, 1)
	if err != nil {
		t.Fatalf("ScanChannelRuntimeMetaSlotPage() error = %v", err)
	}
	if len(page) != 1 || page[0].ChannelID != "g1" || done {
		t.Fatalf("page1 = %#v cursor=%#v done=%t, want g1 and more", page, cursor, done)
	}
	page, _, done, err = node.ScanChannelRuntimeMetaSlotPage(ctx, route.SlotID, cursor, 10)
	if err != nil {
		t.Fatalf("ScanChannelRuntimeMetaSlotPage(page2) error = %v", err)
	}
	if len(page) != 1 || page[0].ChannelID != "g2" || page[0].Status != uint8(channel.StatusCreating) || !done {
		t.Fatalf("page2 = %#v done=%t, want g2 and done", page, done)
	}
}
