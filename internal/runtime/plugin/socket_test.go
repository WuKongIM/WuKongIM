package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/wkrpc"
	wkrpcproto "github.com/WuKongIM/wkrpc/proto"
	"github.com/stretchr/testify/require"
)

func TestSocketStartCreatesParentAndRemovesStaleSocket(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "run", "plugin.sock")
	require.NoError(t, os.MkdirAll(filepath.Dir(socketPath), 0o755))
	require.NoError(t, os.WriteFile(socketPath, []byte("stale"), 0o600))
	backend := &fakeSocketBackend{}
	server := newSocketServerWithBackend(socketPath, backend)

	require.NoError(t, server.Start())

	require.DirExists(t, filepath.Dir(socketPath))
	_, err := os.Stat(socketPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Equal(t, 1, backend.startCount)
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

type fakeSocketBackend struct {
	routes map[string]wkrpc.Handler

	startCount int
	stopCount  int

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
