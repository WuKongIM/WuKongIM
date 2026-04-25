package management

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestListMessagesRejectsInvalidChannelType(t *testing.T) {
	app := New(Options{})

	_, err := app.ListMessages(context.Background(), ListMessagesRequest{
		ChannelID:   "room-1",
		ChannelType: 0,
		Limit:       10,
	})

	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestListMessagesMapsQueryPage(t *testing.T) {
	reader := &fakeMessageReader{
		result: MessageQueryPage{
			Items: []channel.Message{{
				MessageID:   101,
				MessageSeq:  9,
				ClientMsgNo: "c-101",
				ChannelID:   "room-1",
				ChannelType: 2,
				FromUID:     "u1",
				Timestamp:   1713859200,
				Payload:     []byte("hello"),
			}},
			HasMore:       true,
			NextBeforeSeq: 9,
		},
	}
	app := New(Options{Messages: reader})

	result, err := app.ListMessages(context.Background(), ListMessagesRequest{
		ChannelID:   "room-1",
		ChannelType: 2,
		Limit:       1,
		Cursor:      MessageListCursor{BeforeSeq: 12},
		MessageID:   101,
		ClientMsgNo: "c-101",
	})

	require.NoError(t, err)
	require.Equal(t, MessageQueryRequest{
		ChannelID:   channel.ChannelID{ID: "room-1", Type: 2},
		Limit:       1,
		BeforeSeq:   12,
		MessageID:   101,
		ClientMsgNo: "c-101",
	}, reader.lastReq)
	require.Equal(t, ListMessagesResponse{
		Items: []Message{{
			MessageID:   101,
			MessageSeq:  9,
			ClientMsgNo: "c-101",
			ChannelID:   "room-1",
			ChannelType: 2,
			FromUID:     "u1",
			Timestamp:   1713859200,
			Payload:     []byte("hello"),
		}},
		HasMore:    true,
		NextCursor: MessageListCursor{BeforeSeq: 9},
	}, result)
}

func TestListMessagesPropagatesReaderErrors(t *testing.T) {
	app := New(Options{Messages: &fakeMessageReader{err: errors.New("boom")}})

	_, err := app.ListMessages(context.Background(), ListMessagesRequest{
		ChannelID:   "room-1",
		ChannelType: 2,
		Limit:       10,
	})

	require.EqualError(t, err, "boom")
}

type fakeMessageReader struct {
	lastReq MessageQueryRequest
	result  MessageQueryPage
	err     error
}

func (f *fakeMessageReader) QueryMessages(_ context.Context, req MessageQueryRequest) (MessageQueryPage, error) {
	f.lastReq = req
	return f.result, f.err
}
