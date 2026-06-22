package plugin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestReceiveOfflineEligibilityMatrix(t *testing.T) {
	cases := []struct {
		name  string
		event pluginevents.ReceiveOffline
		want  bool
	}{
		{
			name:  "durable offline recipient invokes bound receive plugin",
			event: testReceiveOfflineEvent("alice"),
			want:  true,
		},
		{
			name:  "sender equals recipient skips",
			event: receiveOfflineWith(func(event *pluginevents.ReceiveOffline) { event.FromUID = event.UID }),
		},
		{
			name:  "system sender skips",
			event: receiveOfflineWith(func(event *pluginevents.ReceiveOffline) { event.FromUID = "____system" }),
		},
		{
			name:  "sync once skips",
			event: receiveOfflineWith(func(event *pluginevents.ReceiveOffline) { event.SyncOnce = true }),
		},
		{
			name:  "no persist skips",
			event: receiveOfflineWith(func(event *pluginevents.ReceiveOffline) { event.NoPersist = true }),
		},
		{
			name:  "request scoped skips",
			event: receiveOfflineWith(func(event *pluginevents.ReceiveOffline) { event.MessageScopedUIDs = []string{"bot"} }),
		},
		{
			name:  "temp channel skips",
			event: receiveOfflineWith(func(event *pluginevents.ReceiveOffline) { event.ChannelType = frame.ChannelTypeTemp }),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invoker := &recordingInvoker{}
			app := newReceiveHookTestApp(t, invoker, func() time.Time { return time.Unix(100, 0).UTC() })

			err := app.ReceiveOffline(context.Background(), tc.event)

			require.NoError(t, err)
			if tc.want {
				sends := invoker.sentMessages()
				require.Len(t, sends, 1)
				require.Equal(t, MsgTypeReceive, sends[0].msgType)
				packet := decodeReceivePacket(t, sends[0].body)
				require.Equal(t, "alice", packet.GetFromUid())
				require.Equal(t, "bot", packet.GetToUid())
				require.Equal(t, "g1", packet.GetChannelId())
				require.Equal(t, uint32(frame.ChannelTypeGroup), packet.GetChannelType())
				require.Equal(t, []byte("hello"), packet.GetPayload())
				return
			}
			require.Empty(t, invoker.sentMessages())
		})
	}
}

func TestReceiveOfflineSelectsHighestPriorityRunningBoundPlugin(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "low", Methods: []Method{MethodReceive}, Priority: 1, Status: StatusRunning, Enabled: true},
		{No: "send-only", Methods: []Method{MethodSend}, Priority: 100, Status: StatusRunning, Enabled: true},
		{No: "offline", Methods: []Method{MethodReceive}, Priority: 99, Status: StatusOffline, Enabled: true},
		{No: "high", Methods: []Method{MethodReceive}, Priority: 10, Status: StatusRunning, Enabled: true, ReplySync: true},
	}}
	invoker := &recordingInvoker{}
	app, err := NewApp(Options{
		Runtime:          runtime,
		Invoker:          invoker,
		ReceiveBindings:  receiveBindingReader{bindingsByUID: map[string][]PluginBinding{"bot": {{UID: "bot", PluginNo: "low"}, {UID: "bot", PluginNo: "high"}}}},
		DefaultSenderUID: "____system",
		ReceiveDedupeTTL: time.Minute,
	})
	require.NoError(t, err)

	require.NoError(t, app.ReceiveOffline(context.Background(), testReceiveOfflineEvent("alice")))

	require.Equal(t, []string{"high:" + PathReceive}, invoker.requests)
	require.Empty(t, invoker.sentMessages())
}

func TestReceiveOfflineDedupeUsesMessageIDAndUIDWithinTTL(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	invoker := &recordingInvoker{}
	app := newReceiveHookTestApp(t, invoker, func() time.Time { return now })
	event := testReceiveOfflineEvent("alice")

	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	require.Len(t, invoker.sentMessages(), 1)

	event.UID = "bot-2"
	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	require.Len(t, invoker.sentMessages(), 2)

	event.UID = "bot"
	now = now.Add(time.Minute + time.Second)
	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	require.Len(t, invoker.sentMessages(), 3)
}

func TestReceiveOfflineRetriesAfterHookFailureBeforeDedupe(t *testing.T) {
	sentinel := errors.New("send failed")
	invoker := &recordingInvoker{sendErrors: map[string]error{"bot": sentinel}}
	app := newReceiveHookTestApp(t, invoker, func() time.Time { return time.Unix(100, 0).UTC() })
	event := testReceiveOfflineEvent("alice")

	err := app.ReceiveOffline(context.Background(), event)
	require.ErrorIs(t, err, sentinel)

	invoker.sendErrors = nil
	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	require.Len(t, invoker.sentMessages(), 2)
}

