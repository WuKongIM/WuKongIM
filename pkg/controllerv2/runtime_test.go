package controllerv2

import (
	"context"
	"testing"
	"time"
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
