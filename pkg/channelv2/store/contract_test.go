package store

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
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

	appendRes, err := cs.AppendLeader(ctx, AppendLeaderRequest{Records: []ch.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}, {ID: 2, Payload: []byte("b"), SizeBytes: 1}}, Sync: true})
	require.NoError(t, err)
	require.Equal(t, uint64(1), appendRes.BaseOffset)
	require.Equal(t, uint64(2), appendRes.LastOffset)

	logRes, err := cs.ReadLog(ctx, ReadLogRequest{FromOffset: 1, MaxOffset: 2, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, logRes.Records, 2)

	committed, err := cs.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, MaxSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, committed.Messages, 1)
	require.Equal(t, uint64(2), committed.NextSeq)
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

func TestMemoryStoreContract(t *testing.T) {
	factory := NewMemoryFactory()
	testStoreContract(t, factory)
	testStoreCheckpointHWMonotonic(t, factory)
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
