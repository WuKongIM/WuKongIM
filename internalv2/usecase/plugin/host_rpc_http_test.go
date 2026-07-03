package plugin

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/stretchr/testify/require"
)

func TestHTTPForwardLocalRoutesThroughPluginAndDropsHopByHopHeaders(t *testing.T) {
	invoker := &recordingHTTPRouteInvoker{response: &pluginproto.HttpResponse{Status: http.StatusCreated, Headers: map[string]string{"X-Plugin": "ok"}, Body: []byte("created")}}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: invoker})
	require.NoError(t, err)

	resp, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		PluginNo: "wk.plugin.echo",
		Request: &pluginproto.HttpRequest{
			Method: http.MethodPost,
			Path:   "/hello",
			Headers: map[string]string{
				"Connection":        "X-Drop-Me",
				"X-Drop-Me":         "drop",
				"Transfer-Encoding": "chunked",
				"X-Keep":            "keep",
			},
			Query: map[string]string{"name": "alice"},
			Body:  []byte("payload"),
		},
	}, "caller")

	require.NoError(t, err)
	require.Equal(t, int32(http.StatusCreated), resp.GetStatus())
	require.Equal(t, []byte("created"), resp.GetBody())
	require.Equal(t, "wk.plugin.echo", invoker.pluginNo)
	require.Equal(t, PathRoute, invoker.path)
	require.Equal(t, http.MethodPost, invoker.request.GetMethod())
	require.Equal(t, "/hello", invoker.request.GetPath())
	require.Equal(t, "alice", invoker.request.GetQuery()["name"])
	require.Equal(t, []byte("payload"), invoker.request.GetBody())
	require.Equal(t, "keep", invoker.request.GetHeaders()["X-Keep"])
	for _, key := range []string{"Connection", "X-Drop-Me", "Transfer-Encoding"} {
		require.NotContains(t, invoker.request.GetHeaders(), key)
	}
}

func TestHTTPForwardConnectionTokensAreCaseInsensitive(t *testing.T) {
	invoker := &recordingHTTPRouteInvoker{response: &pluginproto.HttpResponse{Status: http.StatusOK}}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: invoker})
	require.NoError(t, err)

	_, err = app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		PluginNo: "wk.plugin.echo",
		Request: &pluginproto.HttpRequest{Headers: map[string]string{
			"Connection": "X-Drop-Me",
			"x-drop-me":  "drop",
			"X-Keep":     "keep",
		}},
	}, "caller")

	require.NoError(t, err)
	require.NotContains(t, invoker.request.GetHeaders(), "x-drop-me")
	require.Equal(t, "keep", invoker.request.GetHeaders()["X-Keep"])
}

func TestHTTPForwardFillsMissingPluginNoFromCaller(t *testing.T) {
	invoker := &recordingHTTPRouteInvoker{response: &pluginproto.HttpResponse{Status: http.StatusOK}}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: invoker})
	require.NoError(t, err)

	_, err = app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		Request: &pluginproto.HttpRequest{Path: "/self"},
	}, "wk.plugin.self")

	require.NoError(t, err)
	require.Equal(t, "wk.plugin.self", invoker.pluginNo)
}

func TestHTTPForwardPositiveNodeUsesNodeForwarder(t *testing.T) {
	forwarder := &recordingHTTPForwarder{resp: &pluginproto.HttpResponse{Status: http.StatusNoContent}}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingHTTPRouteInvoker{}, HTTPForwarder: forwarder})
	require.NoError(t, err)

	resp, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		PluginNo: "wk.plugin.echo",
		ToNodeId: 3,
		Request: &pluginproto.HttpRequest{Path: "/remote", Headers: map[string]string{
			"Keep-Alive": "timeout=5",
			"X-Keep":     "keep",
		}},
	}, "caller")

	require.NoError(t, err)
	require.Equal(t, int32(http.StatusNoContent), resp.GetStatus())
	require.Equal(t, 1, forwarder.calls)
	require.Equal(t, uint64(3), forwarder.nodeID)
	require.Equal(t, "wk.plugin.echo", forwarder.req.GetPluginNo())
	require.Equal(t, int64(3), forwarder.req.GetToNodeId())
	require.NotContains(t, forwarder.req.GetRequest().GetHeaders(), "Keep-Alive")
	require.Equal(t, "keep", forwarder.req.GetRequest().GetHeaders()["X-Keep"])
}

