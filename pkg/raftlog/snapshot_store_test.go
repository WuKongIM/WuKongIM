package raftlog

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

func TestSnapshotStoreWriteSplitsChunks(t *testing.T) {
	store := testSnapshotStore(t, 3, "0000000000000001")
	snapshot := testSnapshot(10, 2, []byte("abcdefgh"))

	staged, err := store.stage(context.Background(), SlotScope(7), snapshot)
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}

	if staged.manifest.ChunkCount != 3 {
		t.Fatalf("ChunkCount = %d, want 3", staged.manifest.ChunkCount)
	}
	wantChunks := []string{"abc", "def", "gh"}
	for i, want := range wantChunks {
		path := filepath.Join(staged.finalDir, chunkFileName(i))
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read chunk %d: %v", i, err)
		}
		if string(got) != want {
			t.Fatalf("chunk %d = %q, want %q", i, got, want)
		}
	}
}

func TestSnapshotStoreReadReassemblesChunks(t *testing.T) {
	store := testSnapshotStore(t, 4, "0000000000000002")
	want := testSnapshot(11, 3, []byte("hello snapshot chunks"))
	staged, err := store.stage(context.Background(), SlotScope(8), want)
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}

	got, err := store.read(context.Background(), SlotScope(8), staged.manifest)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if got.Metadata.Index != want.Metadata.Index || got.Metadata.Term != want.Metadata.Term {
		t.Fatalf("metadata = (%d,%d), want (%d,%d)", got.Metadata.Index, got.Metadata.Term, want.Metadata.Index, want.Metadata.Term)
	}
	if !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("data = %q, want %q", got.Data, want.Data)
	}
	if !equalUint64s(got.Metadata.ConfState.Voters, want.Metadata.ConfState.Voters) {
		t.Fatalf("conf state voters = %v, want %v", got.Metadata.ConfState.Voters, want.Metadata.ConfState.Voters)
	}
}

func TestSnapshotStoreReadRejectsMissingChunk(t *testing.T) {
	store := testSnapshotStore(t, 4, "0000000000000003")
	staged, err := store.stage(context.Background(), SlotScope(9), testSnapshot(12, 4, []byte("missing chunk")))
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	if err := os.Remove(filepath.Join(staged.finalDir, chunkFileName(1))); err != nil {
		t.Fatalf("remove chunk: %v", err)
	}

	if _, err := store.read(context.Background(), SlotScope(9), staged.manifest); err == nil {
		t.Fatal("read succeeded, want missing chunk error")
	}
}

func TestSnapshotStoreReadRejectsCorruptChunk(t *testing.T) {
	store := testSnapshotStore(t, 4, "0000000000000004")
	staged, err := store.stage(context.Background(), SlotScope(10), testSnapshot(13, 5, []byte("corrupt chunk")))
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staged.finalDir, chunkFileName(0)), []byte("xxxx"), 0o600); err != nil {
		t.Fatalf("corrupt chunk: %v", err)
	}

	if _, err := store.read(context.Background(), SlotScope(10), staged.manifest); err == nil {
		t.Fatal("read succeeded, want checksum error")
	}
}

