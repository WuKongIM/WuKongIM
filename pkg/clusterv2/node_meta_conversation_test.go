package clusterv2

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/propose"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterV2UserConversationBatchFacadeRoutesByUID(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "conversation-channel"
	channelRoute := waitRouteKeyLeaderReady(t, node, channelID)
	uidA := findRouteKeyWithDifferentHashSlot(t, node, channelRoute.HashSlot, "conversation-user-a")
	routeA := waitRouteKeyLeaderReady(t, node, uidA)
	uidB := findRouteKeyWithDifferentHashSlot(t, node, routeA.HashSlot, "conversation-user-b")
	routeB := waitRouteKeyLeaderReady(t, node, uidB)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := node.UpsertUserConversationStatesBatch(ctx, []metadb.UserConversationState{
		{UID: uidA, ChannelID: channelID, ChannelType: 2, ActiveAt: 100, UpdatedAt: 101, SparseActive: true},
		{UID: uidB, ChannelID: channelID, ChannelType: 2, ActiveAt: 200, UpdatedAt: 201},
	}); err != nil {
		t.Fatalf("UpsertUserConversationStatesBatch() error = %v", err)
	}

	gotA, err := node.defaultSlotMetaDB.ForHashSlot(routeA.HashSlot).GetUserConversationState(ctx, uidA, channelID, 2)
	if err != nil {
		t.Fatalf("GetUserConversationState(uidA hash slot): %v", err)
	}
	if !gotA.SparseActive || gotA.ActiveAt != 100 {
		t.Fatalf("uidA state = %+v, want sparse active_at=100", gotA)
	}
	gotB, err := node.defaultSlotMetaDB.ForHashSlot(routeB.HashSlot).GetUserConversationState(ctx, uidB, channelID, 2)
	if err != nil {
		t.Fatalf("GetUserConversationState(uidB hash slot): %v", err)
	}
	if gotB.SparseActive || gotB.ActiveAt != 200 {
		t.Fatalf("uidB state = %+v, want dense active_at=200", gotB)
	}

	if _, err := node.defaultSlotMetaDB.ForHashSlot(channelRoute.HashSlot).GetUserConversationState(ctx, uidA, channelID, 2); err == nil && channelRoute.HashSlot != routeA.HashSlot {
		t.Fatalf("uidA conversation also appeared on channel hash slot %d", channelRoute.HashSlot)
	}
}

func TestClusterV2TouchUserConversationActiveAtBatchRoutesByUID(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	uid := conversationRouteKeyForHashSlot(t, node, 1)
	route := waitRouteKeyLeaderReady(t, node, uid)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := node.UpsertUserConversationStatesBatch(ctx, []metadb.UserConversationState{{
		UID: uid, ChannelID: "g1", ChannelType: 2, ActiveAt: 300, UpdatedAt: 1,
	}}); err != nil {
		t.Fatalf("UpsertUserConversationStatesBatch() error = %v", err)
	}
	if err := node.TouchUserConversationActiveAtBatch(ctx, []metadb.UserConversationActivePatch{{
		UID:             uid,
		ChannelID:       "g1",
		ChannelType:     2,
		ActiveAt:        100,
		MessageSeq:      1,
		SparseActive:    true,
		SparseActiveSet: true,
	}}); err != nil {
		t.Fatalf("TouchUserConversationActiveAtBatch() error = %v", err)
	}

	got, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetUserConversationState(ctx, uid, "g1", 2)
	if err != nil {
		t.Fatalf("GetUserConversationState() error = %v", err)
	}
	if got.ActiveAt != 300 || !got.SparseActive {
		t.Fatalf("state = %+v, want active_at preserved at 300 and sparse_active=true", got)
	}
}

func TestTouchUserConversationActiveAtBatchMarksProposalBackground(t *testing.T) {
	proposer := &recordingProposer{}
	node, err := New(validNodeConfig(t), WithProposer(proposer))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	hashSlotCount := node.cfg.Slots.HashSlotCount
	snapshot := control.Snapshot{
		Revision:     1,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1},
		},
		HashSlots: control.HashSlotTable{Revision: 1, Count: hashSlotCount, Ranges: []control.HashSlotRange{
			{From: 0, To: uint16(hashSlotCount - 1), SlotID: 1},
		}},
	}
	if err := node.router.UpdateControlSnapshot(snapshot); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})
	node.snapshot = Snapshot{NodeID: 1, RoutesReady: true, SlotsReady: true, ChannelsReady: true, SlotCount: 1, HashSlotCount: hashSlotCount}
	node.started.Store(true)

	err = node.TouchUserConversationActiveAtBatch(context.Background(), []metadb.UserConversationActivePatch{{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    10,
	}})
	if err != nil {
		t.Fatalf("TouchUserConversationActiveAtBatch() error = %v", err)
	}
	if got := propose.ProposalClassFromContext(proposer.ctx); got != propose.ProposalClassBackground {
		t.Fatalf("proposal class = %q, want %q", got, propose.ProposalClassBackground)
	}
}

