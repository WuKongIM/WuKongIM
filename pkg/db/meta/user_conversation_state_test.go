package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestUserConversationActiveReturnsRecentFirst(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	states := []UserConversationState{
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
		{UID: "u1", ChannelID: "g2", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20},
		{UID: "u1", ChannelID: "g3", ChannelType: 2, ActiveAt: 0, UpdatedAt: 30},
		{UID: "u1", ChannelID: "g4", ChannelType: 2, ActiveAt: 200, UpdatedAt: 40},
		{UID: "u2", ChannelID: "g1", ChannelType: 2, ActiveAt: 500, UpdatedAt: 50},
	}
	for _, state := range states {
		if err := shard.UpsertUserConversationState(ctx, state); err != nil {
			t.Fatalf("UpsertUserConversationState(%+v): %v", state, err)
		}
	}

	got, err := shard.ListUserConversationActive(ctx, "u1", 10)
	if err != nil {
		t.Fatalf("ListUserConversationActive(): %v", err)
	}
	want := []UserConversationState{
		{UID: "u1", ChannelID: "g2", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20},
		{UID: "u1", ChannelID: "g4", ChannelType: 2, ActiveAt: 200, UpdatedAt: 40},
		{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
	}
	if !equalUserConversationStates(got, want) {
		t.Fatalf("active conversations = %+v, want %+v", got, want)
	}
}

func TestUserConversationActiveIndexKeepsLegacyLayout(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(21)
	ctx := context.Background()

	state := UserConversationState{UID: "u-layout", ChannelID: "g1", ChannelType: 2, ActiveAt: 123, UpdatedAt: 10}
	if err := shard.UpsertUserConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertUserConversationState(): %v", err)
	}

	legacyKey := encodeConversationActiveIndexKey(21, TableIDConversation, state.UID, state.ActiveAt, state.ChannelID, state.ChannelType)
	if _, ok, err := store.engine.Get(legacyKey); err != nil || !ok {
		t.Fatalf("legacy active index exists ok=%v err=%v, want ok", ok, err)
	}
	genericKey, err := encodeTableIndexKey(
		21,
		TableIDConversation,
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

func TestUserConversationActiveSkipsStaleIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(23)
	ctx := context.Background()

	state := UserConversationState{UID: "u-stale", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10}
	if err := shard.UpsertUserConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertUserConversationState(): %v", err)
	}
	batch := store.engine.NewBatch()
	defer batch.Close()
	staleKey := encodeConversationActiveIndexKey(23, TableIDConversation, state.UID, 200, state.ChannelID, state.ChannelType)
	if err := batch.Set(staleKey, nil); err != nil {
		t.Fatalf("Set(stale index): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(stale index): %v", err)
	}

	got, err := shard.ListUserConversationActive(ctx, state.UID, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActive(): %v", err)
	}
	want := []UserConversationState{state}
	if !equalUserConversationStates(got, want) {
		t.Fatalf("active conversations = %+v, want %+v", got, want)
	}
}

func TestUserConversationActiveMalformedIndexReturnsCorruptValue(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(23)
	ctx := context.Background()

	prefix := encodeConversationActiveIndexPrefix(23, TableIDConversation, "u-bad")
	badKey := append(append([]byte(nil), prefix...), 0x01)
	batch := store.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(badKey, nil); err != nil {
		t.Fatalf("Set(malformed index): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(malformed index): %v", err)
	}

	_, err := shard.ListUserConversationActive(ctx, "u-bad", 10)
	if !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("ListUserConversationActive() err = %v, want corrupt value", err)
	}
}

func TestUserConversationActiveLimitDoesNotDecodeTail(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(25)
	ctx := context.Background()

	state := UserConversationState{UID: "u-tail", ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 10}
	if err := shard.UpsertUserConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertUserConversationState(): %v", err)
	}
	malformedTail, err := encodeTableIndexScanPrefix(25, TableIDConversation, conversationActiveIndexID, KeyParts{String(state.UID), Int64Desc(100)})
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

	got, err := shard.ListUserConversationActive(ctx, state.UID, 1)
	if err != nil {
		t.Fatalf("ListUserConversationActive(): %v", err)
	}
	want := []UserConversationState{state}
	if !equalUserConversationStates(got, want) {
		t.Fatalf("active conversations = %+v, want %+v", got, want)
	}
}

func TestUserConversationStatePageMalformedRowReturnsCorruptValue(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(26)
	ctx := context.Background()

	malformed := append(encodeConversationRowPrefix(26, "u-bad-row"), 0x01)
	batch := store.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(malformed, nil); err != nil {
		t.Fatalf("Set(malformed row): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(malformed row): %v", err)
	}

	_, _, _, err := shard.ListUserConversationStatePage(ctx, "u-bad-row", ConversationCursor{}, 10)
	if !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("ListUserConversationStatePage() err = %v, want corrupt value", err)
	}
}

