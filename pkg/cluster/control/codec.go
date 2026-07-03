package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
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

// EncodeRaftBatch encodes Controller Raft messages for cluster RPC.
func EncodeRaftBatch(messages []raftpb.Message) ([]byte, error) {
	payload, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindRaftBatch)
	return append(out, payload...), nil
}

// DecodeRaftBatch decodes Controller Raft messages from cluster RPC.
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

// EncodeStateSyncRequest encodes a Controller full-file sync request.
func EncodeStateSyncRequest(req controller.GetStateRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindStateSyncRequest)
	return append(out, payload...), nil
}

// DecodeStateSyncRequest decodes a Controller full-file sync request.
func DecodeStateSyncRequest(data []byte) (controller.GetStateRequest, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindStateSyncRequest)
	if err != nil {
		return controller.GetStateRequest{}, err
	}
	var req controller.GetStateRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return controller.GetStateRequest{}, err
	}
	return req, nil
}

// EncodeStateSyncResponse encodes a Controller full-file sync response.
func EncodeStateSyncResponse(resp controller.GetStateResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	out := clusternet.PutHeader(nil, controlRPCVersion, controlKindStateSyncResponse)
	return append(out, payload...), nil
}

// DecodeStateSyncResponse decodes a Controller full-file sync response.
func DecodeStateSyncResponse(data []byte) (controller.GetStateResponse, error) {
	payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindStateSyncResponse)
	if err != nil {
		return controller.GetStateResponse{}, err
	}
	var resp controller.GetStateResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return controller.GetStateResponse{}, err
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

// TaskRequest carries one Controller task result or progress write.
type TaskRequest struct {
	// Action selects which payload should be applied.
	Action TaskAction `json:"action"`
	// Result carries complete/fail task result payloads.
	Result controller.TaskResult `json:"result,omitempty"`
	// Progress carries participant progress payloads.
	Progress controller.TaskProgress `json:"progress,omitempty"`
	// LeaderTransfer carries a Slot leader transfer intent.
	LeaderTransfer SlotLeaderTransferRequest `json:"leader_transfer,omitempty"`
	// ReplicaMovePhase carries a fenced Slot replica move phase update.
	ReplicaMovePhase controller.SlotReplicaMovePhaseAdvance `json:"replica_move_phase,omitempty"`
	// ReplicaMoveCommit carries a fenced Slot replica move commit.
	ReplicaMoveCommit controller.SlotReplicaMoveCommit `json:"replica_move_commit,omitempty"`
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
	// ControlWriteActionMarkNodeLeaving submits a node leaving intent.
	ControlWriteActionMarkNodeLeaving ControlWriteAction = "mark_node_leaving"
	// ControlWriteActionMarkNodeRemoved submits a node removed intent.
	ControlWriteActionMarkNodeRemoved ControlWriteAction = "mark_node_removed"
	// ControlWriteActionSlotReplicaMove submits a staged Slot replica move intent.
	ControlWriteActionSlotReplicaMove ControlWriteAction = "slot_replica_move"
	// ControlWriteActionPromoteControllerVoter promotes one active node into Controller Raft voting membership.
	ControlWriteActionPromoteControllerVoter ControlWriteAction = "promote_controller_voter"
	// ControlWriteActionReportNodeHealth submits a low-frequency runtime health report.
	ControlWriteActionReportNodeHealth ControlWriteAction = "report_node_health"
)

