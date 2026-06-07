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

	if err := shard.DeleteUserChannelMembership(ctx, "u1", ConversationKey{ChannelID: "g1", ChannelType: 2}); err != nil {
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

func TestUserChannelMembershipUpsertKeepsMonotonicJoinSeqAndUpdatedAt(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(12)

	if err := shard.UpsertUserChannelMembership(ctx, UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 100, UpdatedAt: 1000,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(initial): %v", err)
	}
	if err := shard.UpsertUserChannelMembership(ctx, UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 90, UpdatedAt: 900,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(stale): %v", err)
	}

	got, ok, err := shard.GetUserChannelMembership(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserChannelMembership() ok=%v err=%v, want ok", ok, err)
	}
	if got.JoinSeq != 100 || got.UpdatedAt != 1000 {
		t.Fatalf("membership after stale upsert = %+v, want original monotonic values", got)
	}

	if err := shard.UpsertUserChannelMembership(ctx, UserChannelMembership{
		UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 110, UpdatedAt: 1100,
	}); err != nil {
		t.Fatalf("UpsertUserChannelMembership(newer): %v", err)
	}
	got, ok, err = shard.GetUserChannelMembership(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserChannelMembership(newer) ok=%v err=%v, want ok", ok, err)
	}
	if got.JoinSeq != 110 || got.UpdatedAt != 1100 {
		t.Fatalf("membership after newer upsert = %+v, want updated monotonic values", got)
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
