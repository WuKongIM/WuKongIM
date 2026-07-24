package manager

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerNodeConfigReturnsSelectedNodeSnapshot(t *testing.T) {
	generatedAt := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	var seenNodeID uint64
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			nodeConfigNodeSink: &seenNodeID,
			nodeConfigSnapshot: managementusecase.NodeConfigSnapshot{
				GeneratedAt:     generatedAt,
				NodeID:          2,
				Source:          managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
				RequiresRestart: true,
				Groups: []managementusecase.NodeConfigGroup{{
					ID:    "cluster",
					Title: "Cluster",
					Items: []managementusecase.NodeConfigItem{
						{
							Key:    "WK_CLUSTER_HASH_SLOT_COUNT",
							Label:  "Hash slot count",
							Value:  "256",
							Source: managementusecase.NodeConfigValueSourceTOML,
						},
						{
							Key:       "WK_TOKEN_AUTH_SECRET",
							Label:     "Token auth secret",
							Value:     "******",
							Source:    managementusecase.NodeConfigValueSourceEnvironment,
							Sensitive: true,
							Redacted:  true,
						},
					},
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/config", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seenNodeID != 2 {
		t.Fatalf("node id = %d, want 2", seenNodeID)
	}
	if !strings.Contains(rec.Body.String(), `"node_id":2`) {
		t.Fatalf("body = %s, want node_id 2", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "WK_CLUSTER_HASH_SLOT_COUNT") {
		t.Fatalf("body = %s, want hash slot count key", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "******") {
		t.Fatalf("body = %s, want redacted value", rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at":"2026-07-08T10:00:00Z",
		"node_id":2,
		"source":"effective_startup_config",
		"requires_restart":true,
		"groups":[{
			"id":"cluster",
			"title":"Cluster",
			"items":[{
				"key":"WK_CLUSTER_HASH_SLOT_COUNT",
				"label":"Hash slot count",
				"value":"256",
				"source":"toml",
				"sensitive":false,
				"redacted":false
			},{
				"key":"WK_TOKEN_AUTH_SECRET",
				"label":"Token auth secret",
				"value":"******",
				"source":"env",
				"sensitive":true,
				"redacted":true
			}]
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerNodeConfigErrors(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		management Management
		wantStatus int
		wantBody   string
	}{
		{
			name:       "missing management",
			url:        "/manager/nodes/2/config",
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"error":"service_unavailable","message":"management not configured"}`,
		},
		{
			name:       "invalid node id",
			url:        "/manager/nodes/bad/config",
			management: managerNodesStub{},
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"bad_request","message":"invalid node_id"}`,
		},
		{
			name:       "zero node id",
			url:        "/manager/nodes/0/config",
			management: managerNodesStub{},
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"bad_request","message":"invalid node_id"}`,
		},
		{
			name:       "invalid argument",
			url:        "/manager/nodes/2/config",
			management: managerNodesStub{nodeConfigErr: metadb.ErrInvalidArgument},
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"bad_request","message":"invalid node config request"}`,
		},
		{
			name:       "not found",
			url:        "/manager/nodes/2/config",
			management: managerNodesStub{nodeConfigErr: metadb.ErrNotFound},
			wantStatus: http.StatusNotFound,
			wantBody:   `{"error":"not_found","message":"node not found"}`,
		},
		{
			name:       "unavailable",
			url:        "/manager/nodes/2/config",
			management: managerNodesStub{nodeConfigErr: managementusecase.ErrNodeConfigUnavailable},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"error":"service_unavailable","message":"node config unavailable"}`,
		},
		{
			name:       "default internal",
			url:        "/manager/nodes/2/config",
			management: managerNodesStub{nodeConfigErr: errors.New("boom")},
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":"internal_error","message":"boom"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: tt.management})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestManagerNodeConfigRequiresNodeReadPermission(t *testing.T) {
	calls := 0
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{nodeConfigCallCount: &calls},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/config", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if calls != 0 {
		t.Fatalf("NodeConfigSnapshot calls = %d, want 0", calls)
	}
}
