package raftlog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/batchrepr"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestPebbleOpenOptionsDefaultExternalSnapshotRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	if db.options.SnapshotPath != path+"-snapshots" {
		t.Fatalf("SnapshotPath = %q, want %q", db.options.SnapshotPath, path+"-snapshots")
	}
	if db.options.SnapshotChunkSize != uint64(8<<20) {
		t.Fatalf("SnapshotChunkSize = %d, want %d", db.options.SnapshotChunkSize, uint64(8<<20))
	}
	if db.options.WriteBatchMaxWait != 5*time.Millisecond {
		t.Fatalf("WriteBatchMaxWait = %v, want %v", db.options.WriteBatchMaxWait, 5*time.Millisecond)
	}
	if db.options.WriteBatchMaxItems != 128 {
		t.Fatalf("WriteBatchMaxItems = %d, want 128", db.options.WriteBatchMaxItems)
	}
}

func TestPebbleOpenRejectsNegativeSnapshotGCGrace(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "raft"), Options{SnapshotGCGrace: -time.Second})
	if err == nil {
		t.Fatal("Open() error = nil, want error")
	}
}

func TestPebbleOpenRejectsInvalidWriteBatchBounds(t *testing.T) {
	for _, opts := range []Options{
		{WriteBatchMaxWait: -time.Nanosecond},
		{WriteBatchMaxItems: -1},
		{WriteBatchMaxItems: maxWriteBatchItems + 1},
	} {
		db, err := Open(filepath.Join(t.TempDir(), "raft"), opts)
		if err == nil {
			_ = db.Close()
			t.Fatalf("Open(%+v) error = nil, want invalid write batch bound", opts)
		}
	}
}

func TestPebbleOpenForSlotReturnsStorage(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
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
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
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

func TestPebbleMetricsSnapshotReportsPhysicalStore(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	snapshot := db.MetricsSnapshot()
	if snapshot.DiskSpaceUsageBytes == 0 {
		t.Fatalf("DiskSpaceUsageBytes = 0, want physical usage")
	}
	if snapshot.ReadAmplification < 0 {
		t.Fatalf("ReadAmplification = %d, want non-negative value", snapshot.ReadAmplification)
	}
}

func TestPebbleOpenInitializesManifest(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
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

func TestPebblePureEntryAppendDoesNotStageRangeDelete(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	scope := SlotScope(7)
	state := &scopeWriteState{
		entries: benchEntries(1, 1, 1, 8),
		meta:    logMeta{FirstIndex: 1, LastIndex: 1},
	}
	batch := db.db.NewBatch()
	defer batch.Close()
	op := saveOp{state: persistentWriteState{Entries: benchEntries(2, 2, 1, 8)}}
	if err := op.apply(batch, state, &pebbleStore{db: db, scope: scope}); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	reader := batchrepr.Read(batch.Repr())
	rangeDeletes := 0
	for {
		kind, _, _, ok, err := reader.Next()
		if err != nil {
			t.Fatalf("read batch: %v", err)
		}
		if !ok {
			break
		}
		if kind == pebble.InternalKeyKindRangeDelete {
			rangeDeletes++
		}
	}
	if rangeDeletes != 0 {
		t.Fatalf("range deletes = %d, want 0 for a pure tail append", rangeDeletes)
	}
}

func TestPebbleEntryOverwriteStagesRangeDelete(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	scope := SlotScope(7)
	state := &scopeWriteState{
		entries: benchEntries(1, 3, 1, 8),
		meta:    logMeta{FirstIndex: 1, LastIndex: 3},
	}
	batch := db.db.NewBatch()
	defer batch.Close()
	op := saveOp{state: persistentWriteState{Entries: benchEntries(2, 2, 2, 8)}}
	if err := op.apply(batch, state, &pebbleStore{db: db, scope: scope}); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	reader := batchrepr.Read(batch.Repr())
	rangeDeletes := 0
	for {
		kind, _, _, ok, err := reader.Next()
		if err != nil {
			t.Fatalf("read batch: %v", err)
		}
		if !ok {
			break
		}
		if kind == pebble.InternalKeyKindRangeDelete {
			rangeDeletes++
		}
	}
	if rangeDeletes != 1 {
		t.Fatalf("range deletes = %d, want 1 for a suffix overwrite", rangeDeletes)
	}
}

func TestPebbleLaterSnapshotDeletesOnlyCurrentEntryPrefix(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	scope := SlotScope(7)
	oldManifest := validSnapshotManifest(scope)
	oldManifest.Index = 6
	oldManifest.Term = 1
	newManifest := validSnapshotManifest(scope)
	newManifest.Index = 8
	newManifest.Term = 2
	state := &scopeWriteState{
		snapshot:         raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 6, Term: 1}},
		snapshotManifest: &oldManifest,
		entries:          benchEntries(7, 4, 2, 8),
		meta:             logMeta{FirstIndex: 7, LastIndex: 10, SnapshotIndex: 6, SnapshotTerm: 1},
	}
	batch := db.db.NewBatch()
	defer batch.Close()
	op := saveOp{state: persistentWriteState{
		Snapshot:         &raftpb.SnapshotMetadata{Index: 8, Term: 2},
		SnapshotManifest: &newManifest,
	}}
	if err := op.apply(batch, state, &pebbleStore{db: db, scope: scope}); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	reader := batchrepr.Read(batch.Repr())
	for {
		kind, start, end, ok, err := reader.Next()
		if err != nil {
			t.Fatalf("read batch: %v", err)
		}
		if !ok {
			t.Fatal("snapshot batch has no range delete")
		}
		if kind != pebble.InternalKeyKindRangeDelete {
			continue
		}
		if want := encodeEntryKey(scope, 7); !bytes.Equal(start, want) {
			t.Fatalf("range delete start = %x, want current FirstIndex %x", start, want)
		}
		if want := encodeEntryKey(scope, 9); !bytes.Equal(end, want) {
			t.Fatalf("range delete end = %x, want snapshot successor %x", end, want)
		}
		break
	}
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

	db, err := Open(path, Options{})
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

	reopened, err := Open(path, Options{})
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

func TestPebbleControllerSnapshotTrimsCoveredEntries(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	store := db.ForController()
	hs := raftpb.HardState{Term: 3, Vote: 1, Commit: 8}
	initial := benchEntries(1, 8, 3, 8)
	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: &hs,
		Entries:   initial,
	}); err != nil {
		t.Fatalf("Save(entries) error = %v", err)
	}

	snap := raftpb.Snapshot{
		Data: []byte("controller-meta"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 6,
			Term:  3,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1},
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
	if first != 7 {
		t.Fatalf("FirstIndex() = %d, want 7", first)
	}
	last, err := store.LastIndex(ctx)
	if err != nil {
		t.Fatalf("LastIndex() error = %v", err)
	}
	if last != 8 {
		t.Fatalf("LastIndex() = %d, want 8", last)
	}
	gotSnap, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !reflect.DeepEqual(gotSnap.Metadata, snap.Metadata) {
		t.Fatalf("Snapshot metadata = %#v, want %#v", gotSnap.Metadata, snap.Metadata)
	}

	entries, err := store.Entries(ctx, 1, 9, 0)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	want := []raftpb.Entry{initial[6], initial[7]}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("Entries(1,9) = %#v, want %#v", entries, want)
	}
}

func TestPebbleStateRoundTripAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")

	db, err := Open(path, Options{})
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

	reopened, err := Open(path, Options{})
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
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
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

func TestPebbleSaveSnapshotWritesExternalChunksOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{SnapshotPath: filepath.Join(t.TempDir(), "snapshots"), SnapshotChunkSize: 16})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	db.snapshotStore.nonce = func() (string, error) { return "0000000000000001", nil }
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	payload := append([]byte("large-snapshot-payload-marker"), bytes.Repeat([]byte("x"), 128)...)
	snap := raftpb.Snapshot{
		Data: payload,
		Metadata: raftpb.SnapshotMetadata{
			Index:     7,
			Term:      2,
			ConfState: raftpb.ConfState{Voters: []uint64{1, 2}},
		},
	}
	if err := db.ForSlot(9).Save(context.Background(), multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("Save(snapshot) error = %v", err)
	}

	value := mustGetRawPebbleValue(t, db.db, encodeSnapshotKey(SlotScope(9)))
	if bytes.Contains(value, []byte("large-snapshot-payload-marker")) {
		t.Fatalf("snapshot Pebble value contains inline payload marker")
	}
	manifest := mustLoadPebbleManifest(t, db, SlotScope(9))
	if manifest.TotalSize != uint64(len(payload)) || manifest.ChunkCount == 0 {
		t.Fatalf("manifest size/chunks = %d/%d, want %d/non-zero", manifest.TotalSize, manifest.ChunkCount, len(payload))
	}
	if _, err := os.Stat(filepath.Join(db.snapshotStore.scopeDir(SlotScope(9)), manifest.SnapshotID, chunkFileName(0))); err != nil {
		t.Fatalf("external chunk missing: %v", err)
	}
}

func TestPebbleSnapshotRoundTripFromExternalChunks(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{SnapshotPath: filepath.Join(t.TempDir(), "snapshots"), SnapshotChunkSize: 5})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	snap := raftpb.Snapshot{
		Data:     []byte("external-round-trip"),
		Metadata: raftpb.SnapshotMetadata{Index: 11, Term: 4, ConfState: raftpb.ConfState{Voters: []uint64{3, 4}}},
	}
	store := db.ForSlot(9)
	if err := store.Save(context.Background(), multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("Save(snapshot) error = %v", err)
	}

	got, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !reflect.DeepEqual(got, snap) {
		t.Fatalf("Snapshot() = %#v, want %#v", got, snap)
	}

	manifest := mustLoadPebbleManifest(t, db, SlotScope(9))
	chunkPath := filepath.Join(db.snapshotStore.scopeDir(SlotScope(9)), manifest.SnapshotID, chunkFileName(0))
	if err := os.WriteFile(chunkPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatalf("corrupt external chunk: %v", err)
	}
	if _, err := store.Snapshot(context.Background()); err == nil {
		t.Fatal("Snapshot() error = nil, want checksum error from external chunk")
	}
}

func TestSnapshotManifestWithMissingChunkFailsSnapshotRead(t *testing.T) {
	db, store := openPebbleDBForSnapshotTest(t, 30)
	snap := raftpb.Snapshot{
		Data:     []byte("payload stored outside pebble"),
		Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
	}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &snap})

	manifest := mustLoadPebbleManifest(t, db, SlotScope(30))
	chunkPath := filepath.Join(db.snapshotStore.scopeDir(SlotScope(30)), manifest.SnapshotID, chunkFileName(0))
	if err := os.Remove(chunkPath); err != nil {
		t.Fatalf("Remove(%q) error = %v", chunkPath, err)
	}

	if got, err := store.Snapshot(context.Background()); err == nil {
		t.Fatalf("Snapshot() = %#v, nil error; want missing external chunk error", got)
	}
}

func TestSnapshotSaveFailureDoesNotWriteManifestOrTrimEntries(t *testing.T) {
	db, store := openPebbleDBForSnapshotTest(t, 31)
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Data: []byte("one")},
		{Index: 2, Term: 1, Data: []byte("two")},
		{Index: 3, Term: 1, Data: []byte("three")},
	}
	mustSave(t, store, multiraft.PersistentState{Entries: entries})
	db.snapshotStore.nonce = func() (string, error) { return "0000000000000031", nil }

	injected := errors.New("injected chunk write failure")
	originalWriteFile := snapshotWriteFile
	snapshotWriteFile = func(path string, data []byte) error { return injected }
	defer func() { snapshotWriteFile = originalWriteFile }()

	snap := raftpb.Snapshot{
		Data:     []byte("snapshot that fails before pebble commit"),
		Metadata: raftpb.SnapshotMetadata{Index: 2, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
	}
	err := store.Save(context.Background(), multiraft.PersistentState{Snapshot: &snap})
	if !errors.Is(err, injected) {
		t.Fatalf("Save(snapshot) error = %v, want injected chunk write failure", err)
	}

	if _, closer, err := db.db.Get(encodeSnapshotKey(SlotScope(31))); err == nil {
		closer.Close()
		t.Fatal("snapshot manifest exists after chunk write failure")
	} else if !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("snapshot manifest Get() error = %v, want not found", err)
	}
	if got := mustEntries(t, store, 1, 4, 0); !reflect.DeepEqual(got, entries) {
		t.Fatalf("Entries() = %#v, want untrimmed entries %#v", got, entries)
	}
	tmpDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(31)), ".tmp-snap-0000000000000002-0000000000000001-0000000000000031")
	assertPathMissing(t, tmpDir)
	if db.isActiveSnapshotPath(tmpDir) {
		t.Fatalf("tmp snapshot path %q is still active after failed save", tmpDir)
	}
}

