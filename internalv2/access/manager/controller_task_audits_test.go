package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerControllerTaskAuditsReturnsFilteredList(t *testing.T) {
	var seen managementusecase.ControllerTaskAuditListRequest
	started := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			controllerTaskAuditsReqSink: &seen,
			controllerTaskAuditsResponse: managementusecase.ControllerTaskAuditListResponse{
				Total:     1,
				Limit:     20,
				Truncated: false,
				Items: []managementusecase.ControllerTaskAuditSnapshot{{
					TaskID:                "slot-1-replica-move-2-to-4-r9",
					Kind:                  "slot_replica_move",
					Status:                "failed",
					Step:                  "remove_voter",
					SlotID:                1,
					SourceNode:            2,
					TargetNode:            4,
					FirstAppliedRaftIndex: 10,
					LastAppliedRaftIndex:  12,
					StartedAt:             started,
					EventCount:            3,
					LastReason:            "not caught up",
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/task-audits?kind=slot_replica_move&status=failed&slot_id=1&node_id=4&keyword=caught&limit=20", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen != (managementusecase.ControllerTaskAuditListRequest{Kind: "slot_replica_move", Status: "failed", SlotID: 1, NodeID: 4, Keyword: "caught", Limit: 20}) {
		t.Fatalf("request = %#v, want parsed filters", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"total": 1,
		"limit": 20,
		"truncated": false,
		"items": [{
			"task_id": "slot-1-replica-move-2-to-4-r9",
			"kind": "slot_replica_move",
			"status": "failed",
			"step": "remove_voter",
			"slot_id": 1,
			"leader_id": 0,
			"source_node": 2,
			"target_node": 4,
			"first_applied_raft_index": 10,
			"last_applied_raft_index": 12,
			"started_at": "2026-06-29T10:00:00Z",
			"completed_at": null,
			"event_count": 3,
			"truncated": false,
			"summary": "",
			"last_reason": "not caught up"
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerControllerTaskAuditEventsReturnsTimeline(t *testing.T) {
	var seen string
	at := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	srv := New(Options{Management: managerNodesStub{
		controllerTaskAuditEventsIDSink: &seen,
		controllerTaskAuditEventsResponse: managementusecase.ControllerTaskAuditEventsResponse{
			Task: managementusecase.ControllerTaskAuditSnapshot{TaskID: "task-a", EventCount: 2},
			Events: []managementusecase.ControllerTaskAuditEvent{
				{EventID: "event-1", TaskID: "task-a", Type: "created", AppliedRaftIndex: 1, OccurredAt: at},
				{EventID: "event-2", TaskID: "task-a", Type: "completed", AppliedRaftIndex: 2, OccurredAt: at.Add(time.Second)},
			},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/task-audits/task-a/events", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen != "task-a" {
		t.Fatalf("task id = %q, want task-a", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"task": {
			"task_id": "task-a",
			"kind": "",
			"status": "",
			"step": "",
			"slot_id": 0,
			"leader_id": 0,
			"source_node": 0,
			"target_node": 0,
			"first_applied_raft_index": 0,
			"last_applied_raft_index": 0,
			"started_at": null,
			"completed_at": null,
			"event_count": 2,
			"truncated": false,
			"summary": "",
			"last_reason": ""
		},
		"events": [{
			"event_id": "event-1",
			"task_id": "task-a",
			"type": "created",
			"kind": "",
			"status": "",
			"slot_id": 0,
			"leader_id": 0,
			"source_node": 0,
			"target_node": 0,
			"applied_raft_index": 1,
			"applied_raft_term": 0,
			"command_kind": "",
			"participant_node": 0,
			"occurred_at": "2026-06-29T10:00:00Z",
			"summary": "",
			"reason": ""
		}, {
			"event_id": "event-2",
			"task_id": "task-a",
			"type": "completed",
			"kind": "",
			"status": "",
			"slot_id": 0,
			"leader_id": 0,
			"source_node": 0,
			"target_node": 0,
			"applied_raft_index": 2,
			"applied_raft_term": 0,
			"command_kind": "",
			"participant_node": 0,
			"occurred_at": "2026-06-29T10:00:01Z",
			"summary": "",
			"reason": ""
		}],
		"truncated": false
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerControllerTaskAuditUnavailableMapsToHTTP503(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{controllerTaskAuditEventsErr: managementusecase.ErrControllerTaskAuditUnavailable}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/task-audits/task-a/events", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"controller task audit unavailable"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}
