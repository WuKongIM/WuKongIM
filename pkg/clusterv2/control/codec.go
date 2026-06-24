package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	controlRPCVersion uint8 = 1

	controlKindRaftBatch uint8 = 1 + iota
	controlKindStateSyncRequest
	controlKindStateSyncResponse
	controlKindTaskRequest
	controlKindWriteRequest
	controlKindWriteResponse
)

// EncodeRaftBatch encodes ControllerV2 Raft messages for clusterv2 RPC.
func EncodeRaftBatch(messages []raftpb.Message) ([]byte, error) {
	payload, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindRaftBatch)
	return append(out, payload...), nil
}

// DecodeRaftBatch decodes ControllerV2 Raft messages from clusterv2 RPC.
func DecodeRaftBatch(data []byte) ([]raftpb.Message, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindRaftBatch)
	if err != nil {
		return nil, err
	}
	var messages []raftpb.Message
	if err := json.Unmarshal(payload, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

// EncodeStateSyncRequest encodes a ControllerV2 full-file sync request.
func EncodeStateSyncRequest(req cv2.GetStateRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindStateSyncRequest)
	return append(out, payload...), nil
}

// DecodeStateSyncRequest decodes a ControllerV2 full-file sync request.
func DecodeStateSyncRequest(data []byte) (cv2.GetStateRequest, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindStateSyncRequest)
	if err != nil {
		return cv2.GetStateRequest{}, err
	}
	var req cv2.GetStateRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return cv2.GetStateRequest{}, err
	}
	return req, nil
}

// EncodeStateSyncResponse encodes a ControllerV2 full-file sync response.
func EncodeStateSyncResponse(resp cv2.GetStateResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindStateSyncResponse)
	return append(out, payload...), nil
}

// DecodeStateSyncResponse decodes a ControllerV2 full-file sync response.
func DecodeStateSyncResponse(data []byte) (cv2.GetStateResponse, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindStateSyncResponse)
	if err != nil {
		return cv2.GetStateResponse{}, err
	}
	var resp cv2.GetStateResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return cv2.GetStateResponse{}, err
	}
	return resp, nil
}

// TaskAction selects which task write the remote Controller should apply.
type TaskAction string

const (
	// TaskActionComplete submits a global task completion.
	TaskActionComplete TaskAction = "complete"
	// TaskActionFail submits a global task failure.
	TaskActionFail TaskAction = "fail"
	// TaskActionProgress submits one participant progress report.
	TaskActionProgress TaskAction = "progress"
	// TaskActionLeaderTransfer submits a Slot leader transfer intent.
	TaskActionLeaderTransfer TaskAction = "leader_transfer"
	// TaskActionReplicaMovePhase advances a staged Slot replica move phase.
	TaskActionReplicaMovePhase TaskAction = "replica_move_phase"
	// TaskActionReplicaMoveCommit commits a staged Slot replica move assignment.
	TaskActionReplicaMoveCommit TaskAction = "replica_move_commit"
)

// TaskRequest carries one ControllerV2 task result or progress write.
type TaskRequest struct {
	// Action selects which payload should be applied.
	Action TaskAction `json:"action"`
	// Result carries complete/fail task result payloads.
	Result cv2.TaskResult `json:"result,omitempty"`
	// Progress carries participant progress payloads.
	Progress cv2.TaskProgress `json:"progress,omitempty"`
	// LeaderTransfer carries a Slot leader transfer intent.
	LeaderTransfer SlotLeaderTransferRequest `json:"leader_transfer,omitempty"`
	// ReplicaMovePhase carries a fenced Slot replica move phase update.
	ReplicaMovePhase cv2.SlotReplicaMovePhaseAdvance `json:"replica_move_phase,omitempty"`
	// ReplicaMoveCommit carries a fenced Slot replica move commit.
	ReplicaMoveCommit cv2.SlotReplicaMoveCommit `json:"replica_move_commit,omitempty"`
}

// EncodeTaskRequest encodes one task write request.
func EncodeTaskRequest(req TaskRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindTaskRequest)
	return append(out, payload...), nil
}