func TestSnapshotPebbleCommitFailureLeavesRetryableOrphanDir(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{
		SnapshotPath:      filepath.Join(t.TempDir(), "snapshots"),
		SnapshotChunkSize: 8,
		SnapshotGCGrace:   0,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	db.snapshotStore.nonce = func() (string, error) { return "0000000000000032", nil }
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	injected := errors.New("injected pebble commit failure")
	db.writeCommitTestHook = func() error { return injected }
	snap := raftpb.Snapshot{
		Data:     []byte("payload"),
		Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
	}
	err = db.ForSlot(32).Save(context.Background(), multiraft.PersistentState{Snapshot: &snap})
	if !errors.Is(err, injected) {
		t.Fatalf("Save(snapshot) error = %v, want injected commit failure", err)
	}

	finalDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(32)), "snap-0000000000000005-0000000000000001-0000000000000032")
	assertPathExists(t, finalDir)
	if db.isActiveSnapshotPath(finalDir) {
		t.Fatalf("final snapshot path %q is still active after failed commit", finalDir)
	}
	if _, closer, err := db.db.Get(encodeSnapshotKey(SlotScope(32))); err == nil {
		closer.Close()
		t.Fatal("snapshot manifest exists after failed commit")
	} else if !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("snapshot manifest Get() error = %v, want not found", err)
	}
	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC() error = %v", err)
	}
	assertPathMissing(t, finalDir)
}

func TestSnapshotRetryBeforeGCSucceedsAfterCommitFailure(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{
		SnapshotPath:      filepath.Join(t.TempDir(), "snapshots"),
		SnapshotChunkSize: 8,
		SnapshotGCGrace:   time.Hour,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	nonces := []string{"0000000000000033", "0000000000000034"}
	db.snapshotStore.nonce = func() (string, error) {
		if len(nonces) == 0 {
			t.Fatal("snapshot nonce requested too many times")
		}
		nonce := nonces[0]
		nonces = nonces[1:]
		return nonce, nil
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	injected := errors.New("injected pebble commit failure")
	db.writeCommitTestHook = func() error { return injected }
	snap := raftpb.Snapshot{
		Data:     []byte("retry payload"),
		Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
	}
	if err := db.ForSlot(33).Save(context.Background(), multiraft.PersistentState{Snapshot: &snap}); !errors.Is(err, injected) {
		t.Fatalf("first Save(snapshot) error = %v, want injected commit failure", err)
	}
	orphanDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(33)), "snap-0000000000000005-0000000000000001-0000000000000033")
	assertPathExists(t, orphanDir)

	db.writeCommitTestHook = nil
	if err := db.ForSlot(33).Save(context.Background(), multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("retry Save(snapshot) error = %v", err)
	}
	manifest := mustLoadPebbleManifest(t, db, SlotScope(33))
	if manifest.SnapshotID != "snap-0000000000000005-0000000000000001-0000000000000034" {
		t.Fatalf("manifest SnapshotID = %q, want retry to use a new snapshot ID", manifest.SnapshotID)
	}
	assertPathExists(t, orphanDir)
	assertPathExists(t, filepath.Join(db.snapshotStore.scopeDir(SlotScope(33)), manifest.SnapshotID))
}

func TestPebbleCurrentMetaUsesAtomicManifestView(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{
		SnapshotPath:      filepath.Join(t.TempDir(), "snapshots"),
		SnapshotChunkSize: 4,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	store := db.ForSlot(22)
	oldSnap := raftpb.Snapshot{
		Data:     []byte("old"),
		Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
	}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &oldSnap})

	var hookRan bool
	db.currentMetaAfterMetaLoadHook = func(scope Scope) {
		if hookRan || scope != SlotScope(22) {
			return
		}
		hookRan = true
		newSnap := raftpb.Snapshot{
			Data:     []byte("new"),
			Metadata: raftpb.SnapshotMetadata{Index: 6, Term: 2, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
		}
		if err := store.Save(ctx, multiraft.PersistentState{Snapshot: &newSnap}); err != nil {
			t.Fatalf("Save(new snapshot) error = %v", err)
		}
	}

	first, err := store.FirstIndex(ctx)
	if err != nil {
		t.Fatalf("FirstIndex() error = %v", err)
	}
	if first != 5+1 && first != 6+1 {
		t.Fatalf("FirstIndex() = %d, want old or new snapshot first index", first)
	}
	if !hookRan {
		t.Fatal("currentMeta hook did not run")
	}
}

func TestPebbleSaveSnapshotCleansPublishedDirWhenCloseWinsAfterPublish(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{
		SnapshotPath:      filepath.Join(t.TempDir(), "snapshots"),
		SnapshotChunkSize: 4,
		SnapshotGCGrace:   0,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	db.snapshotStore.nonce = func() (string, error) { return "00000000000000ab", nil }

	finalDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(23)), "snap-0000000000000005-0000000000000001-00000000000000ab")
	db.snapshotAfterPublishTestHook = func(staged *stagedSnapshot) error {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		db.snapshotAfterPublishTestHook = nil
		return nil
	}

	snap := raftpb.Snapshot{
		Data:     []byte("payload"),
		Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
	}
	err = db.ForSlot(23).Save(context.Background(), multiraft.PersistentState{Snapshot: &snap})
	if !errors.Is(err, errDBClosing) || err.Error() != errDBClosing.Error() {
		t.Fatalf("Save(snapshot) error = %v, want db closing", err)
	}
	if _, err := os.Stat(finalDir); !os.IsNotExist(err) {
		t.Fatalf("published snapshot dir stat error = %v, want not exist", err)
	}
}

