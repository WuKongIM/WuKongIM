package plugin

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/wkrpc"
	"google.golang.org/protobuf/proto"
)

func TestConstructorRequiresPositiveTimeoutAndDefaultsMaxBody(t *testing.T) {
	_, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: &fakeUsecase{}})
	if err == nil {
		t.Fatal("expected missing timeout error")
	}

	srv, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: &fakeUsecase{}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	if srv.MaxBodyBytes() != 10<<20 {
		t.Fatalf("MaxBodyBytes = %d, want %d", srv.MaxBodyBytes(), 10<<20)
	}
}

func TestRegisterRoutes(t *testing.T) {
	routes := &fakeRoutes{}
	_, err := NewServer(Options{Routes: routes, Usecase: &fakeUsecase{}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	want := []string{
		"/plugin/start",
		"/close",
		"/message/send",
		"/channel/messages",
		"/plugin/httpForward",
		"/cluster/config",
		"/cluster/channels/belongNode",
		"/conversation/channels",
		"/stream/open",
		"/stream/write",
		"/stream/close",
	}
	if !reflect.DeepEqual(routes.paths, want) {
		t.Fatalf("registered paths = %#v, want %#v", routes.paths, want)
	}
	for _, path := range want {
		if routes.handlers[path] == nil {
			t.Fatalf("route %s registered nil handler", path)
		}
	}
}

func TestLifecycleStartValidAndEmptyPluginNumber(t *testing.T) {
	uc := &fakeUsecase{startupResp: &pluginproto.StartupResp{NodeId: 7, Success: true}}
	srv := mustServer(t, uc)

	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.PluginInfo{No: "plug-a", Name: "Plugin A"}))
	ctx.uid = "caller-1"
	srv.handlePath("/plugin/start", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if uc.startInfo == nil || uc.startInfo.No != "plug-a" {
		t.Fatalf("StartPlugin info = %#v", uc.startInfo)
	}
	if uc.startCaller != "caller-1" {
		t.Fatalf("StartPlugin caller = %q", uc.startCaller)
	}
	var got pluginproto.StartupResp
	mustUnmarshal(t, ctx.written, &got)
	if got.NodeId != 7 || !got.Success {
		t.Fatalf("startup response = %#v", &got)
	}

	emptyCtx := newFakeRPCContext(mustMarshal(t, &pluginproto.PluginInfo{Name: "missing no"}))
	srv.handlePath("/plugin/start", emptyCtx)
	if emptyCtx.err == nil {
		t.Fatal("expected WriteErr for empty plugin number")
	}
	if uc.startCalls != 1 {
		t.Fatalf("StartPlugin calls = %d, want 1", uc.startCalls)
	}
}

func TestLifecycleCloseCallsUsecaseAndWritesOK(t *testing.T) {
	uc := &fakeUsecase{}
	srv := mustServer(t, uc)
	ctx := newFakeRPCContext([]byte("plug-a"))
	ctx.uid = "caller-2"

	srv.handlePath("/close", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if !ctx.ok {
		t.Fatal("expected WriteOk")
	}
	if uc.closePluginNo != "plug-a" || uc.closeCaller != "caller-2" {
		t.Fatalf("ClosePlugin args = (%q, %q)", uc.closePluginNo, uc.closeCaller)
	}
}

func TestStreamRoutesWriteStableUnimplementedError(t *testing.T) {
	srv := mustServer(t, &fakeUsecase{})
	for _, path := range []string{"/stream/open", "/stream/write", "/stream/close"} {
		t.Run(path, func(t *testing.T) {
			ctx := newFakeRPCContext(nil)
			srv.handlePath(path, ctx)
			if ctx.err == nil {
				t.Fatal("expected WriteErr")
			}
			if ctx.err.Error() != "plugin stream rpc unimplemented in phase 1" {
				t.Fatalf("error = %q", ctx.err.Error())
			}
		})
	}
}

func TestCodecRejectsBodyLargerThanMax(t *testing.T) {
	uc := &fakeUsecase{}
	routes := &fakeRoutes{}
	srv, err := NewServer(Options{Routes: routes, Usecase: uc, Timeout: time.Second, MaxBodyBytes: 3})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	ctx := newFakeRPCContext([]byte{1, 2, 3, 4})
	srv.handlePath("/message/send", ctx)

	if ctx.err == nil {
		t.Fatal("expected max body WriteErr")
	}
	if uc.sendCalls != 0 {
		t.Fatalf("SendMessage calls = %d, want 0", uc.sendCalls)
	}
}

func TestTimeoutWrapsAllHostRPCRoutesAndKeepsShorterIncomingDeadline(t *testing.T) {
	for _, tc := range []struct {
		path string
		body []byte
	}{
		{path: "/plugin/start", body: mustMarshal(t, &pluginproto.PluginInfo{No: "plug-timeout"})},
		{path: "/close", body: []byte("plug-timeout")},
		{path: "/message/send", body: mustMarshal(t, &pluginproto.SendReq{ClientMsgNo: "c1"})},
		{path: "/channel/messages", body: mustMarshal(t, &pluginproto.ChannelMessageBatchReq{})},
		{path: "/plugin/httpForward", body: mustMarshal(t, &pluginproto.ForwardHttpReq{PluginNo: "plug-timeout"})},
		{path: "/cluster/config", body: nil},
		{path: "/cluster/channels/belongNode", body: mustMarshal(t, &pluginproto.ClusterChannelBelongNodeReq{})},
		{path: "/conversation/channels", body: mustMarshal(t, &pluginproto.ConversationChannelReq{Uid: "u1"})},
	} {
		t.Run(tc.path+"/adds timeout", func(t *testing.T) {
			uc := &fakeUsecase{}
			srv := mustServer(t, uc)
			ctx := newFakeRPCContext(tc.body)
			srv.handlePath(tc.path, ctx)
			if ctx.err != nil {
				t.Fatalf("unexpected WriteErr: %v", ctx.err)
			}
			deadline, ok := uc.lastCtx.Deadline()
			if !ok {
				t.Fatal("usecase context has no deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > time.Second {
				t.Fatalf("deadline remaining = %v, want within server timeout", remaining)
			}
		})

		t.Run(tc.path+"/keeps shorter incoming deadline", func(t *testing.T) {
			uc := &fakeUsecase{}
			srv := mustServer(t, uc)
			incoming, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			ctx := newFakeRPCContext(tc.body)
			ctx.ctx = incoming
			srv.handlePath(tc.path, ctx)
			if ctx.err != nil {
				t.Fatalf("unexpected WriteErr: %v", ctx.err)
			}
			deadline, ok := uc.lastCtx.Deadline()
			if !ok {
				t.Fatal("usecase context has no deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > 50*time.Millisecond {
				t.Fatalf("deadline remaining = %v, want incoming shorter deadline", remaining)
			}
		})
	}
}

type fakeRoutes struct {
	paths    []string
	handlers map[string]wkrpc.Handler
}

func (f *fakeRoutes) Route(path string, handler wkrpc.Handler) {
	if f.handlers == nil {
		f.handlers = make(map[string]wkrpc.Handler)
	}
	f.paths = append(f.paths, path)
	f.handlers[path] = handler
}

type fakeRPCContext struct {
	ctx     context.Context
	body    []byte
	uid     string
	written []byte
	err     error
	ok      bool
}

func newFakeRPCContext(body []byte) *fakeRPCContext {
	return &fakeRPCContext{ctx: context.Background(), body: body}
}

func (f *fakeRPCContext) Context() context.Context { return f.ctx }
func (f *fakeRPCContext) Body() []byte             { return f.body }
func (f *fakeRPCContext) Uid() string              { return f.uid }
func (f *fakeRPCContext) Write(data []byte)        { f.written = append([]byte(nil), data...) }
func (f *fakeRPCContext) WriteOk()                 { f.ok = true }
func (f *fakeRPCContext) WriteErr(err error)       { f.err = err }

type fakeUsecase struct {
	lastCtx context.Context

	startupResp *pluginproto.StartupResp
	startCalls  int
	startInfo   *pluginproto.PluginInfo
	startCaller string

	closePluginNo string
	closeCaller   string

	sendCalls int
}

func (f *fakeUsecase) StartPlugin(ctx context.Context, info *pluginproto.PluginInfo, callerUID string) (*pluginproto.StartupResp, error) {
	f.lastCtx = ctx
	f.startCalls++
	f.startInfo = info
	f.startCaller = callerUID
	if f.startupResp != nil {
		return f.startupResp, nil
	}
	return &pluginproto.StartupResp{Success: true}, nil
}

func (f *fakeUsecase) ClosePlugin(ctx context.Context, pluginNo string, callerUID string) error {
	f.lastCtx = ctx
	f.closePluginNo = pluginNo
	f.closeCaller = callerUID
	return nil
}

func (f *fakeUsecase) SendMessage(ctx context.Context, req *pluginproto.SendReq, callerUID string) (*pluginproto.SendResp, error) {
	f.lastCtx = ctx
	f.sendCalls++
	return &pluginproto.SendResp{MessageId: 123}, nil
}

func (f *fakeUsecase) ChannelMessages(ctx context.Context, req *pluginproto.ChannelMessageBatchReq, callerUID string) (*pluginproto.ChannelMessageBatchResp, error) {
	f.lastCtx = ctx
	return &pluginproto.ChannelMessageBatchResp{}, nil
}

func (f *fakeUsecase) HTTPForward(ctx context.Context, req *pluginproto.ForwardHttpReq, callerUID string) (*pluginproto.HttpResponse, error) {
	f.lastCtx = ctx
	return &pluginproto.HttpResponse{}, nil
}

func (f *fakeUsecase) ClusterConfig(ctx context.Context, callerUID string) (*pluginproto.ClusterConfig, error) {
	f.lastCtx = ctx
	return &pluginproto.ClusterConfig{}, nil
}

func (f *fakeUsecase) ClusterChannelsBelongNode(ctx context.Context, req *pluginproto.ClusterChannelBelongNodeReq, callerUID string) (*pluginproto.ClusterChannelBelongNodeBatchResp, error) {
	f.lastCtx = ctx
	return &pluginproto.ClusterChannelBelongNodeBatchResp{}, nil
}

func (f *fakeUsecase) ConversationChannels(ctx context.Context, req *pluginproto.ConversationChannelReq, callerUID string) (*pluginproto.ConversationChannelResp, error) {
	f.lastCtx = ctx
	return &pluginproto.ConversationChannelResp{}, nil
}

func mustServer(t *testing.T, uc *fakeUsecase) *Server {
	t.Helper()
	srv, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: uc, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	return srv
}

func mustMarshal(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal %T: %v", msg, err)
	}
	return b
}

func mustUnmarshal(t *testing.T, data []byte, msg proto.Message) {
	t.Helper()
	if len(data) == 0 {
		t.Fatal("no response body written")
	}
	if err := proto.Unmarshal(data, msg); err != nil {
		t.Fatalf("unmarshal %T: %v", msg, err)
	}
}
