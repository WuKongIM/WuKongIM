package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestPromoteControllerVoterRouteRequiresControllerWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "node-writer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}, {
			Username: "controller-reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}, {
			Username: "controller-writer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			promoteControllerVoterResponse: managementusecase.PromoteControllerVoterResponse{NodeID: 4},
		},
	})

	for _, username := range []string{"node-writer", "controller-reader"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/controller-voter/promote", nil)
		req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, username))

		srv.Engine().ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want %d; body=%s", username, rec.Code, http.StatusForbidden, rec.Body.String())
		}
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/controller-voter/promote", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "controller-writer"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	if allowed.Code == http.StatusForbidden || allowed.Code == http.StatusUnauthorized {
		t.Fatalf("allowed status = %d, want route to pass auth; body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestPromoteControllerVoterRouteReturnsAccepted(t *testing.T) {
	var seen managementusecase.PromoteControllerVoterRequest
	srv := New(Options{
		Management: managerNodesStub{
			promoteControllerVoterReqSink: &seen,
			promoteControllerVoterResponse: managementusecase.PromoteControllerVoterResponse{
				Changed:        true,
				NodeID:         4,
				StateRevision:  13,
				PreviousVoters: []uint64{1, 2},
				NextVoters:     []uint64{1, 2, 4},
				Warnings:       []string{"target_was_recently_added"},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/controller-voter/promote", strings.NewReader(`{"expected_revision":12}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if seen.NodeID != 4 || seen.ExpectedRevision != 12 {
		t.Fatalf("request = %#v, want node 4 expected revision 12", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"changed": true,
		"node_id": 4,
		"state_revision": 13,
		"previous_voters": [1, 2],
		"next_voters": [1, 2, 4],
		"warnings": ["target_was_recently_added"]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestPromoteControllerVoterRouteReturnsOKForIdempotent(t *testing.T) {
	var seen managementusecase.PromoteControllerVoterRequest
	srv := New(Options{
		Management: managerNodesStub{
			promoteControllerVoterReqSink: &seen,
			promoteControllerVoterResponse: managementusecase.PromoteControllerVoterResponse{
				Changed:        false,
				NodeID:         1,
				StateRevision:  12,
				PreviousVoters: []uint64{1, 2},
				NextVoters:     []uint64{1, 2},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/1/controller-voter/promote", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen.NodeID != 1 || seen.ExpectedRevision != 0 {
		t.Fatalf("request = %#v, want node 1 expected revision 0", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"changed": false,
		"node_id": 1,
		"state_revision": 12,
		"previous_voters": [1, 2],
		"next_voters": [1, 2]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestPromoteControllerVoterRouteMapsErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		wantBody   string
		wantReason string
	}{
		{
			name:     "not found",
			err:      managementusecase.ErrNodeLifecycleNotFound,
			status:   http.StatusNotFound,
			wantBody: `{"error":"not_found","message":"not_found"}`,
		},
		{
			name:       "blocked",
			err:        fmt.Errorf("%w: target_health_stale", managementusecase.ErrControllerVoterPromotionBlocked),
			status:     http.StatusConflict,
			wantReason: "target_health_stale",
		},
		{
			name:     "usecase unavailable",
			err:      managementusecase.ErrControllerVoterPromotionUnavailable,
			status:   http.StatusServiceUnavailable,
			wantBody: `{"error":"service_unavailable","message":"service_unavailable"}`,
		},
		{
			name:     "cluster not started",
			err:      cluster.ErrNotStarted,
			status:   http.StatusServiceUnavailable,
			wantBody: `{"error":"service_unavailable","message":"service_unavailable"}`,
		},
		{
			name:     "cluster not leader",
			err:      cluster.ErrNotLeader,
			status:   http.StatusServiceUnavailable,
			wantBody: `{"error":"service_unavailable","message":"service_unavailable"}`,
		},
		{
			name:     "cluster stopping",
			err:      cluster.ErrStopping,
			status:   http.StatusServiceUnavailable,
			wantBody: `{"error":"service_unavailable","message":"service_unavailable"}`,
		},
		{
			name:     "deadline exceeded",
			err:      context.DeadlineExceeded,
			status:   http.StatusServiceUnavailable,
			wantBody: `{"error":"service_unavailable","message":"service_unavailable"}`,
		},
		{
			name:     "internal",
			err:      errors.New("boom"),
			status:   http.StatusInternalServerError,
			wantBody: `{"error":"internal_error","message":"boom"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{promoteControllerVoterErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/controller-voter/promote", nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if tt.wantReason != "" {
				if !strings.Contains(rec.Body.String(), tt.wantReason) {
					t.Fatalf("body = %s, want reason %q", rec.Body.String(), tt.wantReason)
				}
				return
			}
			if !jsonEqual(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestPromoteControllerVoterRouteRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "bad path", path: "/manager/nodes/not-a-number/controller-voter/promote"},
		{name: "zero node", path: "/manager/nodes/0/controller-voter/promote"},
		{name: "malformed body", path: "/manager/nodes/4/controller-voter/promote", body: `{`},
		{name: "second json value", path: "/manager/nodes/4/controller-voter/promote", body: `{"expected_revision":12}{"expected_revision":13}`},
		{name: "trailing garbage", path: "/manager/nodes/4/controller-voter/promote", body: `{"expected_revision":12} garbage`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen managementusecase.PromoteControllerVoterRequest
			srv := New(Options{Management: managerNodesStub{
				promoteControllerVoterReqSink: &seen,
				promoteControllerVoterResponse: managementusecase.PromoteControllerVoterResponse{
					Changed:       true,
					NodeID:        4,
					StateRevision: 13,
				},
			}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if seen != (managementusecase.PromoteControllerVoterRequest{}) {
				t.Fatalf("usecase request = %#v, want no call", seen)
			}
		})
	}
}

func TestPromoteControllerVoterRouteRequiresManagement(t *testing.T) {
	srv := New(Options{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/controller-voter/promote", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"management not configured"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
