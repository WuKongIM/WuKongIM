package manager

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

type opsMCPMutationRecorder struct {
	revokeReq  managementusecase.OpsMCPTokenRevokeRequest
	ownerReq   managementusecase.OpsMCPOwnerUpdateRequest
	startReq   managementusecase.OpsMCPStateMutationRequest
	stopReq    managementusecase.OpsMCPStateMutationRequest
	auditLimit int
	err        error
}

func (f *opsMCPMutationRecorder) OpsMCPStatus(context.Context) (managementusecase.OpsMCPStatus, error) {
	return managementusecase.OpsMCPStatus{}, f.err
}

func (f *opsMCPMutationRecorder) CreateOpsMCPToken(context.Context, managementusecase.OpsMCPTokenCreateRequest) (managementusecase.OpsMCPTokenCreateResponse, error) {
	return managementusecase.OpsMCPTokenCreateResponse{}, f.err
}

func (f *opsMCPMutationRecorder) RevokeOpsMCPToken(_ context.Context, req managementusecase.OpsMCPTokenRevokeRequest) error {
	f.revokeReq = req
	return f.err
}

func (f *opsMCPMutationRecorder) SetOpsMCPOwner(_ context.Context, req managementusecase.OpsMCPOwnerUpdateRequest) error {
	f.ownerReq = req
	return f.err
}

func (f *opsMCPMutationRecorder) StartOpsMCP(_ context.Context, req managementusecase.OpsMCPStateMutationRequest) error {
	f.startReq = req
	return f.err
}

func (f *opsMCPMutationRecorder) StopOpsMCP(_ context.Context, req managementusecase.OpsMCPStateMutationRequest) error {
	f.stopReq = req
	return f.err
}

func (f *opsMCPMutationRecorder) OpsMCPAudits(_ context.Context, limit int) ([]managementusecase.OpsMCPAuditEntry, error) {
	f.auditLimit = limit
	return nil, f.err
}

func TestManagerOpsMCPMutationsPreserveRevisionAndIdempotencyFences(t *testing.T) {
	backend := &opsMCPMutationRecorder{}
	srv, token := newOpsMCPWriterServer(t, backend)
	tests := []struct {
		method string
		path   string
		body   string
		key    string
	}{
		{method: http.MethodDelete, path: "/manager/mcp/tokens/credential-a", body: `{"expected_revision":11}`, key: "revoke-a"},
		{method: http.MethodPut, path: "/manager/mcp/owner", body: `{"expected_revision":12,"owner_node_id":4}`, key: "owner-a"},
		{method: http.MethodPost, path: "/manager/mcp/start", body: `{"expected_revision":13}`, key: "start-a"},
		{method: http.MethodPost, path: "/manager/mcp/stop", body: `{"expected_revision":14}`, key: "stop-a"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", test.key)
		srv.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted || !jsonEqual(recorder.Body.String(), `{"accepted":true}`) {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}
	if backend.revokeReq.CredentialID != "credential-a" || backend.revokeReq.ExpectedRevision != 11 || backend.revokeReq.IdempotencyKey != "revoke-a" {
		t.Fatalf("revoke request = %#v", backend.revokeReq)
	}
	if backend.ownerReq.OwnerNodeID != 4 || backend.ownerReq.ExpectedRevision != 12 || backend.ownerReq.IdempotencyKey != "owner-a" {
		t.Fatalf("owner request = %#v", backend.ownerReq)
	}
	if backend.startReq.ExpectedRevision != 13 || backend.startReq.IdempotencyKey != "start-a" {
		t.Fatalf("start request = %#v", backend.startReq)
	}
	if backend.stopReq.ExpectedRevision != 14 || backend.stopReq.IdempotencyKey != "stop-a" {
		t.Fatalf("stop request = %#v", backend.stopReq)
	}
}

func TestManagerOpsMCPRejectsAmbiguousBodiesAndInvalidAuditBounds(t *testing.T) {
	backend := &opsMCPMutationRecorder{}
	srv, token := newOpsMCPWriterServer(t, backend)
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/manager/mcp/start", body: `{"expected_revision":1,"unknown":true}`},
		{method: http.MethodPost, path: "/manager/mcp/stop", body: `{"expected_revision":1}{"expected_revision":2}`},
		{method: http.MethodPut, path: "/manager/mcp/owner", body: `{"expected_revision":`},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		srv.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s body=%q status=%d response=%s", test.path, test.body, recorder.Code, recorder.Body.String())
		}
	}
	if backend.startReq.ExpectedRevision != 0 || backend.stopReq.ExpectedRevision != 0 || backend.ownerReq.ExpectedRevision != 0 {
		t.Fatalf("backend received rejected body: start=%#v stop=%#v owner=%#v", backend.startReq, backend.stopReq, backend.ownerReq)
	}

	for _, limit := range []string{"0", "201", "bad"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/manager/mcp/audits?limit="+limit, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		srv.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q status=%d body=%s", limit, recorder.Code, recorder.Body.String())
		}
	}
	if backend.auditLimit != 0 {
		t.Fatalf("audit backend called for rejected limit: %d", backend.auditLimit)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/manager/mcp/audits?limit=17", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	srv.Engine().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || backend.auditLimit != 17 {
		t.Fatalf("valid audit status=%d limit=%d body=%s", recorder.Code, backend.auditLimit, recorder.Body.String())
	}
}

func TestManagerOpsMCPMutationsExposeStableErrorsWithoutBackendDetails(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		path       string
		wantStatus int
		wantCode   string
		forbidText string
	}{
		{name: "invalid", err: managementusecase.ErrOpsMCPInvalidRequest, path: "/manager/mcp/start", wantStatus: http.StatusBadRequest, wantCode: "bad_request"},
		{name: "missing credential", err: managementusecase.ErrOpsMCPTokenNotFound, path: "/manager/mcp/tokens/missing", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "revision conflict", err: managementusecase.ErrOpsMCPConflict, path: "/manager/mcp/start", wantStatus: http.StatusConflict, wantCode: "conflict"},
		{name: "unavailable", err: managementusecase.ErrOpsMCPUnavailable, path: "/manager/mcp/start", wantStatus: http.StatusServiceUnavailable, wantCode: "service_unavailable"},
		{name: "private failure", err: errors.New("database password leaked"), path: "/manager/mcp/start", wantStatus: http.StatusInternalServerError, wantCode: "internal_error", forbidText: "database password leaked"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &opsMCPMutationRecorder{err: test.err}
			srv, token := newOpsMCPWriterServer(t, backend)
			method := http.MethodPost
			if strings.Contains(test.path, "/tokens/") {
				method = http.MethodDelete
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(method, test.path, strings.NewReader(`{"expected_revision":7}`))
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("Content-Type", "application/json")
			srv.Engine().ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), `"error":"`+test.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.forbidText != "" && strings.Contains(recorder.Body.String(), test.forbidText) {
				t.Fatalf("private backend detail leaked: %s", recorder.Body.String())
			}
		})
	}
}

func TestManagerOpsMCPValidMutationFailsClosedWithoutBackend(t *testing.T) {
	srv, token := newOpsMCPWriterServer(t, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/manager/mcp/start", strings.NewReader(`{"expected_revision":7}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newOpsMCPWriterServer(t *testing.T, backend OpsMCPManagement) (*Server, string) {
	t.Helper()
	srv := New(Options{
		OpsMCP: backend,
		Auth: testAuthConfig([]UserConfig{{
			Username: "writer", Password: "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.mcp", Actions: []string{"r", "w"}}},
		}}),
	})
	return srv, mustIssueTestToken(t, srv, "writer")
}
