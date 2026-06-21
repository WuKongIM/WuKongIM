package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestConversationKindIsPartOfPrimaryKey(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	normal := ConversationState{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ReadSeq: 3, ActiveAt: 100, UpdatedAt: 100}
	cmd := ConversationState{UID: "u1", Kind: ConversationKindCMD, ChannelID: "g1", ChannelType: 2, ReadSeq: 9, ActiveAt: 200, UpdatedAt: 200}
	if err := shard.UpsertConversationState(ctx, normal); err != nil {
		t.Fatalf("UpsertConversationState(normal): %v", err)
	}
	if err := shard.UpsertConversationState(ctx, cmd); err != nil {
		t.Fatalf("UpsertConversationState(cmd): %v", err)
	}

	gotNormal, ok, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(normal) ok=%v err=%v", ok, err)
	}
	gotCMD, ok, err := shard.GetConversationState(ctx, ConversationKindCMD, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(cmd) ok=%v err=%v", ok, err)
	}
	if gotNormal.ReadSeq != 3 || gotCMD.ReadSeq != 9 {
		t.Fatalf("states share primary key: normal=%+v cmd=%+v", gotNormal, gotCMD)
	}
}

func TestConversationActivePageScansOnlyRequestedKind(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	rows := []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "normal-a", ChannelType: 2, ActiveAt: 100},
		{UID: "u1", Kind: ConversationKindCMD, ChannelID: "cmd-a____cmd", ChannelType: 2, ActiveAt: 300},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "normal-b", ChannelType: 2, ActiveAt: 200},
	}
	for _, row := range rows {
		if err := shard.UpsertConversationState(ctx, row); err != nil {
			t.Fatalf("UpsertConversationState(%+v): %v", row, err)
		}
	}

	got, cursor, done, err := shard.ListConversationActivePage(ctx, ConversationKindNormal, "u1", ConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListConversationActivePage(normal): %v", err)
	}
	want := []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "normal-b", ChannelType: 2, ActiveAt: 200},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "normal-a", ChannelType: 2, ActiveAt: 100},
	}
	if !equalConversationStates(got, want) || !done || cursor.ChannelID != "normal-a" {
		t.Fatalf("normal active page = %+v cursor=%+v done=%v, want %+v done", got, cursor, done, want)
	}
}

func TestConversationTouchAndHideAreKindIsolated(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	for _, kind := range []ConversationKind{ConversationKindNormal, ConversationKindCMD} {
		if err := shard.UpsertConversationState(ctx, ConversationState{UID: "u1", Kind: kind, ChannelID: "same", ChannelType: 2, ReadSeq: 1, ActiveAt: 10}); err != nil {
			t.Fatalf("UpsertConversationState(%d): %v", kind, err)
		}
	}
	if err := shard.TouchConversationActiveAt(ctx, ConversationActivePatch{UID: "u1", Kind: ConversationKindCMD, ChannelID: "same", ChannelType: 2, ReadSeq: 8, ActiveAt: 80, UpdatedAt: 80}); err != nil {
		t.Fatalf("TouchConversationActiveAt(cmd): %v", err)
	}
	if err := shard.HideConversation(ctx, ConversationDelete{UID: "u1", Kind: ConversationKindNormal, ChannelID: "same", ChannelType: 2, DeletedToSeq: 5, UpdatedAt: 90}); err != nil {
		t.Fatalf("HideConversation(normal): %v", err)
	}
	normal, _, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "same", 2)
	if err != nil {
		t.Fatalf("GetConversationState(normal): %v", err)
	}
	cmd, _, err := shard.GetConversationState(ctx, ConversationKindCMD, "u1", "same", 2)
	if err != nil {
		t.Fatalf("GetConversationState(cmd): %v", err)
	}
	if normal.DeletedToSeq != 5 || normal.ReadSeq != 1 || cmd.ReadSeq != 8 || cmd.DeletedToSeq != 0 {
		t.Fatalf("kind isolation failed: normal=%+v cmd=%+v", normal, cmd)
	}
}

