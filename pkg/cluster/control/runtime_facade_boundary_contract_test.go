package control

import (
	"context"
	"errors"
	"testing"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRuntimeFacadeOperationsHonorPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runtime := &Runtime{}

	operations := []struct {
		name string
		run  func() error
	}{
		{name: "start", run: func() error { return runtime.Start(ctx) }},
		{name: "stop", run: func() error { return runtime.Stop(ctx) }},
		{name: "local snapshot", run: func() error { _, err := runtime.LocalSnapshot(ctx); return err }},
		{name: "probe propose", run: func() error { return runtime.ProbePropose(ctx) }},
		{name: "report node", run: func() error { return runtime.ReportNode(ctx, NodeReport{}) }},
		{name: "report slots", run: func() error { return runtime.ReportSlots(ctx, SlotRuntimeReport{}) }},
		{name: "complete task", run: func() error { return runtime.CompleteTask(ctx, TaskResult{}) }},
		{name: "fail task", run: func() error { return runtime.FailTask(ctx, TaskResult{}) }},
		{name: "report task progress", run: func() error { return runtime.ReportTaskProgress(ctx, TaskProgress{}) }},
		{name: "advance replica move", run: func() error { return runtime.AdvanceSlotReplicaMovePhase(ctx, SlotReplicaMovePhaseAdvance{}) }},
		{name: "commit replica move", run: func() error { return runtime.CommitSlotReplicaMove(ctx, SlotReplicaMoveCommit{}) }},
		{name: "request leader transfer", run: func() error {
			_, err := runtime.RequestSlotLeaderTransfer(ctx, SlotLeaderTransferRequest{})
			return err
		}},
		{name: "request replica move", run: func() error { _, err := runtime.RequestSlotReplicaMove(ctx, SlotReplicaMoveRequest{}); return err }},
		{name: "promote controller voter", run: func() error {
			_, err := runtime.PromoteControllerVoter(ctx, PromoteControllerVoterRequest{})
			return err
		}},
		{name: "join node", run: func() error { _, err := runtime.JoinNode(ctx, JoinNodeRequest{}); return err }},
		{name: "activate node", run: func() error { _, err := runtime.ActivateNode(ctx, ActivateNodeRequest{}); return err }},
		{name: "mark node leaving", run: func() error { _, err := runtime.MarkNodeLeaving(ctx, MarkNodeLeavingRequest{}); return err }},
		{name: "mark node removed", run: func() error { _, err := runtime.MarkNodeRemoved(ctx, MarkNodeRemovedRequest{}); return err }},
		{name: "prepare controller voter", run: func() error {
			_, err := runtime.PrepareControllerVoter(ctx, controller.PrepareControllerVoterRequest{})
			return err
		}},
		{name: "local controller state", run: func() error { _, err := runtime.LocalControllerState(ctx); return err }},
		{name: "replace scheduled backup", run: func() error { return runtime.ReplaceScheduledBackupState(ctx, 1, controller.ScheduledBackupState{}) }},
		{name: "replace ops MCP", run: func() error { return runtime.ReplaceOpsMCPState(ctx, 1, controller.OpsMCPState{}) }},
		{name: "controller log entries", run: func() error { _, err := runtime.ControllerLogEntries(ctx, ControllerLogEntriesOptions{}); return err }},
		{name: "controller raft status", run: func() error { _, err := runtime.ControllerRaftStatus(ctx); return err }},
		{name: "compact controller raft log", run: func() error { _, err := runtime.CompactControllerRaftLog(ctx); return err }},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestRuntimeZeroBackendReadFacadeIsSafeAndImmutable(t *testing.T) {
	snapshot := validSnapshot()
	runtime := &Runtime{snapshot: snapshot, watch: make(chan SnapshotEvent, 1)}

	got, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot() error = %v", err)
	}
	got.Nodes[0].Roles[0] = "changed"
	got.Slots[0].DesiredPeers[0] = 99
	if runtime.snapshot.Nodes[0].Roles[0] != RoleController || runtime.snapshot.Slots[0].DesiredPeers[0] != 1 {
		t.Fatalf("LocalSnapshot() aliased runtime state: %#v", runtime.snapshot)
	}
	if leader := runtime.LeaderID(); leader != snapshot.ControllerID {
		t.Fatalf("LeaderID() = %d, want %d", leader, snapshot.ControllerID)
	}
	if err := runtime.Step(context.Background(), raftpb.Message{}); err != nil {
		t.Fatalf("Step() error = %v, want nil for unhosted controller", err)
	}
	state, err := runtime.GetState(context.Background(), controller.GetStateRequest{})
	if err != nil || !state.NotReady {
		t.Fatalf("GetState() = %#v, %v; want not ready", state, err)
	}
	if runtime.Watch() != runtime.watch {
		t.Fatal("Watch() did not return the runtime event stream")
	}
	if err := runtime.ReportSlots(context.Background(), SlotRuntimeReport{NodeID: 1}); err != nil {
		t.Fatalf("ReportSlots() error = %v, want explicit best-effort no-op", err)
	}
	if err := runtime.ReportNode(context.Background(), NodeReport{NodeID: 1}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("ReportNode() error = %v, want ErrNotStarted", err)
	}
	if err := runtime.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil, want missing backend rejection")
	}
}

