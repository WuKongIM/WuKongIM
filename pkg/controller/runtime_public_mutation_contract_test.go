package controller

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
	"github.com/WuKongIM/WuKongIM/pkg/controller/server"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	"github.com/stretchr/testify/require"
)

func TestRuntimeNodeLifecyclePublicAPIIsIdempotentForSettledState(t *testing.T) {
	state := runtimeContractState(t, 12)
	state.Nodes = append(state.Nodes,
		Node{NodeID: 2, Addr: "n2", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateLeaving, Status: NodeStatusAlive, CapacityWeight: 1},
		Node{NodeID: 3, Addr: "n3", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateRemoved, Status: NodeStatusDown, CapacityWeight: 1},
		Node{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateJoining, Status: NodeStatusAlive, CapacityWeight: 1},
	)
	runtime := &Runtime{state: state.Clone(), raft: &controllerraft.Service{}}

	joined, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4"})
	require.NoError(t, err)
	require.False(t, joined.Created)
	require.Equal(t, uint64(12), joined.Revision)
	require.Equal(t, NodeJoinStateJoining, joined.Node.JoinState)

	activated, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 1})
	require.NoError(t, err)
	require.False(t, activated.Changed)
	require.Equal(t, NodeJoinStateActive, activated.Node.JoinState)

	leaving, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 2})
	require.NoError(t, err)
	require.False(t, leaving.Changed)
	require.Equal(t, NodeJoinStateLeaving, leaving.Node.JoinState)

	removed, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 3, ExpectedRevision: 1})
	require.NoError(t, err)
	require.False(t, removed.Changed, "an existing tombstone must remain idempotent across stale retries")
	require.Equal(t, NodeJoinStateRemoved, removed.Node.JoinState)

	visible, err := runtime.LocalState(context.Background())
	require.NoError(t, err)
	require.Equal(t, state, visible)
}

func TestRuntimeNodeLifecycleRejectsInvalidIntentBeforeRaftProposal(t *testing.T) {
	state := runtimeContractState(t, 12)
	state.Nodes = append(state.Nodes, Node{
		NodeID: 2, Addr: "n2", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 1,
	})
	runtime := &Runtime{state: state.Clone(), raft: &controllerraft.Service{}}

	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 2, Addr: "other"})
	require.ErrorIs(t, err, ErrNodeLifecycleConflict)
	_, err = runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 99})
	require.ErrorIs(t, err, ErrNodeLifecycleNotFound)
	_, err = runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 1})
	require.ErrorIs(t, err, ErrNodeLifecycleConflict)
	_, err = runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 2})
	require.ErrorIs(t, err, ErrNodeLifecycleConflict)
}

func TestRuntimeMutationCommandsPropagateUnavailableRaftWithoutPublishingState(t *testing.T) {
	now := time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC)
	state := runtimeContractState(t, 12)
	state.Nodes = append(state.Nodes,
		Node{NodeID: 2, Addr: "n2", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateLeaving, Status: NodeStatusAlive, CapacityWeight: 1},
		Node{NodeID: 3, Addr: "n3", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateJoining, Status: NodeStatusAlive, CapacityWeight: 1},
		Node{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 1},
	)
	runtime := &Runtime{
		cfg:   RuntimeConfig{Now: func() time.Time { return now }},
		state: state.Clone(), raft: &controllerraft.Service{}, watch: make(chan StateEvent, 1),
	}

	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 5, Addr: "n5"})
	require.ErrorIs(t, err, ErrNotStarted)
	_, err = runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 3})
	require.ErrorIs(t, err, ErrNotStarted)
	_, err = runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})
	require.ErrorIs(t, err, ErrNotStarted)
	_, err = runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 2, ExpectedRevision: state.Revision})
	require.ErrorIs(t, err, ErrNotStarted)
	_, err = runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 2, ExpectedRevision: state.Revision - 1})
	require.ErrorIs(t, err, ErrExpectedRevisionMismatch)

	_, err = runtime.ReportNodeHealth(context.Background(), ReportNodeHealthRequest{NodeID: 4, Status: NodeStatusAlive, RuntimeReady: true})
	require.ErrorIs(t, err, ErrNotStarted)
	require.ErrorIs(t, runtime.ReplaceScheduledBackupState(context.Background(), state.Revision, ScheduledBackupState{}), ErrNotStarted)
	require.ErrorIs(t, runtime.ReplaceOpsMCPState(context.Background(), state.Revision, OpsMCPState{}), ErrNotStarted)
	require.ErrorIs(t, runtime.CompleteTask(context.Background(), TaskResult{TaskID: "task-1"}), ErrNotStarted)
	require.ErrorIs(t, runtime.FailTask(context.Background(), TaskResult{TaskID: "task-1"}), ErrNotStarted)
	require.ErrorIs(t, runtime.ReportTaskProgress(context.Background(), TaskProgress{TaskID: "task-1"}), ErrNotStarted)
	require.ErrorIs(t, runtime.AdvanceSlotReplicaMovePhase(context.Background(), SlotReplicaMovePhaseAdvance{TaskID: "task-1"}), ErrNotStarted)
	require.ErrorIs(t, runtime.CommitSlotReplicaMove(context.Background(), SlotReplicaMoveCommit{TaskID: "task-1"}), ErrNotStarted)

	visible, err := runtime.LocalState(context.Background())
	require.NoError(t, err)
	require.Equal(t, state, visible)
	require.Empty(t, runtime.watch)
}

