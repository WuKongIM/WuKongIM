package manager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/require"
)

func TestManagerNodeOnboardingPlanRoute(t *testing.T) {
	srv := New(Options{Management: managementStub{
		nodeOnboardingJob: sampleManagerNodeOnboardingJob(),
	}})
	reqBody := strings.NewReader(`{"target_node_id":4}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/node-onboarding/plan", reqBody)
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"planned"`)
	require.Contains(t, rec.Body.String(), `"pending":1`)
}

func TestManagerNodeOnboardingPlanIgnoresRetryLinkage(t *testing.T) {
	var gotReq managementusecase.CreateNodeOnboardingPlanRequest
	srv := New(Options{Management: managementStub{
		nodeOnboardingJob:         sampleManagerNodeOnboardingJob(),
		nodeOnboardingPlanReqSink: &gotReq,
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/node-onboarding/plan", strings.NewReader(`{"target_node_id":4,"retry_of_job_id":"job-forged"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(4), gotReq.TargetNodeID)
	require.Empty(t, gotReq.RetryOfJobID)
}

func TestManagerNodeOnboardingCandidatesRequiresNodeReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "slot-viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{nodeOnboardingCandidates: managementusecase.NodeOnboardingCandidatesResponse{
			Total: 1,
			Items: []managementusecase.NodeOnboardingCandidate{{NodeID: 4, Status: "alive"}},
		}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/node-onboarding/candidates", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "slot-viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerNodeOnboardingJobsMapsInvalidCursor(t *testing.T) {
	srv := New(Options{Management: managementStub{nodeOnboardingJobErr: raftcluster.ErrInvalidConfig}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/node-onboarding/jobs?cursor=bad", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid_request","message":"invalid request"}`, rec.Body.String())
}

func TestManagerNodeOnboardingStartMapsPlanStale(t *testing.T) {
	srv := New(Options{Management: managementStub{nodeOnboardingJobErr: raftcluster.ErrOnboardingPlanStale}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/node-onboarding/jobs/job-1/start", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.JSONEq(t, `{"error":"plan_stale","message":"plan stale"}`, rec.Body.String())
}

func sampleManagerNodeOnboardingJob() managementusecase.NodeOnboardingJobResponse {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	return managementusecase.NodeOnboardingJobResponse{Job: managementusecase.NodeOnboardingJob{
		JobID:        "onboard-20260426-000001",
		TargetNodeID: 4,
		Status:       "planned",
		CreatedAt:    now,
		UpdatedAt:    now,
		ResultCounts: managementusecase.NodeOnboardingResultCounts{Pending: 1},
		Plan: managementusecase.NodeOnboardingPlan{Moves: []managementusecase.NodeOnboardingPlanMove{{
			SlotID: 2, SourceNodeID: 1, TargetNodeID: 4,
		}}},
		Moves: []managementusecase.NodeOnboardingMove{{
			SlotID: 2, SourceNodeID: 1, TargetNodeID: 4, Status: "pending",
		}},
	}}
}
