package raftlog

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/cockroachdb/pebble/v2"
	"go.etcd.io/raft/v3/raftpb"
)

func TestPebbleOpenForSlotReturnsStorage(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	if db.ForSlot(7) == nil {
		t.Fatal("ForSlot(7) returned nil storage")
	}
}

func TestPebbleForControllerReturnsStorage(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	if db.ForController() == nil {
		t.Fatal("ForController() returned nil storage")
	}
}

func TestPebbleOpenInitializesManifest(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	_, closer, err := db.db.Get(encodeManifestKey())
	if err != nil {
		t.Fatalf("manifest key missing: %v", err)
	}
	defer closer.Close()
}

func TestScopeEntryKeysSortByVersionScopeAndIndex(t *testing.T) {
	a := encodeEntryKey(SlotScope(7), 5)
	b := encodeEntryKey(SlotScope(7), 6)
	c := encodeEntryKey(SlotScope(8), 1)
	d := encodeEntryKey(ControllerScope(), 1)

	if bytes.Compare(a, b) >= 0 {
		t.Fatalf("entry keys for one scope did not sort by index")
	}
	if bytes.Compare(b, c) >= 0 {
		t.Fatalf("entry keys did not sort by scope before index")
	}
	if bytes.Equal(a, d) {
		t.Fatal("slot and controller entry keys collided")
	}
}

func TestPebbleControllerStateRoundTripAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	store := db.ForController()

	hs := raftpb.HardState{Term: 2, Vote: 1, Commit: 3}
	if err := store.Save(context.Background(), multiraft.PersistentState{
		HardState: &hs,
		Entries:   benchEntries(1, 3, 2, 16),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(context.Background(), 3); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	state, err := reopened.ForController().InitialState(context.Background())
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	if state.HardState.Commit != 3 {
		t.Fatalf("HardState.Commit = %d, want 3", state.HardState.Commit)
	}
	if state.AppliedIndex != 3 {
		t.Fatalf("AppliedIndex = %d, want 3", state.AppliedIndex)
	}
}

func TestScopeString(t *testing.T) {
	if got := SlotScope(7).String(); got != "slot/7" {
		t.Fatalf("SlotScope(7).String() = %q, want slot/7", got)
	}
	if got := ControllerScope().String(); got != "controller/1" {
		t.Fatalf("ControllerScope().String() = %q, want controller/1", got)
	}
}

func TestPebbleStateRoundTripAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	store := db.ForSlot(9)

	hs := raftpb.HardState{Term: 2, Vote: 1, Commit: 7}
	snap := raftpb.Snapshot{
		Data: []byte("snap"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 7,
			Term:  2,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2},
			},
		},
	}
	if err := store.Save(context.Background(), multiraft.PersistentState{
		HardState: &hs,
		Snapshot:  &snap,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(context.Background(), 7); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	state, err := reopened.ForSlot(9).InitialState(context.Background())
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	if !reflect.DeepEqual(state.HardState, hs) {
		t.Fatalf("HardState = %#v, want %#v", state.HardState, hs)
	}
	if state.AppliedIndex != 7 {
		t.Fatalf("AppliedIndex = %d, want 7", state.AppliedIndex)
	}
	if !reflect.DeepEqual(state.ConfState, snap.Metadata.ConfState) {
		t.Fatalf("ConfState = %#v, want %#v", state.ConfState, snap.Metadata.ConfState)
	}
}

func TestPebbleSnapshotRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	store := db.ForSlot(9)
	snap := raftpb.Snapshot{
		Data: []byte("snap"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 7,
			Term:  2,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2},
			},
		},
	}
	if err := store.Save(context.Background(), multiraft.PersistentState{
		Snapshot: &snap,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !reflect.DeepEqual(got, snap) {
		t.Fatalf("Snapshot() = %#v, want %#v", got, snap)
	}
}

func TestPebbleGroupsAreIndependent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	left := db.ForSlot(9)
	right := db.ForSlot(10)

	hs := raftpb.HardState{Term: 2, Vote: 1, Commit: 7}
	if err := left.Save(context.Background(), multiraft.PersistentState{
		HardState: &hs,
	}); err != nil {
		t.Fatalf("left.Save() error = %v", err)
	}
	if err := left.MarkApplied(context.Background(), 7); err != nil {
		t.Fatalf("left.MarkApplied() error = %v", err)
	}

	leftState, err := left.InitialState(context.Background())
	if err != nil {
		t.Fatalf("left.InitialState() error = %v", err)
	}
	if !reflect.DeepEqual(leftState.HardState, hs) {
		t.Fatalf("left HardState = %#v, want %#v", leftState.HardState, hs)
	}
	if leftState.AppliedIndex != 7 {
		t.Fatalf("left AppliedIndex = %d, want 7", leftState.AppliedIndex)
	}

	rightState, err := right.InitialState(context.Background())
	if err != nil {
		t.Fatalf("right.InitialState() error = %v", err)
	}
	if !reflect.DeepEqual(rightState.HardState, raftpb.HardState{}) {
		t.Fatalf("right HardState = %#v, want zero value", rightState.HardState)
	}
	if rightState.AppliedIndex != 0 {
		t.Fatalf("right AppliedIndex = %d, want 0", rightState.AppliedIndex)
	}
}

