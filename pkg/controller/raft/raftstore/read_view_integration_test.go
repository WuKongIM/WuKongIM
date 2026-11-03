//go:build integration

package raftstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func viewStore(t *testing.T, segment uint64) (*Store, Config) {
	t.Helper()
	cfg := Config{Dir: filepath.Join(t.TempDir(), "raft"), NodeID: 1, SegmentSize: segment}
	s, err := Open(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s, cfg
}

// TestStoreDurableReadsProgressDuringPrefixSync holds real writer ownership at
// the existing fault seam after a prefix deletion has been durably synchronized.
func TestStoreDurableReadsProgressDuringPrefixSync(t *testing.T) {
	ctx := context.Background()
	s, cfg := viewStore(t, 512)
	for index := uint64(1); index <= 16; index++ {
		require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: index}, []raftpb.Entry{{Index: index, Term: 1, Data: bytes.Repeat([]byte{byte(index)}, 128)}}, raftpb.Snapshot{}))
	}
	require.NoError(t, s.MarkAppliedBatch(ctx, 16))
	snap := raftpb.Snapshot{Data: []byte("durable-snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 8, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	require.NoError(t, s.SaveSnapshot(ctx, snap))
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	s.wal.afterRelease = func(string) error { enterOnce.Do(func() { close(entered) }); <-release; return nil }
	writer := make(chan struct{})
	var writerErr error
	go func() { writerErr = s.Compact(ctx, 8); close(writer) }()
	var readers sync.WaitGroup
	// Even a failing progress assertion must release and join every owner.
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); <-writer; readers.Wait() })
	select {
	case <-entered:
	case <-writer:
		t.Fatalf("prefix fixture did not enter owned IO: %v", writerErr)
	case <-time.After(3 * time.Second):
		t.Fatal("prefix fixture did not reach its IO seam")
	}
	type result struct {
		name  string
		value any
		err   error
	}
	results := make(chan result, 8)
	read := func(name string, call func() (any, error)) {
		readers.Add(1)
		go func() { defer readers.Done(); value, err := call(); results <- result{name, value, err} }()
	}
	read("term", func() (any, error) { return s.Term(16) })
	read("empty_ready", func() (any, error) { return nil, s.SaveReady(ctx, raftpb.HardState{}, nil, raftpb.Snapshot{}) })
	read("first", func() (any, error) { return s.FirstIndex() })
	read("last", func() (any, error) { return s.LastIndex() })
	read("applied", func() (any, error) { return s.AppliedIndex(), nil })
	read("entries", func() (any, error) { return s.Entries(9, 10, 0) })
	read("snapshot", func() (any, error) { return s.Snapshot() })
	read("initial", func() (any, error) { hs, _, err := s.InitialState(); return hs, err })
	observed := make(map[string]result, 8)
	timer := time.NewTimer(500 * time.Millisecond)
	for len(observed) < 8 {
		select {
		case r := <-results:
			observed[r.name] = r
		case <-timer.C:
			goto joined
		}
	}
joined:
	timer.Stop()
	progressed := len(observed)
	select {
	case <-writer:
		t.Errorf("writer escaped held IO: %v", writerErr)
	default:
	}
	releaseOnce.Do(func() { close(release) })
	<-writer
	require.NoError(t, writerErr)
	readers.Wait()
	for len(observed) < 8 {
		r := <-results
		observed[r.name] = r
	}
	for _, r := range observed {
		require.NoError(t, r.err, r.name)
	}
	require.Equal(t, uint64(1), observed["term"].value)
	require.Equal(t, uint64(9), observed["first"].value)
	require.Equal(t, uint64(16), observed["last"].value)
	require.Equal(t, uint64(16), observed["applied"].value)
	require.Equal(t, snap, observed["snapshot"].value)
	require.Equal(t, raftpb.HardState{Term: 1, Vote: 1, Commit: 16}, observed["initial"].value)
	require.Len(t, observed["entries"].value, 1)
	require.NoError(t, s.Close())
	reopened, err := Open(ctx, cfg)
	require.NoError(t, err)
	defer reopened.Close()
	term, err := reopened.Term(16)
	require.NoError(t, err)
	require.Equal(t, uint64(1), term)
	require.Equal(t, 8, progressed, "durable reads waited on unrelated physical writer IO")
}

