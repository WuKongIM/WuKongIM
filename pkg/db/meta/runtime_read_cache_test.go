package meta

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Repeated routed conversation reads must not decode the same unchanged
// runtime row, but each result must still own its mutable replica slices.
func TestRuntimeMetadataWarmReadAllocationBudget(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	shard := s.db.HashSlot(7)
	ctx := context.Background()
	m := testRuntimeMeta("warm-runtime", 2)
	_, err := shard.UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	allocs := testing.AllocsPerRun(100, func() {
		row, found, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, m.ChannelType)
		if err != nil || !found || row.ChannelID != m.ChannelID {
			t.Fatalf("read: %+v %v %v", row, found, err)
		}
	})
	require.LessOrEqual(t, allocs, float64(5))
	first, _, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, m.ChannelType)
	require.NoError(t, err)
	first.Replicas[0] = 999
	first.ISR[0] = 999
	second, _, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, m.ChannelType)
	require.NoError(t, err)
	require.NotEqual(t, uint64(999), second.Replicas[0])
	require.NotEqual(t, uint64(999), second.ISR[0])
}

func TestRuntimeMetadataWarmReadsFollowMutations(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	shard := s.db.HashSlot(7)
	m := testRuntimeMeta("runtime-mutations", 2)
	_, err := shard.UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	check := func(want ChannelRuntimeMeta) {
		got, found, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, m.ChannelType)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, want.LeaderEpoch, got.LeaderEpoch)
		require.Equal(t, want.Leader, got.Leader)
		require.Equal(t, want.RetentionThroughSeq, got.RetentionThroughSeq)
	}
	check(m)
	m.LeaderEpoch++
	_, err = shard.UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	check(m)
	m.LeaderEpoch++
	b := s.db.NewBatch()
	defer b.Close()
	_, err = b.UpsertChannelRuntimeMeta(7, m)
	require.NoError(t, err)
	require.NoError(t, b.Commit(ctx))
	check(m)
	require.NoError(t, shard.DeleteChannelRuntimeMeta(ctx, m.ChannelID, m.ChannelType))
	_, found, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, m.ChannelType)
	require.NoError(t, err)
	require.False(t, found)
	_, err = shard.UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	check(m)
	require.NoError(t, s.db.DeleteHashSlotData(ctx, 7))
	_, found, err = shard.GetChannelRuntimeMeta(ctx, m.ChannelID, m.ChannelType)
	require.NoError(t, err)
	require.False(t, found)
}

func TestRuntimeReadCacheFencesLateFillAndBoundsRetention(t *testing.T) {
	c := newRuntimeReadCache()
	key := runtimeReadKey{hashSlot: 7, channelID: "late", channelType: 2}
	_, _, oldGeneration := c.get(key)
	c.startMutation([]HashSlot{7})
	c.finishMutation([]HashSlot{7})
	c.put(key, testRuntimeMeta(key.channelID, 2), oldGeneration)
	_, found, generation := c.get(key)
	require.False(t, found, "an old miss must not republish after a mutation")
	c.put(key, testRuntimeMeta(key.channelID, 2), generation)
	c.startMutation([]HashSlot{8})
	c.finishMutation([]HashSlot{8})
	_, found, _ = c.get(key)
	require.True(t, found, "an unrelated hash slot should retain its hot row")
	for i := 0; i <= runtimeReadCacheEntries; i++ {
		k := runtimeReadKey{channelID: fmt.Sprint(i)}
		c.put(k, ChannelRuntimeMeta{}, 0)
	}
	require.Len(t, c.entries, runtimeReadCacheEntries)
	for i := 0; i < 100; i++ {
		k := runtimeReadKey{channelID: fmt.Sprint(i) + strings.Repeat("x", 65530)}
		c.put(k, ChannelRuntimeMeta{}, 0)
		require.LessOrEqual(t, c.bytes, runtimeReadCacheBytes)
	}
}

func TestRuntimeMetadataCacheSnapshotCancellationAndClose(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	shard := s.db.HashSlot(7)
	m := testRuntimeMeta("runtime-snapshot", 2)
	_, err := shard.UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	snap, err := s.db.ExportHashSlotSnapshot(ctx, []uint16{7})
	require.NoError(t, err)
	m.LeaderEpoch++
	_, err = shard.UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	got, _, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 2)
	require.NoError(t, err)
	require.Equal(t, m.LeaderEpoch, got.LeaderEpoch)
	require.NoError(t, s.db.ImportHashSlotSnapshot(ctx, snap))
	got, _, err = shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 2)
	require.NoError(t, err)
	require.Equal(t, m.LeaderEpoch-1, got.LeaderEpoch)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = shard.GetChannelRuntimeMeta(canceled, m.ChannelID, 2)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, s.engine.Close())
	_, _, err = shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 2)
	require.Error(t, err)
}

