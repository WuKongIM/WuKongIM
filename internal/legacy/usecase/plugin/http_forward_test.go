package plugin

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

func TestHTTPForwardLocalRoutesThroughPluginAndDropsHopByHopHeaders(t *testing.T) {
	invoker := &fakeInvokerWithResponses{responses: map[string][]byte{PathRoute: mustMarshalProto(t, &pluginproto.HttpResponse{Status: http.StatusCreated, Body: []byte("created")})}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker})

	resp, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		PluginNo: "wk.plugin.echo",
		Request: &pluginproto.HttpRequest{
			Method: http.MethodGet,
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

	if err != nil {
		t.Fatalf("HTTPForward returned error: %v", err)
	}
	if resp.GetStatus() != http.StatusCreated || string(resp.GetBody()) != "created" {
		t.Fatalf("response = %#v", resp)
	}
	got, ok := invoker.lastRequest()
	if !ok {
		t.Fatal("plugin route was not invoked")
	}
	if got.No != "wk.plugin.echo" || got.Path != PathRoute {
		t.Fatalf("route invocation = %#v", got)
	}
	var routed pluginproto.HttpRequest
	if err := routed.Unmarshal(got.Body); err != nil {
		t.Fatalf("unmarshal routed request: %v", err)
	}
	if routed.GetHeaders()["X-Keep"] != "keep" {
		t.Fatalf("kept header missing: %#v", routed.GetHeaders())
	}
	for _, key := range []string{"connection", "x-drop-me", "Transfer-Encoding"} {
		if _, ok := routed.GetHeaders()[key]; ok {
			t.Fatalf("hop-by-hop header %q was forwarded: %#v", key, routed.GetHeaders())
		}
	}
}

func TestHTTPForwardConnectionTokensAreCaseInsensitive(t *testing.T) {
	invoker := &fakeInvokerWithResponses{responses: map[string][]byte{PathRoute: mustMarshalProto(t, &pluginproto.HttpResponse{Status: http.StatusOK})}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker})

	_, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		PluginNo: "wk.plugin.echo",
		Request: &pluginproto.HttpRequest{Headers: map[string]string{
			"Connection": "X-Drop-Me",
			"x-drop-me":  "drop",
			"X-Keep":     "keep",
		}},
	}, "caller")

	if err != nil {
		t.Fatalf("HTTPForward returned error: %v", err)
	}
	got, ok := invoker.lastRequest()
	if !ok {
		t.Fatal("plugin route was not invoked")
	}
	var routed pluginproto.HttpRequest
	if err := routed.Unmarshal(got.Body); err != nil {
		t.Fatalf("unmarshal routed request: %v", err)
	}
	if _, ok := routed.GetHeaders()["x-drop-me"]; ok {
		t.Fatalf("connection token header was forwarded: %#v", routed.GetHeaders())
	}
}

func TestHTTPForwardFillsMissingPluginNoFromCaller(t *testing.T) {
	invoker := &fakeInvokerWithResponses{responses: map[string][]byte{PathRoute: mustMarshalProto(t, &pluginproto.HttpResponse{Status: http.StatusOK})}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker})

	_, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{Request: &pluginproto.HttpRequest{Path: "/self"}}, "wk.plugin.self")

	if err != nil {
		t.Fatalf("HTTPForward returned error: %v", err)
	}
	got, ok := invoker.lastRequest()
	if !ok || got.No != "wk.plugin.self" {
		t.Fatalf("route invocation = %#v ok=%v", got, ok)
	}
}

func TestHTTPForwardPositiveNodeUsesNodeForwarder(t *testing.T) {
	forwarder := &recordingHTTPForwarder{resp: &pluginproto.HttpResponse{Status: http.StatusNoContent}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), HTTPForwarder: forwarder})

	resp, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{
		PluginNo: "wk.plugin.echo",
		ToNodeId: 3,
		Request: &pluginproto.HttpRequest{Path: "/remote", Headers: map[string]string{
			"Keep-Alive": "timeout=5",
			"X-Keep":     "keep",
		}},
	}, "caller")

	if err != nil {
		t.Fatalf("HTTPForward returned error: %v", err)
	}
	if resp.GetStatus() != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.GetStatus())
	}
	if forwarder.calls != 1 || forwarder.nodeID != 3 {
		t.Fatalf("forwarder calls/node = %d/%d", forwarder.calls, forwarder.nodeID)
	}
	if forwarder.req.GetPluginNo() != "wk.plugin.echo" || forwarder.req.GetToNodeId() != 3 {
		t.Fatalf("forward req = %#v", forwarder.req)
	}
	if _, ok := forwarder.req.GetRequest().GetHeaders()["Keep-Alive"]; ok {
		t.Fatalf("hop-by-hop header forwarded remotely: %#v", forwarder.req.GetRequest().GetHeaders())
	}
	if forwarder.req.GetRequest().GetHeaders()["X-Keep"] != "keep" {
		t.Fatalf("kept header missing: %#v", forwarder.req.GetRequest().GetHeaders())
	}
}

func TestHTTPForwardFanoutIsExplicitlyDeferred(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: &fakeInvoker{}})

	_, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo", ToNodeId: -1}, "caller")

	assertErrorIs(t, err, ErrHTTPForwardFanoutDeferred)
}

func TestHTTPForwardEnforcesBodyLimitBeforeLocalRoute(t *testing.T) {
	invoker := &fakeInvokerWithResponses{responses: map[string][]byte{PathRoute: mustMarshalProto(t, &pluginproto.HttpResponse{Status: http.StatusOK})}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: invoker, HTTPForwardMaxBodyBytes: 4})

	_, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo", Request: &pluginproto.HttpRequest{Body: []byte("12345")}}, "caller")

	assertErrorIs(t, err, ErrHTTPForwardBodyTooLarge)
	if _, ok := invoker.lastRequest(); ok {
		t.Fatal("plugin route should not be invoked for oversized body")
	}
}

func TestHTTPForwardRequiresForwarderForRemoteNode(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Invoker: &fakeInvoker{}})

	_, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo", ToNodeId: 9, Request: &pluginproto.HttpRequest{}}, "caller")

	assertErrorIs(t, err, ErrHTTPForwarderRequired)
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

func TestHTTPForwardPropagatesForwarderError(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), HTTPForwarder: &recordingHTTPForwarder{err: errors.New("remote down")}})

	_, err := app.HTTPForward(context.Background(), &pluginproto.ForwardHttpReq{PluginNo: "wk.plugin.echo", ToNodeId: 2}, "caller")

	if err == nil || err.Error() != "remote down" {
		t.Fatalf("error = %v, want remote down", err)
	}
}
