package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controller/server"
	cv2state "github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controller/sync"
)

func TestPublicClusterStateAliasSupportsStrongCompositeLiterals(t *testing.T) {
	st := ClusterState{
		SchemaVersion: CurrentSchemaVersion,
		ClusterID:     "cluster-a",
		Revision:      1,
		Config:        ClusterConfig{SlotCount: 1, HashSlotCount: 4, ReplicaCount: 1},
		Controllers:   []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}},
		Nodes: []Node{{
			NodeID:         1,
			Addr:           "n1",
			Roles:          []NodeRole{NodeRoleControllerVoter, NodeRoleData},
			JoinState:      NodeJoinStateActive,
			Status:         NodeStatusAlive,
			CapacityWeight: 1,
		}},
		Slots:     []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: HashSlotTable{Version: CurrentHashSlotTableVersion, SlotCount: 4, Ranges: []HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
	if err := st.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRuntimeSingleVoterBootstrapsStateEvent(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-single",
		Role:             RuntimeRoleVoter,
		Voters:           []Voter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	event := readStateEvent(t, runtime.Watch())
	if event.State.Revision == 0 || len(event.State.Slots) != 1 || event.State.HashSlots.SlotCount != 4 {
		t.Fatalf("StateEvent = %#v, want bootstrapped state", event)
	}
	state, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	if state.Revision != event.State.Revision || state.Controllers[0].NodeID != 1 {
		t.Fatalf("LocalState() = %#v, event = %#v", state, event.State)
	}
}

func TestRuntimeMirrorSyncsStateEvent(t *testing.T) {
	leader, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "n1",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-mirror",
		Role:             RuntimeRoleVoter,
		Voters:           []Voter{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime(leader) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := leader.Start(ctx); err != nil {
		t.Fatalf("Start(leader) error = %v", err)
	}
	t.Cleanup(func() { _ = leader.Stop(context.Background()) })
	_ = readStateEvent(t, leader.Watch())

	mirror, err := NewRuntime(RuntimeConfig{
		NodeID:    2,
		Addr:      "n2",
		StateDir:  t.TempDir(),
		ClusterID: "cluster-mirror",
		Role:      RuntimeRoleMirror,
		Voters:    []Voter{{NodeID: 1, Addr: "n1"}},
		SyncPeers: fixedPeerPicker{ids: []uint64{1}, endpoints: map[uint64]Endpoint{1: leader}},
	})
	if err != nil {
		t.Fatalf("NewRuntime(mirror) error = %v", err)
	}
	if err := mirror.Start(context.Background()); err != nil {
		t.Fatalf("Start(mirror) error = %v", err)
	}
	t.Cleanup(func() { _ = mirror.Stop(context.Background()) })

	event := readStateEvent(t, mirror.Watch())
	if event.State.ClusterID != "cluster-mirror" || event.State.Revision == 0 || len(event.State.Nodes) != 1 {
		t.Fatalf("mirror StateEvent = %#v, want synced leader state", event)
	}
}

func TestRuntimeMirrorLeaderIDUsesSyncClientKnownLeader(t *testing.T) {
	leaderState := ClusterState{
		SchemaVersion: CurrentSchemaVersion,
		ClusterID:     "cluster-mirror-known-leader",
		Revision:      7,
		Config:        ClusterConfig{SlotCount: 1, HashSlotCount: 4, ReplicaCount: 2},
		Controllers: []ControllerVoter{
			{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter},
			{NodeID: 2, Addr: "n2", Role: ControllerRoleVoter},
		},
		Nodes: []Node{
			{NodeID: 1, Addr: "n1", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 1},
			{NodeID: 2, Addr: "n2", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 1},
		},
		Slots:     []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 3, PreferredLeader: 2}},
		HashSlots: HashSlotTable{Version: CurrentHashSlotTableVersion, SlotCount: 4, Ranges: []HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
	if err := leaderState.Validate(); err != nil {
		t.Fatalf("leaderState.Validate() error = %v", err)
	}
	syncServer := NewStateSyncServer(StateSyncServerConfig{
		NodeID:    2,
		ClusterID: leaderState.ClusterID,
		LeaderID:  func() uint64 { return 2 },
		Ready:     func() bool { return true },
		Snapshot:  func(context.Context) (ClusterState, error) { return leaderState, nil },
	})
	syncClient := cv2sync.NewClient(cv2sync.ClientConfig{
		ClusterID: leaderState.ClusterID,
		Store:     statefile.New(t.TempDir() + "/cluster-state.json"),
		Peers:     fixedPeerPicker{ids: []uint64{1}, endpoints: map[uint64]Endpoint{1: syncServer}},
		LeaderID:  1,
	})
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:     4,
		Addr:       "n4",
		StateDir:   t.TempDir(),
		ClusterID:  leaderState.ClusterID,
		Role:       RuntimeRoleMirror,
		Voters:     []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}},
		SyncClient: syncClient,
	})
	if err != nil {
		t.Fatalf("NewRuntime(mirror) error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start(mirror) error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	if got := runtime.LeaderID(); got != 2 {
		t.Fatalf("LeaderID() = %d, want sync client leader 2", got)
	}
}

func TestRuntimeVoterWiresStateSyncServerOnStart(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "n1",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-state-sync",
		Role:             RuntimeRoleVoter,
		Voters:           []Voter{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	_ = readStateEvent(t, runtime.Watch())

	if runtime.syncServer == nil {
		t.Fatalf("Start() did not wire state sync server")
	}
	first := runtime.syncServer
	resp, err := runtime.GetState(ctx, GetStateRequest{ClusterID: "cluster-state-sync"})
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if resp.LeaderID != 1 || resp.Revision == 0 || len(resp.Payload) == 0 {
		t.Fatalf("GetState() = %#v, want leader payload", resp)
	}
	resp, err = runtime.GetState(ctx, GetStateRequest{
		ClusterID:     "cluster-state-sync",
		LocalRevision: resp.Revision,
		LocalChecksum: resp.Checksum,
	})
	if err != nil {
		t.Fatalf("GetState(not modified) error = %v", err)
	}
	if runtime.syncServer != first {
		t.Fatalf("GetState() rebuilt state sync server")
	}
	if !resp.NotModified {
		t.Fatalf("GetState(not modified) = %#v, want NotModified", resp)
	}
}

func TestRuntimeProbeProposeDoesNotMutateRevision(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-probe",
		Role:             RuntimeRoleVoter,
		Voters:           []Voter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	_ = readStateEvent(t, runtime.Watch())

	before, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(before) error = %v", err)
	}
	if err := runtime.ProbePropose(ctx); err != nil {
		t.Fatalf("ProbePropose() error = %v", err)
	}
	after, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(after) error = %v", err)
	}
	if before.Revision != after.Revision || len(before.Slots) != len(after.Slots) {
		t.Fatalf("ProbePropose mutated state: before=%#v after=%#v", before, after)
	}
}

func TestRuntimeReportNodeHealthUsesLeaderTimestamp(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-node-health")
	_ = readStateEvent(t, runtime.Watch())

	result, err := runtime.ReportNodeHealth(context.Background(), ReportNodeHealthRequest{
		NodeID:                  1,
		Status:                  NodeStatusAlive,
		RuntimeReady:            true,
		ObservedControlRevision: 1,
		ReportSeq:               7,
	})
	if err != nil {
		t.Fatalf("ReportNodeHealth() error = %v", err)
	}
	if !result.Updated || result.Changed {
		t.Fatalf("ReportNodeHealth() = %#v, want updated without changed", result)
	}
	st, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	if len(st.NodeHealthReports) != 1 || st.NodeHealthReports[0].ReportedAtUnixMilli == 0 {
		t.Fatalf("NodeHealthReports = %#v, want leader-side timestamp", st.NodeHealthReports)
	}
}