func TestStoreFailedMutationKeepsPreviousPublishedCut(t *testing.T) {
	for _, op := range []string{"ready", "applied", "canceled"} {
		t.Run(op, func(t *testing.T) {
			ctx := context.Background()
			s, cfg := viewStore(t, 4096)
			require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: 1}, []raftpb.Entry{{Index: 1, Term: 1, Data: []byte("one")}}, raftpb.Snapshot{}))
			require.NoError(t, s.MarkAppliedBatch(ctx, 1))
			meta := filepath.Join(cfg.Dir, "meta.json")
			backup := meta + ".retained"
			if op != "canceled" {
				require.NoError(t, os.Rename(meta, backup))
				require.NoError(t, os.Mkdir(meta, 0700))
			}
			var err error
			switch op {
			case "ready":
				err = s.SaveReady(ctx, raftpb.HardState{Term: 2, Vote: 1, Commit: 2}, []raftpb.Entry{{Index: 2, Term: 2}}, raftpb.Snapshot{})
			case "applied":
				err = s.MarkAppliedBatch(ctx, 2)
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				err = s.SaveReady(c, raftpb.HardState{Term: 2, Commit: 2}, []raftpb.Entry{{Index: 2, Term: 2}}, raftpb.Snapshot{})
			}
			require.Error(t, err, "fault must precede read-view assertions")
			// A nil no-op is not a new successful durability boundary.
			require.NoError(t, s.SaveReady(ctx, raftpb.HardState{}, nil, raftpb.Snapshot{}))
			if op == "applied" {
				require.NoError(t, s.MarkAppliedBatch(ctx, 2))
			}
			hs, _, e := s.InitialState()
			require.NoError(t, e)
			last, e := s.LastIndex()
			require.NoError(t, e)
			require.Equal(t, raftpb.HardState{Term: 1, Vote: 1, Commit: 1}, hs, "failed mutation published future hard state")
			require.Equal(t, uint64(1), last, "failed mutation published future suffix")
			require.Equal(t, uint64(1), s.AppliedIndex(), "failed mutation published future applied watermark")
			if op != "canceled" {
				require.NoError(t, os.Remove(meta))
				require.NoError(t, os.Rename(backup, meta))
			}
		})
	}
}

func TestStoreFailedConfStateWriteCannotMutatePublishedMembership(t *testing.T) {
	ctx := context.Background()
	s, cfg := viewStore(t, 4096)
	for id := uint64(1); id <= 3; id++ {
		data, err := (&raftpb.ConfChange{Type: raftpb.ConfChangeAddNode, NodeID: id}).Marshal()
		require.NoError(t, err)
		require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: id}, []raftpb.Entry{{Index: id, Term: 1, Type: raftpb.EntryConfChange, Data: data}}, raftpb.Snapshot{}))
	}
	meta := filepath.Join(cfg.Dir, "meta.json")
	require.NoError(t, os.Rename(meta, meta+".old"))
	require.NoError(t, os.Mkdir(meta, 0700))
	data, err := (&raftpb.ConfChange{Type: raftpb.ConfChangeRemoveNode, NodeID: 2}).Marshal()
	require.NoError(t, err)
	require.Error(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: 4}, []raftpb.Entry{{Index: 4, Term: 1, Type: raftpb.EntryConfChange, Data: data}}, raftpb.Snapshot{}))
	require.NoError(t, s.SaveReady(ctx, raftpb.HardState{}, nil, raftpb.Snapshot{}))
	_, conf, err := s.InitialState()
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, conf.Voters, "failed writer modified the published ConfState backing")
}

// Compact changes retained physical history, not the successful logical cut.
// A nil compaction must not make an earlier failed metadata write visible.
func TestStoreCompactionCannotPublishFailedMetadata(t *testing.T) {
	for _, op := range []string{"ready", "applied", "snapshot"} {
		t.Run(op, func(t *testing.T) {
			ctx := context.Background()
			s, cfg := viewStore(t, 4096)
			hs := raftpb.HardState{Term: 1, Vote: 1, Commit: 2}
			require.NoError(t, s.SaveReady(ctx, hs, []raftpb.Entry{{Index: 1, Term: 1}, {Index: 2, Term: 1}}, raftpb.Snapshot{}))
			require.NoError(t, s.MarkAppliedBatch(ctx, 1))
			meta := filepath.Join(cfg.Dir, "meta.json")
			require.NoError(t, os.Rename(meta, meta+".retained"))
			require.NoError(t, os.Mkdir(meta, 0700))
			var err error
			switch op {
			case "ready":
				err = s.SaveReady(ctx, raftpb.HardState{Term: 2, Vote: 1, Commit: 3}, []raftpb.Entry{{Index: 3, Term: 2}}, raftpb.Snapshot{})
			case "applied":
				err = s.MarkAppliedBatch(ctx, 2)
			case "snapshot":
				err = s.SaveSnapshot(ctx, raftpb.Snapshot{Data: []byte("one"), Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}})
			}
			require.Error(t, err)
			require.NoError(t, os.Remove(meta))
			require.NoError(t, os.Rename(meta+".retained", meta))
			require.NoError(t, s.Compact(ctx, 0))
			got, _, err := s.InitialState()
			require.NoError(t, err)
			require.Equal(t, hs, got)
			require.Equal(t, uint64(1), s.AppliedIndex())
			last, err := s.LastIndex()
			require.NoError(t, err)
			require.Equal(t, uint64(2), last)
			snap, err := s.Snapshot()
			require.NoError(t, err)
			require.Equal(t, uint64(0), snap.Metadata.Index)
			// Only a later real persistence may publish the dirty writer state.
			require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 2, Vote: 1, Commit: 3}, []raftpb.Entry{{Index: 3, Term: 2}}, raftpb.Snapshot{}))
			last, err = s.LastIndex()
			require.NoError(t, err)
			require.Equal(t, uint64(3), last)
			require.NoError(t, s.Close())
			reopened, err := Open(ctx, cfg)
			require.NoError(t, err)
			defer reopened.Close()
			last, err = reopened.LastIndex()
			require.NoError(t, err)
			require.Equal(t, uint64(3), last)
		})
	}
}

