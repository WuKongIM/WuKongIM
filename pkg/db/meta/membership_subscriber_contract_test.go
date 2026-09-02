package meta

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSubscriberMutationMaintainsCanonicalSetAndRejectsStaleGeneration(t *testing.T) {
	db := openMembershipContractDB(t)
	ctx := context.Background()
	const hashSlot uint16 = 21
	channel := Channel{ChannelID: "subscriber-contract", ChannelType: 2}
	shard := db.ForHashSlot(hashSlot)
	if err := shard.CreateChannel(ctx, channel); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	addBatch := db.NewWriteBatch()
	added, err := addBatch.AddSubscribersCounted(hashSlot, channel.ChannelID, channel.ChannelType, []string{"zoe", "amy", "zoe"}, 5)
	if err != nil {
		t.Fatalf("stage add subscribers: %v", err)
	}
	commitMembershipContractBatch(t, addBatch)
	if added.RequestedCount != 2 || added.ChangedCount != 2 {
		t.Fatalf("add result = %+v, want requested=2 changed=2", added)
	}
	assertMembershipContractSubscribers(t, shard, channel, []string{"amy", "zoe"}, 2, 5)

	replayBatch := db.NewWriteBatch()
	replayed, err := replayBatch.AddSubscribersCounted(hashSlot, channel.ChannelID, channel.ChannelType, []string{"amy", "zoe", "amy"}, 5)
	if err != nil {
		t.Fatalf("stage exact subscriber replay: %v", err)
	}
	commitMembershipContractBatch(t, replayBatch)
	if replayed.RequestedCount != 2 || replayed.ChangedCount != 0 {
		t.Fatalf("replay result = %+v, want requested=2 changed=0", replayed)
	}

	removeBatch := db.NewWriteBatch()
	removed, err := removeBatch.RemoveSubscribersCounted(hashSlot, channel.ChannelID, channel.ChannelType, []string{"nil", "amy", "nil"}, 6)
	if err != nil {
		t.Fatalf("stage remove subscribers: %v", err)
	}
	commitMembershipContractBatch(t, removeBatch)
	if removed.RequestedCount != 2 || removed.ChangedCount != 1 {
		t.Fatalf("remove result = %+v, want requested=2 changed=1", removed)
	}
	assertMembershipContractSubscribers(t, shard, channel, []string{"zoe"}, 1, 6)

	staleBatch := db.NewWriteBatch()
	if err := staleBatch.AddSubscribers(hashSlot, channel.ChannelID, channel.ChannelType, []string{"eve"}, 5); err != nil {
		t.Fatalf("stage stale subscriber mutation: %v", err)
	}
	if err := staleBatch.Commit(); !errors.Is(err, ErrStaleMeta) {
		t.Fatalf("stale commit error = %v, want ErrStaleMeta", err)
	}
	if err := staleBatch.Close(); err != nil {
		t.Fatalf("close stale batch: %v", err)
	}
	assertMembershipContractSubscribers(t, shard, channel, []string{"zoe"}, 1, 6)
}