func TestPebbleSlotAndControllerAreIndependent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	mustSave(t, db.ForSlot(9), benchPersistentState(1, 2, 1, 8))
	mustMarkApplied(t, db.ForSlot(9), 2)
	mustSave(t, db.ForController(), benchPersistentState(1, 3, 2, 8))
	mustMarkApplied(t, db.ForController(), 3)

	slotState := mustInitialState(t, db.ForSlot(9))
	if slotState.HardState.Commit != 2 {
		t.Fatalf("slot commit = %d, want 2", slotState.HardState.Commit)
	}
	if slotState.AppliedIndex != 2 {
		t.Fatalf("slot applied = %d, want 2", slotState.AppliedIndex)
	}

	controllerState := mustInitialState(t, db.ForController())
	if controllerState.HardState.Commit != 3 {
		t.Fatalf("controller commit = %d, want 3", controllerState.HardState.Commit)
	}
	if controllerState.AppliedIndex != 3 {
		t.Fatalf("controller applied = %d, want 3", controllerState.AppliedIndex)
	}
}

func TestPebbleSaveEntriesReplacesTailFromFirstIndex(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 11)

	initial := []raftpb.Entry{
		{Index: 5, Term: 1, Data: []byte("a")},
		{Index: 6, Term: 2, Data: []byte("b")},
		{Index: 7, Term: 2, Data: []byte("c")},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Entries: initial}); err != nil {
		t.Fatalf("Save(initial) error = %v", err)
	}

	replacement := []raftpb.Entry{
		{Index: 6, Term: 3, Data: []byte("bb")},
		{Index: 7, Term: 3, Data: []byte("cc")},
		{Index: 8, Term: 3, Data: []byte("dd")},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Entries: replacement}); err != nil {
		t.Fatalf("Save(replacement) error = %v", err)
	}

	got, err := store.Entries(ctx, 5, 9, 0)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	want := []raftpb.Entry{
		initial[0],
		replacement[0],
		replacement[1],
		replacement[2],
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Entries(5,9) = %#v, want %#v", got, want)
	}
}

