package meta

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestHashSlotMigrationStateCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	state := HashSlotMigrationState{
		HashSlot:        7,
		SourceSlot:      11,
		TargetSlot:      22,
		Phase:           2,
		FenceIndex:      101,
		LastOutboxIndex: 202,
		LastAckedIndex:  102,
	}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}

	got, err := db.LoadHashSlotMigrationState(ctx, state.HashSlot)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if !reflect.DeepEqual(got, state) {
		t.Fatalf("loaded migration state = %#v, want %#v", got, state)
	}

	updated := state
	updated.Phase = 3
	updated.LastAckedIndex = 201
	if err := db.UpsertHashSlotMigrationState(ctx, updated); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(update): %v", err)
	}

	list, err := db.ListHashSlotMigrationStates(ctx)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationStates(): %v", err)
	}
	if !reflect.DeepEqual(list, []HashSlotMigrationState{updated}) {
		t.Fatalf("migration state list = %#v, want %#v", list, []HashSlotMigrationState{updated})
	}

	if err := db.DeleteHashSlotMigrationState(ctx, state.HashSlot); err != nil {
		t.Fatalf("DeleteHashSlotMigrationState(): %v", err)
	}
	if _, err := db.LoadHashSlotMigrationState(ctx, state.HashSlot); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationState() after delete err = %v, want ErrNotFound", err)
	}
}

func TestHashSlotMigrationStateRejectsZeroSlotIdentities(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := db.UpsertHashSlotMigrationState(ctx, HashSlotMigrationState{HashSlot: 1, TargetSlot: 2}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("UpsertHashSlotMigrationState(zero source) err = %v, want ErrInvalidArgument", err)
	}
	if err := db.UpsertHashSlotMigrationState(ctx, HashSlotMigrationState{HashSlot: 1, SourceSlot: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("UpsertHashSlotMigrationState(zero target) err = %v, want ErrInvalidArgument", err)
	}
}

func TestHashSlotMigrationStateRejectsInvalidProgress(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	cases := []struct {
		name  string
		state HashSlotMigrationState
	}{
		{
			name:  "same source target",
			state: HashSlotMigrationState{HashSlot: 1, SourceSlot: 2, TargetSlot: 2},
		},
		{
			name:  "last acked beyond outbox",
			state: HashSlotMigrationState{HashSlot: 1, SourceSlot: 2, TargetSlot: 3, LastOutboxIndex: 10, LastAckedIndex: 11},
		},
		{
			name:  "fence beyond outbox",
			state: HashSlotMigrationState{HashSlot: 1, SourceSlot: 2, TargetSlot: 3, FenceIndex: 12, LastOutboxIndex: 11},
		},
		{
			name:  "unknown phase",
			state: HashSlotMigrationState{HashSlot: 1, SourceSlot: 2, TargetSlot: 3, Phase: 4},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.UpsertHashSlotMigrationState(ctx, tc.state); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("UpsertHashSlotMigrationState() err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestListHashSlotMigrationStatesSkipsDenseAppliedDeltaRecords(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	state := HashSlotMigrationState{HashSlot: 7, SourceSlot: 11, TargetSlot: 22}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	for i := uint64(1); i <= 128; i++ {
		if err := db.MarkAppliedHashSlotDelta(ctx, AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: i}); err != nil {
			t.Fatalf("MarkAppliedHashSlotDelta(%d): %v", i, err)
		}
	}

	scanGuardCtx := &errAfterContext{Context: ctx, remaining: 4}
	states, err := db.ListHashSlotMigrationStates(scanGuardCtx)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationStates(): %v", err)
	}
	if !reflect.DeepEqual(states, []HashSlotMigrationState{state}) {
		t.Fatalf("migration states = %#v, want %#v", states, []HashSlotMigrationState{state})
	}
}

type errAfterContext struct {
	context.Context
	remaining int
}

func (ctx *errAfterContext) Err() error {
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	ctx.remaining--
	return nil
}

func TestAppliedHashSlotDeltaCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	first := AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 100}
	second := AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 101}
	otherHashSlot := AppliedHashSlotDelta{HashSlot: 8, SourceSlot: 11, SourceIndex: 100}

	for _, delta := range []AppliedHashSlotDelta{second, first, otherHashSlot} {
		if err := db.MarkAppliedHashSlotDelta(ctx, delta); err != nil {
			t.Fatalf("MarkAppliedHashSlotDelta(%#v): %v", delta, err)
		}
	}

	ok, err := db.HasAppliedHashSlotDelta(ctx, first)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta(): %v", err)
	}
	if !ok {
		t.Fatal("HasAppliedHashSlotDelta() = false, want true")
	}
	ok, err = db.HasAppliedHashSlotDelta(ctx, AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 102})
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta(missing): %v", err)
	}
	if ok {
		t.Fatal("HasAppliedHashSlotDelta(missing) = true, want false")
	}

	list, err := db.ListAppliedHashSlotDeltas(ctx, 7)
	if err != nil {
		t.Fatalf("ListAppliedHashSlotDeltas(): %v", err)
	}
	if !reflect.DeepEqual(list, []AppliedHashSlotDelta{first, second}) {
		t.Fatalf("applied delta list = %#v, want %#v", list, []AppliedHashSlotDelta{first, second})
	}

	if err := db.DeleteAppliedHashSlotDelta(ctx, first); err != nil {
		t.Fatalf("DeleteAppliedHashSlotDelta(): %v", err)
	}
	ok, err = db.HasAppliedHashSlotDelta(ctx, first)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta(after delete): %v", err)
	}
	if ok {
		t.Fatal("HasAppliedHashSlotDelta(after delete) = true, want false")
	}
}

