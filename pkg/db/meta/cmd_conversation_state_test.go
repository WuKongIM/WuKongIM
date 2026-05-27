package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
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

func TestCMDConversationActiveIndexKeepsLegacyLayout(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(22)
	ctx := context.Background()

	state := CMDConversationState{UID: "u-layout", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 123, UpdatedAt: 10}
	if err := shard.UpsertCMDConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertCMDConversationState(): %v", err)
	}

	legacyKey := encodeConversationActiveIndexKey(22, TableIDCMDConversation, state.UID, state.ActiveAt, state.ChannelID, state.ChannelType)
	if _, ok, err := store.engine.Get(legacyKey); err != nil || !ok {
		t.Fatalf("legacy active index exists ok=%v err=%v, want ok", ok, err)
	}
	genericKey, err := encodeTableIndexKey(
		22,
		TableIDCMDConversation,
		conversationActiveIndexID,
		KeyParts{String(state.UID), Int64Desc(state.ActiveAt), String(state.ChannelID), Int64Ordered(state.ChannelType)},
		KeyParts{String(state.UID), String(state.ChannelID), Int64Ordered(state.ChannelType)},
	)
	if err != nil {
		t.Fatalf("encodeTableIndexKey(): %v", err)
	}
	if _, ok, err := store.engine.Get(genericKey); err != nil || ok {
		t.Fatalf("generic suffixed active index exists ok=%v err=%v, want missing", ok, err)
	}
}

func TestCMDConversationActiveSkipsStaleIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(22)
	ctx := context.Background()

	state := CMDConversationState{UID: "u-stale", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10}
	if err := shard.UpsertCMDConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertCMDConversationState(): %v", err)
	}
	batch := store.engine.NewBatch()
	defer batch.Close()
	staleKey := encodeConversationActiveIndexKey(22, TableIDCMDConversation, state.UID, 200, state.ChannelID, state.ChannelType)
	if err := batch.Set(staleKey, nil); err != nil {
		t.Fatalf("Set(stale index): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(stale index): %v", err)
	}

	got, err := shard.ListCMDConversationActive(ctx, state.UID, 10)
	if err != nil {
		t.Fatalf("ListCMDConversationActive(): %v", err)
	}
	want := []CMDConversationState{state}
	if !equalCMDConversationStates(got, want) {
		t.Fatalf("cmd active conversations = %+v, want %+v", got, want)
	}
}

func TestCMDConversationActiveMalformedIndexReturnsCorruptValue(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(22)
	ctx := context.Background()

	prefix := encodeConversationActiveIndexPrefix(22, TableIDCMDConversation, "u-bad")
	badKey := append(append([]byte(nil), prefix...), 0x01)
	batch := store.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(badKey, nil); err != nil {
		t.Fatalf("Set(malformed index): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(malformed index): %v", err)
	}

	_, err := shard.ListCMDConversationActive(ctx, "u-bad", 10)
	if !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("ListCMDConversationActive() err = %v, want corrupt value", err)
	}
}

func TestCMDConversationActiveLimitDoesNotDecodeTail(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(25)
	ctx := context.Background()

	state := CMDConversationState{UID: "u-tail", ChannelID: "g1____cmd", ChannelType: 2, ActiveAt: 200, UpdatedAt: 10}
	if err := shard.UpsertCMDConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertCMDConversationState(): %v", err)
	}
	malformedTail, err := encodeTableIndexScanPrefix(25, TableIDCMDConversation, conversationActiveIndexID, KeyParts{String(state.UID), Int64Desc(100)})
	if err != nil {
		t.Fatalf("encode malformed tail: %v", err)
	}
	batch := store.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(malformedTail, nil); err != nil {
		t.Fatalf("Set(malformed tail): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(malformed tail): %v", err)
	}

	got, err := shard.ListCMDConversationActive(ctx, state.UID, 1)
	if err != nil {
		t.Fatalf("ListCMDConversationActive(): %v", err)
	}
	want := []CMDConversationState{state}
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
