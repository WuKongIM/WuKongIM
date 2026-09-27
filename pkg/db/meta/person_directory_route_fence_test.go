package meta

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/stretchr/testify/require"
)

func TestPersonDirectoryIncarnationAdvancesAppendRouteAtomically(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		store := openTestMetaStore(t)
		t.Cleanup(func() { store.close(t) })
		ctx := context.Background()
		shard := store.db.HashSlot(7)
		m := testRuntimeMeta("u1@u2", 1)
		m.RouteGeneration = 7
		m.DirectoryGeneration = 1
		if overflow {
			m.RouteGeneration = ^uint64(0)
		}
		_, err := shard.UpsertChannelRuntimeMeta(ctx, m)
		require.NoError(t, err)
		batch := store.db.NewBatch()
		defer batch.Close()
		require.NoError(t, batch.EnsurePersonDirectoryTask(7, PersonDirectoryTask{ChannelID: m.ChannelID, ChannelType: 1, CreatedAt: 123}))
		require.NoError(t, batch.Commit(ctx))
		err = shard.DeleteChannel(ctx, m.ChannelID, 1)
		got, ok, readErr := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 1)
		require.NoError(t, readErr)
		require.True(t, ok)
		_, channelExists, readErr := shard.GetChannel(ctx, m.ChannelID, 1)
		require.NoError(t, readErr)
		_, taskExists, readErr := shard.GetPersonDirectoryTask(ctx, m.ChannelID, 1)
		require.NoError(t, readErr)
		if overflow {
			require.ErrorIs(t, err, dberrors.ErrConflict)
			require.True(t, channelExists)
			require.True(t, taskExists)
			require.Equal(t, uint64(1), got.DirectoryGeneration)
			require.Equal(t, ^uint64(0), got.RouteGeneration)
			continue
		}
		require.NoError(t, err)
		require.False(t, channelExists)
		require.False(t, taskExists)
		require.Equal(t, uint64(2), got.DirectoryGeneration)
		require.Equal(t, uint64(8), got.RouteGeneration)
		require.NoError(t, shard.DeleteChannel(ctx, m.ChannelID, 1))
		again, ok, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 1)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, got, again, "repeated deletion must not advance a missing business channel")
		// Recreating only the business directory keeps the advanced runtime fence.
		next := store.db.NewBatch()
		defer next.Close()
		require.NoError(t, next.EnsurePersonDirectoryTask(7, PersonDirectoryTask{ChannelID: m.ChannelID, ChannelType: 1, CreatedAt: 456}))
		require.NoError(t, next.Commit(ctx))
		again, ok, err = shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 1)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, got, again)
	}
}

func TestPersonDirectoryMonotonicUpdatesCannotReuseAppendRoute(t *testing.T) {
	for _, mode := range []string{"same-epochs", "new-epoch", "new-leader-epoch", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			store := openTestMetaStore(t)
			defer store.close(t)
			ctx := context.Background()
			shard := store.db.HashSlot(7)
			m := testRuntimeMeta("u1@u2", 1)
			m.RouteGeneration = 7
			m.DirectoryGeneration = 1
			if mode == "overflow" {
				m.RouteGeneration = ^uint64(0)
			}
			_, err := shard.UpsertChannelRuntimeMeta(ctx, m)
			require.NoError(t, err)
			next := m
			next.DirectoryGeneration++
			if mode == "new-epoch" {
				next.ChannelEpoch++
			}
			if mode == "new-leader-epoch" {
				next.LeaderEpoch++
			}
			result, err := shard.UpsertChannelRuntimeMeta(ctx, next)
			if mode == "overflow" {
				require.ErrorIs(t, err, dberrors.ErrConflict)
			} else {
				require.NoError(t, err)
			}
			got, ok, err := shard.GetChannelRuntimeMeta(ctx, m.ChannelID, 1)
			require.NoError(t, err)
			require.True(t, ok)
			if mode == "overflow" {
				require.Equal(t, MonotonicConflict, result)
				require.Equal(t, uint64(1), got.DirectoryGeneration)
				require.Equal(t, ^uint64(0), got.RouteGeneration)
			} else {
				require.Equal(t, MonotonicApplied, result)
				require.Equal(t, uint64(2), got.DirectoryGeneration)
				require.Equal(t, uint64(8), got.RouteGeneration)
			}
		})
	}
}
