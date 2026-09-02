package store

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestMemoryStoreFollowerReplaySkipsOnlyTheRetainedPrefix(t *testing.T) {
	ctx := context.Background()
	id := ch.ChannelID{ID: "follower-retained-prefix", Type: 1}
	channelStore, err := NewMemoryFactory().ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)

	initial := []ch.Record{
		{ID: 1, Index: 1, FromUID: "alice", Payload: []byte("one"), SizeBytes: 3},
		{ID: 2, Index: 2, FromUID: "alice", Payload: []byte("two"), SizeBytes: 3},
	}
	applied, err := channelStore.ApplyFollower(ctx, ApplyFollowerRequest{Records: initial, LeaderHW: 1})
	require.NoError(t, err)
	require.Equal(t, ApplyFollowerResult{LEO: 2, CheckpointHW: 1}, applied)

	_, err = channelStore.AdoptRetentionBoundary(ctx, 1, "committed")
	require.NoError(t, err)
	trimmed, err := channelStore.TrimMessagesThrough(ctx, 1, RetentionTrimOptions{})
	require.NoError(t, err)
	require.Equal(t, RetentionTrimResult{DeletedThroughSeq: 1, Deleted: 1}, trimmed)

	replay := []ch.Record{
		{ID: 1, Index: 1, FromUID: "alice", Payload: []byte("already-trimmed"), SizeBytes: len("already-trimmed")},
		initial[1],
		{ID: 3, Index: 3, FromUID: "bob", Payload: []byte("three"), SizeBytes: 5},
	}
	applied, err = channelStore.ApplyFollower(ctx, ApplyFollowerRequest{Records: replay, LeaderHW: 99})
	require.NoError(t, err)
	require.Equal(t, ApplyFollowerResult{LEO: 3, CheckpointHW: 3}, applied)
	loaded, err := channelStore.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, InitialState{LEO: 3, HW: 3, CheckpointHW: 3}, loaded)

	_, err = channelStore.ApplyFollower(ctx, ApplyFollowerRequest{Records: []ch.Record{{
		ID: 22, Index: 2, Payload: []byte("divergent"), SizeBytes: len("divergent"),
	}}})
	require.ErrorIs(t, err, ch.ErrStaleMeta, "an untrimmed duplicate must retain identical durable content")
	_, err = channelStore.ApplyFollower(ctx, ApplyFollowerRequest{Records: []ch.Record{{ID: 5, Index: 5}}})
	require.ErrorIs(t, err, ch.ErrStaleMeta, "a follower batch must not create a log gap")
	_, err = channelStore.ApplyFollower(ctx, ApplyFollowerRequest{Records: []ch.Record{{ID: 4}}})
	require.ErrorIs(t, err, ch.ErrInvalidConfig)

	loadedAfterRejects, err := channelStore.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, loaded, loadedAfterRejects)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = channelStore.ApplyFollower(canceled, ApplyFollowerRequest{Records: []ch.Record{{ID: 4, Index: 4}}})
	require.ErrorIs(t, err, context.Canceled)

	var nilFactory *MemoryFactory
	_, err = nilFactory.ChannelStore("invalid:1", ch.ChannelID{ID: "invalid", Type: 1})
	require.ErrorIs(t, err, ch.ErrInvalidConfig)
}
