package manager

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
	"github.com/stretchr/testify/require"
)

func TestManagerDiagnosticsTrackingCreateSenderUID(t *testing.T) {
	var received managementusecase.DiagnosticsTrackingCreateRequest
	srv := newDiagnosticsTrackingTestServer(t, diagnosticsTrackingHTTPStub{
		createSink: &received,
		createResp: managementusecase.DiagnosticsTrackingMutationResponse{
			Status: managementusecase.DiagnosticsTrackingStatusOK,
			Rule:   managementusecase.DiagnosticsTrackingRule{ID: "rule-1", Target: "sender_uid", UID: "u1", SampleRate: 1},
			Nodes:  []managementusecase.DiagnosticsTrackingNodeResult{{NodeID: 1, Status: "ok"}},
		},
	})

	rec := performDiagnosticsJSONRequest(t, srv, http.MethodPost, "/manager/diagnostics/tracking-rules", "admin", `{"target":"sender_uid","uid":"u1","ttl_seconds":3600,"sample_rate":1}`)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "sender_uid", received.Target)
	require.Equal(t, "u1", received.UID)
	require.Equal(t, 3600, received.TTLSeconds)
	require.JSONEq(t, `{"status":"ok","rule":{"rule_id":"rule-1","target":"sender_uid","uid":"u1","sample_rate":1},"nodes":[{"node_id":1,"status":"ok","notes":[]}],"notes":[]}`, rec.Body.String())
}

func TestManagerDiagnosticsTrackingCreateChannel(t *testing.T) {
	var received managementusecase.DiagnosticsTrackingCreateRequest
	srv := newDiagnosticsTrackingTestServer(t, diagnosticsTrackingHTTPStub{
		createSink: &received,
		createResp: managementusecase.DiagnosticsTrackingMutationResponse{
			Status: managementusecase.DiagnosticsTrackingStatusOK,
			Rule:   managementusecase.DiagnosticsTrackingRule{ID: "rule-2", Target: "channel", ChannelKey: "channel/2/ZzE", ChannelID: "g1", ChannelType: 2, SampleRate: 1},
		},
	})

	rec := performDiagnosticsJSONRequest(t, srv, http.MethodPost, "/manager/diagnostics/tracking-rules", "admin", `{"target":"channel","channel_id":"g1","channel_type":2,"ttl_seconds":600}`)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "channel", received.Target)
	require.Equal(t, "g1", received.ChannelID)
	require.Equal(t, uint8(2), received.ChannelType)
	require.Equal(t, 1.0, received.SampleRate)
}

func TestManagerDiagnosticsTrackingListRequiresReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{Username: "diag-viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.diagnostics", Actions: []string{"r"}}}},
			{Username: "node-viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}}},
		}),
		Management: diagnosticsTrackingHTTPStub{listResp: managementusecase.DiagnosticsTrackingListResponse{Status: managementusecase.DiagnosticsTrackingStatusOK}},
	})

	allowed := performDiagnosticsJSONRequest(t, srv, http.MethodGet, "/manager/diagnostics/tracking-rules", "diag-viewer", "")
	require.Equal(t, http.StatusOK, allowed.Code)

	denied := performDiagnosticsJSONRequest(t, srv, http.MethodGet, "/manager/diagnostics/tracking-rules", "node-viewer", "")
	require.Equal(t, http.StatusForbidden, denied.Code)
}

func TestManagerDiagnosticsTrackingCreateRequiresWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{Username: "diag-viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.diagnostics", Actions: []string{"r"}}}},
			{Username: "diag-writer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.diagnostics", Actions: []string{"w"}}}},
		}),
		Management: diagnosticsTrackingHTTPStub{createResp: managementusecase.DiagnosticsTrackingMutationResponse{Status: managementusecase.DiagnosticsTrackingStatusOK}},
	})

	allowed := performDiagnosticsJSONRequest(t, srv, http.MethodPost, "/manager/diagnostics/tracking-rules", "diag-writer", `{"target":"sender_uid","uid":"u1","ttl_seconds":60}`)
	require.Equal(t, http.StatusOK, allowed.Code)

	denied := performDiagnosticsJSONRequest(t, srv, http.MethodPost, "/manager/diagnostics/tracking-rules", "diag-viewer", `{"target":"sender_uid","uid":"u1","ttl_seconds":60}`)
	require.Equal(t, http.StatusForbidden, denied.Code)
}