func TestPebbleSaveSnapshotCleansFinalDirWhenPublishFsyncFails(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{
		SnapshotPath:      filepath.Join(t.TempDir(), "snapshots"),
		SnapshotChunkSize: 4,
		SnapshotGCGrace:   0,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	db.snapshotStore.nonce = func() (string, error) { return "00000000000000ac", nil }
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	finalDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(24)), "snap-0000000000000005-0000000000000001-00000000000000ac")
	publishParent := filepath.Dir(finalDir)
	fsyncErr := errors.New("injected publish parent fsync failure")
	originalFsync := snapshotFsyncDir
	var finalParentFsyncs int
	snapshotFsyncDir = func(path string) error {
		if sameCleanPath(path, publishParent) {
			finalParentFsyncs++
			if finalParentFsyncs == 2 {
				return fsyncErr
			}
		}
		return originalFsync(path)
	}
	defer func() { snapshotFsyncDir = originalFsync }()

	snap := raftpb.Snapshot{
		Data:     []byte("payload"),
		Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
	}
	err = db.ForSlot(24).Save(context.Background(), multiraft.PersistentState{Snapshot: &snap})
	if !errors.Is(err, fsyncErr) {
		t.Fatalf("Save(snapshot) error = %v, want injected fsync error", err)
	}
	if _, err := os.Stat(finalDir); !os.IsNotExist(err) {
		t.Fatalf("published snapshot dir stat error = %v, want not exist", err)
	}
}

func TestPebbleSaveSnapshotCleansPublishedDirWhenCommitFails(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{
		SnapshotPath:      filepath.Join(t.TempDir(), "snapshots"),
		SnapshotChunkSize: 4,
		SnapshotGCGrace:   0,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	db.snapshotStore.nonce = func() (string, error) { return "00000000000000aa", nil }
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	injected := errors.New("injected post-publish failure")
	db.snapshotAfterPublishTestHook = func(staged *stagedSnapshot) error {
		return injected
	}

	snap := raftpb.Snapshot{
		Data:     []byte("payload"),
		Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}},
	}
	err = db.ForSlot(21).Save(context.Background(), multiraft.PersistentState{Snapshot: &snap})
	if !errors.Is(err, injected) {
		t.Fatalf("Save(snapshot) error = %v, want injected error", err)
	}

	finalDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(21)), "snap-0000000000000005-0000000000000001-00000000000000aa")
	if _, err := os.Stat(finalDir); !os.IsNotExist(err) {
		t.Fatalf("published snapshot dir stat error = %v, want not exist", err)
	}
	value, closer, err := db.db.Get(encodeSnapshotKey(SlotScope(21)))
	if err == nil {
		closer.Close()
		t.Fatalf("snapshot manifest value = %x, want missing", value)
	}
	if !errors.Is(err, pebble.ErrNotFound) {
		t.Fatalf("snapshot manifest Get() error = %v, want not found", err)
	}
}

func TestPebbleTermAtSnapshotIndexUsesManifest(t *testing.T) {
	db, store := openPebbleDBForSnapshotTest(t, 9)
	snap := raftpb.Snapshot{Data: []byte("payload"), Metadata: raftpb.SnapshotMetadata{Index: 6, Term: 3, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &snap})
	manifest := mustLoadPebbleManifest(t, db, SlotScope(9))
	if err := os.RemoveAll(filepath.Join(db.snapshotStore.scopeDir(SlotScope(9)), manifest.SnapshotID)); err != nil {
		t.Fatalf("RemoveAll(snapshot dir) error = %v", err)
	}
	if got := mustTerm(t, store, 6); got != 3 {
		t.Fatalf("Term(snapshot index) = %d, want 3", got)
	}
}

func TestPebbleSaveSnapshotTrimsCoveredEntriesWithManifest(t *testing.T) {
	ctx := context.Background()
	db, store := openPebbleDBForSnapshotTest(t, 10)
	entries := []raftpb.Entry{{Index: 5, Term: 1, Data: []byte("a")}, {Index: 6, Term: 2, Data: []byte("b")}, {Index: 7, Term: 2, Data: []byte("c")}}
	mustSave(t, store, multiraft.PersistentState{Entries: entries})
	snap := raftpb.Snapshot{Data: []byte("payload"), Metadata: raftpb.SnapshotMetadata{Index: 6, Term: 2, ConfState: raftpb.ConfState{Voters: []uint64{1, 2}}}}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &snap})
	if value := mustGetRawPebbleValue(t, db.db, encodeSnapshotKey(SlotScope(10))); bytes.Contains(value, []byte("payload")) {
		t.Fatalf("snapshot Pebble value contains inline payload")
	}
	if got := mustEntries(t, store, 5, 8, 0); !reflect.DeepEqual(got, []raftpb.Entry{entries[2]}) {
		t.Fatalf("Entries() = %#v, want only uncovered entry", got)
	}
	if first, err := store.FirstIndex(ctx); err != nil || first != 7 {
		t.Fatalf("FirstIndex() = %d, %v; want 7, nil", first, err)
	}
}

func TestPebbleInitialStateUsesSnapshotManifestConfState(t *testing.T) {
	db, store := openPebbleDBForSnapshotTest(t, 11)
	snap := raftpb.Snapshot{Data: []byte("payload"), Metadata: raftpb.SnapshotMetadata{Index: 8, Term: 2, ConfState: raftpb.ConfState{Voters: []uint64{4, 5}}}}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &snap})
	manifest := mustLoadPebbleManifest(t, db, SlotScope(11))
	if err := os.RemoveAll(filepath.Join(db.snapshotStore.scopeDir(SlotScope(11)), manifest.SnapshotID)); err != nil {
		t.Fatalf("RemoveAll(snapshot dir) error = %v", err)
	}
	state := mustInitialState(t, store)
	if !reflect.DeepEqual(state.ConfState, snap.Metadata.ConfState) {
		t.Fatalf("ConfState = %#v, want %#v", state.ConfState, snap.Metadata.ConfState)
	}
}