func TestMembershipActivationMutationReplacesDirectoryIndexAtomically(t *testing.T) {
	db := openMembershipContractDB(t)
	ctx := context.Background()
	const hashSlot uint16 = 22
	const uid = "member-1"
	shard := db.ForHashSlot(hashSlot)
	initial := []UserChannelMembership{
		{UID: uid, ChannelID: "room-1", ChannelType: 2, JoinSeq: 11, ReadSeq: 10, DeletedToSeq: 10, ActivatedAt: 100, SourceVersion: 1, UpdatedAt: 100},
		{UID: uid, ChannelID: "room-2", ChannelType: 2, JoinSeq: 21, ReadSeq: 20, DeletedToSeq: 20, ActivatedAt: 300, SourceVersion: 1, UpdatedAt: 300},
		{UID: uid, ChannelID: "room-3", ChannelType: 2, JoinSeq: 31, ReadSeq: 30, DeletedToSeq: 30, SourceVersion: 1, UpdatedAt: 50},
	}
	seed := db.NewWriteBatch()
	for _, membership := range initial {
		if err := seed.UpsertUserChannelMembership(hashSlot, membership); err != nil {
			t.Fatalf("stage membership %s: %v", membership.ChannelID, err)
		}
	}
	commitMembershipContractBatch(t, seed)

	page, cursor, done, err := shard.ListUserChannelMembershipPage(ctx, uid, UserChannelMembershipCursor{}, 2)
	if err != nil {
		t.Fatalf("list initial directory page: %v", err)
	}
	if done || len(page) != 2 || page[0].ChannelID != "room-2" || page[1].ChannelID != "room-1" {
		t.Fatalf("initial page = %+v done=%v, want [room-2 room-1] and more", page, done)
	}
	if cursor != (UserChannelMembershipCursor{ActivatedAt: 100, ChannelID: "room-1", ChannelType: 2}) {
		t.Fatalf("initial cursor = %+v, want room-1 activation position", cursor)
	}

	mutate := db.NewWriteBatch()
	if err := mutate.ActivateUserChannelMembership(hashSlot, uid, ChannelKey{ChannelID: "room-1", ChannelType: 2}, 400, 400); err != nil {
		t.Fatalf("stage membership activation: %v", err)
	}
	if err := mutate.HideUserChannelMembership(hashSlot, uid, ChannelKey{ChannelID: "room-2", ChannelType: 2}, 25, 500); err != nil {
		t.Fatalf("stage membership hide: %v", err)
	}
	commitMembershipContractBatch(t, mutate)

	page, _, done, err = shard.ListUserChannelMembershipPage(ctx, uid, UserChannelMembershipCursor{}, 10)
	if err != nil {
		t.Fatalf("list directory after activation changes: %v", err)
	}
	if !done || len(page) != 3 || page[0].ChannelID != "room-1" || page[1].ChannelID != "room-2" || page[2].ChannelID != "room-3" {
		t.Fatalf("directory after activation changes = %+v done=%v, want [room-1 room-2 room-3] without stale index", page, done)
	}
	if got := page[0]; got.ActivatedAt != 400 || got.ReadSeq != 10 || got.DeletedToSeq != 10 || got.SourceVersion != 1 || got.UpdatedAt != 400 {
		t.Fatalf("activated membership = %+v, want personal state preserved", got)
	}
	if got := page[1]; got.ActivatedAt != 0 || got.DeletedToSeq != 25 || got.ReadSeq != 20 || got.SourceVersion != 1 || got.UpdatedAt != 500 {
		t.Fatalf("hidden membership = %+v, want activation cleared and visibility advanced", got)
	}
}