// ControlWriteRequest carries one generic Controller write.
type ControlWriteRequest struct {
	// Action selects which payload should be applied.
	Action ControlWriteAction `json:"action"`
	// JoinNode carries a data-node join intent.
	JoinNode JoinNodeRequest `json:"join_node,omitempty"`
	// ActivateNode carries a node activation intent.
	ActivateNode ActivateNodeRequest `json:"activate_node,omitempty"`
	// MarkNodeLeaving carries a node leaving intent.
	MarkNodeLeaving MarkNodeLeavingRequest `json:"mark_node_leaving,omitempty"`
	// MarkNodeRemoved carries a node removed intent.
	MarkNodeRemoved MarkNodeRemovedRequest `json:"mark_node_removed,omitempty"`
	// SlotReplicaMove carries a staged Slot replica move intent.
	SlotReplicaMove SlotReplicaMoveRequest `json:"slot_replica_move,omitempty"`
	// PromoteControllerVoter carries a Controller voter promotion intent.
	PromoteControllerVoter PromoteControllerVoterRequest `json:"promote_controller_voter,omitempty"`
	// ReportNodeHealth carries a low-frequency runtime health report.
	ReportNodeHealth NodeReport `json:"report_node_health,omitempty"`
}

type controlWriteRequestJSON struct {
	Action                 ControlWriteAction             `json:"action"`
	JoinNode               *JoinNodeRequest               `json:"join_node,omitempty"`
	ActivateNode           *ActivateNodeRequest           `json:"activate_node,omitempty"`
	MarkNodeLeaving        *MarkNodeLeavingRequest        `json:"mark_node_leaving,omitempty"`
	MarkNodeRemoved        *MarkNodeRemovedRequest        `json:"mark_node_removed,omitempty"`
	SlotReplicaMove        *SlotReplicaMoveRequest        `json:"slot_replica_move,omitempty"`
	PromoteControllerVoter *PromoteControllerVoterRequest `json:"promote_controller_voter,omitempty"`
	ReportNodeHealth       *NodeReport                    `json:"report_node_health,omitempty"`
}

// MarshalJSON encodes only the payload branch selected by Action.
func (req ControlWriteRequest) MarshalJSON() ([]byte, error) {
	wire := controlWriteRequestJSON{Action: req.Action}
	switch req.Action {
	case ControlWriteActionJoinNode:
		wire.JoinNode = &req.JoinNode
	case ControlWriteActionActivateNode:
		wire.ActivateNode = &req.ActivateNode
	case ControlWriteActionMarkNodeLeaving:
		wire.MarkNodeLeaving = &req.MarkNodeLeaving
	case ControlWriteActionMarkNodeRemoved:
		wire.MarkNodeRemoved = &req.MarkNodeRemoved
	case ControlWriteActionSlotReplicaMove:
		wire.SlotReplicaMove = &req.SlotReplicaMove
	case ControlWriteActionPromoteControllerVoter:
		wire.PromoteControllerVoter = &req.PromoteControllerVoter
	case ControlWriteActionReportNodeHealth:
		wire.ReportNodeHealth = &req.ReportNodeHealth
	}
	return json.Marshal(wire)
}

// ControlWriteResponse carries the result of one generic Controller write.
type ControlWriteResponse struct {
	// JoinNode carries the result of a data-node join intent.
	JoinNode JoinNodeResult `json:"join_node,omitempty"`
	// ActivateNode carries the result of a node activation intent.
	ActivateNode ActivateNodeResult `json:"activate_node,omitempty"`
	// MarkNodeLeaving carries the result of a node leaving intent.
	MarkNodeLeaving MarkNodeLeavingResult `json:"mark_node_leaving,omitempty"`
	// MarkNodeRemoved carries the result of a node removed intent.
	MarkNodeRemoved MarkNodeRemovedResult `json:"mark_node_removed,omitempty"`
	// SlotReplicaMove carries the result of a staged Slot replica move intent.
	SlotReplicaMove SlotReplicaMoveResult `json:"slot_replica_move,omitempty"`
	// PromoteControllerVoter carries the result of a Controller voter promotion intent.
	PromoteControllerVoter PromoteControllerVoterResult `json:"promote_controller_voter,omitempty"`
}

