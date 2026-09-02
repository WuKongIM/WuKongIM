package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerNodeOnboardingPlanReturnsPreview(t *testing.T) {
	generatedAt := time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)
	var seen managementusecase.NodeOnboardingPlanRequest
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
			nodeOnboardingPlanReqSink: &seen,
			nodeOnboardingPlan: managementusecase.NodeOnboardingPlanResponse{
				GeneratedAt:   generatedAt,
				StateRevision: 12,
				TargetNodeID:  4,
				MaxSlotMoves:  1,
				Candidates: []managementusecase.NodeOnboardingCandidate{{
					SlotID:       1,
					SourceNodeID: 1,
					TargetNodeID: 4,
					TargetPeers:  []uint64{4, 2, 3},
					ConfigEpoch:  7,
				}},
				Skipped: []managementusecase.NodeOnboardingSkip{},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/onboarding/plan", strings.NewReader(`{"max_slot_moves":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen.TargetNodeID != 4 || seen.MaxSlotMoves != 1 {
		t.Fatalf("request = %#v, want target 4 max 1", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-24T09:00:00Z",
		"state_revision": 12,
		"target_node_id": 4,
		"max_slot_moves": 1,
		"candidates": [{
			"slot_id": 1,
			"source_node_id": 1,
			"target_node_id": 4,
			"target_peers": [4,2,3],
			"config_epoch": 7
		}],
		"skipped": []
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerNodeOnboardingStartReturnsAcceptedWhenCreated(t *testing.T) {
	generatedAt := time.Date(2026, 6, 24, 9, 1, 0, 0, time.UTC)
	var seen managementusecase.NodeOnboardingStartRequest
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
			nodeOnboardingStartReqSink: &seen,
			nodeOnboardingStart: managementusecase.NodeOnboardingStartResponse{
				GeneratedAt:   generatedAt,
				StateRevision: 12,
				TargetNodeID:  4,
				MaxSlotMoves:  1,
				Created:       1,
				Results: []managementusecase.NodeOnboardingTaskResult{{
					SlotID:  1,
					Created: true,
					Task: &managementusecase.SlotTask{
						TaskID:      "slot-1-replica-move-1-to-4-r12",
						Kind:        "slot_replica_move",
						Step:        "open_learner",
						Status:      "pending",
						SourceNode:  1,
						TargetNode:  4,
						TargetPeers: []uint64{4, 2, 3},
						ConfigEpoch: 7,
					},
				}},
				Skipped: []managementusecase.NodeOnboardingSkip{},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/onboarding/start", strings.NewReader(`{"max_slot_moves":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if seen.TargetNodeID != 4 || seen.MaxSlotMoves != 1 {
		t.Fatalf("request = %#v, want target 4 max 1", seen)
	}
	if !strings.Contains(rec.Body.String(), `"created":1`) || !strings.Contains(rec.Body.String(), `"task_id":"slot-1-replica-move-1-to-4-r12"`) {
		t.Fatalf("body = %s, want created count and task id", rec.Body.String())
	}
}

func TestManagerNodeOnboardingStartMapsConflict(t *testing.T) {
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
			nodeOnboardingStartErr: managementusecase.ErrNodeOnboardingConflict,
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/onboarding/start", strings.NewReader(`{"max_slot_moves":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"error":"conflict"`) {
		t.Fatalf("body = %s, want conflict error", rec.Body.String())
	}
}

func TestManagerNodeOnboardingStartMapsClusterUnavailable(t *testing.T) {
	for _, err := range []error{
		cluster.ErrNotStarted,
		cluster.ErrNotLeader,
		cluster.ErrStopping,
	} {
		t.Run(err.Error(), func(t *testing.T) {
			srv := New(Options{
				Auth: testAuthConfig([]UserConfig{{
					Username: "admin",
					Password: "secret",
					Permissions: []PermissionConfig{{
						Resource: "cluster.node",
						Actions:  []string{"w"},
					}},
				}}),
				Management: managerNodesStub{nodeOnboardingStartErr: err},
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/onboarding/start", strings.NewReader(`{"max_slot_moves":1}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"service_unavailable"}`) {
				t.Fatalf("body = %s, want stable service_unavailable", rec.Body.String())
			}
		})
	}
}

func TestManagerNodeOnboardingStatusRequiresReadPermission(t *testing.T) {
	generatedAt := time.Date(2026, 6, 24, 9, 2, 0, 0, time.UTC)
	var seen managementusecase.NodeOnboardingStatusRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			nodeOnboardingStatusReqSink: &seen,
			nodeOnboardingStatus: managementusecase.NodeOnboardingStatusResponse{
				GeneratedAt:   generatedAt,
				StateRevision: 12,
				TargetNodeID:  4,
				Summary: managementusecase.NodeOnboardingStatusSummary{
					TotalActive: 2,
					Pending:     1,
					Running:     1,
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/4/onboarding/status", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen.TargetNodeID != 4 {
		t.Fatalf("request = %#v, want target 4", seen)
	}
	if !strings.Contains(rec.Body.String(), `"total_active":2`) || !strings.Contains(rec.Body.String(), `"running":1`) {
		t.Fatalf("body = %s, want status summary", rec.Body.String())
	}
}

func TestManagerNodeOnboardingStartRequiresWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/onboarding/start", strings.NewReader(`{"max_slot_moves":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestManagerNodeOnboardingAdvancePreservesBoundAndAcceptedSemantics(t *testing.T) {
	var seen managementusecase.NodeOnboardingAdvanceRequest
	srv := New(Options{Management: managerNodesStub{
		nodeOnboardingAdvanceReqSink: &seen,
		nodeOnboardingAdvance: managementusecase.NodeOnboardingStartResponse{
			StateRevision: 18, TargetNodeID: 4, MaxSlotMoves: 3, Created: 1,
			Results: []managementusecase.NodeOnboardingTaskResult{{SlotID: 7, Created: true}},
			Skipped: []managementusecase.NodeOnboardingSkip{{SlotID: 8, Reason: "already_balanced", Message: "target already hosts replica"}},
		},
	}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/manager/nodes/4/onboarding/advance", strings.NewReader(`{"max_slot_moves":3}`))
	request.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if seen.TargetNodeID != 4 || seen.MaxSlotMoves != 3 || !strings.Contains(recorder.Body.String(), `"state_revision":18`) ||
		!strings.Contains(recorder.Body.String(), `"already_balanced"`) {
		t.Fatalf("request=%#v body=%s", seen, recorder.Body.String())
	}

	noop := New(Options{Management: managerNodesStub{nodeOnboardingAdvance: managementusecase.NodeOnboardingStartResponse{TargetNodeID: 4}}})
	recorder = httptest.NewRecorder()
	noop.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/manager/nodes/4/onboarding/advance", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("noop status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestManagerNodeOnboardingAdvanceMapsStableAdmissionFailures(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: metadb.ErrInvalidArgument, want: http.StatusBadRequest},
		{err: managementusecase.ErrNodeOnboardingTargetNotActive, want: http.StatusConflict},
		{err: managementusecase.ErrNodeOnboardingUnavailable, want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		srv := New(Options{Management: managerNodesStub{nodeOnboardingAdvanceErr: test.err}})
		recorder := httptest.NewRecorder()
		srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/manager/nodes/4/onboarding/advance", nil))
		if recorder.Code != test.want {
			t.Fatalf("error=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
	}

	for _, path := range []string{"/manager/nodes/0/onboarding/advance", "/manager/nodes/bad/onboarding/advance"} {
		srv := New(Options{Management: managerNodesStub{}})
		recorder := httptest.NewRecorder()
		srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}
