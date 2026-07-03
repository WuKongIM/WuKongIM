package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	"go.etcd.io/raft/v3/raftpb"
)

func TestControlRaftBatchCodecRoundTrip(t *testing.T) {
	input := []raftpb.Message{{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 3}}
	payload, err := EncodeRaftBatch(input)
	if err != nil {
		t.Fatalf("EncodeRaftBatch() error = %v", err)
	}
	got, err := DecodeRaftBatch(payload)
	if err != nil {
		t.Fatalf("DecodeRaftBatch() error = %v", err)
	}
	if len(got) != 1 || got[0].From != 1 || got[0].To != 2 || got[0].Type != raftpb.MsgHeartbeat || got[0].Term != 3 {
		t.Fatalf("DecodeRaftBatch() = %#v", got)
	}
}

func TestControlSyncCodecRoundTrip(t *testing.T) {
	req := controller.GetStateRequest{ClusterID: "cluster-a", LocalRevision: 7, LocalChecksum: "crc32c:abcd"}
	payload, err := EncodeStateSyncRequest(req)
	if err != nil {
		t.Fatalf("EncodeStateSyncRequest() error = %v", err)
	}
	got, err := DecodeStateSyncRequest(payload)
	if err != nil {
		t.Fatalf("DecodeStateSyncRequest() error = %v", err)
	}
	if got != req {
		t.Fatalf("DecodeStateSyncRequest() = %#v, want %#v", got, req)
	}

	resp := controller.GetStateResponse{LeaderID: 1, Revision: 8, Checksum: "crc32c:1234", Payload: []byte(`{"revision":8}`)}
	encoded, err := EncodeStateSyncResponse(resp)
	if err != nil {
		t.Fatalf("EncodeStateSyncResponse() error = %v", err)
	}
	decoded, err := DecodeStateSyncResponse(encoded)
	if err != nil {
		t.Fatalf("DecodeStateSyncResponse() error = %v", err)
	}
	if decoded.LeaderID != resp.LeaderID || decoded.Revision != resp.Revision || decoded.Checksum != resp.Checksum || string(decoded.Payload) != string(resp.Payload) {
		t.Fatalf("DecodeStateSyncResponse() = %#v, want %#v", decoded, resp)
	}
}

func TestControlTaskRequestCodecRoundTrip(t *testing.T) {
	requests := []TaskRequest{
		{
			Action: TaskActionProgress,
			Progress: controller.TaskProgress{
				TaskID:             "slot-1-bootstrap-1",
				SlotID:             1,
				TaskKind:           controller.TaskKindBootstrap,
				ConfigEpoch:        1,
				TaskAttempt:        0,
				ParticipantNodeID:  2,
				ParticipantAttempt: 0,
				Status:             controller.TaskParticipantStatusDone,
			},
		},
		{
			Action: TaskActionLeaderTransfer,
			LeaderTransfer: SlotLeaderTransferRequest{
				SlotID:        1,
				SourceNode:    1,
				TargetNode:    2,
				TargetPeers:   []uint64{1, 2, 3},
				ConfigEpoch:   7,
				StateRevision: 9,
			},
		},
	}
	for _, req := range requests {
		payload, err := EncodeTaskRequest(req)
		if err != nil {
			t.Fatalf("EncodeTaskRequest() error = %v", err)
		}
		got, err := DecodeTaskRequest(payload)
		if err != nil {
			t.Fatalf("DecodeTaskRequest() error = %v", err)
		}
		if !reflect.DeepEqual(got, req) {
			t.Fatalf("DecodeTaskRequest() = %#v, want %#v", got, req)
		}
	}
}

