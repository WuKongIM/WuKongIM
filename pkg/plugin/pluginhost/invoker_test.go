package pluginhost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInvokerRequestPluginPreservesPathAndBody(t *testing.T) {
	client := &fakeRPCClient{requestResponse: []byte("reply")}
	invoker := NewInvoker(client)

	got, err := invoker.RequestPlugin(context.Background(), "plugin-1", "/hook", []byte("payload"))

	require.NoError(t, err)
	require.Equal(t, []byte("reply"), got)
	require.Equal(t, "plugin-1", client.requestUID)
	require.Equal(t, "/hook", client.requestPath)
	require.Equal(t, []byte("payload"), client.requestBody)
}

func TestInvokerSendPluginPreservesMessageTypeAndBody(t *testing.T) {
	client := &fakeRPCClient{}
	invoker := NewInvoker(client)

	require.NoError(t, invoker.SendPlugin("plugin-1", 42, []byte("payload")))

	require.Equal(t, "plugin-1", client.sendUID)
	require.Equal(t, uint32(42), client.sendMsgType)
	require.Equal(t, []byte("payload"), client.sendBody)
}

func TestInvokerStopUsesStopPath(t *testing.T) {
	client := &fakeRPCClient{}
	invoker := NewInvoker(client)

	require.NoError(t, invoker.Stop(context.Background(), "plugin-1"))

	require.Equal(t, "plugin-1", client.requestUID)
	require.Equal(t, "/stop", client.requestPath)
	require.Empty(t, client.requestBody)
}

func TestInvokerAppliesTimeoutWhenCallerHasNoDeadline(t *testing.T) {
	client := &fakeRPCClient{}
	invoker := NewInvoker(client, WithTimeout(5*time.Second))

	_, err := invoker.RequestPlugin(context.Background(), "plugin-1", "/hook", nil)

	require.NoError(t, err)
	require.True(t, client.requestHadDeadline)
	require.WithinDuration(t, time.Now().Add(5*time.Second), client.requestDeadline, 500*time.Millisecond)
}

func TestInvokerPreservesShorterCallerDeadline(t *testing.T) {
	client := &fakeRPCClient{}
	invoker := NewInvoker(client, WithTimeout(5*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := invoker.RequestPlugin(ctx, "plugin-1", "/hook", nil)

	require.NoError(t, err)
	require.True(t, client.requestHadDeadline)
	require.Less(t, time.Until(client.requestDeadline), time.Second)
}

func TestInvokerPropagatesClientErrors(t *testing.T) {
	expected := errors.New("transport failed")
	client := &fakeRPCClient{requestErr: expected, sendErr: expected}
	invoker := NewInvoker(client)

	_, err := invoker.RequestPlugin(context.Background(), "plugin-1", "/hook", nil)
	require.ErrorIs(t, err, expected)
	require.ErrorIs(t, invoker.SendPlugin("plugin-1", 7, nil), expected)
	require.ErrorIs(t, invoker.Stop(context.Background(), "plugin-1"), expected)
}

type fakeRPCClient struct {
	requestUID         string
	requestPath        string
	requestBody        []byte
	requestResponse    []byte
	requestErr         error
	requestHadDeadline bool
	requestDeadline    time.Time

	sendUID     string
	sendMsgType uint32
	sendBody    []byte
	sendErr     error
}

func (f *fakeRPCClient) Request(ctx context.Context, uid, path string, body []byte) ([]byte, error) {
	f.requestUID = uid
	f.requestPath = path
	f.requestBody = append([]byte(nil), body...)
	f.requestDeadline, f.requestHadDeadline = ctx.Deadline()
	if f.requestErr != nil {
		return nil, f.requestErr
	}
	return append([]byte(nil), f.requestResponse...), nil
}

func (f *fakeRPCClient) Send(uid string, msgType uint32, body []byte) error {
	f.sendUID = uid
	f.sendMsgType = msgType
	f.sendBody = append([]byte(nil), body...)
	if f.sendErr != nil {
		return f.sendErr
	}
	return nil
}