func TestUserConversationActiveReturnsLatestFirst(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	states := []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g2", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g3", ChannelType: 2, ActiveAt: 0, UpdatedAt: 30},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g4", ChannelType: 2, ActiveAt: 200, UpdatedAt: 40},
		{UID: "u2", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 500, UpdatedAt: 50},
	}
	for _, state := range states {
		if err := shard.UpsertConversationState(ctx, state); err != nil {
			t.Fatalf("UpsertConversationState(%+v): %v", state, err)
		}
	}

	got, err := shard.ListConversationActive(ctx, ConversationKindNormal, "u1", 10)
	if err != nil {
		t.Fatalf("ListConversationActive(): %v", err)
	}
	want := []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g2", ChannelType: 2, ActiveAt: 300, UpdatedAt: 20},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g4", ChannelType: 2, ActiveAt: 200, UpdatedAt: 40},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10},
	}
	if !equalConversationStates(got, want) {
		t.Fatalf("active conversations = %+v, want %+v", got, want)
	}
}

func TestConversationStateSparseActiveRoundTrip(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	state := ConversationState{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 101, SparseActive: true}
	if err := shard.UpsertConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertConversationState(): %v", err)
	}
	got, ok, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState() ok=%v err=%v, want ok", ok, err)
	}
	if !got.SparseActive {
		t.Fatalf("SparseActive = false, want true: %+v", got)
	}
}

func TestConversationStateOldValueDecodesSparseInactive(t *testing.T) {
	value := encodeConversationValue(11, 7, 100, 101)
	got, err := decodeConversationStateValue("u1", ConversationKindNormal, "g1", 2, value)
	if err != nil {
		t.Fatalf("decodeConversationStateValue(old value): %v", err)
	}
	if got.SparseActive {
		t.Fatalf("SparseActive = true, want false for old value: %+v", got)
	}
}

func TestUserConversationActivePageUsesCursorAndTies(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	for _, state := range []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-z", ChannelType: 2, ActiveAt: 300},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-a", ChannelType: 2, ActiveAt: 200},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-b", ChannelType: 1, ActiveAt: 200},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-b", ChannelType: 2, ActiveAt: 200},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-c", ChannelType: 2, ActiveAt: 100},
		{UID: "u2", Kind: ConversationKindNormal, ChannelID: "g-other", ChannelType: 2, ActiveAt: 500},
	} {
		if err := shard.UpsertConversationState(ctx, state); err != nil {
			t.Fatalf("UpsertConversationState(%+v): %v", state, err)
		}
	}

	page, cursor, done, err := shard.ListConversationActivePage(ctx, ConversationKindNormal, "u1", ConversationActiveCursor{}, 3)
	if err != nil {
		t.Fatalf("ListConversationActivePage(): %v", err)
	}
	wantFirst := []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-z", ChannelType: 2, ActiveAt: 300},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-a", ChannelType: 2, ActiveAt: 200},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-b", ChannelType: 1, ActiveAt: 200},
	}
	if done || !equalConversationStates(page, wantFirst) || cursor != (ConversationActiveCursor{ActiveAt: 200, ChannelID: "g-b", ChannelType: 1}) {
		t.Fatalf("first page=%+v cursor=%+v done=%v, want first ordered page", page, cursor, done)
	}

	page, cursor, done, err = shard.ListConversationActivePage(ctx, ConversationKindNormal, "u1", cursor, 3)
	if err != nil {
		t.Fatalf("ListConversationActivePage(next): %v", err)
	}
	wantSecond := []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-b", ChannelType: 2, ActiveAt: 200},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-c", ChannelType: 2, ActiveAt: 100},
	}
	if !done || !equalConversationStates(page, wantSecond) || cursor != (ConversationActiveCursor{ActiveAt: 100, ChannelID: "g-c", ChannelType: 2}) {
		t.Fatalf("second page=%+v cursor=%+v done=%v, want final ordered page", page, cursor, done)
	}
}