func TestPebbleEntryOnlySaveDoesNotReadSnapshotChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	snapshotPath := filepath.Join(t.TempDir(), "snapshots")
	db, err := Open(path, Options{SnapshotPath: snapshotPath, SnapshotChunkSize: 8})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	store := db.ForSlot(12)
	snap := raftpb.Snapshot{Data: []byte("payload"), Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &snap})
	manifest := mustLoadPebbleManifest(t, db, SlotScope(12))
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	db, err = Open(path, Options{SnapshotPath: snapshotPath, SnapshotChunkSize: 8})
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := os.RemoveAll(filepath.Join(db.snapshotStore.scopeDir(SlotScope(12)), manifest.SnapshotID)); err != nil {
		t.Fatalf("RemoveAll(snapshot dir) error = %v", err)
	}
	mustSave(t, db.ForSlot(12), multiraft.PersistentState{Entries: []raftpb.Entry{{Index: 6, Term: 2}}})
}

func TestPebbleMarkAppliedDoesNotReadSnapshotChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raft")
	snapshotPath := filepath.Join(t.TempDir(), "snapshots")
	db, err := Open(path, Options{SnapshotPath: snapshotPath, SnapshotChunkSize: 8})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	store := db.ForSlot(13)
	snap := raftpb.Snapshot{Data: []byte("payload"), Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &snap})
	manifest := mustLoadPebbleManifest(t, db, SlotScope(13))
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	db, err = Open(path, Options{SnapshotPath: snapshotPath, SnapshotChunkSize: 8})
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := os.RemoveAll(filepath.Join(db.snapshotStore.scopeDir(SlotScope(13)), manifest.SnapshotID)); err != nil {
		t.Fatalf("RemoveAll(snapshot dir) error = %v", err)
	}
	mustMarkApplied(t, db.ForSlot(13), 5)
}

func TestPebbleSaveSnapshotWorkerRequestDoesNotCarrySnapshotData(t *testing.T) {
	snap := raftpb.Snapshot{Data: []byte("must-not-enter-worker"), Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1}}
	st := withoutSnapshotData(multiraft.PersistentState{Snapshot: &snap})
	if st.Snapshot == nil || st.Snapshot.Index != snap.Metadata.Index || st.Snapshot.Term != snap.Metadata.Term {
		t.Fatalf("worker state snapshot metadata = %#v, want %#v", st.Snapshot, snap.Metadata)
	}
	if len(st.Entries) != 0 || st.HardState != nil {
		t.Fatalf("worker state carried unexpected non-snapshot fields: %#v", st)
	}
}

func TestPebbleRejectsStaleSnapshotWithoutMutatingState(t *testing.T) {
	db, store := openPebbleDBForSnapshotTest(t, 14)
	initial := raftpb.Snapshot{Data: []byte("initial"), Metadata: raftpb.SnapshotMetadata{Index: 10, Term: 3, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &initial})
	before := mustGetRawPebbleValue(t, db.db, encodeSnapshotKey(SlotScope(14)))
	stale := raftpb.Snapshot{Data: []byte("stale"), Metadata: raftpb.SnapshotMetadata{Index: 9, Term: 3, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	err := store.Save(context.Background(), multiraft.PersistentState{Snapshot: &stale})
	if !errors.Is(err, raft.ErrSnapOutOfDate) {
		t.Fatalf("Save(stale snapshot) error = %v, want ErrSnapOutOfDate", err)
	}
	after := mustGetRawPebbleValue(t, db.db, encodeSnapshotKey(SlotScope(14)))
	if !bytes.Equal(after, before) {
		t.Fatalf("snapshot manifest mutated after stale save")
	}
}

func TestPebbleSameIndexSnapshotIsIdempotentOnlyWhenManifestMatches(t *testing.T) {
	db, store := openPebbleDBForSnapshotTest(t, 15)
	snap := raftpb.Snapshot{Data: []byte("same"), Metadata: raftpb.SnapshotMetadata{Index: 7, Term: 2, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &snap})
	before := mustGetRawPebbleValue(t, db.db, encodeSnapshotKey(SlotScope(15)))
	if err := store.Save(context.Background(), multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("Save(same snapshot) error = %v", err)
	}
	if after := mustGetRawPebbleValue(t, db.db, encodeSnapshotKey(SlotScope(15))); !bytes.Equal(after, before) {
		t.Fatalf("same-index identical snapshot changed manifest")
	}
	different := snap
	different.Data = []byte("different")
	if err := store.Save(context.Background(), multiraft.PersistentState{Snapshot: &different}); err == nil {
		t.Fatalf("Save(same-index different snapshot) error = nil, want mismatch")
	}
	if after := mustGetRawPebbleValue(t, db.db, encodeSnapshotKey(SlotScope(15))); !bytes.Equal(after, before) {
		t.Fatalf("same-index mismatched snapshot mutated manifest")
	}
}

func TestPebbleReplaceSnapshotAtomicallyRewritesAppliedBoundary(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "raft")
	snapshotPath := filepath.Join(root, "snapshots")
	db, err := Open(path, Options{
		SnapshotPath: snapshotPath, SnapshotChunkSize: 8,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	store := db.ForSlot(115)
	hardState := raftpb.HardState{Term: 2, Vote: 1, Commit: 7}
	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: &hardState,
		Entries:   benchEntries(1, 7, 2, 8),
	}); err != nil {
		t.Fatalf("Save(entries) error = %v", err)
	}
	if err := store.MarkApplied(ctx, 7); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}
	initial := raftpb.Snapshot{
		Data: []byte("before-restore"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 7, Term: 2,
			ConfState: raftpb.ConfState{Voters: []uint64{1}},
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{
		Snapshot: &initial,
	}); err != nil {
		t.Fatalf("Save(snapshot) error = %v", err)
	}
	replacement := initial
	replacement.Data = []byte("after-restore")
	if err := store.Save(ctx, multiraft.PersistentState{
		Snapshot: &replacement,
	}); err == nil {
		t.Fatal("ordinary Save(same-index replacement) error = nil")
	}
	replacer, ok := store.(multiraft.ExternalSnapshotStorage)
	if !ok {
		t.Fatal("slot storage does not implement ExternalSnapshotStorage")
	}
	if err := replacer.ReplaceSnapshot(ctx, replacement); err != nil {
		t.Fatalf("ReplaceSnapshot() error = %v", err)
	}
	got, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !bytes.Equal(got.Data, replacement.Data) {
		t.Fatalf("Snapshot().Data = %q, want %q", got.Data, replacement.Data)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path, Options{
		SnapshotPath: snapshotPath, SnapshotChunkSize: 8,
	})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Fatalf("reopened Close() error = %v", err)
		}
	})
	got, err = reopened.ForSlot(115).Snapshot(ctx)
	if err != nil {
		t.Fatalf("reopened Snapshot() error = %v", err)
	}
	if !bytes.Equal(got.Data, replacement.Data) {
		t.Fatalf(
			"reopened Snapshot().Data = %q, want %q",
			got.Data, replacement.Data,
		)
	}
}

