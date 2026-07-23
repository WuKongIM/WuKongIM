package plugin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pluginevents "github.com/WuKongIM/WuKongIM/internal/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
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

func TestReceiveOfflineBatchSnapshotsPluginsOnceAndPreservesRecipientOrder(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "bot", Methods: []Method{MethodReceive}, Priority: 1, Status: StatusRunning, Enabled: true},
		{No: "bot-2", Methods: []Method{MethodReceive}, Priority: 1, Status: StatusRunning, Enabled: true},
	}}
	bindings := &countingReceiveBindingReader{bindingsByUID: map[string][]PluginBinding{
		"bot":   {{UID: "bot", PluginNo: "bot"}},
		"bot-2": {{UID: "bot-2", PluginNo: "bot-2"}},
	}}
	invoker := &recordingInvoker{}
	app, err := NewApp(Options{
		Runtime:          runtime,
		Invoker:          invoker,
		ReceiveBindings:  bindings,
		DefaultSenderUID: "____system",
		ReceiveDedupeTTL: time.Minute,
	})
	require.NoError(t, err)

	event := testReceiveOfflineBatchEvent("alice", "bot", "bot-2", "bot")
	require.NoError(t, app.ReceiveOfflineBatch(context.Background(), event))

	sends := invoker.sentMessages()
	require.Len(t, sends, 2)
	require.Equal(t, "bot", decodeReceivePacket(t, sends[0].body).GetToUid())
	require.Equal(t, "bot-2", decodeReceivePacket(t, sends[1].body).GetToUid())
	require.Equal(t, 1, runtime.listCallCount())
	require.Equal(t, []string{"bot", "bot-2"}, bindings.calledUIDs())
}

func TestReceiveOfflineBatchSkipsBindingReadsWhenNoReceivePluginIsRunning(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "send-only", Methods: []Method{MethodSend}, Priority: 1, Status: StatusRunning, Enabled: true},
	}}
	bindings := &countingReceiveBindingReader{bindingsByUID: map[string][]PluginBinding{
		"bot": {{UID: "bot", PluginNo: "send-only"}},
	}}
	invoker := &recordingInvoker{}
	app, err := NewApp(Options{
		Runtime:         runtime,
		Invoker:         invoker,
		ReceiveBindings: bindings,
	})
	require.NoError(t, err)

	require.NoError(t, app.ReceiveOfflineBatch(context.Background(), testReceiveOfflineBatchEvent("alice", "bot")))

	require.Empty(t, invoker.sentMessages())
	require.Equal(t, 1, runtime.listCallCount())
	require.Empty(t, bindings.calledUIDs())
}

func TestReceiveOfflineBatchIgnoresDesiredStateFailuresForUnrelatedPlugins(t *testing.T) {
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "send-only", Methods: []Method{MethodSend}, Priority: 1, Status: StatusRunning, Enabled: true},
	}}
	store := newRecordingDesiredStore()
	store.err = errors.New("unrelated desired-state failure")
	app, err := NewApp(Options{
		Runtime:      runtime,
		Invoker:      &recordingInvoker{},
		DesiredStore: store,
	})
	require.NoError(t, err)

	require.NoError(t, app.ReceiveOfflineBatch(context.Background(), testReceiveOfflineBatchEvent("alice", "bot")))
}