func TestAppliedHashSlotDeltaRejectsZeroIdentities(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := db.MarkAppliedHashSlotDelta(ctx, AppliedHashSlotDelta{HashSlot: 1, SourceIndex: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("MarkAppliedHashSlotDelta(zero source slot) err = %v, want ErrInvalidArgument", err)
	}
	if err := db.MarkAppliedHashSlotDelta(ctx, AppliedHashSlotDelta{HashSlot: 1, SourceSlot: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("MarkAppliedHashSlotDelta(zero source index) err = %v, want ErrInvalidArgument", err)
	}
}

func TestWriteBatchPersistsMigrationAndAppliedDeltaRecords(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	state := HashSlotMigrationState{
		HashSlot:        7,
		SourceSlot:      11,
		TargetSlot:      22,
		Phase:           1,
		FenceIndex:      10,
		LastOutboxIndex: 30,
		LastAckedIndex:  20,
	}
	delta := AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 99}

	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.UpsertHashSlotMigrationState(state); err != nil {
		t.Fatalf("WriteBatch.UpsertHashSlotMigrationState(): %v", err)
	}
	if err := wb.MarkAppliedHashSlotDelta(delta); err != nil {
		t.Fatalf("WriteBatch.MarkAppliedHashSlotDelta(): %v", err)
	}
	if err := wb.Commit(); err != nil {
		t.Fatalf("WriteBatch.Commit(): %v", err)
	}

	got, err := db.LoadHashSlotMigrationState(ctx, state.HashSlot)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if !reflect.DeepEqual(got, state) {
		t.Fatalf("loaded migration state = %#v, want %#v", got, state)
	}
	ok, err := db.HasAppliedHashSlotDelta(ctx, delta)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta(): %v", err)
	}
	if !ok {
		t.Fatal("HasAppliedHashSlotDelta() = false, want true")
	}
}

func TestWriteBatchDeletesMigrationAndAppliedDeltaRecords(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	state := HashSlotMigrationState{HashSlot: 7, SourceSlot: 11, TargetSlot: 22}
	delta := AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 99}
	if err := db.UpsertHashSlotMigrationState(ctx, state); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(): %v", err)
	}
	if err := db.MarkAppliedHashSlotDelta(ctx, delta); err != nil {
		t.Fatalf("MarkAppliedHashSlotDelta(): %v", err)
	}

	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.DeleteHashSlotMigrationState(state.HashSlot); err != nil {
		t.Fatalf("WriteBatch.DeleteHashSlotMigrationState(): %v", err)
	}
	if err := wb.DeleteAppliedHashSlotDelta(delta); err != nil {
		t.Fatalf("WriteBatch.DeleteAppliedHashSlotDelta(): %v", err)
	}
	if err := wb.Commit(); err != nil {
		t.Fatalf("WriteBatch.Commit(): %v", err)
	}

	if _, err := db.LoadHashSlotMigrationState(ctx, state.HashSlot); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationState() after batch delete err = %v, want ErrNotFound", err)
	}
	ok, err := db.HasAppliedHashSlotDelta(ctx, delta)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta() after batch delete: %v", err)
	}
	if ok {
		t.Fatal("HasAppliedHashSlotDelta() after batch delete = true, want false")
	}
}

