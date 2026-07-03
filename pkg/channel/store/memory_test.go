package store

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestMemoryStoreRetentionAdoptAndTrim(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "retention-memory", Type: 1}
	cs, err := NewMemoryFactory().ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{
			{ID: 31, Payload: []byte("one"), SizeBytes: len("one")},
			{ID: 32, Payload: []byte("two"), SizeBytes: len("two")},
			{ID: 33, Payload: []byte("three"), SizeBytes: len("three")},
		},
		Sync: true,
	})
	require.NoError(t, err)

	retained, err := cs.AdoptRetentionBoundary(ctx, 2, "committed")
	require.NoError(t, err)
	require.Equal(t, uint64(3), retained)

	trim, err := cs.TrimMessagesThrough(ctx, 2, RetentionTrimOptions{MaxMessages: 1})
	require.NoError(t, err)
	require.Equal(t, RetentionTrimResult{DeletedThroughSeq: 1, Deleted: 1, More: true}, trim)

	committed, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Equal(t, []uint64{2, 3}, messageSeqs(committed.Messages))

	trim, err = cs.TrimMessagesThrough(ctx, 2, RetentionTrimOptions{MaxMessages: 10})
	require.NoError(t, err)
	require.Equal(t, RetentionTrimResult{DeletedThroughSeq: 2, Deleted: 1}, trim)

	state, err := cs.LoadRetentionState(ctx)
	require.NoError(t, err)
	require.Equal(t, RetentionState{LocalRetentionThroughSeq: 2, PhysicalRetentionThroughSeq: 2, RetainedMaxSeq: 3}, state)
}

func TestMemoryStoreRetentionLEOAfterTrim(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "retention-memory-leo", Type: 1}
	cs, err := NewMemoryFactory().ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{
			{ID: 41, Payload: []byte("one"), SizeBytes: len("one")},
			{ID: 42, Payload: []byte("two"), SizeBytes: len("two")},
		},
		Sync: true,
	})
	require.NoError(t, err)

	retained, err := cs.AdoptRetentionBoundary(ctx, 2, "committed")
	require.NoError(t, err)
	require.Equal(t, uint64(2), retained)
	trim, err := cs.TrimMessagesThrough(ctx, 2, RetentionTrimOptions{MaxMessages: 10})
	require.NoError(t, err)
	require.Equal(t, RetentionTrimResult{DeletedThroughSeq: 2, Deleted: 2}, trim)

	loaded, err := cs.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), loaded.LEO)

	appendResult, err := cs.AppendLeader(ctx, AppendLeaderRequest{Records: []ch.Record{{ID: 43, Payload: []byte("three"), SizeBytes: len("three")}}, Sync: true})
	require.NoError(t, err)
	require.Equal(t, AppendLeaderResult{BaseOffset: 3, LastOffset: 3}, appendResult)
}