func TestStorePublishedViewsKeepDetachedResultsAndBoundaries(t *testing.T) {
	ctx := context.Background()
	s, cfg := viewStore(t, 4096)
	input := []raftpb.Entry{{Index: 1, Term: 1, Data: []byte("one")}, {Index: 2, Term: 1, Data: []byte("two")}, {Index: 3, Term: 1, Data: []byte("old-three")}, {Index: 4, Term: 1, Data: []byte("old-four")}}
	require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: 2}, input, raftpb.Snapshot{}))
	input[2].Data[0] = 'X'
	old, err := s.Entries(3, 4, 0)
	require.NoError(t, err)
	require.Equal(t, "old-three", string(old[0].Data))
	require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 2, Vote: 1, Commit: 4}, []raftpb.Entry{{Index: 3, Term: 2, Data: []byte("new-three")}, {Index: 4, Term: 2, Data: []byte("new-four")}}, raftpb.Snapshot{}))
	old[0].Data[0] = 'Y'
	term, err := s.Term(3)
	require.NoError(t, err)
	require.Equal(t, uint64(2), term)
	got, err := s.Entries(3, 4, 0)
	require.NoError(t, err)
	require.Equal(t, "new-three", string(got[0].Data))
	snap := raftpb.Snapshot{Data: []byte("snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 2, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	require.NoError(t, s.SaveSnapshot(ctx, snap))
	snap.Data[0] = 'X'
	snap.Metadata.ConfState.Voters[0] = 9
	loaded, err := s.Snapshot()
	require.NoError(t, err)
	require.Equal(t, "snapshot", string(loaded.Data))
	loaded.Data[0] = 'Y'
	_, conf, err := s.InitialState()
	require.NoError(t, err)
	require.Equal(t, []uint64{1}, conf.Voters)
	conf.Voters[0] = 9
	require.NoError(t, s.Compact(ctx, 2))
	_, err = s.Term(1)
	require.ErrorIs(t, err, raft.ErrCompacted)
	first, err := s.FirstIndex()
	require.NoError(t, err)
	require.Equal(t, uint64(3), first)
	require.NoError(t, s.Close())
	again, err := Open(ctx, cfg)
	require.NoError(t, err)
	defer again.Close()
	reopened, err := again.Entries(3, 5, 0)
	require.NoError(t, err)
	require.Equal(t, "new-three", string(reopened[0].Data))
	loaded, err = again.Snapshot()
	require.NoError(t, err)
	require.Equal(t, "snapshot", string(loaded.Data))
	require.Equal(t, []uint64{1}, loaded.Metadata.ConfState.Voters)
}

func TestStoreAppliedPublicationDoesNotCloneRetainedPayloads(t *testing.T) {
	ctx := context.Background()
	s, _ := viewStore(t, 8<<20)
	entries := make([]raftpb.Entry, 32)
	for i := range entries {
		entries[i] = raftpb.Entry{Index: uint64(i + 1), Term: 1, Data: bytes.Repeat([]byte{byte(i)}, 64<<10)}
	}
	require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: 32}, entries, raftpb.Snapshot{}))
	require.NoError(t, s.MarkAppliedBatch(ctx, 8))
	require.NoError(t, s.SaveSnapshot(ctx, raftpb.Snapshot{Data: make([]byte, 1<<20), Metadata: raftpb.SnapshotMetadata{Index: 8, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := uint64(9); i <= 24; i++ {
		require.NoError(t, s.MarkAppliedBatch(ctx, i))
	}
	runtime.ReadMemStats(&after)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20), "watermark publication copied retained log/snapshot payload")
	require.Equal(t, uint64(24), s.AppliedIndex())
}