func TestUserConversationActiveIndexKeepsCustomLayout(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(21)
	ctx := context.Background()

	state := ConversationState{UID: "u-layout", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 123, UpdatedAt: 10}
	if err := shard.UpsertConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertConversationState(): %v", err)
	}

	activeKey := encodeConversationActiveIndexKey(21, state.UID, state.Kind, state.ActiveAt, state.ChannelID, state.ChannelType)
	if _, ok, err := store.engine.Get(activeKey); err != nil || !ok {
		t.Fatalf("custom active index exists ok=%v err=%v, want ok", ok, err)
	}
	genericKey, err := encodeTableIndexKey(
		21,
		TableIDConversation,
		conversationActiveIndexID,
		KeyParts{String(state.UID), Uint64(uint64(state.Kind)), Int64Desc(state.ActiveAt), String(state.ChannelID), Int64Ordered(state.ChannelType)},
		KeyParts{String(state.UID), Uint64(uint64(state.Kind)), String(state.ChannelID), Int64Ordered(state.ChannelType)},
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

	state := ConversationState{UID: "u-stale", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 10}
	if err := shard.UpsertConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertConversationState(): %v", err)
	}
	batch := store.engine.NewBatch()
	defer batch.Close()
	staleKey := encodeConversationActiveIndexKey(23, state.UID, state.Kind, 200, state.ChannelID, state.ChannelType)
	if err := batch.Set(staleKey, nil); err != nil {
		t.Fatalf("Set(stale index): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(stale index): %v", err)
	}

	got, err := shard.ListConversationActive(ctx, ConversationKindNormal, state.UID, 10)
	if err != nil {
		t.Fatalf("ListConversationActive(): %v", err)
	}
	want := []ConversationState{state}
	if !equalConversationStates(got, want) {
		t.Fatalf("active conversations = %+v, want %+v", got, want)
	}
}

func TestUserConversationActiveMalformedIndexReturnsCorruptValue(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(23)
	ctx := context.Background()

	prefix := encodeConversationActiveIndexPrefix(23, "u-bad", ConversationKindNormal)
	badKey := append(append([]byte(nil), prefix...), 0x01)
	batch := store.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(badKey, nil); err != nil {
		t.Fatalf("Set(malformed index): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(malformed index): %v", err)
	}

	_, err := shard.ListConversationActive(ctx, ConversationKindNormal, "u-bad", 10)
	if !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("ListConversationActive() err = %v, want corrupt value", err)
	}
}

func TestUserConversationActiveLimitDoesNotDecodeTail(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(25)
	ctx := context.Background()

	state := ConversationState{UID: "u-tail", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 200, UpdatedAt: 10}
	if err := shard.UpsertConversationState(ctx, state); err != nil {
		t.Fatalf("UpsertConversationState(): %v", err)
	}
	malformedTail, err := encodeTableIndexScanPrefix(25, TableIDConversation, conversationActiveIndexID, KeyParts{String(state.UID), Uint64(uint64(state.Kind)), Int64Desc(100)})
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

	got, err := shard.ListConversationActive(ctx, ConversationKindNormal, state.UID, 1)
	if err != nil {
		t.Fatalf("ListConversationActive(): %v", err)
	}
	want := []ConversationState{state}
	if !equalConversationStates(got, want) {
		t.Fatalf("active conversations = %+v, want %+v", got, want)
	}
}

func TestConversationStatePageMalformedRowReturnsCorruptValue(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(26)
	ctx := context.Background()

	malformed := append(encodeConversationRowPrefix(26, "u-bad-row", ConversationKindNormal), 0x01)
	batch := store.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(malformed, nil); err != nil {
		t.Fatalf("Set(malformed row): %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("Commit(malformed row): %v", err)
	}

	_, _, _, err := shard.ListConversationStatePage(ctx, ConversationKindNormal, "u-bad-row", ConversationCursor{}, 10)
	if !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("ListConversationStatePage() err = %v, want corrupt value", err)
	}
}