func TestRuntimeSlotRequestsPreserveIdempotencyAndTopologyFences(t *testing.T) {
	state := runtimeContractState(t, 12)
	state.Nodes = append(state.Nodes, Node{
		NodeID: 2, Addr: "n2", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 1,
	})
	state.Slots[0] = SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 4, PreferredLeader: 2}
	state.Tasks = []ReconcileTask{{
		TaskID: "existing-transfer", SlotID: 1, Kind: TaskKindLeaderTransfer, Step: TaskStepTransferLeader,
		SourceNode: 1, TargetNode: 2, TargetPeers: []uint64{2, 1}, ConfigEpoch: 4,
		CompletionPolicy: TaskCompletionPolicySingleObserver, Status: TaskStatusPending,
	}}
	runtime := &Runtime{cfg: RuntimeConfig{Now: time.Now}, state: state.Clone(), raft: &controllerraft.Service{}}

	transfer, err := runtime.RequestSlotLeaderTransfer(context.Background(), SlotLeaderTransferRequest{
		SlotID: 1, SourceNode: 1, TargetNode: 2, TargetPeers: []uint64{1, 2}, ConfigEpoch: 4, StateRevision: 12,
	})
	require.NoError(t, err)
	require.False(t, transfer.Created)
	require.Equal(t, "existing-transfer", transfer.Task.TaskID)

	_, err = runtime.RequestSlotReplicaMove(context.Background(), SlotReplicaMoveRequest{SlotID: 99})
	require.ErrorContains(t, err, "assignment not found")

	state.Tasks = nil
	state.Slots[0] = SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 4, PreferredLeader: 1}
	runtime.state = state.Clone()
	move, err := runtime.RequestSlotReplicaMove(context.Background(), SlotReplicaMoveRequest{
		SlotID: 1, SourceNode: 1, TargetNode: 2, TargetPeers: []uint64{2}, ConfigEpoch: 4, StateRevision: 12,
	})
	require.ErrorIs(t, err, ErrNotStarted)
	require.Nil(t, move.Task)
}

func TestRuntimeControllerVoterPromotionRejectsUnprovenOrStaleIntent(t *testing.T) {
	state := runtimeContractState(t, 12)
	state.Nodes = append(state.Nodes, Node{
		NodeID: 2, Addr: "n2", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 1,
	})
	runtime := &Runtime{cfg: RuntimeConfig{Now: time.Now}, state: state.Clone(), raft: &controllerraft.Service{}}

	_, err := runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 99})
	require.ErrorIs(t, err, ErrNodeLifecycleNotFound)
	_, err = runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 2, ExpectedRevision: 11})
	require.ErrorIs(t, err, ErrExpectedRevisionMismatch)
	_, err = runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 2, ExpectedVoters: []uint64{2}})
	require.ErrorContains(t, err, fsm.ReasonControllerVoterSetMismatch)

	_, _, err = runtime.ensureControllerRaftVoter(context.Background(), 2)
	require.ErrorIs(t, err, ErrNotStarted)

	var nilRuntime *Runtime
	_, err = nilRuntime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 2})
	require.ErrorIs(t, err, ErrNotStarted)
}

func TestRuntimePrepareControllerVoterRecognizesAlreadyPreparedDurableState(t *testing.T) {
	state := runtimeContractState(t, 12)
	dir := t.TempDir()
	store := statefile.New(filepath.Join(dir, "cluster-state.json"))
	require.NoError(t, store.Save(context.Background(), state))
	sm, err := fsm.New(store)
	require.NoError(t, err)
	require.NoError(t, sm.Load(context.Background()))
	runtime := &Runtime{
		cfg: RuntimeConfig{
			NodeID: 1, Addr: "n1", StateDir: dir, ClusterID: state.ClusterID, Role: RuntimeRoleVoter,
			Voters: []Voter{{NodeID: 1, Addr: "n1"}}, Now: time.Now,
		},
		state: state.Clone(), store: store, sm: sm, raft: &controllerraft.Service{}, server: &server.Server{},
	}
	runtime.syncServer = runtime.newStateSyncServer()

	result, err := runtime.PrepareControllerVoter(context.Background(), PrepareControllerVoterRequest{
		NodeID: 1, ClusterID: state.ClusterID, ExpectedRevision: state.Revision,
		NextVoters: []Voter{{NodeID: 1, Addr: "n1"}},
	})
	require.NoError(t, err)
	require.True(t, result.Prepared)
	require.Equal(t, state.Revision, result.StateRevision)
	require.True(t, runtime.controllerVoterPrepared())
}

