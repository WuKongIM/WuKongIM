package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/wkrpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestServerRegistersHostRPCRoutes(t *testing.T) {
	routes := &recordingRoutes{}
	_, err := NewServer(Options{Routes: routes, Usecase: &recordingUsecase{}, Timeout: time.Second})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"/plugin/start", "/close", "/message/send", "/channel/messages", "/cluster/config", "/cluster/channels/belongNode", "/conversation/channels"}, routes.paths)
}

func TestHandlePluginStartDecodesAndWritesStartupResponse(t *testing.T) {
	usecase := &recordingUsecase{}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.echo", mustMarshalProto(t, &pluginproto.PluginInfo{
		No:      "wk.echo",
		Methods: []string{"PersistAfter"},
	}))

	server.handlePath("/plugin/start", ctx)

	require.NoError(t, ctx.err)
	require.Equal(t, "wk.echo", usecase.started.No)
	require.NotEmpty(t, ctx.body)
	require.Equal(t, 1, usecase.startCalls)
}

func TestHandlePluginStartRejectsEmptyCallerUID(t *testing.T) {
	usecase := &recordingUsecase{}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := newTestRPCContext("", mustMarshalProto(t, &pluginproto.PluginInfo{No: "wk.echo"}))

	server.handlePath("/plugin/start", ctx)

	require.ErrorIs(t, ctx.err, errEmptyCallerUID)
	require.Zero(t, usecase.startCalls)
}

func TestHandlePluginStartRejectsOversizedRequestBody(t *testing.T) {
	server, err := NewServer(Options{
		Usecase:      &recordingUsecase{},
		Timeout:      time.Second,
		MaxBodyBytes: 4,
	})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.echo", []byte("oversized"))

	server.handlePath("/plugin/start", ctx)

	require.Error(t, ctx.err)
	require.Contains(t, ctx.err.Error(), "exceeds max bytes")
}

func TestHandlePluginStartRejectsOversizedResponseBody(t *testing.T) {
	usecase := &recordingUsecase{
		startResp: &pluginproto.StartupResp{
			Success: true,
			Config:  []byte("response body is definitely too large"),
		},
	}
	server, err := NewServer(Options{
		Usecase:      usecase,
		Timeout:      time.Second,
		MaxBodyBytes: 16,
	})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.echo", mustMarshalProto(t, &pluginproto.PluginInfo{No: "wk.echo"}))

	server.handlePath("/plugin/start", ctx)

	require.Error(t, ctx.err)
	require.Contains(t, ctx.err.Error(), "response exceeds max bytes")
	require.Empty(t, ctx.body)
}

func TestHandleCloseUsesCallerUIDAndWritesOK(t *testing.T) {
	usecase := &recordingUsecase{}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.echo", nil)

	server.handlePath("/close", ctx)

	require.NoError(t, ctx.err)
	require.True(t, ctx.ok)
	require.Equal(t, "wk.echo", usecase.closedPluginNo)
	require.Equal(t, "wk.echo", usecase.closedCaller)
}

func TestHandleCloseCloseEventDoesNotWriteAndCallsUsecaseAsync(t *testing.T) {
	usecase := &recordingUsecase{closeCalled: make(chan struct{}, 1)}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := &testRPCContext{uid: "wk.echo", closeEvent: true}

	require.NotPanics(t, func() {
		server.handlePath("/close", ctx)
	})

	require.False(t, ctx.ok)
	require.NoError(t, ctx.err)
	select {
	case <-usecase.closeCalled:
	case <-time.After(time.Second):
		t.Fatal("expected ClosePlugin to be called asynchronously")
	}
	require.Equal(t, "wk.echo", usecase.closedPluginNo)
	require.Equal(t, "wk.echo", usecase.closedCaller)
}

