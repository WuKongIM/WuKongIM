package raftlog

import (
	"context"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func mustMarshalConfChange(t *testing.T, cc raftpb.ConfChange) []byte {
	t.Helper()

	data, err := cc.Marshal()
	if err != nil {
		t.Fatalf("ConfChange.Marshal() error = %v", err)
	}
	return data
}

func TestMemoryInitialStateIsEmpty(t *testing.T) {
	store := NewMemory()

	state, err := store.InitialState(context.Background())
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	if state.AppliedIndex != 0 {
		t.Fatalf("AppliedIndex = %d, want 0", state.AppliedIndex)
	}
	if !reflect.DeepEqual(state.HardState, raftpb.HardState{}) {
		t.Fatalf("HardState = %#v, want zero value", state.HardState)
	}
	if !reflect.DeepEqual(state.ConfState, raftpb.ConfState{}) {
		t.Fatalf("ConfState = %#v, want zero value", state.ConfState)
	}

	first, err := store.FirstIndex(context.Background())
	if err != nil {
		t.Fatalf("FirstIndex() error = %v", err)
	}
	if first != 1 {
		t.Fatalf("FirstIndex() = %d, want 1", first)
	}

	last, err := store.LastIndex(context.Background())
	if err != nil {
		t.Fatalf("LastIndex() error = %v", err)
	}
	if last != 0 {
		t.Fatalf("LastIndex() = %d, want 0", last)
	}
}

func TestMemorySaveAndReadRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()

	hs := raftpb.HardState{Term: 2, Vote: 1, Commit: 7}
	entries := []raftpb.Entry{
		{Index: 5, Term: 1, Data: []byte("a")},
		{Index: 6, Term: 2, Data: []byte("b")},
		{Index: 7, Term: 2, Data: []byte("c")},
	}

	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: &hs,
		Entries:   entries,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	state, err := store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	if !reflect.DeepEqual(state.HardState, hs) {
		t.Fatalf("HardState = %#v, want %#v", state.HardState, hs)
	}

	got, err := store.Entries(ctx, 6, 8, 0)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	want := entries[1:]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Entries(6,8) = %#v, want %#v", got, want)
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

func TestMemorySaveEntriesReplacesTailFromFirstIndex(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()

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

func TestMemorySaveSnapshotTrimsCoveredEntries(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()

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

	gotSnap, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !reflect.DeepEqual(gotSnap, snap) {
		t.Fatalf("Snapshot() = %#v, want %#v", gotSnap, snap)
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

func TestMemoryInitialStateDerivesConfStateFromCommittedEntriesWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()

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

func TestMemoryInitialStateAppliesPostSnapshotConfChanges(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()

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

func TestMemoryMarkAppliedUpdatesBootstrapState(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()

	if err := store.MarkApplied(ctx, 9); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	state, err := store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState() error = %v", err)
	}
	if state.AppliedIndex != 9 {
		t.Fatalf("AppliedIndex = %d, want 9", state.AppliedIndex)
	}
}

func TestUpdateLogMetaUsesSnapshotWhenEntriesAreTrimmed(t *testing.T) {
	meta := logMeta{AppliedIndex: 8}
	snap := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index: 8,
			Term:  2,
		},
	}

	if err := updateLogMeta(&meta, snap, nil, 8); err != nil {
		t.Fatalf("updateLogMeta() error = %v", err)
	}
	if meta.FirstIndex != 9 {
		t.Fatalf("FirstIndex = %d, want 9", meta.FirstIndex)
	}
	if meta.LastIndex != 8 {
		t.Fatalf("LastIndex = %d, want 8", meta.LastIndex)
	}
}

func TestMemoryReturnsClonedEntriesAndSnapshot(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()

	inputEntries := []raftpb.Entry{
		{Index: 5, Term: 1, Data: []byte("a")},
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

	gotEntries, err := store.Entries(ctx, 5, 6, 0)
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

	gotEntries[0].Data[0] = 'y'
	gotSnap.Data[0] = 'q'
	gotSnap.Metadata.ConfState.Voters[0] = 42

	reloadedEntries, err := store.Entries(ctx, 5, 6, 0)
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
