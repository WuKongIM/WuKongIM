//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterUserChannelMembershipFacadeUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	observer := &recordingMembershipMutationObserver{}
	node.cfg.MembershipObserver = observer
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	channelID := "membership-channel"
	channelRoute := waitRouteKeyLeaderReady(t, node, channelID)
	uid := findRouteKeyWithDifferentHashSlot(t, node, channelRoute.HashSlot, "membership-user")
	uidRoute := waitRouteKeyLeaderReady(t, node, uid)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := node.UpsertUserChannelMemberships(ctx, channelID, 2, []string{uid}, 9, 7, 123); err != nil {
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
	public, ok, err := node.GetUserChannelMembership(ctx, uid, channelID, 2)
	if err != nil || !ok || public.JoinSeq != 10 {
		t.Fatalf("GetUserChannelMembership() = %+v ok=%v err=%v", public, ok, err)
	}
	if err := node.ActivateUserChannelMembership(ctx, uid, channelID, 2, 200, 200); err != nil {
		t.Fatalf("ActivateUserChannelMembership() error = %v", err)
	}
	if err := node.AdvanceUserChannelMembershipReadSeq(ctx, uid, channelID, 2, 11, 210); err != nil {
		t.Fatalf("AdvanceUserChannelMembershipReadSeq() error = %v", err)
	}
	if err := node.HideUserChannelMembership(ctx, uid, channelID, 2, 12, 220); err != nil {
		t.Fatalf("HideUserChannelMembership() error = %v", err)
	}
	got, err = node.defaultSlotMetaDB.ForHashSlot(uidRoute.HashSlot).GetUserChannelMembership(ctx, uid, channelID, 2)
	if err != nil || got.ReadSeq != 11 || got.DeletedToSeq != 12 || got.ActivatedAt != 0 {
		t.Fatalf("membership after personal mutations = %+v err=%v", got, err)
	}

	if err := node.TombstoneUserChannelMemberships(ctx, channelID, 2, []string{uid}, 8, 456); err != nil {
		t.Fatalf("TombstoneUserChannelMemberships() error = %v", err)
	}
	got, err = node.defaultSlotMetaDB.ForHashSlot(uidRoute.HashSlot).GetUserChannelMembership(ctx, uid, channelID, 2)
	if err != nil || !got.Tombstone || got.SourceVersion != 8 {
		t.Fatalf("GetUserChannelMembership(after tombstone) = %+v err=%v", got, err)
	}
	if got := observer.totalRows("ordinary"); got != 5 {
		t.Fatalf("ordinary membership proposal rows = %d, want 5", got)
	}
}

func TestClusterUserCMDChannelMembershipFacadeUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	observer := &recordingMembershipMutationObserver{}
	node.cfg.MembershipObserver = observer
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	uid := "cmd-membership-user"
	waitRouteKeyLeaderReady(t, node, uid)
	row := metadb.UserCMDChannelMembership{
		UID: uid, CommandChannelID: "g1____cmd", ChannelType: 2, StartSeq: 4, UpdatedAt: 100,
	}
	if err := node.UpsertUserCMDChannelMemberships(ctx, []metadb.UserCMDChannelMembership{row}); err != nil {
		t.Fatalf("UpsertUserCMDChannelMemberships() error = %v", err)
	}
	rows, cursor, done, err := node.ListUserCMDChannelMembershipPage(ctx, uid, metadb.UserCMDChannelMembershipCursor{}, 10)
	if err != nil || !done || len(rows) != 1 || rows[0].StartSeq != 4 || cursor.CommandChannelID != row.CommandChannelID {
		t.Fatalf("ListUserCMDChannelMembershipPage() rows=%+v cursor=%+v done=%v err=%v", rows, cursor, done, err)
	}
	row.AckSeq, row.UpdatedAt = 8, 120
	if err := node.AdvanceUserCMDChannelMembershipAcks(ctx, []metadb.UserCMDChannelMembership{row}); err != nil {
		t.Fatalf("AdvanceUserCMDChannelMembershipAcks() error = %v", err)
	}
	row.Tombstone, row.TombstoneAt, row.UpdatedAt = true, 140, 140
	if err := node.TombstoneUserCMDChannelMemberships(ctx, []metadb.UserCMDChannelMembership{row}); err != nil {
		t.Fatalf("TombstoneUserCMDChannelMemberships() error = %v", err)
	}
	route, err := node.RouteKey(uid)
	if err != nil {
		t.Fatalf("RouteKey() error = %v", err)
	}
	got, ok, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetUserCMDChannelMembership(ctx, uid, row.CommandChannelID, row.ChannelType)
	if err != nil || !ok || got.AckSeq != 8 || !got.Tombstone {
		t.Fatalf("GetUserCMDChannelMembership() = %+v ok=%v err=%v", got, ok, err)
	}
	if got := observer.totalRows("cmd"); got != 3 {
		t.Fatalf("CMD membership proposal rows = %d, want 3", got)
	}
}

