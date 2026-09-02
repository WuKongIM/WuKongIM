package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestControlWriteHandlerDispatchesEveryBoundedMutation(t *testing.T) {
	tests := []struct {
		name    string
		request ControlWriteRequest
		action  ControlWriteAction
		assert  func(*testing.T, ControlWriteResponse)
	}{
		{
			name: "join node",
			request: ControlWriteRequest{Action: ControlWriteActionJoinNode, JoinNode: JoinNodeRequest{
				NodeID: 4, Name: "node-4", Addr: "n4", Roles: []Role{RoleData}, CapacityWeight: 2,
			}},
			action: ControlWriteActionJoinNode,
			assert: func(t *testing.T, response ControlWriteResponse) {
				if !response.JoinNode.Created || response.JoinNode.Revision != 11 {
					t.Fatalf("join response = %#v", response.JoinNode)
				}
			},
		},
		{
			name:    "activate node",
			request: ControlWriteRequest{Action: ControlWriteActionActivateNode, ActivateNode: ActivateNodeRequest{NodeID: 4}},
			action:  ControlWriteActionActivateNode,
			assert: func(t *testing.T, response ControlWriteResponse) {
				if !response.ActivateNode.Changed || response.ActivateNode.Revision != 12 {
					t.Fatalf("activate response = %#v", response.ActivateNode)
				}
			},
		},
		{
			name:    "mark node leaving",
			request: ControlWriteRequest{Action: ControlWriteActionMarkNodeLeaving, MarkNodeLeaving: MarkNodeLeavingRequest{NodeID: 4}},
			action:  ControlWriteActionMarkNodeLeaving,
			assert: func(t *testing.T, response ControlWriteResponse) {
				if !response.MarkNodeLeaving.Changed || response.MarkNodeLeaving.Revision != 13 {
					t.Fatalf("leaving response = %#v", response.MarkNodeLeaving)
				}
			},
		},
		{
			name:    "mark node removed",
			request: ControlWriteRequest{Action: ControlWriteActionMarkNodeRemoved, MarkNodeRemoved: MarkNodeRemovedRequest{NodeID: 4, StateRevision: 13}},
			action:  ControlWriteActionMarkNodeRemoved,
			assert: func(t *testing.T, response ControlWriteResponse) {
				if !response.MarkNodeRemoved.Changed || response.MarkNodeRemoved.Revision != 14 {
					t.Fatalf("removed response = %#v", response.MarkNodeRemoved)
				}
			},
		},
		{
			name:    "slot leader transfer",
			request: ControlWriteRequest{Action: ControlWriteActionSlotLeaderTransfer, SlotLeaderTransfer: SlotLeaderTransferRequest{SlotID: 1, SourceNode: 1, TargetNode: 2, StateRevision: 14}},
			action:  ControlWriteActionSlotLeaderTransfer,
			assert: func(t *testing.T, response ControlWriteResponse) {
				if !response.SlotLeaderTransfer.Created || response.SlotLeaderTransfer.Task == nil {
					t.Fatalf("leader transfer response = %#v", response.SlotLeaderTransfer)
				}
			},
		},
		{
			name:    "slot replica move",
			request: ControlWriteRequest{Action: ControlWriteActionSlotReplicaMove, SlotReplicaMove: SlotReplicaMoveRequest{SlotID: 1, SourceNode: 2, TargetNode: 3, StateRevision: 14}},
			action:  ControlWriteActionSlotReplicaMove,
			assert: func(t *testing.T, response ControlWriteResponse) {
				if !response.SlotReplicaMove.Created || response.SlotReplicaMove.Task == nil {
					t.Fatalf("replica move response = %#v", response.SlotReplicaMove)
				}
			},
		},
		{
			name:    "promote controller voter",
			request: ControlWriteRequest{Action: ControlWriteActionPromoteControllerVoter, PromoteControllerVoter: PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: 14, ExpectedVoters: []uint64{1, 2, 3}}},
			action:  ControlWriteActionPromoteControllerVoter,
			assert: func(t *testing.T, response ControlWriteResponse) {
				if !response.PromoteControllerVoter.Changed || response.PromoteControllerVoter.Revision != 15 {
					t.Fatalf("promotion response = %#v", response.PromoteControllerVoter)
				}
			},
		},
		{
			name:    "report node health",
			request: ControlWriteRequest{Action: ControlWriteActionReportNodeHealth, ReportNodeHealth: NodeReport{NodeID: 4, ReportSeq: 8}},
			action:  ControlWriteActionReportNodeHealth,
			assert:  func(*testing.T, ControlWriteResponse) {},
		},
		{
			name: "replace scheduled backup",
			request: ControlWriteRequest{Action: ControlWriteActionReplaceScheduledBackup, ReplaceScheduledBackup: ReplaceScheduledBackupRequest{
				ExpectedRevision: 15, Replacement: controller.ScheduledBackupState{Plan: &controller.BackupPlan{Enabled: true, Cron: "0 1 * * *"}},
			}},
			action: ControlWriteActionReplaceScheduledBackup,
			assert: func(*testing.T, ControlWriteResponse) {},
		},
		{
			name: "replace ops MCP",
			request: ControlWriteRequest{Action: ControlWriteActionReplaceOpsMCP, ReplaceOpsMCP: ReplaceOpsMCPRequest{
				ExpectedRevision: 16, State: controller.OpsMCPState{OwnerNodeID: 2},
			}},
			action: ControlWriteActionReplaceOpsMCP,
			assert: func(*testing.T, ControlWriteResponse) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applier := &boundaryControlWriteApplier{}
			payload, err := EncodeControlWriteRequest(tt.request)
			if err != nil {
				t.Fatalf("EncodeControlWriteRequest() error = %v", err)
			}
			encodedResponse, err := NewControlWriteHandler(applier).HandleRPC(context.Background(), payload)
			if err != nil {
				t.Fatalf("HandleRPC() error = %v", err)
			}
			response, err := DecodeControlWriteResponse(encodedResponse)
			if err != nil {
				t.Fatalf("DecodeControlWriteResponse() error = %v", err)
			}
			if !reflect.DeepEqual(applier.actions, []ControlWriteAction{tt.action}) {
				t.Fatalf("applied actions = %#v, want %q", applier.actions, tt.action)
			}
			tt.assert(t, response)
		})
	}
}