func TestRuntimePublishIfChangedRefreshesSameRevisionChecksumDrift(t *testing.T) {
	ctx := context.Background()
	visible := runtimeBootstrapVisibleState(t, 7, true)
	durable := runtimeBootstrapVisibleState(t, 7, false)
	sm := restoredRuntimeStateMachine(t, durable)
	runtime := &Runtime{
		state: visible.Clone(),
		sm:    sm,
		watch: make(chan StateEvent, 1),
	}

	if err := runtime.publishIfChanged(ctx, durable.Revision); err != nil {
		t.Fatalf("publishIfChanged() error = %v", err)
	}

	got, err := runtime.LocalState(ctx)
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	if got.Revision != durable.Revision || len(got.Tasks) != 0 || got.Checksum != durable.Checksum {
		t.Fatalf("LocalState() = %#v, want same revision refreshed to durable task-free state %#v", got, durable)
	}
}

func TestRuntimePublishStateCoalescesLatestEventWhenWatchFull(t *testing.T) {
	first := runtimeBootstrapVisibleState(t, 7, false)
	latest := runtimeBootstrapVisibleState(t, 8, false)
	runtime := &Runtime{
		state: first.Clone(),
		watch: make(chan StateEvent, 1),
	}
	runtime.watch <- StateEvent{State: first.Clone()}

	if err := runtime.publishState(latest); err != nil {
		t.Fatalf("publishState() error = %v", err)
	}

	select {
	case event := <-runtime.Watch():
		if event.State.Revision != latest.Revision {
			t.Fatalf("published revision = %d, want latest revision %d", event.State.Revision, latest.Revision)
		}
	default:
		t.Fatal("watch channel empty, want latest event")
	}
	got, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	if got.Revision != latest.Revision {
		t.Fatalf("LocalState revision = %d, want %d", got.Revision, latest.Revision)
	}
}

func TestRuntimeControlTickRefreshesSameRevisionChecksumDriftAfterBootstrapComplete(t *testing.T) {
	ctx := context.Background()
	visible := runtimeBootstrapVisibleState(t, 7, true)
	durable := runtimeBootstrapVisibleState(t, 7, false)
	sm := restoredRuntimeStateMachine(t, durable)
	srv, err := server.New(server.Config{StateSource: sm})
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}
	runtime := &Runtime{
		cfg: RuntimeConfig{
			NodeID:           1,
			InitialSlotCount: 1,
		},
		state:  visible.Clone(),
		sm:     sm,
		server: srv,
		watch:  make(chan StateEvent, 1),
	}

	if err := runtime.controlTick(ctx); err != nil {
		t.Fatalf("controlTick() error = %v", err)
	}

	got, err := runtime.LocalState(ctx)
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	if got.Revision != durable.Revision || len(got.Tasks) != 0 {
		t.Fatalf("LocalState() = %#v, want bootstrap-complete durable state %#v", got, durable)
	}
}

func TestRuntimeReportTaskProgressProposesCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runtime := newStartedSingleNodeRuntime(t, 1)
	waitForRuntimeSlots(t, runtime, 1)

	err := runtime.ReportTaskProgress(ctx, TaskProgress{
		TaskID:             "slot-1-bootstrap-1",
		SlotID:             1,
		TaskKind:           TaskKindBootstrap,
		ConfigEpoch:        1,
		TaskAttempt:        0,
		ParticipantNodeID:  1,
		ParticipantAttempt: 0,
		Status:             TaskParticipantStatusDone,
		FinishedAt:         time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ReportTaskProgress() error = %v", err)
	}

	st := waitForRuntimeState(t, runtime, func(st ClusterState) bool {
		return len(st.Tasks) == 1 &&
			len(st.Tasks[0].ParticipantProgress) == 1 &&
			st.Tasks[0].ParticipantProgress[0].Status == TaskParticipantStatusDone
	})
	if st.Tasks[0].ParticipantProgress[0].Status != TaskParticipantStatusDone {
		t.Fatalf("participant progress = %#v, want done", st.Tasks[0].ParticipantProgress)
	}
}

func TestRuntimeCompleteTaskProposesCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runtime := newStartedSingleNodeRuntime(t, 1)
	waitForRuntimeSlots(t, runtime, 1)

	err := runtime.CompleteTask(ctx, TaskResult{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		TaskKind:    TaskKindBootstrap,
		ConfigEpoch: 1,
		Attempt:     0,
		FinishedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CompleteTask() error = %v", err)
	}

	st := waitForRuntimeState(t, runtime, func(st ClusterState) bool {
		return len(st.Tasks) == 0
	})
	if len(st.Tasks) != 0 {
		t.Fatalf("Tasks = %#v, want empty", st.Tasks)
	}
}

func TestRuntimeRequestSlotLeaderTransfer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-leader-transfer",
		Role:             RuntimeRoleVoter,
		Voters:           []Voter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     2,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	initial := waitForRuntimeState(t, runtime, func(st ClusterState) bool {
		return st.Revision == 1
	})
	expected := initial.Revision
	node := Node{
		NodeID:         2,
		Addr:           "n2",
		Roles:          []NodeRole{NodeRoleData},
		JoinState:      NodeJoinStateActive,
		Status:         NodeStatusAlive,
		CapacityWeight: 1,
	}
	if err := runtime.raft.Propose(ctx, commandWithNode(expected, node)); err != nil {
		t.Fatalf("Propose(upsert node) error = %v", err)
	}
	ready := waitForRuntimeState(t, runtime, func(st ClusterState) bool {
		return st.Revision > 1 && len(st.Nodes) == 2 && len(st.Slots) == 1
	})
	bootstrapTask := ready.Tasks[0]
	if err := runtime.CompleteTask(ctx, TaskResult{
		TaskID:      bootstrapTask.TaskID,
		SlotID:      bootstrapTask.SlotID,
		TaskKind:    bootstrapTask.Kind,
		ConfigEpoch: bootstrapTask.ConfigEpoch,
		Attempt:     bootstrapTask.Attempt,
		FinishedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CompleteTask(bootstrap) error = %v", err)
	}
	ready = waitForRuntimeState(t, runtime, func(st ClusterState) bool {
		return len(st.Nodes) == 2 && len(st.Slots) == 1 && len(st.Tasks) == 0
	})
	wantTaskID := fmt.Sprintf("slot-1-leader-transfer-7-r%d", ready.Revision)

	result, err := runtime.RequestSlotLeaderTransfer(ctx, SlotLeaderTransferRequest{
		SlotID:        1,
		SourceNode:    1,
		TargetNode:    2,
		TargetPeers:   []uint64{1, 2},
		ConfigEpoch:   7,
		StateRevision: ready.Revision,
	})
	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer() error = %v", err)
	}
	if !result.Created || result.Task == nil || result.Task.TaskID != wantTaskID {
		t.Fatalf("RequestSlotLeaderTransfer() = %#v, want created task", result)
	}

	st := waitForRuntimeState(t, runtime, func(st ClusterState) bool {
		return len(st.Tasks) == 1 && st.Tasks[0].Kind == TaskKindLeaderTransfer
	})
	if st.Slots[0].PreferredLeader != 2 || st.Tasks[0].Step != TaskStepTransferLeader {
		t.Fatalf("state after transfer request = %#v", st)
	}

	sameRevisionDupe, err := runtime.RequestSlotLeaderTransfer(ctx, SlotLeaderTransferRequest{
		SlotID:        1,
		SourceNode:    1,
		TargetNode:    2,
		TargetPeers:   []uint64{1, 2},
		ConfigEpoch:   7,
		StateRevision: ready.Revision,
	})
	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer(same revision duplicate) error = %v", err)
	}
	if sameRevisionDupe.Created || sameRevisionDupe.Task == nil || sameRevisionDupe.Task.TaskID != wantTaskID {
		t.Fatalf("RequestSlotLeaderTransfer(same revision duplicate) = %#v, want existing task no-op", sameRevisionDupe)
	}

	dupe, err := runtime.RequestSlotLeaderTransfer(ctx, SlotLeaderTransferRequest{
		SlotID:        1,
		SourceNode:    1,
		TargetNode:    2,
		TargetPeers:   []uint64{1, 2},
		ConfigEpoch:   7,
		StateRevision: ready.Revision - 1,
	})
	if err != nil {
		t.Fatalf("RequestSlotLeaderTransfer(duplicate) error = %v", err)
	}
	if dupe.Created || dupe.Task == nil || dupe.Task.TaskID != wantTaskID {
		t.Fatalf("RequestSlotLeaderTransfer(duplicate) = %#v, want existing task no-op", dupe)
	}
}