func TestPebbleSaveSnapshotTrimsCoveredEntries(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 12)

	entries := []raftpb.Entry{
		{Index: 5, Term: 1, Data: []byte("a")},
		{Index: 6, Term: 2, Data: []byte("b")},
		{Index: 7, Term: 2, Data: []byte("c")},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Entries: entries}); err != nil {
		t.Fatalf("Save(entries) error = %v", err)
	}

	snap := raftpb.Snapshot{
		Data: []byte("snap"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 6,
			Term:  2,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2},
			},
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("Save(snapshot) error = %v", err)
	}

	state, err := store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	if !reflect.DeepEqual(state.ConfState, snap.Metadata.ConfState) {
		t.Fatalf("ConfState = %#v, want %#v", state.ConfState, snap.Metadata.ConfState)
	}

	got, err := store.Entries(ctx, 5, 8, 0)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	want := []raftpb.Entry{entries[2]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Entries(5,8) = %#v, want %#v", got, want)
	}

	first, err := store.FirstIndex(ctx)
	if err != nil {
		t.Fatalf("FirstIndex() error = %v", err)
	}
	if first != 7 {
		t.Fatalf("FirstIndex() = %d, want 7", first)
	}

	term, err := store.Term(ctx, 6)
	if err != nil {
		t.Fatalf("Term() error = %v", err)
	}
	if term != 2 {
		t.Fatalf("Term(6) = %d, want 2", term)
	}
}

func TestPebbleEntriesWindowingAndTerm(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 13)

	entries := []raftpb.Entry{
		{Index: 5, Term: 1, Data: []byte("aa")},
		{Index: 6, Term: 2, Data: []byte("bbbb")},
		{Index: 7, Term: 2, Data: []byte("cccc")},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Entries: entries}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Entries(ctx, 5, 8, uint64(entries[0].Size()+entries[1].Size()-1))
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	want := []raftpb.Entry{entries[0]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Entries(5,8,maxSize) = %#v, want %#v", got, want)
	}

	first, err := store.FirstIndex(ctx)
	if err != nil {
		t.Fatalf("FirstIndex() error = %v", err)
	}
	if first != 5 {
		t.Fatalf("FirstIndex() = %d, want 5", first)
	}

	last, err := store.LastIndex(ctx)
	if err != nil {
		t.Fatalf("LastIndex() error = %v", err)
	}
	if last != 7 {
		t.Fatalf("LastIndex() = %d, want 7", last)
	}

	term, err := store.Term(ctx, 6)
	if err != nil {
		t.Fatalf("Term() error = %v", err)
	}
	if term != 2 {
		t.Fatalf("Term(6) = %d, want 2", term)
	}
}

func TestPebbleEntriesRoundTripAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	store := db.ForSlot(16)
	entries := []raftpb.Entry{
		{Index: 5, Term: 1, Data: []byte("a")},
		{Index: 6, Term: 2, Data: []byte("b")},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Entries: entries}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	got, err := reopened.ForSlot(16).Entries(ctx, 5, 7, 0)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if !reflect.DeepEqual(got, entries) {
		t.Fatalf("Entries(5,7) = %#v, want %#v", got, entries)
	}
}

func TestPebbleInitialStateDerivesConfStateFromCommittedEntriesWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 14)

	hs := raftpb.HardState{Term: 1, Commit: 3}
	entries := []raftpb.Entry{
		{
			Index: 1,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}),
		},
		{
			Index: 2,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 2}),
		},
		{
			Index: 3,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 3}),
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: &hs,
		Entries:   entries,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(ctx, 3); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	state, err := store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	want := raftpb.ConfState{Voters: []uint64{1, 2, 3}}
	if !reflect.DeepEqual(state.ConfState, want) {
		t.Fatalf("ConfState = %#v, want %#v", state.ConfState, want)
	}
}

func TestPebbleInitialStateAppliesPostSnapshotConfChanges(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 17)

	snap := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index: 2,
			Term:  1,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2},
			},
		},
	}
	hs := raftpb.HardState{Term: 2, Commit: 3}
	entries := []raftpb.Entry{
		{
			Index: 3,
			Term:  2,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 3}),
		},
	}

	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: &hs,
		Snapshot:  &snap,
		Entries:   entries,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(ctx, 3); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	state, err := store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	want := raftpb.ConfState{Voters: []uint64{1, 2, 3}}
	if !reflect.DeepEqual(state.ConfState, want) {
		t.Fatalf("ConfState = %#v, want %#v", state.ConfState, want)
	}
}

