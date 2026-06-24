package manager

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerJoinNodeRouteReturnsAccepted(t *testing.T) {
	var seen managementusecase.JoinNodeRequest
	srv := New(Options{
		Management: managerNodesStub{
			joinNodeReqSink: &seen,
			joinNodeResponse: managementusecase.JoinNodeResponse{
				Created:   true,
				NodeID:    4,
				Addr:      "10.0.0.4:11110",
				JoinState: "joining",
				Revision:  12,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/join", strings.NewReader(`{"node_id":4,"name":"node-4","addr":"10.0.0.4:11110","capacity_weight":3}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if seen.NodeID != 4 || seen.Name != "node-4" || seen.Addr != "10.0.0.4:11110" || seen.CapacityWeight != 3 {
		t.Fatalf("request = %#v, want parsed join body", seen)
	}
	if !jsonEqual(rec.Body.String(), `{"created":true,"node_id":4,"addr":"10.0.0.4:11110","join_state":"joining","revision":12}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerJoinNodeRouteReturnsOKForIdempotent(t *testing.T) {
	srv := New(Options{
		Management: managerNodesStub{
			joinNodeResponse: managementusecase.JoinNodeResponse{
				Created:   false,
				NodeID:    4,
				Addr:      "10.0.0.4:11110",
				JoinState: "joining",
				Revision:  12,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/join", strings.NewReader(`{"node_id":4,"addr":"10.0.0.4:11110"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestManagerActivateNodeRouteReturnsAccepted(t *testing.T) {
	var seen managementusecase.ActivateNodeRequest
	srv := New(Options{
		Management: managerNodesStub{
			activateNodeReqSink: &seen,
			activateNodeResponse: managementusecase.ActivateNodeResponse{
				Changed:   true,
				NodeID:    4,
				JoinState: "active",
				Revision:  13,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/activate", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if seen.NodeID != 4 {
		t.Fatalf("request = %#v, want node 4", seen)
	}
	if !jsonEqual(rec.Body.String(), `{"changed":true,"node_id":4,"join_state":"active","revision":13}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerJoinAndActivateNodeRoutesMapErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid argument", err: metadb.ErrInvalidArgument, status: http.StatusBadRequest, code: "bad_request"},
		{name: "lifecycle unavailable", err: managementusecase.ErrNodeLifecycleUnavailable, status: http.StatusServiceUnavailable, code: "service_unavailable"},
		{name: "not started", err: clusterv2.ErrNotStarted, status: http.StatusServiceUnavailable, code: "service_unavailable"},
		{name: "not leader", err: clusterv2.ErrNotLeader, status: http.StatusServiceUnavailable, code: "service_unavailable"},
		{name: "stopping", err: clusterv2.ErrStopping, status: http.StatusServiceUnavailable, code: "service_unavailable"},
		{name: "internal", err: errors.New("boom"), status: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, tt := range tests {
		t.Run("join "+tt.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{joinNodeErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/manager/nodes/join", strings.NewReader(`{"node_id":4,"addr":"10.0.0.4:11110"}`))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if tt.code == "internal_error" {
				if !jsonEqual(rec.Body.String(), `{"error":"internal_error","message":"boom"}`) {
					t.Fatalf("body = %s, want internal error", rec.Body.String())
				}
				return
			}
			if !jsonEqual(rec.Body.String(), `{"error":"`+tt.code+`","message":"`+tt.code+`"}`) {
				t.Fatalf("body = %s, want code %s", rec.Body.String(), tt.code)
			}
		})

		t.Run("activate "+tt.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{activateNodeErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/activate", nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
		})
	}
}

func TestManagerJoinNodeRouteRejectsBadRequests(t *testing.T) {
	for _, body := range []string{
		`{`,
		`{"node_id":0,"addr":"10.0.0.4:11110"}`,
		`{"node_id":4,"addr":"   "}`,
	} {
		srv := New(Options{Management: managerNodesStub{}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/nodes/join", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		srv.Engine().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, want %d; response=%s", body, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	}
}

func TestManagerNodeLifecycleRequiresNodeWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}, {
			Username: "writer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodPost, "/manager/nodes/join", strings.NewReader(`{"node_id":4,"addr":"10.0.0.4:11110"}`))
	deniedReq.Header.Set("Content-Type", "application/json")
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d; body=%s", denied.Code, http.StatusForbidden, denied.Body.String())
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/activate", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	if allowed.Code == http.StatusForbidden || allowed.Code == http.StatusUnauthorized {
		t.Fatalf("allowed status = %d, want route to pass auth; body=%s", allowed.Code, allowed.Body.String())
	}
}