func TestRuntimeAllocatedButNotStartedPreservesControllerAvailabilityErrors(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "n1",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-not-started-boundaries",
		Role:             RuntimeRoleVoter,
		Voters:           []RuntimeVoter{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	runtime.snapshot = validSnapshot()

	got, err := runtime.LocalSnapshot(context.Background())
	if err != nil || got.Revision != runtime.snapshot.Revision {
		t.Fatalf("LocalSnapshot() = revision %d, error %v; want cached last-good snapshot", got.Revision, err)
	}
	if leader := runtime.LeaderID(); leader != 0 {
		t.Fatalf("LeaderID() = %d, want unstarted backend leader 0", leader)
	}

	operations := []struct {
		name string
		run  func() error
	}{
		{name: "report node", run: func() error { return runtime.ReportNode(context.Background(), NodeReport{NodeID: 1}) }},
		{name: "slot leader transfer", run: func() error {
			_, err := runtime.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1, SourceNode: 1, TargetNode: 2, ConfigEpoch: 3, StateRevision: 7})
			return err
		}},
		{name: "slot replica move", run: func() error {
			_, err := runtime.RequestSlotReplicaMove(context.Background(), SlotReplicaMoveRequest{SlotID: 1, SourceNode: 2, TargetNode: 3, ConfigEpoch: 3, StateRevision: 7})
			return err
		}},
		{name: "promote controller voter", run: func() error {
			_, err := runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 2, ExpectedRevision: 7, ExpectedVoters: []uint64{1}})
			return err
		}},
		{name: "replace scheduled backup", run: func() error {
			return runtime.ReplaceScheduledBackupState(context.Background(), 7, controller.ScheduledBackupState{})
		}},
		{name: "replace ops MCP", run: func() error {
			return runtime.ReplaceOpsMCPState(context.Background(), 7, controller.OpsMCPState{OwnerNodeID: 1})
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, controller.ErrNotStarted) {
				t.Fatalf("operation error = %v, want ErrNotStarted", err)
			}
		})
	}
}

func TestRuntimePublishesOnlyValidAdaptedControllerState(t *testing.T) {
	watch := make(chan SnapshotEvent, 1)
	runtime := &Runtime{watch: watch, publisher: newSnapshotWatchPublisher(watch)}
	state := controllerState()

	if err := runtime.publishState(state); err != nil {
		t.Fatalf("publishState() error = %v", err)
	}
	state.Nodes[0].Addr = "mutated-after-publish"
	if runtime.snapshot.Revision != 7 || runtime.snapshot.Nodes[0].Addr == "mutated-after-publish" {
		t.Fatalf("published snapshot = %#v, want detached revision 7", runtime.snapshot)
	}
	select {
	case event := <-watch:
		if event.Snapshot.Revision != 7 {
			t.Fatalf("event revision = %d, want 7", event.Snapshot.Revision)
		}
	default:
		t.Fatal("publishState() did not emit snapshot event")
	}

	invalid := controllerState()
	invalid.Revision = 8
	invalid.HashSlots.Ranges = nil
	if err := runtime.publishState(invalid); err == nil {
		t.Fatal("publishState(invalid) error = nil")
	}
	if runtime.snapshot.Revision != 7 {
		t.Fatalf("invalid publication changed revision to %d, want 7", runtime.snapshot.Revision)
	}
}