type recordingMembershipMutationObserver struct {
	events []MembershipMutationObservation
}

func (o *recordingMembershipMutationObserver) ObserveMembershipMutation(event MembershipMutationObservation) {
	o.events = append(o.events, event)
}

func (o *recordingMembershipMutationObserver) totalRows(directory string) int {
	total := 0
	for _, event := range o.events {
		if event.Directory == directory {
			total += event.Rows
		}
	}
	return total
}

func TestClusterEnsuresPersonChannelDirectoryReady(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	waitChannelDataNode(t, node, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	channelID := "person-a@person-b"
	waitRouteKeyLeaderReady(t, node, channelID)
	if err := node.EnsureChannelDirectoryReady(ctx, channelID, 1); err != nil {
		t.Fatalf("EnsureChannelDirectoryReady() error = %v", err)
	}
	got, err := node.GetChannelMetadataAuthoritative(ctx, channelID, 1)
	if err != nil || got.DirectoryReady != 1 {
		t.Fatalf("GetChannelMetadataAuthoritative() = %+v err=%v", got, err)
	}
}

func TestClusterPreparesPersonDirectoryRuntimeMetaBeforePublishingReady(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	waitChannelDataNode(t, node, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	left, right := "person-prepare-a", "person-prepare-b"
	channelID := left + "@" + right
	for _, key := range []string{left, right, channelID} {
		waitRouteKeyLeaderReady(t, node, key)
	}
	memberships := []metadb.UserChannelMembership{
		{UID: left, ChannelID: channelID, ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 1},
		{UID: right, ChannelID: channelID, ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 1},
	}
	keys := []metadb.ChannelKey{{ChannelID: channelID, ChannelType: 1}}
	if err := node.PreparePersonChannelDirectoryBatch(ctx, memberships, keys); err != nil {
		t.Fatalf("PreparePersonChannelDirectoryBatch() error = %v", err)
	}
	for _, uid := range []string{left, right} {
		membership, ok, err := node.GetUserChannelMembership(ctx, uid, channelID, 1)
		if err != nil || !ok || membership.UID != uid {
			t.Fatalf("GetUserChannelMembership(%s) = %#v ok=%v err=%v", uid, membership, ok, err)
		}
	}
	runtimeMeta, err := node.GetChannelRuntimeMeta(ctx, channelID, 1)
	if err != nil || runtimeMeta.ChannelID != channelID || runtimeMeta.Leader == 0 || len(runtimeMeta.Replicas) == 0 {
		t.Fatalf("GetChannelRuntimeMeta() = %#v err=%v", runtimeMeta, err)
	}
	if channel, err := node.GetChannelMetadataAuthoritative(ctx, channelID, 1); err == nil && channel.DirectoryReady != 0 {
		t.Fatalf("directory became ready during prepare: %#v", channel)
	}
	if err := node.EnsureChannelDirectoriesReady(ctx, keys); err != nil {
		t.Fatalf("EnsureChannelDirectoriesReady() error = %v", err)
	}
	channel, err := node.GetChannelMetadataAuthoritative(ctx, channelID, 1)
	if err != nil || channel.DirectoryReady != 1 {
		t.Fatalf("GetChannelMetadataAuthoritative() = %#v err=%v, want ready", channel, err)
	}
}