func TestHandleSendMessageDecodesUsesTimeoutAndWritesResponse(t *testing.T) {
	usecase := &recordingUsecase{sendResp: &pluginproto.SendResp{MessageId: 987}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.sender", mustMarshalProto(t, &pluginproto.SendReq{
		Header:      &pluginproto.Header{NoPersist: true, SyncOnce: true, RedDot: true},
		ClientMsgNo: "client-1",
		ChannelId:   "g1",
		ChannelType: 2,
		Payload:     []byte("hello"),
	}))

	server.handlePath("/message/send", ctx)

	require.NoError(t, ctx.err)
	require.Equal(t, 1, usecase.sendCalls)
	require.Equal(t, "wk.sender", usecase.sendCaller)
	require.Equal(t, "client-1", usecase.sendReq.GetClientMsgNo())
	require.Equal(t, "g1", usecase.sendReq.GetChannelId())
	require.Equal(t, []byte("hello"), usecase.sendReq.GetPayload())
	require.True(t, usecase.sendReq.GetHeader().GetNoPersist())
	require.True(t, usecase.sendReq.GetHeader().GetSyncOnce())
	require.True(t, usecase.sendReq.GetHeader().GetRedDot())
	require.True(t, usecase.sendDeadlineSet)
	require.WithinDuration(t, time.Now().Add(time.Second), usecase.sendDeadline, 100*time.Millisecond)
	var got pluginproto.SendResp
	require.NoError(t, proto.Unmarshal(ctx.body, &got))
	require.Equal(t, int64(987), got.GetMessageId())
}

func TestHandleChannelMessagesDecodesUsesTimeoutAndWritesResponse(t *testing.T) {
	usecase := &recordingUsecase{channelMessagesResp: &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{
		ChannelId:       "g1",
		ChannelType:     2,
		StartMessageSeq: 7,
		Limit:           1,
		Messages: []*pluginproto.Message{{
			MessageId:   1001,
			MessageSeq:  8,
			ClientMsgNo: "client-8",
			Payload:     []byte("stored"),
		}},
	}}}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.reader", mustMarshalProto(t, &pluginproto.ChannelMessageBatchReq{
		ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
			ChannelId:       "g1",
			ChannelType:     2,
			StartMessageSeq: 7,
			Limit:           1,
		}},
	}))

	server.handlePath("/channel/messages", ctx)

	require.NoError(t, ctx.err)
	require.Equal(t, 1, usecase.channelMessagesCalls)
	require.Equal(t, "wk.reader", usecase.channelMessagesCaller)
	require.Len(t, usecase.channelMessagesReq.GetChannelMessageReqs(), 1)
	require.Equal(t, "g1", usecase.channelMessagesReq.GetChannelMessageReqs()[0].GetChannelId())
	require.True(t, usecase.channelMessagesDeadlineSet)
	require.WithinDuration(t, time.Now().Add(time.Second), usecase.channelMessagesDeadline, 100*time.Millisecond)
	var got pluginproto.ChannelMessageBatchResp
	require.NoError(t, proto.Unmarshal(ctx.body, &got))
	require.Len(t, got.GetChannelMessageResps(), 1)
	require.Equal(t, int64(1001), got.GetChannelMessageResps()[0].GetMessages()[0].GetMessageId())
}