func TestUserConversationTouchClearHideAndPage(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	base := UserConversationState{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 5,
		ActiveAt:     100,
		UpdatedAt:    200,
	}
	if err := shard.UpsertUserConversationState(ctx, base); err != nil {
		t.Fatalf("UpsertUserConversationState(): %v", err)
	}
	if err := shard.TouchUserConversationActiveAt(ctx, UserConversationActivePatch{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    300,
		MessageSeq:  5,
	}); err != nil {
		t.Fatalf("TouchUserConversationActiveAt(deleted): %v", err)
	}
	got, ok, err := shard.GetUserConversationState(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserConversationState() ok=%v err=%v", ok, err)
	}
	if got.ActiveAt != 100 {
		t.Fatalf("ActiveAt after deleted touch = %d, want 100", got.ActiveAt)
	}

	if err := shard.TouchUserConversationActiveAt(ctx, UserConversationActivePatch{
		UID:         "u1",
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    300,
		MessageSeq:  6,
	}); err != nil {
		t.Fatalf("TouchUserConversationActiveAt(): %v", err)
	}
	got, ok, err = shard.GetUserConversationState(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserConversationState(touched) ok=%v err=%v", ok, err)
	}
	if got.ActiveAt != 300 || got.UpdatedAt != 200 || got.ReadSeq != 10 || got.DeletedToSeq != 5 {
		t.Fatalf("touched state = %+v, want active 300 while preserving other fields", got)
	}

	if err := shard.UpsertUserConversationState(ctx, UserConversationState{UID: "u1", ChannelID: "g2", ChannelType: 2, UpdatedAt: 20}); err != nil {
		t.Fatalf("UpsertUserConversationState(g2): %v", err)
	}
	page, cursor, done, err := shard.ListUserConversationStatePage(ctx, "u1", ConversationCursor{}, 1)
	if err != nil {
		t.Fatalf("ListUserConversationStatePage(): %v", err)
	}
	if done || len(page) != 1 || page[0].ChannelID != "g1" || cursor != (ConversationCursor{ChannelID: "g1", ChannelType: 2}) {
		t.Fatalf("first page=%+v cursor=%+v done=%v, want g1 and more", page, cursor, done)
	}
	page, cursor, done, err = shard.ListUserConversationStatePage(ctx, "u1", cursor, 2)
	if err != nil {
		t.Fatalf("ListUserConversationStatePage(next): %v", err)
	}
	if !done || len(page) != 1 || page[0].ChannelID != "g2" || cursor != (ConversationCursor{ChannelID: "g2", ChannelType: 2}) {
		t.Fatalf("next page=%+v cursor=%+v done=%v, want g2 final", page, cursor, done)
	}

	if err := shard.ClearUserConversationActiveAt(ctx, "u1", []ConversationKey{{ChannelID: "g1", ChannelType: 2}}); err != nil {
		t.Fatalf("ClearUserConversationActiveAt(): %v", err)
	}
	got, ok, err = shard.GetUserConversationState(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserConversationState(cleared) ok=%v err=%v", ok, err)
	}
	if got.ActiveAt != 0 || got.ReadSeq != 10 || got.DeletedToSeq != 5 || got.UpdatedAt != 200 {
		t.Fatalf("cleared state = %+v, want only active cleared", got)
	}

	if err := shard.HideUserConversation(ctx, UserConversationDelete{
		UID:          "u1",
		ChannelID:    "g1",
		ChannelType:  2,
		DeletedToSeq: 20,
		UpdatedAt:    500,
	}); err != nil {
		t.Fatalf("HideUserConversation(): %v", err)
	}
	got, ok, err = shard.GetUserConversationState(ctx, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserConversationState(hidden) ok=%v err=%v", ok, err)
	}
	if got.ActiveAt != 0 || got.DeletedToSeq != 20 || got.UpdatedAt != 500 {
		t.Fatalf("hidden state = %+v, want deleted 20 updated 500 inactive", got)
	}

	if err := shard.HideUserConversation(ctx, UserConversationDelete{UID: "u1", ChannelID: "missing", ChannelType: 2}); err != nil {
		t.Fatalf("HideUserConversation(missing zero): %v", err)
	}
	if _, ok, err := shard.GetUserConversationState(ctx, "u1", "missing", 2); err != nil || ok {
		t.Fatalf("missing hidden state ok=%v err=%v, want no row", ok, err)
	}
}

func TestUserConversationRejectsInvalidInputs(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)

	err := shard.UpsertUserConversationState(context.Background(), UserConversationState{ChannelID: "g1", ChannelType: 2})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("invalid upsert err = %v, want invalid argument", err)
	}
	_, _, _, err = shard.ListUserConversationStatePage(context.Background(), "u1", ConversationCursor{ChannelType: 2}, 1)
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("invalid cursor err = %v, want invalid argument", err)
	}
}

func equalUserConversationStates(a, b []UserConversationState) bool {
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