func TestHashSlotSnapshotPreservesMigrationAndAppliedDeltaRecords(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	includedState := HashSlotMigrationState{
		HashSlot:        7,
		SourceSlot:      11,
		TargetSlot:      22,
		Phase:           2,
		FenceIndex:      10,
		LastOutboxIndex: 30,
		LastAckedIndex:  20,
	}
	excludedState := HashSlotMigrationState{
		HashSlot:        8,
		SourceSlot:      11,
		TargetSlot:      33,
		Phase:           1,
		FenceIndex:      40,
		LastOutboxIndex: 70,
		LastAckedIndex:  60,
	}
	includedDelta := AppliedHashSlotDelta{HashSlot: 7, SourceSlot: 11, SourceIndex: 99}
	excludedDelta := AppliedHashSlotDelta{HashSlot: 8, SourceSlot: 11, SourceIndex: 100}

	if err := db.UpsertHashSlotMigrationState(ctx, includedState); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(included): %v", err)
	}
	if err := db.UpsertHashSlotMigrationState(ctx, excludedState); err != nil {
		t.Fatalf("UpsertHashSlotMigrationState(excluded): %v", err)
	}
	if err := db.MarkAppliedHashSlotDelta(ctx, includedDelta); err != nil {
		t.Fatalf("MarkAppliedHashSlotDelta(included): %v", err)
	}
	if err := db.MarkAppliedHashSlotDelta(ctx, excludedDelta); err != nil {
		t.Fatalf("MarkAppliedHashSlotDelta(excluded): %v", err)
	}

	snap, err := db.ExportHashSlotSnapshot(ctx, []uint16{7})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}

	restoreDB := openTestDB(t)
	if err := restoreDB.ImportHashSlotSnapshot(ctx, snap); err != nil {
		t.Fatalf("ImportHashSlotSnapshot(): %v", err)
	}

	gotState, err := restoreDB.LoadHashSlotMigrationState(ctx, 7)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(restored included): %v", err)
	}
	if !reflect.DeepEqual(gotState, includedState) {
		t.Fatalf("restored migration state = %#v, want %#v", gotState, includedState)
	}
	if _, err := restoreDB.LoadHashSlotMigrationState(ctx, 8); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationState(restored excluded) err = %v, want ErrNotFound", err)
	}

	ok, err := restoreDB.HasAppliedHashSlotDelta(ctx, includedDelta)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta(restored included): %v", err)
	}
	if !ok {
		t.Fatal("HasAppliedHashSlotDelta(restored included) = false, want true")
	}
	ok, err = restoreDB.HasAppliedHashSlotDelta(ctx, excludedDelta)
	if err != nil {
		t.Fatalf("HasAppliedHashSlotDelta(restored excluded): %v", err)
	}
	if ok {
		t.Fatal("HasAppliedHashSlotDelta(restored excluded) = true, want false")
	}
}

func TestHashSlotMigrationOutboxCRUDAndOrdering(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	first := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("cmd-100")}
	second := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: 101, Data: []byte("cmd-101")}
	third := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: 102, Data: []byte("cmd-102")}
	otherHashSlot := HashSlotMigrationOutboxRow{HashSlot: 8, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("other")}

	for _, row := range []HashSlotMigrationOutboxRow{third, first, otherHashSlot, second} {
		if err := db.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("UpsertHashSlotMigrationOutbox(%#v): %v", row, err)
		}
	}

	got, err := db.LoadHashSlotMigrationOutbox(ctx, first.HashSlot, first.SourceSlot, first.TargetSlot, first.SourceIndex)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationOutbox(): %v", err)
	}
	if !reflect.DeepEqual(got, first) {
		t.Fatalf("loaded outbox row = %#v, want %#v", got, first)
	}
	got.Data[0] = 'X'
	gotAgain, err := db.LoadHashSlotMigrationOutbox(ctx, first.HashSlot, first.SourceSlot, first.TargetSlot, first.SourceIndex)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationOutbox(second): %v", err)
	}
	if !reflect.DeepEqual(gotAgain, first) {
		t.Fatalf("outbox row data was not copied: %#v, want %#v", gotAgain, first)
	}

	rows, err := db.ListHashSlotMigrationOutbox(ctx, 7, 11, 22, 0, 10)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(): %v", err)
	}
	if !reflect.DeepEqual(rows, []HashSlotMigrationOutboxRow{first, second, third}) {
		t.Fatalf("outbox rows = %#v, want ordered source rows", rows)
	}

	if err := db.DeleteHashSlotMigrationOutbox(ctx, first.HashSlot, first.SourceSlot, first.TargetSlot, first.SourceIndex); err != nil {
		t.Fatalf("DeleteHashSlotMigrationOutbox(): %v", err)
	}
	if _, err := db.LoadHashSlotMigrationOutbox(ctx, first.HashSlot, first.SourceSlot, first.TargetSlot, first.SourceIndex); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationOutbox() after delete err = %v, want ErrNotFound", err)
	}
}