func TestReceiveOfflineMapsRecipientChannelView(t *testing.T) {
	cases := []struct {
		name        string
		channelID   string
		channelType uint8
		want        string
	}{
		{
			name:        "command group strips suffix",
			channelID:   channelid.ToCommandChannel("g1"),
			channelType: frame.ChannelTypeGroup,
			want:        "g1",
		},
		{
			name:        "person shows counterpart from recipient view and strips command suffix",
			channelID:   channelid.ToCommandChannel(channelid.EncodePersonChannel("alice", "bot")),
			channelType: frame.ChannelTypePerson,
			want:        "alice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invoker := &recordingInvoker{}
			app := newReceiveHookTestApp(t, invoker, func() time.Time { return time.Unix(100, 0).UTC() })
			event := testReceiveOfflineEvent("alice")
			event.ChannelID = tc.channelID
			event.ChannelType = tc.channelType

			require.NoError(t, app.ReceiveOffline(context.Background(), event))

			sends := invoker.sentMessages()
			require.Len(t, sends, 1)
			packet := decodeReceivePacket(t, sends[0].body)
			require.Equal(t, tc.want, packet.GetChannelId())
		})
	}
}

func TestReceiveOfflineClonesPayloadAcrossPluginBoundary(t *testing.T) {
	payload := []byte("hello")
	invoker := &recordingInvoker{}
	app := newReceiveHookTestApp(t, invoker, func() time.Time { return time.Unix(100, 0).UTC() })
	event := testReceiveOfflineEvent("alice")
	event.Payload = payload

	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	payload[0] = 'H'

	packet := decodeReceivePacket(t, invoker.sentMessages()[0].body)
	require.Equal(t, []byte("hello"), packet.GetPayload())
}

func TestStartPluginRegistersReceiveMethod(t *testing.T) {
	runtime := &recordingRuntime{}
	app, err := NewApp(Options{Runtime: runtime, Invoker: &recordingInvoker{}})
	require.NoError(t, err)

	resp, err := app.StartPlugin(context.Background(), &pluginproto.PluginInfo{
		No:      "receiver",
		Methods: []string{"Receive", "Send"},
	}, "receiver")
	require.NoError(t, err)
	require.True(t, resp.GetSuccess())

	require.Equal(t, []Method{MethodReceive, MethodSend}, runtime.registeredPlugins()[0].Methods)
}

func newReceiveHookTestApp(t *testing.T, invoker *recordingInvoker, clock func() time.Time) *App {
	t.Helper()
	app, err := NewApp(Options{
		Runtime: &recordingRuntime{plugins: []ObservedPlugin{
			{No: "bot", Methods: []Method{MethodReceive}, Priority: 1, Status: StatusRunning, Enabled: true},
			{No: "bot-2", Methods: []Method{MethodReceive}, Priority: 1, Status: StatusRunning, Enabled: true},
		}},
		Invoker: invoker,
		ReceiveBindings: receiveBindingReader{bindingsByUID: map[string][]PluginBinding{
			"bot":   {{UID: "bot", PluginNo: "bot"}},
			"bot-2": {{UID: "bot-2", PluginNo: "bot-2"}},
		}},
		DefaultSenderUID: "____system",
		ReceiveDedupeTTL: time.Minute,
		Clock:            clock,
	})
	require.NoError(t, err)
	return app
}

func testReceiveOfflineEvent(fromUID string) pluginevents.ReceiveOffline {
	return pluginevents.ReceiveOffline{
		MessageID:         1001,
		MessageSeq:        12,
		ChannelID:         "g1",
		ChannelType:       frame.ChannelTypeGroup,
		FromUID:           fromUID,
		UID:               "bot",
		ClientMsgNo:       "client-1",
		ServerTimestampMS: 1713859200123,
		Payload:           []byte("hello"),
	}
}

func receiveOfflineWith(mutator func(*pluginevents.ReceiveOffline)) pluginevents.ReceiveOffline {
	event := testReceiveOfflineEvent("alice")
	mutator(&event)
	return event
}

type receiveBindingReader struct {
	bindingsByUID map[string][]PluginBinding
	err           error
}

func (r receiveBindingReader) ListPluginBindingsByUID(_ context.Context, uid string) ([]PluginBinding, error) {
	if r.err != nil {
		return nil, r.err
	}
	return append([]PluginBinding(nil), r.bindingsByUID[uid]...), nil
}

func decodeReceivePacket(t *testing.T, body []byte) *pluginproto.RecvPacket {
	t.Helper()
	var packet pluginproto.RecvPacket
	require.NoError(t, packet.Unmarshal(body))
	return &packet
}
