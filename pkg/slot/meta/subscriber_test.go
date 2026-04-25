package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type subscriberShardStore interface {
	AddSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error
	RemoveSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error
	ListSubscribersPage(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
	ListSubscribersSnapshot(ctx context.Context, channelID string, channelType int64) ([]string, error)
}

func TestShardAddAndPageChannelSubscribers(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	shard, ok := any(db.ForSlot(1)).(subscriberShardStore)
	require.True(t, ok, "subscriber shard store methods missing")

	require.NoError(t, shard.AddSubscribers(ctx, "g1", 2, []string{"u3", "u1", "u2", "u2"}))

	page1, cursor, done, err := shard.ListSubscribersPage(ctx, "g1", 2, "", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2"}, page1)
	require.Equal(t, "u2", cursor)
	require.False(t, done)

	page2, cursor, done, err := shard.ListSubscribersPage(ctx, "g1", 2, cursor, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u3"}, page2)
	require.Equal(t, "u3", cursor)
	require.True(t, done)

	require.NoError(t, shard.RemoveSubscribers(ctx, "g1", 2, []string{"u2"}))

	page1, cursor, done, err = shard.ListSubscribersPage(ctx, "g1", 2, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u3"}, page1)
	require.Equal(t, "u3", cursor)
	require.True(t, done)
}

func TestShardSnapshotChannelSubscribersReturnsSortedFullList(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	shard, ok := any(db.ForSlot(1)).(subscriberShardStore)
	require.True(t, ok, "subscriber shard store methods missing")

	require.NoError(t, shard.AddSubscribers(ctx, "g1", 2, []string{"u3", "u1", "u2"}))

	snapshot, err := shard.ListSubscribersSnapshot(ctx, "g1", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2", "u3"}, snapshot)
}
