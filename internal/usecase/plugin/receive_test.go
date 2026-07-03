package plugin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestReceiveOfflineEligibilityMatrix(t *testing.T) {
	cases := []struct {
		name  string
		event OfflineReceiveEvent
		want  bool
	}{
		{
			name:  "durable offline recipient invokes bound receive plugin",
			event: OfflineReceiveEvent{UID: "bot", Message: testReceiveMessage("alice", frame.Framer{})},
			want:  true,
		},
		{
			name:  "sender equals recipient skips",
			event: OfflineReceiveEvent{UID: "bot", Message: testReceiveMessage("bot", frame.Framer{})},
		},
		{
			name:  "system sender skips",
			event: OfflineReceiveEvent{UID: "bot", Message: testReceiveMessage("____system", frame.Framer{})},
		},
		{
			name:  "sync once skips",
			event: OfflineReceiveEvent{UID: "bot", Message: testReceiveMessage("alice", frame.Framer{SyncOnce: true})},
		},
		{
			name:  "no persist skips",
			event: OfflineReceiveEvent{UID: "bot", Message: testReceiveMessage("alice", frame.Framer{NoPersist: true})},
		},
		{
			name:  "request scoped skips",
			event: OfflineReceiveEvent{UID: "bot", RequestScoped: true, Message: testReceiveMessage("alice", frame.Framer{})},
		},
		{
			name:  "temp channel skips",
			event: OfflineReceiveEvent{UID: "bot", Message: testReceiveMessageForChannel("alice", frame.ChannelTypeTemp, frame.Framer{})},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invoker := &receiveHookInvoker{}
			app := newReceiveHookTestApp(t, invoker, func() time.Time { return time.Unix(100, 0).UTC() })

			err := app.ReceiveOffline(context.Background(), tc.event)

			require.NoError(t, err)
			if tc.want {
				require.Len(t, invoker.calls, 1)
				packet := decodeReceivePacket(t, invoker.calls[0].body)
				require.Equal(t, "alice", packet.GetFromUid())
				require.Equal(t, "bot", packet.GetToUid())
				require.Equal(t, "g1", packet.GetChannelId())
				require.Equal(t, uint32(frame.ChannelTypeGroup), packet.GetChannelType())
				require.Equal(t, []byte("hello"), packet.GetPayload())
				return
			}
			require.Empty(t, invoker.calls)
		})
	}
}

func TestReceiveOfflineSelectsHighestPriorityRunningBoundPlugin(t *testing.T) {
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["low"] = ObservedPlugin{No: "low", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 1}
	rt.plugins["high"] = ObservedPlugin{No: "high", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 10, ReplySync: true}
	store := newFakeBindingStore()
	store.bindingsByUID["bot"] = []PluginBinding{{UID: "bot", PluginNo: "low"}, {UID: "bot", PluginNo: "high"}}
	invoker := &receiveHookInvoker{}
	app := mustNewTestApp(t, Options{
		Runtime:          rt,
		DesiredStore:     newFakeDesiredStore(),
		BindingStore:     store,
		Invoker:          invoker,
		DefaultSenderUID: "____system",
		ReceiveDedupeTTL: time.Minute,
		Clock:            func() time.Time { return time.Unix(100, 0).UTC() },
	})

	require.NoError(t, app.ReceiveOffline(context.Background(), OfflineReceiveEvent{UID: "bot", Message: testReceiveMessage("alice", frame.Framer{})}))

	require.Len(t, invoker.calls, 1)
	require.Equal(t, receiveHookCall{no: "high", sync: true, path: PathReceive}, invoker.calls[0].summary())
}

func TestReceiveOfflineDedupeUsesMessageIDAndUIDWithinTTL(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	invoker := &receiveHookInvoker{}
	app := newReceiveHookTestApp(t, invoker, func() time.Time { return now })
	event := OfflineReceiveEvent{UID: "bot", Message: testReceiveMessage("alice", frame.Framer{})}

	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	require.Len(t, invoker.calls, 1)

	event.UID = "bot-2"
	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	require.Len(t, invoker.calls, 2)

	event.UID = "bot"
	now = now.Add(time.Minute + time.Second)
	require.NoError(t, app.ReceiveOffline(context.Background(), event))
	require.Len(t, invoker.calls, 3)
}

