package meta

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestCompatibilityMigrationFacadePersistsAndCleansReplayState(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	}()
	ctx := context.Background()
	const hashSlot uint16 = 31

	state := HashSlotMigrationState{
		HashSlot: hashSlot, SourceSlot: 100, TargetSlot: 200,
		Phase: 1, FenceIndex: 50, LastOutboxIndex: 60, LastAckedIndex: 40,
	}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	gotState, err := db.LoadHashSlotMigrationState(ctx, hashSlot)
	if err != nil || gotState != state {
		t.Fatalf("LoadHashSlotMigrationState() = (%+v, %v), want %+v", gotState, err, state)
	}
	states, err := db.ListHashSlotMigrationStates(ctx)
	if err != nil || len(states) != 1 || states[0] != state {
		t.Fatalf("ListHashSlotMigrationStates() = (%+v, %v)", states, err)
	}

	delta := AppliedHashSlotDelta{HashSlot: hashSlot, SourceSlot: 100, SourceIndex: 51}
	if err := db.MarkAppliedHashSlotDelta(ctx, delta); err != nil {
		t.Fatalf("MarkAppliedHashSlotDelta(): %v", err)
	}
	present, err := db.HasAppliedHashSlotDelta(ctx, delta)
	if err != nil || !present {
		t.Fatalf("HasAppliedHashSlotDelta() = (%v, %v), want true", present, err)
	}
	deltas, err := db.ListAppliedHashSlotDeltas(ctx, hashSlot)
	if err != nil || len(deltas) != 1 || deltas[0] != delta {
		t.Fatalf("ListAppliedHashSlotDeltas() = (%+v, %v)", deltas, err)
	}

	first := HashSlotMigrationOutboxRow{HashSlot: hashSlot, SourceSlot: 100, TargetSlot: 200, SourceIndex: 52, Data: []byte("delta-52")}
	second := HashSlotMigrationOutboxRow{HashSlot: hashSlot, SourceSlot: 100, TargetSlot: 200, SourceIndex: 53, Data: []byte("delta-53")}
	for _, row := range []HashSlotMigrationOutboxRow{second, first} {
		if err := db.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("UpsertHashSlotMigrationOutbox(%+v): %v", row, err)
		}
	}
	loaded, err := db.LoadHashSlotMigrationOutbox(ctx, hashSlot, first.SourceSlot, first.TargetSlot, first.SourceIndex)
	if err != nil || !equalHashSlotMigrationOutboxRow(loaded, first) {
		t.Fatalf("LoadHashSlotMigrationOutbox() = (%+v, %v), want %+v", loaded, err, first)
	}
	rows, err := db.ListHashSlotMigrationOutbox(ctx, hashSlot, 100, 200, 52, 10)
	if err != nil || len(rows) != 1 || !equalHashSlotMigrationOutboxRow(rows[0], second) {
		t.Fatalf("ListHashSlotMigrationOutbox(after 52) = (%+v, %v)", rows, err)
	}
	if err := db.DeleteHashSlotMigrationOutbox(ctx, hashSlot, first.SourceSlot, first.TargetSlot, first.SourceIndex); err != nil {
		t.Fatalf("DeleteHashSlotMigrationOutbox(): %v", err)
	}
	if _, err := db.LoadHashSlotMigrationOutbox(ctx, hashSlot, first.SourceSlot, first.TargetSlot, first.SourceIndex); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationOutbox(after delete) error = %v, want not found", err)
	}

	batchDelta := AppliedHashSlotDelta{HashSlot: hashSlot, SourceSlot: 100, SourceIndex: 54}
	pairRow := HashSlotMigrationOutboxRow{HashSlot: hashSlot, SourceSlot: 100, TargetSlot: 200, SourceIndex: 54, Data: []byte("same-pair")}
	otherPairRow := HashSlotMigrationOutboxRow{HashSlot: hashSlot, SourceSlot: 101, TargetSlot: 201, SourceIndex: 55, Data: []byte("other-pair")}
	state.Phase = 2
	state.LastOutboxIndex = 70
	batch := db.NewWriteBatch()
	defer batch.Close()
	if err := batch.UpsertHashSlotMigrationState(state); err != nil {
		t.Fatalf("batch UpsertHashSlotMigrationState(): %v", err)
	}
	if err := batch.MarkAppliedHashSlotDelta(batchDelta); err != nil {
		t.Fatalf("batch MarkAppliedHashSlotDelta(): %v", err)
	}
	if err := batch.UpsertHashSlotMigrationOutbox(pairRow); err != nil {
		t.Fatalf("batch UpsertHashSlotMigrationOutbox(pair): %v", err)
	}
	if err := batch.UpsertHashSlotMigrationOutbox(otherPairRow); err != nil {
		t.Fatalf("batch UpsertHashSlotMigrationOutbox(other pair): %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("batch Commit(): %v", err)
	}
	gotState, err = db.ForHashSlot(hashSlot).LoadHashSlotMigrationState(ctx)
	if err != nil || gotState != state {
		t.Fatalf("shard LoadHashSlotMigrationState() = (%+v, %v), want %+v", gotState, err, state)
	}
	present, err = db.ForHashSlot(hashSlot).HasAppliedHashSlotDelta(ctx, batchDelta)
	if err != nil || !present {
		t.Fatalf("shard HasAppliedHashSlotDelta() = (%v, %v), want true", present, err)
	}
	loaded, err = db.ForHashSlot(hashSlot).LoadHashSlotMigrationOutbox(ctx, otherPairRow.SourceSlot, otherPairRow.TargetSlot, otherPairRow.SourceIndex)
	if err != nil || !bytes.Equal(loaded.Data, otherPairRow.Data) {
		t.Fatalf("shard LoadHashSlotMigrationOutbox() = (%+v, %v)", loaded, err)
	}

	cleanup := db.NewWriteBatch()
	defer cleanup.Close()
	if err := cleanup.DeleteAppliedHashSlotDelta(batchDelta); err != nil {
		t.Fatalf("DeleteAppliedHashSlotDelta(): %v", err)
	}
	if err := cleanup.DeleteHashSlotMigrationOutbox(hashSlot, second.SourceSlot, second.TargetSlot, second.SourceIndex); err != nil {
		t.Fatalf("DeleteHashSlotMigrationOutbox(): %v", err)
	}
	if err := cleanup.DeleteHashSlotMigrationOutboxForPair(hashSlot, pairRow.SourceSlot, pairRow.TargetSlot); err != nil {
		t.Fatalf("DeleteHashSlotMigrationOutboxForPair(): %v", err)
	}
	if err := cleanup.DeleteAllHashSlotMigrationOutbox(hashSlot); err != nil {
		t.Fatalf("DeleteAllHashSlotMigrationOutbox(): %v", err)
	}
	if err := cleanup.DeleteHashSlotMigrationState(hashSlot); err != nil {
		t.Fatalf("DeleteHashSlotMigrationState(): %v", err)
	}
	if err := cleanup.Commit(); err != nil {
		t.Fatalf("cleanup Commit(): %v", err)
	}
	if _, err := db.LoadHashSlotMigrationState(ctx, hashSlot); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationState(after cleanup) error = %v, want not found", err)
	}
	present, err = db.HasAppliedHashSlotDelta(ctx, batchDelta)
	if err != nil || present {
		t.Fatalf("HasAppliedHashSlotDelta(after cleanup) = (%v, %v), want false", present, err)
	}
	rows, err = db.ForHashSlot(hashSlot).ListHashSlotMigrationOutbox(ctx, 101, 201, 0, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("ListHashSlotMigrationOutbox(after cleanup) = (%+v, %v), want empty", rows, err)
	}

	if err := db.DeleteAppliedHashSlotDelta(ctx, delta); err != nil {
		t.Fatalf("DeleteAppliedHashSlotDelta(original): %v", err)
	}
	if err := db.DeleteAllHashSlotMigrationOutbox(ctx, hashSlot); err != nil {
		t.Fatalf("DeleteAllHashSlotMigrationOutbox(idempotent): %v", err)
	}
}

