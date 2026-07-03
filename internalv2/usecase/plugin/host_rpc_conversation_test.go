package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestConversationChannelsMapsAuthoritativeReaderResult(t *testing.T) {
	reader := &recordingConversationReader{channels: []message.ChannelID{
		{ID: "g1", Type: frame.ChannelTypeGroup},
		{ID: "p1", Type: frame.ChannelTypePerson},
	}}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, Conversations: reader})
	require.NoError(t, err)

	resp, err := app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{Uid: " u1 "}, "plugin.conversation")

	require.NoError(t, err)
	require.Equal(t, 1, reader.calls)
	require.Equal(t, "u1", reader.uid)
	require.Equal(t, 1000, reader.limit)
	channels := resp.GetChannels()
	require.Len(t, channels, 2)
	require.Equal(t, "g1", channels[0].GetChannelId())
	require.Equal(t, uint32(frame.ChannelTypeGroup), channels[0].GetChannelType())
	require.Equal(t, "p1", channels[1].GetChannelId())
	require.Equal(t, uint32(frame.ChannelTypePerson), channels[1].GetChannelType())
}

func TestConversationChannelsRequiresUIDAndReader(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}})
	require.NoError(t, err)
	_, err = app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{Uid: "u1"}, "plugin.conversation")
	require.ErrorIs(t, err, ErrConversationReaderRequired)

	app, err = NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, Conversations: &recordingConversationReader{}})
	require.NoError(t, err)
	_, err = app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{}, "plugin.conversation")
	require.ErrorIs(t, err, ErrConversationUIDRequired)
}

func TestConversationChannelsPropagatesReaderError(t *testing.T) {
	wantErr := errors.New("conversation read failed")
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, Conversations: &recordingConversationReader{err: wantErr}})
	require.NoError(t, err)

	_, err = app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{Uid: "u1"}, "plugin.conversation")

	require.ErrorIs(t, err, wantErr)
}

func TestConversationChannelsKeepsReaderOrder(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingInvoker{}, Conversations: &recordingConversationReader{channels: []message.ChannelID{
		{ID: "z", Type: frame.ChannelTypeGroup},
		{ID: "a", Type: frame.ChannelTypeGroup},
		{ID: "m", Type: frame.ChannelTypePerson},
	}}})
	require.NoError(t, err)

	resp, err := app.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{Uid: "u1"}, "plugin.conversation")

	require.NoError(t, err)
	require.Equal(t, []string{"z", "a", "m"}, []string{
		resp.GetChannels()[0].GetChannelId(),
		resp.GetChannels()[1].GetChannelId(),
		resp.GetChannels()[2].GetChannelId(),
	})
}

type recordingConversationReader struct {
	calls    int
	uid      string
	limit    int
	channels []message.ChannelID
	err      error
}

func (r *recordingConversationReader) ConversationChannels(_ context.Context, uid string, limit int) ([]message.ChannelID, error) {
	r.calls++
	r.uid = uid
	r.limit = limit
	if r.err != nil {
		return nil, r.err
	}
	return append([]message.ChannelID(nil), r.channels...), nil
}
