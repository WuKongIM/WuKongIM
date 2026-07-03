package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerSlotsReturnsTaskProofFieldsInJSON(t *testing.T) {
	var seen managementusecase.ListSlotsOptions
	srv := New(Options{
		Management: managerNodesStub{
			lastSlotsOptions: &seen,
			slots: []managementusecase.Slot{{
				SlotID: 9,
				Task: &managementusecase.SlotTask{
					TaskID:              "slot-9-replica-move-7",
					Kind:                "slot_replica_move",
					Step:                "promote_learner",
					Status:              "running",
					TargetPeers:         []uint64{1, 2, 3},
					CompletionPolicy:    "all_target_peers",
					PhaseIndex:          2,
					ObservedConfigIndex: 77,
					ObservedVoters:      []uint64{1, 2, 3},
					ObservedLearners:    []uint64{4},
				},
			}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if seen != (managementusecase.ListSlotsOptions{}) {
		t.Fatalf("options = %#v, want zero-value list options", seen)
	}
	if !jsonEqual(rec.Body.String(), `{
		"total": 1,
		"items": [{
			"slot_id": 9,
			"state": {
				"quorum": "",
				"sync": "",
				"leader_match": false,
				"leader_transfer_pending": false
			},
			"assignment": {
				"desired_peers": null,
				"preferred_leader_id": 0,
				"config_epoch": 0,
				"balance_version": 0
			},
			"task": {
				"task_id": "slot-9-replica-move-7",
				"kind": "slot_replica_move",
				"step": "promote_learner",
				"status": "running",
				"target_peers": [1,2,3],
				"completion_policy": "all_target_peers",
				"attempt": 0,
				"phase_index": 2,
				"observed_config_index": 77,
				"observed_voters": [1,2,3],
				"observed_learners": [4]
			},
			"runtime": {
				"current_peers": null,
				"current_voters": null,
				"preferred_leader_id": 0,
				"healthy_voters": 0,
				"has_quorum": false,
				"observed_config_epoch": 0,
				"last_report_at": "0001-01-01T00:00:00Z"
			}
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerSlotsPreservesTaskProofFieldsInJSONAndClonesSlices(t *testing.T) {
	task := &managementusecase.SlotTask{
		TaskID:              "slot-9-replica-move-7",
		Kind:                "slot_replica_move",
		Step:                "promote_learner",
		Status:              "running",
		TargetPeers:         []uint64{1, 2, 3},
		PhaseIndex:          2,
		ObservedConfigIndex: 77,
		ObservedVoters:      []uint64{1, 2, 3},
		ObservedLearners:    []uint64{4},
	}

	dto := slotTaskDTO(task)
	body, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("Marshal SlotTaskDTO error = %v", err)
	}
	if !jsonEqual(string(body), `{
		"task_id": "slot-9-replica-move-7",
		"kind": "slot_replica_move",
		"step": "promote_learner",
		"status": "running",
		"target_peers": [1,2,3],
		"completion_policy": "",
		"attempt": 0,
		"phase_index": 2,
		"observed_config_index": 77,
		"observed_voters": [1,2,3],
		"observed_learners": [4]
	}`) {
		t.Fatalf("body = %s", string(body))
	}

	dto.ObservedVoters[0] = 99
	dto.ObservedLearners[0] = 99

	second := slotTaskDTO(task)
	if !sameUint64Slice(second.ObservedVoters, []uint64{1, 2, 3}) {
		t.Fatalf("ObservedVoters = %#v, want cloned [1 2 3]", second.ObservedVoters)
	}
	if !sameUint64Slice(second.ObservedLearners, []uint64{4}) {
		t.Fatalf("ObservedLearners = %#v, want cloned [4]", second.ObservedLearners)
	}
}

func sameUint64Slice(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
