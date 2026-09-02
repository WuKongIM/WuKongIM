package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRestoreMaintenanceBlocksBusinessAndMutationRoutesButKeepsRecoveryReads(t *testing.T) {
	srv := New(Options{
		Maintenance: func() bool { return true },
		Management:  managerNodesStub{},
	})
	tests := []struct {
		method       string
		path         string
		wantStatus   int
		wantMarker   string
		forbidMarker string
	}{
		{method: http.MethodGet, path: "/manager/channels", wantStatus: http.StatusServiceUnavailable, wantMarker: "restore_maintenance"},
		{method: http.MethodHead, path: "/manager/messages", wantStatus: http.StatusServiceUnavailable, wantMarker: "restore_maintenance"},
		{method: http.MethodPost, path: "/manager/nodes/join", wantStatus: http.StatusServiceUnavailable, wantMarker: "restore_maintenance"},
		{method: http.MethodGet, path: "/manager/nodes", wantStatus: http.StatusOK, forbidMarker: "restore_maintenance"},
		{method: http.MethodGet, path: "/", wantStatus: http.StatusOK, forbidMarker: "restore_maintenance"},
		{method: http.MethodGet, path: "/manager/backups", wantStatus: http.StatusServiceUnavailable, wantMarker: "backup_service_unavailable", forbidMarker: "restore_maintenance"},
		{method: http.MethodOptions, path: "/manager/channels", wantStatus: http.StatusNoContent, forbidMarker: "restore_maintenance"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		srv.Engine().ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != test.wantStatus {
			t.Fatalf("%s %s status=%d want=%d body=%s", test.method, test.path, recorder.Code, test.wantStatus, recorder.Body.String())
		}
		if test.wantMarker != "" && !strings.Contains(recorder.Body.String(), test.wantMarker) {
			t.Fatalf("%s %s body=%s, want %q", test.method, test.path, recorder.Body.String(), test.wantMarker)
		}
		if test.forbidMarker != "" && strings.Contains(recorder.Body.String(), test.forbidMarker) {
			t.Fatalf("%s %s body=%s, must not contain %q", test.method, test.path, recorder.Body.String(), test.forbidMarker)
		}
	}
}

func TestRestoreMaintenanceGateIsInactiveWithoutProviderOrActiveState(t *testing.T) {
	for _, maintenance := range []func() bool{nil, func() bool { return false }} {
		srv := New(Options{Maintenance: maintenance, Management: managerNodesStub{}})
		recorder := httptest.NewRecorder()
		srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/channels", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestRestoreMaintenanceAllowlistStaysNarrowAndRecoverySafe(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{method: http.MethodOptions, path: "/manager/channels", want: true},
		{method: http.MethodPost, path: "/manager/login", want: true},
		{method: http.MethodPost, path: "/mcp", want: true},
		{method: http.MethodPost, path: "/manager/backups/jobs", want: true},
		{method: http.MethodGet, path: "/manager/permissions", want: true},
		{method: http.MethodPost, path: "/public/health", want: true},
		{method: http.MethodGet, path: "/manager/nodes", want: true},
		{method: http.MethodHead, path: "/manager/controller/tasks", want: true},
		{method: http.MethodPost, path: "/manager/nodes/join", want: false},
		{method: http.MethodGet, path: "/manager/channels", want: false},
		{method: http.MethodGet, path: "/manager/conversations", want: false},
		{method: http.MethodGet, path: "/manager/messages", want: false},
		{method: http.MethodGet, path: "/manager/connections", want: false},
		{method: http.MethodGet, path: "/manager/users", want: false},
		{method: http.MethodGet, path: "/manager/system-users", want: false},
		{method: http.MethodGet, path: "/manager/db/inspect/tables", want: false},
		{method: http.MethodGet, path: "/manager/channel-runtime-meta", want: false},
		{method: http.MethodGet, path: "/manager/channel-migrations/active", want: false},
	}
	for _, test := range tests {
		if got := managerMaintenanceAllowed(test.method, test.path); got != test.want {
			t.Fatalf("managerMaintenanceAllowed(%q, %q)=%v want=%v", test.method, test.path, got, test.want)
		}
	}
}
