package plugin

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestSendMessageMapsPluginRequestToMessageUsecase(t *testing.T) {
	sender := &recordingMessageSender{result: message.SendResult{MessageID: 1001, Reason: message.ReasonSuccess}}
	app, err := NewApp(Options{
		Runtime:          &recordingRuntime{},
		Invoker:          &recordingInvoker{},
		Messages:         sender,
		DefaultSenderUID: "____system",
	})
	require.NoError(t, err)
	payload := []byte("hello")

	resp, err := app.SendMessage(context.Background(), &pluginproto.SendReq{
		Header:      &pluginproto.Header{NoPersist: true, SyncOnce: true, RedDot: true},
		ClientMsgNo: "client-1",
		ChannelId:   "u2",
		ChannelType: uint32(frame.ChannelTypePerson),
		Payload:     payload,
	}, "wk.sender")

	require.NoError(t, err)
	require.Equal(t, int64(1001), resp.GetMessageId())
	require.Equal(t, 1, sender.calls)
	cmd := sender.last
	require.Equal(t, "____system", cmd.FromUID)
	require.Equal(t, "client-1", cmd.ClientMsgNo)
	require.Equal(t, "u2", cmd.ChannelID)
	require.Equal(t, uint8(frame.ChannelTypePerson), cmd.ChannelType)
	require.True(t, cmd.NoPersist)
	require.True(t, cmd.SyncOnce)
	require.True(t, cmd.RedDot)
	require.True(t, cmd.NormalizePersonChannel)
	require.Equal(t, message.SendOriginPlugin, cmd.Origin)
	require.False(t, cmd.SkipPluginHooks)
	require.Equal(t, []byte("hello"), cmd.Payload)
	payload[0] = 'H'
	require.Equal(t, []byte("hello"), cmd.Payload)
}

func TestSendMessagePreservesExplicitFromUID(t *testing.T) {
	sender := &recordingMessageSender{}
	app, err := NewApp(Options{
		Runtime:          &recordingRuntime{},
		Invoker:          &recordingInvoker{},
		Messages:         sender,
		DefaultSenderUID: "____system",
	})
	require.NoError(t, err)

	_, err = app.SendMessage(context.Background(), &pluginproto.SendReq{
		FromUid:     "alice",
		ChannelId:   "room1",
		ChannelType: uint32(frame.ChannelTypeGroup),
		Payload:     []byte("hi"),
	}, "wk.sender")

	require.NoError(t, err)
	require.Equal(t, "alice", sender.last.FromUID)
	require.False(t, sender.last.NormalizePersonChannel)
}

func TestSendMessageRequiresMessageSenderAndDefaultSender(t *testing.T) {
	app, err := NewApp(Options{
		Runtime:          &recordingRuntime{},
		Invoker:          &recordingInvoker{},
		DefaultSenderUID: "____system",
	})
	require.NoError(t, err)

	_, err = app.SendMessage(context.Background(), &pluginproto.SendReq{
		ChannelId:   "room1",
		ChannelType: uint32(frame.ChannelTypeGroup),
		Payload:     []byte("hi"),
	}, "wk.sender")
	require.ErrorIs(t, err, ErrMessageSenderRequired)

	app, err = NewApp(Options{
		Runtime:  &recordingRuntime{},
		Invoker:  &recordingInvoker{},
		Messages: &recordingMessageSender{},
	})
	require.NoError(t, err)
	_, err = app.SendMessage(context.Background(), &pluginproto.SendReq{
		ChannelId:   "room1",
		ChannelType: uint32(frame.ChannelTypeGroup),
		Payload:     []byte("hi"),
	}, "wk.sender")
	require.ErrorIs(t, err, ErrDefaultSenderUIDRequired)
}

type recordingMessageSender struct {
	calls  int
	last   message.SendCommand
	result message.SendResult
	err    error
}

func (s *recordingMessageSender) Send(_ context.Context, cmd message.SendCommand) (message.SendResult, error) {
	s.calls++
	s.last = cmd
	return s.result, s.err
}
