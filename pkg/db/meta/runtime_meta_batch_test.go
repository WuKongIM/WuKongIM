package meta

import (
	"context"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/stretchr/testify/require"
)

func TestRuntimeMetadataBatchExactKeysAndFreshOwnedRows(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	keys := []ChannelRuntimeMetaReadKey{{200, "channel-z", 1}, {1, "channel-a", 2}, {1, "channel-a", 1}, {200, "channel-z", 1}}
	for i, key := range keys[:3] {
		_, err := store.db.HashSlot(key.HashSlot).UpsertChannelRuntimeMeta(ctx, ChannelRuntimeMeta{ChannelID: key.ChannelID, ChannelType: key.ChannelType, ChannelEpoch: uint64(i + 1), LeaderEpoch: 1, Leader: 1, MinISR: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}})
		require.NoError(t, err)
	}
	// The missing key seeks onto an existing neighbor; it must remain absent.
	requested := append([]ChannelRuntimeMetaReadKey{{1, "channel-a", 0}}, keys...)
	rows, err := store.db.GetChannelRuntimeMetaBatch(ctx, requested)
	require.NoError(t, err)
	require.Len(t, rows, len(keys))
	for i, key := range keys {
		want, ok, err := store.db.HashSlot(key.HashSlot).GetChannelRuntimeMeta(ctx, key.ChannelID, key.ChannelType)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, want, rows[i])
	}
	rows[0].Replicas[0] = 99
	require.Equal(t, uint64(1), rows[3].Replicas[0], "duplicate results must not alias")
	next := rows[1]
	next.ChannelEpoch++
	next.LeaderEpoch++
	_, err = store.db.HashSlot(keys[1].HashSlot).UpsertChannelRuntimeMeta(ctx, next)
	require.NoError(t, err)
	fresh, err := store.db.GetChannelRuntimeMetaBatch(ctx, keys)
	require.NoError(t, err)
	require.Equal(t, next.ChannelEpoch, fresh[1].ChannelEpoch)
	require.Equal(t, uint64(1), fresh[0].Replicas[0])
	require.NotEqual(t, next.ChannelEpoch, rows[1].ChannelEpoch)
	require.NoError(t, store.db.HashSlot(keys[2].HashSlot).DeleteChannelRuntimeMeta(ctx, keys[2].ChannelID, keys[2].ChannelType))
	fresh, err = store.db.GetChannelRuntimeMetaBatch(ctx, keys)
	require.NoError(t, err)
	require.Len(t, fresh, 3)
}

func TestRuntimeMetadataBatchRejectsInvalidOrCorruptReads(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	for _, keys := range [][]ChannelRuntimeMetaReadKey{
		make([]ChannelRuntimeMetaReadKey, ChannelRuntimeMetaBatchMaxReads+1),
		{{1, "", 1}}, {{1, strings.Repeat("x", maxKeyStringLen+1), 1}},
	} {
		_, err := store.db.GetChannelRuntimeMetaBatch(ctx, keys)
		require.ErrorIs(t, err, dberrors.ErrInvalidArgument)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := store.db.GetChannelRuntimeMetaBatch(canceled, nil)
	require.ErrorIs(t, err, context.Canceled)
	rows, err := store.db.GetChannelRuntimeMetaBatch(nil, nil)
	require.NoError(t, err)
	require.Empty(t, rows)
	var closed *DB
	_, err = closed.GetChannelRuntimeMetaBatch(ctx, nil)
	require.ErrorIs(t, err, dberrors.ErrClosed)
	key := ChannelRuntimeMetaReadKey{1, "corrupt-channel", 1}
	raw := encodeChannelRuntimeMetaRowKey(key.HashSlot, key.ChannelID, key.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	batch := store.db.engine.NewBatch()
	defer batch.Close()
	require.NoError(t, batch.Set(raw, []byte("corrupt")))
	require.NoError(t, batch.Commit(true))
	_, _, pointErr := store.db.HashSlot(1).GetChannelRuntimeMeta(ctx, key.ChannelID, 1)
	require.Error(t, pointErr)
	rows, err = store.db.GetChannelRuntimeMetaBatch(ctx, []ChannelRuntimeMetaReadKey{key})
	require.ErrorIs(t, err, dberrors.ErrCorruptValue)
	require.EqualError(t, err, pointErr.Error())
	require.Nil(t, rows)

	// A structurally valid row copied from another primary key must fail its
	// key-bound checksum rather than being relabeled as the requested channel.
	wrongKey := encodeChannelRuntimeMetaRowKey(2, key.ChannelID, key.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	value := encodeChannelRuntimeMetaValue(wrongKey, ChannelRuntimeMeta{ChannelEpoch: 1})
	replacement := store.db.engine.NewBatch()
	defer replacement.Close()
	require.NoError(t, replacement.Set(raw, value))
	require.NoError(t, replacement.Commit(true))
	rows, err = store.db.GetChannelRuntimeMetaBatch(ctx, []ChannelRuntimeMetaReadKey{key})
	require.ErrorIs(t, err, dberrors.ErrChecksumMismatch)
	require.Nil(t, rows)
}