func TestSnapshotStoreReadRejectsOversizedChunkBeforeAllocation(t *testing.T) {
	store := testSnapshotStore(t, 4, "000000000000000c")
	staged, err := store.stage(context.Background(), SlotScope(20), testSnapshot(26, 18, []byte("data")))
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	chunkPath := filepath.Join(staged.finalDir, chunkFileName(0))
	file, err := os.OpenFile(chunkPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open chunk: %v", err)
	}
	if err := file.Truncate(16 << 20); err != nil {
		_ = file.Close()
		t.Fatalf("truncate chunk: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close chunk: %v", err)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err = store.read(context.Background(), SlotScope(20), staged.manifest)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "invalid snapshot chunk size") {
		t.Fatalf("read error = %v, want invalid chunk size", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
		t.Fatalf("read allocated %d bytes for oversized chunk, want bounded allocation", allocated)
	}
}

func TestSnapshotStoreWriteUsesTmpThenFinalDirectory(t *testing.T) {
	store := testSnapshotStore(t, 4, "0000000000000005")
	scope := SlotScope(11)
	staged, err := store.prepare(context.Background(), scope, testSnapshot(14, 6, []byte("tmp then final")))
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if _, err := os.Stat(store.scopeDir(scope)); !os.IsNotExist(err) {
		t.Fatalf("prepare created scope dir or unexpected stat error: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(staged.tmpDir), ".tmp-snap-") {
		t.Fatalf("tmp dir base = %q, want .tmp-snap-*", filepath.Base(staged.tmpDir))
	}

	if err := store.write(context.Background(), staged, []byte("tmp then final")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := os.Stat(staged.tmpDir); err != nil {
		t.Fatalf("tmp dir missing after write: %v", err)
	}
	if _, err := os.Stat(staged.finalDir); !os.IsNotExist(err) {
		t.Fatalf("final dir exists before publish or unexpected stat error: %v", err)
	}

	if err := store.publishFinal(staged); err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	if _, err := os.Stat(staged.finalDir); err != nil {
		t.Fatalf("final dir missing after publish: %v", err)
	}
	if _, err := os.Stat(staged.tmpDir); !os.IsNotExist(err) {
		t.Fatalf("tmp dir still exists after publish or unexpected stat error: %v", err)
	}
}

func TestSnapshotStoreWriteHandlesFinalDirCollisionByRegeneratingID(t *testing.T) {
	store := testSnapshotStore(t, 4, "1111111111111111", "2222222222222222")
	scope := SlotScope(12)
	firstID := "snap-000000000000000f-0000000000000007-1111111111111111"
	firstDir := filepath.Join(store.scopeDir(scope), firstID)
	if err := os.MkdirAll(firstDir, 0o755); err != nil {
		t.Fatalf("create collision dir: %v", err)
	}
	marker := filepath.Join(firstDir, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	staged, err := store.stage(context.Background(), scope, testSnapshot(15, 7, []byte("collision")))
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	if strings.HasSuffix(staged.manifest.SnapshotID, "1111111111111111") {
		t.Fatalf("stage used colliding snapshot ID %q", staged.manifest.SnapshotID)
	}
	if !strings.HasSuffix(staged.manifest.SnapshotID, "2222222222222222") {
		t.Fatalf("stage snapshot ID = %q, want second nonce", staged.manifest.SnapshotID)
	}
	gotMarker, err := os.ReadFile(marker)
	if err != nil || string(gotMarker) != "keep" {
		t.Fatalf("collision dir marker changed: data=%q err=%v", gotMarker, err)
	}
}

func TestSnapshotStoreReadRejectsPathTraversalSnapshotID(t *testing.T) {
	store := testSnapshotStore(t, 4, "0000000000000006")
	manifest := validManifestForRead(SlotScope(13), 16, 8, 4, 0)
	manifest.SnapshotID = "../snap-0000000000000010-0000000000000008-0000000000000006"

	if _, err := store.read(context.Background(), SlotScope(13), manifest); err == nil {
		t.Fatal("read succeeded, want invalid snapshot ID error")
	}
}

func TestSnapshotStoreAllowsZeroLengthPayload(t *testing.T) {
	store := testSnapshotStore(t, 4, "0000000000000007")
	staged, err := store.stage(context.Background(), Scope{Kind: ScopeController, ID: 3}, testSnapshot(17, 9, nil))
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	if staged.manifest.ChunkCount != 0 || len(staged.manifest.ChunkChecksums) != 0 {
		t.Fatalf("zero payload chunks = count %d checksums %d", staged.manifest.ChunkCount, len(staged.manifest.ChunkChecksums))
	}
	entries, err := os.ReadDir(staged.finalDir)
	if err != nil {
		t.Fatalf("read final dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("final dir entries = %d, want 0", len(entries))
	}
	got, err := store.read(context.Background(), Scope{Kind: ScopeController, ID: 3}, staged.manifest)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(got.Data) != 0 {
		t.Fatalf("data len = %d, want 0", len(got.Data))
	}
}

func TestSnapshotStoreUsesCRC32CCastagnoliBigEndian(t *testing.T) {
	store := testSnapshotStore(t, 3, "0000000000000008")
	data := []byte("abcdef")
	staged, err := store.stage(context.Background(), SlotScope(14), testSnapshot(18, 10, data))
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}

	wantWhole := make([]byte, 4)
	binary.BigEndian.PutUint32(wantWhole, crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli)))
	if !bytes.Equal(staged.manifest.WholeChecksum, wantWhole) {
		t.Fatalf("whole checksum = %x, want %x", staged.manifest.WholeChecksum, wantWhole)
	}
	wantChunk := make([]byte, 4)
	binary.BigEndian.PutUint32(wantChunk, crc32.Checksum([]byte("abc"), crc32.MakeTable(crc32.Castagnoli)))
	if !bytes.Equal(staged.manifest.ChunkChecksums[0], wantChunk) {
		t.Fatalf("chunk checksum = %x, want %x", staged.manifest.ChunkChecksums[0], wantChunk)
	}
}

func TestSnapshotStoreRejectsMalformedShapeAndOverflowingSizes(t *testing.T) {
	store := testSnapshotStore(t, 4, "0000000000000009")
	scope := SlotScope(15)
	t.Run("overflowing total size", func(t *testing.T) {
		manifest := validManifestForRead(scope, 19, 11, 4, 0)
		manifest.TotalSize = uint64(maxInt()) + 1
		if _, err := store.read(context.Background(), scope, manifest); err == nil {
			t.Fatal("read succeeded, want overflow error")
		}
	})
	t.Run("non-final chunk size mismatch", func(t *testing.T) {
		staged, err := store.stage(context.Background(), scope, testSnapshot(20, 12, []byte("abcdefghi")))
		if err != nil {
			t.Fatalf("stage failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(staged.finalDir, chunkFileName(0)), []byte("abc"), 0o600); err != nil {
			t.Fatalf("rewrite chunk: %v", err)
		}
		if _, err := store.read(context.Background(), scope, staged.manifest); err == nil {
			t.Fatal("read succeeded, want chunk size error")
		}
	})
	t.Run("chunk count mismatch", func(t *testing.T) {
		manifest := validManifestForRead(scope, 21, 13, 4, 8)
		manifest.ChunkCount = 1
		if _, err := store.read(context.Background(), scope, manifest); err == nil {
			t.Fatal("read succeeded, want shape error")
		}
	})
}

func TestSnapshotStoreRejectsSnapshotIDWithInvalidNonce(t *testing.T) {
	store := testSnapshotStore(t, 4, "not-hex")
	if _, err := store.prepare(context.Background(), SlotScope(16), testSnapshot(22, 14, []byte("bad nonce"))); err == nil {
		t.Fatal("prepare succeeded, want invalid nonce error")
	}
}

func TestSnapshotStorePublishFinalDoesNotOverwriteExistingDirectory(t *testing.T) {
	store := testSnapshotStore(t, 4, "000000000000000a")
	staged, err := store.prepare(context.Background(), SlotScope(17), testSnapshot(23, 15, []byte("no overwrite")))
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if err := store.write(context.Background(), staged, []byte("no overwrite")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := os.Mkdir(staged.finalDir, 0o755); err != nil {
		t.Fatalf("create final collision: %v", err)
	}
	marker := filepath.Join(staged.finalDir, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if err := store.publishFinal(staged); err == nil {
		t.Fatal("publish succeeded, want collision error")
	}
	if _, err := os.Stat(staged.tmpDir); err != nil {
		t.Fatalf("tmp dir removed on failed publish: %v", err)
	}
	gotMarker, err := os.ReadFile(marker)
	if err != nil || string(gotMarker) != "keep" {
		t.Fatalf("final dir marker changed: data=%q err=%v", gotMarker, err)
	}
}

func TestSnapshotStoreAtomicNoOverwriteRenameRejectsExistingEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	tmpDir := filepath.Join(root, "tmp")
	finalDir := filepath.Join(root, "final")
	if err := os.Mkdir(tmpDir, 0o700); err != nil {
		t.Fatalf("create tmp dir: %v", err)
	}
	tmpMarker := filepath.Join(tmpDir, "marker")
	if err := os.WriteFile(tmpMarker, []byte("tmp"), 0o600); err != nil {
		t.Fatalf("write tmp marker: %v", err)
	}
	if err := os.Mkdir(finalDir, 0o755); err != nil {
		t.Fatalf("create final dir: %v", err)
	}

	err := renameNoOverwrite(tmpDir, finalDir)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("renameNoOverwrite error = %v, want os.ErrExist", err)
	}
	if _, err := os.Stat(tmpMarker); err != nil {
		t.Fatalf("tmp marker missing after failed rename: %v", err)
	}
	entries, err := os.ReadDir(finalDir)
	if err != nil {
		t.Fatalf("read final dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("final dir entry count = %d, want 0", len(entries))
	}
}

func TestSnapshotStoreWriteCleansTmpDirOnValidationFailure(t *testing.T) {
	store := testSnapshotStore(t, 4, "000000000000000b")
	staged, err := store.prepare(context.Background(), SlotScope(18), testSnapshot(24, 16, []byte("cleanup")))
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	staged.manifest.ChunkSize = 0

	if err := store.write(context.Background(), staged, []byte("cleanup")); err == nil {
		t.Fatal("write succeeded, want validation error")
	}
	if _, err := os.Stat(staged.tmpDir); !os.IsNotExist(err) {
		t.Fatalf("tmp dir exists after failed write or unexpected stat error: %v", err)
	}
}

func TestSnapshotStoreStageCleansTmpDirOnPublishCollisionRetry(t *testing.T) {
	store := testSnapshotStore(t, 1024, "3333333333333333", "4444444444444444")
	scope := SlotScope(19)
	snapshot := testSnapshot(25, 17, bytes.Repeat([]byte("x"), 256*1024))
	firstID := "snap-0000000000000019-0000000000000011-3333333333333333"
	firstTmpDir := filepath.Join(store.scopeDir(scope), ".tmp-"+firstID)
	firstFinalDir := filepath.Join(store.scopeDir(scope), firstID)
	marker := filepath.Join(firstFinalDir, "marker")
	done := make(chan struct{})

	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(firstTmpDir); err == nil {
				if err := os.Mkdir(firstFinalDir, 0o755); err == nil {
					_ = os.WriteFile(marker, []byte("keep"), 0o600)
				}
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	staged, err := store.stage(context.Background(), scope, snapshot)
	<-done
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	if !strings.HasSuffix(staged.manifest.SnapshotID, "4444444444444444") {
		t.Fatalf("stage snapshot ID = %q, want retry nonce", staged.manifest.SnapshotID)
	}
	if _, err := os.Stat(firstTmpDir); !os.IsNotExist(err) {
		t.Fatalf("old tmp dir exists after publish collision retry or unexpected stat error: %v", err)
	}
	gotMarker, err := os.ReadFile(marker)
	if err != nil || string(gotMarker) != "keep" {
		t.Fatalf("colliding final dir changed: data=%q err=%v", gotMarker, err)
	}
}

func testSnapshotStore(t *testing.T, chunkSize uint64, nonces ...string) *snapshotStore {
	t.Helper()
	store := newSnapshotStore(t.TempDir(), chunkSize)
	idx := 0
	store.nonce = func() (string, error) {
		if idx >= len(nonces) {
			t.Fatalf("nonce called %d times, only %d configured", idx+1, len(nonces))
		}
		nonce := nonces[idx]
		idx++
		return nonce, nil
	}
	return store
}

func testSnapshot(index, term uint64, data []byte) raftpb.Snapshot {
	return raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{
			Index:     index,
			Term:      term,
			ConfState: raftpb.ConfState{Voters: []uint64{1, 2, 3}},
		},
		Data: append([]byte(nil), data...),
	}
}

func validManifestForRead(scope Scope, index, term, chunkSize, totalSize uint64) SnapshotManifest {
	chunkCount, _ := manifestChunkCount(totalSize, chunkSize)
	checksums := make([][]byte, chunkCount)
	for i := range checksums {
		checksums[i] = snapshotChecksum(nil)
	}
	return SnapshotManifest{
		Version:        snapshotManifestVersion,
		ScopeKind:      uint8(scope.Kind),
		ScopeID:        scope.ID,
		Index:          index,
		Term:           term,
		SnapshotID:     "snap-0000000000000001-0000000000000001-0000000000000001",
		ChunkSize:      chunkSize,
		ChunkCount:     chunkCount,
		TotalSize:      totalSize,
		ChecksumType:   snapshotChecksumCRC32C,
		WholeChecksum:  snapshotChecksum(make([]byte, int(minUint64(totalSize, uint64(math.MaxInt32))))),
		ChunkChecksums: checksums,
	}
}

func equalUint64s(a, b []uint64) bool {
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

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