func TestReceiveOfflineBatchContinuesAfterIndependentRecipientFailure(t *testing.T) {
	sentinel := errors.New("first recipient failed")
	runtime := &recordingRuntime{plugins: []ObservedPlugin{
		{No: "bad-plugin", Methods: []Method{MethodReceive}, Priority: 1, Status: StatusRunning, Enabled: true},
		{No: "good-plugin", Methods: []Method{MethodReceive}, Priority: 1, Status: StatusRunning, Enabled: true},
	}}
	invoker := &recordingInvoker{sendErrors: map[string]error{"bad-plugin": sentinel}}
	app, err := NewApp(Options{
		Runtime: runtime,
		Invoker: invoker,
		ReceiveBindings: receiveBindingReader{bindingsByUID: map[string][]PluginBinding{
			"bot":   {{UID: "bot", PluginNo: "bad-plugin"}},
			"bot-2": {{UID: "bot-2", PluginNo: "good-plugin"}},
		}},
	})
	require.NoError(t, err)

	err = app.ReceiveOfflineBatch(context.Background(), testReceiveOfflineBatchEvent("alice", "bot", "bot-2"))

	require.ErrorIs(t, err, sentinel)
	sends := invoker.sentMessages()
	require.Len(t, sends, 2)
	require.Equal(t, "bot", decodeReceivePacket(t, sends[0].body).GetToUid())
	require.Equal(t, "bot-2", decodeReceivePacket(t, sends[1].body).GetToUid())
}

func TestReceiveOfflineBatchTimeoutIsIndependentPerRecipient(t *testing.T) {
	invoker := &timeoutFirstReceiveInvoker{}
	app, err := NewApp(Options{
		Runtime: &recordingRuntime{plugins: []ObservedPlugin{
			{No: "slow-plugin", Methods: []Method{MethodReceive}, Priority: 1, Status: StatusRunning, Enabled: true, ReplySync: true},
			{No: "fast-plugin", Methods: []Method{MethodReceive}, Priority: 1, Status: StatusRunning, Enabled: true, ReplySync: true},
		}},
		Invoker: invoker,
		ReceiveBindings: receiveBindingReader{bindingsByUID: map[string][]PluginBinding{
			"slow": {{UID: "slow", PluginNo: "slow-plugin"}},
			"fast": {{UID: "fast", PluginNo: "fast-plugin"}},
		}},
	})
	require.NoError(t, err)

	err = app.ReceiveOfflineBatchWithTimeout(
		context.Background(),
		testReceiveOfflineBatchEvent("alice", "slow", "fast"),
		20*time.Millisecond,
	)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, []string{"slow-plugin", "fast-plugin"}, invoker.calledPluginNos())
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

func testReceiveOfflineBatchEvent(fromUID string, uids ...string) pluginevents.ReceiveOfflineBatch {
	event := testReceiveOfflineEvent(fromUID)
	return pluginevents.ReceiveOfflineBatch{
		MessageID:         event.MessageID,
		MessageSeq:        event.MessageSeq,
		ChannelID:         event.ChannelID,
		ChannelType:       event.ChannelType,
		FromUID:           event.FromUID,
		UIDs:              append([]string(nil), uids...),
		ClientMsgNo:       event.ClientMsgNo,
		ServerTimestampMS: event.ServerTimestampMS,
		Payload:           append([]byte(nil), event.Payload...),
		NoPersist:         event.NoPersist,
		SyncOnce:          event.SyncOnce,
		MessageScopedUIDs: append([]string(nil), event.MessageScopedUIDs...),
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

type countingReceiveBindingReader struct {
	bindingsByUID map[string][]PluginBinding
	calls         []string
}

type timeoutFirstReceiveInvoker struct {
	mu      sync.Mutex
	plugins []string
}

func (i *timeoutFirstReceiveInvoker) RequestPlugin(ctx context.Context, pluginNo, _ string, _ []byte) ([]byte, error) {
	i.mu.Lock()
	i.plugins = append(i.plugins, pluginNo)
	i.mu.Unlock()
	if pluginNo == "slow-plugin" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, nil
}

func (i *timeoutFirstReceiveInvoker) SendPlugin(string, uint32, []byte) error {
	return nil
}

func (i *timeoutFirstReceiveInvoker) calledPluginNos() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]string(nil), i.plugins...)
}

func (r *countingReceiveBindingReader) ListPluginBindingsByUID(_ context.Context, uid string) ([]PluginBinding, error) {
	r.calls = append(r.calls, uid)
	return append([]PluginBinding(nil), r.bindingsByUID[uid]...), nil
}

func (r *countingReceiveBindingReader) calledUIDs() []string {
	return append([]string(nil), r.calls...)
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
