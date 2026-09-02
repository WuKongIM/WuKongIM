package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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

func TestManagerChannelMigrationReplicaReplaceAndAbortPreserveFencedIdentity(t *testing.T) {
	var replaceReq managementusecase.ReplicaReplaceInput
	var abortReq managementusecase.ChannelMigrationAbortInput
	summary := managementusecase.ChannelMigrationSummary{
		TaskID: "task-replace", ChannelID: "room-a", ChannelType: 2,
		Kind: "replica_replace", Status: "blocked", Phase: "warm_catch_up",
		SourceNode: 1, TargetNode: 4, DesiredLeader: 2,
		BlockerMessage: "target is catching up", LastError: "bounded failure",
	}
	srv := New(Options{Management: managerNodesStub{
		lastChannelReplicaReplaceRequest: &replaceReq,
		lastChannelMigrationAbortRequest: &abortReq,
		channelMigrationSummary:          summary,
	}})

	replace := httptest.NewRecorder()
	replaceRequest := httptest.NewRequest(http.MethodPost, "/manager/channel-migrations/replica-replace", strings.NewReader(`{
		"channel_id":"room-a","channel_type":2,"source_node":1,"target_node":4,"task_id":"replace-1"
	}`))
	replaceRequest.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(replace, replaceRequest)
	if replace.Code != http.StatusAccepted {
		t.Fatalf("replace status=%d body=%s", replace.Code, replace.Body.String())
	}
	if replaceReq.ChannelID != "room-a" || replaceReq.ChannelType != 2 || replaceReq.SourceNode != 1 ||
		replaceReq.TargetNode != 4 || replaceReq.TaskID != "replace-1" {
		t.Fatalf("replace request = %#v, want exact migration identity", replaceReq)
	}
	var replaceBody ChannelMigrationResponse
	if err := json.Unmarshal(replace.Body.Bytes(), &replaceBody); err != nil {
		t.Fatalf("decode replace response: %v", err)
	}
	if replaceBody.TaskID != "task-replace" || replaceBody.SourceNode != 1 || replaceBody.TargetNode != 4 ||
		replaceBody.DesiredLeader != 2 || replaceBody.BlockerMessage == "" || replaceBody.LastError == "" {
		t.Fatalf("replace response = %#v, want complete bounded state", replaceBody)
	}

	abort := httptest.NewRecorder()
	abortRequest := httptest.NewRequest(http.MethodPost, "/manager/channel-migrations/task-replace/abort", strings.NewReader(`{
		"channel_id":"room-a","channel_type":2,"reason":"operator rollback"
	}`))
	abortRequest.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(abort, abortRequest)
	if abort.Code != http.StatusOK {
		t.Fatalf("abort status=%d body=%s", abort.Code, abort.Body.String())
	}
	if abortReq.ChannelID != "room-a" || abortReq.ChannelType != 2 || abortReq.TaskID != "task-replace" || abortReq.Reason != "operator rollback" {
		t.Fatalf("abort request = %#v, want exact scoped task", abortReq)
	}
}

func TestManagerChannelMigrationLookupPreservesScopeAndStableErrors(t *testing.T) {
	var lookupReq managementusecase.ChannelMigrationLookupInput
	srv := New(Options{Management: managerNodesStub{
		lastChannelMigrationLookupRequest: &lookupReq,
		channelMigrationSummary: managementusecase.ChannelMigrationSummary{
			TaskID: "task-a", ChannelID: "room-a", ChannelType: 3,
			Kind: "leader_transfer", Status: "running", Phase: "switch_leader",
		},
	}})
	recorder := httptest.NewRecorder()
	srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/channel-migrations/task-a?channel_id=room-a&channel_type=3", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("lookup status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if lookupReq.TaskID != "task-a" || lookupReq.ChannelID != "room-a" || lookupReq.ChannelType != 3 {
		t.Fatalf("lookup request = %#v, want exact query and task scope", lookupReq)
	}

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: metadb.ErrInvalidArgument, want: http.StatusBadRequest},
		{name: "not found", err: managementusecase.ErrChannelMigrationNotFound, want: http.StatusNotFound},
		{name: "conflict", err: managementusecase.ErrChannelMigrationConflict, want: http.StatusConflict},
		{name: "not leader", err: cluster.ErrNotLeader, want: http.StatusServiceUnavailable},
		{name: "unavailable", err: managementusecase.ErrChannelMigrationUnavailable, want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := New(Options{Management: managerNodesStub{channelMigrationErr: test.err}})
			result := httptest.NewRecorder()
			server.Engine().ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/manager/channel-migrations/task-a?channel_id=room-a&channel_type=3", nil))
			if result.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", result.Code, test.want, result.Body.String())
			}
		})
	}
}

func TestManagerChannelMigrationRejectsIncompleteWritesBeforeManagement(t *testing.T) {
	var replaceReq managementusecase.ReplicaReplaceInput
	var abortReq managementusecase.ChannelMigrationAbortInput
	srv := New(Options{Management: managerNodesStub{
		lastChannelReplicaReplaceRequest: &replaceReq,
		lastChannelMigrationAbortRequest: &abortReq,
	}})
	cases := []struct {
		path string
		body string
	}{
		{path: "/manager/channel-migrations/replica-replace", body: `{"channel_id":"room-a","source_node":1}`},
		{path: "/manager/channel-migrations/replica-replace", body: `{"channel_id":"room-a","target_node":4}`},
		{path: "/manager/channel-migrations/task-a/abort", body: `{"channel_id":""}`},
		{path: "/manager/channel-migrations/task-a/abort", body: `{"channel_id":`},
	}
	for _, test := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		srv.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("path=%s body=%q status=%d response=%s", test.path, test.body, recorder.Code, recorder.Body.String())
		}
	}
	if replaceReq.ChannelID != "" || abortReq.ChannelID != "" {
		t.Fatalf("management received invalid requests: replace=%#v abort=%#v", replaceReq, abortReq)
	}
}

func TestManagerChannelMigrationWritesFailClosedWhenControlPlaneIsUnwired(t *testing.T) {
	srv := New(Options{})
	tests := []struct {
		path string
		body string
	}{
		{path: "/manager/channel-migrations/replica-replace", body: `{"channel_id":"room-a","channel_type":2,"source_node":1,"target_node":4}`},
		{path: "/manager/channel-migrations/task-a/abort", body: `{"channel_id":"room-a","channel_type":2}`},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		srv.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("path=%s status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
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