func TestHandleClusterConfigUsesTimeoutAndWritesResponse(t *testing.T) {
	usecase := &recordingUsecase{clusterConfigResp: &pluginproto.ClusterConfig{
		Nodes: []*pluginproto.Node{{
			Id:            1,
			ClusterAddr:   "127.0.0.1:7001",
			ApiServerAddr: "http://127.0.0.1:5001",
			Online:        true,
		}},
		Slots: []*pluginproto.Slot{{
			Id:       7,
			Leader:   2,
			Term:     3,
			Replicas: []uint64{1, 2, 3},
		}},
	}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.cluster", nil)

	server.handlePath("/cluster/config", ctx)

	require.NoError(t, ctx.err)
	require.Equal(t, 1, usecase.clusterConfigCalls)
	require.Equal(t, "wk.cluster", usecase.clusterConfigCaller)
	require.True(t, usecase.clusterConfigDeadlineSet)
	require.WithinDuration(t, time.Now().Add(time.Second), usecase.clusterConfigDeadline, 100*time.Millisecond)
	var got pluginproto.ClusterConfig
	require.NoError(t, proto.Unmarshal(ctx.body, &got))
	require.Len(t, got.GetNodes(), 1)
	require.Equal(t, uint64(1), got.GetNodes()[0].GetId())
	require.Len(t, got.GetSlots(), 1)
	require.Equal(t, uint32(7), got.GetSlots()[0].GetId())
}

func TestHandleClusterChannelsBelongNodeDecodesUsesTimeoutAndWritesResponse(t *testing.T) {
	usecase := &recordingUsecase{clusterBelongNodeResp: &pluginproto.ClusterChannelBelongNodeBatchResp{
		ClusterChannelBelongNodeResps: []*pluginproto.ClusterChannelBelongNodeResp{{
			NodeId:   2,
			Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}},
		}},
	}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.cluster", mustMarshalProto(t, &pluginproto.ClusterChannelBelongNodeReq{
		Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}},
	}))

	server.handlePath("/cluster/channels/belongNode", ctx)

	require.NoError(t, ctx.err)
	require.Equal(t, 1, usecase.clusterBelongNodeCalls)
	require.Equal(t, "wk.cluster", usecase.clusterBelongNodeCaller)
	require.Len(t, usecase.clusterBelongNodeReq.GetChannels(), 1)
	require.Equal(t, "g1", usecase.clusterBelongNodeReq.GetChannels()[0].GetChannelId())
	require.True(t, usecase.clusterBelongNodeDeadlineSet)
	require.WithinDuration(t, time.Now().Add(time.Second), usecase.clusterBelongNodeDeadline, 100*time.Millisecond)
	var got pluginproto.ClusterChannelBelongNodeBatchResp
	require.NoError(t, proto.Unmarshal(ctx.body, &got))
	require.Len(t, got.GetClusterChannelBelongNodeResps(), 1)
	require.Equal(t, uint64(2), got.GetClusterChannelBelongNodeResps()[0].GetNodeId())
}

func TestHandleConversationChannelsDecodesUsesTimeoutAndWritesResponse(t *testing.T) {
	usecase := &recordingUsecase{conversationChannelsResp: &pluginproto.ConversationChannelResp{
		Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}},
	}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	ctx := newTestRPCContext("wk.conversation", mustMarshalProto(t, &pluginproto.ConversationChannelReq{Uid: "u1"}))

	server.handlePath("/conversation/channels", ctx)

	require.NoError(t, ctx.err)
	require.Equal(t, 1, usecase.conversationChannelsCalls)
	require.Equal(t, "wk.conversation", usecase.conversationChannelsCaller)
	require.Equal(t, "u1", usecase.conversationChannelsReq.GetUid())
	require.True(t, usecase.conversationChannelsDeadlineSet)
	require.WithinDuration(t, time.Now().Add(time.Second), usecase.conversationChannelsDeadline, 100*time.Millisecond)
	var got pluginproto.ConversationChannelResp
	require.NoError(t, proto.Unmarshal(ctx.body, &got))
	require.Len(t, got.GetChannels(), 1)
	require.Equal(t, "g1", got.GetChannels()[0].GetChannelId())
}

