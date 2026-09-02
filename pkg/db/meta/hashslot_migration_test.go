package meta

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestHashSlotMigrationStateAndAppliedDelta(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(7)
	ctx := context.Background()

	state := HashSlotMigrationState{
		HashSlot:        7,
		SourceSlot:      11,
		TargetSlot:      22,
		Phase:           2,
		FenceIndex:      100,
		LastOutboxIndex: 120,
		LastAckedIndex:  90,
	}
	if err := shard.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	got, ok, err := shard.LoadHashSlotMigrationState(ctx)
	if err != nil || !ok {
		t.Fatalf("LoadHashSlotMigrationState() ok=%v err=%v", ok, err)
	}
	if got != state {
		t.Fatalf("migration state = %+v, want %+v", got, state)
	}

	updated := state
	updated.LastAckedIndex = 110
	if err := shard.UpsertHashSlotMigrationState(ctx, updated); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(update): %v", err)
	}
	got, ok, err = shard.LoadHashSlotMigrationState(ctx)
	if err != nil || !ok || got != updated {
		t.Fatalf("updated state=%+v ok=%v err=%v, want %+v", got, ok, err, updated)
	}

	first := AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 100}
	second := AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 101}
	for _, delta := range []AppliedHashSlotDelta{second, first} {
		if err := shard.MarkAppliedHashSlotDelta(ctx, delta); err != nil {
			t.Fatalf("MarkAppliedHashSlotDelta(%+v): %v", delta, err)
		}
	}
	exists, err := shard.HasAppliedHashSlotDelta(ctx, first)
	if err != nil || !exists {
		t.Fatalf("HasAppliedHashSlotDelta() = %v err %v, want true", exists, err)
	}
	deltas, err := shard.ListAppliedHashSlotDeltas(ctx)
	if err != nil {
		t.Fatalf("ListAppliedHashSlotDeltas(): %v", err)
	}
	if len(deltas) != 2 || deltas[0] != first || deltas[1] != second {
		t.Fatalf("applied deltas = %+v, want ordered first second", deltas)
	}
	if err := shard.DeleteAppliedHashSlotDelta(ctx, first); err != nil {
		t.Fatalf("DeleteAppliedHashSlotDelta(): %v", err)
	}
	exists, err = shard.HasAppliedHashSlotDelta(ctx, first)
	if err != nil || exists {
		t.Fatalf("HasAppliedHashSlotDelta(after delete) = %v err %v, want false", exists, err)
	}

	if err := shard.DeleteHashSlotMigrationState(ctx); err != nil {
		t.Fatalf("DeleteHashSlotMigrationState(): %v", err)
	}
	if _, ok, err := shard.LoadHashSlotMigrationState(ctx); err != nil || ok {
		t.Fatalf("LoadHashSlotMigrationState(after delete) ok=%v err=%v, want missing", ok, err)
	}
}

func TestHashSlotMigrationStateKeepsLegacyRowLayout(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(7)
	ctx := context.Background()

	state := HashSlotMigrationState{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, Phase: 1, FenceIndex: 100, LastOutboxIndex: 120, LastAckedIndex: 90}
	if err := shard.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}

	legacyKey := encodeHashSlotMigrationStateKey(7)
	runtimeKey, err := hashSlotMigrationTable.primaryRowKey(7, hashSlotMigrationStatePrimaryKey())
	if err != nil {
		t.Fatalf("runtime state row key: %v", err)
	}
	if string(runtimeKey) != string(legacyKey) {
		t.Fatalf("runtime state key %x, want legacy %x", runtimeKey, legacyKey)
	}
	if _, ok, err := store.db.get(legacyKey); err != nil || !ok {
		t.Fatalf("legacy state row ok=%v err=%v", ok, err)
	}

	applied := AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 121}
	if err := shard.MarkAppliedHashSlotDelta(ctx, applied); err != nil {
		t.Fatalf("MarkAppliedHashSlotDelta(): %v", err)
	}
	if _, ok, err := store.db.get(encodeAppliedHashSlotDeltaKey(applied)); err != nil || !ok {
		t.Fatalf("applied delta row ok=%v err=%v", ok, err)
	}
}

func TestHashSlotMigrationOutboxCRUDScanAndAckDelete(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(7)
	ctx := context.Background()

	first := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("cmd-100")}
	second := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: 101, Data: []byte("cmd-101")}
	third := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: 102, Data: []byte("cmd-102")}
	for _, row := range []HashSlotMigrationOutboxRow{third, first, second} {
		if err := shard.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("UpsertHashSlotMigrationOutbox(%+v): %v", row, err)
		}
	}
	got, ok, err := shard.LoadHashSlotMigrationOutbox(ctx, first.SourceSlot, first.TargetSlot, first.SourceIndex)
	if err != nil || !ok {
		t.Fatalf("LoadHashSlotMigrationOutbox() ok=%v err=%v", ok, err)
	}
	if !equalHashSlotMigrationOutboxRow(got, first) {
		t.Fatalf("outbox row = %+v, want %+v", got, first)
	}
	got.Data[0] = 'X'
	gotAgain, _, err := shard.LoadHashSlotMigrationOutbox(ctx, first.SourceSlot, first.TargetSlot, first.SourceIndex)
	if err != nil || !bytes.Equal(gotAgain.Data, first.Data) {
		t.Fatalf("outbox data was not copied: got %+v err=%v want %+v", gotAgain, err, first)
	}

	rows, err := shard.ListHashSlotMigrationOutbox(ctx, 11, 22, 100, 2)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(): %v", err)
	}
	if len(rows) != 2 || !equalHashSlotMigrationOutboxRow(rows[0], second) || !equalHashSlotMigrationOutboxRow(rows[1], third) {
		t.Fatalf("outbox page = %+v, want second third", rows)
	}

	if err := shard.DeleteHashSlotMigrationOutboxThrough(ctx, 11, 22, 101); err != nil {
		t.Fatalf("DeleteHashSlotMigrationOutboxThrough(): %v", err)
	}
	if _, ok, err := shard.LoadHashSlotMigrationOutbox(ctx, 11, 22, 100); err != nil || ok {
		t.Fatalf("Load deleted 100 ok=%v err=%v, want missing", ok, err)
	}
	rows, err = shard.ListHashSlotMigrationOutbox(ctx, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(after delete): %v", err)
	}
	if len(rows) != 1 || !equalHashSlotMigrationOutboxRow(rows[0], third) {
		t.Fatalf("remaining outbox rows = %+v, want third only", rows)
	}
}

func TestHashSlotMigrationRejectsInvalidRows(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(7)
	ctx := context.Background()

	err := shard.UpsertHashSlotMigrationState(ctx, HashSlotMigrationState{HashSlot: 8, SourceSlot: 11, TargetSlot: 22})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("state hash-slot mismatch err = %v, want invalid argument", err)
	}
	err = shard.MarkAppliedHashSlotDelta(ctx, AppliedHashSlotDelta{HashSlot: 7, SourceIndex: 100})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("applied delta zero source err = %v, want invalid argument", err)
	}
	err = shard.UpsertHashSlotMigrationOutbox(ctx, HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 11, SourceIndex: 100, Data: []byte("x")})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("outbox same source target err = %v, want invalid argument", err)
	}
}

func equalHashSlotMigrationOutboxRow(a, b HashSlotMigrationOutboxRow) bool {
	return a.HashSlot == b.HashSlot &&
		a.SourceSlot == b.SourceSlot &&
		a.TargetSlot == b.TargetSlot &&
		a.SourceIndex == b.SourceIndex &&
		bytes.Equal(a.Data, b.Data)
}
