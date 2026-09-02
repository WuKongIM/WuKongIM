package store

import (
	"bytes"
	"context"
	"io"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestMessageDBFactoryCatalogLatestAndBackupRoundTrip(t *testing.T) {
	ctx := context.Background()
	source := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { require.NoError(t, source.Close()) })

	firstID := ch.ChannelID{ID: "adapter-catalog-a", Type: 1}
	secondID := ch.ChannelID{ID: "adapter-catalog-b", Type: 1}
	firstKey := ch.ChannelKeyForID(firstID)
	secondKey := ch.ChannelKeyForID(secondID)
	appendFactoryRecord(t, source, firstKey, firstID, ch.Record{
		ID: 1_001, FromUID: "alice", ClientMsgNo: "client-1001",
		Payload: []byte("first"), SizeBytes: len("first"), ServerTimestampMS: 1_700_000_000_001,
	})
	appendFactoryRecord(t, source, secondKey, secondID, ch.Record{
		ID: 1_003, FromUID: "bob", ClientMsgNo: "client-1003",
		Payload: []byte("second"), SizeBytes: len("second"), ServerTimestampMS: 1_700_000_000_003,
	})

	page, cursor, more, err := source.ListChannelsPage(ctx, "", 1)
	require.NoError(t, err)
	require.Equal(t, []ChannelCatalogEntry{{Key: firstKey, ID: firstID}}, page)
	require.Equal(t, firstKey, cursor)
	require.True(t, more)
	page, cursor, more, err = source.ListChannelsPage(ctx, cursor, 1)
	require.NoError(t, err)
	require.Equal(t, []ChannelCatalogEntry{{Key: secondKey, ID: secondID}}, page)
	require.Equal(t, secondKey, cursor)
	require.False(t, more)

	latest, hasMore, before, err := source.ListLatestMessages(ctx, 0, 1)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Equal(t, uint64(1_003), before)
	require.Len(t, latest, 1)
	require.Equal(t, ch.Message{
		MessageID: 1_003, MessageSeq: 1, ChannelID: secondID.ID, ChannelType: secondID.Type,
		FromUID: "bob", ClientMsgNo: "client-1003", Payload: []byte("second"),
		ServerTimestampMS: 1_700_000_000_003,
	}, latest[0])
	latest[0].Payload[0] = 'x'
	latest, hasMore, before, err = source.ListLatestMessages(ctx, before, 1)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Zero(t, before)
	require.Len(t, latest, 1)
	require.Equal(t, uint64(1_001), latest[0].MessageID)
	require.Equal(t, []byte("first"), latest[0].Payload)

	request := BackupSnapshotRequest{
		HashSlot: 37,
		Channels: []BackupChannelCut{{
			Key: firstKey, ID: firstID, Epoch: 8, HW: 1,
		}},
	}
	reader, err := source.OpenBackupSnapshot(ctx, request)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.NotEmpty(t, body)

	target := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { require.NoError(t, target.Close()) })
	wantStats := BackupSnapshotStats{HashSlot: 37, ChannelCount: 1, MessageCount: 1, MaxMessageID: 1_001}
	stats, err := target.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	require.Equal(t, wantStats, stats)
	stats, err = target.ImportBackupSnapshot(ctx, body)
	require.NoError(t, err)
	require.Equal(t, wantStats, stats, "replaying the same authenticated snapshot must be idempotent")

	restored, err := target.ChannelStore(firstKey, firstID)
	require.NoError(t, err)
	committed, err := restored.ReadCommitted(ctx, ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, committed.Messages, 1)
	require.Equal(t, uint64(1_001), committed.Messages[0].MessageID)
	require.Equal(t, []byte("first"), committed.Messages[0].Payload)
	require.NoError(t, restored.Close())

	require.NoError(t, target.DiscardRestoreChannels(ctx, []RestoreChannelBoundary{{
		ID: firstID, Epoch: 8, HW: 1,
	}}))
	page, cursor, more, err = target.ListChannelsPage(ctx, "", 10)
	require.NoError(t, err)
	require.Empty(t, page)
	require.Empty(t, cursor)
	require.False(t, more)
	reopened, err := target.ChannelStore(firstKey, firstID)
	require.NoError(t, err)
	state, err := reopened.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, InitialState{}, state)
	require.NoError(t, reopened.Close())
}

func TestMessageDBFactoryDiscardRestoreChannelsValidatesTheCleanupSet(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { require.NoError(t, factory.Close()) })
	valid := RestoreChannelBoundary{ID: ch.ChannelID{ID: "restore-validation", Type: 1}, Epoch: 1, HW: 1}

	require.ErrorIs(t, factory.DiscardRestoreChannels(context.Background(), []RestoreChannelBoundary{{}}), ch.ErrInvalidConfig)
	require.ErrorIs(t, factory.DiscardRestoreChannels(context.Background(), []RestoreChannelBoundary{valid, valid}), ch.ErrInvalidConfig)
	require.ErrorIs(t, factory.DiscardRestoreChannels(context.Background(), make([]RestoreChannelBoundary, 4_097)), ch.ErrInvalidConfig)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, factory.DiscardRestoreChannels(canceled, []RestoreChannelBoundary{valid}), context.Canceled)
}

func appendFactoryRecord(t *testing.T, factory Factory, key ch.ChannelKey, id ch.ChannelID, record ch.Record) {
	t.Helper()
	channelStore, err := factory.ChannelStore(key, id)
	require.NoError(t, err)
	_, err = channelStore.AppendLeader(context.Background(), AppendLeaderRequest{Records: []ch.Record{record}})
	require.NoError(t, err)
	require.NoError(t, channelStore.Close())
}