func TestClusterV2HideUserConversationsBatchRoutesByUID(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	uid := conversationRouteKeyForHashSlot(t, node, 1)
	route := waitRouteKeyLeaderReady(t, node, uid)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := node.UpsertUserConversationStatesBatch(ctx, []metadb.UserConversationState{{
		UID: uid, ChannelID: "g1", ChannelType: 2, ActiveAt: 300, UpdatedAt: 1,
	}}); err != nil {
		t.Fatalf("UpsertUserConversationStatesBatch() error = %v", err)
	}
	if err := node.HideUserConversationsBatch(ctx, []metadb.UserConversationDelete{{
		UID: uid, ChannelID: "g1", ChannelType: 2, DeletedToSeq: 12, UpdatedAt: 2,
	}}); err != nil {
		t.Fatalf("HideUserConversationsBatch() error = %v", err)
	}

	got, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetUserConversationState(ctx, uid, "g1", 2)
	if err != nil {
		t.Fatalf("GetUserConversationState() error = %v", err)
	}
	if got.DeletedToSeq != 12 || got.ActiveAt != 0 || got.UpdatedAt != 2 {
		t.Fatalf("state = %+v, want hidden through seq 12 and inactive", got)
	}
}

func TestClusterV2GetUserConversationStateUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	ctx := context.Background()
	uid := "u-primary"
	waitRouteKeyLeaderReady(t, node, uid)
	state := metadb.UserConversationState{UID: uid, ChannelID: "g1", ChannelType: 2, ActiveAt: 100}
	if err := node.UpsertUserConversationStatesBatch(ctx, []metadb.UserConversationState{state}); err != nil {
		t.Fatalf("UpsertUserConversationStatesBatch() error = %v", err)
	}
	got, ok, err := node.GetUserConversationState(ctx, uid, "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserConversationState() ok=%v err=%v, want ok", ok, err)
	}
	if got.UID != uid || got.ChannelID != "g1" || got.ActiveAt != 100 {
		t.Fatalf("state = %#v, want %#v", got, state)
	}
}

func TestClusterV2GetUserConversationStatesUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	ctx := context.Background()
	uidA := conversationRouteKeyForHashSlot(t, node, 1)
	uidB := conversationRouteKeyForHashSlot(t, node, 2)
	waitRouteKeyLeaderReady(t, node, uidA)
	waitRouteKeyLeaderReady(t, node, uidB)
	states := []metadb.UserConversationState{
		{UID: uidA, ChannelID: "g1", ChannelType: 2, ActiveAt: 100},
		{UID: uidB, ChannelID: "g2", ChannelType: 2, ActiveAt: 200},
	}
	if err := node.UpsertUserConversationStatesBatch(ctx, states); err != nil {
		t.Fatalf("UpsertUserConversationStatesBatch() error = %v", err)
	}

	got, err := node.GetUserConversationStates(ctx, []metadb.UserConversationKey{
		{UID: uidA, ChannelID: "g1", ChannelType: 2},
		{UID: uidB, ChannelID: "g2", ChannelType: 2},
		{UID: uidA, ChannelID: "missing", ChannelType: 2},
	})
	if err != nil {
		t.Fatalf("GetUserConversationStates() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("states len = %d, want 2: %#v", len(got), got)
	}
	if got[metadb.UserConversationKey{UID: uidA, ChannelID: "g1", ChannelType: 2}].ActiveAt != 100 {
		t.Fatalf("uidA state = %#v, want active_at 100", got)
	}
	if got[metadb.UserConversationKey{UID: uidB, ChannelID: "g2", ChannelType: 2}].ActiveAt != 200 {
		t.Fatalf("uidB state = %#v, want active_at 200", got)
	}
}

func TestClusterV2ListUserConversationActivePageUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	uid := conversationRouteKeyForHashSlot(t, node, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := node.UpsertUserConversationStatesBatch(ctx, []metadb.UserConversationState{
		{UID: uid, ChannelID: "g-b", ChannelType: 2, ActiveAt: 200, UpdatedAt: 1, SparseActive: true},
		{UID: uid, ChannelID: "g-a", ChannelType: 2, ActiveAt: 300, UpdatedAt: 2},
	}); err != nil {
		t.Fatalf("UpsertUserConversationStatesBatch() error = %v", err)
	}

	page, cursor, done, err := node.ListUserConversationActivePage(ctx, uid, metadb.UserConversationActiveCursor{}, 1)
	if err != nil {
		t.Fatalf("ListUserConversationActivePage() error = %v", err)
	}
	if done || len(page) != 1 || page[0].ChannelID != "g-a" || page[0].SparseActive {
		t.Fatalf("first page = %+v done=%t, want g-a dense and more", page, done)
	}
	if cursor != (metadb.UserConversationActiveCursor{ActiveAt: 300, ChannelID: "g-a", ChannelType: 2}) {
		t.Fatalf("cursor = %+v, want g-a active cursor", cursor)
	}

	page, cursor, done, err = node.ListUserConversationActivePage(ctx, uid, cursor, 1)
	if err != nil {
		t.Fatalf("ListUserConversationActivePage(next) error = %v", err)
	}
	if !done || len(page) != 1 || page[0].ChannelID != "g-b" || !page[0].SparseActive {
		t.Fatalf("second page = %+v done=%t, want sparse g-b done", page, done)
	}
	if cursor != (metadb.UserConversationActiveCursor{ActiveAt: 200, ChannelID: "g-b", ChannelType: 2}) {
		t.Fatalf("next cursor = %+v, want g-b active cursor", cursor)
	}
}

func conversationRouteKeyForHashSlot(t testing.TB, node *Node, want uint16) string {
	t.Helper()
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("conversation-route-%d-%d", want, i)
		route := waitRouteKeyLeaderReady(t, node, key)
		if route.HashSlot == want {
			return key
		}
	}
	t.Fatalf("could not find route key for hash slot %d", want)
	return ""
}
