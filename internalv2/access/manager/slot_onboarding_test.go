package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
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
