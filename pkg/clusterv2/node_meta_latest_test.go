package clusterv2

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterV2ChannelLatestFacadeUsesChannelHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "latest-channel"
	route := waitRouteKeyLeaderReady(t, node, channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := node.UpsertChannelLatest(ctx, metadb.ChannelLatest{
		ChannelID:      channelID,
		ChannelType:    2,
		LastMessageID:  100,
		LastMessageSeq: 10,
		LastAt:         1000,
		FromUID:        "u1",
		Payload:        []byte("hello"),
		UpdatedAt:      1001,
	}); err != nil {
		t.Fatalf("UpsertChannelLatest() error = %v", err)
	}

	got, err := node.GetChannelLatest(ctx, channelID, 2)
	if err != nil {
		t.Fatalf("GetChannelLatest() error = %v", err)
	}
	if got.LastMessageID != 100 || got.LastMessageSeq != 10 || string(got.Payload) != "hello" {
		t.Fatalf("latest = %#v, want message 100 seq 10", got)
	}

	stored, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelLatest(ctx, channelID, 2)
	if err != nil {
		t.Fatalf("GetChannelLatest(channel hash slot): %v", err)
	}
	if stored.LastMessageSeq != got.LastMessageSeq {
		t.Fatalf("stored latest seq = %d, want %d", stored.LastMessageSeq, got.LastMessageSeq)
	}
}

func TestClusterV2ChannelLatestBatchFacadeGroupsByPhysicalSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelA := channelLatestKeyForHashSlot(t, 1, 4)
	channelB := channelLatestKeyForHashSlot(t, 3, 4)
	routeA := waitRouteKeyLeaderReady(t, node, channelA)
	routeB := waitRouteKeyLeaderReady(t, node, channelB)
	if routeA.SlotID != routeB.SlotID || routeA.HashSlot == routeB.HashSlot {
		t.Fatalf("routes = %#v %#v, want different hash slots in one physical slot", routeA, routeB)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := node.UpsertChannelLatestBatch(ctx, []metadb.ChannelLatest{
		{
			ChannelID:      channelA,
			ChannelType:    2,
			LastMessageID:  101,
			LastMessageSeq: 11,
			LastAt:         1000,
			FromUID:        "u1",
			Payload:        []byte("a"),
			UpdatedAt:      1001,
		},
		{
			ChannelID:      channelB,
			ChannelType:    2,
			LastMessageID:  303,
			LastMessageSeq: 33,
			LastAt:         3000,
			FromUID:        "u3",
			Payload:        []byte("b"),
			UpdatedAt:      3001,
		},
	})
	if err != nil {
		t.Fatalf("UpsertChannelLatestBatch() error = %v", err)
	}

	gotA, err := node.defaultSlotMetaDB.ForHashSlot(routeA.HashSlot).GetChannelLatest(ctx, channelA, 2)
	if err != nil {
		t.Fatalf("GetChannelLatest(channelA) error = %v", err)
	}
	if gotA.LastMessageSeq != 11 || string(gotA.Payload) != "a" {
		t.Fatalf("channelA latest = %#v, want seq 11 payload a", gotA)
	}
	gotB, err := node.defaultSlotMetaDB.ForHashSlot(routeB.HashSlot).GetChannelLatest(ctx, channelB, 2)
	if err != nil {
		t.Fatalf("GetChannelLatest(channelB) error = %v", err)
	}
	if gotB.LastMessageSeq != 33 || string(gotB.Payload) != "b" {
		t.Fatalf("channelB latest = %#v, want seq 33 payload b", gotB)
	}
}

func TestClusterV2GetChannelLatestBatchRoutesEachChannel(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelA := channelLatestKeyForHashSlot(t, 1, 4)
	channelB := channelLatestKeyForHashSlot(t, 3, 4)
	routeA := waitRouteKeyLeaderReady(t, node, channelA)
	routeB := waitRouteKeyLeaderReady(t, node, channelB)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := node.UpsertChannelLatestBatch(ctx, []metadb.ChannelLatest{
		{
			ChannelID:      channelA,
			ChannelType:    2,
			LastMessageID:  101,
			LastMessageSeq: 11,
			LastAt:         1000,
			Payload:        []byte("a"),
			UpdatedAt:      1001,
		},
		{
			ChannelID:      channelB,
			ChannelType:    2,
			LastMessageID:  303,
			LastMessageSeq: 33,
			LastAt:         3000,
			Payload:        []byte("b"),
			UpdatedAt:      3001,
		},
	}); err != nil {
		t.Fatalf("UpsertChannelLatestBatch() error = %v", err)
	}

	got, err := node.GetChannelLatestBatch(ctx, []metadb.ConversationKey{
		{ChannelID: channelA, ChannelType: 2},
		{ChannelID: "missing-latest", ChannelType: 2},
		{ChannelID: channelB, ChannelType: 2},
	})
	if err != nil {
		t.Fatalf("GetChannelLatestBatch() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetChannelLatestBatch() returned %d rows, want 2: %#v", len(got), got)
	}
	keyA := metadb.ConversationKey{ChannelID: channelA, ChannelType: 2}
	keyB := metadb.ConversationKey{ChannelID: channelB, ChannelType: 2}
	if got[keyA].LastMessageSeq != 11 || string(got[keyA].Payload) != "a" {
		t.Fatalf("channelA latest = %#v, want seq 11 payload a", got[keyA])
	}
	if got[keyB].LastMessageSeq != 33 || string(got[keyB].Payload) != "b" {
		t.Fatalf("channelB latest = %#v, want seq 33 payload b", got[keyB])
	}

	if _, err := node.defaultSlotMetaDB.ForHashSlot(routeA.HashSlot).GetChannelLatest(ctx, channelA, 2); err != nil {
		t.Fatalf("stored channelA latest on route hash slot: %v", err)
	}
	if _, err := node.defaultSlotMetaDB.ForHashSlot(routeB.HashSlot).GetChannelLatest(ctx, channelB, 2); err != nil {
		t.Fatalf("stored channelB latest on route hash slot: %v", err)
	}
}

func channelLatestKeyForHashSlot(t *testing.T, want, count uint16) string {
	t.Helper()
	for i := 0; i < 100000; i++ {
		key := fmt.Sprintf("latest-batch-%d", i)
		if routing.HashSlotForKey(key, count) == want {
			return key
		}
	}
	t.Fatalf("no channel key found for hash slot %d/%d", want, count)
	return ""
}