func TestControlWriteHandlerPreservesSemanticErrorsForEveryMutation(t *testing.T) {
	requests := []ControlWriteRequest{
		{Action: ControlWriteActionJoinNode},
		{Action: ControlWriteActionActivateNode},
		{Action: ControlWriteActionMarkNodeLeaving},
		{Action: ControlWriteActionMarkNodeRemoved},
		{Action: ControlWriteActionSlotLeaderTransfer},
		{Action: ControlWriteActionSlotReplicaMove},
		{Action: ControlWriteActionPromoteControllerVoter},
		{Action: ControlWriteActionReportNodeHealth},
		{Action: ControlWriteActionReplaceScheduledBackup},
		{Action: ControlWriteActionReplaceOpsMCP},
	}
	for _, request := range requests {
		t.Run(string(request.Action), func(t *testing.T) {
			payload, err := EncodeControlWriteRequest(request)
			if err != nil {
				t.Fatalf("EncodeControlWriteRequest() error = %v", err)
			}
			response, err := NewControlWriteHandler(&boundaryControlWriteApplier{err: controller.ErrExpectedRevisionMismatch}).HandleRPC(context.Background(), payload)
			if err != nil {
				t.Fatalf("HandleRPC() transport error = %v", err)
			}
			_, err = DecodeControlWriteResponse(response)
			if !errors.Is(err, controller.ErrExpectedRevisionMismatch) {
				t.Fatalf("DecodeControlWriteResponse() error = %v, want revision mismatch", err)
			}
		})
	}
}

func TestControlWriteHandlerRejectsMalformedUnsupportedAndUnknownWrites(t *testing.T) {
	handler := NewControlWriteHandler(nil)
	if _, err := handler.HandleRPC(context.Background(), []byte("bad")); err == nil {
		t.Fatal("malformed request error = nil")
	}
	payload, err := EncodeControlWriteRequest(ControlWriteRequest{Action: ControlWriteActionJoinNode})
	if err != nil {
		t.Fatalf("EncodeControlWriteRequest() error = %v", err)
	}
	if _, err := handler.HandleRPC(context.Background(), payload); err == nil || !strings.Contains(err.Error(), "applier is required") {
		t.Fatalf("nil applier error = %v", err)
	}

	requiredOnly := requiredControlWriteApplier{}
	backup, _ := EncodeControlWriteRequest(ControlWriteRequest{Action: ControlWriteActionReplaceScheduledBackup})
	if _, err := NewControlWriteHandler(&requiredOnly).HandleRPC(context.Background(), backup); err == nil || !strings.Contains(err.Error(), "scheduled backup replacement is unsupported") {
		t.Fatalf("unsupported backup error = %v", err)
	}
	ops, _ := EncodeControlWriteRequest(ControlWriteRequest{Action: ControlWriteActionReplaceOpsMCP})
	if _, err := NewControlWriteHandler(&requiredOnly).HandleRPC(context.Background(), ops); err == nil || !strings.Contains(err.Error(), "ops MCP replacement is unsupported") {
		t.Fatalf("unsupported ops MCP error = %v", err)
	}
	unknown, _ := EncodeControlWriteRequest(ControlWriteRequest{Action: "future_action"})
	if _, err := NewControlWriteHandler(&requiredOnly).HandleRPC(context.Background(), unknown); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("unknown action error = %v", err)
	}

	var client *ControlWriteClient
	if _, err := client.Submit(context.Background(), 1, ControlWriteRequest{}); err == nil || !strings.Contains(err.Error(), "caller is required") {
		t.Fatalf("nil client Submit() error = %v", err)
	}
	client = NewControlWriteClient(nil)
	if _, err := client.Submit(context.Background(), 1, ControlWriteRequest{}); err == nil || !strings.Contains(err.Error(), "caller is required") {
		t.Fatalf("nil caller Submit() error = %v", err)
	}
}

