package control

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
)

func TestRuntimeCanceledStopLeavesRaftTransportRetryable(t *testing.T) {
	raftTransport := NewRaftTransport(nil)
	runtime := &Runtime{cfg: RuntimeConfig{RaftTransport: raftTransport}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop(canceled) error = %v, want context canceled", err)
	}
	raftTransport.mu.RLock()
	stopped := raftTransport.stopped
	raftTransport.mu.RUnlock()
	if stopped {
		t.Fatal("canceled Stop permanently stopped the Raft transport")
	}

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop(retry) error = %v", err)
	}
	raftTransport.mu.RLock()
	stopped = raftTransport.stopped
	raftTransport.mu.RUnlock()
	if !stopped {
		t.Fatal("successful Stop left the Raft transport running")
	}
}

func TestRuntimeProbeProposeWithoutRaftReturnsNotStarted(t *testing.T) {
	var runtime Runtime
	if err := runtime.ProbePropose(context.Background()); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("ProbePropose() error = %v, want ErrNotStarted", err)
	}
}

func TestRuntimeTaskWritersWithoutBackendReturnNotStarted(t *testing.T) {
	var runtime Runtime
	if err := runtime.ReportTaskProgress(context.Background(), TaskProgress{TaskID: "bootstrap-1"}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("ReportTaskProgress() error = %v, want ErrNotStarted", err)
	}
	if err := runtime.CompleteTask(context.Background(), TaskResult{TaskID: "bootstrap-1"}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("CompleteTask() error = %v, want ErrNotStarted", err)
	}
	if err := runtime.FailTask(context.Background(), TaskResult{TaskID: "bootstrap-1"}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("FailTask() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{SlotID: 1}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v, want ErrNotStarted", err)
	}
	if err := runtime.AdvanceSlotReplicaMovePhase(context.Background(), SlotReplicaMovePhaseAdvance{TaskID: "move-1"}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("AdvanceSlotReplicaMovePhase() error = %v, want ErrNotStarted", err)
	}
	if err := runtime.CommitSlotReplicaMove(context.Background(), SlotReplicaMoveCommit{TaskID: "move-1"}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("CommitSlotReplicaMove() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.RequestSlotReplicaMove(context.Background(), SlotReplicaMoveRequest{SlotID: 1}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("RequestSlotReplicaMove() error = %v, want ErrNotStarted", err)
	}
	if err := runtime.ReplaceScheduledBackupState(context.Background(), 1, controller.ScheduledBackupState{}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("ReplaceScheduledBackupState() error = %v, want ErrNotStarted", err)
	}
	if err := runtime.ReplaceOpsMCPState(context.Background(), 1, controller.OpsMCPState{}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("ReplaceOpsMCPState() error = %v, want ErrNotStarted", err)
	}
}

func TestRuntimeReadOperationsWithoutBackendReturnNotStarted(t *testing.T) {
	var runtime Runtime
	if _, err := runtime.LocalControllerState(context.Background()); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("LocalControllerState() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.ControllerLogEntries(context.Background(), ControllerLogEntriesOptions{}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("ControllerLogEntries() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.ControllerRaftStatus(context.Background()); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("ControllerRaftStatus() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.CompactControllerRaftLog(context.Background()); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("CompactControllerRaftLog() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.PrepareControllerVoter(context.Background(), controller.PrepareControllerVoterRequest{}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("PrepareControllerVoter() error = %v, want ErrNotStarted", err)
	}
}

func TestRuntimeLifecycleWritesNotStartedWithoutForwardPreserveNotStarted(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "n1",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-lifecycle-not-started",
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
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 2, Addr: "n2"}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("JoinNode() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 2}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("ActivateNode() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 2}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("MarkNodeLeaving() error = %v, want ErrNotStarted", err)
	}
	if _, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 2}); !errors.Is(err, controller.ErrNotStarted) {
		t.Fatalf("MarkNodeRemoved() error = %v, want ErrNotStarted", err)
	}
}

func TestRuntimePrepareControllerVoterDelegatesToMaterializedBackend(t *testing.T) {
	stateDir := t.TempDir()
	mirrorState := controllerState()
	if err := statefile.New(filepath.Join(stateDir, "cluster-state.json")).Save(context.Background(), mirrorState); err != nil {
		t.Fatalf("Save(mirror state) error = %v", err)
	}
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:       2,
		Addr:         "127.0.0.1:1002",
		StateDir:     stateDir,
		ClusterID:    mirrorState.ClusterID,
		Role:         RuntimeRoleMirror,
		Voters:       []RuntimeVoter{{NodeID: 1, Addr: "127.0.0.1:1001"}, {NodeID: 2, Addr: "127.0.0.1:1002"}},
		TickInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	result, err := runtime.PrepareControllerVoter(context.Background(), controller.PrepareControllerVoterRequest{
		NodeID:           2,
		ClusterID:        mirrorState.ClusterID,
		ExpectedRevision: mirrorState.Revision,
		NextVoters: []controller.Voter{
			{NodeID: 1, Addr: "127.0.0.1:1001"},
			{NodeID: 2, Addr: "127.0.0.1:1002"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}
	if !result.Prepared || result.StateRevision != mirrorState.Revision {
		t.Fatalf("PrepareControllerVoter() = %#v, want prepared revision %d", result, mirrorState.Revision)
	}
}

func TestPromoteControllerVoterResultFromControllerAddsEvenVoterWarning(t *testing.T) {
	previous := []uint64{1, 2, 3}
	next := []uint64{1, 2, 3, 4}
	result := promoteControllerVoterResultFromController(controller.PromoteControllerVoterResult{
		Changed: true,
		Node: controller.Node{
			NodeID:         4,
			Addr:           "n4",
			Roles:          []controller.NodeRole{controller.NodeRoleData, controller.NodeRoleControllerVoter},
			JoinState:      controller.NodeJoinStateActive,
			Status:         controller.NodeStatusAlive,
			CapacityWeight: 1,
		},
		Revision:       10,
		PreviousVoters: previous,
		NextVoters:     next,
	})

	if !result.Changed || result.Node.NodeID != 4 || result.Node.Roles[1] != RoleController || result.Revision != 10 {
		t.Fatalf("promoteControllerVoterResultFromController() = %#v, want mapped controller node", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "controller_voter_count_even" {
		t.Fatalf("warnings = %#v, want controller_voter_count_even", result.Warnings)
	}
	result.PreviousVoters[0] = 99
	result.NextVoters[0] = 99
	if previous[0] != 1 || next[0] != 1 {
		t.Fatalf("promoteControllerVoterResultFromController did not copy voter slices: previous=%v next=%v", previous, next)
	}
}