func TestMembershipSourceFenceProtectsTombstoneAndResetsTrueRejoin(t *testing.T) {
	db := openMembershipContractDB(t)
	const hashSlot uint16 = 23
	key := ChannelKey{ChannelID: "room-source", ChannelType: 2}
	shard := db.ForHashSlot(hashSlot)
	initial := UserChannelMembership{
		UID: "member-source", ChannelID: key.ChannelID, ChannelType: key.ChannelType,
		JoinSeq: 11, ReadSeq: 10, DeletedToSeq: 10, ActivatedAt: 100,
		SourceVersion: 2, UpdatedAt: 100,
	}
	if err := shard.UpsertUserChannelMembership(context.Background(), initial); err != nil {
		t.Fatalf("upsert initial membership: %v", err)
	}

	personal := db.NewWriteBatch()
	if err := personal.AdvanceUserChannelMembershipReadSeq(hashSlot, initial.UID, key, 20, 200); err != nil {
		t.Fatalf("stage read floor: %v", err)
	}
	if err := personal.ActivateUserChannelMembership(hashSlot, initial.UID, key, 400, 400); err != nil {
		t.Fatalf("stage activation: %v", err)
	}
	if err := personal.HideUserChannelMembership(hashSlot, initial.UID, key, 25, 500); err != nil {
		t.Fatalf("stage visibility hide: %v", err)
	}
	commitMembershipContractBatch(t, personal)
	want := initial
	want.ReadSeq = 20
	want.DeletedToSeq = 25
	want.ActivatedAt = 0
	want.UpdatedAt = 500
	assertMembershipContractRow(t, shard, want)

	staleDelete := want
	staleDelete.Tombstone = true
	staleDelete.TombstoneAt = 600
	staleDelete.SourceVersion = 1
	staleDelete.UpdatedAt = 600
	if err := shard.UpsertUserChannelMembership(context.Background(), staleDelete); err != nil {
		t.Fatalf("upsert stale tombstone: %v", err)
	}
	assertMembershipContractRow(t, shard, want)

	remove := want
	remove.Tombstone = true
	remove.TombstoneAt = 700
	remove.SourceVersion = 3
	remove.UpdatedAt = 700
	if err := shard.UpsertUserChannelMembership(context.Background(), remove); err != nil {
		t.Fatalf("upsert authoritative tombstone: %v", err)
	}
	want.Tombstone = true
	want.TombstoneAt = 700
	want.SourceVersion = 3
	want.UpdatedAt = 700
	assertMembershipContractRow(t, shard, want)

	projected := UserChannelMembership{
		UID: initial.UID, ChannelID: key.ChannelID, ChannelType: key.ChannelType,
		JoinSeq: 51, ReadSeq: 50, DeletedToSeq: 50, SourceVersion: 4, UpdatedAt: 800,
	}
	projection := db.NewWriteBatch()
	if err := projection.EnsureUserChannelMembership(hashSlot, projected); err != nil {
		t.Fatalf("stage newer directory projection: %v", err)
	}
	commitMembershipContractBatch(t, projection)
	want.JoinSeq = 51
	want.ReadSeq = 50
	want.DeletedToSeq = 50
	want.SourceVersion = 4
	want.UpdatedAt = 800
	assertMembershipContractRow(t, shard, want)
	staleProjection := projected
	staleProjection.JoinSeq = 999
	staleProjection.ReadSeq = 999
	staleProjection.DeletedToSeq = 999
	staleProjection.SourceVersion = 3
	staleProjection.UpdatedAt = 900
	staleProjectorBatch := db.NewWriteBatch()
	if err := staleProjectorBatch.EnsureUserChannelMembership(hashSlot, staleProjection); err != nil {
		t.Fatalf("stage stale directory projection: %v", err)
	}
	commitMembershipContractBatch(t, staleProjectorBatch)
	assertMembershipContractRow(t, shard, want)

	protected := db.NewWriteBatch()
	if err := protected.AdvanceUserChannelMembershipReadSeq(hashSlot, initial.UID, key, 99, 900); err != nil {
		t.Fatalf("stage tombstoned read floor: %v", err)
	}
	if err := protected.ActivateUserChannelMembership(hashSlot, initial.UID, key, 900, 900); err != nil {
		t.Fatalf("stage tombstoned activation: %v", err)
	}
	if err := protected.HideUserChannelMembership(hashSlot, initial.UID, key, 99, 900); err != nil {
		t.Fatalf("stage tombstoned visibility hide: %v", err)
	}
	commitMembershipContractBatch(t, protected)
	assertMembershipContractRow(t, shard, want)

	rejoin := UserChannelMembership{
		UID: initial.UID, ChannelID: key.ChannelID, ChannelType: key.ChannelType,
		JoinSeq: 61, ReadSeq: 60, DeletedToSeq: 60, ActivatedAt: 0,
		SourceVersion: 5, UpdatedAt: 1000,
	}
	if err := shard.UpsertUserChannelMembership(context.Background(), rejoin); err != nil {
		t.Fatalf("upsert true rejoin: %v", err)
	}
	assertMembershipContractRow(t, shard, rejoin)
}

func openMembershipContractDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open metadata DB: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close metadata DB: %v", err)
		}
	})
	return db
}

func commitMembershipContractBatch(t *testing.T, batch *WriteBatch) {
	t.Helper()
	if err := batch.Commit(); err != nil {
		t.Fatalf("commit metadata batch: %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("close metadata batch: %v", err)
	}
}

func assertMembershipContractSubscribers(t *testing.T, shard *ShardStore, channel Channel, wantUIDs []string, wantCount, wantVersion uint64) {
	t.Helper()
	ctx := context.Background()
	uids, err := shard.ListSubscribersSnapshot(ctx, channel.ChannelID, channel.ChannelType)
	if err != nil {
		t.Fatalf("list subscribers: %v", err)
	}
	if !reflect.DeepEqual(uids, wantUIDs) {
		t.Fatalf("subscribers = %v, want %v", uids, wantUIDs)
	}
	stored, err := shard.GetChannel(ctx, channel.ChannelID, channel.ChannelType)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if stored.SubscriberCount != wantCount || stored.SubscriberMutationVersion != wantVersion {
		t.Fatalf("channel subscriber state = count %d version %d, want count %d version %d",
			stored.SubscriberCount, stored.SubscriberMutationVersion, wantCount, wantVersion)
	}
}

func assertMembershipContractRow(t *testing.T, shard *ShardStore, want UserChannelMembership) {
	t.Helper()
	got, err := shard.GetUserChannelMembership(context.Background(), want.UID, want.ChannelID, want.ChannelType)
	if err != nil {
		t.Fatalf("get membership: %v", err)
	}
	if got != want {
		t.Fatalf("membership = %+v, want %+v", got, want)
	}
}