func TestUserConversationTouchClearHideAndPage(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	base := ConversationState{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 5,
		ActiveAt:     100,
		UpdatedAt:    200,
	}
	if err := shard.UpsertConversationState(ctx, base); err != nil {
		t.Fatalf("UpsertConversationState(): %v", err)
	}
	if err := shard.TouchConversationActiveAt(ctx, ConversationActivePatch{
		UID:         "u1",
		Kind:        ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    300,
		MessageSeq:  5,
	}); err != nil {
		t.Fatalf("TouchConversationActiveAt(deleted): %v", err)
	}
	got, ok, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState() ok=%v err=%v", ok, err)
	}
	if got.ActiveAt != 100 {
		t.Fatalf("ActiveAt after deleted touch = %d, want 100", got.ActiveAt)
	}

	if err := shard.TouchConversationActiveAt(ctx, ConversationActivePatch{
		UID:         "u1",
		Kind:        ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    300,
		MessageSeq:  6,
	}); err != nil {
		t.Fatalf("TouchConversationActiveAt(): %v", err)
	}
	got, ok, err = shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(touched) ok=%v err=%v", ok, err)
	}
	if got.ActiveAt != 300 || got.UpdatedAt != 200 || got.ReadSeq != 10 || got.DeletedToSeq != 5 {
		t.Fatalf("touched state = %+v, want active 300 while preserving other fields", got)
	}

	if err := shard.UpsertConversationState(ctx, ConversationState{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g2", ChannelType: 2, UpdatedAt: 20}); err != nil {
		t.Fatalf("UpsertConversationState(g2): %v", err)
	}
	page, cursor, done, err := shard.ListConversationStatePage(ctx, ConversationKindNormal, "u1", ConversationCursor{}, 1)
	if err != nil {
		t.Fatalf("ListConversationStatePage(): %v", err)
	}
	if done || len(page) != 1 || page[0].ChannelID != "g1" || cursor != (ConversationCursor{ChannelID: "g1", ChannelType: 2}) {
		t.Fatalf("first page=%+v cursor=%+v done=%v, want g1 and more", page, cursor, done)
	}
	page, cursor, done, err = shard.ListConversationStatePage(ctx, ConversationKindNormal, "u1", cursor, 2)
	if err != nil {
		t.Fatalf("ListConversationStatePage(next): %v", err)
	}
	if !done || len(page) != 1 || page[0].ChannelID != "g2" || cursor != (ConversationCursor{ChannelID: "g2", ChannelType: 2}) {
		t.Fatalf("next page=%+v cursor=%+v done=%v, want g2 final", page, cursor, done)
	}

	if err := shard.ClearConversationActiveAt(ctx, ConversationKindNormal, "u1", []ConversationKey{{ChannelID: "g1", ChannelType: 2}}); err != nil {
		t.Fatalf("ClearConversationActiveAt(): %v", err)
	}
	got, ok, err = shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(cleared) ok=%v err=%v", ok, err)
	}
	if got.ActiveAt != 0 || got.ReadSeq != 10 || got.DeletedToSeq != 5 || got.UpdatedAt != 200 {
		t.Fatalf("cleared state = %+v, want only active cleared", got)
	}

	if err := shard.HideConversation(ctx, ConversationDelete{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		DeletedToSeq: 20,
		UpdatedAt:    500,
	}); err != nil {
		t.Fatalf("HideConversation(): %v", err)
	}
	got, ok, err = shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(hidden) ok=%v err=%v", ok, err)
	}
	if got.ActiveAt != 0 || got.DeletedToSeq != 20 || got.UpdatedAt != 500 {
		t.Fatalf("hidden state = %+v, want deleted 20 updated 500 inactive", got)
	}

	if err := shard.HideConversation(ctx, ConversationDelete{UID: "u1", Kind: ConversationKindNormal, ChannelID: "missing", ChannelType: 2}); err != nil {
		t.Fatalf("HideConversation(missing zero): %v", err)
	}
	if _, ok, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "missing", 2); err != nil || ok {
		t.Fatalf("missing hidden state ok=%v err=%v, want no row", ok, err)
	}
}

func TestUserConversationTouchIgnoredBelowDeletedToSeq(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	base := ConversationState{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 0}
	if err := shard.UpsertConversationState(ctx, base); err != nil {
		t.Fatalf("UpsertConversationState(): %v", err)
	}
	err := shard.TouchConversationActiveAt(ctx, ConversationActivePatch{
		UID:         "u1",
		Kind:        ConversationKindNormal,
		ChannelID:   "g1",
		ChannelType: 2,
		ActiveAt:    500,
		MessageSeq:  10,
	})
	if err != nil {
		t.Fatalf("TouchConversationActiveAt(): %v", err)
	}
	got, _, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState(): %v", err)
	}
	if got.ActiveAt != 0 {
		t.Fatalf("ActiveAt = %d, want unchanged 0", got.ActiveAt)
	}
}

