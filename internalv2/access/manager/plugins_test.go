package manager

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
)

func TestManagerNodePluginsReturnsReadOnlyInventory(t *testing.T) {
	lastSeenAt := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	var requestedNodeID uint64
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.plugin",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			pluginListNodeSink: &requestedNodeID,
			pluginList: managementusecase.NodePluginList{
				NodeID: 2,
				Plugins: []managementusecase.Plugin{{
					NodeID:           2,
					No:               "wk.persist",
					Name:             "Persist",
					Version:          "v1.2.3",
					Methods:          []pluginusecase.Method{pluginusecase.MethodPersistAfter, pluginusecase.MethodSend},
					Priority:         9,
					PersistAfterSync: true,
					ReplySync:        true,
					Status:           "running",
					Enabled:          true,
					IsAI:             1,
					PID:              101,
					LastSeenAt:       lastSeenAt,
					LastError:        "last warning",
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/plugins", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if requestedNodeID != 2 {
		t.Fatalf("requested node id = %d, want 2", requestedNodeID)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 2,
		"total": 1,
		"items": [{
			"node_id": 2,
			"plugin_no": "wk.persist",
			"name": "Persist",
			"version": "v1.2.3",
			"methods": ["PersistAfter", "Send"],
			"priority": 9,
			"persist_after_sync": true,
			"reply_sync": true,
			"status": "running",
			"enabled": true,
			"is_ai": 1,
			"pid": 101,
			"last_seen_at": "2026-06-22T10:00:00Z",
			"last_error": "last warning"
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerNodePluginReturnsDetail(t *testing.T) {
	var requestedNodeID uint64
	var requestedPluginNo string
	createdAt := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 6, 22, 9, 30, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.plugin",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			pluginDetailNodeSink: &requestedNodeID,
			pluginDetailNoSink:   &requestedPluginNo,
			pluginDetail: managementusecase.Plugin{
				NodeID:           3,
				No:               "wk.persist",
				Name:             "Persist",
				ConfigTemplate:   &pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{{Name: "mode", Type: "string", Label: "Mode"}}},
				Config:           map[string]any{"mode": "fast"},
				CreatedAt:        &createdAt,
				UpdatedAt:        &updatedAt,
				Methods:          []pluginusecase.Method{pluginusecase.MethodPersistAfter},
				PersistAfterSync: true,
				Status:           "running",
				Enabled:          true,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/3/plugins/wk.persist", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if requestedNodeID != 3 || requestedPluginNo != "wk.persist" {
		t.Fatalf("request target = node %d plugin %q, want node 3 plugin wk.persist", requestedNodeID, requestedPluginNo)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 3,
		"plugin_no": "wk.persist",
		"name": "Persist",
		"version": "",
		"config_template": {"fields": [{"name": "mode", "type": "string", "label": "Mode"}]},
		"config": {"mode": "fast"},
		"created_at": "2026-06-22T09:00:00Z",
		"updated_at": "2026-06-22T09:30:00Z",
		"methods": ["PersistAfter"],
		"priority": 0,
		"persist_after_sync": true,
		"reply_sync": false,
		"status": "running",
		"enabled": true,
		"is_ai": 0,
		"pid": 0,
		"last_seen_at": "0001-01-01T00:00:00Z",
		"last_error": ""
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerNodePluginLifecycleMutations(t *testing.T) {
	var updateNodeID uint64
	var updatePluginNo string
	var updateConfig string
	var restartNodeID uint64
	var restartPluginNo string
	var uninstallNodeID uint64
	var uninstallPluginNo string
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.plugin",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			pluginConfigUpdateNodeSink: &updateNodeID,
			pluginConfigUpdateNoSink:   &updatePluginNo,
			pluginConfigUpdateBodySink: &updateConfig,
			pluginRestartNodeSink:      &restartNodeID,
			pluginRestartNoSink:        &restartPluginNo,
			pluginUninstallNodeSink:    &uninstallNodeID,
			pluginUninstallNoSink:      &uninstallPluginNo,
			pluginMutationDetail: managementusecase.Plugin{
				NodeID:  2,
				No:      "wk.persist",
				Status:  "running",
				Enabled: true,
				Config:  map[string]any{"mode": "fast"},
			},
		},
	})
	token := mustIssueTestToken(t, srv, "admin")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/manager/nodes/2/plugins/wk.persist/config", strings.NewReader(`{"mode":"fast"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if updateNodeID != 2 || updatePluginNo != "wk.persist" || updateConfig != `{"mode":"fast"}` {
		t.Fatalf("update call = node:%d plugin:%q config:%s", updateNodeID, updatePluginNo, updateConfig)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 2,
		"plugin_no": "wk.persist",
		"changed": true,
		"plugin": {
			"node_id": 2,
			"plugin_no": "wk.persist",
			"name": "",
			"version": "",
			"config": {"mode": "fast"},
			"methods": [],
			"priority": 0,
			"persist_after_sync": false,
			"reply_sync": false,
			"status": "running",
			"enabled": true,
			"is_ai": 0,
			"pid": 0,
			"last_seen_at": "0001-01-01T00:00:00Z",
			"last_error": ""
		}
	}`) {
		t.Fatalf("update body = %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/nodes/2/plugins/wk.persist/restart", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restart status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if restartNodeID != 2 || restartPluginNo != "wk.persist" {
		t.Fatalf("restart call = node:%d plugin:%q", restartNodeID, restartPluginNo)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/manager/nodes/2/plugins/wk.persist", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("uninstall status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if uninstallNodeID != 2 || uninstallPluginNo != "wk.persist" {
		t.Fatalf("uninstall call = node:%d plugin:%q", uninstallNodeID, uninstallPluginNo)
	}
	if !jsonEqual(rec.Body.String(), `{"node_id":2,"plugin_no":"wk.persist","changed":true}`) {
		t.Fatalf("uninstall body = %s", rec.Body.String())
	}
}

func TestManagerNodePluginConfigRejectsInvalidBody(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})

	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "empty", body: ""},
		{name: "array", body: `[]`},
		{name: "invalid json", body: `{"mode":`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/manager/nodes/2/plugins/wk.persist/config", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
}

func TestManagerNodePluginsRequiresPluginReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/plugins", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestManagerPluginBindingsUseQueryAndMutationBodies(t *testing.T) {
	var listSink managementusecase.PluginBindingListRequest
	var bindSink managementusecase.PluginBindingMutationRequest
	var unbindSink managementusecase.PluginBindingMutationRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.plugin",
				Actions:  []string{"r", "w"},
			}},
		}}),
		Management: managerNodesStub{
			pluginBindingListReqSink:     &listSink,
			pluginBindingMutationReqSink: &bindSink,
			pluginBindingUnbindReqSink:   &unbindSink,
			pluginBindingList: managementusecase.PluginBindingListResponse{
				Bindings: []managementusecase.PluginBinding{{UID: "u1", PluginNo: "wk.receive"}},
			},
			pluginBindingMutation: managementusecase.PluginBindingMutationResponse{
				Binding: managementusecase.PluginBinding{UID: "u1", PluginNo: "wk.receive"},
				Changed: true,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/plugin-bindings?uid=u1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if listSink != (managementusecase.PluginBindingListRequest{UID: "u1"}) {
		t.Fatalf("list request = %#v, want uid u1", listSink)
	}
	if !jsonEqual(rec.Body.String(), `{
		"items": [{"uid":"u1","plugin_no":"wk.receive","warnings":[]}],
		"total": 1,
		"has_more": false
	}`) {
		t.Fatalf("list body = %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/plugin-bindings", bytes.NewBufferString(`{"uid":"u1","plugin_no":"wk.receive"}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bind status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if bindSink != (managementusecase.PluginBindingMutationRequest{UID: "u1", PluginNo: "wk.receive"}) {
		t.Fatalf("bind request = %#v, want u1/wk.receive", bindSink)
	}
	if !jsonEqual(rec.Body.String(), `{
		"binding": {"uid":"u1","plugin_no":"wk.receive","warnings":[]},
		"changed": true
	}`) {
		t.Fatalf("bind body = %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/manager/plugin-bindings", strings.NewReader(`{"uid":"u1","plugin_no":"wk.receive"}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unbind status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if unbindSink != (managementusecase.PluginBindingMutationRequest{UID: "u1", PluginNo: "wk.receive"}) {
		t.Fatalf("unbind request = %#v, want u1/wk.receive", unbindSink)
	}
}

func TestManagerPluginBindingsRequirePluginPermissions(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r", "w"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "read", method: http.MethodGet, path: "/manager/plugin-bindings?uid=u1"},
		{name: "bind", method: http.MethodPost, path: "/manager/plugin-bindings", body: `{"uid":"u1","plugin_no":"wk.receive"}`},
		{name: "unbind", method: http.MethodDelete, path: "/manager/plugin-bindings", body: `{"uid":"u1","plugin_no":"wk.receive"}`},
		{name: "config update", method: http.MethodPut, path: "/manager/nodes/2/plugins/wk.persist/config", body: `{}`},
		{name: "restart", method: http.MethodPost, path: "/manager/nodes/2/plugins/wk.persist/restart"},
		{name: "uninstall", method: http.MethodDelete, path: "/manager/nodes/2/plugins/wk.persist"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}

func TestManagerPluginBindingsRejectInvalidRequests(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})

	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "missing selector", method: http.MethodGet, path: "/manager/plugin-bindings"},
		{name: "ambiguous selector", method: http.MethodGet, path: "/manager/plugin-bindings?uid=u1&plugin_no=wk.receive"},
		{name: "invalid limit", method: http.MethodGet, path: "/manager/plugin-bindings?plugin_no=wk.receive&limit=bad"},
		{name: "missing bind uid", method: http.MethodPost, path: "/manager/plugin-bindings", body: `{"plugin_no":"wk.receive"}`},
		{name: "invalid json", method: http.MethodDelete, path: "/manager/plugin-bindings", body: `{"uid":`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
}

func TestManagerNodePluginRejectsInvalidTarget(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})

	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "invalid node id",
			path: "/manager/nodes/bad/plugins",
			body: `{"error":"bad_request","message":"invalid node_id"}`,
		},
		{
			name: "empty plugin no",
			path: "/manager/nodes/2/plugins/%20",
			body: `{"error":"bad_request","message":"invalid plugin_no"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.body)
			}
		})
	}
}

func TestManagerNodePluginMapsUsecaseErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		body string
	}{
		{
			name: "not found",
			err:  pluginusecase.ErrPluginNotFound,
			code: http.StatusNotFound,
			body: `{"error":"not_found","message":"plugin not found"}`,
		},
		{
			name: "node unavailable",
			err:  managementusecase.ErrPluginNodeUnavailable,
			code: http.StatusServiceUnavailable,
			body: `{"error":"service_unavailable","message":"plugin node unavailable"}`,
		},
		{
			name: "unexpected",
			err:  errors.New("boom"),
			code: http.StatusInternalServerError,
			body: `{"error":"internal_error","message":"boom"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{pluginDetailErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/plugins/wk.persist", nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.code {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.code, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.body)
			}
		})
	}
}
