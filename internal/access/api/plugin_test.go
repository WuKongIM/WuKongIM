package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/stretchr/testify/require"
)

func TestPluginRouteDisabledReturnsNotFound(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/plugins/wk.plugin.echo/hello", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPluginRouteConvertsHTTPRequestAndWritesPluginResponse(t *testing.T) {
	plugins := &recordingPluginRouteUsecase{resp: &pluginproto.HttpResponse{
		Status:  http.StatusAccepted,
		Headers: map[string]string{"X-Plugin": "echo", "Content-Type": "application/custom"},
		Body:    []byte("accepted"),
	}}
	srv := New(Options{PluginRoutes: plugins})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/plugins/wk.plugin.echo/hello?name=alice&debug=1", strings.NewReader("payload"))
	req.Header.Set("X-Trace", "trace-1")
	req.Header.Set("Content-Type", "text/plain")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, "echo", rec.Header().Get("X-Plugin"))
	require.Equal(t, "application/custom", rec.Header().Get("Content-Type"))
	require.Equal(t, "accepted", rec.Body.String())
	require.Equal(t, 1, plugins.calls)
	require.Equal(t, "wk.plugin.echo", plugins.pluginNo)
	require.NotNil(t, plugins.req)
	require.Equal(t, http.MethodPost, plugins.req.GetMethod())
	require.Equal(t, "/hello", plugins.req.GetPath())
	require.Equal(t, "alice", plugins.req.GetQuery()["name"])
	require.Equal(t, "1", plugins.req.GetQuery()["debug"])
	require.Equal(t, "trace-1", plugins.req.GetHeaders()["X-Trace"])
	require.Equal(t, "text/plain", plugins.req.GetHeaders()["Content-Type"])
	require.Equal(t, []byte("payload"), plugins.req.GetBody())
}

func TestPluginRouteDropsHopByHopHeaders(t *testing.T) {
	plugins := &recordingPluginRouteUsecase{resp: &pluginproto.HttpResponse{Status: http.StatusOK, Headers: map[string]string{
		"Connection":        "X-Resp-Drop",
		"X-Resp-Drop":       "drop",
		"Transfer-Encoding": "chunked",
		"X-Keep":            "keep",
	}}}
	srv := New(Options{PluginRoutes: plugins})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/plugins/wk.plugin.echo/hello", nil)
	req.Header.Set("Connection", "X-Req-Drop")
	req.Header.Set("X-Req-Drop", "drop")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("X-Keep", "keep")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "keep", plugins.req.GetHeaders()["X-Keep"])
	require.NotContains(t, plugins.req.GetHeaders(), "Connection")
	require.NotContains(t, plugins.req.GetHeaders(), "X-Req-Drop")
	require.NotContains(t, plugins.req.GetHeaders(), "Upgrade")
	require.Equal(t, "keep", rec.Header().Get("X-Keep"))
	require.Empty(t, rec.Header().Get("X-Resp-Drop"))
}

func TestPluginRouteOptionsReachesPluginUsecase(t *testing.T) {
	plugins := &recordingPluginRouteUsecase{resp: &pluginproto.HttpResponse{Status: http.StatusAccepted}}
	srv := New(Options{PluginRoutes: plugins})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/plugins/wk.plugin.echo/hello", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, 1, plugins.calls)
	require.Equal(t, http.MethodOptions, plugins.req.GetMethod())
}

func TestPluginRouteRejectsOversizedBody(t *testing.T) {
	plugins := &recordingPluginRouteUsecase{}
	srv := New(Options{PluginRoutes: plugins})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/plugins/wk.plugin.echo/hello", strings.NewReader(strings.Repeat("x", int(defaultPluginRouteMaxBodyBytes)+1)))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Equal(t, 0, plugins.calls)
}

func TestPluginRouteUsecaseErrorReturnsInternalServerError(t *testing.T) {
	srv := New(Options{PluginRoutes: &recordingPluginRouteUsecase{err: errors.New("route failed")}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/plugins/wk.plugin.echo/hello", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "route failed")
}

type recordingPluginRouteUsecase struct {
	calls    int
	pluginNo string
	req      *pluginproto.HttpRequest
	resp     *pluginproto.HttpResponse
	err      error
}

func (r *recordingPluginRouteUsecase) Route(_ context.Context, pluginNo string, req *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error) {
	r.calls++
	r.pluginNo = pluginNo
	r.req = req
	if r.err != nil {
		return nil, r.err
	}
	if r.resp != nil {
		return r.resp, nil
	}
	return &pluginproto.HttpResponse{Status: http.StatusOK}, nil
}
