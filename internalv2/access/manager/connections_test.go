package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerConnectionsReturnsList(t *testing.T) {
	connectedAt := time.Date(2026, 4, 23, 8, 0, 0, 0, time.UTC)
	var received managementusecase.ListConnectionsRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "admin",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.connection", Actions: []string{"r"}}},
		}}),
		Management: managerNodesStub{
			connectionsReqSink: &received,
			connections: []managementusecase.Connection{{
				NodeID: 2, SessionID: 101, UID: "u1", DeviceID: "device-a",
				DeviceFlag: "app", DeviceLevel: "master", SlotID: 9, State: "active",
				Listener: "tcp", ConnectedAt: connectedAt,
				RemoteAddr: "10.0.0.1:5000", LocalAddr: "127.0.0.1:7000",
			}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections?node_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if received != (managementusecase.ListConnectionsRequest{NodeID: 2}) {
		t.Fatalf("request = %#v, want node_id 2", received)
	}
	if !jsonEqual(rec.Body.String(), `{
		"total": 1,
		"items": [{
			"node_id": 2,
			"session_id": 101,
			"uid": "u1",
			"device_id": "device-a",
			"device_flag": "app",
			"device_level": "master",
			"slot_id": 9,
			"state": "active",
			"listener": "tcp",
			"connected_at": "2026-04-23T08:00:00Z",
			"remote_addr": "10.0.0.1:5000",
			"local_addr": "127.0.0.1:7000"
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerConnectionsRequiresConnectionReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "viewer",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.user", Actions: []string{"r"}}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestManagerConnectionsRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "admin",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.connection", Actions: []string{"r"}}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections?node_id=bad", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !jsonEqual(rec.Body.String(), `{"error":"bad_request","message":"invalid node_id"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerConnectionDetailReturnsItem(t *testing.T) {
	connectedAt := time.Date(2026, 4, 23, 8, 10, 0, 0, time.UTC)
	var received managementusecase.GetConnectionRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "admin",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.connection", Actions: []string{"r"}}},
		}}),
		Management: managerNodesStub{
			connectionDetailReqSink: &received,
			connectionDetail: managementusecase.ConnectionDetail{
				NodeID: 2, SessionID: 202, UID: "u2", DeviceID: "device-b",
				DeviceFlag: "web", DeviceLevel: "slave", SlotID: 3, State: "closing",
				Listener: "ws", ConnectedAt: connectedAt,
				RemoteAddr: "10.0.0.2:6000", LocalAddr: "127.0.0.1:7100",
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections/202?node_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if received != (managementusecase.GetConnectionRequest{NodeID: 2, SessionID: 202}) {
		t.Fatalf("request = %#v, want node 2 session 202", received)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 2,
		"session_id": 202,
		"uid": "u2",
		"device_id": "device-b",
		"device_flag": "web",
		"device_level": "slave",
		"slot_id": 3,
		"state": "closing",
		"listener": "ws",
		"connected_at": "2026-04-23T08:10:00Z",
		"remote_addr": "10.0.0.2:6000",
		"local_addr": "127.0.0.1:7100"
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerConnectionDetailRejectsInvalidSessionAndReturnsNotFound(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "admin",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.connection", Actions: []string{"r"}}},
		}}),
		Management: managerNodesStub{connectionDetailErr: metadb.ErrNotFound},
	})

	invalid := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/connections/bad", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(invalid, req)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, want %d", invalid.Code, http.StatusBadRequest)
	}

	missing := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/manager/connections/99", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(missing, req)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want %d", missing.Code, http.StatusNotFound)
	}
	if !jsonEqual(missing.Body.String(), `{"error":"not_found","message":"connection not found"}`) {
		t.Fatalf("body = %s", missing.Body.String())
	}
}
