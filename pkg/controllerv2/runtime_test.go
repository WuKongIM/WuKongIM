package controllerv2

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
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