func TestPebbleSnapshotAndEntriesSameSaveDropsCompactedEntryPrefix(t *testing.T) {
	_, store := openPebbleDBForSnapshotTest(t, 16)
	snap := raftpb.Snapshot{Data: []byte("payload"), Metadata: raftpb.SnapshotMetadata{Index: 5, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	entries := []raftpb.Entry{{Index: 4, Term: 1}, {Index: 5, Term: 1}, {Index: 6, Term: 2, Data: []byte("six")}, {Index: 7, Term: 2, Data: []byte("seven")}}
	mustSave(t, store, multiraft.PersistentState{Snapshot: &snap, Entries: entries})
	got := mustEntries(t, store, 1, 8, 0)
	want := []raftpb.Entry{entries[2], entries[3]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Entries() = %#v, want %#v", got, want)
	}
}

func TestPebbleManifestAndMetaMismatchIsCorruption(t *testing.T) {
	db, store := openPebbleDBForSnapshotTest(t, 17)
	scope := SlotScope(17)
	manifest := validSnapshotManifest(scope)
	manifest.Index = 8
	manifest.Term = 2
	manifest.SnapshotID = "snap-0000000000000008-0000000000000002-0000000000000001"
	mustSetPebbleManifest(t, db.db, scope, manifest)
	mustSetPebbleMeta(t, db.db, scope, logMeta{FirstIndex: 9, LastIndex: 8, SnapshotIndex: 8, SnapshotTerm: 3, ConfState: manifest.ConfState})
	if _, err := store.InitialState(context.Background()); err == nil {
		t.Fatal("InitialState() error = nil, want manifest/meta mismatch corruption")
	}
}

func TestPebbleMissingManifestWithSnapshotMetaIsCorruption(t *testing.T) {
	db, store := openPebbleDBForSnapshotTest(t, 18)
	mustSetPebbleMeta(t, db.db, SlotScope(18), logMeta{FirstIndex: 7, LastIndex: 6, SnapshotIndex: 6, SnapshotTerm: 2, ConfState: raftpb.ConfState{Voters: []uint64{1}}})
	if _, err := store.InitialState(context.Background()); err == nil {
		t.Fatal("InitialState() error = nil, want missing manifest corruption")
	}
}

func TestPebbleOldInlineSnapshotValueIsMalformedManifest(t *testing.T) {
	db, store := openPebbleDBForSnapshotTest(t, 19)
	snap := raftpb.Snapshot{Data: []byte("legacy-inline"), Metadata: raftpb.SnapshotMetadata{Index: 6, Term: 2, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	data, err := snap.Marshal()
	if err != nil {
		t.Fatalf("Snapshot.Marshal() error = %v", err)
	}
	mustSetRawPebbleValue(t, db.db, encodeSnapshotKey(SlotScope(19)), data)
	if _, err := store.Snapshot(context.Background()); err == nil {
		t.Fatal("Snapshot() error = nil, want malformed manifest")
	}
}

func TestPebbleGroupsAreIndependent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
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
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
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

	db, err := Open(path, Options{})
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

	reopened, err := Open(path, Options{})
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

	db, err := Open(path, Options{})
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

	reopened, err := Open(path, Options{})
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

	db, err := Open(path, Options{})
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

func TestPebbleSameGroupSaveThenMarkAppliedPreservesOrder(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{})
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

	reopened, err := Open(path, Options{})
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
	db, err := Open(path, Options{})
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

func TestPebbleWriteStateCacheRetainsOnlyConfChangePayloads(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	normalPayload := bytes.Repeat([]byte("normal-payload-"), 4096)
	confChange := raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}
	confChangePayload := mustMarshalConfChange(t, confChange)
	entries := []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: normalPayload},
		{Index: 2, Term: 1, Type: raftpb.EntryConfChange, Data: confChangePayload},
	}
	hs := raftpb.HardState{Term: 1, Commit: 2}
	store := db.ForSlot(54)
	if err := store.Save(ctx, multiraft.PersistentState{
		HardState: &hs,
		Entries:   entries,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cached := db.stateCache[SlotScope(54)].entries
	if len(cached) != len(entries) {
		t.Fatalf("len(cached entries) = %d, want %d", len(cached), len(entries))
	}
	if cached[0].Data != nil {
		t.Errorf("normal cached entry retained %d payload bytes, want nil", len(cached[0].Data))
	}
	if !bytes.Equal(cached[1].Data, confChangePayload) {
		t.Fatalf("conf-change cached payload = %x, want %x", cached[1].Data, confChangePayload)
	}
	var decoded raftpb.ConfChange
	if err := decoded.Unmarshal(cached[1].Data); err != nil {
		t.Fatalf("ConfChange.Unmarshal(cached payload) error = %v", err)
	}
	if decoded.Type != confChange.Type || decoded.NodeID != confChange.NodeID {
		t.Fatalf("decoded cached ConfChange = %#v, want %#v", decoded, confChange)
	}

	got, err := store.Entries(ctx, 1, 3, 0)
	if err != nil {
		t.Fatalf("Entries() error = %v", err)
	}
	if !reflect.DeepEqual(got, entries) {
		t.Fatalf("Entries() = %#v, want %#v", got, entries)
	}
}

func TestPebbleWriteStateCacheCompactsPayloadsLoadedAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}

	confChangePayload := mustMarshalConfChange(t, raftpb.ConfChange{
		Type:   raftpb.ConfChangeAddNode,
		NodeID: 2,
	})
	store := db.ForSlot(55)
	if err := store.Save(ctx, multiraft.PersistentState{Entries: []raftpb.Entry{
		{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: bytes.Repeat([]byte("payload"), 8192)},
		{Index: 2, Term: 1, Type: raftpb.EntryConfChange, Data: confChangePayload},
	}}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}

	db, err = Open(path, Options{})
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(second) error = %v", err)
		}
	})
	store = db.ForSlot(55)
	if err := store.MarkApplied(ctx, 1); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	cached := db.stateCache[SlotScope(55)].entries
	if len(cached) != 2 {
		t.Fatalf("len(cached entries) = %d, want 2", len(cached))
	}
	if cached[0].Data != nil {
		t.Fatalf("reloaded normal entry retained %d payload bytes, want nil", len(cached[0].Data))
	}
	if !bytes.Equal(cached[1].Data, confChangePayload) {
		t.Fatalf("reloaded conf-change payload = %x, want %x", cached[1].Data, confChangePayload)
	}
}

