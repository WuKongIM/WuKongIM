package app

import (
	"context"
	"testing"
	"time"

	runtimeplugin "github.com/WuKongIM/WuKongIM/internal/runtime/plugin"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestNewSkipsPluginSubsystemWhenDisabled(t *testing.T) {
	app, err := newTestApp(t, Config{DataDir: t.TempDir()}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)
	require.Nil(t, app.pluginRuntime)
	require.Nil(t, app.pluginHook)
	require.Nil(t, app.pluginPersistAfter)
}

func TestNewWiresPluginSubsystemWhenEnabled(t *testing.T) {
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)
	require.NotNil(t, app.pluginRuntime)
	require.NotNil(t, app.plugins)
	require.NotNil(t, app.pluginHook)
	require.NotNil(t, app.pluginPersistAfter)
}

func TestNewWiresPluginUsecaseAsMessageSendHook(t *testing.T) {
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(&fakeCluster{}), WithGateway(nil))
	require.NoError(t, err)
	_, err = app.plugins.StartPlugin(context.Background(), &pluginproto.PluginInfo{
		No: "send-plugin", Methods: []string{"Send"},
	}, "send-plugin")
	require.NoError(t, err)

	_, err = app.Messages().Send(context.Background(), message.SendCommand{
		FromUID: "u1", ChannelID: "g1", ChannelType: 2, Payload: []byte("hello"),
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, message.ErrRouteNotReady)
	require.Contains(t, err.Error(), "/plugin/send")
}

func TestNewWiresPluginUsecaseAsMessageSender(t *testing.T) {
	submitter := &recordingAppMessageSubmitter{result: message.SendResult{MessageID: 222, Reason: message.ReasonSuccess}}
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(&fakeCluster{}), WithGateway(nil), WithMessages(message.New(message.Options{Submitter: submitter})))
	require.NoError(t, err)

	resp, err := app.plugins.SendMessage(context.Background(), &pluginproto.SendReq{
		FromUid:     "u1",
		ChannelId:   "g1",
		ChannelType: 2,
		Payload:     []byte("hello"),
	}, "wk.sender")

	require.NoError(t, err)
	require.Equal(t, int64(222), resp.GetMessageId())
	require.Equal(t, 1, submitter.calls)
	require.Equal(t, "u1", submitter.last.FromUID)
	require.Equal(t, message.SendOriginPlugin, submitter.last.Origin)
	require.NotErrorIs(t, err, pluginusecase.ErrMessageSenderRequired)
}

func TestNewWiresPluginUsecaseAsChannelMessageReader(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		conversationMessages: map[metadb.ConversationKey][]channelv2.Message{
			{ChannelID: "g1", ChannelType: 2}: {{
				MessageID:   321,
				MessageSeq:  7,
				ChannelID:   "g1",
				ChannelType: 2,
				FromUID:     "u1",
				ClientMsgNo: "client-7",
				Payload:     []byte("stored"),
			}},
		},
	}
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(cluster), WithGateway(nil))
	require.NoError(t, err)

	resp, err := app.plugins.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{
		ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
			ChannelId:       "g1",
			ChannelType:     2,
			StartMessageSeq: 7,
			Limit:           1,
		}},
	}, "wk.reader")

	require.NoError(t, err)
	require.Len(t, resp.GetChannelMessageResps(), 1)
	msgs := resp.GetChannelMessageResps()[0].GetMessages()
	require.Len(t, msgs, 1)
	require.Equal(t, int64(321), msgs[0].GetMessageId())
	require.Equal(t, []byte("stored"), msgs[0].GetPayload())
}

func TestNewWiresPluginUsecaseAsClusterReader(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		snapshot: control.Snapshot{
			Nodes: []control.Node{{NodeID: 1, Addr: "127.0.0.1:7001", Status: control.NodeAlive}},
			Slots: []control.SlotAssignment{{SlotID: 7, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 3, PreferredLeader: 2}},
		},
	}
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(cluster), WithGateway(nil))
	require.NoError(t, err)

	resp, err := app.plugins.ClusterConfig(context.Background(), "wk.cluster")

	require.NoError(t, err)
	require.Len(t, resp.GetNodes(), 1)
	require.Equal(t, uint64(1), resp.GetNodes()[0].GetId())
	require.Equal(t, "127.0.0.1:7001", resp.GetNodes()[0].GetClusterAddr())
	require.True(t, resp.GetNodes()[0].GetOnline())
	require.Len(t, resp.GetSlots(), 1)
	require.Equal(t, uint32(7), resp.GetSlots()[0].GetId())
	require.Equal(t, uint64(2), resp.GetSlots()[0].GetLeader())
}

