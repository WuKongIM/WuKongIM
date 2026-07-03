package plugin

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pluginhost "github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/wkrpc"
	wkrpcproto "github.com/WuKongIM/wkrpc/proto"
	"google.golang.org/protobuf/proto"
)

func TestConstructorRequiresPositiveTimeoutAndDefaultsMaxBody(t *testing.T) {
	_, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: &fakeUsecase{}})
	if err == nil {
		t.Fatal("expected missing timeout error")
	}
	_, err = NewServer(Options{Routes: &fakeRoutes{}, Timeout: time.Second})
	if !errors.Is(err, ErrUsecaseRequired) {
		t.Fatalf("error = %v, want %v", err, ErrUsecaseRequired)
	}

	srv, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: &fakeUsecase{}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	if srv.MaxBodyBytes() != DefaultHostRPCMaxBodyBytes {
		t.Fatalf("MaxBodyBytes = %d, want %d", srv.MaxBodyBytes(), DefaultHostRPCMaxBodyBytes)
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

func TestRouteRegistrarAcceptsRuntimeSocketServer(t *testing.T) {
	var _ RouteRegistrar = pluginhost.NewSocketServer("/tmp/wukongim-plugin-test.sock")
}

func TestRouteRegistrarAcceptsDirectWKRPCServer(t *testing.T) {
	var _ RouteRegistrar = (*wkrpc.Server)(nil)
}

func TestRegisterRoutesSupportsWKRPCHandlerRegistrar(t *testing.T) {
	routes := &fakeHandlerRoutes{}
	_, err := NewServer(Options{Routes: routes, Usecase: &fakeUsecase{}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	if !reflect.DeepEqual(routes.paths, routePaths) {
		t.Fatalf("registered paths = %#v, want %#v", routes.paths, routePaths)
	}
	for _, path := range routePaths {
		if routes.handlers[path] == nil {
			t.Fatalf("route %s registered nil handler", path)
		}
	}
}

func TestRegisteredHandlerDispatchesThroughWKRPCAdapter(t *testing.T) {
	socketPath := shortAccessSocketPath(t)
	routes := pluginhost.NewSocketServer(socketPath)
	uc := &fakeUsecase{startupResp: &pluginproto.StartupResp{NodeId: 9, Success: true}}
	_, err := NewServer(Options{Routes: routes, Usecase: uc, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	if err := routes.Start(); err != nil {
		t.Fatalf("socket start: %v", err)
	}
	t.Cleanup(routes.Stop)

	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatalf("dial plugin socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	wkrpcConnect(t, conn, "registered")

	resp := wkrpcRequest(t, conn, 2, "/plugin/start", mustMarshal(t, &pluginproto.PluginInfo{No: "registered"}))
	if resp.Status != wkrpcproto.StatusOK {
		t.Fatalf("response status = %v body=%q", resp.Status, string(resp.Body))
	}
	var got pluginproto.StartupResp
	mustUnmarshal(t, resp.Body, &got)
	if got.NodeId != 9 || !got.Success {
		t.Fatalf("startup response = %#v", &got)
	}
	uc.waitStartCalls(t, 1)
	if uc.startInfo == nil || uc.startInfo.No != "registered" {
		t.Fatalf("StartPlugin info = %#v", uc.startInfo)
	}
	if uc.startCaller != "registered" {
		t.Fatalf("StartPlugin caller = %q", uc.startCaller)
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

	emptyCallerCtx := newFakeRPCContext(mustMarshal(t, &pluginproto.PluginInfo{No: "plug-a"}))
	srv.handlePath("/plugin/start", emptyCallerCtx)
	if emptyCallerCtx.err == nil {
		t.Fatal("expected WriteErr for empty caller uid")
	}
	if uc.startCalls != 1 {
		t.Fatalf("StartPlugin calls = %d, want 1", uc.startCalls)
	}
}

func TestLifecycleCloseRequestWithEmptyBodyWritesOK(t *testing.T) {
	uc := &fakeUsecase{}
	srv := mustServer(t, uc)
	ctx := newFakeRequestContext(nil)
	ctx.uid = "plug-request-close"

	srv.handlePath("/close", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if !ctx.ok {
		t.Fatal("expected WriteOk for an explicit /close request")
	}
	if uc.closePluginNo != "plug-request-close" {
		t.Fatalf("ClosePlugin pluginNo = %q", uc.closePluginNo)
	}
}

func TestLifecycleCloseEventDoesNotBlockOnUsecase(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	uc := &fakeUsecase{closeStarted: started, closeBlock: block}
	srv := mustServer(t, uc)
	ctx := newFakeCloseEventContext(nil)
	ctx.uid = "plug-close"
	returned := make(chan struct{})

	go func() {
		srv.handlePath("/close", ctx)
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("close handler did not return before blocked ClosePlugin was released")
	}
	if ctx.ok {
		t.Fatal("close event should not write OK to a closing connection")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("ClosePlugin was not started asynchronously")
	}
	close(block)
}

func TestLifecycleCloseEventLogsCleanupError(t *testing.T) {
	logger := newRecordingLogger()
	uc := &fakeUsecase{closeErr: errors.New("cleanup failed")}
	srv, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: uc, Timeout: time.Second, Logger: logger})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	ctx := newFakeCloseEventContext(nil)
	ctx.uid = "plug-close"

	srv.handlePath("/close", ctx)

	logger.waitWarn(t)
	if logger.lastWarnMsg != "plugin close event cleanup failed" {
		t.Fatalf("warn msg = %q", logger.lastWarnMsg)
	}
	if got := logger.warnFieldValue("pluginNo"); got != "plug-close" {
		t.Fatalf("logged pluginNo = %#v", got)
	}
	if got := logger.warnFieldValue("error"); got == nil {
		t.Fatal("expected logged error field")
	}
}

func TestLifecycleCloseCallsUsecaseAndWritesOK(t *testing.T) {
	uc := &fakeUsecase{}
	srv := mustServer(t, uc)
	ctx := newFakeRPCContext(nil)
	ctx.uid = "plug-a"

	srv.handlePath("/close", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if !ctx.ok {
		t.Fatal("expected WriteOk")
	}
	if uc.closePluginNo != "plug-a" || uc.closeCaller != "plug-a" {
		t.Fatalf("ClosePlugin args = (%q, %q)", uc.closePluginNo, uc.closeCaller)
	}

	emptyCtx := newFakeRPCContext(nil)
	srv.handlePath("/close", emptyCtx)
	if emptyCtx.err == nil {
		t.Fatal("expected WriteErr for empty plugin number")
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

func TestCodecRejectsBodyLargerThanMaxOnCloseAndStream(t *testing.T) {
	for _, path := range []string{"/close", "/stream/open", "/stream/write", "/stream/close"} {
		t.Run(path, func(t *testing.T) {
			uc := &fakeUsecase{}
			srv, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: uc, Timeout: time.Second, MaxBodyBytes: 3})
			if err != nil {
				t.Fatalf("NewServer returned error: %v", err)
			}
			ctx := newFakeRPCContext([]byte{1, 2, 3, 4})
			ctx.uid = "plug-a"

			srv.handlePath(path, ctx)

			if ctx.err == nil {
				t.Fatal("expected max body WriteErr")
			}
			if !strings.Contains(ctx.err.Error(), "body exceeds max bytes") {
				t.Fatalf("error = %q", ctx.err.Error())
			}
			if uc.closePluginNo != "" {
				t.Fatalf("ClosePlugin called with %q", uc.closePluginNo)
			}
		})
	}
}

func TestStreamRoutesLogPathAndPluginNumber(t *testing.T) {
	logger := newRecordingLogger()
	srv, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: &fakeUsecase{}, Timeout: time.Second, Logger: logger})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	ctx := newFakeRPCContext(nil)
	ctx.uid = "plug-stream"

	srv.handlePath("/stream/open", ctx)

	if ctx.err == nil {
		t.Fatal("expected stream unimplemented error")
	}
	if logger.lastMsg != "plugin stream rpc unimplemented" {
		t.Fatalf("log msg = %q", logger.lastMsg)
	}
	if got := logger.fieldValue("path"); got != "/stream/open" {
		t.Fatalf("logged path = %#v", got)
	}
	if got := logger.fieldValue("pluginNo"); got != "plug-stream" {
		t.Fatalf("logged pluginNo = %#v", got)
	}
}

func TestCodecRejectsResponseLargerThanMax(t *testing.T) {
	uc := &fakeUsecase{startupResp: &pluginproto.StartupResp{Config: []byte("too large")}}
	srv, err := NewServer(Options{Routes: &fakeRoutes{}, Usecase: uc, Timeout: time.Second, MaxBodyBytes: 3})
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.PluginInfo{No: "plug-a"}))

	srv.handlePath("/plugin/start", ctx)

	if ctx.err == nil {
		t.Fatal("expected max response body WriteErr")
	}
	if len(ctx.written) != 0 {
		t.Fatalf("unexpected response body written: %d bytes", len(ctx.written))
	}
}

func TestTimeoutWrapsAllHostRPCRoutesAndKeepsShorterIncomingDeadline(t *testing.T) {
	for _, tc := range []struct {
		path string
		body []byte
	}{
		{path: "/plugin/start", body: mustMarshal(t, &pluginproto.PluginInfo{No: "plug-timeout"})},
		{path: "/close", body: nil},
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
			ctx.uid = "plug-timeout"
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
			incoming, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			ctx := newFakeRPCContext(tc.body)
			ctx.uid = "plug-timeout"
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
			if remaining <= 0 || remaining > 250*time.Millisecond {
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

type fakeHandlerRoutes struct {
	paths    []string
	handlers map[string]wkrpc.Handler
}

func (f *fakeHandlerRoutes) Route(path string, handler wkrpc.Handler) {
	if f.handlers == nil {
		f.handlers = make(map[string]wkrpc.Handler)
	}
	f.paths = append(f.paths, path)
	f.handlers[path] = handler
}

func shortAccessSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wkp-access-")
	if err != nil {
		t.Fatalf("create temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "plugin.sock")
}

func wkrpcConnect(t *testing.T, conn net.Conn, uid string) {
	t.Helper()
	req := &wkrpcproto.Connect{Id: 1, Uid: uid}
	payload, err := req.Marshal()
	if err != nil {
		t.Fatalf("marshal connect: %v", err)
	}
	writeWKRPCFrame(t, conn, wkrpcproto.MsgTypeConnect, payload)
	msgType, data := readWKRPCFrame(t, conn)
	if msgType != wkrpcproto.MsgTypeConnack {
		t.Fatalf("connect response type = %v", msgType)
	}
	var ack wkrpcproto.Connack
	if err := ack.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal connack: %v", err)
	}
	if ack.Status != wkrpcproto.StatusOK {
		t.Fatalf("connack status = %v body=%q", ack.Status, string(ack.Body))
	}
}

func wkrpcRequest(t *testing.T, conn net.Conn, id uint64, path string, body []byte) *wkrpcproto.Response {
	t.Helper()
	req := &wkrpcproto.Request{Id: id, Path: path, Body: body}
	payload, err := req.Marshal()
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	writeWKRPCFrame(t, conn, wkrpcproto.MsgTypeRequest, payload)
	msgType, data := readWKRPCFrame(t, conn)
	if msgType != wkrpcproto.MsgTypeResp {
		t.Fatalf("request response type = %v", msgType)
	}
	var resp wkrpcproto.Response
	if err := resp.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return &resp
}

func writeWKRPCFrame(t *testing.T, conn net.Conn, msgType wkrpcproto.MsgType, payload []byte) {
	t.Helper()
	frame := make([]byte, len(wkrpcproto.MagicNumberStart)+1+4+len(payload))
	copy(frame, wkrpcproto.MagicNumberStart)
	frame[len(wkrpcproto.MagicNumberStart)] = msgType.Uint8()
	binary.BigEndian.PutUint32(frame[len(wkrpcproto.MagicNumberStart)+1:], uint32(len(payload)))
	copy(frame[len(wkrpcproto.MagicNumberStart)+1+4:], payload)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write wkrpc frame: %v", err)
	}
}

func readWKRPCFrame(t *testing.T, conn net.Conn) (wkrpcproto.MsgType, []byte) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	header := make([]byte, len(wkrpcproto.MagicNumberStart)+1+4)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read wkrpc header: %v", err)
	}
	if string(header[:len(wkrpcproto.MagicNumberStart)]) != string(wkrpcproto.MagicNumberStart) {
		t.Fatalf("invalid wkrpc magic: %q", header[:len(wkrpcproto.MagicNumberStart)])
	}
	msgType := wkrpcproto.MsgType(header[len(wkrpcproto.MagicNumberStart)])
	length := binary.BigEndian.Uint32(header[len(wkrpcproto.MagicNumberStart)+1:])
	body := make([]byte, length)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("read wkrpc body: %v", err)
	}
	return msgType, body
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

type fakeCloseEventContext struct {
	*fakeRPCContext
}

func newFakeCloseEventContext(body []byte) *fakeCloseEventContext {
	return &fakeCloseEventContext{fakeRPCContext: newFakeRPCContext(body)}
}

func (f *fakeCloseEventContext) CloseEvent() bool { return true }

type fakeRequestContext struct {
	*fakeRPCContext
}

func newFakeRequestContext(body []byte) *fakeRequestContext {
	return &fakeRequestContext{fakeRPCContext: newFakeRPCContext(body)}
}

func (f *fakeRequestContext) CloseEvent() bool { return false }

func newRecordingLogger() *recordingLogger {
	return &recordingLogger{warned: make(chan struct{})}
}

type recordingLogger struct {
	lastMsg       string
	lastFields    []wklog.Field
	lastWarnMsg   string
	lastWarnField []wklog.Field
	warned        chan struct{}
	warnOnce      sync.Once
}

func (r *recordingLogger) Debug(msg string, fields ...wklog.Field) {
	r.lastMsg = msg
	r.lastFields = append([]wklog.Field(nil), fields...)
}
func (r *recordingLogger) Info(msg string, fields ...wklog.Field) {}
func (r *recordingLogger) Warn(msg string, fields ...wklog.Field) {
	r.lastWarnMsg = msg
	r.lastWarnField = append([]wklog.Field(nil), fields...)
	if r.warned != nil {
		r.warnOnce.Do(func() { close(r.warned) })
	}
}
func (r *recordingLogger) Error(msg string, fields ...wklog.Field) {}
func (r *recordingLogger) Fatal(msg string, fields ...wklog.Field) {}
func (r *recordingLogger) Named(string) wklog.Logger               { return r }
func (r *recordingLogger) With(fields ...wklog.Field) wklog.Logger { return r }
func (r *recordingLogger) Sync() error                             { return nil }

func (r *recordingLogger) fieldValue(key string) any {
	return fieldValue(r.lastFields, key)
}

func (r *recordingLogger) warnFieldValue(key string) any {
	return fieldValue(r.lastWarnField, key)
}

func (r *recordingLogger) waitWarn(t *testing.T) {
	t.Helper()
	select {
	case <-r.warned:
	case <-time.After(time.Second):
		t.Fatal("logger did not record warning")
	}
}

func fieldValue(fields []wklog.Field, key string) any {
	for _, field := range fields {
		if field.Key == key {
			return field.Value
		}
	}
	return nil
}

type fakeUsecase struct {
	mu sync.Mutex

	lastCtx context.Context

	startupResp *pluginproto.StartupResp
	startCalls  int
	startInfo   *pluginproto.PluginInfo
	startCaller string

	closePluginNo string
	closeCaller   string
	closeStarted  chan struct{}
	closeBlock    <-chan struct{}
	closeErr      error

	sendResp   *pluginproto.SendResp
	sendCalls  int
	sendCtx    context.Context
	sendReq    *pluginproto.SendReq
	sendCaller string

	channelMessagesResp   *pluginproto.ChannelMessageBatchResp
	channelMessagesCalls  int
	channelMessagesCtx    context.Context
	channelMessagesReq    *pluginproto.ChannelMessageBatchReq
	channelMessagesCaller string

	clusterConfigResp   *pluginproto.ClusterConfig
	clusterConfigCalls  int
	clusterConfigCtx    context.Context
	clusterConfigCaller string

	clusterBelongNodeResp   *pluginproto.ClusterChannelBelongNodeBatchResp
	clusterBelongNodeCalls  int
	clusterBelongNodeCtx    context.Context
	clusterBelongNodeReq    *pluginproto.ClusterChannelBelongNodeReq
	clusterBelongNodeCaller string

	conversationChannelsResp   *pluginproto.ConversationChannelResp
	conversationChannelsCalls  int
	conversationChannelsCtx    context.Context
	conversationChannelsReq    *pluginproto.ConversationChannelReq
	conversationChannelsCaller string

	httpForwardResp   *pluginproto.HttpResponse
	httpForwardCalls  int
	httpForwardCtx    context.Context
	httpForwardReq    *pluginproto.ForwardHttpReq
	httpForwardCaller string
}

func (f *fakeUsecase) StartPlugin(ctx context.Context, info *pluginproto.PluginInfo, callerUID string) (*pluginproto.StartupResp, error) {
	f.mu.Lock()
	f.lastCtx = ctx
	f.startCalls++
	f.startInfo = info
	f.startCaller = callerUID
	resp := f.startupResp
	f.mu.Unlock()
	if resp != nil {
		return resp, nil
	}
	return &pluginproto.StartupResp{Success: true}, nil
}

func (f *fakeUsecase) waitStartCalls(t *testing.T, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		f.mu.Lock()
		got := f.startCalls
		f.mu.Unlock()
		if got >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("StartPlugin calls = %d, want at least %d", got, want)
		case <-tick.C:
		}
	}
}

func (f *fakeUsecase) ClosePlugin(ctx context.Context, pluginNo string, callerUID string) error {
	f.mu.Lock()
	f.lastCtx = ctx
	f.closePluginNo = pluginNo
	f.closeCaller = callerUID
	started := f.closeStarted
	block := f.closeBlock
	closeErr := f.closeErr
	f.mu.Unlock()
	if started != nil {
		close(started)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return closeErr
}

func (f *fakeUsecase) SendMessage(ctx context.Context, req *pluginproto.SendReq, callerUID string) (*pluginproto.SendResp, error) {
	f.mu.Lock()
	f.lastCtx = ctx
	f.sendCalls++
	f.sendCtx = ctx
	f.sendReq = req
	f.sendCaller = callerUID
	resp := f.sendResp
	f.mu.Unlock()
	if resp != nil {
		return resp, nil
	}
	return &pluginproto.SendResp{MessageId: 123}, nil
}

func (f *fakeUsecase) ChannelMessages(ctx context.Context, req *pluginproto.ChannelMessageBatchReq, callerUID string) (*pluginproto.ChannelMessageBatchResp, error) {
	f.mu.Lock()
	f.lastCtx = ctx
	f.channelMessagesCalls++
	f.channelMessagesCtx = ctx
	f.channelMessagesReq = req
	f.channelMessagesCaller = callerUID
	resp := f.channelMessagesResp
	f.mu.Unlock()
	if resp != nil {
		return resp, nil
	}
	return &pluginproto.ChannelMessageBatchResp{}, nil
}

func (f *fakeUsecase) HTTPForward(ctx context.Context, req *pluginproto.ForwardHttpReq, callerUID string) (*pluginproto.HttpResponse, error) {
	f.mu.Lock()
	f.lastCtx = ctx
	f.httpForwardCalls++
	f.httpForwardCtx = ctx
	f.httpForwardReq = req
	f.httpForwardCaller = callerUID
	resp := f.httpForwardResp
	f.mu.Unlock()
	if resp != nil {
		return resp, nil
	}
	return &pluginproto.HttpResponse{}, nil
}

func (f *fakeUsecase) ClusterConfig(ctx context.Context, callerUID string) (*pluginproto.ClusterConfig, error) {
	f.mu.Lock()
	f.lastCtx = ctx
	f.clusterConfigCalls++
	f.clusterConfigCtx = ctx
	f.clusterConfigCaller = callerUID
	resp := f.clusterConfigResp
	f.mu.Unlock()
	if resp != nil {
		return resp, nil
	}
	return &pluginproto.ClusterConfig{}, nil
}

func (f *fakeUsecase) ClusterChannelsBelongNode(ctx context.Context, req *pluginproto.ClusterChannelBelongNodeReq, callerUID string) (*pluginproto.ClusterChannelBelongNodeBatchResp, error) {
	f.mu.Lock()
	f.lastCtx = ctx
	f.clusterBelongNodeCalls++
	f.clusterBelongNodeCtx = ctx
	f.clusterBelongNodeReq = req
	f.clusterBelongNodeCaller = callerUID
	resp := f.clusterBelongNodeResp
	f.mu.Unlock()
	if resp != nil {
		return resp, nil
	}
	return &pluginproto.ClusterChannelBelongNodeBatchResp{}, nil
}

func (f *fakeUsecase) ConversationChannels(ctx context.Context, req *pluginproto.ConversationChannelReq, callerUID string) (*pluginproto.ConversationChannelResp, error) {
	f.mu.Lock()
	f.lastCtx = ctx
	f.conversationChannelsCalls++
	f.conversationChannelsCtx = ctx
	f.conversationChannelsReq = req
	f.conversationChannelsCaller = callerUID
	resp := f.conversationChannelsResp
	f.mu.Unlock()
	if resp != nil {
		return resp, nil
	}
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