func TestRuntimeMetadataConcurrentReadsRemainMonotonic(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	shard := s.db.HashSlot(7)
	m := testRuntimeMeta("concurrent-runtime", 2)
	_, err := shard.UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var previous uint64
			for range 500 {
				got, found, err := shard.GetChannelRuntimeMeta(ctx, "concurrent-runtime", 2)
				if err != nil || !found || got.LeaderEpoch < previous {
					t.Errorf("runtime epoch regressed: previous=%d got=%+v found=%v err=%v", previous, got, found, err)
					return
				}
				previous = got.LeaderEpoch
			}
		}()
	}
	defer wg.Wait()
	for range 20 {
		m.LeaderEpoch++
		_, err := shard.UpsertChannelRuntimeMeta(ctx, m)
		require.NoError(t, err)
		got, _, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 2)
		require.NoError(t, err)
		require.Equal(t, m.LeaderEpoch, got.LeaderEpoch, "completed mutation must be visible")
	}
}

func TestRuntimeMetadataMissCannotRegressDuringCommitPublication(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	shard := s.db.HashSlot(7)
	m := testRuntimeMeta("publication-window", 2)
	_, err := shard.UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	// Hold the exact typed writer ownership through physical commit and both
	// readers. Pause reader A between its durable read and cache publication.
	unlock := s.db.lockHashSlots([]HashSlot{7})
	defer unlock()
	key := runtimeReadKey{hashSlot: 7, channelID: m.ChannelID, channelType: 2}
	_, _, generation := s.db.runtimeCache.get(key)
	old, found, err := channelRuntimeMetaTable.Get(ctx, shard, channelRuntimeMetaPrimaryKey(m.ChannelID, 2))
	require.NoError(t, err)
	require.True(t, found)
	m.LeaderEpoch++
	rowKey := encodeChannelRuntimeMetaRowKey(7, m.ChannelID, 2, channelRuntimeMetaPrimaryFamilyID)
	value, err := channelRuntimeMetaTable.encodeValue(rowKey, m)
	require.NoError(t, err)
	b := s.engine.NewBatch()
	defer b.Close()
	require.NoError(t, b.Set(rowKey, value))
	require.NoError(t, b.Commit(true))
	newer, _, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 2)
	require.NoError(t, err)
	require.Equal(t, m.LeaderEpoch, newer.LeaderEpoch)
	s.db.runtimeCache.put(key, old, generation)
	again, _, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 2)
	require.NoError(t, err)
	require.Equal(t, newer.LeaderEpoch, again.LeaderEpoch, "reader A must not replace reader B's newer result")
}

// Fail only after replacement has committed its delete batch. Preflight and
// checksum validation still read the complete valid stream successfully.
type runtimeRestoreFailureReader struct {
	*bytes.Reader
	db  *MetaDB
	key []byte
}

func (r runtimeRestoreFailureReader) Read(p []byte) (int, error) {
	_, found, err := r.db.get(r.key)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, errors.New("injected snapshot read failure after deletion")
	}
	return r.Reader.Read(p)
}

func TestRuntimeMetadataFailedStreamImportDropsOldCache(t *testing.T) {
	s := openTestMetaStore(t)
	defer s.close(t)
	ctx := context.Background()
	shard := s.db.HashSlot(7)
	m := testRuntimeMeta("partial-runtime", 2)
	_, err := shard.UpsertChannelRuntimeMeta(ctx, m)
	require.NoError(t, err)
	stream, err := s.db.OpenHashSlotSnapshot(ctx, []uint16{7})
	require.NoError(t, err)
	payload, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	_, _, err = shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 2)
	require.NoError(t, err)
	reader := runtimeRestoreFailureReader{Reader: bytes.NewReader(payload), db: s.db,
		key: encodeChannelRuntimeMetaRowKey(7, m.ChannelID, 2, channelRuntimeMetaPrimaryFamilyID)}
	err = s.db.ImportHashSlotSnapshotReader(ctx, []uint16{7}, reader, int64(len(payload)))
	require.Error(t, err)
	_, found, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 2)
	require.NoError(t, err)
	require.False(t, found, "failed replacement already deleted the durable row")
}
