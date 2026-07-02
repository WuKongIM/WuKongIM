package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerChannelMigrationLeaderTransferCreatesTask(t *testing.T) {
	var gotReq managementusecase.LeaderTransferInput
	srv := New(Options{Management: managerNodesStub{
		lastChannelLeaderTransferRequest: &gotReq,
		channelMigrationSummary: managementusecase.ChannelMigrationSummary{
			TaskID:      "task-g1",
			ChannelID:   "g1",
			ChannelType: 1,
			Kind:        "leader_transfer",
			Status:      "pending",
			Phase:       "validate",
			SourceNode:  1,
			TargetNode:  2,
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channel-migrations/leader-transfer", strings.NewReader(`{"channel_id":"g1","channel_type":1,"target_node":2,"task_id":"op-1"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if gotReq.ChannelID != "g1" || gotReq.ChannelType != 1 || gotReq.TargetNode != 2 || gotReq.TaskID != "op-1" {
		t.Fatalf("request = %#v, want channel g1 type 1 target 2 task op-1", gotReq)
	}
	var body ChannelMigrationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if body.TaskID != "task-g1" || body.Kind != "leader_transfer" || body.Status != "pending" || body.Phase != "validate" {
		t.Fatalf("body = %#v, want migration summary", body)
	}
}

func TestManagerChannelMigrationDuplicateReturnsConflict(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{channelMigrationErr: managementusecase.ErrChannelMigrationConflict}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channel-migrations/leader-transfer", strings.NewReader(`{"channel_id":"g1","channel_type":1,"target_node":2}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestManagerActiveChannelMigrationsReadsScopedActiveTask(t *testing.T) {
	var gotReq managementusecase.ChannelMigrationListInput
	srv := New(Options{Management: managerNodesStub{
		lastChannelMigrationListRequest: &gotReq,
		channelMigrationSummary: managementusecase.ChannelMigrationSummary{
			TaskID:      "task-g1",
			ChannelID:   "g1",
			ChannelType: 2,
			Kind:        "replica_replace",
			Status:      "running",
			Phase:       "warm_catch_up",
			SourceNode:  1,
			TargetNode:  4,
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-migrations/active?channel_id=g1&channel_type=2&limit=50", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotReq.ChannelID != "g1" || gotReq.ChannelType != 2 || gotReq.Limit != 50 {
		t.Fatalf("request = %#v, want channel g1 type 2 limit 50", gotReq)
	}
	var body ChannelMigrationListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].TaskID != "task-g1" || body.Items[0].Kind != "replica_replace" {
		t.Fatalf("body = %#v, want scoped active task", body)
	}
}

func TestManagerChannelRuntimeMetaIncludesMigrationFields(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{
		channelRuntimeMeta: managementusecase.ListChannelRuntimeMetaResponse{
			Items: []managementusecase.ChannelRuntimeMeta{{
				ChannelID:         "g1",
				ChannelType:       1,
				SlotID:            9,
				Leader:            2,
				Replicas:          []uint64{1, 2, 3},
				ISR:               []uint64{1, 2},
				MinISR:            2,
				Status:            "active",
				WriteFenceToken:   "task-g1",
				WriteFenceVersion: 7,
				WriteFenceReason:  "leader_transfer",
				ActiveTaskID:      "task-g1",
				Degraded:          true,
				DegradedReason:    "isr_below_replicas",
			}},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body ChannelRuntimeMetaListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %#v, want one row", body.Items)
	}
	got := body.Items[0]
	if got.WriteFenceToken != "task-g1" ||
		got.WriteFenceVersion != 7 ||
		got.WriteFenceReason != "leader_transfer" ||
		got.ActiveTaskID != "task-g1" ||
		!got.Degraded ||
		got.DegradedReason != "isr_below_replicas" {
		t.Fatalf("runtime meta dto = %#v, want migration fields", got)
	}
}
