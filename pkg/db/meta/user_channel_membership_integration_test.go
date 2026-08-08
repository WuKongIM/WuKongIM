//go:build integration

package meta

import (
	"context"
	"errors"
	"testing"
)

func TestUserChannelMembershipUpsertGetListAndDelete(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(11)

	if err := shard.UpsertUserChannelMembership(ctx, UserChannelMembership{
		UID: "u1", ChannelID: "g2", ChannelType: 2, JoinSeq: 20, UpdatedAt: 200,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(g2): %v", err)
	}
	if err := shard.UpsertUserChannelMembership(ctx, UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 10, UpdatedAt: 100,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(g1): %v", err)
	}
	if err := shard.UpsertUserChannelMembership(ctx, UserChannelMembership{
		UID: "u2", ChannelID: "g0", ChannelType: 2, JoinSeq: 1, UpdatedAt: 10,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(u2): %v", err)
	}

	got, ok, err := shard.GetUserChannelMembership(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserChannelMembership() ok=%v err=%v, want ok", ok, err)
	}
	if got.JoinSeq != 10 || got.UpdatedAt != 100 {
		t.Fatalf("membership = %+v, want join_seq=10 updated_at=100", got)
	}

	page, cursor, done, err := shard.ListUserChannelMembershipPage(ctx, "u1", UserChannelMembershipCursor{}, 1)
	if err != nil {
		t.Fatalf("ListUserChannelMembershipPage(first): %v", err)
	}
	if done || cursor.ChannelID != "g1" || cursor.ChannelType != 2 || len(page) != 1 || page[0].ChannelID != "g1" {
		t.Fatalf("first page = %+v cursor=%+v done=%v, want g1 and more", page, cursor, done)
	}
	page, cursor, done, err = shard.ListUserChannelMembershipPage(ctx, "u1", cursor, 10)
	if err != nil {
		t.Fatalf("ListUserChannelMembershipPage(next): %v", err)
	}
	if !done || cursor.ChannelID != "g2" || cursor.ChannelType != 2 || len(page) != 1 || page[0].ChannelID != "g2" {
		t.Fatalf("next page = %+v cursor=%+v done=%v, want g2 and done", page, cursor, done)
	}

	if err := shard.DeleteUserChannelMembership(ctx, "u1", ChannelKey{ChannelID: "g1", ChannelType: 2}); err != nil {
		t.Fatalf("DeleteUserChannelMembership(): %v", err)
	}
	_, ok, err = shard.GetUserChannelMembership(ctx, "u1", "g1", 2)
	if err != nil || ok {
		t.Fatalf("GetUserChannelMembership(deleted) ok=%v err=%v, want missing", ok, err)
	}
	page, _, done, err = shard.ListUserChannelMembershipPage(ctx, "u1", UserChannelMembershipCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserChannelMembershipPage(after delete): %v", err)
	}
	if !done || len(page) != 1 || page[0].ChannelID != "g2" {
		t.Fatalf("page after delete = %+v done=%v, want only g2", page, done)
	}
}

func TestUserChannelMembershipLiveUpsertPreservesPersonalStateAndAdvancesSourceVersion(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(12)

	if err := shard.UpsertUserChannelMembership(ctx, UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 100, SourceVersion: 2, UpdatedAt: 1000,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(initial): %v", err)
	}
	if err := shard.UpsertUserChannelMembership(ctx, UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 90, SourceVersion: 1, UpdatedAt: 900,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(stale): %v", err)
	}

	got, ok, err := shard.GetUserChannelMembership(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserChannelMembership() ok=%v err=%v, want ok", ok, err)
	}
	if got.JoinSeq != 100 || got.UpdatedAt != 1000 {
		t.Fatalf("membership after stale upsert = %+v, want original state", got)
	}

	if err := shard.UpsertUserChannelMembership(ctx, UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 110, SourceVersion: 3, UpdatedAt: 1100,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(newer): %v", err)
	}
	got, ok, err = shard.GetUserChannelMembership(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserChannelMembership(newer) ok=%v err=%v, want ok", ok, err)
	}
	if got.JoinSeq != 100 || got.SourceVersion != 3 || got.UpdatedAt != 1100 {
		t.Fatalf("membership after newer live upsert = %+v, want preserved personal state and source version 3", got)
	}
}

func TestUserChannelMembershipBatchWritesMultipleHashSlots(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()

	batch := store.db.NewBatch()
	if err := batch.UpsertUserChannelMembership(9, UserChannelMembership{
		UID: "u9", ChannelID: "g1", ChannelType: 2, JoinSeq: 9, UpdatedAt: 90,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(9): %v", err)
	}
	if err := batch.UpsertUserChannelMembership(1, UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1, UpdatedAt: 10,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(1): %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if got := batch.lockedOrderForTest(); len(got) != 2 || got[0] != 1 || got[1] != 9 {
		t.Fatalf("locked order = %+v, want [1 9]", got)
	}

	if _, ok, err := store.db.HashSlot(9).GetUserChannelMembership(ctx, "u9", "g1", 2); err != nil || !ok {
		t.Fatalf("GetUserChannelMembership(9) ok=%v err=%v, want ok", ok, err)
	}
	if _, ok, err := store.db.HashSlot(1).GetUserChannelMembership(ctx, "u1", "g1", 2); err != nil || !ok {
		t.Fatalf("GetUserChannelMembership(1) ok=%v err=%v, want ok", ok, err)
	}
}

func TestUserChannelMembershipDirectoryUsesActivationOrderAndRemovesOldIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(13)

	for _, membership := range []UserChannelMembership{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 11, ReadSeq: 10, DeletedToSeq: 10, ActivatedAt: 100, SourceVersion: 1, UpdatedAt: 100},
		{UID: "u1", ChannelID: "g2", ChannelType: 2, JoinSeq: 21, ReadSeq: 20, DeletedToSeq: 20, ActivatedAt: 300, SourceVersion: 1, UpdatedAt: 300},
		{UID: "u1", ChannelID: "g3", ChannelType: 2, JoinSeq: 31, ReadSeq: 30, DeletedToSeq: 30, ActivatedAt: 0, SourceVersion: 1, UpdatedAt: 50},
	} {
		if err := shard.UpsertUserChannelMembership(ctx, membership); err != nil {
			t.Fatalf("UpsertUserChannelMembership(%s): %v", membership.ChannelID, err)
		}
	}

	page, cursor, done, err := shard.ListUserChannelMembershipPage(ctx, "u1", UserChannelMembershipCursor{}, 2)
	if err != nil {
		t.Fatalf("ListUserChannelMembershipPage(first): %v", err)
	}
	if done || len(page) != 2 || page[0].ChannelID != "g2" || page[1].ChannelID != "g1" {
		t.Fatalf("first page = %+v done=%v, want [g2 g1] and more", page, done)
	}
	if cursor.ActivatedAt != 100 || cursor.ChannelID != "g1" || cursor.ChannelType != 2 {
		t.Fatalf("first cursor = %+v, want full g1 activation position", cursor)
	}

	updated := page[1]
	if err := shard.SetUserChannelMembershipActivatedAt(ctx, updated.UID, ChannelKey{ChannelID: updated.ChannelID, ChannelType: updated.ChannelType}, 400, 400); err != nil {
		t.Fatalf("SetUserChannelMembershipActivatedAt(move activation): %v", err)
	}
	page, _, done, err = shard.ListUserChannelMembershipPage(ctx, "u1", UserChannelMembershipCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserChannelMembershipPage(after move): %v", err)
	}
	if !done || len(page) != 3 || page[0].ChannelID != "g1" || page[1].ChannelID != "g2" || page[2].ChannelID != "g3" {
		t.Fatalf("page after activation move = %+v done=%v, want [g1 g2 g3] without stale index", page, done)
	}
	if got := page[0]; got.ReadSeq != 10 || got.DeletedToSeq != 10 || got.SourceVersion != 1 || got.Tombstone {
		t.Fatalf("encoded membership fields = %+v", got)
	}
}

func TestUserChannelMembershipReducerFencesStaleAndResetsTrueRejoin(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(14)

	initial := UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 11, ReadSeq: 10, DeletedToSeq: 10, ActivatedAt: 100, SourceVersion: 2, UpdatedAt: 100}
	if err := shard.UpsertUserChannelMembership(ctx, initial); err != nil {
		t.Fatalf("UpsertUserChannelMembership(initial): %v", err)
	}
	staleDelete := initial
	staleDelete.Tombstone = true
	staleDelete.TombstoneAt = 200
	staleDelete.SourceVersion = 1
	if err := shard.UpsertUserChannelMembership(ctx, staleDelete); err != nil {
		t.Fatalf("UpsertUserChannelMembership(stale delete): %v", err)
	}
	got, ok, err := shard.GetUserChannelMembership(ctx, "u1", "g1", 2)
	if err != nil || !ok || got.Tombstone {
		t.Fatalf("membership after stale delete = (%+v, %v, %v), want live", got, ok, err)
	}

	remove := initial
	remove.Tombstone = true
	remove.TombstoneAt = 300
	remove.SourceVersion = 3
	remove.UpdatedAt = 300
	if err := shard.UpsertUserChannelMembership(ctx, remove); err != nil {
		t.Fatalf("UpsertUserChannelMembership(remove): %v", err)
	}
	rejoin := UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 51, ReadSeq: 50, DeletedToSeq: 50, SourceVersion: 4, UpdatedAt: 400}
	if err := shard.UpsertUserChannelMembership(ctx, rejoin); err != nil {
		t.Fatalf("UpsertUserChannelMembership(rejoin): %v", err)
	}
	got, ok, err = shard.GetUserChannelMembership(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserChannelMembership(rejoin) = (%+v, %v, %v)", got, ok, err)
	}
	if got.Tombstone || got.JoinSeq != 51 || got.ReadSeq != 50 || got.DeletedToSeq != 50 || got.ActivatedAt != 0 || got.SourceVersion != 4 {
		t.Fatalf("membership after true rejoin = %+v", got)
	}
}