func TestCompatibilityLegacySlotDeletionRemovesWatermarkAndOnlyItsHashSlot(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	batch := db.NewWriteBatch()
	defer batch.Close()
	if err := batch.CreateUser(8, User{UID: "deleted", Token: "slot-8"}); err != nil {
		t.Fatalf("CreateUser(slot 8): %v", err)
	}
	if err := batch.CreateUser(9, User{UID: "survivor", Token: "slot-9"}); err != nil {
		t.Fatalf("CreateUser(slot 9): %v", err)
	}
	if err := batch.SetSlotAppliedIndex(8, 77); err != nil {
		t.Fatalf("SetSlotAppliedIndex(): %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if err := db.DeleteSlotData(ctx, 8); err != nil {
		t.Fatalf("DeleteSlotData(): %v", err)
	}
	if _, err := db.ForSlot(8).GetUser(ctx, "deleted"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUser(deleted slot) error = %v, want not found", err)
	}
	if applied, err := db.SlotAppliedIndex(ctx, 8); err != nil || applied != 0 {
		t.Fatalf("SlotAppliedIndex(after delete) = (%d, %v), want zero", applied, err)
	}
	if user, err := db.ForSlot(9).GetUser(ctx, "survivor"); err != nil || user.Token != "slot-9" {
		t.Fatalf("GetUser(neighbor slot) = (%+v, %v), want survivor", user, err)
	}
}