func TestTaskHandlerDispatchesFencedTaskActionsAndRejectsUnknownInput(t *testing.T) {
	requests := []TaskRequest{
		{Action: TaskActionComplete, Result: TaskResult{TaskID: "task-complete"}},
		{Action: TaskActionFail, Result: TaskResult{TaskID: "task-fail"}},
		{Action: TaskActionProgress, Progress: TaskProgress{TaskID: "task-progress"}},
		{Action: TaskActionLeaderTransfer, LeaderTransfer: SlotLeaderTransferRequest{SlotID: 1}},
		{Action: TaskActionReplicaMovePhase, ReplicaMovePhase: SlotReplicaMovePhaseAdvance{TaskID: "task-phase"}},
		{Action: TaskActionReplicaMoveCommit, ReplicaMoveCommit: SlotReplicaMoveCommit{TaskID: "task-commit"}},
	}
	for _, request := range requests {
		t.Run(string(request.Action), func(t *testing.T) {
			applier := &boundaryTaskApplier{}
			payload, err := EncodeTaskRequest(request)
			if err != nil {
				t.Fatalf("EncodeTaskRequest() error = %v", err)
			}
			if _, err := NewTaskHandler(applier).HandleRPC(context.Background(), payload); err != nil {
				t.Fatalf("HandleRPC() error = %v", err)
			}
			if !reflect.DeepEqual(applier.actions, []TaskAction{request.Action}) {
				t.Fatalf("task actions = %#v, want %q", applier.actions, request.Action)
			}
		})
	}

	failing := &boundaryTaskApplier{err: controller.ErrStopped}
	payload, _ := EncodeTaskRequest(TaskRequest{Action: TaskActionReplicaMoveCommit})
	if _, err := NewTaskHandler(failing).HandleRPC(context.Background(), payload); !errors.Is(err, controller.ErrStopped) {
		t.Fatalf("task applier error = %v, want ErrStopped", err)
	}
	if _, err := NewTaskHandler(failing).HandleRPC(context.Background(), []byte("bad")); err == nil {
		t.Fatal("malformed task request error = nil")
	}
	unknown, _ := EncodeTaskRequest(TaskRequest{Action: "future_action"})
	if _, err := NewTaskHandler(failing).HandleRPC(context.Background(), unknown); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("unknown task action error = %v", err)
	}
}

func TestControlCodecsRejectCorruptedJSONAfterValidHeader(t *testing.T) {
	badJSON := []byte("{")
	frames := []struct {
		name   string
		kind   uint8
		decode func([]byte) error
	}{
		{name: "raft batch", kind: controlKindRaftBatch, decode: func(frame []byte) error { _, err := DecodeRaftBatch(frame); return err }},
		{name: "state sync request", kind: controlKindStateSyncRequest, decode: func(frame []byte) error { _, err := DecodeStateSyncRequest(frame); return err }},
		{name: "state sync response", kind: controlKindStateSyncResponse, decode: func(frame []byte) error { _, err := DecodeStateSyncResponse(frame); return err }},
		{name: "task request", kind: controlKindTaskRequest, decode: func(frame []byte) error { _, err := DecodeTaskRequest(frame); return err }},
		{name: "control write request", kind: controlKindWriteRequest, decode: func(frame []byte) error { _, err := DecodeControlWriteRequest(frame); return err }},
		{name: "control write response", kind: controlKindWriteResponse, decode: func(frame []byte) error { _, err := DecodeControlWriteResponse(frame); return err }},
	}
	for _, tt := range frames {
		t.Run(tt.name, func(t *testing.T) {
			frame := append(clusternet.PutHeader(nil, controlRPCVersion, tt.kind), badJSON...)
			if err := tt.decode(frame); err == nil {
				t.Fatal("decoder error = nil for corrupted JSON")
			}
		})
	}

	plain, err := encodeControlWriteResponseEnvelope(controlWriteResponseEnvelope{Error: "remote detail", ErrorCode: "future_code"})
	if err != nil {
		t.Fatalf("encodeControlWriteResponseEnvelope() error = %v", err)
	}
	if _, err := DecodeControlWriteResponse(plain); err == nil || err.Error() != "remote detail" {
		t.Fatalf("unknown semantic code error = %v, want plain remote detail", err)
	}
	encoded, err := encodeControlWriteErrorResponse(nil)
	if err != nil {
		t.Fatalf("encodeControlWriteErrorResponse(nil) error = %v", err)
	}
	if _, err := DecodeControlWriteResponse(encoded); err != nil {
		t.Fatalf("empty error response decode = %v", err)
	}
}