func TestUserChannelMembershipPersonalStateIsMonotonicAndTombstoneProtected(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(17)
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	initial := UserChannelMembership{UID: "u1", ChannelID: key.ChannelID, ChannelType: key.ChannelType, JoinSeq: 11, ReadSeq: 10, DeletedToSeq: 10, SourceVersion: 1, UpdatedAt: 100}
	if err := shard.UpsertUserChannelMembership(ctx, initial); err != nil {
		t.Fatalf("UpsertUserChannelMembership(): %v", err)
	}
	if err := shard.AdvanceUserChannelMembershipReadSeq(ctx, "u1", key, 20, 200); err != nil {
		t.Fatalf("AdvanceUserChannelMembershipReadSeq(20): %v", err)
	}
	if err := shard.AdvanceUserChannelMembershipReadSeq(ctx, "u1", key, 15, 300); err != nil {
		t.Fatalf("AdvanceUserChannelMembershipReadSeq(15): %v", err)
	}
	if err := shard.SetUserChannelMembershipActivatedAt(ctx, "u1", key, 400, 400); err != nil {
		t.Fatalf("SetUserChannelMembershipActivatedAt(): %v", err)
	}
	if err := shard.HideUserChannelMembership(ctx, "u1", key, 25, 500); err != nil {
		t.Fatalf("HideUserChannelMembership(): %v", err)
	}

	got, ok, err := shard.GetUserChannelMembership(ctx, "u1", key.ChannelID, key.ChannelType)
	if err != nil || !ok || got.ReadSeq != 20 || got.DeletedToSeq != 25 || got.ActivatedAt != 0 || got.SourceVersion != 1 {
		t.Fatalf("membership after personal state = (%+v, %v, %v)", got, ok, err)
	}
	remove := got
	remove.Tombstone = true
	remove.TombstoneAt = 600
	remove.SourceVersion = 2
	remove.UpdatedAt = 600
	if err := shard.UpsertUserChannelMembership(ctx, remove); err != nil {
		t.Fatalf("UpsertUserChannelMembership(tombstone): %v", err)
	}
	if err := shard.AdvanceUserChannelMembershipReadSeq(ctx, "u1", key, 99, 700); err != nil {
		t.Fatalf("AdvanceUserChannelMembershipReadSeq(tombstone): %v", err)
	}
	if err := shard.SetUserChannelMembershipActivatedAt(ctx, "u1", key, 800, 800); err != nil {
		t.Fatalf("SetUserChannelMembershipActivatedAt(tombstone): %v", err)
	}
	got, ok, err = shard.GetUserChannelMembership(ctx, "u1", key.ChannelID, key.ChannelType)
	if err != nil || !ok || !got.Tombstone || got.ReadSeq != 20 || got.ActivatedAt != 0 {
		t.Fatalf("membership after tombstone commands = (%+v, %v, %v)", got, ok, err)
	}
}

func TestUserChannelMembershipShardStoreReturnsNotFound(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()

	_, err = db.ForHashSlot(3).GetUserChannelMembership(context.Background(), "u1", "missing", 2)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserChannelMembership() err = %v, want ErrNotFound", err)
	}
}