func TestPebbleWriteStateCacheRetainsConfChangeV2AcrossRestartAndSnapshotTrim(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}

	addNode2Payload := mustMarshalConfChangeV2(t, raftpb.ConfChangeV2{
		Changes: []raftpb.ConfChangeSingle{{Type: raftpb.ConfChangeAddNode, NodeID: 2}},
	})
	addLearner3Payload := mustMarshalConfChangeV2(t, raftpb.ConfChangeV2{
		Changes: []raftpb.ConfChangeSingle{{Type: raftpb.ConfChangeAddLearnerNode, NodeID: 3}},
	})
	entries := []raftpb.Entry{
		{
			Index: 1,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}),
		},
		{Index: 2, Term: 1, Type: raftpb.EntryConfChangeV2, Data: addNode2Payload},
		{Index: 3, Term: 1, Type: raftpb.EntryNormal, Data: bytes.Repeat([]byte("normal"), 4096)},
		{Index: 4, Term: 1, Type: raftpb.EntryConfChangeV2, Data: addLearner3Payload},
	}
	hs := raftpb.HardState{Term: 1, Commit: 4}
	store := db.ForSlot(57)
	if err := store.Save(ctx, multiraft.PersistentState{HardState: &hs, Entries: entries}); err != nil {
		t.Fatalf("Save(initial) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}

	db, err = Open(path, Options{})
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	store = db.ForSlot(57)
	if err := store.MarkApplied(ctx, 1); err != nil {
		t.Fatalf("MarkApplied(after first restart) error = %v", err)
	}
	cached := db.stateCache[SlotScope(57)].entries
	if len(cached) != len(entries) {
		t.Fatalf("len(cached entries after restart) = %d, want %d", len(cached), len(entries))
	}
	if !bytes.Equal(cached[1].Data, addNode2Payload) || !bytes.Equal(cached[3].Data, addLearner3Payload) {
		t.Fatalf("ConfChangeV2 payloads were not retained after restart: %#v", cached)
	}
	if cached[2].Data != nil {
		t.Fatalf("normal cached entry retained %d payload bytes after restart, want nil", len(cached[2].Data))
	}

	snap := raftpb.Snapshot{
		Data: []byte("snapshot-through-index-2"),
		Metadata: raftpb.SnapshotMetadata{
			Index:     2,
			Term:      1,
			ConfState: raftpb.ConfState{Voters: []uint64{1, 2}},
		},
	}
	if err := store.Save(ctx, multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatalf("Save(snapshot) error = %v", err)
	}
	cached = db.stateCache[SlotScope(57)].entries
	if len(cached) != 2 || cached[0].Index != 3 || cached[1].Index != 4 {
		t.Fatalf("cached entries after snapshot trim = %#v, want indexes 3 and 4", cached)
	}
	if cached[0].Data != nil {
		t.Fatalf("trimmed normal cached entry retained %d payload bytes, want nil", len(cached[0].Data))
	}
	if !bytes.Equal(cached[1].Data, addLearner3Payload) {
		t.Fatalf("post-snapshot ConfChangeV2 payload = %x, want %x", cached[1].Data, addLearner3Payload)
	}
	if got := mustEntries(t, store, 3, 5, 0); !reflect.DeepEqual(got, entries[2:]) {
		t.Fatalf("Entries() after snapshot trim = %#v, want %#v", got, entries[2:])
	}
	wantConfState := raftpb.ConfState{Voters: []uint64{1, 2}, Learners: []uint64{3}}
	state, err := store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState(after snapshot trim) error = %v", err)
	}
	if !reflect.DeepEqual(state.ConfState, wantConfState) {
		t.Fatalf("ConfState after snapshot trim = %#v, want %#v", state.ConfState, wantConfState)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close(second) error = %v", err)
	}

	db, err = Open(path, Options{})
	if err != nil {
		t.Fatalf("Open(third) error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close(third) error = %v", err)
		}
	})
	store = db.ForSlot(57)
	state, err = store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState(after second restart) error = %v", err)
	}
	if !reflect.DeepEqual(state.ConfState, wantConfState) {
		t.Fatalf("ConfState after second restart = %#v, want %#v", state.ConfState, wantConfState)
	}
	if got := mustEntries(t, store, 3, 5, 0); !reflect.DeepEqual(got, entries[2:]) {
		t.Fatalf("Entries() after second restart = %#v, want %#v", got, entries[2:])
	}
	if err := store.MarkApplied(ctx, 2); err != nil {
		t.Fatalf("MarkApplied(after second restart) error = %v", err)
	}
	cached = db.stateCache[SlotScope(57)].entries
	if len(cached) != 2 || cached[0].Data != nil || !bytes.Equal(cached[1].Data, addLearner3Payload) {
		t.Fatalf("cached entries after second restart = %#v, want normal metadata plus retained ConfChangeV2", cached)
	}
}

func TestPebbleAppendKeepsRetainedNormalEntriesMetadataOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	store := db.ForSlot(53)
	if err := store.Save(ctx, multiraft.PersistentState{
		Entries: benchEntries(1, 4, 2, 16),
	}); err != nil {
		t.Fatalf("Save(initial) error = %v", err)
	}

	scope := SlotScope(53)
	before := db.stateCache[scope].entries
	if len(before) != 4 || before[0].Data != nil {
		t.Fatalf("initial cached entries = %#v, want four metadata-only normal entries", before)
	}

	if err := store.Save(ctx, multiraft.PersistentState{
		Entries: benchEntries(5, 1, 2, 16),
	}); err != nil {
		t.Fatalf("Save(append) error = %v", err)
	}

	after := db.stateCache[scope].entries
	if len(after) != 5 {
		t.Fatalf("len(cached entries) = %d, want 5", len(after))
	}
	for i, entry := range after {
		if entry.Data != nil {
			t.Fatalf("cached normal entry %d retained %d payload bytes, want nil", i, len(entry.Data))
		}
	}
}

