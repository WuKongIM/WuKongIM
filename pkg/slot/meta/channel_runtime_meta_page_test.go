package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShardListChannelRuntimeMetaPageReturnsOrderedPage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForHashSlot(7)

	metas := []ChannelRuntimeMeta{
		{
			ChannelID:    "g2",
			ChannelType:  2,
			ChannelEpoch: 12,
			LeaderEpoch:  6,
			Replicas:     []uint64{3, 5, 8},
			ISR:          []uint64{3, 5},
			Leader:       3,
			MinISR:       2,
			Status:       2,
			Features:     1,
			LeaseUntilMS: 1700000000002,
		},
		{
			ChannelID:    "g1",
			ChannelType:  2,
			ChannelEpoch: 11,
			LeaderEpoch:  5,
			Replicas:     []uint64{3, 5, 8},
			ISR:          []uint64{3, 5},
			Leader:       3,
			MinISR:       2,
			Status:       2,
			Features:     1,
			LeaseUntilMS: 1700000000001,
		},
		{
			ChannelID:    "g1",
			ChannelType:  1,
			ChannelEpoch: 10,
			LeaderEpoch:  4,
			Replicas:     []uint64{3, 5, 8},
			ISR:          []uint64{3, 5},
			Leader:       5,
			MinISR:       2,
			Status:       1,
			Features:     1,
			LeaseUntilMS: 1700000000000,
		},
	}
	for _, meta := range metas {
		require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, meta))
	}

	page, cursor, done, err := shard.ListChannelRuntimeMetaPage(ctx, ChannelRuntimeMetaCursor{}, 2)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []ChannelRuntimeMeta{metas[2], metas[1]}, page)
	require.Equal(t, ChannelRuntimeMetaCursor{ChannelID: "g1", ChannelType: 2}, cursor)
}

func TestShardListChannelRuntimeMetaPageContinuesAfterCursor(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForHashSlot(9)

	metas := []ChannelRuntimeMeta{
		{
			ChannelID:    "g1",
			ChannelType:  1,
			ChannelEpoch: 10,
			LeaderEpoch:  4,
			Replicas:     []uint64{1, 2, 3},
			ISR:          []uint64{1, 2},
			Leader:       1,
			MinISR:       2,
			Status:       1,
			Features:     1,
			LeaseUntilMS: 1700000000000,
		},
		{
			ChannelID:    "g1",
			ChannelType:  2,
			ChannelEpoch: 11,
			LeaderEpoch:  5,
			Replicas:     []uint64{1, 2, 3},
			ISR:          []uint64{1, 2},
			Leader:       2,
			MinISR:       2,
			Status:       2,
			Features:     1,
			LeaseUntilMS: 1700000000001,
		},
		{
			ChannelID:    "g2",
			ChannelType:  1,
			ChannelEpoch: 12,
			LeaderEpoch:  6,
			Replicas:     []uint64{1, 2, 3},
			ISR:          []uint64{1, 2, 3},
			Leader:       2,
			MinISR:       2,
			Status:       2,
			Features:     1,
			LeaseUntilMS: 1700000000002,
		},
	}
	for _, meta := range metas {
		require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, meta))
	}

	page1, cursor, done, err := shard.ListChannelRuntimeMetaPage(ctx, ChannelRuntimeMetaCursor{}, 2)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []ChannelRuntimeMeta{metas[0], metas[1]}, page1)
	require.Equal(t, ChannelRuntimeMetaCursor{ChannelID: "g1", ChannelType: 2}, cursor)

	page2, cursor, done, err := shard.ListChannelRuntimeMetaPage(ctx, cursor, 2)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []ChannelRuntimeMeta{metas[2]}, page2)
	require.Equal(t, ChannelRuntimeMetaCursor{ChannelID: "g2", ChannelType: 1}, cursor)
}

func TestShardListChannelRuntimeMetaPageRejectsInvalidLimit(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForHashSlot(7)

	_, _, _, err := shard.ListChannelRuntimeMetaPage(ctx, ChannelRuntimeMetaCursor{}, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
}
