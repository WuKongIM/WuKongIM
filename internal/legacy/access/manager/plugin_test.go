package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/stretchr/testify/require"
)

func TestManagerPluginRoutesRequireClusterPluginPermissions(t *testing.T) {
	readRoutes := []string{
		"/manager/nodes/2/plugins",
		"/manager/nodes/2/plugins/wk.echo",
		"/manager/plugin-bindings?uid=u1",
		"/manager/plugin-bindings?plugin_no=wk.echo",
	}
	for _, path := range readRoutes {
		t.Run("read "+path, func(t *testing.T) {
			assertPluginRoutePermission(t, http.MethodGet, path, "", "r")
		})
	}

	writeRoutes := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPut, path: "/manager/nodes/2/plugins/wk.echo/config", body: `{"secret":"******"}`},
		{method: http.MethodPost, path: "/manager/nodes/2/plugins/wk.echo/restart", body: `{}`},
		{method: http.MethodDelete, path: "/manager/nodes/2/plugins/wk.echo", body: ``},
		{method: http.MethodPost, path: "/manager/plugin-bindings", body: `{"uid":"u1","plugin_no":"wk.echo"}`},
		{method: http.MethodDelete, path: "/manager/plugin-bindings", body: `{"uid":"u1","plugin_no":"wk.echo"}`},
	}
	for _, tc := range writeRoutes {
		t.Run("write "+tc.path, func(t *testing.T) {
			assertPluginRoutePermission(t, tc.method, tc.path, tc.body, "w")
		})
	}
}