type controlWriteResponseJSON struct {
	JoinNode               *JoinNodeResult               `json:"join_node,omitempty"`
	ActivateNode           *ActivateNodeResult           `json:"activate_node,omitempty"`
	MarkNodeLeaving        *MarkNodeLeavingResult        `json:"mark_node_leaving,omitempty"`
	MarkNodeRemoved        *MarkNodeRemovedResult        `json:"mark_node_removed,omitempty"`
	SlotReplicaMove        *SlotReplicaMoveResult        `json:"slot_replica_move,omitempty"`
	PromoteControllerVoter *PromoteControllerVoterResult `json:"promote_controller_voter,omitempty"`
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
	if !reflect.DeepEqual(resp.MarkNodeLeaving, MarkNodeLeavingResult{}) {
		wire.MarkNodeLeaving = &resp.MarkNodeLeaving
	}
	if !reflect.DeepEqual(resp.MarkNodeRemoved, MarkNodeRemovedResult{}) {
		wire.MarkNodeRemoved = &resp.MarkNodeRemoved
	}
	if !reflect.DeepEqual(resp.SlotReplicaMove, SlotReplicaMoveResult{}) {
		wire.SlotReplicaMove = &resp.SlotReplicaMove
	}
	if !reflect.DeepEqual(resp.PromoteControllerVoter, PromoteControllerVoterResult{}) {
		wire.PromoteControllerVoter = &resp.PromoteControllerVoter
	}
	return json.Marshal(wire)
}

type controlWriteResponseEnvelope struct {
	Response  ControlWriteResponse `json:"response,omitempty"`
	Error     string               `json:"error,omitempty"`
	ErrorCode string               `json:"error_code,omitempty"`
}

const (
	controlWriteErrorCodeNotLeader         = "controller_not_leader"
	controlWriteErrorCodeNotStarted        = "controller_not_started"
	controlWriteErrorCodeStopped           = "controller_stopped"
	controlWriteErrorCodeRevisionMismatch  = "controller_expected_revision_mismatch"
	controlWriteErrorCodeProposalRejected  = "controller_proposal_rejected"
	controlWriteErrorCodeLifecycleConflict = "controller_node_lifecycle_conflict"
	controlWriteErrorCodeLifecycleNotFound = "controller_node_lifecycle_not_found"
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
	case errors.Is(err, controller.ErrNotLeader):
		return controlWriteErrorCodeNotLeader
	case errors.Is(err, controller.ErrNotStarted):
		return controlWriteErrorCodeNotStarted
	case errors.Is(err, controller.ErrStopped):
		return controlWriteErrorCodeStopped
	case controller.IsExpectedRevisionMismatch(err):
		return controlWriteErrorCodeRevisionMismatch
	case errors.Is(err, controller.ErrProposalRejected):
		return controlWriteErrorCodeProposalRejected
	case errors.Is(err, controller.ErrNodeLifecycleConflict):
		return controlWriteErrorCodeLifecycleConflict
	case errors.Is(err, controller.ErrNodeLifecycleNotFound):
		return controlWriteErrorCodeLifecycleNotFound
	default:
		return ""
	}
}

func controlWriteSemanticError(code, text string) error {
	var target error
	switch code {
	case controlWriteErrorCodeNotLeader:
		target = controller.ErrNotLeader
	case controlWriteErrorCodeNotStarted:
		target = controller.ErrNotStarted
	case controlWriteErrorCodeStopped:
		target = controller.ErrStopped
	case controlWriteErrorCodeRevisionMismatch:
		target = controller.ErrExpectedRevisionMismatch
	case controlWriteErrorCodeProposalRejected:
		target = controller.ErrProposalRejected
	case controlWriteErrorCodeLifecycleConflict:
		target = controller.ErrNodeLifecycleConflict
	case controlWriteErrorCodeLifecycleNotFound:
		target = controller.ErrNodeLifecycleNotFound
	default:
		return nil
	}
	if text == "" || text == target.Error() {
		return target
	}
	return fmt.Errorf("%w: %s", target, text)
}