func TestStaticPeerPickerPreservesConfiguredOrderAndOwnsReturnedIDs(t *testing.T) {
	caller := &runtimeContractCaller{}
	picker := NewStaticPeerPicker(caller, []RuntimeVoter{
		{NodeID: 3, Addr: "n3"},
		{NodeID: 1, Addr: "n1"},
		{NodeID: 2, Addr: "n2"},
	})

	ids := picker.PeerIDs()
	if !reflect.DeepEqual(ids, []uint64{3, 1, 2}) {
		t.Fatalf("PeerIDs() = %v, want configured order", ids)
	}
	ids[0] = 99
	if got := picker.PeerIDs(); got[0] != 3 {
		t.Fatalf("PeerIDs() returned aliased slice: %v", got)
	}
	endpoint, ok := picker.Endpoint(1)
	if !ok {
		t.Fatal("Endpoint(1) missing")
	}
	remote, ok := endpoint.(*StateSyncEndpoint)
	if !ok || remote.nodeID != 1 || remote.caller != caller {
		t.Fatalf("Endpoint(1) = %#v, want caller-bound node 1 endpoint", endpoint)
	}
	if _, ok := picker.Endpoint(99); ok {
		t.Fatal("Endpoint(99) unexpectedly found")
	}
	var nilPicker *StaticPeerPicker
	if endpoint, ok := nilPicker.Endpoint(1); ok || endpoint != nil {
		t.Fatalf("nil Endpoint() = %#v, %v", endpoint, ok)
	}
	if ids := nilPicker.PeerIDs(); ids != nil {
		t.Fatalf("nil PeerIDs() = %#v, want nil", ids)
	}
}

type boundaryControlWriteApplier struct {
	actions []ControlWriteAction
	err     error
}

func (a *boundaryControlWriteApplier) record(action ControlWriteAction) error {
	a.actions = append(a.actions, action)
	return a.err
}

func (a *boundaryControlWriteApplier) ReportNode(context.Context, NodeReport) error {
	return a.record(ControlWriteActionReportNodeHealth)
}

func (a *boundaryControlWriteApplier) JoinNode(context.Context, JoinNodeRequest) (JoinNodeResult, error) {
	err := a.record(ControlWriteActionJoinNode)
	return JoinNodeResult{Created: true, Node: controlNodeFromControllerNode(sourceControllerNode()), Revision: 11}, err
}

func (a *boundaryControlWriteApplier) ActivateNode(context.Context, ActivateNodeRequest) (ActivateNodeResult, error) {
	err := a.record(ControlWriteActionActivateNode)
	return ActivateNodeResult{Changed: true, Node: controlNodeFromControllerNode(sourceControllerNode()), Revision: 12}, err
}

func (a *boundaryControlWriteApplier) MarkNodeLeaving(context.Context, MarkNodeLeavingRequest) (MarkNodeLeavingResult, error) {
	err := a.record(ControlWriteActionMarkNodeLeaving)
	return MarkNodeLeavingResult{Changed: true, Node: controlNodeFromControllerNode(sourceControllerNode()), Revision: 13}, err
}

func (a *boundaryControlWriteApplier) MarkNodeRemoved(context.Context, MarkNodeRemovedRequest) (MarkNodeRemovedResult, error) {
	err := a.record(ControlWriteActionMarkNodeRemoved)
	return MarkNodeRemovedResult{Changed: true, Node: controlNodeFromControllerNode(sourceControllerNode()), Revision: 14}, err
}