func TestManagerPluginListReturnsNodeScopedDTOs(t *testing.T) {
	now := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	srv := New(Options{Management: managementStub{pluginList: managementPluginList(now)}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/plugins", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"node_id": 2,
		"total": 1,
		"items": [{
			"node_id": 2,
			"plugin_no": "wk.echo",
			"name": "Echo",
			"version": "1.0.0",
			"config_template": {"fields":[{"name":"api_key","type":"secret","label":"API Key"}]},
			"config": {"api_key":"******"},
			"created_at": "2026-05-16T09:00:00Z",
			"updated_at": "2026-05-16T09:00:00Z",
			"status": "running",
			"enabled": true,
			"methods": ["Route", "Send"],
			"priority": 7,
			"persist_after_sync": false,
			"reply_sync": true,
			"is_ai": 1,
			"pid": 123,
			"last_seen_at": "2026-05-16T09:00:00Z",
			"last_error": ""
		}]
	}`, rec.Body.String())
}

func TestManagerPluginUpdateConfigPassesRawJSON(t *testing.T) {
	var sink managementusecase.UpdatePluginConfigRequest
	srv := New(Options{Management: managementStub{
		pluginUpdateReqSink: &sink,
		pluginDetail:        pluginusecase.LocalPluginDetail{NodeID: 2, No: "wk.echo", Status: pluginusecase.StatusRunning, Enabled: true},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/manager/nodes/2/plugins/wk.echo/config", bytes.NewBufferString(`{"secret":"******","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(2), sink.NodeID)
	require.Equal(t, "wk.echo", sink.PluginNo)
	require.JSONEq(t, `{"secret":"******","enabled":true}`, string(sink.Config))
	require.Contains(t, rec.Body.String(), `"node_id":2`)
}

func TestManagerPluginBindingsUseQueryAndMutationBodies(t *testing.T) {
	var listSink managementusecase.PluginBindingListRequest
	var bindSink managementusecase.PluginBindingMutationRequest
	var unbindSink managementusecase.PluginBindingMutationRequest
	srv := New(Options{Management: managementStub{
		pluginBindingListReqSink:     &listSink,
		pluginBindingMutationReqSink: &bindSink,
		pluginBindingUnbindReqSink:   &unbindSink,
		pluginBindingList: managementusecase.PluginBindingListResponse{
			Bindings: []pluginusecase.PluginBinding{{UID: "u1", PluginNo: "wk.echo"}},
		},
		pluginBindingMutation: managementusecase.PluginBindingMutationResponse{Binding: pluginusecase.PluginBinding{UID: "u1", PluginNo: "wk.echo"}, Changed: true},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/plugin-bindings?uid=u1", nil)
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.PluginBindingListRequest{UID: "u1"}, listSink)
	require.JSONEq(t, `{"items":[{"uid":"u1","plugin_no":"wk.echo","warnings":[]}],"total":1,"has_more":false}`, rec.Body.String())

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/plugin-bindings", bytes.NewBufferString(`{"uid":"u1","plugin_no":"wk.echo"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.PluginBindingMutationRequest{UID: "u1", PluginNo: "wk.echo"}, bindSink)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/manager/plugin-bindings", bytes.NewBufferString(`{"uid":"u1","plugin_no":"wk.echo"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.PluginBindingMutationRequest{UID: "u1", PluginNo: "wk.echo"}, unbindSink)
}

func TestManagerPluginMapsUnsupportedAndUnavailable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "unsupported", err: managementusecase.ErrPluginNodeUnsupported, want: http.StatusNotImplemented},
		{name: "unavailable", err: managementusecase.ErrPluginNodeUnavailable, want: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(Options{Management: managementStub{pluginListErr: tc.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/plugins", nil)

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, tc.want, rec.Code)
		})
	}
}

func assertPluginRoutePermission(t *testing.T, method, path, body, action string) {
	t.Helper()
	allowed := New(Options{
		Auth: testAuthConfig([]UserConfig{{Username: "plugin-admin", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.plugin", Actions: []string{action}}}}}),
		Management: managementStub{
			pluginList:            managementPluginList(time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)),
			pluginDetail:          pluginusecase.LocalPluginDetail{NodeID: 2, No: "wk.echo", Status: pluginusecase.StatusRunning, Enabled: true},
			pluginBindingList:     managementusecase.PluginBindingListResponse{Bindings: []pluginusecase.PluginBinding{{UID: "u1", PluginNo: "wk.echo"}}},
			pluginBindingMutation: managementusecase.PluginBindingMutationResponse{Binding: pluginusecase.PluginBinding{UID: "u1", PluginNo: "wk.echo"}, Changed: true},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, allowed, "plugin-admin"))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	allowed.Engine().ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusForbidden, rec.Code)
	require.NotEqual(t, http.StatusNotFound, rec.Code)
	if rec.Code >= 400 {
		t.Fatalf("allowed plugin route returned %d: %s", rec.Code, rec.Body.String())
	}

	denied := New(Options{
		Auth:       testAuthConfig([]UserConfig{{Username: "viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r", "w"}}}}}),
		Management: managementStub{},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, denied, "viewer"))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	denied.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func managementPluginList(now time.Time) pluginusecase.LocalPluginList {
	return pluginusecase.LocalPluginList{
		NodeID: 2,
		Plugins: []pluginusecase.LocalPlugin{{
			NodeID:         2,
			No:             "wk.echo",
			Name:           "Echo",
			Version:        "1.0.0",
			ConfigTemplate: &pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{{Name: "api_key", Type: pluginproto.FieldTypeSecret.String(), Label: "API Key"}}},
			Config:         map[string]any{"api_key": pluginusecase.SecretHidden},
			CreatedAt:      &now,
			UpdatedAt:      &now,
			Status:         pluginusecase.StatusRunning,
			Enabled:        true,
			Methods:        []pluginusecase.Method{pluginusecase.MethodRoute, pluginusecase.MethodSend},
			Priority:       7,
			ReplySync:      true,
			IsAI:           1,
			PID:            123,
			LastSeenAt:     now,
		}},
	}
}

func TestManagerPermissionCatalogIncludesPlugins(t *testing.T) {
	resources := managerPermissionCatalog()
	data, err := json.Marshal(resources)
	require.NoError(t, err)
	require.JSONEq(t, `[{"resource":"cluster.plugin","actions":["r","w"],"description":"Read and manage node-local plugins and cluster-wide plugin bindings."}]`, filterPluginPermissionCatalogJSON(t, data))
}

func filterPluginPermissionCatalogJSON(t *testing.T, data []byte) string {
	t.Helper()
	var resources []PermissionResourceDTO
	require.NoError(t, json.Unmarshal(data, &resources))
	filtered := make([]PermissionResourceDTO, 0, 1)
	for _, resource := range resources {
		if resource.Resource == "cluster.plugin" {
			filtered = append(filtered, resource)
		}
	}
	out, err := json.Marshal(filtered)
	require.NoError(t, err)
	return string(out)
}