func TestRuntimeRequestSlotReplicaMoveCreatesTaskWithoutChangingDesiredPeers(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-slot-replica-move")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	ready := waitForState(t, runtime, func(st ClusterState) bool {
		return len(st.Tasks) == 1 && len(st.Slots) == 1
	})
	bootstrapTask := ready.Tasks[0]
	if err := runtime.CompleteTask(context.Background(), TaskResult{
		TaskID:      bootstrapTask.TaskID,
		SlotID:      bootstrapTask.SlotID,
		TaskKind:    bootstrapTask.Kind,
		ConfigEpoch: bootstrapTask.ConfigEpoch,
		Attempt:     bootstrapTask.Attempt,
		FinishedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CompleteTask(bootstrap) error = %v", err)
	}
	waitForState(t, runtime, func(st ClusterState) bool {
		return len(st.Tasks) == 0
	})
	before, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	assignment := before.Slots[0]
	source := assignment.DesiredPeers[0]

	result, err := runtime.RequestSlotReplicaMove(context.Background(), SlotReplicaMoveRequest{
		SlotID:        assignment.SlotID,
		SourceNode:    source,
		TargetNode:    4,
		TargetPeers:   replacePeerForTest(assignment.DesiredPeers, source, 4),
		ConfigEpoch:   assignment.ConfigEpoch,
		StateRevision: before.Revision,
	})
	if err != nil {
		t.Fatalf("RequestSlotReplicaMove() error = %v", err)
	}
	if !result.Created || result.Task == nil || result.Task.Kind != TaskKindSlotReplicaMove {
		t.Fatalf("RequestSlotReplicaMove() = %#v, want slot_replica_move task", result)
	}

	after := waitForState(t, runtime, func(st ClusterState) bool {
		for _, task := range st.Tasks {
			if task.Kind == TaskKindSlotReplicaMove && task.TargetNode == 4 {
				return true
			}
		}
		return false
	})
	if !sameUint64SetForTest(after.Slots[0].DesiredPeers, assignment.DesiredPeers) {
		t.Fatalf("DesiredPeers = %v, want unchanged %v", after.Slots[0].DesiredPeers, assignment.DesiredPeers)
	}
	if after.Tasks[0].CompletionPolicy != TaskCompletionPolicySingleObserver || len(after.Tasks[0].ParticipantProgress) != 0 {
		t.Fatalf("task progress policy = %#v progress=%#v, want single observer and empty progress", after.Tasks[0].CompletionPolicy, after.Tasks[0].ParticipantProgress)
	}
}

func TestRuntimeJoinNodeCreatesJoiningDataNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-join-node")
	before, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(before) error = %v", err)
	}

	result, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "node-4",
		Addr:           "127.0.0.1:10004",
		Roles:          []NodeRole{NodeRoleData},
		CapacityWeight: 3,
	})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if !result.Created || result.Node.JoinState != NodeJoinStateJoining {
		t.Fatalf("JoinNode() = %#v, want created joining node", result)
	}
	if result.Revision <= before.Revision {
		t.Fatalf("JoinNode() revision = %d, want greater than %d", result.Revision, before.Revision)
	}

	st := waitForState(t, runtime, func(st ClusterState) bool {
		for _, node := range st.Nodes {
			if node.NodeID == 4 && node.JoinState == NodeJoinStateJoining && node.Status == NodeStatusAlive {
				return true
			}
		}
		return false
	})
	if len(st.Slots) != 1 || containsUint64(st.Slots[0].DesiredPeers, 4) {
		t.Fatalf("Slots after join = %#v, want unchanged assignments without node 4", st.Slots)
	}
}

func TestRuntimeJoinNodeRepeatedCallIsIdempotent(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-join-node-idempotent")

	first, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "node-4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleControllerVoter, NodeRoleData},
		CapacityWeight: 2,
	})
	if err != nil {
		t.Fatalf("JoinNode(first) error = %v", err)
	}
	if !first.Created {
		t.Fatalf("JoinNode(first) = %#v, want created", first)
	}

	second, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "ignored",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		CapacityWeight: 9,
	})
	if err != nil {
		t.Fatalf("JoinNode(second) error = %v", err)
	}
	if second.Created || second.Revision != first.Revision {
		t.Fatalf("JoinNode(second) = %#v, want idempotent revision %d", second, first.Revision)
	}
	if second.Node.Name != "node-4" || !sameNodeRoles(second.Node.Roles, []NodeRole{NodeRoleData}) {
		t.Fatalf("JoinNode(second) node = %#v, want existing normalized data node", second.Node)
	}
}

func TestRuntimeJoinNodeReportsNoCreateAfterStaleEquivalentNoop(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-join-node-stale-noop")
	stale, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(stale) error = %v", err)
	}
	node := Node{
		NodeID:         4,
		Name:           "node-4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		JoinState:      NodeJoinStateJoining,
		Status:         NodeStatusAlive,
		CapacityWeight: 1,
	}
	if err := runtime.raft.Propose(context.Background(), commandWithNode(stale.Revision, node)); err != nil {
		t.Fatalf("Propose(join node) error = %v", err)
	}
	applied := runtime.sm.Snapshot(context.Background())
	if applied.Revision <= stale.Revision {
		t.Fatalf("applied revision = %d, want greater than stale %d", applied.Revision, stale.Revision)
	}
	setRuntimeStateForTest(runtime, stale)

	result, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Name: "node-4", Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	if err != nil {
		t.Fatalf("JoinNode(stale equivalent) error = %v", err)
	}
	if result.Created || result.Revision != applied.Revision {
		t.Fatalf("JoinNode(stale equivalent) = %#v, want no create at revision %d", result, applied.Revision)
	}
}

func TestRuntimeActivateNodeTurnsJoiningNodeActive(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-activate-node")
	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	before, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(before activate) error = %v", err)
	}

	result, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if !result.Changed || result.Node.JoinState != NodeJoinStateActive {
		t.Fatalf("ActivateNode() = %#v, want changed active node", result)
	}
	if result.Revision <= before.Revision {
		t.Fatalf("ActivateNode() revision = %d, want greater than %d", result.Revision, before.Revision)
	}
}

func TestRuntimeActivateNodeRepeatedCallIsIdempotent(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-activate-node-idempotent")
	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	first, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode(first) error = %v", err)
	}
	if !first.Changed {
		t.Fatalf("ActivateNode(first) = %#v, want changed", first)
	}

	second, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode(second) error = %v", err)
	}
	if second.Changed || second.Revision != first.Revision || second.Node.JoinState != NodeJoinStateActive {
		t.Fatalf("ActivateNode(second) = %#v, want idempotent active revision %d", second, first.Revision)
	}
}

func TestRuntimeActivateNodeReportsNoChangeAfterStaleEquivalentNoop(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-activate-node-stale-noop")
	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	stale, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(stale) error = %v", err)
	}
	node, ok := findNodeForTest(stale, 4)
	if !ok {
		t.Fatalf("node 4 missing from stale state: %#v", stale.Nodes)
	}
	node.JoinState = NodeJoinStateActive
	node.Status = NodeStatusAlive
	if err := runtime.raft.Propose(context.Background(), commandWithNode(stale.Revision, node)); err != nil {
		t.Fatalf("Propose(activate node) error = %v", err)
	}
	applied := runtime.sm.Snapshot(context.Background())
	if applied.Revision <= stale.Revision {
		t.Fatalf("applied revision = %d, want greater than stale %d", applied.Revision, stale.Revision)
	}
	setRuntimeStateForTest(runtime, stale)

	result, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode(stale equivalent) error = %v", err)
	}
	if result.Changed || result.Revision != applied.Revision {
		t.Fatalf("ActivateNode(stale equivalent) = %#v, want no change at revision %d", result, applied.Revision)
	}
}

