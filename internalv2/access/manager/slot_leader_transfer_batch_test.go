package manager

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerSlotLeaderTransferBatchPlanReturnsPreview(t *testing.T) {
	generatedAt := time.Date(2026, 6, 20, 9, 30, 0, 0, time.UTC)
	var seen managementusecase.SlotLeaderTransferBatchPlanRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			slotLeaderTransferBatchPlanSink: &seen,
			slotLeaderTransferBatchPlan: managementusecase.SlotLeaderTransferBatchPlanResponse{
				GeneratedAt:   generatedAt,
				StateRevision: 22,
				PlanID:        "plan-22",
				SourceNodeID:  1,
				TargetPolicy:  managementusecase.SlotLeaderTransferTargetPolicyLeastLeaders,
				MaxTasks:      4,
				Summary: managementusecase.SlotLeaderTransferBatchPlanSummary{
					Scanned:       3,
					Candidates:    1,
					Skipped:       1,
					ExistingTasks: 0,
					WouldCreate:   1,
				},
				Candidates: []managementusecase.SlotLeaderTransferBatchCandidate{{
					SlotID:          1,
					SourceNodeID:    1,
					TargetNodeID:    2,
					PreferredLeader: 1,
					ActualLeader:    1,
					DesiredPeers:    []uint64{1, 2, 3},
					CurrentVoters:   []uint64{1, 2, 3},
					ConfigEpoch:     7,
					ExistingTaskID:  "",
					Action:          managementusecase.SlotLeaderTransferBatchActionCreate,
				}},
				Skipped: []managementusecase.SlotLeaderTransferBatchSkip{{
					SlotID:  2,
					Reason:  managementusecase.SlotLeaderTransferBatchSkipAlreadyOnTarget,
					Message: "slot is already led by target node",
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-plan", strings.NewReader(`{
		"source_node_id": 1,
		"target_node_id": 2,
		"slot_ids": [1,2],
		"max_tasks": 4,
		"target_policy": "least_leaders"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen.SourceNodeID != 1 || seen.TargetNodeID != 2 || seen.MaxTasks != 4 || seen.TargetPolicy != managementusecase.SlotLeaderTransferTargetPolicyLeastLeaders {
		t.Fatalf("request = %#v, want parsed plan request", seen)
	}
	if len(seen.SlotIDs) != 2 || seen.SlotIDs[0] != 1 || seen.SlotIDs[1] != 2 {
		t.Fatalf("slot_ids = %#v, want [1 2]", seen.SlotIDs)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-20T09:30:00Z",
		"state_revision": 22,
		"plan_id": "plan-22",
		"source_node_id": 1,
		"target_policy": "least_leaders",
		"max_tasks": 4,
		"summary": {
			"scanned": 3,
			"candidates": 1,
			"skipped": 1,
			"existing_tasks": 0,
			"would_create": 1
		},
		"candidates": [{
			"slot_id": 1,
			"source_node_id": 1,
			"target_node_id": 2,
			"preferred_leader": 1,
			"actual_leader": 1,
			"desired_peers": [1,2,3],
			"current_voters": [1,2,3],
			"config_epoch": 7,
			"existing_task_id": "",
			"action": "create"
		}],
		"skipped": [{
			"slot_id": 2,
			"reason": "already_on_target",
			"message": "slot is already led by target node"
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerSlotLeaderTransferBatchExecuteReturnsAcceptedWhenCreated(t *testing.T) {
	generatedAt := time.Date(2026, 6, 20, 9, 35, 0, 0, time.UTC)
	var seen managementusecase.SlotLeaderTransferBatchExecuteRequest
	srv := New(Options{Management: managerNodesStub{
		slotLeaderTransferBatchExecuteSink: &seen,
		slotLeaderTransferBatchExecute: managementusecase.SlotLeaderTransferBatchExecuteResponse{
			GeneratedAt:   generatedAt,
			StateRevision: 22,
			PlanID:        "plan-22",
			Summary: managementusecase.SlotLeaderTransferBatchExecuteSummary{
				Requested: 1,
				Created:   1,
			},
			Results: []managementusecase.SlotLeaderTransferBatchExecuteResult{{
				SlotID:       1,
				TargetNodeID: 2,
				Status:       managementusecase.SlotLeaderTransferBatchResultCreated,
				TaskID:       "slot-1-leader-transfer-7-r9",
				Message:      managementusecase.SlotLeaderTransferMessageCreated,
			}},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-batch", strings.NewReader(`{
		"source_node_id": 1,
		"target_node_id": 2,
		"slot_ids": [1],
		"max_tasks": 1,
		"target_policy": "least_leaders",
		"state_revision": 22,
		"plan_id": "plan-22"
	}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if seen.StateRevision != 22 || seen.PlanID != "plan-22" || len(seen.SlotIDs) != 1 || seen.SlotIDs[0] != 1 {
		t.Fatalf("request = %#v, want parsed execute fence", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-20T09:35:00Z",
		"state_revision": 22,
		"plan_id": "plan-22",
		"summary": {
			"requested": 1,
			"created": 1,
			"existing": 0,
			"already_leader": 0,
			"skipped": 0,
			"failed": 0
		},
		"results": [{
			"slot_id": 1,
			"target_node_id": 2,
			"status": "created",
			"task_id": "slot-1-leader-transfer-7-r9",
			"message": "leader transfer task created"
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerSlotLeaderTransferBatchExecuteReturnsOKWhenNoCreates(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{
		slotLeaderTransferBatchExecute: managementusecase.SlotLeaderTransferBatchExecuteResponse{
			Summary: managementusecase.SlotLeaderTransferBatchExecuteSummary{
				Requested: 1,
				Existing:  1,
			},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-batch", strings.NewReader(`{"source_node_id":1,"target_node_id":2,"state_revision":22,"plan_id":"plan-22"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestManagerSlotLeaderTransferBatchMapsErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{name: "invalid", err: metadb.ErrInvalidArgument, status: http.StatusBadRequest, body: `{"error":"bad_request","message":"bad_request"}`},
		{name: "stale", err: managementusecase.ErrSlotLeaderTransferPlanStale, status: http.StatusConflict, body: `{"error":"conflict","message":"conflict"}`},
		{name: "mismatch", err: managementusecase.ErrSlotLeaderTransferPlanMismatch, status: http.StatusConflict, body: `{"error":"conflict","message":"conflict"}`},
		{name: "conflict", err: managementusecase.ErrSlotLeaderTransferConflict, status: http.StatusConflict, body: `{"error":"conflict","message":"conflict"}`},
		{name: "unavailable", err: managementusecase.ErrSlotLeaderTransferUnavailable, status: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"service_unavailable"}`},
		{name: "runtime unavailable", err: managementusecase.ErrSlotRuntimeStatusUnavailable, status: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"service_unavailable"}`},
		{name: "operator unavailable", err: managementusecase.ErrSlotRaftOperatorUnavailable, status: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"service_unavailable"}`},
		{name: "slot not found", err: clusterv2.ErrSlotNotFound, status: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"service_unavailable"}`},
		{name: "not started", err: clusterv2.ErrNotStarted, status: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"service_unavailable"}`},
		{name: "not leader", err: clusterv2.ErrNotLeader, status: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"service_unavailable"}`},
		{name: "stopping", err: clusterv2.ErrStopping, status: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"service_unavailable"}`},
		{name: "unexpected", err: errors.New("unexpected"), status: http.StatusInternalServerError, body: `{"error":"internal_error","message":"unexpected"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{slotLeaderTransferBatchPlanErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-plan", strings.NewReader(`{"source_node_id":1}`))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.body)
			}
		})
	}
}

func TestManagerSlotLeaderTransferBatchPermissions(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}, {
			Username: "writer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			slotLeaderTransferBatchExecute: managementusecase.SlotLeaderTransferBatchExecuteResponse{
				Summary: managementusecase.SlotLeaderTransferBatchExecuteSummary{Created: 1},
			},
		},
	})

	plan := httptest.NewRecorder()
	planReq := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-plan", strings.NewReader(`{"source_node_id":1}`))
	planReq.Header.Set("Content-Type", "application/json")
	planReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(plan, planReq)
	if plan.Code != http.StatusOK {
		t.Fatalf("plan status = %d, want %d; body=%s", plan.Code, http.StatusOK, plan.Body.String())
	}

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-batch", strings.NewReader(`{"source_node_id":1,"state_revision":1,"plan_id":"plan"}`))
	deniedReq.Header.Set("Content-Type", "application/json")
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("execute reader status = %d, want %d; body=%s", denied.Code, http.StatusForbidden, denied.Body.String())
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodPost, "/manager/slots/leader-transfer-batch", strings.NewReader(`{"source_node_id":1,"state_revision":1,"plan_id":"plan"}`))
	allowedReq.Header.Set("Content-Type", "application/json")
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusAccepted {
		t.Fatalf("execute writer status = %d, want %d; body=%s", allowed.Code, http.StatusAccepted, allowed.Body.String())
	}
}

func TestManagerSlotLeaderTransferBatchRejectsInvalidJSON(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})

	for _, path := range []string{"/manager/slots/leader-transfer-plan", "/manager/slots/leader-transfer-batch"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"source_node_id":`))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"bad_request","message":"bad_request"}`) {
				t.Fatalf("body = %s, want bad_request", rec.Body.String())
			}
		})
	}
}