func TestPebbleInitialStateReopenUsesPersistedMetadata(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	store := db.ForSlot(21)

	hs := raftpb.HardState{Term: 2, Commit: 3}
	entries := []raftpb.Entry{
		{
			Index: 1,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}),
		},
		{
			Index: 2,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 2}),
		},
		{
			Index: 3,
			Term:  2,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 3}),
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{HardState: &hs, Entries: entries}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(ctx, 3); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}
	if _, err := store.(*pebbleStore).getValue(encodeGroupStateKey(SlotScope(21))); err != nil {
		t.Fatalf("group metadata missing before reopen: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	state, err := reopened.ForSlot(21).InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	want := raftpb.ConfState{Voters: []uint64{1, 2, 3}}
	if !reflect.DeepEqual(state.ConfState, want) {
		t.Fatalf("ConfState = %#v, want %#v", state.ConfState, want)
	}
	if _, err := reopened.ForSlot(21).(*pebbleStore).getValue(encodeGroupStateKey(SlotScope(21))); err != nil {
		t.Fatalf("group metadata missing after reopen: %v", err)
	}
}

func TestPebbleFirstAndLastIndexUsePersistedMetadataAfterSnapshotTrim(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 22)

	if err := store.Save(ctx, multiraft.PersistentState{
		Entries: benchEntries(5, 4, 1, 8),
	}); err != nil {
		t.Fatalf("Save(entries) error = %v", err)
	}
	snap := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index: 6,
			Term:  1,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2},
			},
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("Save(snapshot) error = %v", err)
	}
	if _, err := store.(*pebbleStore).getValue(encodeGroupStateKey(SlotScope(22))); err != nil {
		t.Fatalf("group metadata missing after snapshot trim: %v", err)
	}

	first, err := store.FirstIndex(ctx)
	if err != nil {
		t.Fatalf("FirstIndex() error = %v", err)
	}
	last, err := store.LastIndex(ctx)
	if err != nil {
		t.Fatalf("LastIndex() error = %v", err)
	}
	if first != 7 || last != 8 {
		t.Fatalf("FirstIndex()/LastIndex() = %d/%d, want 7/8", first, last)
	}
}

func TestPebbleInitialStateReopenBackfillsLegacyStoreMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	mustWriteLegacyPebbleState(t, path, SlotScope(31), legacyPebbleState{
		hardState: raftpb.HardState{Term: 2, Commit: 3},
		applied:   3,
		entries: []raftpb.Entry{
			{
				Index: 1,
				Term:  1,
				Type:  raftpb.EntryConfChange,
				Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}),
			},
			{
				Index: 2,
				Term:  1,
				Type:  raftpb.EntryConfChange,
				Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 2}),
			},
			{
				Index: 3,
				Term:  2,
				Type:  raftpb.EntryConfChange,
				Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 3}),
			},
		},
	})

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	state, err := db.ForSlot(31).InitialState(context.Background())
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	want := raftpb.ConfState{Voters: []uint64{1, 2, 3}}
	if !reflect.DeepEqual(state.ConfState, want) {
		t.Fatalf("ConfState = %#v, want %#v", state.ConfState, want)
	}
	if !hasScopeMetadata(t, db, SlotScope(31)) {
		t.Fatal("metadata was not backfilled after legacy reopen")
	}
}

