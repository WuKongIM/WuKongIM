package meta

import (
	"context"
	"testing"
)

func TestCMDConversationActiveReturnsOnlyCMDRowsRecentFirst(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	if err := shard.UpsertCMDConversationState(ctx, CMDConversationState{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10}); err != nil {
		t.Fatalf("UpsertCMDConversationState(g1): %v", err)
	}
	if err := shard.UpsertCMDConversationState(ctx, CMDConversationState{UID: "u1", ChannelID: "g2____cmd", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20}); err != nil {
		t.Fatalf("UpsertCMDConversationState(g2): %v", err)
	}
	if err := shard.UpsertCMDConversationState(ctx, CMDConversationState{UID: "u2", ChannelID: "g3____cmd", ChannelType: 2, ActiveAt: 500, UpdatedAt: 30}); err != nil {
		t.Fatalf("UpsertCMDConversationState(u2): %v", err)
	}
	if err := shard.UpsertUserConversationState(ctx, UserConversationState{UID: "u1", ChannelID: "g-chat", ChannelType: 2, ActiveAt: 400, UpdatedAt: 40}); err != nil {
		t.Fatalf("UpsertUserConversationState(): %v", err)
	}

	got, err := shard.ListCMDConversationActive(ctx, "u1", 10)
	if err != nil {
		t.Fatalf("ListCMDConversationActive(): %v", err)
	}
	want := []CMDConversationState{
		{UID: "u1", ChannelID: "g2____cmd", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20},
		{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
	}
	if !equalCMDConversationStates(got, want) {
		t.Fatalf("cmd active conversations = %+v, want %+v", got, want)
	}
}

func TestCMDConversationUpsertMergesMaxFieldsAndAdvanceRead(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	if err := shard.UpsertCMDConversationState(ctx, CMDConversationState{
		UID:          "u1",
		ChannelID:    "g1____cmd",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 4,
		ActiveAt:     300,
		UpdatedAt:    20,
	}); err != nil {
		t.Fatalf("UpsertCMDConversationState(base): %v", err)
	}
	if err := shard.UpsertCMDConversationState(ctx, CMDConversationState{
		UID:          "u1",
		ChannelID:    "g1____cmd",
		ChannelType:  2,
		ReadSeq:      8,
		DeletedToSeq: 7,
		ActiveAt:     100,
		UpdatedAt:    30,
	}); err != nil {
		t.Fatalf("UpsertCMDConversationState(merge): %v", err)
	}
	got, ok, err := shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
	if err != nil || !ok {
		t.Fatalf("GetCMDConversationState() ok=%v err=%v", ok, err)
	}
	want := CMDConversationState{
		UID:          "u1",
		ChannelID:    "g1____cmd",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 7,
		ActiveAt:     300,
		UpdatedAt:    30,
	}
	if got != want {
		t.Fatalf("merged cmd state = %+v, want %+v", got, want)
	}

	if err := shard.AdvanceCMDConversationReadSeq(ctx, CMDConversationReadPatch{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 9, UpdatedAt: 40}); err != nil {
		t.Fatalf("AdvanceCMDConversationReadSeq(stale): %v", err)
	}
	got, ok, err = shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
	if err != nil || !ok {
		t.Fatalf("GetCMDConversationState(stale) ok=%v err=%v", ok, err)
	}
	if got.ReadSeq != 10 || got.UpdatedAt != 30 {
		t.Fatalf("stale advance changed state = %+v", got)
	}

	if err := shard.AdvanceCMDConversationReadSeq(ctx, CMDConversationReadPatch{UID: "u1", ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 15, UpdatedAt: 50}); err != nil {
		t.Fatalf("AdvanceCMDConversationReadSeq(): %v", err)
	}
	got, ok, err = shard.GetCMDConversationState(ctx, "u1", "g1____cmd", 2)
	if err != nil || !ok {
		t.Fatalf("GetCMDConversationState(advanced) ok=%v err=%v", ok, err)
	}
	if got.ReadSeq != 15 || got.UpdatedAt != 50 {
		t.Fatalf("advanced state = %+v, want read 15 updated 50", got)
	}

	if err := shard.AdvanceCMDConversationReadSeq(ctx, CMDConversationReadPatch{UID: "u1", ChannelID: "missing____cmd", ChannelType: 2, ReadSeq: 99, UpdatedAt: 60}); err != nil {
		t.Fatalf("AdvanceCMDConversationReadSeq(missing): %v", err)
	}
	if _, ok, err := shard.GetCMDConversationState(ctx, "u1", "missing____cmd", 2); err != nil || ok {
		t.Fatalf("missing cmd state ok=%v err=%v, want no row", ok, err)
	}
}

func equalCMDConversationStates(a, b []CMDConversationState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