func TestUserConversationUpsertMergePreservesMonotonicFloors(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	base := ConversationState{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      20,
		DeletedToSeq: 15,
		ActiveAt:     300,
		UpdatedAt:    400,
		SparseActive: true,
	}
	if err := shard.UpsertConversationState(ctx, base); err != nil {
		t.Fatalf("UpsertConversationState(base): %v", err)
	}
	if err := shard.UpsertConversationState(ctx, ConversationState{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 7,
		ActiveAt:     100,
		UpdatedAt:    200,
	}); err != nil {
		t.Fatalf("UpsertConversationState(regression): %v", err)
	}
	got, ok, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState() ok=%v err=%v, want ok", ok, err)
	}
	if got.ReadSeq != 20 || got.DeletedToSeq != 15 || got.ActiveAt != 300 || got.UpdatedAt != 400 {
		t.Fatalf("merged state = %+v, want monotonic fields preserved", got)
	}
	if got.SparseActive {
		t.Fatalf("SparseActive = true, want explicit upsert false to clear")
	}

	if err := shard.UpsertConversationState(ctx, ConversationState{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g2",
		ChannelType:  2,
		ReadSeq:      41,
		DeletedToSeq: 41,
		UpdatedAt:    500,
		SparseActive: true,
	}); err != nil {
		t.Fatalf("UpsertConversationState(join floor): %v", err)
	}
	got, ok, err = shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g2", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(join floor) ok=%v err=%v, want ok", ok, err)
	}
	if got.ReadSeq != 41 || got.DeletedToSeq != 41 || !got.SparseActive {
		t.Fatalf("join floor state = %+v, want read/delete floor and sparse active", got)
	}
}

func TestUserConversationTouchCanSetSparseActiveWithoutRegressingActiveAt(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	base := ConversationState{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 300}
	if err := shard.UpsertConversationState(ctx, base); err != nil {
		t.Fatalf("UpsertConversationState(): %v", err)
	}
	if err := shard.TouchConversationActiveAt(ctx, ConversationActivePatch{
		UID:             "u1",
		Kind:            ConversationKindNormal,
		ChannelID:       "g1",
		ChannelType:     2,
		ActiveAt:        100,
		SparseActive:    true,
		SparseActiveSet: true,
	}); err != nil {
		t.Fatalf("TouchConversationActiveAt(): %v", err)
	}
	got, ok, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState() ok=%v err=%v, want ok", ok, err)
	}
	if got.ActiveAt != 300 || !got.SparseActive {
		t.Fatalf("touched state = %+v, want active_at preserved and sparse set", got)
	}
}

