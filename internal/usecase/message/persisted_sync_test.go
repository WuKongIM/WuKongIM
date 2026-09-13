package message

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestPersistedSyncUsesExplicitReaderAndPreservesMissingStorageFailure(t *testing.T) {
	committed := &recordingChannelMessageReader{}
	persisted := &recordingChannelMessageReader{batchResults: []ChannelMessageReadResult{{Err: metadb.ErrNotFound}}}
	app := New(Options{Reader: committed, PersistedReader: persisted, Memberships: liveSyncMembershipStore()})
	query := SyncChannelMessagesBatchQuery{LoginUID: "u1", Items: []SyncChannelMessagesQuery{{ChannelID: "g1", ChannelType: 2, Limit: 1}}}
	got, err := app.SyncPersistedChannelMessagesBatch(context.Background(), query)
	require.NoError(t, err)
	require.ErrorIs(t, got.Items[0].Err, metadb.ErrNotFound)
	require.Empty(t, committed.queries)
	app.persistedReader = nil
	_, err = app.SyncPersistedChannelMessagesBatch(context.Background(), query)
	require.ErrorIs(t, err, ErrSyncBatchReaderRequired)
	require.Empty(t, committed.queries)
}

type shortPersistedScans struct{ calls int }

func (s *shortPersistedScans) ReadPersistedMessages(_ context.Context, queries []MessageScanQuery) ([]MessageScanResult, error) {
	s.calls++
	if len(queries) != 1 || queries[0].MaxBytes != 1<<20 {
		return nil, errors.New("unbounded scan")
	}
	switch s.calls {
	case 1:
		return []MessageScanResult{{Messages: []SyncedMessage{{MessageSeq: 8}}, HasMore: true}}, nil
	case 2:
		return []MessageScanResult{{Messages: []SyncedMessage{{MessageSeq: 7}, {MessageSeq: 6}}}}, nil
	default:
		return nil, errors.New("unexpected scan")
	}
}

func TestPersistedPageContinuesShortByteLimitedScan(t *testing.T) {
	scans := &shortPersistedScans{}
	got, err := NewPersistedPageReader(scans).SyncMessages(context.Background(), ChannelMessageQuery{ChannelID: ChannelID{ID: "g1", Type: 2}, Limit: 2, PullMode: PullModeDown})
	require.NoError(t, err)
	require.Len(t, got.Messages, 2)
	require.Equal(t, uint64(7), got.Messages[0].MessageSeq)
	require.Equal(t, uint64(8), got.Messages[1].MessageSeq)
	require.True(t, got.HasMore)
	require.Equal(t, 2, scans.calls)
}
