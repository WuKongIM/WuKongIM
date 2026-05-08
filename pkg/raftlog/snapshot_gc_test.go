package raftlog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

func TestSnapshotGCRemovesTmpDirsWithoutManifest(t *testing.T) {
	db := openSnapshotGCTestDB(t)
	defer db.Close()

	tmpDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(7)), ".tmp-snap-000000000000000a-0000000000000002-0000000000000001")
	mustCreateOldDir(t, tmpDir)

	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC() error = %v", err)
	}
	assertPathMissing(t, tmpDir)
}

func TestSnapshotGCRemovesUnreferencedFinalDirs(t *testing.T) {
	db := openSnapshotGCTestDB(t)
	defer db.Close()

	finalDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(8)), "snap-000000000000000b-0000000000000003-0000000000000002")
	mustCreateOldDir(t, finalDir)

	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC() error = %v", err)
	}
	assertPathMissing(t, finalDir)
}

func TestSnapshotGCKeepsManifestReferencedSnapshot(t *testing.T) {
	db := openSnapshotGCTestDB(t)
	defer db.Close()

	scope := SlotScope(9)
	manifest := validManifestForRead(scope, 12, 4, 16, 0)
	manifest.SnapshotID = "snap-000000000000000c-0000000000000004-0000000000000003"
	encoded, err := encodeSnapshotManifest(scope, manifest)
	if err != nil {
		t.Fatalf("encodeSnapshotManifest() error = %v", err)
	}
	if err := db.db.Set(encodeSnapshotKey(scope), encoded, pebble.Sync); err != nil {
		t.Fatalf("write snapshot manifest error = %v", err)
	}
	finalDir := filepath.Join(db.snapshotStore.scopeDir(scope), manifest.SnapshotID)
	mustCreateOldDir(t, finalDir)

	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC() error = %v", err)
	}
	assertPathExists(t, finalDir)
}

func TestSnapshotGCStopsOnCorruptExternalManifestValue(t *testing.T) {
	db := openSnapshotGCTestDB(t)
	defer db.Close()

	scope := SlotScope(14)
	finalDir := filepath.Join(db.snapshotStore.scopeDir(scope), "snap-0000000000000012-000000000000000a-0000000000000009")
	mustCreateOldDir(t, finalDir)
	if err := db.db.Set(encodeSnapshotKey(scope), []byte(snapshotManifestMagic), pebble.Sync); err != nil {
		t.Fatalf("write corrupt manifest value error = %v", err)
	}

	if err := db.runSnapshotGC(context.Background()); err == nil {
		t.Fatal("runSnapshotGC() error = nil, want corrupt manifest error")
	}
	assertPathExists(t, finalDir)
}

func TestSnapshotGCDoesNotCrossIndependentRaftLogDBRoots(t *testing.T) {
	parent := t.TempDir()
	dbA := openSnapshotGCTestDBAt(t, filepath.Join(parent, "db-a"), filepath.Join(parent, "snap-a"))
	defer dbA.Close()

	ownFinal := filepath.Join(dbA.snapshotStore.scopeDir(SlotScope(10)), "snap-000000000000000d-0000000000000005-0000000000000004")
	otherRoot := filepath.Join(parent, "snap-b")
	otherFinal := filepath.Join(otherRoot, "slot-10", "snap-000000000000000e-0000000000000006-0000000000000005")
	mustCreateOldDir(t, ownFinal)
	mustCreateOldDir(t, otherFinal)

	if err := dbA.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC() error = %v", err)
	}
	assertPathMissing(t, ownFinal)
	assertPathExists(t, otherFinal)
}

func TestSnapshotGCSkipsInFlightSnapshotFinalization(t *testing.T) {
	db := openSnapshotGCTestDB(t)
	defer db.Close()

	finalDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(11)), "snap-000000000000000f-0000000000000007-0000000000000006")
	mustCreateOldDir(t, finalDir)
	unregister := db.registerActiveSnapshotPath(finalDir)

	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC(active) error = %v", err)
	}
	assertPathExists(t, finalDir)

	unregister()
	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC(inactive) error = %v", err)
	}
	assertPathMissing(t, finalDir)
}

func TestSnapshotGCKeepsDoubleRegisteredActiveFinalPath(t *testing.T) {
	db := openSnapshotGCTestDB(t)
	defer db.Close()

	finalDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(13)), "snap-0000000000000011-0000000000000009-0000000000000008")
	mustCreateOldDir(t, finalDir)
	unregisterFirst := db.registerActiveSnapshotPath(finalDir)
	unregisterSecond := db.registerActiveSnapshotPath(finalDir)

	unregisterFirst()
	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC(first active ref) error = %v", err)
	}
	assertPathExists(t, finalDir)

	unregisterSecond()
	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC(inactive) error = %v", err)
	}
	assertPathMissing(t, finalDir)
}

func TestSnapshotGCSkipsInFlightTmpStaging(t *testing.T) {
	db := openSnapshotGCTestDB(t)
	defer db.Close()

	tmpDir := filepath.Join(db.snapshotStore.scopeDir(SlotScope(12)), ".tmp-snap-0000000000000010-0000000000000008-0000000000000007")
	mustCreateOldDir(t, tmpDir)
	unregister := db.registerActiveSnapshotPath(tmpDir)

	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC(active) error = %v", err)
	}
	assertPathExists(t, tmpDir)

	unregister()
	if err := db.runSnapshotGC(context.Background()); err != nil {
		t.Fatalf("runSnapshotGC(inactive) error = %v", err)
	}
	assertPathMissing(t, tmpDir)
}

func TestSnapshotCloseWaitsForRunningGC(t *testing.T) {
	db := openSnapshotGCTestDB(t)

	started := make(chan struct{})
	release := make(chan struct{})
	db.snapshotGCTestHook = func() {
		close(started)
		<-release
	}

	if !db.startSnapshotGC() {
		t.Fatal("startSnapshotGC() = false, want running GC")
	}
	<-started

	closed := make(chan error, 1)
	go func() { closed <- db.Close() }()

	select {
	case err := <-closed:
		t.Fatalf("Close returned before running GC finished: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for running GC to finish")
	}
}

func openSnapshotGCTestDB(t *testing.T) *DB {
	t.Helper()
	return openSnapshotGCTestDBAt(t, filepath.Join(t.TempDir(), "raft"), filepath.Join(t.TempDir(), "snapshots"))
}

func openSnapshotGCTestDBAt(t *testing.T, dbPath, snapshotPath string) *DB {
	t.Helper()
	db, err := Open(dbPath, Options{SnapshotPath: snapshotPath, SnapshotChunkSize: 16, SnapshotGCGrace: 0})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return db
}

func mustCreateOldDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chunk-000000"), []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", dir, err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "chunk-000000"), old, old); err != nil {
		t.Fatalf("Chtimes(file in %q) error = %v", dir, err)
	}
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", dir, err)
	}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat(%q) error = %v, want path to exist", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) error = %v, want path missing", path, err)
	}
}
