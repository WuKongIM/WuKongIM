package manager

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

func TestManagerScaleInRemoveRequiresWritePermission(t *testing.T) {
	var seen managementusecase.MarkNodeRemovedRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{
				Username: "reader",
				Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			},
			{
				Username: "admin",
				Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"w"},
				}},
			},
		}),
		Management: managerNodesStub{
			markNodeRemovedReqSink: &seen,
			markNodeRemoved: managementusecase.MarkNodeRemovedResponse{
				Changed:   true,
				NodeID:    4,
				JoinState: "removed",
				Revision:  33,
			},
		},
	})

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/remove", nil)
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d body=%s, want %d", denied.Code, denied.Body.String(), http.StatusForbidden)
	}
	if seen.NodeID != 0 {
		t.Fatalf("denied request reached management: %#v", seen)
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/remove", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusAccepted {
		t.Fatalf("allowed status = %d body=%s, want %d", allowed.Code, allowed.Body.String(), http.StatusAccepted)
	}
	if seen.NodeID != 4 {
		t.Fatalf("request = %#v, want node 4", seen)
	}
}

func TestManagerScaleInRemoveBlocksUnsafeStatus(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{markNodeRemovedErr: managementusecase.ErrNodeScaleInUnsafe},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/remove", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusConflict)
	}
	if !jsonEqual(rec.Body.String(), `{"error":"conflict","message":"conflict"}`) {
		t.Fatalf("body = %s, want conflict", rec.Body.String())
	}
}

func TestManagerScaleInRemoveReturnsAcceptedWhenChanged(t *testing.T) {
	var seen managementusecase.MarkNodeRemovedRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			markNodeRemovedReqSink: &seen,
			markNodeRemoved: managementusecase.MarkNodeRemovedResponse{
				Changed:   true,
				NodeID:    4,
				JoinState: "removed",
				Revision:  33,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/remove", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusAccepted)
	}
	if seen.NodeID != 4 {
		t.Fatalf("request = %#v, want node 4", seen)
	}
	if !jsonEqual(rec.Body.String(), `{"changed":true,"node_id":4,"join_state":"removed","revision":33}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerScaleInRemoveReturnsOKWhenAlreadyRemoved(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			markNodeRemoved: managementusecase.MarkNodeRemovedResponse{
				Changed:   false,
				NodeID:    4,
				JoinState: "removed",
				Revision:  33,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/remove", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}
	if !jsonEqual(rec.Body.String(), `{"changed":false,"node_id":4,"join_state":"removed","revision":33}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerScaleInRemoveMapsErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{
			name:   "missing",
			err:    managementusecase.ErrNodeLifecycleNotFound,
			status: http.StatusNotFound,
			body:   `{"error":"not_found","message":"not_found"}`,
		},
		{
			name:   "lifecycle unavailable",
			err:    managementusecase.ErrNodeLifecycleUnavailable,
			status: http.StatusServiceUnavailable,
			body:   `{"error":"service_unavailable","message":"service_unavailable"}`,
		},
		{
			name:   "cluster unavailable",
			err:    clusterv2.ErrNotLeader,
			status: http.StatusServiceUnavailable,
			body:   `{"error":"service_unavailable","message":"service_unavailable"}`,
		},
		{
			name:   "unexpected",
			err:    errors.New("boom"),
			status: http.StatusInternalServerError,
			body:   `{"error":"internal_error","message":"boom"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{
				Auth: testAuthConfig([]UserConfig{{
					Username: "admin",
					Password: "secret",
					Permissions: []PermissionConfig{{
						Resource: "cluster.node",
						Actions:  []string{"w"},
					}},
				}}),
				Management: managerNodesStub{markNodeRemovedErr: tt.err},
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/scale-in/remove", nil)
			req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.body)
			}
		})
	}
}