func TestRuntimeJoinNodeRejectsAddressConflict(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-join-conflict")

	_, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	if err != nil {
		t.Fatalf("JoinNode(first) error = %v", err)
	}
	_, err = runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 5, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1})
	if !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("JoinNode(conflicting addr) error = %v, want ErrNodeLifecycleConflict", err)
	}
}

func TestRuntimeJoinNodeRejectsInvalidRequest(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-join-invalid")
	tests := []JoinNodeRequest{
		{Addr: "n4", Roles: []NodeRole{NodeRoleData}},
		{NodeID: 4, Roles: []NodeRole{NodeRoleData}},
		{NodeID: 4, Addr: "   ", Roles: []NodeRole{NodeRoleData}},
	}
	for _, req := range tests {
		if _, err := runtime.JoinNode(context.Background(), req); err == nil {
			t.Fatalf("JoinNode(%#v) error = nil, want invalid request error", req)
		}
	}
}

func TestRuntimeActivateNodeRejectsMissingOrNonJoiningNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-activate-invalid")
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{}); err == nil {
		t.Fatal("ActivateNode(zero node) error = nil, want invalid request error")
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); !errors.Is(err, ErrNodeLifecycleNotFound) {
		t.Fatalf("ActivateNode(missing node) error = %v, want ErrNodeLifecycleNotFound", err)
	}
	st, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	leaving := Node{
		NodeID:         5,
		Addr:           "n5",
		Roles:          []NodeRole{NodeRoleData},
		JoinState:      NodeJoinStateLeaving,
		Status:         NodeStatusAlive,
		CapacityWeight: 1,
	}
	if err := runtime.raft.Propose(context.Background(), commandWithNode(st.Revision, leaving)); err != nil {
		t.Fatalf("Propose(leaving node) error = %v", err)
	}
	if err := runtime.publishFromState(context.Background()); err != nil {
		t.Fatalf("publishFromState() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 5}); !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("ActivateNode(leaving node) error = %v, want ErrNodeLifecycleConflict", err)
	}
}

func TestRuntimeMarkNodeLeavingTurnsActiveDataNodeLeaving(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-leaving")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "n4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		CapacityWeight: 1,
	}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}

	result, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	if !result.Changed || result.Node.JoinState != NodeJoinStateLeaving {
		t.Fatalf("MarkNodeLeaving() = %#v, want changed leaving node", result)
	}

	second, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() second error = %v", err)
	}
	if second.Changed || second.Revision != result.Revision || second.Node.JoinState != NodeJoinStateLeaving {
		t.Fatalf("MarkNodeLeaving() second = %#v, want idempotent unchanged leaving node", second)
	}
}

func TestRuntimeMarkNodeLeavingRejectsControllerVoter(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-controller-leaving")

	_, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 1})
	if !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("MarkNodeLeaving(controller voter) error = %v, want %v", err, ErrNodeLifecycleConflict)
	}
}

func TestRuntimeMarkNodeLeavingRejectsMissingNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-missing-leaving")

	_, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 99})
	if !errors.Is(err, ErrNodeLifecycleNotFound) {
		t.Fatalf("MarkNodeLeaving(missing) error = %v, want %v", err, ErrNodeLifecycleNotFound)
	}
}

func TestRuntimeMarkNodeRemovedTurnsLeavingNodeRemoved(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "n4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		CapacityWeight: 1,
	}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if _, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4}); err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}

	result, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if !result.Changed || result.Node.JoinState != NodeJoinStateRemoved || result.Node.Status != NodeStatusDown {
		t.Fatalf("MarkNodeRemoved() = %#v, want changed removed down node", result)
	}

	second, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() second error = %v", err)
	}
	if second.Changed || second.Revision != result.Revision || second.Node.JoinState != NodeJoinStateRemoved {
		t.Fatalf("MarkNodeRemoved() second = %#v, want idempotent removed node", second)
	}
}

func TestRuntimeMarkNodeRemovedRejectsActiveNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed-active")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "n4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		CapacityWeight: 1,
	}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}

	_, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4})
	if !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("MarkNodeRemoved(active) error = %v, want %v", err, ErrNodeLifecycleConflict)
	}
}

func TestRuntimeMarkNodeRemovedRejectsControllerVoter(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed-controller")

	_, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 1})
	if !errors.Is(err, ErrNodeLifecycleConflict) {
		t.Fatalf("MarkNodeRemoved(controller voter) error = %v, want %v", err, ErrNodeLifecycleConflict)
	}
}

func TestRuntimeMarkNodeRemovedRejectsMissingNode(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed-missing")

	_, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 99})
	if !errors.Is(err, ErrNodeLifecycleNotFound) {
		t.Fatalf("MarkNodeRemoved(missing) error = %v, want %v", err, ErrNodeLifecycleNotFound)
	}
}

func TestRuntimeMarkNodeRemovedRejectsStaleExpectedRevision(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed-stale-revision")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "n4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		CapacityWeight: 1,
	}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if _, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4}); err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	state, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}

	_, err = runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4, ExpectedRevision: state.Revision + 1})
	if !errors.Is(err, ErrExpectedRevisionMismatch) {
		t.Fatalf("MarkNodeRemoved(stale revision) error = %v, want %v", err, ErrExpectedRevisionMismatch)
	}
}

func TestRuntimeMarkNodeRemovedAllowsStaleExpectedRevisionWhenAlreadyRemoved(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-mark-removed-idempotent-stale-revision")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{
		NodeID:         4,
		Name:           "n4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		CapacityWeight: 1,
	}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if _, err := runtime.MarkNodeLeaving(context.Background(), MarkNodeLeavingRequest{NodeID: 4}); err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	state, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState() error = %v", err)
	}
	removed, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4, ExpectedRevision: state.Revision})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}

	second, err := runtime.MarkNodeRemoved(context.Background(), MarkNodeRemovedRequest{NodeID: 4, ExpectedRevision: state.Revision})
	if err != nil {
		t.Fatalf("MarkNodeRemoved(already removed stale revision) error = %v", err)
	}
	if second.Changed || second.Revision != removed.Revision || second.Node.JoinState != NodeJoinStateRemoved {
		t.Fatalf("MarkNodeRemoved(already removed stale revision) = %#v, want idempotent removed node", second)
	}
}

func TestRuntimePromoteControllerVoterRejectsMismatchedLiveVoterProof(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-controller-voter-proof")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}

	_, err := runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{
		NodeID:              4,
		ObservedConfigIndex: 11,
		ObservedVoters:      []uint64{1},
	})
	if !errors.Is(err, ErrProposalRejected) {
		t.Fatalf("PromoteControllerVoter() error = %v, want %v", err, ErrProposalRejected)
	}
}

func TestRuntimePromoteControllerVoterCommitsStateAfterProof(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-controller-voter-commit")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	before, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(before) error = %v", err)
	}

	result, err := runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{
		NodeID:              4,
		ExpectedRevision:    before.Revision,
		ExpectedVoters:      []uint64{1},
		ObservedConfigIndex: 11,
		ObservedVoters:      []uint64{1, 4},
	})
	if err != nil {
		t.Fatalf("PromoteControllerVoter() error = %v", err)
	}
	if !result.Changed || result.Revision <= before.Revision {
		t.Fatalf("PromoteControllerVoter() = %#v, want changed revision > %d", result, before.Revision)
	}
	if !sameUint64SetForTest(result.NextVoters, []uint64{1, 4}) {
		t.Fatalf("NextVoters = %v, want [1 4]", result.NextVoters)
	}

	after := waitForState(t, runtime, func(st ClusterState) bool {
		node, ok := findNodeForTest(st, 4)
		return ok && node.HasRole(NodeRoleControllerVoter)
	})
	node, ok := findNodeForTest(after, 4)
	if !ok || !node.HasRole(NodeRoleControllerVoter) {
		t.Fatalf("node 4 after promotion = %#v, want controller_voter role", node)
	}
}