func TestRuntimePrepareControllerVoterValidatesIdentityBeforeMovingMirrorState(t *testing.T) {
	state := runtimeContractState(t, 12)
	dir := t.TempDir()
	require.NoError(t, statefile.New(filepath.Join(dir, "cluster-state.json")).Save(context.Background(), state))
	runtime := &Runtime{cfg: RuntimeConfig{
		NodeID: 2, Addr: "n2", StateDir: dir, ClusterID: state.ClusterID, Role: RuntimeRoleMirror,
		Voters: []Voter{{NodeID: 1, Addr: "n1"}},
	}}

	_, err := runtime.PrepareControllerVoter(context.Background(), PrepareControllerVoterRequest{NodeID: 3})
	require.ErrorContains(t, err, "node mismatch")
	_, err = runtime.PrepareControllerVoter(context.Background(), PrepareControllerVoterRequest{NodeID: 2, ClusterID: "other"})
	require.ErrorContains(t, err, "cluster mismatch")
	_, err = runtime.PrepareControllerVoter(context.Background(), PrepareControllerVoterRequest{NodeID: 2, ClusterID: state.ClusterID})
	require.ErrorContains(t, err, "requires next voters")

	_, err = runtime.PrepareControllerVoter(context.Background(), PrepareControllerVoterRequest{
		NodeID: 2, ClusterID: state.ClusterID, ExpectedRevision: state.Revision + 1,
		NextVoters: []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}},
	})
	require.ErrorIs(t, err, ErrExpectedRevisionMismatch)
	require.FileExists(t, filepath.Join(dir, "cluster-state.json"))

	var nilRuntime *Runtime
	_, err = nilRuntime.PrepareControllerVoter(context.Background(), PrepareControllerVoterRequest{})
	require.ErrorIs(t, err, ErrNotStarted)
}

func TestMirrorStateMoveKeepsSelectedBackupAndStopsOnlyMirrorLoops(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "cluster-state.json")
	backupPath := filepath.Join(dir, mirrorBeforeControllerVoterPromotionFile)
	require.NoError(t, statefile.New(activePath).Save(context.Background(), runtimeContractState(t, 8)))
	require.NoError(t, statefile.New(backupPath).Save(context.Background(), runtimeContractState(t, 9)))
	selection := mirrorStateSelection{
		active:   loadMirrorStateCandidate(context.Background(), activePath),
		backup:   loadMirrorStateCandidate(context.Background(), backupPath),
		selected: loadMirrorStateCandidate(context.Background(), backupPath),
	}
	require.NoError(t, moveMirrorStateAside(selection))
	require.NoFileExists(t, activePath)
	require.FileExists(t, backupPath)

	runtime := &Runtime{
		cfg:        RuntimeConfig{Role: RuntimeRoleMirror, TickInterval: time.Hour},
		syncClient: &SyncClient{},
	}
	runtime.startRefreshLoop()
	require.True(t, runtime.stopMirrorRefreshLoop())
	require.Nil(t, runtime.refreshCancel)
	runtime.restartMirrorRefreshLoopIfStillMirror()
	require.NotNil(t, runtime.refreshCancel)
	runtime.stopRefreshLoop()
	runtime.cfg.Role = RuntimeRoleVoter
	runtime.restartMirrorRefreshLoopIfStillMirror()
	require.Nil(t, runtime.refreshCancel)
}

func TestRuntimeFaultParsersRemainTargetedAndBounded(t *testing.T) {
	require.NoError(t, gofailMarkNodeRemovedPostCommitFault(" "))
	require.EqualError(t, gofailMarkNodeRemovedPostCommitFault("disk lost"), "controller: disk lost")
	require.NoError(t, gofailReportNodeHealthFault("other:ignored", 4))
	require.NoError(t, gofailReportNodeHealthFault("3:ignored", 4))
	require.EqualError(t, gofailReportNodeHealthFault("4: unhealthy ", 4), "controller: unhealthy")
	require.EqualError(t, gofailReportNodeHealthFault("all:", 4), "controller: node health report fault")
	require.EqualError(t, gofailReportNodeHealthFault("unstructured", 4), "controller: unstructured")
}
