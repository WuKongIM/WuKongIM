package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/stretchr/testify/require"
)

type recordingPluginRouter struct {
	plugin   string
	request  *pluginproto.HttpRequest
	deadline time.Time
	response *pluginproto.HttpResponse
	err      error
}

func (r *recordingPluginRouter) Route(ctx context.Context, plugin string, request *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error) {
	r.plugin, r.request = plugin, request
	r.deadline, _ = ctx.Deadline()
	return r.response, r.err
}

func TestPluginHTTPRoutePreservesLegacyRequestAndResponse(t *testing.T) {
	router := &recordingPluginRouter{response: &pluginproto.HttpResponse{Status: 202, Headers: map[string]string{"Content-Type": "application/json", "X-Plugin": "search", "Connection": "X-Private", "X-Private": "drop", "Content-Length": "999"}, Body: []byte(`{"messages":[]}`)}}
	srv := New(Options{Plugins: router, PluginTimeout: 3 * time.Second})
	req := httptest.NewRequest(http.MethodPost, "/plugins/wk.plugin.search/usersearch?limit=5", strings.NewReader(`{"uid":"fixture-user"}`))
	req.Header.Set("X-Request-ID", "fixture-request")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, 202, rec.Code)
	require.Equal(t, `{"messages":[]}`, rec.Body.String())
	require.Equal(t, "search", rec.Header().Get("X-Plugin"))
	for _, header := range []string{"Connection", "X-Private"} {
		require.Empty(t, rec.Header().Get(header))
	}
	require.NotEqual(t, "999", rec.Header().Get("Content-Length"))
	require.Equal(t, "wk.plugin.search", router.plugin)
	require.Equal(t, "POST", router.request.Method)
	require.Equal(t, "/usersearch", router.request.Path)
	require.Equal(t, "5", router.request.Query["limit"])
	require.Equal(t, "fixture-request", router.request.Headers["X-Request-Id"])
	require.Equal(t, []byte(`{"uid":"fixture-user"}`), router.request.Body)
	require.False(t, router.deadline.IsZero())
	require.LessOrEqual(t, time.Until(router.deadline), 3*time.Second)
}

func TestPluginHTTPRouteRejectsUnavailableOversizedAndMaintenanceRequests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options Options
		body    string
		status  int
	}{
		{"unavailable", Options{}, `{}`, 503},
		{"oversized", Options{Plugins: &recordingPluginRouter{}}, strings.Repeat("x", (10<<20)+1), 413},
		{"maintenance", Options{Plugins: &recordingPluginRouter{}, Maintenance: func() bool { return true }}, `{}`, 503},
		{"failure", Options{Plugins: &recordingPluginRouter{err: errors.New("private-plugin-error")}}, `{}`, 502},
		{"invalid status", Options{Plugins: &recordingPluginRouter{response: &pluginproto.HttpResponse{Status: 1000}}}, `{}`, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(tc.options).Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/plugins/wk.plugin.search/search", strings.NewReader(tc.body)))
			require.Equal(t, tc.status, rec.Code)
			require.NotContains(t, rec.Body.String(), "private-plugin-error")
			if router, ok := tc.options.Plugins.(*recordingPluginRouter); ok && (tc.name == "oversized" || tc.name == "maintenance") {
				require.Nil(t, router.request)
			}
		})
	}
}