func TestRuntimePromoteControllerVoterRejectsStaleExpectedRevision(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-controller-voter-stale-revision")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	before, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(before) error = %v", err)
	}

	_, err = runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{
		NodeID:              4,
		ExpectedRevision:    before.Revision + 1,
		ExpectedVoters:      []uint64{1},
		ObservedConfigIndex: 11,
		ObservedVoters:      []uint64{1, 4},
	})
	if !errors.Is(err, ErrProposalRejected) || !IsExpectedRevisionMismatch(err) {
		t.Fatalf("PromoteControllerVoter(stale revision) error = %v, want proposal rejected expected revision mismatch", err)
	}
	after, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(after) error = %v", err)
	}
	node, ok := findNodeForTest(after, 4)
	if !ok {
		t.Fatalf("node 4 missing after stale promotion rejection")
	}
	if node.HasRole(NodeRoleControllerVoter) || !sameUint64SetForTest(controllerNodeIDsForRuntime(after.Controllers), []uint64{1}) || after.Revision != before.Revision {
		t.Fatalf("LocalState(after stale promotion rejection) = %#v, before revision %d, want unchanged data-only node 4", after, before.Revision)
	}
}

func TestRuntimePromoteControllerVoterRetryIsIdempotent(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-controller-voter-idempotent")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	req := PromoteControllerVoterRequest{
		NodeID:              4,
		ObservedConfigIndex: 11,
		ObservedVoters:      []uint64{1, 4},
	}

	first, err := runtime.PromoteControllerVoter(context.Background(), req)
	if err != nil {
		t.Fatalf("PromoteControllerVoter(first) error = %v", err)
	}
	if !first.Changed || !sameUint64SetForTest(first.NextVoters, []uint64{1, 4}) {
		t.Fatalf("PromoteControllerVoter(first) = %#v, want changed voters [1 4]", first)
	}

	second, err := runtime.PromoteControllerVoter(context.Background(), req)
	if err != nil {
		t.Fatalf("PromoteControllerVoter(second) error = %v", err)
	}
	if second.Changed || second.Revision != first.Revision {
		t.Fatalf("PromoteControllerVoter(second) = %#v, want unchanged revision %d", second, first.Revision)
	}
	if !second.Node.HasRole(NodeRoleControllerVoter) {
		t.Fatalf("PromoteControllerVoter(second) node roles = %v, want controller voter", second.Node.Roles)
	}
	if !sameUint64SetForTest(second.PreviousVoters, []uint64{1, 4}) || !sameUint64SetForTest(second.NextVoters, []uint64{1, 4}) {
		t.Fatalf("PromoteControllerVoter(second) voters previous=%v next=%v, want stable [1 4]", second.PreviousVoters, second.NextVoters)
	}
	after, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(after retry) error = %v", err)
	}
	node, ok := findNodeForTest(after, 4)
	if !ok || !node.HasRole(NodeRoleControllerVoter) || !sameUint64SetForTest(controllerNodeIDsForRuntime(after.Controllers), []uint64{1, 4}) {
		t.Fatalf("LocalState(after retry) = %#v, want node 4 controller voter and voters [1 4]", after)
	}
}

func TestRuntimePromoteControllerVoterPreservesExplicitEmptyExpectedVoterFence(t *testing.T) {
	runtime := startSingleVoterRuntime(t, "cluster-controller-voter-empty-fence")
	if _, err := runtime.JoinNode(context.Background(), JoinNodeRequest{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, CapacityWeight: 1}); err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if _, err := runtime.ActivateNode(context.Background(), ActivateNodeRequest{NodeID: 4}); err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}

	_, err := runtime.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{
		NodeID:              4,
		ExpectedVoters:      []uint64{},
		ObservedConfigIndex: 11,
		ObservedVoters:      []uint64{1, 4},
	})
	if !errors.Is(err, ErrProposalRejected) {
		t.Fatalf("PromoteControllerVoter(explicit empty fence) error = %v, want %v", err, ErrProposalRejected)
	}
}

func TestRuntimePrepareControllerVoterMovesMirrorStateAside(t *testing.T) {
	stateDir := t.TempDir()
	mirrorState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare", 9)
	store := statefile.New(filepath.Join(stateDir, "cluster-state.json"))
	if err := store.Save(context.Background(), mirrorState); err != nil {
		t.Fatalf("Save(mirror state) error = %v", err)
	}
	runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare")

	result, err := runtime.PrepareControllerVoter(context.Background(), prepareControllerVoterRequestForTest("wk-prepare", mirrorState.Revision))
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	if !result.Prepared || result.StateRevision != mirrorState.Revision {
		t.Fatalf("PrepareControllerVoter() = %#v, want prepared revision %d", result, mirrorState.Revision)
	}
	if runtime.raft == nil {
		t.Fatalf("PrepareControllerVoter() did not start Controller Raft service")
	}
	if runtime.syncClient != nil {
		t.Fatalf("PrepareControllerVoter() kept mirror sync client")
	}
	activePath := filepath.Join(stateDir, "cluster-state.json")
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Fatalf("active mirror state stat error = %v, want not exist", err)
	}
	backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup mirror state stat error = %v", err)
	}
	assertStateFileRevisionForTest(t, backupPath, mirrorState.Revision)
}

func TestRuntimePrepareControllerVoterFromStartedMirrorRuntime(t *testing.T) {
	stateDir := t.TempDir()
	clusterID := "wk-prepare-started-mirror"
	leaderState := mirroredPrepareDataNodeStateForTest(t, clusterID, 9)
	leader := NewStateSyncServer(StateSyncServerConfig{
		NodeID:    1,
		ClusterID: clusterID,
		LeaderID:  func() uint64 { return 1 },
		Ready:     func() bool { return true },
		Snapshot: func(context.Context) (ClusterState, error) {
			return leaderState, nil
		},
	})
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:       4,
		Addr:         "n4",
		StateDir:     stateDir,
		ClusterID:    clusterID,
		Role:         RuntimeRoleMirror,
		Voters:       []Voter{{NodeID: 1, Addr: "n1"}},
		SyncPeers:    fixedPeerPicker{ids: []uint64{1}, endpoints: map[uint64]Endpoint{1: leader}},
		TickInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	if runtime.server == nil || runtime.syncClient == nil || runtime.refreshCancel == nil {
		t.Fatalf("Start() mirror fields server=%p syncClient=%p refreshCancel=%v, want started mirror", runtime.server, runtime.syncClient, runtime.refreshCancel)
	}
	synced, err := runtime.LocalState(context.Background())
	if err != nil {
		t.Fatalf("LocalState(synced) error = %v", err)
	}
	node, ok := findNodeForTest(synced, 4)
	if !ok || !sameUint64SetForTest(controllerNodeIDsForRuntime(synced.Controllers), []uint64{1}) || !sameNodeRoles(node.Roles, []NodeRole{NodeRoleData}) {
		t.Fatalf("LocalState(synced) = %#v, want node 4 active data-only mirror state with controller 1", synced)
	}

	result, err := runtime.PrepareControllerVoter(context.Background(), prepareControllerVoterRequestForTest(clusterID, leaderState.Revision))
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}
	if !result.Prepared || result.StateRevision != leaderState.Revision {
		t.Fatalf("PrepareControllerVoter() = %#v, want prepared revision %d", result, leaderState.Revision)
	}
	if runtime.cfg.Role != RuntimeRoleVoter || runtime.cfg.AllowBootstrap {
		t.Fatalf("runtime role=%s allowBootstrap=%v, want voter without bootstrap", runtime.cfg.Role, runtime.cfg.AllowBootstrap)
	}
	if runtime.syncClient != nil || runtime.raft == nil || runtime.server == nil || runtime.syncServer == nil {
		t.Fatalf("runtime fields after prepare syncClient=%p raft=%p server=%p syncServer=%p, want voter runtime", runtime.syncClient, runtime.raft, runtime.server, runtime.syncServer)
	}
	activePath := filepath.Join(stateDir, "cluster-state.json")
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Fatalf("active mirror state stat error = %v, want not exist", err)
	}
	backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
	assertStateFileRevisionForTest(t, backupPath, leaderState.Revision)
}

