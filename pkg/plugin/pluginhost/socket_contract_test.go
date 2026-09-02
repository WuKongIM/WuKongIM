package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	wkrpcproto "github.com/WuKongIM/wkrpc/proto"
	"github.com/stretchr/testify/require"
)

func TestSocketStartRollsBackBackendAfterReadinessFailure(t *testing.T) {
	backend := &fakeSocketBackend{}
	server := newSocketServerWithBackend(filepath.Join(shortSocketTempDir(t), "plugin.sock"), backend)
	expected := errors.New("connack rejected")
	checks := 0
	server.readyCheck = func(string, time.Duration) error {
		checks++
		if checks == 1 {
			return expected
		}
		return nil
	}

	err := server.Start()
	require.ErrorIs(t, err, expected)
	require.Equal(t, 1, backend.startCount)
	require.Equal(t, 1, backend.stopCount)
	require.False(t, server.started)

	require.NoError(t, server.Start())
	require.Equal(t, 2, backend.startCount)
	require.True(t, server.started)
	server.Stop()
}

func TestSocketStartReportsBackendFailureWithoutReadinessProbe(t *testing.T) {
	expected := errors.New("bind failed")
	base := &fakeSocketBackend{}
	backend := &startErrorSocketBackend{fakeSocketBackend: base, err: expected}
	server := newSocketServerWithBackend(filepath.Join(shortSocketTempDir(t), "plugin.sock"), backend)
	readyCalled := false
	server.readyCheck = func(string, time.Duration) error {
		readyCalled = true
		return nil
	}

	err := server.Start()

	require.ErrorIs(t, err, expected)
	require.False(t, readyCalled)
	require.Equal(t, 1, base.startCount)
	require.Equal(t, 0, base.stopCount)
}

func TestSocketStartReportsParentDirectoryFailure(t *testing.T) {
	dir := shortSocketTempDir(t)
	blockingPath := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(blockingPath, []byte("x"), 0o600))
	backend := &fakeSocketBackend{}
	server := newSocketServerWithBackend(filepath.Join(blockingPath, "plugin.sock"), backend)

	err := server.Start()

	require.ErrorContains(t, err, "create plugin socket dir")
	require.Zero(t, backend.startCount)
}

func TestSocketRequestRejectsTransportNilAndFailureResponses(t *testing.T) {
	expected := errors.New("transport canceled")
	tests := []struct {
		name    string
		backend *fakeSocketBackend
		wantErr string
		isErr   error
	}{
		{name: "transport", backend: &fakeSocketBackend{requestErr: expected}, wantErr: expected.Error(), isErr: expected},
		{name: "nil response", backend: &fakeSocketBackend{}, wantErr: "nil response"},
		{name: "plugin status", backend: &fakeSocketBackend{response: &wkrpcproto.Response{Status: wkrpcproto.StatusError}}, wantErr: "status 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newSocketServerWithBackend(filepath.Join(t.TempDir(), "plugin.sock"), tt.backend)

			got, err := server.RequestWithContext(context.Background(), "alpha", "/hook", []byte("request"))

			require.Nil(t, got)
			require.ErrorContains(t, err, tt.wantErr)
			if tt.isErr != nil {
				require.ErrorIs(t, err, tt.isErr)
			}
		})
	}
}

func TestSocketSendClonesCallerBodyAndPropagatesFailure(t *testing.T) {
	expected := errors.New("connection closed")
	backend := &retainingMessageSocketBackend{err: expected}
	server := newSocketServerWithBackend(filepath.Join(t.TempDir(), "plugin.sock"), backend)
	body := []byte("message")

	err := server.Send("alpha", 42, body)
	body[0] = 'X'

	require.ErrorIs(t, err, expected)
	require.Equal(t, "alpha", backend.uid)
	require.Equal(t, uint32(42), backend.msg.MsgType)
	require.Equal(t, []byte("message"), backend.msg.Content)
	require.NotZero(t, backend.msg.Timestamp)
}

type startErrorSocketBackend struct {
	*fakeSocketBackend
	err error
}

func (b *startErrorSocketBackend) Start() error {
	b.startCount++
	return b.err
}

type retainingMessageSocketBackend struct {
	fakeSocketBackend
	uid string
	msg *wkrpcproto.Message
	err error
}

func (b *retainingMessageSocketBackend) Send(uid string, msg *wkrpcproto.Message) error {
	b.uid = uid
	b.msg = msg
	return b.err
}