func TestHTTPForwardFanoutIsExplicitlyDeferred(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingHTTPRouteInvoker{}})
	require.NoError(t, err)

	_, err = app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo", ToNodeId: -1}, "caller")

	require.ErrorIs(t, err, ErrHTTPForwardFanoutDeferred)
}

func TestHTTPForwardEnforcesBodyLimitBeforeLocalRoute(t *testing.T) {
	invoker := &recordingHTTPRouteInvoker{response: &pluginproto.HttpResponse{Status: http.StatusOK}}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: invoker, HTTPForwardMaxBodyBytes: 4})
	require.NoError(t, err)

	_, err = app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		PluginNo: "wk.plugin.echo",
		Request:  &pluginproto.HttpRequest{Body: []byte("12345")},
	}, "caller")

	require.ErrorIs(t, err, ErrHTTPForwardBodyTooLarge)
	require.Empty(t, invoker.path)
}

func TestHTTPForwardEnforcesResponseBodyLimit(t *testing.T) {
	invoker := &recordingHTTPRouteInvoker{response: &pluginproto.HttpResponse{Status: http.StatusOK, Body: []byte("12345")}}
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: invoker, HTTPForwardMaxBodyBytes: 4})
	require.NoError(t, err)

	_, err = app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo"}, "caller")

	require.ErrorIs(t, err, ErrHTTPForwardBodyTooLarge)
}

func TestHTTPForwardRejectsOversizedHeadersAndQuery(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingHTTPRouteInvoker{}})
	require.NoError(t, err)

	_, err = app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		PluginNo: "wk.plugin.echo",
		Request: &pluginproto.HttpRequest{
			Headers: map[string]string{"X-Large": string(make([]byte, defaultHTTPForwardMaxHeaderBytes))},
			Query:   map[string]string{"q": "v"},
		},
	}, "caller")

	require.ErrorIs(t, err, ErrHTTPForwardHeaderTooLarge)
}

func TestHTTPForwardRequiresForwarderForRemoteNode(t *testing.T) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingHTTPRouteInvoker{}})
	require.NoError(t, err)

	_, err = app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo", ToNodeId: 9}, "caller")

	require.ErrorIs(t, err, ErrHTTPForwarderRequired)
}

func TestHTTPForwardPropagatesForwarderError(t *testing.T) {
	wantErr := errors.New("remote down")
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingHTTPRouteInvoker{}, HTTPForwarder: &recordingHTTPForwarder{err: wantErr}})
	require.NoError(t, err)

	_, err = app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo", ToNodeId: 2}, "caller")

	require.ErrorIs(t, err, wantErr)
}

type recordingHTTPRouteInvoker struct {
	pluginNo string
	path     string
	request  pluginproto.HttpRequest
	response *pluginproto.HttpResponse
	err      error
}

func (r *recordingHTTPRouteInvoker) RequestPlugin(_ context.Context, no, path string, body []byte) ([]byte, error) {
	r.pluginNo = no
	r.path = path
	if err := r.request.Unmarshal(body); err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	resp := r.response
	if resp == nil {
		resp = &pluginproto.HttpResponse{Status: http.StatusOK}
	}
	return resp.Marshal()
}

func (r *recordingHTTPRouteInvoker) SendPlugin(string, uint32, []byte) error {
	return nil
}

type recordingHTTPForwarder struct {
	calls  int
	nodeID uint64
	req    *pluginproto.ForwardHttpReq
	resp   *pluginproto.HttpResponse
	err    error
}

func (r *recordingHTTPForwarder) ForwardPluginHTTP(_ context.Context, nodeID uint64, req *pluginproto.ForwardHttpReq) (*pluginproto.HttpResponse, error) {
	r.calls++
	r.nodeID = nodeID
	r.req = req
	if r.err != nil {
		return nil, r.err
	}
	if r.resp != nil {
		return r.resp, nil
	}
	return &pluginproto.HttpResponse{Status: http.StatusOK}, nil
}