func TestPebbleCloseDrainsConcurrentWritesBeforeClosingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	const writers = 16
	reqs := make([]*writeRequest, 0, writers)
	for i := 0; i < writers; i++ {
		hs := raftpb.HardState{Term: 1, Commit: 16}
		req := &writeRequest{
			scope: SlotScope(uint64(i + 1)),
			op: saveOp{state: multiraft.PersistentState{
				HardState: &hs,
				Entries:   benchEntries(1, 16, 1, 1024),
			}},
			done: make(chan error, 1),
		}
		db.writeCh <- req
		reqs = append(reqs, req)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	for _, req := range reqs {
		if err := <-req.done; err != nil {
			t.Fatalf("concurrent Save() error = %v", err)
		}
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	for group := uint64(1); group <= writers; group++ {
		last, err := reopened.ForSlot(group).LastIndex(context.Background())
		if err != nil {
			t.Fatalf("group %d LastIndex() error = %v", group, err)
		}
		if last != 16 {
			t.Fatalf("group %d LastIndex() = %d, want 16", group, last)
		}
	}
}

func TestPebbleConcurrentSaveAndMarkAppliedAreDurableAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	const writers = 8
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(group uint64) {
			store := db.ForSlot(group)
			hs := raftpb.HardState{Term: 1, Commit: 4}
			if err := store.Save(context.Background(), multiraft.PersistentState{
				HardState: &hs,
				Entries:   benchEntries(1, 4, 1, 32),
			}); err != nil {
				errCh <- err
				return
			}
			errCh <- store.MarkApplied(context.Background(), 4)
		}(uint64(i + 1))
	}

	for i := 0; i < writers; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent Save/MarkApplied error = %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	for group := uint64(1); group <= writers; group++ {
		state, err := reopened.ForSlot(group).InitialState(context.Background())
		if err != nil {
			t.Fatalf("group %d InitialState() error = %v", group, err)
		}
		if state.AppliedIndex != 4 {
			t.Fatalf("group %d AppliedIndex = %d, want 4", group, state.AppliedIndex)
		}
	}
}

func TestPebbleSameGroupSaveThenMarkAppliedPreservesOrder(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	store := db.ForSlot(51)

	hs := raftpb.HardState{Term: 2, Commit: 3}
	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: &hs,
		Entries:   benchEntries(1, 3, 2, 16),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(ctx, 3); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	state, err := reopened.ForSlot(51).InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	if state.HardState.Commit != 3 || state.AppliedIndex != 3 {
		t.Fatalf("commit/applied = %d/%d, want 3/3", state.HardState.Commit, state.AppliedIndex)
	}
}

func TestPebbleMarkAppliedMetadataWriteKeepsCachedEntriesBacking(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	store := db.ForSlot(52)
	hs := raftpb.HardState{Term: 2, Commit: 4}
	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: &hs,
		Entries:   benchEntries(1, 4, 2, 16),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	scope := SlotScope(52)
	before := cachedEntriesBackingPtr(db.stateCache[scope].entries)
	if before == 0 {
		t.Fatal("cached entries backing pointer = 0")
	}

	if err := store.MarkApplied(ctx, 4); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	after := cachedEntriesBackingPtr(db.stateCache[scope].entries)
	if after != before {
		t.Fatalf("cached entries backing changed after metadata-only MarkApplied(): got %x want %x", after, before)
	}
}

func TestPebbleMetadataTracksTailReplaceAndSnapshotTrim(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 52)

	if err := store.Save(ctx, multiraft.PersistentState{Entries: benchEntries(5, 4, 1, 16)}); err != nil {
		t.Fatalf("Save(initial) error = %v", err)
	}
	if err := store.Save(ctx, multiraft.PersistentState{Entries: benchEntries(7, 3, 2, 16)}); err != nil {
		t.Fatalf("Save(replace) error = %v", err)
	}
	snap := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index: 8,
			Term:  2,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2},
			},
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("Save(snapshot) error = %v", err)
	}

	first, err := store.FirstIndex(ctx)
	if err != nil {
		t.Fatalf("FirstIndex() error = %v", err)
	}
	last, err := store.LastIndex(ctx)
	if err != nil {
		t.Fatalf("LastIndex() error = %v", err)
	}
	if first != 9 || last != 9 {
		t.Fatalf("FirstIndex()/LastIndex() = %d/%d, want 9/9", first, last)
	}
}