func TestManagerDiagnosticsTrackingDeleteRequiresWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{Username: "diag-viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.diagnostics", Actions: []string{"r"}}}},
			{Username: "diag-writer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.diagnostics", Actions: []string{"w"}}}},
		}),
		Management: diagnosticsTrackingHTTPStub{deleteResp: managementusecase.DiagnosticsTrackingDeleteResponse{Status: managementusecase.DiagnosticsTrackingStatusOK, RuleID: "rule-1"}},
	})

	allowed := performDiagnosticsJSONRequest(t, srv, http.MethodDelete, "/manager/diagnostics/tracking-rules/rule-1", "diag-writer", "")
	require.Equal(t, http.StatusOK, allowed.Code)

	denied := performDiagnosticsJSONRequest(t, srv, http.MethodDelete, "/manager/diagnostics/tracking-rules/rule-1", "diag-viewer", "")
	require.Equal(t, http.StatusForbidden, denied.Code)
}

func TestManagerDiagnosticsTrackingRejectsInvalidRequest(t *testing.T) {
	srv := newDiagnosticsTrackingTestServer(t, diagnosticsTrackingHTTPStub{})

	rec := performDiagnosticsJSONRequest(t, srv, http.MethodPost, "/manager/diagnostics/tracking-rules", "admin", `{"target":"sender_uid","ttl_seconds":60}`)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestManagerDiagnosticsTrackingListAndDelete(t *testing.T) {
	var deleted string
	srv := newDiagnosticsTrackingTestServer(t, diagnosticsTrackingHTTPStub{
		listResp: managementusecase.DiagnosticsTrackingListResponse{
			Status: managementusecase.DiagnosticsTrackingStatusOK,
			Rules:  []managementusecase.DiagnosticsTrackingRule{{ID: "rule-1", Target: "sender_uid", UID: "u1", SampleRate: 1}},
		},
		deleteSink: &deleted,
		deleteResp: managementusecase.DiagnosticsTrackingDeleteResponse{
			Status: managementusecase.DiagnosticsTrackingStatusOK,
			RuleID: "rule-1",
		},
	})

	list := performDiagnosticsJSONRequest(t, srv, http.MethodGet, "/manager/diagnostics/tracking-rules", "admin", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.JSONEq(t, `{"status":"ok","rules":[{"rule_id":"rule-1","target":"sender_uid","uid":"u1","sample_rate":1}],"nodes":[],"notes":[]}`, list.Body.String())

	deleteRec := performDiagnosticsJSONRequest(t, srv, http.MethodDelete, "/manager/diagnostics/tracking-rules/rule-1", "admin", "")
	require.Equal(t, http.StatusOK, deleteRec.Code)
	require.Equal(t, "rule-1", deleted)
}

func newDiagnosticsTrackingTestServer(t *testing.T, management Management) *Server {
	t.Helper()
	return New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "admin",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.diagnostics", Actions: []string{"r", "w"}}},
		}}),
		Management: management,
	})
}

func performDiagnosticsJSONRequest(t *testing.T, srv *Server, method, path, username, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var reqBody *bytes.Reader
	if body != "" {
		reqBody = bytes.NewReader([]byte(body))
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, username))
	srv.Engine().ServeHTTP(rec, req)
	return rec
}

type diagnosticsTrackingHTTPStub struct {
	managementStub
	createSink *managementusecase.DiagnosticsTrackingCreateRequest
	createResp managementusecase.DiagnosticsTrackingMutationResponse
	createErr  error
	listResp   managementusecase.DiagnosticsTrackingListResponse
	listErr    error
	deleteSink *string
	deleteResp managementusecase.DiagnosticsTrackingDeleteResponse
	deleteErr  error
}

func (s diagnosticsTrackingHTTPStub) CreateDiagnosticsTrackingRule(_ context.Context, req managementusecase.DiagnosticsTrackingCreateRequest) (managementusecase.DiagnosticsTrackingMutationResponse, error) {
	if s.createSink != nil {
		*s.createSink = req
	}
	return s.createResp, s.createErr
}

func (s diagnosticsTrackingHTTPStub) ListDiagnosticsTrackingRules(context.Context) (managementusecase.DiagnosticsTrackingListResponse, error) {
	return s.listResp, s.listErr
}

func (s diagnosticsTrackingHTTPStub) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) (managementusecase.DiagnosticsTrackingDeleteResponse, error) {
	if s.deleteSink != nil {
		*s.deleteSink = ruleID
	}
	return s.deleteResp, s.deleteErr
}
