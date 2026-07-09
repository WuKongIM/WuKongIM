package pluginhost

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/wkrpc"
	wkrpcproto "github.com/WuKongIM/wkrpc/proto"
	"github.com/stretchr/testify/require"
)

func TestSocketStartRejectsNonSocketAtPath(t *testing.T) {
	dir := shortSocketTempDir(t)
	socketPath := filepath.Join(dir, "plugin.sock")
	require.NoError(t, os.MkdirAll(filepath.Dir(socketPath), 0o755))
	require.NoError(t, os.WriteFile(socketPath, []byte("not a socket"), 0o600))
	backend := &fakeSocketBackend{}
	server := newSocketServerWithBackend(socketPath, backend)

	err := server.Start()

	require.Error(t, err)
	require.FileExists(t, socketPath)
	require.Equal(t, 0, backend.startCount)
}

func TestSocketStartRejectsTooLongPathBeforeBackendStart(t *testing.T) {
	dir := shortSocketTempDir(t)
	socketPath := filepath.Join(dir, strings.Repeat("x", 180), "plugin.sock")
	backend := &fakeSocketBackend{}
	server := newSocketServerWithBackend(socketPath, backend)
	server.readyCheck = func(string, time.Duration) error { return nil }

	err := server.Start()

	require.Error(t, err)
	require.Contains(t, err.Error(), "plugin socket path")
	require.Equal(t, 0, backend.startCount)
}

func TestSocketStartRemovesStaleSocket(t *testing.T) {
	dir := shortSocketTempDir(t)
	socketPath := filepath.Join(dir, "plugin.sock")
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	backend := &fakeSocketBackend{}
	server := newSocketServerWithBackend(socketPath, backend)
	server.readyCheck = func(path string, timeout time.Duration) error {
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
		return nil
	}

	require.NoError(t, server.Start())

	require.Equal(t, 1, backend.startCount)
}

func TestSocketStartWaitsForReadiness(t *testing.T) {
	dir := shortSocketTempDir(t)
	socketPath := filepath.Join(dir, "plugin.sock")
	backend := &fakeSocketBackend{}
	server := newSocketServerWithBackend(socketPath, backend)
	server.readyTimeout = time.Second
	ready := make(chan struct{})
	var listener net.Listener
	t.Cleanup(func() {
		if listener != nil {
			_ = listener.Close()
		}
	})
	backend.onStart = func() {
		go func() {
			time.Sleep(25 * time.Millisecond)
			var err error
			listener, err = net.Listen("unix", socketPath)
			require.NoError(t, err)
			close(ready)
		}()
	}

	require.NoError(t, server.Start())

	select {
	case <-ready:
	default:
		t.Fatal("Start returned before socket path was ready")
	}
}

func TestSocketRouteDelegatesToBackend(t *testing.T) {
	backend := &fakeSocketBackend{}
	server := newSocketServerWithBackend(filepath.Join(t.TempDir(), "plugin.sock"), backend)
	handler := func(*wkrpc.Context) {}

	server.Route("/plugin/start", handler)

	require.Contains(t, backend.routes, "/plugin/start")
}

func TestSocketStartStopAreIdempotent(t *testing.T) {
	backend := &fakeSocketBackend{}
	server := newSocketServerWithBackend(filepath.Join(t.TempDir(), "plugin.sock"), backend)
	server.readyCheck = func(string, time.Duration) error { return nil }

	require.NoError(t, server.Start())
	require.NoError(t, server.Start())
	server.Stop()
	server.Stop()

	require.Equal(t, 1, backend.startCount)
	require.Equal(t, 1, backend.stopCount)
}

func TestSocketRequestClonesResponseBody(t *testing.T) {
	response := &wkrpcproto.Response{Body: []byte("reply")}
	server := newSocketServerWithBackend(filepath.Join(t.TempDir(), "plugin.sock"), &fakeSocketBackend{response: response})

	got, err := server.Request(context.Background(), "plugin-a", "/hook", nil)
	require.NoError(t, err)
	got[0] = 'X'

	require.Equal(t, []byte("reply"), response.Body)
}

func TestSocketRequestAndSendExposeByteInvokerSurface(t *testing.T) {
	backend := &fakeSocketBackend{response: &wkrpcproto.Response{Body: []byte("reply")}}
	server := newSocketServerWithBackend(filepath.Join(t.TempDir(), "plugin.sock"), backend)

	got, err := server.Request(context.Background(), "plugin-a", "/hook", []byte("body"))
	require.NoError(t, err)
	require.Equal(t, []byte("reply"), got)
	require.Equal(t, "plugin-a", backend.requestUID)
	require.Equal(t, "/hook", backend.requestPath)
	require.Equal(t, []byte("body"), backend.requestBody)

	require.NoError(t, server.Send("plugin-a", 7, []byte("message")))
	require.Equal(t, "plugin-a", backend.sendUID)
	require.Equal(t, uint32(7), backend.sentMsg.MsgType)
	require.Equal(t, []byte("message"), backend.sentMsg.Content)
}

func shortSocketTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wkp-sock-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type fakeSocketBackend struct {
	routes map[string]wkrpc.Handler

	startCount int
	stopCount  int
	onStart    func()

	requestUID  string
	requestPath string
	requestBody []byte
	response    *wkrpcproto.Response
	requestErr  error

	sendUID string
	sentMsg *wkrpcproto.Message
	sendErr error
}

func (f *fakeSocketBackend) Route(path string, handler wkrpc.Handler) {
	if f.routes == nil {
		f.routes = make(map[string]wkrpc.Handler)
	}
	f.routes[path] = handler
}

func (f *fakeSocketBackend) Start() error {
	f.startCount++
	if f.onStart != nil {
		f.onStart()
	}
	return nil
}

func (f *fakeSocketBackend) Stop() {
	f.stopCount++
}

func (f *fakeSocketBackend) RequestWithContext(ctx context.Context, uid, path string, body []byte) (*wkrpcproto.Response, error) {
	f.requestUID = uid
	f.requestPath = path
	f.requestBody = append([]byte(nil), body...)
	if f.requestErr != nil {
		return nil, f.requestErr
	}
	return f.response, nil
}

func (f *fakeSocketBackend) Send(uid string, msg *wkrpcproto.Message) error {
	f.sendUID = uid
	copy := *msg
	copy.Content = append([]byte(nil), msg.Content...)
	f.sentMsg = &copy
	return f.sendErr
}
