package plugin

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestChannelMessagesReadsAuthoritativePagesAndMapsMessages(t *testing.T) {
	reader := &recordingChannelMessageReader{pages: []message.ChannelMessagePage{{
		Messages: []message.SyncedMessage{{
			MessageID:   1001,
			MessageSeq:  8,
			ClientMsgNo: "client-8",
			Timestamp:   1234,
			FromUID:     "alice",
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			Topic:       "topic-a",
			Payload:     []byte("stored"),
		}},
	}, {
		Messages: []message.SyncedMessage{{
			MessageID:   1002,
			MessageSeq:  12,
			FromUID:     "bob",
			ChannelID:   "p1",
			ChannelType: frame.ChannelTypePerson,
			Payload:     []byte("person"),
		}},
	}}}
	app, err := NewApp(Options{
		Runtime:       &recordingRuntime{},
		Invoker:       &recordingInvoker{},
		MessageReader: reader,
	})
	require.NoError(t, err)

	resp, err := app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{
		ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
			ChannelId:       "g1",
			ChannelType:     uint32(frame.ChannelTypeGroup),
			StartMessageSeq: 7,
			Limit:           1,
		}, {
			ChannelId:       "p1",
			ChannelType:     uint32(frame.ChannelTypePerson),
			StartMessageSeq: 11,
			Limit:           2,
		}},
	}, "plugin.reader")

	require.NoError(t, err)
	require.Equal(t, 2, reader.calls)
	require.Equal(t, message.ChannelMessageQuery{
		ChannelID: message.ChannelID{ID: "g1", Type: frame.ChannelTypeGroup},
		StartSeq:  7,
		Limit:     1,
		PullMode:  message.PullModeUp,
	}, reader.queries[0])
	require.Equal(t, message.ChannelMessageQuery{
		ChannelID: message.ChannelID{ID: "p1", Type: frame.ChannelTypePerson},
		StartSeq:  11,
		Limit:     2,
		PullMode:  message.PullModeUp,
	}, reader.queries[1])

	resps := resp.GetChannelMessageResps()
	require.Len(t, resps, 2)
	require.Equal(t, "g1", resps[0].GetChannelId())
	require.Equal(t, uint32(frame.ChannelTypeGroup), resps[0].GetChannelType())
	require.Equal(t, uint64(7), resps[0].GetStartMessageSeq())
	require.Equal(t, uint32(1), resps[0].GetLimit())
	msgs := resps[0].GetMessages()
	require.Len(t, msgs, 1)
	msg := msgs[0]
	require.Equal(t, int64(1001), msg.GetMessageId())
	require.Equal(t, uint64(8), msg.GetMessageSeq())
	require.Equal(t, "client-8", msg.GetClientMsgNo())
	require.Equal(t, uint32(1234), msg.GetTimestamp())
	require.Equal(t, "alice", msg.GetFrom())
	require.Equal(t, "g1", msg.GetChannelId())
	require.Equal(t, uint32(frame.ChannelTypeGroup), msg.GetChannelType())
	require.Equal(t, "topic-a", msg.GetTopic())
	require.Equal(t, []byte("stored"), msg.GetPayload())
	reader.returnedPayloads[0][0] = 'X'
	require.Equal(t, []byte("stored"), msg.GetPayload())
}

func TestChannelMessagesDefaultsAndCapsLegacyLimit(t *testing.T) {
	reader := &recordingChannelMessageReader{}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, MessageReader: reader})
	require.NoError(t, err)

	_, err = app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{
		ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
			ChannelId:       "default",
			ChannelType:     uint32(frame.ChannelTypeGroup),
			StartMessageSeq: 1,
		}, {
			ChannelId:       "capped",
			ChannelType:     uint32(frame.ChannelTypeGroup),
			StartMessageSeq: 2,
			Limit:           1<<32 - 1,
		}},
	}, "plugin.reader")

	require.NoError(t, err)
	require.Equal(t, 100, reader.queries[0].Limit)
	require.Equal(t, 10000, reader.queries[1].Limit)
}

func TestChannelMessagesReturnsEmptyResponseForMissingChannel(t *testing.T) {
	reader := &recordingChannelMessageReader{err: metadb.ErrNotFound}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, MessageReader: reader})
	require.NoError(t, err)

	resp, err := app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{
		ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
			ChannelId:       "missing",
			ChannelType:     uint32(frame.ChannelTypeGroup),
			StartMessageSeq: 7,
			Limit:           1,
		}},
	}, "plugin.reader")

	require.NoError(t, err)
	resps := resp.GetChannelMessageResps()
	require.Len(t, resps, 1)
	require.Equal(t, "missing", resps[0].GetChannelId())
	require.Empty(t, resps[0].GetMessages())
}

func TestChannelMessagesRequiresReader(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	_, err = app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{
		ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{ChannelId: "g1", ChannelType: uint32(frame.ChannelTypeGroup)}},
	}, "plugin.reader")

	require.ErrorIs(t, err, ErrMessageReaderRequired)
}

type recordingChannelMessageReader struct {
	calls            int
	queries          []message.ChannelMessageQuery
	pages            []message.ChannelMessagePage
	err              error
	returnedPayloads [][]byte
}

func (r *recordingChannelMessageReader) SyncMessages(_ context.Context, query message.ChannelMessageQuery) (message.ChannelMessagePage, error) {
	r.calls++
	r.queries = append(r.queries, query)
	if r.err != nil {
		return message.ChannelMessagePage{}, r.err
	}
	if len(r.pages) == 0 {
		return message.ChannelMessagePage{}, nil
	}
	page := r.pages[0]
	r.pages = r.pages[1:]
	page.Messages = append([]message.SyncedMessage(nil), page.Messages...)
	for i := range page.Messages {
		if page.Messages[i].Payload != nil {
			r.returnedPayloads = append(r.returnedPayloads, page.Messages[i].Payload)
		}
	}
	return page, nil
}