func TestPebbleFailedTailReplaceDoesNotPolluteWriteStateCache(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	entries := []raftpb.Entry{
		{
			Index: 1,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 1}),
		},
		{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: []byte("original-normal")},
		{
			Index: 3,
			Term:  1,
			Type:  raftpb.EntryConfChange,
			Data:  mustMarshalConfChange(t, raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: 2}),
		},
	}
	hs := raftpb.HardState{Term: 1, Commit: 3}
	store := db.ForSlot(56)
	if err := store.Save(ctx, multiraft.PersistentState{HardState: &hs, Entries: entries}); err != nil {
		t.Fatalf("Save(initial) error = %v", err)
	}

	scope := SlotScope(56)
	cachedBefore := make([]raftpb.Entry, 0, len(db.stateCache[scope].entries))
	for _, entry := range db.stateCache[scope].entries {
		cachedBefore = append(cachedBefore, cloneEntry(entry))
	}
	wantConfState := raftpb.ConfState{Voters: []uint64{1, 2}}

	injected := errors.New("injected tail-replace commit failure")
	db.writeCommitTestHook = func() error { return injected }
	err = store.Save(ctx, multiraft.PersistentState{Entries: []raftpb.Entry{{
		Index: 2,
		Term:  2,
		Type:  raftpb.EntryNormal,
		Data:  []byte("uncommitted-replacement"),
	}}})
	db.writeCommitTestHook = nil
	if !errors.Is(err, injected) {
		t.Fatalf("Save(failed tail replace) error = %v, want %v", err, injected)
	}

	if got := db.stateCache[scope].entries; !reflect.DeepEqual(got, cachedBefore) {
		t.Errorf("cached entries changed after failed tail replace: got %#v want %#v", got, cachedBefore)
	}
	if got := mustEntries(t, store, 1, 4, 0); !reflect.DeepEqual(got, entries) {
		t.Errorf("Entries() after failed tail replace = %#v, want %#v", got, entries)
	}
	state, err := store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState(after failed tail replace) error = %v", err)
	}
	if !reflect.DeepEqual(state.ConfState, wantConfState) {
		t.Errorf("ConfState after failed tail replace = %#v, want %#v", state.ConfState, wantConfState)
	}

	nextHardState := raftpb.HardState{Term: 2, Vote: 1, Commit: 3}
	if err := store.Save(ctx, multiraft.PersistentState{HardState: &nextHardState}); err != nil {
		t.Fatalf("Save(HardState-only) error = %v", err)
	}
	if got := mustEntries(t, store, 1, 4, 0); !reflect.DeepEqual(got, entries) {
		t.Errorf("Entries() after HardState-only save = %#v, want %#v", got, entries)
	}
	state, err = store.InitialState(ctx)
	if err != nil {
		t.Fatalf("InitialState(after HardState-only save) error = %v", err)
	}
	if !reflect.DeepEqual(state.ConfState, wantConfState) {
		t.Errorf("ConfState after HardState-only save = %#v, want %#v", state.ConfState, wantConfState)
	}
	first, err := store.FirstIndex(ctx)
	if err != nil {
		t.Fatalf("FirstIndex() error = %v", err)
	}
	last, err := store.LastIndex(ctx)
	if err != nil {
		t.Fatalf("LastIndex() error = %v", err)
	}
	if first != 1 || last != 3 {
		t.Errorf("first/last index after HardState-only save = %d/%d, want 1/3", first, last)
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

func TestPebbleFailedPureAppendClearsHiddenCachedConfChangePayload(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "raft")
	db, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	store := db.ForSlot(58)
	initial := raftpb.Entry{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: []byte("initial")}
	if err := store.Save(ctx, multiraft.PersistentState{Entries: []raftpb.Entry{initial}}); err != nil {
		t.Fatalf("Save(initial) error = %v", err)
	}

	scope := SlotScope(58)
	cached := db.stateCache[scope]
	withTail := make([]raftpb.Entry, len(cached.entries), len(cached.entries)+2)
	copy(withTail, cached.entries)
	cached.entries = withTail
	db.stateCache[scope] = cached

	payload := mustMarshalConfChangeV2(t, raftpb.ConfChangeV2{
		Changes: []raftpb.ConfChangeSingle{{Type: raftpb.ConfChangeAddNode, NodeID: 2}},
	})
	injected := errors.New("injected pure-append commit failure")
	db.writeCommitTestHook = func() error { return injected }
	err = store.Save(ctx, multiraft.PersistentState{Entries: []raftpb.Entry{{
		Index: 2,
		Term:  1,
		Type:  raftpb.EntryConfChangeV2,
		Data:  payload,
	}}})
	db.writeCommitTestHook = nil
	if !errors.Is(err, injected) {
		t.Fatalf("Save(failed pure append) error = %v, want %v", err, injected)
	}

	visible := db.stateCache[scope].entries
	if len(visible) != 1 || visible[0].Index != 1 {
		t.Fatalf("visible cached entries after failure = %#v, want only index 1", visible)
	}
	backing := visible[:cap(visible)]
	for index := len(visible); index < len(backing); index++ {
		if backing[index].Data != nil {
			t.Fatalf("hidden cached entry %d retained %d payload bytes after failed append", index, len(backing[index].Data))
		}
	}
	if got := mustEntries(t, store, 1, 2, 0); !reflect.DeepEqual(got, []raftpb.Entry{initial}) {
		t.Fatalf("Entries() after failed pure append = %#v, want initial entry", got)
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
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
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

func openPebbleDBForSnapshotTest(t *testing.T, group uint64) (*DB, multiraft.Storage) {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{
		SnapshotPath:      filepath.Join(t.TempDir(), "snapshots"),
		SnapshotChunkSize: 8,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return db, db.ForSlot(group)
}

func openTestPebbleStore(t *testing.T, group uint64) multiraft.Storage {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{})
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