func TestConversationActivePatchPersistsFloorsInOneMutation(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	if err := shard.TouchConversationActiveAt(ctx, ConversationActivePatch{
		UID:             "u1",
		Kind:            ConversationKindNormal,
		ChannelID:       "g-floor",
		ChannelType:     2,
		ReadSeq:         8,
		DeletedToSeq:    8,
		ActiveAt:        300,
		UpdatedAt:       301,
		MessageSeq:      9,
		SparseActive:    true,
		SparseActiveSet: true,
	}); err != nil {
		t.Fatalf("TouchConversationActiveAt(floor): %v", err)
	}
	got, ok, err := shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g-floor", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(floor) ok=%v err=%v, want ok", ok, err)
	}
	if got.ReadSeq != 8 || got.DeletedToSeq != 8 || got.ActiveAt != 300 || got.UpdatedAt != 301 || !got.SparseActive {
		t.Fatalf("floor patch state = %+v, want floors, active_at, updated_at, and sparse active", got)
	}

	if err := shard.UpsertConversationState(ctx, ConversationState{
		UID: "u1", Kind: ConversationKindNormal, ChannelID: "g-hidden", ChannelType: 2, DeletedToSeq: 10,
	}); err != nil {
		t.Fatalf("UpsertConversationState(hidden): %v", err)
	}
	if err := shard.TouchConversationActiveAt(ctx, ConversationActivePatch{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g-hidden",
		ChannelType:  2,
		ReadSeq:      12,
		DeletedToSeq: 12,
		ActiveAt:     500,
		UpdatedAt:    501,
		MessageSeq:   9,
	}); err != nil {
		t.Fatalf("TouchConversationActiveAt(hidden floor): %v", err)
	}
	got, ok, err = shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g-hidden", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(hidden) ok=%v err=%v, want ok", ok, err)
	}
	if got.ActiveAt != 0 || got.ReadSeq != 12 || got.DeletedToSeq != 12 || got.UpdatedAt != 501 {
		t.Fatalf("hidden floor state = %+v, want active blocked and floors advanced", got)
	}

	if err := shard.TouchConversationActiveAt(ctx, ConversationActivePatch{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g-self-hidden",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 10,
		ActiveAt:     600,
		UpdatedAt:    601,
		MessageSeq:   9,
	}); err != nil {
		t.Fatalf("TouchConversationActiveAt(self hidden floor): %v", err)
	}
	got, ok, err = shard.GetConversationState(ctx, ConversationKindNormal, "u1", "g-self-hidden", 2)
	if err != nil || !ok {
		t.Fatalf("GetConversationState(self hidden) ok=%v err=%v, want ok", ok, err)
	}
	if got.ActiveAt != 0 || got.ReadSeq != 10 || got.DeletedToSeq != 10 || got.UpdatedAt != 601 {
		t.Fatalf("self hidden floor state = %+v, want patch floor to block active", got)
	}
}

func TestShardStoreListsUserConversationActivePage(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()
	shardStore := db.ForHashSlot(3)
	ctx := context.Background()

	for _, state := range []ConversationState{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 200},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g2", ChannelType: 2, ActiveAt: 100},
	} {
		if err := shardStore.UpsertConversationState(ctx, state); err != nil {
			t.Fatalf("UpsertConversationState(%+v): %v", state, err)
		}
	}
	page, cursor, done, err := shardStore.ListConversationActivePage(ctx, ConversationKindNormal, "u1", ConversationActiveCursor{}, 1)
	if err != nil {
		t.Fatalf("ListConversationActivePage(): %v", err)
	}
	if done || len(page) != 1 || page[0].ChannelID != "g1" || cursor.ActiveAt != 200 {
		t.Fatalf("page=%+v cursor=%+v done=%v, want first active row and more", page, cursor, done)
	}
}