func TestNewWiresPluginUsecaseAsChannelOwnerReader(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		channelOwnerMetas: map[channelv2.ChannelID]channelv2.Meta{
			{ID: "g1", Type: 2}: {
				ID:     channelv2.ChannelID{ID: "g1", Type: 2},
				Leader: 3,
			},
		},
	}
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(cluster), WithGateway(nil))
	require.NoError(t, err)

	resp, err := app.plugins.ClusterChannelsBelongNode(context.Background(), &pluginproto.ClusterChannelBelongNodeReq{
		Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}},
	}, "wk.cluster")

	require.NoError(t, err)
	require.Len(t, resp.GetClusterChannelBelongNodeResps(), 1)
	require.Equal(t, uint64(3), resp.GetClusterChannelBelongNodeResps()[0].GetNodeId())
}

func TestNewWiresPluginUsecaseAsConversationReader(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		conversationPages: map[string][]metadb.ConversationState{
			"u1": {
				{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "g1", ChannelType: 2, ActiveAt: 2},
				{UID: "u1", Kind: metadb.ConversationKindNormal, ChannelID: "p1", ChannelType: 1, ActiveAt: 1},
			},
		},
	}
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(cluster), WithGateway(nil))
	require.NoError(t, err)

	resp, err := app.plugins.ConversationChannels(context.Background(), &pluginproto.ConversationChannelReq{Uid: "u1"}, "wk.conversation")

	require.NoError(t, err)
	require.Len(t, resp.GetChannels(), 2)
	require.Equal(t, "g1", resp.GetChannels()[0].GetChannelId())
	require.Equal(t, uint32(2), resp.GetChannels()[0].GetChannelType())
	require.Equal(t, "p1", resp.GetChannels()[1].GetChannelId())
}

func TestNewWiresPluginUsecaseAsHTTPForwarder(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(cluster), WithGateway(nil))
	require.NoError(t, err)

	_, err = app.plugins.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		PluginNo: "wk.http",
		ToNodeId: 2,
		Request: &pluginproto.HttpRequest{
			Method: "GET",
			Path:   "/echo",
		},
	}, "caller")

	require.Error(t, err)
	require.NotErrorIs(t, err, pluginusecase.ErrHTTPForwarderRequired)
	require.Equal(t, uint64(2), cluster.rpcNodeID)
	require.Equal(t, accessnode.ManagerPluginRPCServiceID, cluster.rpcServiceID)
}

func TestNewPassesPluginFailOpenToPluginUsecase(t *testing.T) {
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false, FailOpen: true},
	}, WithCluster(&fakeCluster{}), WithGateway(nil))
	require.NoError(t, err)
	_, err = app.plugins.StartPlugin(context.Background(), &pluginproto.PluginInfo{
		No: "send-plugin", Methods: []string{"Send"},
	}, "send-plugin")
	require.NoError(t, err)

	cmd, reason, err := app.plugins.BeforeSend(context.Background(), message.SendCommand{
		FromUID: "u1", ChannelID: "g1", ChannelType: 2, Payload: []byte("hello"),
	})
	require.NoError(t, err)
	require.Equal(t, message.ReasonSuccess, reason)
	require.Equal(t, []byte("hello"), cmd.Payload)
}

func TestNewRegistersManagerPluginRPCWhenPluginEnabled(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}
	_, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(cluster), WithGateway(nil))
	require.NoError(t, err)

	if _, ok := cluster.registeredHandlers[accessnode.ManagerPluginRPCServiceID]; !ok {
		t.Fatalf("manager plugin rpc handler not registered")
	}
}

func TestPluginRuntimeAdapterPreservesRuntimeFields(t *testing.T) {
	lastSeenAt := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	runtime := runtimeplugin.NewRuntime(runtimeplugin.RuntimeOptions{Registry: runtimeplugin.NewRegistry()})
	adapter := pluginRuntimeAdapter{runtime: runtime}
	err := adapter.RegisterObserved(context.Background(), pluginusecase.ObservedPlugin{
		No:               "wk.persist",
		Name:             "Persist",
		Version:          "v1",
		Methods:          []pluginusecase.Method{pluginusecase.MethodPersistAfter},
		Priority:         9,
		PersistAfterSync: true,
		ReplySync:        true,
		Status:           pluginusecase.StatusRunning,
		Enabled:          true,
		PID:              101,
		LastSeenAt:       lastSeenAt,
		LastError:        "warning",
	})
	require.NoError(t, err)

	plugins := adapter.List()
	require.Len(t, plugins, 1)
	require.True(t, plugins[0].ReplySync)
	require.Equal(t, 101, plugins[0].PID)
	require.Equal(t, lastSeenAt, plugins[0].LastSeenAt)
}