func (a *boundaryControlWriteApplier) RequestSlotLeaderTransfer(context.Context, SlotLeaderTransferRequest) (SlotLeaderTransferResult, error) {
	err := a.record(ControlWriteActionSlotLeaderTransfer)
	task := ReconcileTask{TaskID: "transfer", SlotID: 1, Kind: TaskKindLeaderTransfer, Step: TaskStepTransferLeader, Status: TaskStatusPending}
	return SlotLeaderTransferResult{Created: true, Task: &task}, err
}

func (a *boundaryControlWriteApplier) RequestSlotReplicaMove(context.Context, SlotReplicaMoveRequest) (SlotReplicaMoveResult, error) {
	err := a.record(ControlWriteActionSlotReplicaMove)
	task := ReconcileTask{TaskID: "move", SlotID: 1, Kind: TaskKindSlotReplicaMove, Step: TaskStepOpenLearner, Status: TaskStatusPending}
	return SlotReplicaMoveResult{Created: true, Task: &task}, err
}

func (a *boundaryControlWriteApplier) PromoteControllerVoter(context.Context, PromoteControllerVoterRequest) (PromoteControllerVoterResult, error) {
	err := a.record(ControlWriteActionPromoteControllerVoter)
	return PromoteControllerVoterResult{Changed: true, Node: controlNodeFromControllerNode(sourceControllerNode()), Revision: 15}, err
}

func (a *boundaryControlWriteApplier) ReplaceScheduledBackupState(context.Context, uint64, controller.ScheduledBackupState) error {
	return a.record(ControlWriteActionReplaceScheduledBackup)
}

func (a *boundaryControlWriteApplier) ReplaceOpsMCPState(context.Context, uint64, controller.OpsMCPState) error {
	return a.record(ControlWriteActionReplaceOpsMCP)
}

// requiredControlWriteApplier intentionally exposes only the required protocol
// surface so optional mutation capability negotiation can be verified.
type requiredControlWriteApplier struct {
}

func (*requiredControlWriteApplier) ReportNode(context.Context, NodeReport) error { return nil }
func (*requiredControlWriteApplier) JoinNode(context.Context, JoinNodeRequest) (JoinNodeResult, error) {
	return JoinNodeResult{}, nil
}
func (*requiredControlWriteApplier) ActivateNode(context.Context, ActivateNodeRequest) (ActivateNodeResult, error) {
	return ActivateNodeResult{}, nil
}
func (*requiredControlWriteApplier) MarkNodeLeaving(context.Context, MarkNodeLeavingRequest) (MarkNodeLeavingResult, error) {
	return MarkNodeLeavingResult{}, nil
}
func (*requiredControlWriteApplier) MarkNodeRemoved(context.Context, MarkNodeRemovedRequest) (MarkNodeRemovedResult, error) {
	return MarkNodeRemovedResult{}, nil
}
func (*requiredControlWriteApplier) RequestSlotLeaderTransfer(context.Context, SlotLeaderTransferRequest) (SlotLeaderTransferResult, error) {
	return SlotLeaderTransferResult{}, nil
}
func (*requiredControlWriteApplier) RequestSlotReplicaMove(context.Context, SlotReplicaMoveRequest) (SlotReplicaMoveResult, error) {
	return SlotReplicaMoveResult{}, nil
}
func (*requiredControlWriteApplier) PromoteControllerVoter(context.Context, PromoteControllerVoterRequest) (PromoteControllerVoterResult, error) {
	return PromoteControllerVoterResult{}, nil
}

type boundaryTaskApplier struct {
	actions []TaskAction
	err     error
}

func (a *boundaryTaskApplier) record(action TaskAction) error {
	a.actions = append(a.actions, action)
	return a.err
}

func (a *boundaryTaskApplier) CompleteTask(context.Context, TaskResult) error {
	return a.record(TaskActionComplete)
}

func (a *boundaryTaskApplier) FailTask(context.Context, TaskResult) error {
	return a.record(TaskActionFail)
}

func (a *boundaryTaskApplier) ReportTaskProgress(context.Context, TaskProgress) error {
	return a.record(TaskActionProgress)
}

func (a *boundaryTaskApplier) RequestSlotLeaderTransfer(context.Context, SlotLeaderTransferRequest) (SlotLeaderTransferResult, error) {
	return SlotLeaderTransferResult{}, a.record(TaskActionLeaderTransfer)
}

func (a *boundaryTaskApplier) AdvanceSlotReplicaMovePhase(context.Context, SlotReplicaMovePhaseAdvance) error {
	return a.record(TaskActionReplicaMovePhase)
}

func (a *boundaryTaskApplier) CommitSlotReplicaMove(context.Context, SlotReplicaMoveCommit) error {
	return a.record(TaskActionReplicaMoveCommit)
}