func TestRuntimePrepareControllerVoterRetryIsIdempotent(t *testing.T) {
	stateDir := t.TempDir()
	mirrorState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-idempotent", 9)
	activePath := filepath.Join(stateDir, "cluster-state.json")
	if err := statefile.New(activePath).Save(context.Background(), mirrorState); err != nil {
		t.Fatalf("Save(active mirror state) error = %v", err)
	}
	runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare-idempotent")
	req := prepareControllerVoterRequestForTest("wk-prepare-idempotent", mirrorState.Revision)

	first, err := runtime.PrepareControllerVoter(context.Background(), req)
	if err != nil {
		t.Fatalf("PrepareControllerVoter(first) error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	if !first.Prepared || first.StateRevision != mirrorState.Revision {
		t.Fatalf("PrepareControllerVoter(first) = %#v, want prepared revision %d", first, mirrorState.Revision)
	}
	raftBefore := runtime.raft
	serverBefore := runtime.server
	syncServerBefore := runtime.syncServer
	if raftBefore == nil || serverBefore == nil || syncServerBefore == nil {
		t.Fatalf("PrepareControllerVoter(first) runtime fields raft=%p server=%p syncServer=%p, want initialized", raftBefore, serverBefore, syncServerBefore)
	}

	second, err := runtime.PrepareControllerVoter(context.Background(), req)
	if err != nil {
		t.Fatalf("PrepareControllerVoter(second) error = %v", err)
	}
	if !second.Prepared || second.StateRevision != first.StateRevision {
		t.Fatalf("PrepareControllerVoter(second) = %#v, want prepared revision %d", second, first.StateRevision)
	}
	if runtime.raft != raftBefore || runtime.server != serverBefore || runtime.syncServer != syncServerBefore {
		t.Fatalf("PrepareControllerVoter(second) recreated runtime fields raft=%p/%p server=%p/%p syncServer=%p/%p", runtime.raft, raftBefore, runtime.server, serverBefore, runtime.syncServer, syncServerBefore)
	}
	if runtime.cfg.Role != RuntimeRoleVoter || runtime.syncClient != nil {
		t.Fatalf("PrepareControllerVoter(second) role=%s syncClient=%p, want voter with no mirror client", runtime.cfg.Role, runtime.syncClient)
	}
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Fatalf("active mirror state stat error = %v, want not exist", err)
	}
	backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
	assertStateFileRevisionForTest(t, backupPath, mirrorState.Revision)
}

func TestRuntimePrepareControllerVoterLoadsMirrorStateFromBackupOnly(t *testing.T) {
	stateDir := t.TempDir()
	backupState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-backup-only", 12)
	backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
	if err := statefile.New(backupPath).Save(context.Background(), backupState); err != nil {
		t.Fatalf("Save(backup mirror state) error = %v", err)
	}
	runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare-backup-only")

	result, err := runtime.PrepareControllerVoter(context.Background(), prepareControllerVoterRequestForTest("wk-prepare-backup-only", backupState.Revision))
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	if !result.Prepared || result.StateRevision != backupState.Revision {
		t.Fatalf("PrepareControllerVoter() = %#v, want prepared revision %d", result, backupState.Revision)
	}
	assertStateFileRevisionForTest(t, backupPath, backupState.Revision)
	if _, err := os.Stat(filepath.Join(stateDir, "cluster-state.json")); !os.IsNotExist(err) {
		t.Fatalf("active mirror state stat error = %v, want not exist", err)
	}
}

func TestRuntimePrepareControllerVoterKeepsNewerActiveMirrorStateOverBackup(t *testing.T) {
	stateDir := t.TempDir()
	backupState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-newer-active", 7)
	activeState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-newer-active", 11)
	backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
	if err := statefile.New(backupPath).Save(context.Background(), backupState); err != nil {
		t.Fatalf("Save(backup mirror state) error = %v", err)
	}
	if err := statefile.New(filepath.Join(stateDir, "cluster-state.json")).Save(context.Background(), activeState); err != nil {
		t.Fatalf("Save(active mirror state) error = %v", err)
	}
	runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare-newer-active")

	result, err := runtime.PrepareControllerVoter(context.Background(), prepareControllerVoterRequestForTest("wk-prepare-newer-active", activeState.Revision))
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	if !result.Prepared || result.StateRevision != activeState.Revision {
		t.Fatalf("PrepareControllerVoter() = %#v, want prepared revision %d", result, activeState.Revision)
	}
	assertStateFileRevisionForTest(t, backupPath, activeState.Revision)
	if _, err := os.Stat(filepath.Join(stateDir, "cluster-state.json")); !os.IsNotExist(err) {
		t.Fatalf("active mirror state stat error = %v, want not exist", err)
	}
}

func TestRuntimePrepareControllerVoterUsesPreservedStateOverStaleMemory(t *testing.T) {
	stateDir := t.TempDir()
	staleMemory := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-stale-memory", 1)
	backupState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-stale-memory", 9)
	backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
	if err := statefile.New(backupPath).Save(context.Background(), backupState); err != nil {
		t.Fatalf("Save(backup mirror state) error = %v", err)
	}
	runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare-stale-memory")
	setRuntimeStateForTest(runtime, staleMemory)

	result, err := runtime.PrepareControllerVoter(context.Background(), prepareControllerVoterRequestForTest("wk-prepare-stale-memory", backupState.Revision))
	if err != nil {
		t.Fatalf("PrepareControllerVoter() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	if !result.Prepared || result.StateRevision != backupState.Revision {
		t.Fatalf("PrepareControllerVoter() = %#v, want prepared revision %d", result, backupState.Revision)
	}
	assertStateFileRevisionForTest(t, backupPath, backupState.Revision)
}

func TestRuntimePrepareControllerVoterDoesNotPublishFailedPreservedState(t *testing.T) {
	tests := []struct {
		name               string
		runtimeClusterID   string
		preservedClusterID string
		expectedRevision   uint64
		wantErr            error
	}{
		{
			name:               "cluster mismatch",
			runtimeClusterID:   "wk-prepare-local",
			preservedClusterID: "wk-prepare-foreign",
			expectedRevision:   9,
		},
		{
			name:               "expected revision mismatch",
			runtimeClusterID:   "wk-prepare-revision",
			preservedClusterID: "wk-prepare-revision",
			expectedRevision:   10,
			wantErr:            ErrExpectedRevisionMismatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stateDir := t.TempDir()
			staleMemory := mirroredPrepareDataNodeStateForTest(t, tt.runtimeClusterID, 1)
			preservedState := mirroredPrepareDataNodeStateForTest(t, tt.preservedClusterID, 9)
			activePath := filepath.Join(stateDir, "cluster-state.json")
			if err := statefile.New(activePath).Save(context.Background(), preservedState); err != nil {
				t.Fatalf("Save(active mirror state) error = %v", err)
			}
			runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, tt.runtimeClusterID)
			setRuntimeStateForTest(runtime, staleMemory)

			_, err := runtime.PrepareControllerVoter(context.Background(), prepareControllerVoterRequestForTest(tt.runtimeClusterID, tt.expectedRevision))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("PrepareControllerVoter() error = %v, want %v", err, tt.wantErr)
				}
			} else if err == nil {
				t.Fatalf("PrepareControllerVoter() error = nil, want validation error")
			}
			visible, err := runtime.LocalState(context.Background())
			if err != nil {
				t.Fatalf("LocalState() error = %v", err)
			}
			if visible.ClusterID != staleMemory.ClusterID || visible.Revision != staleMemory.Revision {
				t.Fatalf("LocalState() = cluster %q revision %d, want stale cluster %q revision %d", visible.ClusterID, visible.Revision, staleMemory.ClusterID, staleMemory.Revision)
			}
		})
	}
}

