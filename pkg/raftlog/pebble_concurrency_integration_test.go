//go:build integration

package raftlog

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func TestPebbleSnapshotReadRegistersBeforeNewerSnapshotGC(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{
		SnapshotPath:      filepath.Join(t.TempDir(), "snapshots"),
		SnapshotChunkSize: 4,
		SnapshotGCGrace:   0,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	store := db.ForSlot(20)
	oldSnap := raftpb.Snapshot{
		Data:     []byte("old-snapshot-payload"),
		Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
	}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &oldSnap})
	oldManifest := mustLoadPebbleManifest(t, db, SlotScope(20))
	oldDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(20)), oldManifest.SnapshotID)

	mutationDone := make(chan error, 1)
	db.snapshotReadAfterManifestHook = func(scope Scope, manifest SnapshotManifest) {
		if scope != SlotScope(20) || manifest.SnapshotID != oldManifest.SnapshotID {
			return
		}
		newSnap := raftpb.Snapshot{
			Data:     []byte("new-snapshot-payload"),
			Metadata: raftpb.SnapshotMetadata{Index: 6, Term: 2, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
		}
		go func() {
			if err := store.Save(ctx, multiraft.PersistentState{Snapshot: &newSnap}); err != nil {
				mutationDone <- err
				return
			}
			mutationDone <- db.runSnapshotGC(ctx)
		}()
		select {
		case err := <-mutationDone:
			if err != nil {
				t.Fatalf("new snapshot save/GC error = %v", err)
			}
			t.Fatalf("new snapshot save and GC completed before old read registered %s active", oldDir)
		case <-time.After(200 * time.Millisecond):
		}
	}

	got, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !reflect.DeepEqual(got, oldSnap) {
		t.Fatalf("Snapshot() = %#v, want %#v", got, oldSnap)
	}

	select {
	case err := <-mutationDone:
		if err != nil {
			t.Fatalf("new snapshot save/GC error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("new snapshot save/GC did not finish")
	}
}

func TestPebbleCloseDrainsConcurrentWritesBeforeClosingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	const writers = 16
	reqs := make([]*writeRequest, 0, writers)
	for i := 0; i < writers; i++ {
		hs := raftpb.HardState{Term: 1, Commit: 16}
		req := &writeRequest{
			scope: SlotScope(uint64(i + 1)),
			op: saveOp{state: withoutSnapshotData(multiraft.PersistentState{
				HardState: &hs,
				Entries:   benchEntries(1, 16, 1, 1024),
			})},
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

	reopened, err := Open(path, Options{})
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
	db, err := Open(path, Options{})
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

	reopened, err := Open(path, Options{})
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