func TestRuntimeControlMappingPreservesFencesAndOwnsSlices(t *testing.T) {
	voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}}
	mappedVoters := runtimeFacadeVoters(voters)
	voters[0].Addr = "changed"
	if mappedVoters[0].NodeID != 1 || mappedVoters[0].Addr != "n1" {
		t.Fatalf("runtimeFacadeVoters() = %#v, want detached voter mapping", mappedVoters)
	}

	source := controllerState().Tasks[0]
	source.ObservedVoters = []uint64{1, 2}
	source.ObservedLearners = []uint64{3}
	task := reconcileTaskFromController(source)
	source.TargetPeers[0] = 99
	source.ObservedVoters[0] = 99
	source.ObservedLearners[0] = 99
	if task.TargetPeers[0] == 99 || task.ObservedVoters[0] == 99 || task.ObservedLearners[0] == 99 {
		t.Fatalf("reconcileTaskFromController() aliased source slices: %#v", task)
	}
	if task.TaskID == "" || task.ConfigEpoch != controllerState().Tasks[0].ConfigEpoch || task.PhaseIndex != controllerState().Tasks[0].PhaseIndex {
		t.Fatalf("reconcileTaskFromController() lost task fences: %#v", task)
	}

	join := controller.JoinNodeResult{Created: true, Node: sourceControllerNode(), Revision: 11}
	if got := joinNodeResultFromController(join); !got.Created || got.Revision != 11 || got.Node.NodeID != 4 {
		t.Fatalf("joinNodeResultFromController() = %#v", got)
	}
	activate := controller.ActivateNodeResult{Changed: true, Node: sourceControllerNode(), Revision: 12}
	if got := activateNodeResultFromController(activate); !got.Changed || got.Revision != 12 || got.Node.JoinState != NodeJoinStateActive {
		t.Fatalf("activateNodeResultFromController() = %#v", got)
	}
	leaving := controller.MarkNodeLeavingResult{Changed: true, Node: sourceControllerNode(), Revision: 13}
	if got := markNodeLeavingResultFromController(leaving); !got.Changed || got.Revision != 13 {
		t.Fatalf("markNodeLeavingResultFromController() = %#v", got)
	}
	removed := controller.MarkNodeRemovedResult{Changed: true, Node: sourceControllerNode(), Revision: 14}
	if got := markNodeRemovedResultFromController(removed); !got.Changed || got.Revision != 14 {
		t.Fatalf("markNodeRemovedResultFromController() = %#v", got)
	}

	if got := copyOptionalUint64Slice(nil); got != nil {
		t.Fatalf("copyOptionalUint64Slice(nil) = %#v, want nil", got)
	}
	empty := []uint64{}
	if got := copyOptionalUint64Slice(empty); got == nil || len(got) != 0 {
		t.Fatalf("copyOptionalUint64Slice(empty) = %#v, want non-nil empty", got)
	}
	if warnings := controllerVoterPromotionWarnings([]uint64{1, 2, 3}); warnings != nil {
		t.Fatalf("odd voter warnings = %#v, want nil", warnings)
	}
}

func TestRuntimeEmptyControllerStateClassification(t *testing.T) {
	if !emptyControllerState(controller.ClusterState{}) {
		t.Fatal("emptyControllerState(zero) = false")
	}
	fields := []controller.ClusterState{
		{SchemaVersion: 1},
		{ClusterID: "cluster"},
		{Revision: 1},
		{AppliedRaftIndex: 1},
		{Controllers: []controller.ControllerVoter{{NodeID: 1}}},
		{Nodes: []controller.Node{{NodeID: 1}}},
		{NodeHealthReports: []controller.NodeHealthReport{{NodeID: 1}}},
		{Slots: []controller.SlotAssignment{{SlotID: 1}}},
		{HashSlots: controller.HashSlotTable{SlotCount: 1}},
		{HashSlots: controller.HashSlotTable{Ranges: []controller.HashSlotRange{{SlotID: 1}}}},
		{Tasks: []controller.ReconcileTask{{TaskID: "task"}}},
	}
	for i, state := range fields {
		if emptyControllerState(state) {
			t.Fatalf("emptyControllerState(non-empty case %d) = true: %#v", i, state)
		}
	}
}