func TestRuntimePrepareControllerVoterRestartsMirrorRefreshAfterValidationFailure(t *testing.T) {
	stateDir := t.TempDir()
	mirrorState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-refresh-restart", 9)
	activePath := filepath.Join(stateDir, "cluster-state.json")
	if err := statefile.New(activePath).Save(context.Background(), mirrorState); err != nil {
		t.Fatalf("Save(active mirror state) error = %v", err)
	}
	runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare-refresh-restart")
	runtime.syncClient = runtime.cfg.SyncClient
	runtime.startRefreshLoop()
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	if runtime.refreshCancel == nil {
		t.Fatalf("refresh loop was not started for test")
	}

	_, err := runtime.PrepareControllerVoter(context.Background(), prepareControllerVoterRequestForTest("wk-prepare-refresh-restart", mirrorState.Revision+1))
	if !errors.Is(err, ErrExpectedRevisionMismatch) {
		t.Fatalf("PrepareControllerVoter() error = %v, want %v", err, ErrExpectedRevisionMismatch)
	}
	if runtime.refreshCancel == nil {
		t.Fatalf("PrepareControllerVoter() stopped mirror refresh after validation failure")
	}
}

func TestRuntimePrepareControllerVoterRestartsMirrorRefreshAfterMoveFailure(t *testing.T) {
	stateDir := t.TempDir()
	backupState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-move-failure", 7)
	activeState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-move-failure", 11)
	activePath := filepath.Join(stateDir, "cluster-state.json")
	backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
	if err := statefile.New(activePath).Save(context.Background(), activeState); err != nil {
		t.Fatalf("Save(active mirror state) error = %v", err)
	}
	if err := statefile.New(backupPath).Save(context.Background(), backupState); err != nil {
		t.Fatalf("Save(backup mirror state) error = %v", err)
	}
	oldBackupPath := backupPath + ".old"
	if err := os.MkdirAll(oldBackupPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(old backup dir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldBackupPath, "busy"), []byte("busy"), 0o644); err != nil {
		t.Fatalf("WriteFile(old backup marker) error = %v", err)
	}
	runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare-move-failure")
	runtime.syncClient = runtime.cfg.SyncClient
	runtime.startRefreshLoop()
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	_, err := runtime.PrepareControllerVoter(context.Background(), prepareControllerVoterRequestForTest("wk-prepare-move-failure", activeState.Revision))
	if err == nil {
		t.Fatalf("PrepareControllerVoter() error = nil, want move failure")
	}
	if runtime.refreshCancel == nil {
		t.Fatalf("PrepareControllerVoter() stopped mirror refresh after move failure")
	}
	if runtime.cfg.Role != RuntimeRoleMirror {
		t.Fatalf("runtime role = %s, want %s", runtime.cfg.Role, RuntimeRoleMirror)
	}
	if runtime.syncClient == nil {
		t.Fatalf("syncClient = nil, want mirror sync client retained")
	}
	assertStateFileRevisionForTest(t, activePath, activeState.Revision)
	assertStateFileRevisionForTest(t, backupPath, backupState.Revision)
}

func TestRuntimePrepareControllerVoterValidatesNextVotersBeforeMovingState(t *testing.T) {
	stateDir := t.TempDir()
	mirrorState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-invalid-voters", 9)
	activePath := filepath.Join(stateDir, "cluster-state.json")
	if err := statefile.New(activePath).Save(context.Background(), mirrorState); err != nil {
		t.Fatalf("Save(active mirror state) error = %v", err)
	}
	runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare-invalid-voters")

	_, err := runtime.PrepareControllerVoter(context.Background(), PrepareControllerVoterRequest{
		NodeID:           4,
		ClusterID:        "wk-prepare-invalid-voters",
		ExpectedRevision: mirrorState.Revision,
		NextVoters:       []Voter{{NodeID: 1, Addr: "n1"}},
	})
	if err == nil {
		t.Fatalf("PrepareControllerVoter(invalid voters) error = nil, want validation error")
	}
	if _, statErr := os.Stat(activePath); statErr != nil {
		t.Fatalf("active mirror state stat error = %v, want still present", statErr)
	}
	backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
	if _, statErr := os.Stat(backupPath); !os.IsNotExist(statErr) {
		t.Fatalf("backup mirror state stat error = %v, want not exist", statErr)
	}
}

func TestRuntimePrepareControllerVoterValidatesNextVotersAgainstPreservedStateBeforeMovingState(t *testing.T) {
	tests := []struct {
		name       string
		nextVoters []Voter
	}{
		{
			name:       "omits existing controller",
			nextVoters: []Voter{{NodeID: 4, Addr: "n4"}},
		},
		{
			name:       "changes existing controller addr",
			nextVoters: []Voter{{NodeID: 1, Addr: "n1-wrong"}, {NodeID: 4, Addr: "n4"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stateDir := t.TempDir()
			mirrorState := mirroredPrepareDataNodeStateForTest(t, "wk-prepare-semantic-voters", 9)
			activePath := filepath.Join(stateDir, "cluster-state.json")
			if err := statefile.New(activePath).Save(context.Background(), mirrorState); err != nil {
				t.Fatalf("Save(active mirror state) error = %v", err)
			}
			runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare-semantic-voters")
			req := prepareControllerVoterRequestForTest("wk-prepare-semantic-voters", mirrorState.Revision)
			req.NextVoters = tt.nextVoters

			if _, err := runtime.PrepareControllerVoter(context.Background(), req); err == nil {
				t.Fatalf("PrepareControllerVoter(%s) error = nil, want validation error", tt.name)
			}
			if _, statErr := os.Stat(activePath); statErr != nil {
				t.Fatalf("active mirror state stat error = %v, want still present", statErr)
			}
			backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
			if _, statErr := os.Stat(backupPath); !os.IsNotExist(statErr) {
				t.Fatalf("backup mirror state stat error = %v, want not exist", statErr)
			}
		})
	}
}

func TestRuntimePrepareControllerVoterValidatesLocalNodeAgainstPreservedStateBeforeMovingState(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(ClusterState) ClusterState
		nextVoters []Voter
	}{
		{
			name: "missing local node",
			mutate: func(st ClusterState) ClusterState {
				st.Controllers = []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}}
				st.Nodes = []Node{st.Nodes[0]}
				return checksumClusterStateForTest(t, st)
			},
			nextVoters: []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
		},
		{
			name: "inactive local node",
			mutate: func(st ClusterState) ClusterState {
				st.Controllers = []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}}
				st.Nodes[1].Roles = []NodeRole{NodeRoleData}
				st.Nodes[1].JoinState = NodeJoinStateLeaving
				return checksumClusterStateForTest(t, st)
			},
			nextVoters: []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
		},
		{
			name: "local durable addr mismatch",
			mutate: func(st ClusterState) ClusterState {
				st.Controllers = []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}}
				st.Nodes[1].Roles = []NodeRole{NodeRoleData}
				st.Nodes[1].Addr = "n4-durable"
				return checksumClusterStateForTest(t, st)
			},
			nextVoters: []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stateDir := t.TempDir()
			mirrorState := tt.mutate(mirroredPrepareDataNodeStateForTest(t, "wk-prepare-local-node", 9))
			activePath := filepath.Join(stateDir, "cluster-state.json")
			if err := statefile.New(activePath).Save(context.Background(), mirrorState); err != nil {
				t.Fatalf("Save(active mirror state) error = %v", err)
			}
			runtime := newPrepareControllerVoterRuntimeForTest(t, stateDir, "wk-prepare-local-node")
			req := prepareControllerVoterRequestForTest("wk-prepare-local-node", mirrorState.Revision)
			req.NextVoters = tt.nextVoters

			if _, err := runtime.PrepareControllerVoter(context.Background(), req); err == nil {
				t.Fatalf("PrepareControllerVoter(%s) error = nil, want validation error", tt.name)
			}
			if _, statErr := os.Stat(activePath); statErr != nil {
				t.Fatalf("active mirror state stat error = %v, want still present", statErr)
			}
			backupPath := filepath.Join(stateDir, mirrorBeforeControllerVoterPromotionFile)
			if _, statErr := os.Stat(backupPath); !os.IsNotExist(statErr) {
				t.Fatalf("backup mirror state stat error = %v, want not exist", statErr)
			}
		})
	}
}

