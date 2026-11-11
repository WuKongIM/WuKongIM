package raftstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

// crashStore abandons s without Close, as a process crash would: the WAL file
// is released but no metadata checkpoint is written.
func crashStore(t *testing.T, s *Store) {
	t.Helper()
	require.NoError(t, s.wal.close())
}

func readMetaFile(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	require.NoError(t, err)
	return data
}

// F6: the per-entry hot path keeps only the WAL fsync; derived metadata is a
// checkpoint, so neither Ready persistence nor the applied marker rewrites it.
func TestStoreHotPathDoesNotRewriteMetadata(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Dir: t.TempDir(), NodeID: 1, SegmentSize: 1 << 20}
	s, err := Open(ctx, cfg)
	require.NoError(t, err)
	defer s.Close()
	before := readMetaFile(t, cfg.Dir)

	for i := uint64(1); i <= 5; i++ {
		require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: i}, []raftpb.Entry{{Index: i, Term: 1, Data: []byte{byte(i)}}}, raftpb.Snapshot{}))
		require.NoError(t, s.MarkAppliedBatch(ctx, i))
	}

	require.Equal(t, before, readMetaFile(t, cfg.Dir))
	require.Equal(t, uint64(5), s.AppliedIndex())
}

// F2: a crash with stale metadata must recover HardState (term/vote/commit)
// and the applied boundary from the WAL, never from the stale checkpoint.
func TestStoreCrashWithStaleMetadataRecoversFromWAL(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Dir: t.TempDir(), NodeID: 1, SegmentSize: 1 << 20}
	s, err := Open(ctx, cfg)
	require.NoError(t, err)
	hs := raftpb.HardState{Term: 3, Vote: 1, Commit: 4}
	entries := []raftpb.Entry{{Index: 1, Term: 1}, {Index: 2, Term: 2}, {Index: 3, Term: 3}, {Index: 4, Term: 3}}
	require.NoError(t, s.SaveReady(ctx, hs, entries, raftpb.Snapshot{}))
	require.NoError(t, s.MarkAppliedBatch(ctx, 3))
	crashStore(t, s)

	reopened, err := Open(ctx, cfg)
	require.NoError(t, err)
	defer reopened.Close()
	gotHS, _, err := reopened.InitialState()
	require.NoError(t, err)
	require.Equal(t, hs, gotHS)
	require.Equal(t, uint64(3), reopened.AppliedIndex())
	got, err := reopened.Entries(1, 5, 0)
	require.NoError(t, err)
	require.Equal(t, entries, got)
}

// F1: WAL segments do not carry state forward, so compaction may delete the
// only record of the latest HardState. Compact must checkpoint metadata before
// releasing any prefix, or a crash afterwards forgets the vote.
func TestStoreCompactionCheckpointsHardStateBeforeReleasingWAL(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Dir: t.TempDir(), NodeID: 1, SegmentSize: 1 << 20}
	s, err := Open(ctx, cfg)
	require.NoError(t, err)
	entries := []raftpb.Entry{{Index: 1, Term: 1}, {Index: 2, Term: 1}, {Index: 3, Term: 1}}
	require.NoError(t, s.SaveReady(ctx, raftpb.HardState{Term: 1, Vote: 1, Commit: 3}, entries, raftpb.Snapshot{}))
	require.NoError(t, s.MarkAppliedBatch(ctx, 2))
	snap := raftpb.Snapshot{Data: []byte("snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 3, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	require.NoError(t, s.SaveSnapshot(ctx, snap))
	// A newer term and vote land in the same (soon covered) segment.
	latest := raftpb.HardState{Term: 2, Vote: 1, Commit: 3}
	require.NoError(t, s.SaveReady(ctx, latest, nil, raftpb.Snapshot{}))
	// Force a cut so the next segment holds no HardState record; the applied
	// marker must advance to actually write the WAL.
	s.wal.cfg.SegmentSize = 1
	require.NoError(t, s.MarkAppliedBatch(ctx, 3))
	s.wal.cfg.SegmentSize = 1 << 20
	segmentsBefore, err := walSegmentFiles(filepath.Join(cfg.Dir, "wal"))
	require.NoError(t, err)
	require.NoError(t, s.Compact(ctx, 3))
	segmentsAfter, err := walSegmentFiles(filepath.Join(cfg.Dir, "wal"))
	require.NoError(t, err)
	require.Less(t, len(segmentsAfter), len(segmentsBefore), "the segment holding the latest HardState must be released for this test to bite")
	crashStore(t, s)

	reopened, err := Open(ctx, cfg)
	require.NoError(t, err)
	defer reopened.Close()
	gotHS, _, err := reopened.InitialState()
	require.NoError(t, err)
	require.Equal(t, latest, gotHS)
}

// A clean close leaves an up-to-date metadata checkpoint.
func TestStoreCloseCheckpointsMetadata(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Dir: t.TempDir(), NodeID: 1, SegmentSize: 1 << 20}
	s, err := Open(ctx, cfg)
	require.NoError(t, err)
	hs := raftpb.HardState{Term: 2, Vote: 1, Commit: 2}
	require.NoError(t, s.SaveReady(ctx, hs, []raftpb.Entry{{Index: 1, Term: 1}, {Index: 2, Term: 2}}, raftpb.Snapshot{}))
	require.NoError(t, s.MarkAppliedBatch(ctx, 2))
	require.NoError(t, s.Close())

	meta, err := loadMetadata(filepath.Join(cfg.Dir, "meta.json"))
	require.NoError(t, err)
	require.Equal(t, hs, meta.HardState)
	require.Equal(t, uint64(2), meta.AppliedIndex)
}