func TestPluginLifecycleStartsBeforeChannelAppendAndStopsAfter(t *testing.T) {
	var calls []string
	var app *App
	pluginHook := &recordingWorkerRuntime{
		name:  "plugin_hook",
		calls: &calls,
		onStart: func() {
			require.False(t, app.channelAppendStarted)
		},
		onStop: func() {
			require.False(t, app.channelAppendStarted)
		},
	}
	app = &App{
		cluster:        &fakeCluster{calls: &calls},
		pluginRuntime:  &recordingWorkerRuntime{name: "plugin_runtime", calls: &calls},
		pluginHook:     pluginHook,
		channelAppends: newNoopStartedChannelAppendGroupForLifecycleTest(),
	}
	require.NoError(t, app.Start(context.Background()))
	require.True(t, app.channelAppendStarted)
	require.NoError(t, app.Stop(context.Background()))
	requireLifecycleOrder(t, calls, []string{
		"cluster.start",
		"plugin_runtime.start",
		"plugin_hook.start",
		"plugin_hook.stop",
		"plugin_runtime.stop",
		"cluster.stop",
	})
}

func newNoopStartedChannelAppendGroupForLifecycleTest() *channelappend.Group {
	return channelappend.New(channelappend.Options{})
}

func requireLifecycleOrder(t *testing.T, calls []string, want []string) {
	t.Helper()
	require.Equal(t, want, calls)
}

func TestPluginPersistAfterAdapterIsAvailableForChannelAppendWhenEnabled(t *testing.T) {
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Cluster: clusterv2.Config{NodeID: 1},
		Plugin:  PluginConfig{Enable: true, HotReload: false},
	}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)
	require.NotNil(t, app.pluginPersistAfter)
}

func TestPluginPersistAfterEnqueuerMapsCommittedEnvelope(t *testing.T) {
	worker := &recordingPluginPersistAfterWorker{}
	enqueuer := pluginPersistAfterEnqueuer{worker: worker}
	source := channelappend.CommittedEnvelope{
		MessageID:         101,
		MessageSeq:        202,
		ChannelID:         "room-a",
		ChannelType:       2,
		FromUID:           "sender-u1",
		SenderNodeID:      9,
		SenderSessionID:   18,
		ClientMsgNo:       "client-1",
		RedDot:            true,
		SyncOnce:          true,
		ServerTimestampMS: 123456789,
		Payload:           []byte("payload"),
		MessageScopedUIDs: []string{"u2", "u3"},
	}

	enqueuer.EnqueuePersistAfter(context.Background(), source)
	source.Payload[0] = 'X'
	source.MessageScopedUIDs[0] = "mutated"

	require.Equal(t, []pluginevents.PersistAfterCommitted{{
		MessageID:         101,
		MessageSeq:        202,
		ChannelID:         "room-a",
		ChannelType:       2,
		FromUID:           "sender-u1",
		SenderNodeID:      9,
		SenderSessionID:   18,
		ClientMsgNo:       "client-1",
		RedDot:            true,
		SyncOnce:          true,
		ServerTimestampMS: 123456789,
		Payload:           []byte("payload"),
		MessageScopedUIDs: []string{"u2", "u3"},
	}}, worker.events)
}

type recordingPluginPersistAfterWorker struct {
	events []pluginevents.PersistAfterCommitted
}

func (w *recordingPluginPersistAfterWorker) EnqueuePersistAfter(_ context.Context, event pluginevents.PersistAfterCommitted) {
	w.events = append(w.events, event)
}

type recordingAppMessageSubmitter struct {
	calls  int
	last   message.SendCommand
	result message.SendResult
	err    error
}

func (s *recordingAppMessageSubmitter) Send(_ context.Context, cmd message.SendCommand) (message.SendResult, error) {
	s.calls++
	s.last = cmd
	return s.result, s.err
}

func (s *recordingAppMessageSubmitter) SendBatch(items []message.SendBatchItem) []message.SendBatchItemResult {
	results := make([]message.SendBatchItemResult, len(items))
	for i, item := range items {
		result, err := s.Send(item.Context, item.Command)
		results[i] = message.SendBatchItemResult{Result: result, Err: err}
	}
	return results
}