// DecodeTaskRequest decodes one task write request.
func DecodeTaskRequest(data []byte) (TaskRequest, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindTaskRequest)
	if err != nil {
		return TaskRequest{}, err
	}
	var req TaskRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return TaskRequest{}, err
	}
	return req, nil
}

// ControlWriteAction selects which generic control write the remote Controller should apply.
type ControlWriteAction string

const (
	// ControlWriteActionJoinNode submits a data-node join intent.
	ControlWriteActionJoinNode ControlWriteAction = "join_node"
	// ControlWriteActionActivateNode submits a node activation intent.
	ControlWriteActionActivateNode ControlWriteAction = "activate_node"
	// ControlWriteActionSlotReplicaMove submits a staged Slot replica move intent.
	ControlWriteActionSlotReplicaMove ControlWriteAction = "slot_replica_move"
)

// ControlWriteRequest carries one generic ControllerV2 write.
type ControlWriteRequest struct {
	// Action selects which payload should be applied.
	Action ControlWriteAction `json:"action"`
	// JoinNode carries a data-node join intent.
	JoinNode JoinNodeRequest `json:"join_node,omitempty"`
	// ActivateNode carries a node activation intent.
	ActivateNode ActivateNodeRequest `json:"activate_node,omitempty"`
	// SlotReplicaMove carries a staged Slot replica move intent.
	SlotReplicaMove SlotReplicaMoveRequest `json:"slot_replica_move,omitempty"`
}

type controlWriteRequestJSON struct {
	Action          ControlWriteAction      `json:"action"`
	JoinNode        *JoinNodeRequest        `json:"join_node,omitempty"`
	ActivateNode    *ActivateNodeRequest    `json:"activate_node,omitempty"`
	SlotReplicaMove *SlotReplicaMoveRequest `json:"slot_replica_move,omitempty"`
}

// MarshalJSON encodes only the payload branch selected by Action.
func (req ControlWriteRequest) MarshalJSON() ([]byte, error) {
	wire := controlWriteRequestJSON{Action: req.Action}
	switch req.Action {
	case ControlWriteActionJoinNode:
		wire.JoinNode = &req.JoinNode
	case ControlWriteActionActivateNode:
		wire.ActivateNode = &req.ActivateNode
	case ControlWriteActionSlotReplicaMove:
		wire.SlotReplicaMove = &req.SlotReplicaMove
	}
	return json.Marshal(wire)
}

// ControlWriteResponse carries the result of one generic ControllerV2 write.
type ControlWriteResponse struct {
	// JoinNode carries the result of a data-node join intent.
	JoinNode JoinNodeResult `json:"join_node,omitempty"`
	// ActivateNode carries the result of a node activation intent.
	ActivateNode ActivateNodeResult `json:"activate_node,omitempty"`
	// SlotReplicaMove carries the result of a staged Slot replica move intent.
	SlotReplicaMove SlotReplicaMoveResult `json:"slot_replica_move,omitempty"`
}

type controlWriteResponseJSON struct {
	JoinNode        *JoinNodeResult        `json:"join_node,omitempty"`
	ActivateNode    *ActivateNodeResult    `json:"activate_node,omitempty"`
	SlotReplicaMove *SlotReplicaMoveResult `json:"slot_replica_move,omitempty"`
}

// MarshalJSON encodes only response branches that carry a result.
func (resp ControlWriteResponse) MarshalJSON() ([]byte, error) {
	var wire controlWriteResponseJSON
	if !reflect.DeepEqual(resp.JoinNode, JoinNodeResult{}) {
		wire.JoinNode = &resp.JoinNode
	}
	if !reflect.DeepEqual(resp.ActivateNode, ActivateNodeResult{}) {
		wire.ActivateNode = &resp.ActivateNode
	}
	if !reflect.DeepEqual(resp.SlotReplicaMove, SlotReplicaMoveResult{}) {
		wire.SlotReplicaMove = &resp.SlotReplicaMove
	}
	return json.Marshal(wire)
}

