package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

type fakeOpsMCPManagement struct {
	status        managementusecase.OpsMCPStatus
	createRequest managementusecase.OpsMCPTokenCreateRequest
}

func (f *fakeOpsMCPManagement) OpsMCPStatus(context.Context) (managementusecase.OpsMCPStatus, error) {
	return f.status, nil
}

func (f *fakeOpsMCPManagement) CreateOpsMCPToken(_ context.Context, req managementusecase.OpsMCPTokenCreateRequest) (managementusecase.OpsMCPTokenCreateResponse, error) {
	f.createRequest = req
	return managementusecase.OpsMCPTokenCreateResponse{
		CredentialID: "token-a", Token: "wko_token-a_secret", CreatedAtUnixMillis: 1710000001000, Revision: 8,
	}, nil
}

func (f *fakeOpsMCPManagement) RevokeOpsMCPToken(context.Context, managementusecase.OpsMCPTokenRevokeRequest) error {
	return nil
}

func (f *fakeOpsMCPManagement) SetOpsMCPOwner(context.Context, managementusecase.OpsMCPOwnerUpdateRequest) error {
	return nil
}

func (f *fakeOpsMCPManagement) StartOpsMCP(context.Context, managementusecase.OpsMCPStateMutationRequest) error {
	return nil
}

func (f *fakeOpsMCPManagement) StopOpsMCP(context.Context, managementusecase.OpsMCPStateMutationRequest) error {
	return nil
}

func (f *fakeOpsMCPManagement) OpsMCPAudits(context.Context, int) ([]managementusecase.OpsMCPAuditEntry, error) {
	return []managementusecase.OpsMCPAuditEntry{{
		RequestID: "request-1", CredentialID: "credential-1", Tool: "pprof_analyze",
		NodeID: 2, SlotID: 3, ChannelType: 4, PprofKind: "cpu", PprofSeconds: 10,
	}}, nil
}

func TestManagerOpsMCPRoutesUseDedicatedPermissionsAndOneTimeToken(t *testing.T) {
	management := &fakeOpsMCPManagement{status: managementusecase.OpsMCPStatus{
		ClusterID: "cluster-a", Revision: 7,
		OwnerCandidates: []managementusecase.OpsMCPOwnerCandidate{{NodeID: 2, Status: "alive"}},
		Credentials:     []managementusecase.OpsMCPCredentialStatus{},
	}}
	srv := New(Options{
		OpsMCP: management,
		Auth: testAuthConfig([]UserConfig{
			{Username: "reader", Password: "reader", Permissions: []PermissionConfig{{Resource: "cluster.mcp", Actions: []string{"r"}}}},
			{Username: "writer", Password: "writer", Permissions: []PermissionConfig{{Resource: "cluster.mcp", Actions: []string{"r", "w"}}}},
		}),
	})

	readRecorder := httptest.NewRecorder()
	readRequest := httptest.NewRequest(http.MethodGet, "/manager/mcp", nil)
	readRequest.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(readRecorder, readRequest)
	if readRecorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", readRecorder.Code, readRecorder.Body.String())
	}
	var statusBody opsMCPStatusResponse
	if err := json.Unmarshal(readRecorder.Body.Bytes(), &statusBody); err != nil ||
		len(statusBody.OwnerCandidates) != 1 || statusBody.OwnerCandidates[0].NodeID != 2 {
		t.Fatalf("GET body = %s error=%v, want owner candidates without cluster.node:r", readRecorder.Body.String(), err)
	}

	deniedRecorder := httptest.NewRecorder()
	deniedRequest := httptest.NewRequest(http.MethodPost, "/manager/mcp/tokens", bytes.NewBufferString(`{"expected_revision":7}`))
	deniedRequest.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	deniedRequest.Header.Set("Content-Type", "application/json")
	deniedRequest.Header.Set("Idempotency-Key", "create-1")
	srv.Engine().ServeHTTP(deniedRecorder, deniedRequest)
	if deniedRecorder.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d body=%s", deniedRecorder.Code, deniedRecorder.Body.String())
	}

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/manager/mcp/tokens", bytes.NewBufferString(`{"expected_revision":7}`))
	createRequest.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Idempotency-Key", "create-1")
	srv.Engine().ServeHTTP(createRecorder, createRequest)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode create body: %v", err)
	}
	if body["token"] != "wko_token-a_secret" || management.createRequest.IdempotencyKey != "create-1" {
		t.Fatalf("create body=%#v request=%#v", body, management.createRequest)
	}
}

func TestManagerOpsMCPAdministrationRequiresManagerAuth(t *testing.T) {
	srv := New(Options{OpsMCP: &fakeOpsMCPManagement{}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/manager/mcp", nil)

	srv.Engine().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable ||
		!jsonEqual(recorder.Body.String(), `{"error":"manager_auth_required","message":"manager authentication must be enabled before MCP administration"}`) {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestManagerOpsMCPAuditProjectsBoundedTargets(t *testing.T) {
	srv := New(Options{
		OpsMCP: &fakeOpsMCPManagement{},
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader", Password: "reader",
			Permissions: []PermissionConfig{{Resource: "cluster.mcp", Actions: []string{"r"}}},
		}}),
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/manager/mcp/audits", nil)
	request.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Items []opsMCPAuditResponse `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode audit body: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].NodeID != 2 || body.Items[0].SlotID != 3 ||
		body.Items[0].ChannelType != 4 || body.Items[0].PprofKind != "cpu" || body.Items[0].PprofSeconds != 10 {
		t.Fatalf("audit body = %#v", body)
	}
}