func TestWriteBatchUserConversationUpsertUsesReadYourWrites(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()

	batch := db.NewWriteBatch()
	defer batch.Close()
	if err := batch.UpsertConversationState(3, ConversationState{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      30,
		DeletedToSeq: 20,
		ActiveAt:     300,
		UpdatedAt:    400,
		SparseActive: true,
	}); err != nil {
		t.Fatalf("UpsertConversationState(first): %v", err)
	}
	if err := batch.UpsertConversationState(3, ConversationState{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		ReadSeq:      10,
		DeletedToSeq: 5,
		ActiveAt:     100,
		UpdatedAt:    200,
	}); err != nil {
		t.Fatalf("UpsertConversationState(second): %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}

	got, err := db.ForHashSlot(3).GetConversationState(context.Background(), ConversationKindNormal, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState(): %v", err)
	}
	if got.ReadSeq != 30 || got.DeletedToSeq != 20 || got.ActiveAt != 300 || got.UpdatedAt != 400 {
		t.Fatalf("batch upsert state = %+v, want monotonic maxima", got)
	}
	if got.SparseActive {
		t.Fatalf("SparseActive = true, want second explicit upsert false to clear")
	}
}

func TestWriteBatchUserConversationTouchUsesReadYourWrites(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()

	batch := db.NewWriteBatch()
	defer batch.Close()
	if err := batch.TouchConversationActiveAt(3, []ConversationActivePatch{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 300},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100, SparseActive: true, SparseActiveSet: true},
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 200, SparseActive: false, SparseActiveSet: true},
	}); err != nil {
		t.Fatalf("TouchConversationActiveAt(): %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}

	got, err := db.ForHashSlot(3).GetConversationState(context.Background(), ConversationKindNormal, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState(): %v", err)
	}
	if got.ActiveAt != 300 || got.SparseActive {
		t.Fatalf("batch touch state = %+v, want max active_at 300 and sparse false", got)
	}
}

func TestWriteBatchUserConversationTouchSeesStagedDeleteFence(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()

	batch := db.NewWriteBatch()
	defer batch.Close()
	if err := batch.UpsertConversationState(3, ConversationState{
		UID:          "u1",
		Kind:         ConversationKindNormal,
		ChannelID:    "g1",
		ChannelType:  2,
		DeletedToSeq: 10,
	}); err != nil {
		t.Fatalf("UpsertConversationState(delete floor): %v", err)
	}
	if err := batch.TouchConversationActiveAt(3, []ConversationActivePatch{
		{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 500, MessageSeq: 10},
	}); err != nil {
		t.Fatalf("TouchConversationActiveAt(): %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}

	got, err := db.ForHashSlot(3).GetConversationState(context.Background(), ConversationKindNormal, "u1", "g1", 2)
	if err != nil {
		t.Fatalf("GetConversationState(): %v", err)
	}
	if got.DeletedToSeq != 10 || got.ActiveAt != 0 {
		t.Fatalf("batch fenced touch state = %+v, want deleted_to_seq 10 and inactive", got)
	}
}

func TestWriteBatchConversationRejectsInvalidInputs(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(): %v", err)
		}
	}()

	tests := []struct {
		name string
		run  func(*WriteBatch) error
	}{
		{
			name: "upsert zero kind",
			run: func(batch *WriteBatch) error {
				return batch.UpsertConversationState(3, ConversationState{UID: "u1", ChannelID: "g1", ChannelType: 2})
			},
		},
		{
			name: "touch zero kind",
			run: func(batch *WriteBatch) error {
				return batch.TouchConversationActiveAt(3, []ConversationActivePatch{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}})
			},
		},
		{
			name: "touch validates whole batch before staging",
			run: func(batch *WriteBatch) error {
				return batch.TouchConversationActiveAt(3, []ConversationActivePatch{
					{UID: "u1", Kind: ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 100},
					{UID: "u1", ChannelID: "g2", ChannelType: 2, ActiveAt: 200},
				})
			},
		},
		{
			name: "clear zero kind",
			run: func(batch *WriteBatch) error {
				return batch.ClearConversationActiveAt(3, 0, "u1", []ConversationKey{{ChannelID: "g1", ChannelType: 2}})
			},
		},
		{
			name: "clear invalid key",
			run: func(batch *WriteBatch) error {
				return batch.ClearConversationActiveAt(3, ConversationKindNormal, "u1", []ConversationKey{{ChannelType: 2}})
			},
		},
		{
			name: "hide zero kind",
			run: func(batch *WriteBatch) error {
				return batch.HideConversation(3, ConversationDelete{UID: "u1", ChannelID: "g1", ChannelType: 2, DeletedToSeq: 1})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := db.NewWriteBatch()
			defer batch.Close()
			err := tt.run(batch)
			if !errors.Is(err, dberrors.ErrInvalidArgument) {
				t.Fatalf("batch conversation invalid err = %v, want invalid argument", err)
			}
			if err := batch.Commit(); err != nil {
				t.Fatalf("Commit() after rejected op: %v", err)
			}
			active, err := db.ForHashSlot(3).ListConversationActive(context.Background(), ConversationKindNormal, "u1", 10)
			if err != nil {
				t.Fatalf("ListConversationActive(): %v", err)
			}
			if len(active) != 0 {
				t.Fatalf("rejected batch staged rows: %+v", active)
			}
		})
	}
}

func TestUserConversationRejectsInvalidInputs(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(3)

	err := shard.UpsertConversationState(context.Background(), ConversationState{ChannelID: "g1", ChannelType: 2})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("invalid upsert err = %v, want invalid argument", err)
	}
	_, _, _, err = shard.ListConversationStatePage(context.Background(), ConversationKindNormal, "u1", ConversationCursor{ChannelType: 2}, 1)
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("invalid cursor err = %v, want invalid argument", err)
	}
}

func equalConversationStates(a, b []ConversationState) bool {
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