func cachedEntriesBackingPtr(entries []raftpb.Entry) uintptr {
	if len(entries) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.SliceData(entries)))
}

func TestPebbleReturnsClonedData(t *testing.T) {
	ctx := context.Background()
	store := openTestPebbleStore(t, 15)

	inputEntries := []raftpb.Entry{
		{Index: 6, Term: 1, Data: []byte("a")},
	}
	inputSnap := raftpb.Snapshot{
		Data: []byte("snap"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 5,
			Term:  1,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1, 2},
			},
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{
		Entries:  inputEntries,
		Snapshot: &inputSnap,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	inputEntries[0].Data[0] = 'z'
	inputSnap.Data[0] = 'x'
	inputSnap.Metadata.ConfState.Voters[0] = 99

	gotEntries, err := store.Entries(ctx, 6, 7, 0)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if string(gotEntries[0].Data) != "a" {
		t.Fatalf("stored entry data = %q, want %q", gotEntries[0].Data, "a")
	}

	gotSnap, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if string(gotSnap.Data) != "snap" {
		t.Fatalf("stored snapshot data = %q, want %q", gotSnap.Data, "snap")
	}
	if gotSnap.Metadata.ConfState.Voters[0] != 1 {
		t.Fatalf("stored conf state voters = %#v, want %#v", gotSnap.Metadata.ConfState.Voters, []uint64{1, 2})
	}

	gotSnap.Data[0] = 'q'
	gotSnap.Metadata.ConfState.Voters[0] = 42
	gotEntries[0].Data[0] = 'y'

	reloadedEntries, err := store.Entries(ctx, 6, 7, 0)
	if err != nil {
		t.Fatalf("Entries() reload error = %v", err)
	}
	if string(reloadedEntries[0].Data) != "a" {
		t.Fatalf("reloaded entry data = %q, want %q", reloadedEntries[0].Data, "a")
	}

	reloadedSnap, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() reload error = %v", err)
	}
	if string(reloadedSnap.Data) != "snap" {
		t.Fatalf("reloaded snapshot data = %q, want %q", reloadedSnap.Data, "snap")
	}
	if reloadedSnap.Metadata.ConfState.Voters[0] != 1 {
		t.Fatalf("reloaded conf state voters = %#v, want %#v", reloadedSnap.Metadata.ConfState.Voters, []uint64{1, 2})
	}
}

func TestGroupMetaUnmarshalRejectsTruncatedData(t *testing.T) {
	var meta groupMeta

	if err := meta.Unmarshal(nil); err == nil {
		t.Fatal("Unmarshal(nil) error = nil, want error")
	}
	if err := meta.Unmarshal(make([]byte, groupMetaHeaderSize-1)); err == nil {
		t.Fatal("Unmarshal(short) error = nil, want error")
	}

	// Build a valid header but with confState length exceeding remaining data.
	buf := make([]byte, groupMetaHeaderSize+2)
	// Set confState size to 10 but only provide 2 bytes after header.
	buf[40] = 0
	buf[41] = 0
	buf[42] = 0
	buf[43] = 10
	if err := meta.Unmarshal(buf); err == nil {
		t.Fatal("Unmarshal(bad confState size) error = nil, want error")
	}
}

func TestLoadAppliedIndexRejectsInvalidEncoding(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	store := db.ForSlot(99).(*pebbleStore)

	// Write a 3-byte value to the applied index key (should be 8).
	if err := db.db.Set(encodeAppliedIndexKey(SlotScope(99)), []byte{0x01, 0x02, 0x03}, pebble.Sync); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	_, err = store.loadAppliedIndex()
	if err == nil {
		t.Fatal("loadAppliedIndex() error = nil, want invalid encoding error")
	}
}

func openTestPebbleStore(t *testing.T, group uint64) multiraft.Storage {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "raft"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return db.ForSlot(group)
}