func readStateEvent(t *testing.T, watch <-chan StateEvent) StateEvent {
	t.Helper()
	select {
	case event := <-watch:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for StateEvent")
		return StateEvent{}
	}
}

func startSingleVoterRuntime(t *testing.T, clusterID string) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        clusterID,
		Role:             RuntimeRoleVoter,
		Voters:           []Voter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	waitForState(t, runtime, func(st ClusterState) bool { return st.Revision > 0 })
	return runtime
}

func newStartedSingleNodeRuntime(t *testing.T, slotCount uint32) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-task-facade",
		Role:             RuntimeRoleVoter,
		Voters:           []Voter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: slotCount,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	return runtime
}

func waitForRuntimeSlots(t *testing.T, runtime *Runtime, slotCount int) ClusterState {
	t.Helper()
	return waitForRuntimeState(t, runtime, func(st ClusterState) bool {
		return len(st.Slots) == slotCount && len(st.Tasks) == slotCount
	})
}

func waitForState(t *testing.T, runtime *Runtime, match func(ClusterState) bool) ClusterState {
	t.Helper()
	return waitForRuntimeState(t, runtime, match)
}

func waitForRuntimeState(t *testing.T, runtime *Runtime, match func(ClusterState) bool) ClusterState {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		st, err := runtime.LocalState(context.Background())
		if err != nil {
			t.Fatalf("LocalState() error = %v", err)
		}
		if match(st) {
			return st
		}
		select {
		case event := <-runtime.Watch():
			if match(event.State) {
				return event.State
			}
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("timeout waiting for runtime state, last=%#v", st)
		}
	}
}

func containsUint64(values []uint64, want uint64) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func restoredRuntimeStateMachine(t *testing.T, st ClusterState) *fsm.StateMachine {
	t.Helper()
	sm, err := fsm.New(statefile.New(filepath.Join(t.TempDir(), "cluster-state.json")))
	if err != nil {
		t.Fatalf("fsm.New() error = %v", err)
	}
	if err := sm.Restore(context.Background(), st); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	return sm
}

func runtimeBootstrapVisibleState(t *testing.T, revision uint64, withTask bool) ClusterState {
	t.Helper()
	st := ClusterState{
		SchemaVersion: CurrentSchemaVersion,
		ClusterID:     "cluster-runtime-visible",
		Revision:      revision,
		Config: ClusterConfig{
			SlotCount:             2,
			HashSlotCount:         4,
			ReplicaCount:          1,
			DefaultCapacityWeight: 1,
		},
		Controllers: []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}},
		Nodes: []Node{{
			NodeID:         1,
			Addr:           "n1",
			Roles:          []NodeRole{NodeRoleControllerVoter, NodeRoleData},
			JoinState:      NodeJoinStateActive,
			Status:         NodeStatusAlive,
			CapacityWeight: 1,
		}},
		Slots: []SlotAssignment{{
			SlotID:          1,
			DesiredPeers:    []uint64{1},
			ConfigEpoch:     1,
			PreferredLeader: 1,
		}},
		HashSlots: HashSlotTable{
			Version:   CurrentHashSlotTableVersion,
			SlotCount: 4,
			Ranges: []HashSlotRange{
				{From: 0, To: 1, SlotID: 1},
				{From: 2, To: 3, SlotID: 2},
			},
		},
	}
	if withTask {
		st.Tasks = []ReconcileTask{{
			TaskID:           "slot-1-bootstrap-1",
			SlotID:           1,
			Kind:             TaskKindBootstrap,
			Step:             TaskStepCreateSlot,
			TargetNode:       1,
			TargetPeers:      []uint64{1},
			CompletionPolicy: TaskCompletionPolicyAllTargetPeers,
			ParticipantProgress: []TaskParticipantProgress{{
				NodeID: 1,
				Status: TaskParticipantStatusDone,
			}},
			ConfigEpoch: 1,
			Status:      TaskStatusPending,
		}}
	}
	if err := st.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	checksum, err := cv2state.Checksum(st)
	if err != nil {
		t.Fatalf("Checksum() error = %v", err)
	}
	st.Checksum = checksum
	return st
}

func mirroredPrepareDataNodeStateForTest(t *testing.T, clusterID string, revision uint64) ClusterState {
	t.Helper()
	st := ClusterState{
		SchemaVersion:    CurrentSchemaVersion,
		ClusterID:        clusterID,
		Revision:         revision,
		AppliedRaftIndex: 22,
		UpdatedAt:        time.Unix(1710000000, 0).UTC(),
		Config: ClusterConfig{
			SlotCount:             1,
			HashSlotCount:         4,
			ReplicaCount:          1,
			DefaultCapacityWeight: 1,
		},
		Controllers: []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}},
		Nodes: []Node{
			{NodeID: 1, Addr: "n1", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 1},
			{NodeID: 4, Addr: "n4", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 1},
		},
		Slots:     []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: HashSlotTable{Version: CurrentHashSlotTableVersion, SlotCount: 4, Ranges: []HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
	if err := st.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	checksum, err := cv2state.Checksum(st)
	if err != nil {
		t.Fatalf("Checksum() error = %v", err)
	}
	st.Checksum = checksum
	return st
}

func checksumClusterStateForTest(t *testing.T, st ClusterState) ClusterState {
	t.Helper()
	if err := st.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	checksum, err := cv2state.Checksum(st)
	if err != nil {
		t.Fatalf("Checksum() error = %v", err)
	}
	st.Checksum = checksum
	return st
}

func newPrepareControllerVoterRuntimeForTest(t *testing.T, stateDir string, clusterID string) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:       4,
		Addr:         "n4",
		StateDir:     stateDir,
		ClusterID:    clusterID,
		Role:         RuntimeRoleMirror,
		Voters:       []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
		SyncClient:   &SyncClient{},
		TickInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	return runtime
}

func prepareControllerVoterRequestForTest(clusterID string, expectedRevision uint64) PrepareControllerVoterRequest {
	return PrepareControllerVoterRequest{
		NodeID:           4,
		ClusterID:        clusterID,
		ExpectedRevision: expectedRevision,
		NextVoters:       []Voter{{NodeID: 1, Addr: "n1"}, {NodeID: 4, Addr: "n4"}},
	}
}

func assertStateFileRevisionForTest(t *testing.T, path string, revision uint64) {
	t.Helper()
	backup, err := statefile.New(path).Load(context.Background())
	if err != nil {
		t.Fatalf("Load(%s) error = %v", path, err)
	}
	if backup.Revision != revision {
		t.Fatalf("%s revision = %d, want %d", path, backup.Revision, revision)
	}
}

func findNodeForTest(st ClusterState, nodeID uint64) (Node, bool) {
	for _, node := range st.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return Node{}, false
}

func sameNodeRoles(left, right []NodeRole) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[NodeRole]int, len(left))
	for _, role := range left {
		seen[role]++
	}
	for _, role := range right {
		seen[role]--
		if seen[role] < 0 {
			return false
		}
	}
	return true
}

func replacePeerForTest(peers []uint64, source uint64, target uint64) []uint64 {
	out := append([]uint64(nil), peers...)
	for i, peer := range out {
		if peer == source {
			out[i] = target
			return out
		}
	}
	return out
}

func sameUint64SetForTest(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[uint64]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		seen[value]--
		if seen[value] < 0 {
			return false
		}
	}
	return true
}

func setRuntimeStateForTest(runtime *Runtime, st ClusterState) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.state = st.Clone()
}

func commandWithNode(expectedRevision uint64, node Node) command.Command {
	return command.Command{
		Kind:             command.KindUpsertNode,
		ExpectedRevision: &expectedRevision,
		Node:             &node,
	}
}

type fixedPeerPicker struct {
	ids       []uint64
	endpoints map[uint64]Endpoint
}

func (p fixedPeerPicker) Endpoint(nodeID uint64) (Endpoint, bool) {
	endpoint, ok := p.endpoints[nodeID]
	return endpoint, ok
}

func (p fixedPeerPicker) PeerIDs() []uint64 {
	return append([]uint64(nil), p.ids...)
}
