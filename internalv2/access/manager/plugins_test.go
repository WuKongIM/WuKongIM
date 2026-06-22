package manager

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