func TestControlWriteRequestCodecRoundTripSlotReplicaMove(t *testing.T) {
	requests := []ControlWriteRequest{
		{
			Action: ControlWriteActionJoinNode,
			JoinNode: JoinNodeRequest{
				NodeID:         4,
				Name:           "node-4",
				Addr:           "127.0.0.1:10004",
				Roles:          []Role{RoleData},
				CapacityWeight: 2,
			},
		},
		{
			Action: ControlWriteActionSlotReplicaMove,
			SlotReplicaMove: SlotReplicaMoveRequest{
				SlotID:        1,
				SourceNode:    1,
				TargetNode:    4,
				TargetPeers:   []uint64{4, 2, 3},
				ConfigEpoch:   7,
				StateRevision: 9,
			},
		},
		{
			Action:          ControlWriteActionMarkNodeLeaving,
			MarkNodeLeaving: MarkNodeLeavingRequest{NodeID: 4},
		},
		{
			Action:          ControlWriteActionMarkNodeRemoved,
			MarkNodeRemoved: MarkNodeRemovedRequest{NodeID: 4, StateRevision: 9},
		},
		{
			Action: ControlWriteActionReportNodeHealth,
			ReportNodeHealth: NodeReport{
				NodeID:                  1,
				Status:                  NodeAlive,
				RuntimeReady:            true,
				ObservedControlRevision: 9,
				ObservedSlotRevision:    12,
				ReportSeq:               3,
				ErrorCode:               "warming",
			},
		},
	}
	for _, req := range requests {
		payload, err := EncodeControlWriteRequest(req)
		if err != nil {
			t.Fatalf("EncodeControlWriteRequest() error = %v", err)
		}
		got, err := DecodeControlWriteRequest(payload)
		if err != nil {
			t.Fatalf("DecodeControlWriteRequest() error = %v", err)
		}
		if !reflect.DeepEqual(got, req) {
			t.Fatalf("DecodeControlWriteRequest() = %#v, want %#v", got, req)
		}
	}
}

