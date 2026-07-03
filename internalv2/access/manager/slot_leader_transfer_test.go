package manager

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerSlotLeaderTransferReturnsAcceptedTask(t *testing.T) {
	generatedAt := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	var seen managementusecase.SlotLeaderTransferRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{
			slotLeaderTransferReqSink: &seen,
			slotLeaderTransferResponse: managementusecase.SlotLeaderTransferResponse{
				GeneratedAt:     generatedAt,
				SlotID:          1,
				TargetNode:      2,
				PreferredLeader: 1,
				ActualLeader:    1,
				Created:         true,
				Message:         managementusecase.SlotLeaderTransferMessageCreated,
				Task: &managementusecase.SlotTask{
					TaskID:           "slot-1-leader-transfer-7-r9",
					Kind:             "leader_transfer",
					Step:             "transfer_leader",
					Status:           "pending",
					SourceNode:       1,
					TargetNode:       2,
					TargetPeers:      []uint64{1, 2, 3},
					CompletionPolicy: "single_observer",
					ConfigEpoch:      7,
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/1/leader-transfer", strings.NewReader(`{"target_node":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if seen.SlotID != 1 || seen.TargetNode != 2 {
		t.Fatalf("request = %#v, want slot 1 target 2", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-19T12:00:00Z",
		"slot_id": 1,
		"target_node": 2,
		"preferred_leader": 1,
		"actual_leader": 1,
		"created": true,
		"message": "leader transfer task created",
		"task": {
			"task_id": "slot-1-leader-transfer-7-r9",
			"kind": "leader_transfer",
			"step": "transfer_leader",
			"status": "pending",
			"source_node": 1,
			"target_node": 2,
			"target_peers": [1,2,3],
			"completion_policy": "single_observer",
			"config_epoch": 7,
			"attempt": 0
		}
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerSlotLeaderTransferMapsErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid argument", err: metadb.ErrInvalidArgument, status: http.StatusBadRequest, code: "bad_request"},
		{name: "missing slot", err: managementusecase.ErrSlotLeaderTransferSlotNotFound, status: http.StatusNotFound, code: "not_found"},
		{name: "conflict", err: managementusecase.ErrSlotLeaderTransferConflict, status: http.StatusConflict, code: "conflict"},
		{name: "writer unavailable", err: managementusecase.ErrSlotLeaderTransferUnavailable, status: http.StatusServiceUnavailable, code: "service_unavailable"},
		{name: "runtime unavailable", err: managementusecase.ErrSlotRuntimeStatusUnavailable, status: http.StatusServiceUnavailable, code: "service_unavailable"},
		{name: "slot raft operator unavailable", err: managementusecase.ErrSlotRaftOperatorUnavailable, status: http.StatusServiceUnavailable, code: "service_unavailable"},
		{name: "not leader", err: cluster.ErrNotLeader, status: http.StatusServiceUnavailable, code: "service_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{slotLeaderTransferErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/manager/slots/1/leader-transfer", strings.NewReader(`{"target_node":2}`))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"`+tt.code+`","message":"`+tt.code+`"}`) {
				t.Fatalf("body = %s, want code %s", rec.Body.String(), tt.code)
			}
		})
	}

	srv := New(Options{Management: managerNodesStub{slotLeaderTransferErr: errors.New("unexpected")}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/1/leader-transfer", strings.NewReader(`{"target_node":2}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected error status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"error":"internal_error","message":"unexpected"}`) {
		t.Fatalf("body = %s, want internal_error", rec.Body.String())
	}
}

func TestManagerSlotLeaderTransferRequiresSlotWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots/1/leader-transfer", strings.NewReader(`{"target_node":2}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}
