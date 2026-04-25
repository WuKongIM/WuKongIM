package message

import (
	"context"
	"errors"
	"testing"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestSyncChannelMessagesNormalizesPersonChannelAndCapsLimit(t *testing.T) {
	reader := &recordingChannelMessageReader{
		page: ChannelMessagePage{
			Messages: []channel.Message{{MessageID: 88, MessageSeq: 9}},
			HasMore:  true,
		},
	}
	app := New(Options{MessageReader: reader})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:        "u1",
		ChannelID:       "u2",
		ChannelType:     frame.ChannelTypePerson,
		StartMessageSeq: 1,
		EndMessageSeq:   10,
		Limit:           20000,
		PullMode:        PullModeUp,
	})

	require.NoError(t, err)
	require.True(t, result.More)
	require.Equal(t, []channel.Message{{MessageID: 88, MessageSeq: 9}}, result.Messages)
	require.Len(t, reader.queries, 1)
	require.Equal(t, ChannelMessageQuery{
		ChannelID: channel.ChannelID{
			ID:   runtimechannelid.EncodePersonChannel("u1", "u2"),
			Type: frame.ChannelTypePerson,
		},
		StartSeq: 1,
		EndSeq:   10,
		Limit:    10000,
		PullMode: PullModeUp,
	}, reader.queries[0])
}

func TestSyncChannelMessagesReturnsEmptyForMissingChannelRuntime(t *testing.T) {
	app := New(Options{MessageReader: &recordingChannelMessageReader{err: metadb.ErrNotFound}})

	result, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Limit:       30,
	})

	require.NoError(t, err)
	require.False(t, result.More)
	require.Empty(t, result.Messages)
}

func TestSyncChannelMessagesRejectsMissingRequiredFields(t *testing.T) {
	app := New(Options{MessageReader: &recordingChannelMessageReader{}})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
	})

	require.ErrorIs(t, err, ErrSyncLoginUIDRequired)
}

func TestSyncChannelMessagesPropagatesReaderError(t *testing.T) {
	readerErr := errors.New("reader failed")
	app := New(Options{MessageReader: &recordingChannelMessageReader{err: readerErr}})

	_, err := app.SyncChannelMessages(context.Background(), SyncChannelMessagesQuery{
		LoginUID:    "u1",
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Limit:       30,
	})

	require.ErrorIs(t, err, readerErr)
}

type recordingChannelMessageReader struct {
	queries []ChannelMessageQuery
	page    ChannelMessagePage
	err     error
}

func (r *recordingChannelMessageReader) SyncMessages(_ context.Context, query ChannelMessageQuery) (ChannelMessagePage, error) {
	r.queries = append(r.queries, query)
	return r.page, r.err
}
