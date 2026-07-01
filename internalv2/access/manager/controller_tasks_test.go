package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerControllerTasksReturnsFilteredList(t *testing.T) {
	var seen managementusecase.ListControllerTasksRequest
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
			controllerTasksReqSink: &seen,
			controllerTasksResponse: managementusecase.ListControllerTasksResponse{
				Total: 1,
				Items: []managementusecase.ControllerTask{{
					TaskID:              "slot-1-leader-transfer-7-r9",
					SlotID:              1,
					Kind:                "leader_transfer",
					Step:                "transfer_leader",
					Status:              "pending",
					SourceNode:          1,
					TargetNode:          2,
					TargetPeers:         []uint64{1, 2, 3},
					CompletionPolicy:    "single_observer",
					ConfigEpoch:         7,
					Attempt:             1,
					LastError:           "not leader",
					PhaseIndex:          2,
					ObservedConfigIndex: 55,
					ObservedVoters:      []uint64{1, 2, 3},
					ObservedLearners:    []uint64{4},
					Participants: []managementusecase.ControllerTaskParticipant{
						{NodeID: 2, Attempt: 1, Status: "failed", LastError: "not leader"},
					},
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks?kind=leader_transfer&status=pending&slot_id=1&node_id=2&limit=20", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen != (managementusecase.ListControllerTasksRequest{Kind: "leader_transfer", Status: "pending", SlotID: 1, NodeID: 2, Limit: 20}) {
		t.Fatalf("request = %#v, want parsed filters", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"total": 1,
		"items": [{
			"task_id": "slot-1-leader-transfer-7-r9",
			"slot_id": 1,
			"kind": "leader_transfer",
			"step": "transfer_leader",
			"status": "pending",
			"source_node": 1,
			"target_node": 2,
			"target_peers": [1,2,3],
			"completion_policy": "single_observer",
			"config_epoch": 7,
			"attempt": 1,
			"last_error": "not leader",
			"phase_index": 2,
			"observed_config_index": 55,
			"observed_voters": [1,2,3],
			"observed_learners": [4],
			"participants": [{
				"node_id": 2,
				"attempt": 1,
				"status": "failed",
				"last_error": "not leader"
			}]
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerControllerTaskReturnsDetail(t *testing.T) {
	var seen string
	srv := New(Options{Management: managerNodesStub{
		controllerTaskIDSink: &seen,
		controllerTask: managementusecase.ControllerTask{
			TaskID:              "slot-1-bootstrap-1",
			SlotID:              1,
			Kind:                "bootstrap",
			Step:                "create_slot",
			Status:              "running",
			TargetNode:          1,
			TargetPeers:         []uint64{1, 2, 3},
			CompletionPolicy:    "all_target_peers",
			ConfigEpoch:         1,
			PhaseIndex:          3,
			ObservedConfigIndex: 66,
			ObservedVoters:      []uint64{1, 2, 3},
			ObservedLearners:    []uint64{4},
			Participants:        []managementusecase.ControllerTaskParticipant{},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks/slot-1-bootstrap-1", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen != "slot-1-bootstrap-1" {
		t.Fatalf("task id = %q, want slot-1-bootstrap-1", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"task_id": "slot-1-bootstrap-1",
		"slot_id": 1,
		"kind": "bootstrap",
		"step": "create_slot",
		"status": "running",
		"source_node": 0,
		"target_node": 1,
		"target_peers": [1,2,3],
		"completion_policy": "all_target_peers",
		"config_epoch": 1,
		"attempt": 0,
		"last_error": "",
		"phase_index": 3,
		"observed_config_index": 66,
		"observed_voters": [1,2,3],
		"observed_learners": [4],
		"participants": []
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerControllerTaskDTOClonesProofSlices(t *testing.T) {
	task := managementusecase.ControllerTask{
		TaskID:              "slot-9-replica-move-7",
		SlotID:              9,
		Kind:                "slot_replica_move",
		Step:                "promote_learner",
		Status:              "running",
		ObservedVoters:      []uint64{1, 2, 3},
		ObservedLearners:    []uint64{4},
		ObservedConfigIndex: 77,
		PhaseIndex:          2,
	}

	first := controllerTaskDTO(task)
	first.ObservedVoters[0] = 99
	first.ObservedLearners[0] = 99

	second := controllerTaskDTO(task)
	if !sameUint64Slice(second.ObservedVoters, []uint64{1, 2, 3}) {
		t.Fatalf("ObservedVoters = %#v, want cloned [1 2 3]", second.ObservedVoters)
	}
	if !sameUint64Slice(second.ObservedLearners, []uint64{4}) {
		t.Fatalf("ObservedLearners = %#v, want cloned [4]", second.ObservedLearners)
	}
}

func TestManagerControllerTasksRejectInvalidQueries(t *testing.T) {
	tests := []string{
		"/manager/controller/tasks?kind=rebalance",
		"/manager/controller/tasks?status=done",
		"/manager/controller/tasks?slot_id=0",
		"/manager/controller/tasks?slot_id=bad",
		"/manager/controller/tasks?node_id=0",
		"/manager/controller/tasks?node_id=bad",
		"/manager/controller/tasks?limit=-1",
		"/manager/controller/tasks?limit=bad",
		"/manager/controller/tasks?limit=501",
	}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, target, nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), `{"error":"bad_request","message":"invalid controller task query"}`) {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestManagerControllerTaskKindAcceptsSlotReplicaMove(t *testing.T) {
	var seen managementusecase.ListControllerTasksRequest
	srv := New(Options{Management: managerNodesStub{controllerTasksReqSink: &seen}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks?kind=slot_replica_move", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen.Kind != "slot_replica_move" {
		t.Fatalf("kind = %q, want slot_replica_move", seen.Kind)
	}
}

func TestManagerControllerTaskMapsErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
		body   string
	}{
		{name: "invalid", err: metadb.ErrInvalidArgument, status: http.StatusBadRequest, code: "bad_request", body: `{"error":"bad_request","message":"invalid controller task request"}`},
		{name: "missing", err: managementusecase.ErrControllerTaskNotFound, status: http.StatusNotFound, code: "not_found", body: `{"error":"not_found","message":"controller task not found"}`},
		{name: "snapshot unavailable", err: clusterv2.ErrNotStarted, status: http.StatusServiceUnavailable, code: "service_unavailable", body: `{"error":"service_unavailable","message":"controller task read unavailable"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{controllerTaskErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks/missing", nil)

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

func TestManagerControllerTasksMapsListSnapshotUnavailable(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{controllerTasksErr: clusterv2.ErrNotStarted}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"controller task read unavailable"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerControllerTasksRequireControllerReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "writer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.controller",
				Actions:  []string{"w"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/controller/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}
