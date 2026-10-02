package raftlog

import (
	"bytes"
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestStartupSnapshotReaderSeeksAcrossChunksAndRejectsCorruption(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "raft"), Options{SnapshotChunkSize: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.ForSlot(1)
	body := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	snap := raftpb.Snapshot{Data: body, Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	if err := store.Save(ctx, multiraft.PersistentState{Snapshot: &snap}); err != nil {
		t.Fatal(err)
	}
	streaming, ok := store.(multiraft.StartupSnapshotStorage)
	if !ok {
		t.Fatal("startup falls back to whole-payload snapshot")
	}
	opened, err := streaming.OpenStartupSnapshot(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Size != int64(len(body)) || opened.Metadata.Index != 1 {
		t.Fatal("snapshot identity changed")
	}
	for _, offset := range []int64{0, 6, 7, 20, int64(len(body))} {
		if _, err := opened.Reader.Seek(offset, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(opened.Reader)
		if err != nil || !bytes.Equal(got, body[offset:]) {
			t.Fatalf("seek %d: %q %v", offset, got, err)
		}
	}
	if err := opened.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := opened.Reader.Close(); err != nil {
		t.Fatal(err)
	}
	concrete := store.(*pebbleStore)
	manifest, _, err := concrete.loadSnapshotManifest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(db.snapshotStore.scopeDir(concrete.scope), manifest.SnapshotID, chunkFileName(1))
	if err := os.WriteFile(path, []byte("xxxxxxx"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := streaming.OpenStartupSnapshot(ctx, nil); err == nil {
		t.Fatal("corrupt chunk accepted")
	}
}