func TestMessageHostRPCKeepsShorterIncomingDeadline(t *testing.T) {
	usecase := &recordingUsecase{sendResp: &pluginproto.SendResp{MessageId: 1}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	incoming, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ctx := newTimedTestRPCContext(incoming, "wk.sender", mustMarshalProto(t, &pluginproto.SendReq{ClientMsgNo: "client-1"}))

	server.handlePath("/message/send", ctx)

	require.NoError(t, ctx.err)
	require.True(t, usecase.sendDeadlineSet)
	require.WithinDuration(t, time.Now().Add(200*time.Millisecond), usecase.sendDeadline, 100*time.Millisecond)
}

func TestClusterHostRPCKeepsShorterIncomingDeadline(t *testing.T) {
	for _, tc := range []struct {
		name             string
		path             string
		body             []byte
		deadlineObserved func(*recordingUsecase) (time.Time, bool)
	}{
		{
			name: "cluster config",
			path: "/cluster/config",
			deadlineObserved: func(u *recordingUsecase) (time.Time, bool) {
				return u.clusterConfigDeadline, u.clusterConfigDeadlineSet
			},
		},
		{
			name: "/cluster/channels/belongNode",
			path: "/cluster/channels/belongNode",
			body: mustMarshalProto(t, &pluginproto.ClusterChannelBelongNodeReq{}),
			deadlineObserved: func(u *recordingUsecase) (time.Time, bool) {
				return u.clusterBelongNodeDeadline, u.clusterBelongNodeDeadlineSet
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usecase := &recordingUsecase{clusterConfigResp: &pluginproto.ClusterConfig{}, clusterBelongNodeResp: &pluginproto.ClusterChannelBelongNodeBatchResp{}}
			server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
			require.NoError(t, err)
			incoming, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			ctx := newTimedTestRPCContext(incoming, "wk.cluster", tc.body)

			server.handlePath(tc.path, ctx)

			require.NoError(t, ctx.err)
			deadline, ok := tc.deadlineObserved(usecase)
			require.True(t, ok)
			require.WithinDuration(t, time.Now().Add(200*time.Millisecond), deadline, 100*time.Millisecond)
		})
	}
}

func TestConversationHostRPCKeepsShorterIncomingDeadline(t *testing.T) {
	usecase := &recordingUsecase{conversationChannelsResp: &pluginproto.ConversationChannelResp{}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	incoming, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ctx := newTimedTestRPCContext(incoming, "wk.conversation", mustMarshalProto(t, &pluginproto.ConversationChannelReq{Uid: "u1"}))

	server.handlePath("/conversation/channels", ctx)

	require.NoError(t, ctx.err)
	require.True(t, usecase.conversationChannelsDeadlineSet)
	require.WithinDuration(t, time.Now().Add(200*time.Millisecond), usecase.conversationChannelsDeadline, 100*time.Millisecond)
}

func TestChannelMessagesHostRPCKeepsShorterIncomingDeadline(t *testing.T) {
	usecase := &recordingUsecase{channelMessagesResp: &pluginproto.ChannelMessageBatchResp{}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(t, err)
	incoming, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ctx := newTimedTestRPCContext(incoming, "wk.reader", mustMarshalProto(t, &pluginproto.ChannelMessageBatchReq{}))

	server.handlePath("/channel/messages", ctx)

	require.NoError(t, ctx.err)
	require.True(t, usecase.channelMessagesDeadlineSet)
	require.WithinDuration(t, time.Now().Add(200*time.Millisecond), usecase.channelMessagesDeadline, 100*time.Millisecond)
}

func TestHandlePluginStartPassesCancelableTimeoutContextToUsecase(t *testing.T) {
	usecase := &recordingUsecase{
		startResp:       &pluginproto.StartupResp{Success: true},
		startDoneSignal: make(chan struct{}, 1),
	}
	server, err := NewServer(Options{
		Usecase: usecase,
		Timeout: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	ctx := newTimedTestRPCContext(context.Background(), "wk.echo", mustMarshalProto(t, &pluginproto.PluginInfo{No: "wk.echo"}))

	server.handlePath("/plugin/start", ctx)

	require.True(t, usecase.startDeadlineSet)
	require.WithinDuration(t, time.Now().Add(50*time.Millisecond), usecase.startDeadline, 100*time.Millisecond)
	select {
	case <-usecase.startDoneSignal:
	case <-time.After(time.Second):
		t.Fatal("expected handler cancel to reach usecase context")
	}
}

type recordingRoutes struct {
	paths []string
}

func (r *recordingRoutes) Route(path string, _ wkrpc.Handler) {
	r.paths = append(r.paths, path)
}

type recordingUsecase struct {
	started                         *pluginproto.PluginInfo
	startCalls                      int
	startResp                       *pluginproto.StartupResp
	startDoneSignal                 chan struct{}
	startDeadline                   time.Time
	startDeadlineSet                bool
	caller                          string
	closedPluginNo                  string
	closedCaller                    string
	closeCalled                     chan struct{}
	closeErr                        error
	sendCalls                       int
	sendReq                         *pluginproto.SendReq
	sendResp                        *pluginproto.SendResp
	sendCaller                      string
	sendDeadline                    time.Time
	sendDeadlineSet                 bool
	channelMessagesCalls            int
	channelMessagesReq              *pluginproto.ChannelMessageBatchReq
	channelMessagesResp             *pluginproto.ChannelMessageBatchResp
	channelMessagesCaller           string
	channelMessagesDeadline         time.Time
	channelMessagesDeadlineSet      bool
	clusterConfigCalls              int
	clusterConfigResp               *pluginproto.ClusterConfig
	clusterConfigCaller             string
	clusterConfigDeadline           time.Time
	clusterConfigDeadlineSet        bool
	clusterBelongNodeCalls          int
	clusterBelongNodeReq            *pluginproto.ClusterChannelBelongNodeReq
	clusterBelongNodeResp           *pluginproto.ClusterChannelBelongNodeBatchResp
	clusterBelongNodeCaller         string
	clusterBelongNodeDeadline       time.Time
	clusterBelongNodeDeadlineSet    bool
	conversationChannelsCalls       int
	conversationChannelsReq         *pluginproto.ConversationChannelReq
	conversationChannelsResp        *pluginproto.ConversationChannelResp
	conversationChannelsCaller      string
	conversationChannelsDeadline    time.Time
	conversationChannelsDeadlineSet bool
}

func (r *recordingUsecase) StartPlugin(ctx context.Context, info *pluginproto.PluginInfo, callerUID string) (*pluginproto.StartupResp, error) {
	r.started = proto.Clone(info).(*pluginproto.PluginInfo)
	r.startCalls++
	r.caller = callerUID
	if deadline, ok := ctx.Deadline(); ok {
		r.startDeadline = deadline
		r.startDeadlineSet = true
	}
	if r.startDoneSignal != nil {
		done := r.startDoneSignal
		go func() {
			<-ctx.Done()
			select {
			case done <- struct{}{}:
			default:
			}
		}()
	}
	if r.startResp != nil {
		return r.startResp, nil
	}
	return &pluginproto.StartupResp{Success: true}, nil
}

func (r *recordingUsecase) ClosePlugin(_ context.Context, pluginNo string, callerUID string) error {
	r.closedPluginNo = pluginNo
	r.closedCaller = callerUID
	if r.closeCalled != nil {
		select {
		case r.closeCalled <- struct{}{}:
		default:
		}
	}
	return r.closeErr
}

func (r *recordingUsecase) SendMessage(ctx context.Context, req *pluginproto.SendReq, callerUID string) (*pluginproto.SendResp, error) {
	r.sendCalls++
	r.sendReq = proto.Clone(req).(*pluginproto.SendReq)
	r.sendCaller = callerUID
	if deadline, ok := ctx.Deadline(); ok {
		r.sendDeadline = deadline
		r.sendDeadlineSet = true
	}
	if r.sendResp != nil {
		return r.sendResp, nil
	}
	return &pluginproto.SendResp{}, nil
}

func (r *recordingUsecase) ChannelMessages(ctx context.Context, req *pluginproto.ChannelMessageBatchReq, callerUID string) (*pluginproto.ChannelMessageBatchResp, error) {
	r.channelMessagesCalls++
	r.channelMessagesReq = proto.Clone(req).(*pluginproto.ChannelMessageBatchReq)
	r.channelMessagesCaller = callerUID
	if deadline, ok := ctx.Deadline(); ok {
		r.channelMessagesDeadline = deadline
		r.channelMessagesDeadlineSet = true
	}
	if r.channelMessagesResp != nil {
		return r.channelMessagesResp, nil
	}
	return &pluginproto.ChannelMessageBatchResp{}, nil
}

func (r *recordingUsecase) ClusterConfig(ctx context.Context, callerUID string) (*pluginproto.ClusterConfig, error) {
	r.clusterConfigCalls++
	r.clusterConfigCaller = callerUID
	if deadline, ok := ctx.Deadline(); ok {
		r.clusterConfigDeadline = deadline
		r.clusterConfigDeadlineSet = true
	}
	if r.clusterConfigResp != nil {
		return r.clusterConfigResp, nil
	}
	return &pluginproto.ClusterConfig{}, nil
}

func (r *recordingUsecase) ClusterChannelsBelongNode(ctx context.Context, req *pluginproto.ClusterChannelBelongNodeReq, callerUID string) (*pluginproto.ClusterChannelBelongNodeBatchResp, error) {
	r.clusterBelongNodeCalls++
	r.clusterBelongNodeReq = proto.Clone(req).(*pluginproto.ClusterChannelBelongNodeReq)
	r.clusterBelongNodeCaller = callerUID
	if deadline, ok := ctx.Deadline(); ok {
		r.clusterBelongNodeDeadline = deadline
		r.clusterBelongNodeDeadlineSet = true
	}
	if r.clusterBelongNodeResp != nil {
		return r.clusterBelongNodeResp, nil
	}
	return &pluginproto.ClusterChannelBelongNodeBatchResp{}, nil
}

func (r *recordingUsecase) ConversationChannels(ctx context.Context, req *pluginproto.ConversationChannelReq, callerUID string) (*pluginproto.ConversationChannelResp, error) {
	r.conversationChannelsCalls++
	r.conversationChannelsReq = proto.Clone(req).(*pluginproto.ConversationChannelReq)
	r.conversationChannelsCaller = callerUID
	if deadline, ok := ctx.Deadline(); ok {
		r.conversationChannelsDeadline = deadline
		r.conversationChannelsDeadlineSet = true
	}
	if r.conversationChannelsResp != nil {
		return r.conversationChannelsResp, nil
	}
	return &pluginproto.ConversationChannelResp{}, nil
}

type testRPCContext struct {
	baseCtx     context.Context
	uid         string
	requestBody []byte
	body        []byte
	err         error
	ok          bool
	closeEvent  bool
}

func newTestRPCContext(uid string, body []byte) *testRPCContext {
	return newTimedTestRPCContext(context.Background(), uid, body)
}

func newTimedTestRPCContext(baseCtx context.Context, uid string, body []byte) *testRPCContext {
	return &testRPCContext{baseCtx: baseCtx, uid: uid, requestBody: body}
}

func (c *testRPCContext) Context() context.Context { return c.baseCtx }
func (c *testRPCContext) Body() []byte             { return c.requestBody }
func (c *testRPCContext) Uid() string              { return c.uid }
func (c *testRPCContext) Write(data []byte)        { c.body = append([]byte(nil), data...) }
func (c *testRPCContext) WriteOk()                 { c.ok = true }
func (c *testRPCContext) WriteErr(err error)       { c.err = err }
func (c *testRPCContext) CloseEvent() bool         { return c.closeEvent }

func mustMarshalProto(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	require.NoError(t, err)
	return data
}

func BenchmarkMessageSendHostRPCHandler(b *testing.B) {
	usecase := &recordingUsecase{sendResp: &pluginproto.SendResp{MessageId: 1}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(b, err)
	body, err := proto.Marshal(&pluginproto.SendReq{
		Header:      &pluginproto.Header{NoPersist: true, SyncOnce: true},
		ClientMsgNo: "bench-client",
		ChannelId:   "receiver",
		ChannelType: 1,
		Payload:     []byte("hello"),
	})
	require.NoError(b, err)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := newTestRPCContext("bench.plugin", body)
		server.handlePath("/message/send", ctx)
		if ctx.err != nil {
			b.Fatal(ctx.err)
		}
		if len(ctx.body) == 0 {
			b.Fatal("empty response")
		}
	}
}

func BenchmarkChannelMessagesHostRPCHandler(b *testing.B) {
	usecase := &recordingUsecase{channelMessagesResp: &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{
		ChannelId:       "g1",
		ChannelType:     2,
		StartMessageSeq: 1,
		Limit:           1,
		Messages:        []*pluginproto.Message{{MessageId: 1, MessageSeq: 1, Payload: []byte("payload")}},
	}}}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(b, err)
	body, err := proto.Marshal(&pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
		ChannelId:       "g1",
		ChannelType:     2,
		StartMessageSeq: 1,
		Limit:           1,
	}}})
	require.NoError(b, err)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := newTestRPCContext("bench.plugin", body)
		server.handlePath("/channel/messages", ctx)
		if ctx.err != nil {
			b.Fatal(ctx.err)
		}
		if len(ctx.body) == 0 {
			b.Fatal("empty response")
		}
	}
}