func TestHashSlotMigrationOutboxListAfterAndLimit(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	for _, index := range []uint64{100, 101, 102} {
		row := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: index, Data: []byte{byte(index)}}
		if err := db.UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("UpsertHashSlotMigrationOutbox(%d): %v", index, err)
		}
	}

	rows, err := db.ListHashSlotMigrationOutbox(ctx, 7, 11, 22, 100, 1)
	if err != nil {
		t.Fatalf("ListHashSlotMigrationOutbox(after, limit): %v", err)
	}
	if len(rows) != 1 || rows[0].SourceIndex != 101 {
		t.Fatalf("outbox page = %#v, want only source index 101", rows)
	}

	if _, err := db.ListHashSlotMigrationOutbox(ctx, 7, 11, 22, 0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ListHashSlotMigrationOutbox(limit=0) err = %v, want ErrInvalidArgument", err)
	}
}

func TestHashSlotMigrationOutboxRejectsInvalidRows(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	cases := []struct {
		name string
		row  HashSlotMigrationOutboxRow
	}{
		{name: "zero source slot", row: HashSlotMigrationOutboxRow{HashSlot: 7, TargetSlot: 22, SourceIndex: 100, Data: []byte("cmd")}},
		{name: "zero target slot", row: HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, SourceIndex: 100, Data: []byte("cmd")}},
		{name: "same source target", row: HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 11, SourceIndex: 100, Data: []byte("cmd")}},
		{name: "zero source index", row: HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, Data: []byte("cmd")}},
		{name: "empty data", row: HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.UpsertHashSlotMigrationOutbox(ctx, tc.row); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("UpsertHashSlotMigrationOutbox() err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestWriteBatchPersistsHashSlotMigrationOutboxWithState(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	state := HashSlotMigrationState{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, LastOutboxIndex: 100}
	row := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("cmd-100")}

	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.UpsertHashSlotMigrationOutbox(row); err != nil {
		t.Fatalf("WriteBatch.UpsertHashSlotMigrationOutbox(): %v", err)
	}
	if err := wb.UpsertHashSlotMigrationState(state); err != nil {
		t.Fatalf("WriteBatch.UpsertHashSlotMigrationState(): %v", err)
	}
	if err := wb.Commit(); err != nil {
		t.Fatalf("WriteBatch.Commit(): %v", err)
	}

	gotState, err := db.LoadHashSlotMigrationState(ctx, state.HashSlot)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationState(): %v", err)
	}
	if !reflect.DeepEqual(gotState, state) {
		t.Fatalf("loaded migration state = %#v, want %#v", gotState, state)
	}
	gotRow, err := db.LoadHashSlotMigrationOutbox(ctx, row.HashSlot, row.SourceSlot, row.TargetSlot, row.SourceIndex)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationOutbox(): %v", err)
	}
	if !reflect.DeepEqual(gotRow, row) {
		t.Fatalf("loaded outbox row = %#v, want %#v", gotRow, row)
	}
}

func TestHashSlotSnapshotPreservesMigrationOutboxRows(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	included := HashSlotMigrationOutboxRow{HashSlot: 7, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("included")}
	excluded := HashSlotMigrationOutboxRow{HashSlot: 8, SourceSlot: 11, TargetSlot: 22, SourceIndex: 100, Data: []byte("excluded")}
	if err := db.UpsertHashSlotMigrationOutbox(ctx, included); err != nil {
		t.Fatalf("UpsertHashSlotMigrationOutbox(included): %v", err)
	}
	if err := db.UpsertHashSlotMigrationOutbox(ctx, excluded); err != nil {
		t.Fatalf("UpsertHashSlotMigrationOutbox(excluded): %v", err)
	}

	snap, err := db.ExportHashSlotSnapshot(ctx, []uint16{7})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}
	restoreDB := openTestDB(t)
	if err := restoreDB.ImportHashSlotSnapshot(ctx, snap); err != nil {
		t.Fatalf("ImportHashSlotSnapshot(): %v", err)
	}

	got, err := restoreDB.LoadHashSlotMigrationOutbox(ctx, included.HashSlot, included.SourceSlot, included.TargetSlot, included.SourceIndex)
	if err != nil {
		t.Fatalf("LoadHashSlotMigrationOutbox(restored included): %v", err)
	}
	if !reflect.DeepEqual(got, included) {
		t.Fatalf("restored outbox row = %#v, want %#v", got, included)
	}
	if _, err := restoreDB.LoadHashSlotMigrationOutbox(ctx, excluded.HashSlot, excluded.SourceSlot, excluded.TargetSlot, excluded.SourceIndex); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LoadHashSlotMigrationOutbox(restored excluded) err = %v, want ErrNotFound", err)
	}
}
