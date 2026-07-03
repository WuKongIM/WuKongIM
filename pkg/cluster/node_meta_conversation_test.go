package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterConversationBatchFacadeRoutesByUID(t *testing.T) {
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
	if err := node.UpsertConversationStatesBatch(ctx, []metadb.ConversationState{
		{UID: uidA, Kind: metadb.ConversationKindNormal, ChannelID: channelID, ChannelType: 2, ActiveAt: 100, UpdatedAt: 101, SparseActive: true},
		{UID: uidB, Kind: metadb.ConversationKindNormal, ChannelID: channelID, ChannelType: 2, ActiveAt: 200, UpdatedAt: 201},
	}); err != nil {
		t.Fatalf("UpsertConversationStatesBatch() error = %v", err)
	}

	gotA, err := node.defaultSlotMetaDB.ForHashSlot(routeA.HashSlot).GetConversationState(ctx, metadb.ConversationKindNormal, uidA, channelID, 2)
	if err != nil {
		t.Fatalf("GetConversationState(uidA hash slot): %v", err)
	}
	if !gotA.SparseActive || gotA.ActiveAt != 100 {
		t.Fatalf("uidA state = %+v, want sparse active_at=100", gotA)
	}
	gotB, err := node.defaultSlotMetaDB.ForHashSlot(routeB.HashSlot).GetConversationState(ctx, metadb.ConversationKindNormal, uidB, channelID, 2)
	if err != nil {
		t.Fatalf("GetConversationState(uidB hash slot): %v", err)
	}
	if gotB.SparseActive || gotB.ActiveAt != 200 {
		t.Fatalf("uidB state = %+v, want dense active_at=200", gotB)
	}

	if _, err := node.defaultSlotMetaDB.ForHashSlot(channelRoute.HashSlot).GetConversationState(ctx, metadb.ConversationKindNormal, uidA, channelID, 2); err == nil && channelRoute.HashSlot != routeA.HashSlot {
		t.Fatalf("uidA conversation also appeared on channel hash slot %d", channelRoute.HashSlot)
	}
}

func TestClusterConversationBatchFacadeRoutesByUIDAndKind(t *testing.T) {
	node := newTestNodeWithMeta(t)
	ctx := context.Background()

	uid := uidForHashSlot(t, node.cfg.Slots.HashSlotCount, 5)
	if err := node.UpsertConversationStatesBatch(ctx, []metadb.ConversationState{
		{UID: uid, Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100},
		{UID: uid, Kind: metadb.ConversationKindCMD, ChannelID: "g1", ChannelType: 2, ActiveAt: 300},
	}); err != nil {
		t.Fatalf("UpsertConversationStatesBatch(): %v", err)
	}

	normalPage, _, done, err := node.ListConversationActivePage(ctx, metadb.ConversationKindNormal, uid, metadb.ConversationActiveCursor{}, 10)
	if err != nil || !done {
		t.Fatalf("ListConversationActivePage(normal) done=%v err=%v", done, err)
	}
	cmdPage, _, done, err := node.ListConversationActivePage(ctx, metadb.ConversationKindCMD, uid, metadb.ConversationActiveCursor{}, 10)
	if err != nil || !done {
		t.Fatalf("ListConversationActivePage(cmd) done=%v err=%v", done, err)
	}
	if len(normalPage) != 1 || normalPage[0].Kind != metadb.ConversationKindNormal || len(cmdPage) != 1 || cmdPage[0].Kind != metadb.ConversationKindCMD {
		t.Fatalf("pages = normal:%+v cmd:%+v", normalPage, cmdPage)
	}
}