func BenchmarkConversationHostRPCHandler(b *testing.B) {
	usecase := &recordingUsecase{conversationChannelsResp: &pluginproto.ConversationChannelResp{
		Channels: []*pluginproto.Channel{
			{ChannelId: "g1", ChannelType: 2},
			{ChannelId: "p1", ChannelType: 1},
		},
	}}
	server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
	require.NoError(b, err)
	body, err := proto.Marshal(&pluginproto.ConversationChannelReq{Uid: "u1"})
	require.NoError(b, err)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := newTestRPCContext("bench.plugin", body)
		server.handlePath("/conversation/channels", ctx)
		if ctx.err != nil {
			b.Fatal(ctx.err)
		}
		if len(ctx.body) == 0 {
			b.Fatal("empty response")
		}
	}
}

func BenchmarkClusterHostRPCHandlers(b *testing.B) {
	b.Run("config", func(b *testing.B) {
		usecase := &recordingUsecase{clusterConfigResp: &pluginproto.ClusterConfig{
			Nodes: []*pluginproto.Node{{Id: 1, ClusterAddr: "127.0.0.1:7001", Online: true}},
			Slots: []*pluginproto.Slot{{Id: 1, Leader: 1, Term: 1, Replicas: []uint64{1, 2, 3}}},
		}}
		server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
		require.NoError(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ctx := newTestRPCContext("bench.plugin", nil)
			server.handlePath("/cluster/config", ctx)
			if ctx.err != nil {
				b.Fatal(ctx.err)
			}
			if len(ctx.body) == 0 {
				b.Fatal("empty response")
			}
		}
	})
	b.Run("belong_node", func(b *testing.B) {
		usecase := &recordingUsecase{clusterBelongNodeResp: &pluginproto.ClusterChannelBelongNodeBatchResp{
			ClusterChannelBelongNodeResps: []*pluginproto.ClusterChannelBelongNodeResp{{
				NodeId:   1,
				Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}},
			}},
		}}
		server, err := NewServer(Options{Usecase: usecase, Timeout: time.Second})
		require.NoError(b, err)
		body, err := proto.Marshal(&pluginproto.ClusterChannelBelongNodeReq{
			Channels: []*pluginproto.Channel{{ChannelId: "g1", ChannelType: 2}},
		})
		require.NoError(b, err)
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ctx := newTestRPCContext("bench.plugin", body)
			server.handlePath("/cluster/channels/belongNode", ctx)
			if ctx.err != nil {
				b.Fatal(ctx.err)
			}
			if len(ctx.body) == 0 {
				b.Fatal("empty response")
			}
		}
	})
}
