package controllerv2

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/server"
	cv2state "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
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