func TestClusterTouchConversationActiveAtBatchRoutesByUID(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	uid := conversationRouteKeyForHashSlot(t, node, 1)
	route := waitRouteKeyLeaderReady(t, node, uid)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := node.UpsertConversationStatesBatch(ctx, []metadb.ConversationState{{
		UID: uid, Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 300, UpdatedAt: 1,
	}}); err != nil {
		t.Fatalf("UpsertConversationStatesBatch() error = %v", err)
	}
	if err := node.TouchConversationActiveAtBatch(ctx, []metadb.ConversationActivePatch{{
		UID:             uid,
		Kind:            metadb.ConversationKindNormal,
		ChannelID:       "g1",
		ChannelType:     2,
		ActiveAt:        100,
		MessageSeq:      1,
		SparseActive:    true,
		SparseActiveSet: true,
	}}); err != nil {
		t.Fatalf("TouchConversationActiveAtBatch() error = %v", err)
	}

	got, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetConversationState(ctx, metadb.ConversationKindNormal, uid, "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState() error = %v", err)
	}
	if got.ActiveAt != 300 || !got.SparseActive {
		t.Fatalf("state = %+v, want active_at preserved at 300 and sparse_active=true", got)
	}
}

func TestTouchConversationActiveAtBatchMarksProposalBackground(t *testing.T) {
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

	err = node.TouchConversationActiveAtBatch(context.Background(), []metadb.ConversationActivePatch{{
		UID:         "u1",
		Kind:        metadb.ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    10,
	}})
	if err != nil {
		t.Fatalf("TouchConversationActiveAtBatch() error = %v", err)
	}
	if got := propose.ProposalClassFromContext(proposer.ctx); got != propose.ProposalClassBackground {
		t.Fatalf("proposal class = %q, want %q", got, propose.ProposalClassBackground)
	}
}

func TestClusterHideConversationsBatchRoutesByUID(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	uid := conversationRouteKeyForHashSlot(t, node, 1)
	route := waitRouteKeyLeaderReady(t, node, uid)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := node.UpsertConversationStatesBatch(ctx, []metadb.ConversationState{{
		UID: uid, Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 300, UpdatedAt: 1,
	}}); err != nil {
		t.Fatalf("UpsertConversationStatesBatch() error = %v", err)
	}
	if err := node.HideConversationsBatch(ctx, []metadb.ConversationDelete{{
		UID: uid, Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, DeletedToSeq: 12, UpdatedAt: 2,
	}}); err != nil {
		t.Fatalf("HideConversationsBatch() error = %v", err)
	}

	got, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetConversationState(ctx, metadb.ConversationKindNormal, uid, "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState() error = %v", err)
	}
	if got.DeletedToSeq != 12 || got.ActiveAt != 0 || got.UpdatedAt != 2 {
		t.Fatalf("state = %+v, want hidden through seq 12 and inactive", got)
	}
}

func TestClusterGetConversationStateUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	ctx := context.Background()
	uid := "u-primary"
	waitRouteKeyLeaderReady(t, node, uid)
	state := metadb.ConversationState{UID: uid, Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100}
	if err := node.UpsertConversationStatesBatch(ctx, []metadb.ConversationState{state}); err != nil {
		t.Fatalf("UpsertConversationStatesBatch() error = %v", err)
	}
	got, ok, err := node.GetConversationState(ctx, metadb.ConversationKindNormal, uid, "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState() ok=%v err=%v, want ok", ok, err)
	}
	if got.UID != uid || got.ChannelID != "g1" || got.ActiveAt != 100 {
		t.Fatalf("state = %#v, want %#v", got, state)
	}
}

func TestClusterGetConversationStatesUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	ctx := context.Background()
	uidA := conversationRouteKeyForHashSlot(t, node, 1)
	uidB := conversationRouteKeyForHashSlot(t, node, 2)
	waitRouteKeyLeaderReady(t, node, uidA)
	waitRouteKeyLeaderReady(t, node, uidB)
	states := []metadb.ConversationState{
		{UID: uidA, Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100},
		{UID: uidB, Kind: metadb.ConversationKindNormal, ChannelID: "g2", ChannelType: 2, ActiveAt: 200},
	}
	if err := node.UpsertConversationStatesBatch(ctx, states); err != nil {
		t.Fatalf("UpsertConversationStatesBatch() error = %v", err)
	}

	got, err := node.GetConversationStates(ctx, []metadb.ConversationStateKey{
		{UID: uidA, Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2},
		{UID: uidB, Kind: metadb.ConversationKindNormal, ChannelID: "g2", ChannelType: 2},
		{UID: uidA, Kind: metadb.ConversationKindNormal, ChannelID: "missing", ChannelType: 2},
	})
	if err != nil {
		t.Fatalf("GetConversationStates() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("states len = %d, want 2: %#v", len(got), got)
	}
	if got[metadb.ConversationStateKey{UID: uidA, Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2}].ActiveAt != 100 {
		t.Fatalf("uidA state = %#v, want active_at 100", got)
	}
	if got[metadb.ConversationStateKey{UID: uidB, Kind: metadb.ConversationKindNormal, ChannelID: "g2", ChannelType: 2}].ActiveAt != 200 {
		t.Fatalf("uidB state = %#v, want active_at 200", got)
	}
}

func TestClusterListConversationActivePageUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	uid := conversationRouteKeyForHashSlot(t, node, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := node.UpsertConversationStatesBatch(ctx, []metadb.ConversationState{
		{UID: uid, Kind: metadb.ConversationKindNormal, ChannelID: "g-b", ChannelType: 2, ActiveAt: 200, UpdatedAt: 1, SparseActive: true},
		{UID: uid, Kind: metadb.ConversationKindNormal, ChannelID: "g-a", ChannelType: 2, ActiveAt: 300, UpdatedAt: 2},
	}); err != nil {
		t.Fatalf("UpsertConversationStatesBatch() error = %v", err)
	}

	page, cursor, done, err := node.ListConversationActivePage(ctx, metadb.ConversationKindNormal, uid, metadb.ConversationActiveCursor{}, 1)
	if err != nil {
		t.Fatalf("ListConversationActivePage() error = %v", err)
	}
	if done || len(page) != 1 || page[0].ChannelID != "g-a" || page[0].SparseActive {
		t.Fatalf("first page = %+v done=%t, want g-a dense and more", page, done)
	}
	if cursor != (metadb.ConversationActiveCursor{ActiveAt: 300, ChannelID: "g-a", ChannelType: 2}) {
		t.Fatalf("cursor = %+v, want g-a active cursor", cursor)
	}

	page, cursor, done, err = node.ListConversationActivePage(ctx, metadb.ConversationKindNormal, uid, cursor, 1)
	if err != nil {
		t.Fatalf("ListConversationActivePage(next) error = %v", err)
	}
	if !done || len(page) != 1 || page[0].ChannelID != "g-b" || !page[0].SparseActive {
		t.Fatalf("second page = %+v done=%t, want sparse g-b done", page, done)
	}
	if cursor != (metadb.ConversationActiveCursor{ActiveAt: 200, ChannelID: "g-b", ChannelType: 2}) {
		t.Fatalf("next cursor = %+v, want g-b active cursor", cursor)
	}
}

func newTestNodeWithMeta(t testing.TB) *Node {
	t.Helper()
	cfg := Config{NodeID: 1, ListenAddr: freeTCPAddr(t.(*testing.T)), DataDir: t.TempDir()}
	cfg.Control.ClusterID = "cluster-conversation-meta"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 8
	cfg.Slots.ReplicaCount = 1
	cfg.Channel.TickInterval = time.Millisecond
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New(conversation meta node) error = %v", err)
	}
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	waitUntil(t.(*testing.T), func() bool {
		for hashSlot := uint16(0); hashSlot < cfg.Slots.HashSlotCount; hashSlot++ {
			route, err := node.RouteHashSlot(hashSlot)
			if err != nil || route.Leader == 0 {
				return false
			}
		}
		return true
	})
	return node
}

func uidForHashSlot(t testing.TB, count, want uint16) string {
	t.Helper()
	for i := 0; i < 10000; i++ {
		uid := fmt.Sprintf("conversation-hash-%d-%d", want, i)
		if routing.HashSlotForKey(uid, count) == want {
			return uid
		}
	}
	t.Fatalf("could not find uid for hash slot %d/%d", want, count)
	return ""
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