type controlWriteResponseEnvelope struct {
	Response  ControlWriteResponse `json:"response,omitempty"`
	Error     string               `json:"error,omitempty"`
	ErrorCode string               `json:"error_code,omitempty"`
}

const (
	controlWriteErrorCodeNotLeader         = "controllerv2_not_leader"
	controlWriteErrorCodeNotStarted        = "controllerv2_not_started"
	controlWriteErrorCodeStopped           = "controllerv2_stopped"
	controlWriteErrorCodeProposalRejected  = "controllerv2_proposal_rejected"
	controlWriteErrorCodeLifecycleConflict = "controllerv2_node_lifecycle_conflict"
	controlWriteErrorCodeLifecycleNotFound = "controllerv2_node_lifecycle_not_found"
)

// EncodeControlWriteRequest encodes one generic control write request.
func EncodeControlWriteRequest(req ControlWriteRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindWriteRequest)
	return append(out, payload...), nil
}

// DecodeControlWriteRequest decodes one generic control write request.
func DecodeControlWriteRequest(data []byte) (ControlWriteRequest, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindWriteRequest)
	if err != nil {
		return ControlWriteRequest{}, err
	}
	var req ControlWriteRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return ControlWriteRequest{}, err
	}
	return req, nil
}

// EncodeControlWriteResponse encodes one generic control write response.
func EncodeControlWriteResponse(resp ControlWriteResponse) ([]byte, error) {
	return encodeControlWriteResponseEnvelope(controlWriteResponseEnvelope{Response: resp})
}

func encodeControlWriteErrorResponse(err error) ([]byte, error) {
	if err == nil {
		return EncodeControlWriteResponse(ControlWriteResponse{})
	}
	return encodeControlWriteResponseEnvelope(controlWriteResponseEnvelope{
		Error:     err.Error(),
		ErrorCode: controlWriteErrorCode(err),
	})
}

func encodeControlWriteResponseEnvelope(env controlWriteResponseEnvelope) ([]byte, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindWriteResponse)
	return append(out, payload...), nil
}

// DecodeControlWriteResponse decodes one generic control write response.
func DecodeControlWriteResponse(data []byte) (ControlWriteResponse, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindWriteResponse)
	if err != nil {
		return ControlWriteResponse{}, err
	}
	var env controlWriteResponseEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return ControlWriteResponse{}, err
	}
	if env.Error != "" {
		if semantic := controlWriteSemanticError(env.ErrorCode, env.Error); semantic != nil {
			return env.Response, semantic
		}
		return env.Response, errors.New(env.Error)
	}
	return env.Response, nil
}

func controlWriteErrorCode(err error) string {
	switch {
	case errors.Is(err, cv2.ErrNotLeader):
		return controlWriteErrorCodeNotLeader
	case errors.Is(err, cv2.ErrNotStarted):
		return controlWriteErrorCodeNotStarted
	case errors.Is(err, cv2.ErrStopped):
		return controlWriteErrorCodeStopped
	case errors.Is(err, cv2.ErrProposalRejected):
		return controlWriteErrorCodeProposalRejected
	case errors.Is(err, cv2.ErrNodeLifecycleConflict):
		return controlWriteErrorCodeLifecycleConflict
	case errors.Is(err, cv2.ErrNodeLifecycleNotFound):
		return controlWriteErrorCodeLifecycleNotFound
	default:
		return ""
	}
}

func controlWriteSemanticError(code, text string) error {
	var target error
	switch code {
	case controlWriteErrorCodeNotLeader:
		target = cv2.ErrNotLeader
	case controlWriteErrorCodeNotStarted:
		target = cv2.ErrNotStarted
	case controlWriteErrorCodeStopped:
		target = cv2.ErrStopped
	case controlWriteErrorCodeProposalRejected:
		target = cv2.ErrProposalRejected
	case controlWriteErrorCodeLifecycleConflict:
		target = cv2.ErrNodeLifecycleConflict
	case controlWriteErrorCodeLifecycleNotFound:
		target = cv2.ErrNodeLifecycleNotFound
	default:
		return nil
	}
	if text == "" || text == target.Error() {
		return target
	}
	return fmt.Errorf("%w: %s", target, text)
}