func TestControlWriteRequestCodecRoundTripPromoteControllerVoter(t *testing.T) {
	req := ControlWriteRequest{
		Action: ControlWriteActionPromoteControllerVoter,
		PromoteControllerVoter: PromoteControllerVoterRequest{
			NodeID:              4,
			ExpectedRevision:    9,
			ExpectedVoters:      []uint64{1, 2, 3},
			ObservedConfigIndex: 11,
			ObservedVoters:      []uint64{1, 2, 3, 4},
		},
	}
	payload, err := EncodeControlWriteRequest(req)
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	got, err := DecodeControlWriteRequest(payload)
	if err != nil {
		t.Fatalf("DecodeControlWriteRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("DecodeControlWriteRequest() = %#v, want %#v", got, req)
	}

	var reqObject map[string]json.RawMessage
	if err := json.Unmarshal(payload[2:], &reqObject); err != nil {
		t.Fatalf("request JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, reqObject, "action", "promote_controller_voter")
	var promoteReq map[string]json.RawMessage
	if err := json.Unmarshal(reqObject["promote_controller_voter"], &promoteReq); err != nil {
		t.Fatalf("promote_controller_voter request JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, promoteReq, "node_id", "expected_revision", "expected_voters", "observed_config_index", "observed_voters")
	for _, forbidden := range []string{"NodeID", "ExpectedRevision", "ExpectedVoters", "ObservedConfigIndex", "ObservedVoters"} {
		if _, ok := promoteReq[forbidden]; ok {
			t.Fatalf("promote_controller_voter request keys = %#v, contains Go field name %s", promoteReq, forbidden)
		}
	}

	respPayload, err := EncodeControlWriteResponse(ControlWriteResponse{
		PromoteControllerVoter: PromoteControllerVoterResult{
			Changed: true,
			Node: Node{
				NodeID:         4,
				Addr:           "n4",
				Roles:          []Role{RoleData, RoleController},
				JoinState:      NodeJoinStateActive,
				Status:         NodeAlive,
				CapacityWeight: 1,
			},
			Revision:       10,
			PreviousVoters: []uint64{1, 2, 3},
			NextVoters:     []uint64{1, 2, 3, 4},
			Warnings:       []string{"controller_voter_count_even"},
		},
	})
	if err != nil {
		t.Fatalf("EncodeControlWriteResponse() error = %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(respPayload[2:], &envelope); err != nil {
		t.Fatalf("response envelope JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, envelope, "response")
	var response map[string]json.RawMessage
	if err := json.Unmarshal(envelope["response"], &response); err != nil {
		t.Fatalf("response JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, response, "promote_controller_voter")
	var promoteResp map[string]json.RawMessage
	if err := json.Unmarshal(response["promote_controller_voter"], &promoteResp); err != nil {
		t.Fatalf("promote_controller_voter response JSON unmarshal error = %v", err)
	}
	for _, want := range []string{"changed", "node", "revision", "previous_voters", "next_voters", "warnings"} {
		if _, ok := promoteResp[want]; !ok {
			t.Fatalf("promote_controller_voter response keys = %#v, missing %s", promoteResp, want)
		}
	}
	for _, forbidden := range []string{"PreviousVoters", "NextVoters"} {
		if _, ok := promoteResp[forbidden]; ok {
			t.Fatalf("promote_controller_voter response keys = %#v, contains Go field name %s", promoteResp, forbidden)
		}
	}
}

func TestControlWriteRequestCodecPreservesExplicitEmptyPromoteControllerVoterExpectedVoters(t *testing.T) {
	req := ControlWriteRequest{
		Action: ControlWriteActionPromoteControllerVoter,
		PromoteControllerVoter: PromoteControllerVoterRequest{
			NodeID:              4,
			ExpectedRevision:    9,
			ExpectedVoters:      []uint64{},
			ObservedConfigIndex: 11,
			ObservedVoters:      []uint64{1, 4},
		},
	}
	payload, err := EncodeControlWriteRequest(req)
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	got, err := DecodeControlWriteRequest(payload)
	if err != nil {
		t.Fatalf("DecodeControlWriteRequest() error = %v", err)
	}
	if got.PromoteControllerVoter.ExpectedVoters == nil || len(got.PromoteControllerVoter.ExpectedVoters) != 0 {
		t.Fatalf("ExpectedVoters = %#v, want non-nil empty slice", got.PromoteControllerVoter.ExpectedVoters)
	}

	var reqObject map[string]json.RawMessage
	if err := json.Unmarshal(payload[2:], &reqObject); err != nil {
		t.Fatalf("request JSON unmarshal error = %v", err)
	}
	var promoteReq map[string]json.RawMessage
	if err := json.Unmarshal(reqObject["promote_controller_voter"], &promoteReq); err != nil {
		t.Fatalf("promote_controller_voter request JSON unmarshal error = %v", err)
	}
	raw, ok := promoteReq["expected_voters"]
	if !ok {
		t.Fatalf("promote_controller_voter request keys = %#v, missing expected_voters", promoteReq)
	}
	var voters []uint64
	if err := json.Unmarshal(raw, &voters); err != nil {
		t.Fatalf("expected_voters JSON unmarshal error = %v", err)
	}
	if voters == nil || len(voters) != 0 {
		t.Fatalf("expected_voters JSON = %s, want []", raw)
	}
}

func TestControlWriteRequestCodecPreservesNilPromoteControllerVoterExpectedVoters(t *testing.T) {
	req := ControlWriteRequest{
		Action: ControlWriteActionPromoteControllerVoter,
		PromoteControllerVoter: PromoteControllerVoterRequest{
			NodeID:              4,
			ExpectedRevision:    9,
			ObservedConfigIndex: 11,
			ObservedVoters:      []uint64{1, 4},
		},
	}
	payload, err := EncodeControlWriteRequest(req)
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	got, err := DecodeControlWriteRequest(payload)
	if err != nil {
		t.Fatalf("DecodeControlWriteRequest() error = %v", err)
	}
	if got.PromoteControllerVoter.ExpectedVoters != nil {
		t.Fatalf("ExpectedVoters = %#v, want nil", got.PromoteControllerVoter.ExpectedVoters)
	}

	var reqObject map[string]json.RawMessage
	if err := json.Unmarshal(payload[2:], &reqObject); err != nil {
		t.Fatalf("request JSON unmarshal error = %v", err)
	}
	var promoteReq map[string]json.RawMessage
	if err := json.Unmarshal(reqObject["promote_controller_voter"], &promoteReq); err != nil {
		t.Fatalf("promote_controller_voter request JSON unmarshal error = %v", err)
	}
	if raw, ok := promoteReq["expected_voters"]; ok && string(raw) != "null" {
		t.Fatalf("expected_voters JSON = %s, want null or omitted for nil", raw)
	}
}

func TestControlWriteReportNodeHealthUsesSnakeCaseJSON(t *testing.T) {
	reqPayload, err := EncodeControlWriteRequest(ControlWriteRequest{
		Action: ControlWriteActionReportNodeHealth,
		ReportNodeHealth: NodeReport{
			NodeID:                  1,
			Status:                  NodeAlive,
			RuntimeReady:            true,
			ObservedControlRevision: 9,
			ObservedSlotRevision:    12,
			ReportSeq:               3,
			ErrorCode:               "warming",
		},
	})
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	var reqObject map[string]json.RawMessage
	if err := json.Unmarshal(reqPayload[2:], &reqObject); err != nil {
		t.Fatalf("request JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, reqObject, "action", "report_node_health")
	var reportReq map[string]json.RawMessage
	if err := json.Unmarshal(reqObject["report_node_health"], &reportReq); err != nil {
		t.Fatalf("report_node_health request JSON unmarshal error = %v", err)
	}
	for _, want := range []string{"node_id", "status", "runtime_ready", "observed_control_revision", "observed_slot_revision", "report_seq", "error_code"} {
		if _, ok := reportReq[want]; !ok {
			t.Fatalf("report_node_health request keys = %#v, missing %s", reportReq, want)
		}
	}
	for _, forbidden := range []string{"NodeID", "RuntimeReady", "ObservedControlRevision", "ObservedSlotRevision", "ReportSeq", "ErrorCode"} {
		if _, ok := reportReq[forbidden]; ok {
			t.Fatalf("report_node_health request keys = %#v, contains Go field name %s", reportReq, forbidden)
		}
	}
}

func TestControlWriteHandlerAppliesReportNodeHealthWithoutResponsePayload(t *testing.T) {
	applier := &recordingControlWriteApplier{}
	handler := NewControlWriteHandler(applier)
	req, err := EncodeControlWriteRequest(ControlWriteRequest{
		Action: ControlWriteActionReportNodeHealth,
		ReportNodeHealth: NodeReport{
			NodeID:                  1,
			Status:                  NodeAlive,
			RuntimeReady:            true,
			ObservedControlRevision: 9,
			ReportSeq:               3,
		},
	})
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	payload, err := handler.HandleRPC(context.Background(), req)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	resp, err := DecodeControlWriteResponse(payload)
	if err != nil {
		t.Fatalf("DecodeControlWriteResponse() error = %v", err)
	}
	if !reflect.DeepEqual(resp, ControlWriteResponse{}) {
		t.Fatalf("ControlWriteResponse = %#v, want empty response", resp)
	}
	if len(applier.nodeReports) != 1 || applier.nodeReports[0].ReportSeq != 3 {
		t.Fatalf("nodeReports = %#v, want one report seq 3", applier.nodeReports)
	}
}

func TestEncodeControlWriteMarkNodeLeavingUsesSnakeCaseJSON(t *testing.T) {
	reqPayload, err := EncodeControlWriteRequest(ControlWriteRequest{
		Action:          ControlWriteActionMarkNodeLeaving,
		MarkNodeLeaving: MarkNodeLeavingRequest{NodeID: 4},
	})
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	var reqObject map[string]json.RawMessage
	if err := json.Unmarshal(reqPayload[2:], &reqObject); err != nil {
		t.Fatalf("request JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, reqObject, "action", "mark_node_leaving")
	var leavingReq map[string]json.RawMessage
	if err := json.Unmarshal(reqObject["mark_node_leaving"], &leavingReq); err != nil {
		t.Fatalf("mark_node_leaving request JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, leavingReq, "node_id")
	if _, ok := leavingReq["NodeID"]; ok {
		t.Fatalf("mark_node_leaving request keys = %#v, contains Go field name NodeID", leavingReq)
	}

	respPayload, err := EncodeControlWriteResponse(ControlWriteResponse{
		MarkNodeLeaving: MarkNodeLeavingResult{
			Changed: true,
			Node: Node{
				NodeID:         4,
				Addr:           "n4",
				Roles:          []Role{RoleData},
				JoinState:      NodeJoinStateLeaving,
				Status:         NodeAlive,
				CapacityWeight: 1,
			},
			Revision: 9,
		},
	})
	if err != nil {
		t.Fatalf("EncodeControlWriteResponse() error = %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(respPayload[2:], &envelope); err != nil {
		t.Fatalf("response envelope JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, envelope, "response")
	var response map[string]json.RawMessage
	if err := json.Unmarshal(envelope["response"], &response); err != nil {
		t.Fatalf("response JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, response, "mark_node_leaving")
	var leavingResp map[string]json.RawMessage
	if err := json.Unmarshal(response["mark_node_leaving"], &leavingResp); err != nil {
		t.Fatalf("mark_node_leaving response JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, leavingResp, "changed", "node", "revision")
}

func TestEncodeControlWriteMarkNodeRemovedUsesSnakeCaseJSON(t *testing.T) {
	reqPayload, err := EncodeControlWriteRequest(ControlWriteRequest{
		Action:          ControlWriteActionMarkNodeRemoved,
		MarkNodeRemoved: MarkNodeRemovedRequest{NodeID: 4, StateRevision: 9},
	})
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	var reqObject map[string]json.RawMessage
	if err := json.Unmarshal(reqPayload[2:], &reqObject); err != nil {
		t.Fatalf("request JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, reqObject, "action", "mark_node_removed")
	var removedReq map[string]json.RawMessage
	if err := json.Unmarshal(reqObject["mark_node_removed"], &removedReq); err != nil {
		t.Fatalf("mark_node_removed request JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, removedReq, "node_id", "state_revision")
	if _, ok := removedReq["NodeID"]; ok {
		t.Fatalf("mark_node_removed request keys = %#v, contains Go field name NodeID", removedReq)
	}
	if _, ok := removedReq["StateRevision"]; ok {
		t.Fatalf("mark_node_removed request keys = %#v, contains Go field name StateRevision", removedReq)
	}

	respPayload, err := EncodeControlWriteResponse(ControlWriteResponse{
		MarkNodeRemoved: MarkNodeRemovedResult{
			Changed: true,
			Node: Node{
				NodeID:         4,
				Addr:           "n4",
				Roles:          []Role{RoleData},
				JoinState:      NodeJoinStateRemoved,
				Status:         NodeDown,
				CapacityWeight: 1,
			},
			Revision: 9,
		},
	})
	if err != nil {
		t.Fatalf("EncodeControlWriteResponse() error = %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(respPayload[2:], &envelope); err != nil {
		t.Fatalf("response envelope JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, envelope, "response")
	var response map[string]json.RawMessage
	if err := json.Unmarshal(envelope["response"], &response); err != nil {
		t.Fatalf("response JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, response, "mark_node_removed")
	var removedResp map[string]json.RawMessage
	if err := json.Unmarshal(response["mark_node_removed"], &removedResp); err != nil {
		t.Fatalf("mark_node_removed response JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, removedResp, "changed", "node", "revision")
}

func TestControlWriteSlotReplicaMoveUsesSnakeCaseJSON(t *testing.T) {
	reqPayload, err := EncodeControlWriteRequest(ControlWriteRequest{
		Action: ControlWriteActionSlotReplicaMove,
		SlotReplicaMove: SlotReplicaMoveRequest{
			SlotID:        1,
			SourceNode:    1,
			TargetNode:    4,
			TargetPeers:   []uint64{4, 2, 3},
			ConfigEpoch:   7,
			StateRevision: 9,
		},
	})
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	var reqObject map[string]json.RawMessage
	if err := json.Unmarshal(reqPayload[2:], &reqObject); err != nil {
		t.Fatalf("request JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, reqObject, "action", "slot_replica_move")
	var moveReq map[string]json.RawMessage
	if err := json.Unmarshal(reqObject["slot_replica_move"], &moveReq); err != nil {
		t.Fatalf("slot_replica_move request JSON unmarshal error = %v", err)
	}
	for _, want := range []string{"slot_id", "source_node", "target_node", "target_peers", "config_epoch", "state_revision"} {
		if _, ok := moveReq[want]; !ok {
			t.Fatalf("slot_replica_move request keys = %#v, missing %s", moveReq, want)
		}
	}
	for _, forbidden := range []string{"SlotID", "SourceNode", "TargetNode", "TargetPeers", "ConfigEpoch", "StateRevision"} {
		if _, ok := moveReq[forbidden]; ok {
			t.Fatalf("slot_replica_move request keys = %#v, contains Go field name %s", moveReq, forbidden)
		}
	}

	respPayload, err := EncodeControlWriteResponse(ControlWriteResponse{
		SlotReplicaMove: SlotReplicaMoveResult{
			Created: true,
			Task: &ReconcileTask{
				TaskID:      "slot-1-replica-move-1-to-4-r9",
				SlotID:      1,
				Kind:        TaskKindSlotReplicaMove,
				Step:        TaskStepOpenLearner,
				SourceNode:  1,
				TargetNode:  4,
				TargetPeers: []uint64{2, 3, 4},
				ConfigEpoch: 7,
				Status:      TaskStatusPending,
			},
		},
	})
	if err != nil {
		t.Fatalf("EncodeControlWriteResponse() error = %v", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(respPayload[2:], &envelope); err != nil {
		t.Fatalf("response envelope JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, envelope, "response")
	var response map[string]json.RawMessage
	if err := json.Unmarshal(envelope["response"], &response); err != nil {
		t.Fatalf("response JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, response, "slot_replica_move")
	var moveResp map[string]json.RawMessage
	if err := json.Unmarshal(response["slot_replica_move"], &moveResp); err != nil {
		t.Fatalf("slot_replica_move response JSON unmarshal error = %v", err)
	}
	requireJSONKeys(t, moveResp, "created", "task")
	var task map[string]json.RawMessage
	if err := json.Unmarshal(moveResp["task"], &task); err != nil {
		t.Fatalf("slot_replica_move task JSON unmarshal error = %v", err)
	}
	for _, want := range []string{"task_id", "slot_id", "kind", "step"} {
		if _, ok := task[want]; !ok {
			t.Fatalf("slot_replica_move task keys = %#v, missing %s", task, want)
		}
	}
	for _, forbidden := range []string{"TaskID", "SlotID", "TargetPeers"} {
		if _, ok := task[forbidden]; ok {
			t.Fatalf("slot_replica_move task keys = %#v, contains Go field name %s", task, forbidden)
		}
	}
}

func requireJSONKeys(t *testing.T, object map[string]json.RawMessage, keys ...string) {
	t.Helper()
	if len(object) != len(keys) {
		t.Fatalf("JSON object keys = %#v, want exactly %v", object, keys)
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			t.Fatalf("JSON object keys = %#v, missing %s", object, key)
		}
	}
}

func TestControlWriteResponsePreservesSemanticErrorIdentity(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "not leader", err: controller.ErrNotLeader, want: controller.ErrNotLeader},
		{name: "not started", err: controller.ErrNotStarted, want: controller.ErrNotStarted},
		{name: "stopped", err: controller.ErrStopped, want: controller.ErrStopped},
		{name: "proposal rejected", err: controller.ErrProposalRejected, want: controller.ErrProposalRejected},
		{name: "expected revision mismatch", err: controller.ErrExpectedRevisionMismatch, want: controller.ErrExpectedRevisionMismatch},
		{name: "node lifecycle conflict", err: controller.ErrNodeLifecycleConflict, want: controller.ErrNodeLifecycleConflict},
		{name: "node lifecycle not found", err: controller.ErrNodeLifecycleNotFound, want: controller.ErrNodeLifecycleNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := encodeControlWriteErrorResponse(tt.err)
			if err != nil {
				t.Fatalf("encodeControlWriteErrorResponse() error = %v", err)
			}
			_, err = DecodeControlWriteResponse(payload)
			if !errors.Is(err, tt.want) {
				t.Fatalf("DecodeControlWriteResponse() error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}

func TestControlCodecRejectsWrongKind(t *testing.T) {
	frame := []byte{controlRPCVersion, controlKindRaftBatch + 99}
	if _, err := DecodeRaftBatch(frame); err == nil {
		t.Fatal("DecodeRaftBatch() error = nil, want invalid frame")
	}
}
