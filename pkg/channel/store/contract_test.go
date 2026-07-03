package store

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func testStoreContract(t *testing.T, factory Factory) {
	t.Helper()
	ctx := context.Background()
	cs, err := factory.ChannelStore(ch.ChannelKey("1:a"), ch.ChannelID{ID: "a", Type: 1})
	require.NoError(t, err)
	initial, err := cs.Load(ctx)
	require.NoError(t, err)
	require.Zero(t, initial.LEO)

	appendRes, err := cs.AppendLeader(ctx, AppendLeaderRequest{Records: []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1, SyncOnce: true}, {ID: 2, Payload: []byte("b"), SizeBytes: 1}}, Sync: true})
	require.NoError(t, err)
	require.Equal(t, uint64(1), appendRes.BaseOffset)
	require.Equal(t, uint64(2), appendRes.LastOffset)

	logRes, err := cs.ReadLog(ctx, ReadLogRequest{FromOffset: 1, MaxOffset: 2, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, logRes.Records, 2)
	require.True(t, logRes.Records[0].SyncOnce)
	require.False(t, logRes.Records[1].SyncOnce)

	committed, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, committed.Messages, 1)
	require.Equal(t, uint64(2), committed.NextSeq)
	require.True(t, committed.Messages[0].SyncOnce)
}

func testStoreCheckpointHWMonotonic(t *testing.T, factory Factory) {
	t.Helper()
	ctx := context.Background()
	cs, err := factory.ChannelStore(ch.ChannelKey("1:checkpoint"), ch.ChannelID{ID: "checkpoint", Type: 1})
	require.NoError(t, err)
	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{Records: []ch.Record{
		{ID: 1, Payload: []byte("a"), SizeBytes: 1},
		{ID: 2, Payload: []byte("b"), SizeBytes: 1},
		{ID: 3, Payload: []byte("c"), SizeBytes: 1},
		{ID: 4, Payload: []byte("d"), SizeBytes: 1},
		{ID: 5, Payload: []byte("e"), SizeBytes: 1},
	}, Sync: true})
	require.NoError(t, err)

	require.NoError(t, cs.StoreCheckpoint(ctx, ch.Checkpoint{HW: 5}))
	require.NoError(t, cs.StoreCheckpoint(ctx, ch.Checkpoint{HW: 3}))
	loaded, err := cs.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(5), loaded.CheckpointHW)
}

func testStoreReadCommittedHonorsMinSeq(t *testing.T, factory Factory) {
	t.Helper()
	ctx := context.Background()
	cs, err := factory.ChannelStore(ch.ChannelKey("1:retained"), ch.ChannelID{ID: "retained", Type: 1})
	require.NoError(t, err)
	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{Records: []ch.Record{
		{ID: 1, Payload: []byte("a"), SizeBytes: 1},
		{ID: 2, Payload: []byte("b"), SizeBytes: 1},
		{ID: 3, Payload: []byte("c"), SizeBytes: 1},
		{ID: 4, Payload: []byte("d"), SizeBytes: 1},
	}, Sync: true})
	require.NoError(t, err)

	forward, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, MaxSeq: 4, MinSeq: 3, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Equal(t, []uint64{3, 4}, messageSeqs(forward.Messages))
	require.Equal(t, uint64(5), forward.NextSeq)

	reverse, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 4, MaxSeq: 4, MinSeq: 3, Limit: 10, MaxBytes: 1024, Reverse: true})
	require.NoError(t, err)
	require.Equal(t, []uint64{4, 3}, messageSeqs(reverse.Messages))
	require.Equal(t, uint64(2), reverse.NextSeq)
}

func TestMemoryStoreContract(t *testing.T) {
	factory := NewMemoryFactory()
	testStoreContract(t, factory)
	testStoreCheckpointHWMonotonic(t, factory)
	testStoreReadCommittedHonorsMinSeq(t, factory)
}

func TestMemoryStoreApplyFollowerSkipsDuplicatePrefix(t *testing.T) {
	ctx := context.Background()
	cs, err := NewMemoryFactory().ChannelStore(ch.ChannelKey("1:a"), ch.ChannelID{ID: "a", Type: 1})
	require.NoError(t, err)
	_, err = cs.ApplyFollower(ctx, ApplyFollowerRequest{Records: []ch.Record{{ID: 1, Index: 1, Payload: []byte("a"), SizeBytes: 1}, {ID: 2, Index: 2, Payload: []byte("b"), SizeBytes: 1}}})
	require.NoError(t, err)
	res, err := cs.ApplyFollower(ctx, ApplyFollowerRequest{Records: []ch.Record{{ID: 2, Index: 2, Payload: []byte("b"), SizeBytes: 1}, {ID: 3, Index: 3, Payload: []byte("c"), SizeBytes: 1}}})
	require.NoError(t, err)
	require.Equal(t, uint64(3), res.LEO)
	logRes, err := cs.ReadLog(ctx, ReadLogRequest{FromOffset: 1, MaxOffset: 3, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, logRes.Records, 3)
}

func TestTraceMetadataIsNotStoredInDBCompatibleMessage(t *testing.T) {
	ctx := context.Background()
	factory := NewMemoryFactory()
	id := ch.ChannelID{ID: "trace-not-persisted", Type: 1}
	key := ch.ChannelKeyForID(id)
	cs, err := factory.ChannelStore(key, id)
	require.NoError(t, err)

	_, err = cs.AppendLeader(ctx, AppendLeaderRequest{
		Records: []ch.Record{{ID: 10, Payload: []byte("payload"), SizeBytes: len("payload")}},
		Sync:    true,
	})
	require.NoError(t, err)

	lookup, ok := cs.(MessageLookup)
	require.True(t, ok)
	msg, found, err := lookup.LookupMessageByID(ctx, 10)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, msg.TraceID)
	require.Empty(t, msg.ChannelKey)
}

func messageSeqs(messages []ch.Message) []uint64 {
	out := make([]uint64, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.MessageSeq)
	}
	return out
}
