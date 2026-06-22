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
	require.ElementsMatch(t, []string{"/plugin/start", "/close", "/message/send"}, routes.paths)
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
	started          *pluginproto.PluginInfo
	startCalls       int
	startResp        *pluginproto.StartupResp
	startDoneSignal  chan struct{}
	startDeadline    time.Time
	startDeadlineSet bool
	caller           string
	closedPluginNo   string
	closedCaller     string
	closeCalled      chan struct{}
	closeErr         error
	sendCalls        int
	sendReq          *pluginproto.SendReq
	sendResp         *pluginproto.SendResp
	sendCaller       string
	sendDeadline     time.Time
	sendDeadlineSet  bool
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