func TestRuntimeForwardingHelpersRequireRemoteLeaderAndPreservePayload(t *testing.T) {
	response, err := EncodeControlWriteResponse(ControlWriteResponse{ActivateNode: ActivateNodeResult{Changed: true, Revision: 9}})
	if err != nil {
		t.Fatalf("EncodeControlWriteResponse() error = %v", err)
	}
	caller := &runtimeContractCaller{response: response}
	runtime := &Runtime{
		cfg:         RuntimeConfig{NodeID: 1},
		snapshot:    Snapshot{ControllerID: 2},
		taskClient:  NewTaskClient(caller),
		writeClient: NewControlWriteClient(caller),
	}
	if !runtime.canForwardControlWriteToLeader() {
		t.Fatal("canForwardControlWriteToLeader() = false, want remote leader forwarding")
	}
	if err := runtime.forwardTaskRequest(context.Background(), TaskRequest{Action: TaskActionComplete, Result: TaskResult{TaskID: "task-1"}}); err != nil {
		t.Fatalf("forwardTaskRequest() error = %v", err)
	}
	got, err := runtime.forwardControlWrite(context.Background(), ControlWriteRequest{Action: ControlWriteActionActivateNode, ActivateNode: ActivateNodeRequest{NodeID: 4}})
	if err != nil || !got.ActivateNode.Changed {
		t.Fatalf("forwardControlWrite() = %#v, %v", got, err)
	}
	if len(caller.calls) != 2 || caller.calls[0].nodeID != 2 || caller.calls[0].serviceID != clusternet.RPCControlTaskResult || caller.calls[1].serviceID != clusternet.RPCControlWrite {
		t.Fatalf("forwarded calls = %#v, want task then control write to node 2", caller.calls)
	}
	decodedTask, err := DecodeTaskRequest(caller.calls[0].payload)
	if err != nil || decodedTask.Result.TaskID != "task-1" {
		t.Fatalf("forwarded task = %#v, %v", decodedTask, err)
	}

	fallback := errors.New("original failure")
	missing := &Runtime{cfg: RuntimeConfig{NodeID: 1}, snapshot: Snapshot{ControllerID: 2}}
	if err := missing.forwardTaskRequest(context.Background(), TaskRequest{}); !errors.Is(err, controller.ErrNotLeader) {
		t.Fatalf("forwardTaskRequest(no client) error = %v, want ErrNotLeader", err)
	}
	if _, err := missing.forwardControlWrite(context.Background(), ControlWriteRequest{}); !errors.Is(err, controller.ErrNotLeader) {
		t.Fatalf("forwardControlWrite(no client) error = %v, want ErrNotLeader", err)
	}
	if _, err := missing.forwardControlWriteAfterError(context.Background(), ControlWriteRequest{}, fallback); !errors.Is(err, fallback) {
		t.Fatalf("forwardControlWriteAfterError(no client) error = %v, want original failure", err)
	}
	if !errors.Is(fallbackControlWriteError(nil), controller.ErrNotLeader) || !errors.Is(fallbackControlWriteError(fallback), fallback) {
		t.Fatal("fallbackControlWriteError() did not preserve fallback semantics")
	}
	if !shouldForwardTaskWrite(controller.ErrNotLeader) || !shouldForwardTaskWrite(controller.ErrNotStarted) || shouldForwardTaskWrite(fallback) {
		t.Fatal("shouldForwardTaskWrite() classification mismatch")
	}
	if !shouldForwardControlWrite(controller.ErrNotLeader) || !shouldForwardControlWrite(controller.ErrNotStarted) || shouldForwardControlWrite(fallback) {
		t.Fatal("shouldForwardControlWrite() classification mismatch")
	}

	localLeader := &Runtime{cfg: RuntimeConfig{NodeID: 2}, snapshot: Snapshot{ControllerID: 2}, taskClient: NewTaskClient(caller), writeClient: NewControlWriteClient(caller)}
	if localLeader.canForwardControlWriteToLeader() {
		t.Fatal("canForwardControlWriteToLeader() = true for local leader")
	}
	if err := localLeader.forwardTaskRequest(context.Background(), TaskRequest{}); !errors.Is(err, controller.ErrNotLeader) {
		t.Fatalf("forwardTaskRequest(local leader) error = %v", err)
	}
	if _, err := localLeader.forwardControlWrite(context.Background(), ControlWriteRequest{}); !errors.Is(err, controller.ErrNotLeader) {
		t.Fatalf("forwardControlWrite(local leader) error = %v", err)
	}
	if _, err := localLeader.forwardControlWriteAfterError(context.Background(), ControlWriteRequest{}, fallback); !errors.Is(err, fallback) {
		t.Fatalf("forwardControlWriteAfterError(local leader) error = %v", err)
	}
}

func sourceControllerNode() controller.Node {
	return controller.Node{
		NodeID:         4,
		Addr:           "n4",
		Roles:          []controller.NodeRole{controller.NodeRoleData},
		JoinState:      controller.NodeJoinStateActive,
		Status:         controller.NodeStatusAlive,
		CapacityWeight: 2,
	}
}

type runtimeContractCall struct {
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

type runtimeContractCaller struct {
	calls    []runtimeContractCall
	response []byte
	err      error
}

func (c *runtimeContractCaller) Call(_ context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	c.calls = append(c.calls, runtimeContractCall{nodeID: nodeID, serviceID: serviceID, payload: append([]byte(nil), payload...)})
	return append([]byte(nil), c.response...), c.err
}

var _ clusternet.Caller = (*runtimeContractCaller)(nil)