func TestReceiveOfflineRetriesAfterHookFailureBeforeDedupe(t *testing.T) {
	invoker := &receiveHookInvoker{sendErr: errors.New("send failed")}
	app := newReceiveHookTestApp(t, invoker, func() time.Time { return time.Unix(100, 0).UTC() })
	event := OfflineReceiveEvent{UID: "bot", Message: testReceiveMessage("alice", frame.Framer{})}

	require.Error(t, app.ReceiveOffline(context.Background(), event))
	invoker.sendErr = nil
	require.NoError(t, app.ReceiveOffline(context.Background(), event))

	require.Len(t, invoker.calls, 2)
}

func TestReceiveOfflineMapsClientChannelView(t *testing.T) {
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
			name:        "person shows sender from recipient view and strips command suffix",
			channelID:   channelid.ToCommandChannel(channelid.EncodePersonChannel("alice", "bot")),
			channelType: frame.ChannelTypePerson,
			want:        "alice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invoker := &receiveHookInvoker{}
			app := newReceiveHookTestApp(t, invoker, func() time.Time { return time.Unix(100, 0).UTC() })
			msg := testReceiveMessageForChannel("alice", tc.channelType, frame.Framer{})
			msg.ChannelID = tc.channelID

			require.NoError(t, app.ReceiveOffline(context.Background(), OfflineReceiveEvent{UID: "bot", Message: msg}))

			require.Len(t, invoker.calls, 1)
			packet := decodeReceivePacket(t, invoker.calls[0].body)
			require.Equal(t, tc.want, packet.GetChannelId())
		})
	}
}

func newReceiveHookTestApp(t *testing.T, invoker *receiveHookInvoker, clock func() time.Time) *App {
	t.Helper()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["bot"] = ObservedPlugin{No: "bot", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 1}
	rt.plugins["bot-2"] = ObservedPlugin{No: "bot-2", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 1}
	store := newFakeBindingStore()
	store.bindingsByUID["bot"] = []PluginBinding{{UID: "bot", PluginNo: "bot"}}
	store.bindingsByUID["bot-2"] = []PluginBinding{{UID: "bot-2", PluginNo: "bot-2"}}
	return mustNewTestApp(t, Options{
		Runtime:          rt,
		DesiredStore:     newFakeDesiredStore(),
		BindingStore:     store,
		Invoker:          invoker,
		DefaultSenderUID: "____system",
		ReceiveDedupeTTL: time.Minute,
		Clock:            clock,
	})
}

func testReceiveMessage(fromUID string, framer frame.Framer) channel.Message {
	return testReceiveMessageForChannel(fromUID, frame.ChannelTypeGroup, framer)
}

func testReceiveMessageForChannel(fromUID string, channelType uint8, framer frame.Framer) channel.Message {
	return channel.Message{
		MessageID:   1001,
		MessageSeq:  12,
		Framer:      framer,
		FromUID:     fromUID,
		ChannelID:   "g1",
		ChannelType: channelType,
		Payload:     []byte("hello"),
	}
}

type receiveHookCall struct {
	no      string
	sync    bool
	path    string
	msgType uint32
	body    []byte
}

func (c receiveHookCall) summary() receiveHookCall {
	return receiveHookCall{no: c.no, sync: c.sync, path: c.path, msgType: c.msgType}
}

type receiveHookInvoker struct {
	calls      []receiveHookCall
	requestErr error
	sendErr    error
}

func (i *receiveHookInvoker) RequestPlugin(_ context.Context, no, path string, body []byte) ([]byte, error) {
	i.calls = append(i.calls, receiveHookCall{no: no, sync: true, path: path, body: append([]byte(nil), body...)})
	return nil, i.requestErr
}

func (i *receiveHookInvoker) SendPlugin(no string, msgType uint32, body []byte) error {
	i.calls = append(i.calls, receiveHookCall{no: no, msgType: msgType, body: append([]byte(nil), body...)})
	return i.sendErr
}

func (i *receiveHookInvoker) Stop(context.Context, string) error { return nil }

func decodeReceivePacket(t *testing.T, body []byte) *pluginproto.RecvPacket {
	t.Helper()
	var packet pluginproto.RecvPacket
	require.NoError(t, packet.Unmarshal(body))
	return &packet
}
