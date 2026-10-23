//go:build integration

package cluster

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

// A synchronized Raft Save must be observable through the same public snapshot
// the periodic collector consumes; a default zero is not a measured latency.
func TestNodeStorageMetricsSnapshotReportsRaftWALFsync(t *testing.T) {
	db, err := raftlog.Open(filepath.Join(t.TempDir(), "raft"), raftlog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	})
	node := &Node{defaultSlotRaftDB: db}
	before, ok := findStorageMetricsSnapshot(node.StorageMetricsSnapshot(), "raft")
	if !ok {
		t.Fatal("open raft store is missing")
	}
	if err := db.ForSlot(7).Save(context.Background(), multiraft.PersistentState{
		Entries: []raftpb.Entry{{Index: 1, Term: 1, Data: []byte("raft metrics")}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	after, ok := findStorageMetricsSnapshot(node.StorageMetricsSnapshot(), "raft")
	if !ok {
		t.Fatal("raft store is missing after Save")
	}
	if after.WALFsyncCount <= before.WALFsyncCount {
		t.Fatalf("WALFsyncCount before=%d after=%d; want a real synchronous Save observation", before.WALFsyncCount, after.WALFsyncCount)
	}
	if after.WALFsyncSumNanos <= before.WALFsyncSumNanos {
		t.Fatalf("WALFsyncSumNanos before=%d after=%d; want cumulative nanoseconds", before.WALFsyncSumNanos, after.WALFsyncSumNanos)
	}
}

func TestNodeStorageMetricsSnapshotOmitsClosedRaft(t *testing.T) {
	db, err := raftlog.Open(filepath.Join(t.TempDir(), "raft"), raftlog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := findStorageMetricsSnapshot((&Node{defaultSlotRaftDB: db}).StorageMetricsSnapshot(), "raft"); ok {
		t.Fatal("closed raft store was reported as an available zero snapshot")
	}
}
