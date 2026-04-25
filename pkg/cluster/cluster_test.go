package cluster

import (
	"bytes"
	"context"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/slotmigration"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

type testClusterStateMachine struct{}

func (testClusterStateMachine) Apply(context.Context, multiraft.Command) ([]byte, error) {
	return nil, nil
}

func (testClusterStateMachine) Restore(context.Context, multiraft.Snapshot) error {
	return nil
}

func (testClusterStateMachine) Snapshot(context.Context) (multiraft.Snapshot, error) {
	return multiraft.Snapshot{}, nil
}

type testHashSlotOwnershipUpdater struct {
	testClusterStateMachine
	updates [][]uint16

	outgoingDeltaTargets []map[uint16]multiraft.SlotID
	incomingDeltaSlots   [][]uint16
	deltaForwarder       func(context.Context, multiraft.SlotID, multiraft.Command) error
}

func (u *testHashSlotOwnershipUpdater) UpdateOwnedHashSlots(hashSlots []uint16) {
	u.updates = append(u.updates, append([]uint16(nil), hashSlots...))
}

func (u *testHashSlotOwnershipUpdater) UpdateOutgoingDeltaTargets(targets map[uint16]multiraft.SlotID) {
	cloned := make(map[uint16]multiraft.SlotID, len(targets))
	for hashSlot, target := range targets {
		cloned[hashSlot] = target
	}
	u.outgoingDeltaTargets = append(u.outgoingDeltaTargets, cloned)
}

func (u *testHashSlotOwnershipUpdater) UpdateIncomingDeltaHashSlots(hashSlots []uint16) {
	u.incomingDeltaSlots = append(u.incomingDeltaSlots, append([]uint16(nil), hashSlots...))
}

func (u *testHashSlotOwnershipUpdater) SetDeltaForwarder(fn func(context.Context, multiraft.SlotID, multiraft.Command) error) {
	u.deltaForwarder = fn
}

type singleNodeDiscovery struct {
	addr string
}

func (d singleNodeDiscovery) Resolve(nodeID transport.NodeID) (string, error) {
	if nodeID != 1 {
		return "", transport.ErrNodeNotFound
	}
	return d.addr, nil
}

type testHashSlotSnapshotStateMachine struct {
	testHashSlotOwnershipUpdater
	exportedHashSlots []uint16
	importedSnapshots []metadb.SlotSnapshot
	snapshotData      []byte
}

func (s *testHashSlotSnapshotStateMachine) ExportHashSlotSnapshot(_ context.Context, hashSlot uint16) (metadb.SlotSnapshot, error) {
	s.exportedHashSlots = append(s.exportedHashSlots, hashSlot)
	return metadb.SlotSnapshot{
		HashSlots: []uint16{hashSlot},
		Data:      append([]byte(nil), s.snapshotData...),
	}, nil
}

func (s *testHashSlotSnapshotStateMachine) ImportHashSlotSnapshot(_ context.Context, snap metadb.SlotSnapshot) error {
	cloned := metadb.SlotSnapshot{
		HashSlots: append([]uint16(nil), snap.HashSlots...),
		Data:      append([]byte(nil), snap.Data...),
	}
	s.importedSnapshots = append(s.importedSnapshots, cloned)
	return nil
}

func TestObservationPeersForGroupPreferRuntimeMembership(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{
			Slots: []SlotConfig{
				{SlotID: 7, Peers: []multiraft.NodeID{1, 2, 3}},
			},
		},
		runState: newRuntimeState(),
		agentResources: agentResources{
			assignments: newAssignmentCache(),
		},
	}
	cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{
		{SlotID: 7, DesiredPeers: []uint64{2, 3}},
	})
	cluster.setRuntimePeers(7, []multiraft.NodeID{4, 5, 6})

	peers := cluster.observationPeersForSlot(7)
	if len(peers) != 3 || peers[0] != 4 || peers[1] != 5 || peers[2] != 6 {
		t.Fatalf("observationPeersForSlot() = %v", peers)
	}
}

func TestControllerReportAddrUsesBoundListener(t *testing.T) {
	srv := newStartedTestServer(t)

	cluster := &Cluster{
		cfg: Config{ListenAddr: "127.0.0.1:0"},
		transportResources: transportResources{
			server: srv,
		},
	}

	if got, want := cluster.controllerReportAddr(), srv.Listener().Addr().String(); got != want {
		t.Fatalf("controllerReportAddr() = %q, want %q", got, want)
	}
}

func TestListObservedRuntimeViewsUsesControllerClientWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertRuntimeView(context.Background(), controllermeta.SlotRuntimeView{
		SlotID:       1,
		CurrentPeers: []uint64{1, 2, 3},
		LastReportAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertRuntimeView() error = %v", err)
	}

	sentinel := errors.New("controller client called")
	cluster := newTestClusterWithController(store, 0, fakeControllerClient{listRuntimeViewsErr: sentinel})

	_, err = cluster.ListObservedRuntimeViews(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("ListObservedRuntimeViews() error = %v, want %v", err, sentinel)
	}
}

func TestListObservedRuntimeViewsReadsLeaderObservationSnapshot(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.controllerClient = fakeControllerClient{
		listRuntimeViewsErr: errors.New("controller client should not be used by local leader"),
	}

	if err := host.meta.UpsertRuntimeView(context.Background(), controllermeta.SlotRuntimeView{
		SlotID:       7,
		CurrentPeers: []uint64{9, 9, 9},
		LastReportAt: time.Unix(1, 0),
	}); err != nil {
		t.Fatalf("UpsertRuntimeView() error = %v", err)
	}
	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     2,
		ObservedAt: time.Unix(1710000300, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:              7,
			CurrentPeers:        []uint64{1, 2, 3},
			LeaderID:            2,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 9,
			LastReportAt:        time.Unix(1710000301, 0),
		}},
	})

	views, err := cluster.ListObservedRuntimeViews(context.Background())
	if err != nil {
		t.Fatalf("ListObservedRuntimeViews() error = %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("ListObservedRuntimeViews() len = %d, want 1", len(views))
	}
	if got, want := views[0].CurrentPeers, []uint64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListObservedRuntimeViews() peers = %v, want %v", got, want)
	}
}

func TestSnapshotPlannerStateUsesObservationSnapshotForRuntime(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	if err := host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          uint64(cluster.cfg.NodeID),
		Addr:            cluster.cfg.Nodes[0].Addr,
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710000400, 0),
		CapacityWeight:  1,
	}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}
	if err := host.meta.UpsertRuntimeView(context.Background(), controllermeta.SlotRuntimeView{
		SlotID:       1,
		CurrentPeers: []uint64{9, 9, 9},
		LastReportAt: time.Unix(1, 0),
	}); err != nil {
		t.Fatalf("UpsertRuntimeView() error = %v", err)
	}
	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     uint64(cluster.cfg.NodeID),
		ObservedAt: time.Unix(1710000401, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:              1,
			CurrentPeers:        []uint64{1},
			LeaderID:            1,
			HealthyVoters:       1,
			HasQuorum:           true,
			ObservedConfigEpoch: 3,
			LastReportAt:        time.Unix(1710000402, 0),
		}},
	})

	state, err := cluster.snapshotPlannerState(context.Background())
	if err != nil {
		t.Fatalf("snapshotPlannerState() error = %v", err)
	}
	if got, want := state.Runtime[1].CurrentPeers, []uint64{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshotPlannerState() runtime peers = %v, want %v", got, want)
	}
}

func TestSnapshotPlannerStateUsesMetadataSnapshotWhenWarm(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)

	storeNode := controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710010000, 0),
		CapacityWeight:  1,
	}
	storeAssignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	storeTask := controllermeta.ReconcileTask{
		SlotID:  1,
		Kind:    controllermeta.TaskKindBootstrap,
		Step:    controllermeta.TaskStepAddLearner,
		Status:  controllermeta.TaskStatusPending,
		Attempt: 1,
	}
	requireNoErr(t, host.meta.UpsertNode(context.Background(), storeNode))
	requireNoErr(t, host.meta.UpsertAssignment(context.Background(), storeAssignment))
	requireNoErr(t, host.meta.UpsertTask(context.Background(), storeTask))

	setControllerMetadataSnapshotStateForTest(host, controllerMetadataSnapshot{
		Nodes: []controllermeta.ClusterNode{{
			NodeID:          1,
			Addr:            "127.0.0.1:7001",
			Status:          controllermeta.NodeStatusAlive,
			LastHeartbeatAt: time.Unix(1710010001, 0),
			CapacityWeight:  9,
		}},
		NodesByID: map[uint64]controllermeta.ClusterNode{
			1: {
				NodeID:          1,
				Addr:            "127.0.0.1:7001",
				Status:          controllermeta.NodeStatusAlive,
				LastHeartbeatAt: time.Unix(1710010001, 0),
				CapacityWeight:  9,
			},
		},
		Assignments: []controllermeta.SlotAssignment{{
			SlotID:       1,
			DesiredPeers: []uint64{1, 2, 3},
			ConfigEpoch:  9,
		}},
		AssignmentsBySlot: map[uint32]controllermeta.SlotAssignment{
			1: {
				SlotID:       1,
				DesiredPeers: []uint64{1, 2, 3},
				ConfigEpoch:  9,
			},
		},
		Tasks: []controllermeta.ReconcileTask{{
			SlotID:  1,
			Kind:    controllermeta.TaskKindRepair,
			Step:    controllermeta.TaskStepPromote,
			Status:  controllermeta.TaskStatusRetrying,
			Attempt: 7,
		}},
		TasksBySlot: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:  1,
				Kind:    controllermeta.TaskKindRepair,
				Step:    controllermeta.TaskStepPromote,
				Status:  controllermeta.TaskStatusRetrying,
				Attempt: 7,
			},
		},
		LeaderID:   host.localNode,
		Generation: 1,
		Ready:      true,
		Dirty:      false,
	})

	state, err := cluster.snapshotPlannerState(context.Background())
	if err != nil {
		t.Fatalf("snapshotPlannerState() error = %v", err)
	}
	if got := state.Nodes[1].CapacityWeight; got != 9 {
		t.Fatalf("snapshotPlannerState() node capacity = %d, want 9 from metadata snapshot", got)
	}
	if got := state.Assignments[1].ConfigEpoch; got != 9 {
		t.Fatalf("snapshotPlannerState() assignment config epoch = %d, want 9 from metadata snapshot", got)
	}
	if got := state.Tasks[1].Attempt; got != 7 {
		t.Fatalf("snapshotPlannerState() task attempt = %d, want 7 from metadata snapshot", got)
	}
}

func TestSnapshotPlannerStateFallsBackToStoreWhenMetadataSnapshotDirty(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)

	storeNode := controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710010100, 0),
		CapacityWeight:  2,
	}
	storeAssignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 4},
		ConfigEpoch:  4,
	}
	storeTask := controllermeta.ReconcileTask{
		SlotID:  1,
		Kind:    controllermeta.TaskKindBootstrap,
		Step:    controllermeta.TaskStepAddLearner,
		Status:  controllermeta.TaskStatusPending,
		Attempt: 2,
	}
	requireNoErr(t, host.meta.UpsertNode(context.Background(), storeNode))
	requireNoErr(t, host.meta.UpsertAssignment(context.Background(), storeAssignment))
	requireNoErr(t, host.meta.UpsertTask(context.Background(), storeTask))

	setControllerMetadataSnapshotStateForTest(host, controllerMetadataSnapshot{
		Nodes: []controllermeta.ClusterNode{{
			NodeID:          1,
			Addr:            "127.0.0.1:7001",
			Status:          controllermeta.NodeStatusAlive,
			LastHeartbeatAt: time.Unix(1710010101, 0),
			CapacityWeight:  99,
		}},
		NodesByID: map[uint64]controllermeta.ClusterNode{
			1: {
				NodeID:          1,
				Addr:            "127.0.0.1:7001",
				Status:          controllermeta.NodeStatusAlive,
				LastHeartbeatAt: time.Unix(1710010101, 0),
				CapacityWeight:  99,
			},
		},
		Assignments: []controllermeta.SlotAssignment{{
			SlotID:       1,
			DesiredPeers: []uint64{9, 9, 9},
			ConfigEpoch:  99,
		}},
		AssignmentsBySlot: map[uint32]controllermeta.SlotAssignment{
			1: {
				SlotID:       1,
				DesiredPeers: []uint64{9, 9, 9},
				ConfigEpoch:  99,
			},
		},
		Tasks: []controllermeta.ReconcileTask{{
			SlotID:  1,
			Kind:    controllermeta.TaskKindRepair,
			Step:    controllermeta.TaskStepPromote,
			Status:  controllermeta.TaskStatusRetrying,
			Attempt: 99,
		}},
		TasksBySlot: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:  1,
				Kind:    controllermeta.TaskKindRepair,
				Step:    controllermeta.TaskStepPromote,
				Status:  controllermeta.TaskStatusRetrying,
				Attempt: 99,
			},
		},
		LeaderID:   host.localNode,
		Generation: 1,
		Ready:      true,
		Dirty:      true,
	})

	state, err := cluster.snapshotPlannerState(context.Background())
	if err != nil {
		t.Fatalf("snapshotPlannerState() error = %v", err)
	}
	if got := state.Nodes[1].CapacityWeight; got != 2 {
		t.Fatalf("snapshotPlannerState() node capacity = %d, want 2 from store fallback", got)
	}
	if got := state.Assignments[1].ConfigEpoch; got != 4 {
		t.Fatalf("snapshotPlannerState() assignment config epoch = %d, want 4 from store fallback", got)
	}
	if got := state.Tasks[1].Attempt; got != 2 {
		t.Fatalf("snapshotPlannerState() task attempt = %d, want 2 from store fallback", got)
	}
}

func TestControllerTickOnceSkipsPlanningUntilRuntimeWarm(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.cfg.SlotReplicaN = 1
	if err := host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          uint64(cluster.cfg.NodeID),
		Addr:            cluster.cfg.Nodes[0].Addr,
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Now(),
		CapacityWeight:  1,
	}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}

	cluster.controllerTickOnce(context.Background())

	tasks, err := host.meta.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("ListTasks() = %#v, want no planning during warmup", tasks)
	}
}

func TestControllerTickOnceRunsAfterRuntimeFullSync(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.cfg.SlotReplicaN = 1
	if err := host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          uint64(cluster.cfg.NodeID),
		Addr:            cluster.cfg.Nodes[0].Addr,
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Now(),
		CapacityWeight:  1,
	}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}
	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     uint64(cluster.cfg.NodeID),
		ObservedAt: time.Unix(1710000601, 0),
		FullSync:   true,
	})

	cluster.controllerTickOnce(context.Background())

	tasks, err := host.meta.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) == 0 {
		t.Fatalf("ListTasks() = %#v, want planner to run after runtime full sync", tasks)
	}
}

func TestControllerTickOnceDoesNotProposeEvaluateTimeouts(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     2,
		ObservedAt: time.Unix(1710000700, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:              1,
			CurrentPeers:        []uint64{1},
			LeaderID:            1,
			HealthyVoters:       1,
			HasQuorum:           true,
			ObservedConfigEpoch: 3,
			LastReportAt:        time.Unix(1710000701, 0),
		}},
	})

	before, err := host.raftDB.ForController().LastIndex(context.Background())
	if err != nil {
		t.Fatalf("LastIndex(before) error = %v", err)
	}

	cluster.controllerTickOnce(context.Background())

	after, err := host.raftDB.ForController().LastIndex(context.Background())
	if err != nil {
		t.Fatalf("LastIndex(after) error = %v", err)
	}
	if after != before {
		t.Fatalf("LastIndex() after idle controllerTickOnce = %d, want unchanged %d", after, before)
	}
}

func TestControllerPlannerRunsOnDirtyWake(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.cfg.SlotReplicaN = 1
	if err := host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          uint64(cluster.cfg.NodeID),
		Addr:            cluster.cfg.Nodes[0].Addr,
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Now(),
		CapacityWeight:  1,
	}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}
	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     uint64(cluster.cfg.NodeID),
		ObservedAt: time.Unix(1710000801, 0),
		FullSync:   true,
	})

	cluster.plannerWakeOnce(context.Background())

	tasks, err := host.meta.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) == 0 {
		t.Fatalf("ListTasks() = %#v, want planner wake to schedule work", tasks)
	}
}

func TestControllerPlannerSafetyTickStillEvaluatesWhenNoHintArrives(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.cfg.SlotReplicaN = 1
	if err := host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          uint64(cluster.cfg.NodeID),
		Addr:            cluster.cfg.Nodes[0].Addr,
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Now(),
		CapacityWeight:  1,
	}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}
	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     uint64(cluster.cfg.NodeID),
		ObservedAt: time.Unix(1710000901, 0),
		FullSync:   true,
	})
	if !host.consumePlannerDirty() {
		t.Fatal("consumePlannerDirty() = false, want runtime full sync to mark the planner dirty first")
	}

	cluster.plannerSafetyOnce(context.Background())

	tasks, err := host.meta.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) == 0 {
		t.Fatalf("ListTasks() = %#v, want planner safety tick to evaluate even without a wake", tasks)
	}
}

func TestListObservedRuntimeViewsFallsBackToLocalControllerMetaWhenLeaderUnavailable(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	view := controllermeta.SlotRuntimeView{
		SlotID:       1,
		CurrentPeers: []uint64{1, 2, 3},
		LastReportAt: time.Now(),
	}
	if err := store.UpsertRuntimeView(context.Background(), view); err != nil {
		t.Fatalf("UpsertRuntimeView() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{listRuntimeViewsErr: ErrNoLeader})

	views, err := cluster.ListObservedRuntimeViews(context.Background())
	if err != nil {
		t.Fatalf("ListObservedRuntimeViews() error = %v", err)
	}
	if len(views) != 1 || views[0].SlotID != view.SlotID {
		t.Fatalf("ListObservedRuntimeViews() = %v", views)
	}
}

func TestListObservedRuntimeViewsFallsBackToLocalControllerMetaWhenControllerReadTimesOut(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	view := controllermeta.SlotRuntimeView{
		SlotID:       1,
		CurrentPeers: []uint64{1, 2, 3},
		LastReportAt: time.Now(),
	}
	if err := store.UpsertRuntimeView(context.Background(), view); err != nil {
		t.Fatalf("UpsertRuntimeView() error = %v", err)
	}
	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{listRuntimeViewsErr: context.DeadlineExceeded})

	views, err := cluster.ListObservedRuntimeViews(context.Background())
	if err != nil {
		t.Fatalf("ListObservedRuntimeViews() error = %v", err)
	}
	if len(views) != 1 || views[0].SlotID != view.SlotID {
		t.Fatalf("ListObservedRuntimeViews() = %v", views)
	}
}

func TestListNodesStrictReturnsLeaderDataWithoutLocalFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          99,
		Addr:            "127.0.0.1:7099",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Now(),
		CapacityWeight:  1,
	}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{
		nodes: []controllermeta.ClusterNode{{
			NodeID: 1,
			Addr:   "127.0.0.1:7001",
		}},
	})

	nodes, err := cluster.ListNodesStrict(context.Background())
	if err != nil {
		t.Fatalf("ListNodesStrict() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != 1 {
		t.Fatalf("ListNodesStrict() = %v, want leader data only", nodes)
	}
}

func TestListNodesStrictReturnsLeaderReadErrorWithoutLocalFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          99,
		Addr:            "127.0.0.1:7099",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Now(),
		CapacityWeight:  1,
	}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{listNodesErr: ErrNoLeader})

	_, err = cluster.ListNodesStrict(context.Background())
	if !errors.Is(err, ErrNoLeader) {
		t.Fatalf("ListNodesStrict() error = %v, want %v", err, ErrNoLeader)
	}
}

func TestListObservedRuntimeViewsStrictReturnsLeaderReadErrorWithoutLocalFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertRuntimeView(context.Background(), controllermeta.SlotRuntimeView{
		SlotID:       9,
		CurrentPeers: []uint64{9, 9, 9},
		LastReportAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertRuntimeView() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{listRuntimeViewsErr: context.DeadlineExceeded})

	_, err = cluster.ListObservedRuntimeViewsStrict(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ListObservedRuntimeViewsStrict() error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestListSlotAssignmentsStrictReturnsLeaderReadErrorWithoutLocalFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertAssignment(context.Background(), controllermeta.SlotAssignment{
		SlotID:       7,
		DesiredPeers: []uint64{7, 8, 9},
		ConfigEpoch:  5,
	}); err != nil {
		t.Fatalf("UpsertAssignment() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{assignmentsErr: ErrNoLeader})

	_, err = cluster.ListSlotAssignmentsStrict(context.Background())
	if !errors.Is(err, ErrNoLeader) {
		t.Fatalf("ListSlotAssignmentsStrict() error = %v, want %v", err, ErrNoLeader)
	}
}

func TestListSlotAssignmentsFallsBackToLocalControllerMetaWhenLeaderUnavailable(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 2, 3},
		ConfigEpoch:  1,
	}
	if err := store.UpsertAssignment(context.Background(), assignment); err != nil {
		t.Fatalf("UpsertAssignment() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{assignmentsErr: ErrNoLeader})

	assignments, err := cluster.ListSlotAssignments(context.Background())
	if err != nil {
		t.Fatalf("ListSlotAssignments() error = %v", err)
	}
	if len(assignments) != 1 || assignments[0].SlotID != assignment.SlotID {
		t.Fatalf("ListSlotAssignments() = %v", assignments)
	}
}

func TestListSlotAssignmentsFallsBackToLocalControllerMetaWhenControllerReadTimesOut(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 3, 4},
		ConfigEpoch:  2,
	}
	if err := store.UpsertAssignment(context.Background(), assignment); err != nil {
		t.Fatalf("UpsertAssignment() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{assignmentsErr: context.DeadlineExceeded})

	assignments, err := cluster.ListSlotAssignments(context.Background())
	if err != nil {
		t.Fatalf("ListSlotAssignments() error = %v", err)
	}
	if len(assignments) != 1 || assignments[0].SlotID != assignment.SlotID {
		t.Fatalf("ListSlotAssignments() = %v", assignments)
	}
}

func TestListTasksUsesControllerClientWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	if err := store.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	sentinel := errors.New("controller client called")
	cluster := newTestClusterWithController(store, 0, fakeControllerClient{listTasksErr: sentinel})

	_, err = cluster.ListTasks(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("ListTasks() error = %v, want %v", err, sentinel)
	}
}

func TestListTasksUsesLocalControllerFastPathWithoutSelfRPC(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	requireNoErr(t, host.meta.UpsertTask(context.Background(), task))

	cluster.controllerClient = newControllerClient(cluster, cluster.cfg.Nodes, nil)

	tasks, err := cluster.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].SlotID != task.SlotID || tasks[0].Kind != task.Kind {
		t.Fatalf("ListTasks() = %#v, want local controller task for slot %d", tasks, task.SlotID)
	}
}

func TestListTasksFallsBackToLocalControllerMetaWhenLeaderUnavailable(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusRetrying,
		Attempt:   2,
		NextRunAt: time.Now().Add(time.Second),
	}
	if err := store.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{listTasksErr: ErrNoLeader})

	tasks, err := cluster.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].SlotID != task.SlotID {
		t.Fatalf("ListTasks() = %v", tasks)
	}
}

func TestListTasksStrictReturnsLeaderTasksWithoutLocalFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertTask(context.Background(), controllermeta.ReconcileTask{
		SlotID:    99,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{
		listTasks: []controllermeta.ReconcileTask{{
			SlotID:    1,
			Kind:      controllermeta.TaskKindBootstrap,
			Step:      controllermeta.TaskStepAddLearner,
			Status:    controllermeta.TaskStatusPending,
			NextRunAt: time.Now(),
		}},
	})

	tasks, err := cluster.ListTasksStrict(context.Background())
	if err != nil {
		t.Fatalf("ListTasksStrict() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].SlotID != 1 {
		t.Fatalf("ListTasksStrict() = %v, want leader data only", tasks)
	}
}

func TestListTasksStrictReturnsLeaderReadErrorWithoutLocalFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertTask(context.Background(), controllermeta.ReconcileTask{
		SlotID:    99,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusRetrying,
		Attempt:   1,
		NextRunAt: time.Now().Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{listTasksErr: ErrNoLeader})

	_, err = cluster.ListTasksStrict(context.Background())
	if !errors.Is(err, ErrNoLeader) {
		t.Fatalf("ListTasksStrict() error = %v, want %v", err, ErrNoLeader)
	}
}

func TestManagedSlotsReadyAllowsIdleLocalNode(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{
			NodeID:    1,
			SlotCount: 2,
		},
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				assignments: []controllermeta.SlotAssignment{
					{SlotID: 1, DesiredPeers: []uint64{2, 3}},
					{SlotID: 2, DesiredPeers: []uint64{2, 3}},
				},
			},
		},
	}

	ready, err := cluster.managedSlotsReady(context.Background())
	if err != nil {
		t.Fatalf("managedSlotsReady() error = %v", err)
	}
	if !ready {
		t.Fatal("managedSlotsReady() = false, want true when local node has no assigned slots")
	}
}

func TestWaitForManagedSlotsReadyUsesConfiguredObservationInterval(t *testing.T) {
	start := time.Now()
	cluster := &Cluster{
		cfg: Config{
			NodeID:    1,
			SlotCount: 1,
			Timeouts: Timeouts{
				ControllerObservation: 40 * time.Millisecond,
			},
		},
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				refreshAssignmentsFn: func(context.Context) ([]controllermeta.SlotAssignment, error) {
					if time.Since(start) < 15*time.Millisecond {
						return nil, nil
					}
					return []controllermeta.SlotAssignment{
						{SlotID: 1, DesiredPeers: []uint64{2, 3}},
					}, nil
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	if err := cluster.WaitForManagedSlotsReady(ctx); err != nil {
		t.Fatalf("WaitForManagedSlotsReady() error = %v, want nil", err)
	}
}

func TestSlotIDsUseInitialSlotCount(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{
			HashSlotCount:    8,
			InitialSlotCount: 3,
		},
	}

	slotIDs := cluster.SlotIDs()
	want := []multiraft.SlotID{1, 2, 3}
	if len(slotIDs) != len(want) {
		t.Fatalf("SlotIDs() = %v, want %v", slotIDs, want)
	}
	for i := range want {
		if slotIDs[i] != want[i] {
			t.Fatalf("SlotIDs() = %v, want %v", slotIDs, want)
		}
	}
}

func TestNewStateMachineUsesAssignedHashSlots(t *testing.T) {
	var gotSlotID multiraft.SlotID
	var gotHashSlots []uint16
	cluster := &Cluster{
		cfg: Config{
			NewStateMachineWithHashSlots: func(slotID multiraft.SlotID, hashSlots []uint16) (multiraft.StateMachine, error) {
				gotSlotID = slotID
				gotHashSlots = append([]uint16(nil), hashSlots...)
				return testClusterStateMachine{}, nil
			},
		},
		router: NewRouter(NewHashSlotTable(8, 2), 1, nil),
	}

	if _, err := cluster.newStateMachine(2); err != nil {
		t.Fatalf("newStateMachine() error = %v", err)
	}
	if gotSlotID != 2 {
		t.Fatalf("slotID = %d, want 2", gotSlotID)
	}
	want := []uint16{4, 5, 6, 7}
	if len(gotHashSlots) != len(want) {
		t.Fatalf("hashSlots = %v, want %v", gotHashSlots, want)
	}
	for i := range want {
		if gotHashSlots[i] != want[i] {
			t.Fatalf("hashSlots = %v, want %v", gotHashSlots, want)
		}
	}
}

func TestApplyHashSlotTablePayloadUpdatesRegisteredStateMachineOwnership(t *testing.T) {
	updater := &testHashSlotOwnershipUpdater{}
	cluster := &Cluster{
		router: NewRouter(NewHashSlotTable(8, 2), 1, nil),
		hashSlotRuntimeResources: hashSlotRuntimeResources{
			runtimeStateMachines: map[multiraft.SlotID]hashSlotOwnershipUpdater{
				2: updater,
			},
		},
	}

	updated := NewHashSlotTable(8, 2)
	updated.Reassign(0, 2)

	if err := cluster.applyHashSlotTablePayload(updated.Encode()); err != nil {
		t.Fatalf("applyHashSlotTablePayload() error = %v", err)
	}
	if len(updater.updates) != 1 {
		t.Fatalf("UpdateOwnedHashSlots() calls = %d, want 1", len(updater.updates))
	}

	want := []uint16{0, 4, 5, 6, 7}
	got := updater.updates[0]
	if len(got) != len(want) {
		t.Fatalf("updated hashSlots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("updated hashSlots = %v, want %v", got, want)
		}
	}
}

func TestApplyHashSlotTablePayloadPublishesDeltaMigrationRuntime(t *testing.T) {
	source := &testHashSlotOwnershipUpdater{}
	target := &testHashSlotOwnershipUpdater{}
	cluster := &Cluster{
		router: NewRouter(NewHashSlotTable(8, 2), 1, nil),
		hashSlotRuntimeResources: hashSlotRuntimeResources{
			runtimeStateMachines: map[multiraft.SlotID]hashSlotOwnershipUpdater{
				1: source,
				2: target,
			},
		},
	}

	updated := NewHashSlotTable(8, 2)
	updated.StartMigration(3, 1, 2)
	updated.AdvanceMigration(3, PhaseDelta)

	if err := cluster.applyHashSlotTablePayload(updated.Encode()); err != nil {
		t.Fatalf("applyHashSlotTablePayload() error = %v", err)
	}

	if len(source.outgoingDeltaTargets) != 1 {
		t.Fatalf("source UpdateOutgoingDeltaTargets() calls = %d, want 1", len(source.outgoingDeltaTargets))
	}
	if len(target.incomingDeltaSlots) != 1 {
		t.Fatalf("target UpdateIncomingDeltaHashSlots() calls = %d, want 1", len(target.incomingDeltaSlots))
	}

	if got := source.outgoingDeltaTargets[0][3]; got != 2 {
		t.Fatalf("source outgoing target for hash slot 3 = %d, want 2", got)
	}
	gotIncoming := target.incomingDeltaSlots[0]
	if len(gotIncoming) != 1 || gotIncoming[0] != 3 {
		t.Fatalf("target incoming delta hash slots = %v, want [3]", gotIncoming)
	}
}

func TestNewStateMachineInstallsDeltaForwarder(t *testing.T) {
	updater := &testHashSlotOwnershipUpdater{}
	cluster := &Cluster{
		cfg: Config{
			NewStateMachineWithHashSlots: func(slotID multiraft.SlotID, hashSlots []uint16) (multiraft.StateMachine, error) {
				return updater, nil
			},
		},
		router: NewRouter(NewHashSlotTable(8, 2), 1, nil),
	}

	if _, err := cluster.newStateMachine(1); err != nil {
		t.Fatalf("newStateMachine() error = %v", err)
	}
	if updater.deltaForwarder == nil {
		t.Fatal("deltaForwarder = nil, want installed callback")
	}
}

func TestLegacyProposeHashSlotUsesSingleAssignedHashSlot(t *testing.T) {
	cluster := &Cluster{
		router: NewRouter(NewHashSlotTable(4, 4), 1, nil),
	}

	hashSlot, err := cluster.legacyProposeHashSlot(2)
	if err != nil {
		t.Fatalf("legacyProposeHashSlot() error = %v", err)
	}
	if hashSlot != 1 {
		t.Fatalf("legacyProposeHashSlot() = %d, want 1", hashSlot)
	}
}

func TestLegacyProposeHashSlotRejectsAmbiguousAssignment(t *testing.T) {
	cluster := &Cluster{
		router: NewRouter(NewHashSlotTable(8, 2), 1, nil),
	}

	_, err := cluster.legacyProposeHashSlot(1)
	if !errors.Is(err, ErrHashSlotRequired) {
		t.Fatalf("legacyProposeHashSlot() error = %v, want %v", err, ErrHashSlotRequired)
	}
}

func TestLegacyProposeHashSlotRejectsUnassignedSlot(t *testing.T) {
	cluster := &Cluster{
		router: NewRouter(NewHashSlotTable(4, 4), 1, nil),
	}

	_, err := cluster.legacyProposeHashSlot(9)
	if !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("legacyProposeHashSlot() error = %v, want %v", err, ErrSlotNotFound)
	}
}

func TestLocalAssignedGroupIDsFiltersAssignmentsToLocalNode(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{NodeID: 2},
	}

	slotIDs := cluster.localAssignedSlotIDs([]controllermeta.SlotAssignment{
		{SlotID: 1, DesiredPeers: []uint64{1, 2}},
		{SlotID: 2, DesiredPeers: []uint64{1, 3}},
		{SlotID: 3, DesiredPeers: []uint64{2, 3}},
	})

	want := []multiraft.SlotID{1, 3}
	if len(slotIDs) != len(want) {
		t.Fatalf("localAssignedSlotIDs() = %v, want %v", slotIDs, want)
	}
	for i := range want {
		if slotIDs[i] != want[i] {
			t.Fatalf("localAssignedSlotIDs() = %v, want %v", slotIDs, want)
		}
	}
}

func TestGetReconcileTaskReturnsLeaderUnavailableWhenControllerLeaderIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	if err := store.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{getTaskErr: ErrNoLeader})

	got, err := cluster.GetReconcileTask(context.Background(), task.SlotID)
	if !errors.Is(err, ErrNoLeader) {
		t.Fatalf("GetReconcileTask() error = %v, want %v", err, ErrNoLeader)
	}
	if got != (controllermeta.ReconcileTask{}) {
		t.Fatalf("GetReconcileTask() = %+v, want zero task on leader-unavailable read", got)
	}
}

func TestGetReconcileTaskReturnsReadTimeoutWhenControllerReadTimesOut(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		Step:       controllermeta.TaskStepAddLearner,
		SourceNode: 2,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusRetrying,
		Attempt:    1,
		NextRunAt:  time.Now(),
	}
	if err := store.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{getTaskErr: context.DeadlineExceeded})

	got, err := cluster.GetReconcileTask(context.Background(), task.SlotID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetReconcileTask() error = %v, want %v", err, context.DeadlineExceeded)
	}
	if got != (controllermeta.ReconcileTask{}) {
		t.Fatalf("GetReconcileTask() = %+v, want zero task on timeout read", got)
	}
}

func TestGetReconcileTaskStrictReturnsLeaderTaskWithoutLocalFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	localTask := controllermeta.ReconcileTask{
		SlotID:    9,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	if err := store.UpsertTask(context.Background(), localTask); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	leaderTask := controllermeta.ReconcileTask{
		SlotID:     9,
		Kind:       controllermeta.TaskKindRebalance,
		Step:       controllermeta.TaskStepTransferLeader,
		SourceNode: 2,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusRetrying,
		Attempt:    2,
		NextRunAt:  time.Now().Add(time.Minute),
	}
	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{
		tasks: map[uint32]controllermeta.ReconcileTask{
			leaderTask.SlotID: leaderTask,
		},
	})

	got, err := cluster.GetReconcileTaskStrict(context.Background(), leaderTask.SlotID)
	if err != nil {
		t.Fatalf("GetReconcileTaskStrict() error = %v", err)
	}
	if got.Kind != leaderTask.Kind || got.Step != leaderTask.Step || got.TargetNode != leaderTask.TargetNode {
		t.Fatalf("GetReconcileTaskStrict() = %+v, want leader task %+v", got, leaderTask)
	}
}

func TestGetReconcileTaskStrictReturnsNotFoundWithoutLocalFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertTask(context.Background(), controllermeta.ReconcileTask{
		SlotID:    8,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{})

	_, err = cluster.GetReconcileTaskStrict(context.Background(), 8)
	if !errors.Is(err, controllermeta.ErrNotFound) {
		t.Fatalf("GetReconcileTaskStrict() error = %v, want %v", err, controllermeta.ErrNotFound)
	}
}

func TestGetReconcileTaskStrictReturnsLeaderReadErrorWithoutLocalFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertTask(context.Background(), controllermeta.ReconcileTask{
		SlotID:    7,
		Kind:      controllermeta.TaskKindRepair,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, fakeControllerClient{getTaskErr: context.DeadlineExceeded})

	_, err = cluster.GetReconcileTaskStrict(context.Background(), 7)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetReconcileTaskStrict() error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestGroupAgentSyncAssignmentsFallsBackToLocalControllerMetaWhenControllerReadTimesOut(t *testing.T) {
	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 3, 4},
		ConfigEpoch:  2,
	}
	if err := store.UpsertAssignment(context.Background(), assignment); err != nil {
		t.Fatalf("UpsertAssignment() error = %v", err)
	}

	cache := newAssignmentCache()
	cluster := newTestClusterWithController(store, testControllerLeaderWaitTimeout, nil)
	cluster.assignments = cache
	agent := &slotAgent{
		cluster: cluster,
		client:  fakeControllerClient{assignmentsErr: context.DeadlineExceeded},
		cache:   cache,
	}

	if err := agent.SyncAssignments(context.Background()); err != nil {
		t.Fatalf("SyncAssignments() error = %v", err)
	}
	assignments := cache.Snapshot()
	if len(assignments) != 1 || assignments[0].SlotID != assignment.SlotID {
		t.Fatalf("cached assignments = %v", assignments)
	}
}

func TestSlotAgentListControllerNodesUsesLocalMetadataSnapshot(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710010200, 0),
		CapacityWeight:  1,
	}))
	setControllerMetadataSnapshotStateForTest(host, controllerMetadataSnapshot{
		Nodes: []controllermeta.ClusterNode{{
			NodeID:          1,
			Addr:            "127.0.0.1:7001",
			Status:          controllermeta.NodeStatusAlive,
			LastHeartbeatAt: time.Unix(1710010201, 0),
			CapacityWeight:  8,
		}},
		NodesByID: map[uint64]controllermeta.ClusterNode{
			1: {
				NodeID:          1,
				Addr:            "127.0.0.1:7001",
				Status:          controllermeta.NodeStatusAlive,
				LastHeartbeatAt: time.Unix(1710010201, 0),
				CapacityWeight:  8,
			},
		},
		LeaderID:   host.localNode,
		Generation: 1,
		Ready:      true,
		Dirty:      false,
	})

	agent := &slotAgent{
		cluster: cluster,
		client:  fakeControllerClient{listNodesErr: errors.New("should not call controller client")},
	}
	nodes, err := agent.listControllerNodes(context.Background())
	if err != nil {
		t.Fatalf("listControllerNodes() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].CapacityWeight != 8 {
		t.Fatalf("listControllerNodes() = %#v, want metadata snapshot node with capacity 8", nodes)
	}
}

func TestSlotAgentGetTaskUsesLocalMetadataSnapshot(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	requireNoErr(t, host.meta.UpsertTask(context.Background(), controllermeta.ReconcileTask{
		SlotID:  1,
		Kind:    controllermeta.TaskKindBootstrap,
		Step:    controllermeta.TaskStepAddLearner,
		Status:  controllermeta.TaskStatusPending,
		Attempt: 1,
	}))
	setControllerMetadataSnapshotStateForTest(host, controllerMetadataSnapshot{
		Tasks: []controllermeta.ReconcileTask{{
			SlotID:  1,
			Kind:    controllermeta.TaskKindRepair,
			Step:    controllermeta.TaskStepPromote,
			Status:  controllermeta.TaskStatusRetrying,
			Attempt: 6,
		}},
		TasksBySlot: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:  1,
				Kind:    controllermeta.TaskKindRepair,
				Step:    controllermeta.TaskStepPromote,
				Status:  controllermeta.TaskStatusRetrying,
				Attempt: 6,
			},
		},
		LeaderID:   host.localNode,
		Generation: 1,
		Ready:      true,
		Dirty:      false,
	})

	agent := &slotAgent{
		cluster: cluster,
		client:  fakeControllerClient{getTaskErr: errors.New("should not call controller client")},
	}
	task, err := agent.getTask(context.Background(), 1)
	if err != nil {
		t.Fatalf("getTask() error = %v", err)
	}
	if task.Attempt != 6 || task.Kind != controllermeta.TaskKindRepair {
		t.Fatalf("getTask() = %+v, want metadata snapshot task attempt 6 repair", task)
	}
}

func TestObserveHashSlotMigrationsAdvancesControllerLifecycle(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)

	var advanced []slotcontroller.MigrationRequest
	var finalized []slotcontroller.MigrationRequest
	worker := &fakeHashSlotMigrationWorker{
		transitionsByTick: [][]slotmigration.Transition{
			{{HashSlot: 3, Source: 1, Target: 2, To: slotmigration.PhaseDelta}},
			{{HashSlot: 3, Source: 1, Target: 2, To: slotmigration.PhaseSwitching}},
			{{HashSlot: 3, Source: 1, Target: 2, To: slotmigration.PhaseDone}},
		},
	}
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: worker,
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				advanceMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					advanced = append(advanced, req)
					return nil
				},
				finalizeMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					finalized = append(finalized, req)
					return nil
				},
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("first observeHashSlotMigrations() error = %v", err)
	}
	if len(advanced) != 1 || advanced[0].HashSlot != 3 || advanced[0].Phase != uint8(slotmigration.PhaseDelta) {
		t.Fatalf("advanced after first tick = %#v", advanced)
	}

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("second observeHashSlotMigrations() error = %v", err)
	}
	if len(advanced) != 2 || advanced[1].Phase != uint8(slotmigration.PhaseSwitching) {
		t.Fatalf("advanced after second tick = %#v", advanced)
	}

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("third observeHashSlotMigrations() error = %v", err)
	}
	if len(finalized) != 1 || finalized[0].HashSlot != 3 {
		t.Fatalf("finalized after third tick = %#v", finalized)
	}
}

func TestTransportPoolStatsMergeRaftAndRPCPools(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					if _, _, err := transport.ReadMessage(c); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	cluster := &Cluster{
		transportResources: transportResources{
			raftPool: transport.NewPool(transport.PoolConfig{
				Discovery:   singleNodeDiscovery{addr: ln.Addr().String()},
				Size:        2,
				DialTimeout: time.Second,
				QueueSizes:  [3]int{4, 4, 4},
				DefaultPri:  transport.PriorityRaft,
			}),
			rpcPool: transport.NewPool(transport.PoolConfig{
				Discovery:   singleNodeDiscovery{addr: ln.Addr().String()},
				Size:        3,
				DialTimeout: time.Second,
				QueueSizes:  [3]int{4, 4, 4},
				DefaultPri:  transport.PriorityRPC,
			}),
		},
	}
	defer cluster.raftPool.Close()
	defer cluster.rpcPool.Close()

	if err := cluster.raftPool.Send(1, 0, 1, []byte("raft")); err != nil {
		t.Fatalf("raftPool.Send() error = %v", err)
	}
	if err := cluster.rpcPool.Send(1, 0, 2, []byte("rpc-a")); err != nil {
		t.Fatalf("rpcPool first Send() error = %v", err)
	}
	if err := cluster.rpcPool.Send(1, 1, 2, []byte("rpc-b")); err != nil {
		t.Fatalf("rpcPool second Send() error = %v", err)
	}

	stats := cluster.TransportPoolStats()
	if len(stats) != 1 {
		t.Fatalf("TransportPoolStats() len = %d, want 1", len(stats))
	}
	if stats[0].NodeID != 1 {
		t.Fatalf("TransportPoolStats() nodeID = %d, want 1", stats[0].NodeID)
	}
	if stats[0].Active != 3 {
		t.Fatalf("TransportPoolStats() active = %d, want 3", stats[0].Active)
	}
	if stats[0].Idle != 2 {
		t.Fatalf("TransportPoolStats() idle = %d, want 2", stats[0].Idle)
	}

	_ = ln.Close()
	<-done
}

func TestObserveHashSlotMigrationsSkipsWhenLocalNodeIsNotSourceLeader(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)

	advanceCalls := 0
	worker := &fakeHashSlotMigrationWorker{}
	cluster := &Cluster{
		cfg:             Config{NodeID: 2},
		router:          NewRouter(table, 2, nil),
		migrationWorker: worker,
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				advanceMigrationFn: func(_ context.Context, _ slotcontroller.MigrationRequest) error {
					advanceCalls++
					return nil
				},
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("observeHashSlotMigrations() error = %v", err)
	}
	if advanceCalls != 0 {
		t.Fatalf("advanceCalls = %d, want 0", advanceCalls)
	}
	if len(worker.started) != 0 {
		t.Fatalf("worker started = %#v, want none", worker.started)
	}
}

func TestObserveHashSlotMigrationsMarksSwitchCompleteWhenControllerPhaseSwitches(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.AdvanceMigration(3, PhaseSwitching)

	worker := &fakeHashSlotMigrationWorker{
		active: []slotmigration.Migration{{HashSlot: 3, Source: 1, Target: 2, Phase: slotmigration.PhaseSwitching}},
	}
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: worker,
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("observeHashSlotMigrations() error = %v", err)
	}
	if got := worker.switchCompleted; len(got) != 1 || got[0] != 3 {
		t.Fatalf("worker.MarkSwitchComplete() calls = %v, want [3]", got)
	}
}

func TestObserveHashSlotMigrationsAbortsTimedOutMigration(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.AdvanceMigration(3, PhaseDelta)

	var aborted []slotcontroller.MigrationRequest
	worker := &fakeHashSlotMigrationWorker{
		active: []slotmigration.Migration{
			{HashSlot: 3, Source: 1, Target: 2, Phase: slotmigration.PhaseDelta},
		},
		transitionsByTick: [][]slotmigration.Transition{
			{{HashSlot: 3, Source: 1, Target: 2, TimedOut: true}},
		},
	}
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: worker,
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				abortMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					aborted = append(aborted, req)
					return nil
				},
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("observeHashSlotMigrations() error = %v", err)
	}
	if len(aborted) != 1 || aborted[0].HashSlot != 3 || aborted[0].Source != 1 || aborted[0].Target != 2 {
		t.Fatalf("abort migration calls = %#v", aborted)
	}
	if len(worker.aborted) != 1 || worker.aborted[0] != 3 {
		t.Fatalf("worker abort calls = %#v", worker.aborted)
	}
	if len(worker.active) != 0 {
		t.Fatalf("worker active migrations = %#v, want none", worker.active)
	}
}

func TestObserveHashSlotMigrationsDoesNotRestartTimedOutMigrationWhileAbortPending(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.AdvanceMigration(3, PhaseDelta)

	abortCalls := 0
	worker := &fakeHashSlotMigrationWorker{
		active: []slotmigration.Migration{
			{HashSlot: 3, Source: 1, Target: 2, Phase: slotmigration.PhaseDelta},
		},
		removeTimedOutOnTick: true,
		transitionsByTick: [][]slotmigration.Transition{
			{{HashSlot: 3, Source: 1, Target: 2, TimedOut: true}},
		},
	}
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: worker,
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				abortMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					abortCalls++
					if req.HashSlot != 3 || req.Source != 1 || req.Target != 2 {
						t.Fatalf("abort migration req = %#v", req)
					}
					if abortCalls == 1 {
						return errors.New("boom")
					}
					return nil
				},
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err == nil {
		t.Fatal("first observeHashSlotMigrations() error = nil, want abort failure")
	}
	if len(worker.active) != 0 {
		t.Fatalf("worker active migrations after failed abort = %#v, want none", worker.active)
	}

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("second observeHashSlotMigrations() error = %v", err)
	}
	if abortCalls != 2 {
		t.Fatalf("abort calls = %d, want 2", abortCalls)
	}
	if len(worker.active) != 0 {
		t.Fatalf("worker restarted timed out migration while abort pending: %#v", worker.active)
	}
}

func TestObserveHashSlotMigrationsAllowsReplacementMigrationAfterAbortPending(t *testing.T) {
	table := NewHashSlotTable(8, 4)
	table.StartMigration(1, 1, 2)
	table.AdvanceMigration(1, PhaseDelta)

	worker := &fakeHashSlotMigrationWorker{
		active: []slotmigration.Migration{
			{HashSlot: 1, Source: 1, Target: 2, Phase: slotmigration.PhaseDelta},
		},
		removeTimedOutOnTick: true,
		transitionsByTick: [][]slotmigration.Transition{
			{{HashSlot: 1, Source: 1, Target: 2, TimedOut: true}},
		},
	}
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: worker,
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				abortMigrationFn: func(_ context.Context, _ slotcontroller.MigrationRequest) error {
					return nil
				},
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("first observeHashSlotMigrations() error = %v", err)
	}

	replanned := NewHashSlotTable(8, 4)
	replanned.StartMigration(1, 1, 4)
	cluster.router.UpdateHashSlotTable(replanned)

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("second observeHashSlotMigrations() error = %v", err)
	}
	if len(worker.active) != 1 {
		t.Fatalf("worker active migrations = %#v, want replacement migration", worker.active)
	}
	if worker.active[0].HashSlot != 1 || worker.active[0].Source != 1 || worker.active[0].Target != 4 {
		t.Fatalf("worker replacement migration = %#v, want hash slot 1 source 1 target 4", worker.active[0])
	}
}

func TestObserveHashSlotMigrationsContinuesOtherMigrationsWhenPendingAbortRetryFails(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(1, 1, 2)
	table.AdvanceMigration(1, PhaseDelta)
	table.StartMigration(5, 2, 1)

	worker := &fakeHashSlotMigrationWorker{}
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: worker,
		pendingHashSlotAborts: map[uint16]pendingHashSlotAbort{
			1: {migration: HashSlotMigration{HashSlot: 1, Source: 1, Target: 2, Phase: PhaseDelta}},
		},
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				abortMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					if req.HashSlot == 1 {
						return errors.New("boom")
					}
					return nil
				},
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		switch slotID {
		case 1, 2:
			return 1, nil, true
		default:
			return 0, nil, false
		}
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err == nil {
		t.Fatal("observeHashSlotMigrations() error = nil, want pending abort retry failure")
	}
	if len(worker.active) != 1 {
		t.Fatalf("worker active migrations = %#v, want unaffected migration to keep progressing", worker.active)
	}
	if worker.active[0].HashSlot != 5 || worker.active[0].Source != 2 || worker.active[0].Target != 1 {
		t.Fatalf("worker active migration = %#v, want hash slot 5 source 2 target 1", worker.active[0])
	}
}

func TestObserveHashSlotMigrationsDoesNotRepeatSuccessfulPendingAbortForSameTableVersion(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(1, 1, 2)
	table.AdvanceMigration(1, PhaseDelta)

	abortCalls := 0
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: &fakeHashSlotMigrationWorker{},
		pendingHashSlotAborts: map[uint16]pendingHashSlotAbort{
			1: {migration: HashSlotMigration{HashSlot: 1, Source: 1, Target: 2}},
		},
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				abortMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					abortCalls++
					if req.HashSlot != 1 || req.Source != 1 || req.Target != 2 {
						t.Fatalf("abort migration req = %#v", req)
					}
					return nil
				},
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("first observeHashSlotMigrations() error = %v", err)
	}
	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("second observeHashSlotMigrations() error = %v", err)
	}
	if abortCalls != 1 {
		t.Fatalf("abort calls = %d, want 1 for unchanged table version", abortCalls)
	}
}

func TestObserveOnceContinuesHashSlotMigrationObservationWhenAssignmentSyncFails(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.AdvanceMigration(3, PhaseDelta)

	abortCalls := 0
	client := fakeControllerClient{
		assignmentsErr: errors.New("sync failed"),
		abortMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
			abortCalls++
			if req.HashSlot != 3 || req.Source != 1 || req.Target != 2 {
				t.Fatalf("abort migration req = %#v", req)
			}
			return nil
		},
	}
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		runtime:         &multiraft.Runtime{},
		router:          NewRouter(table, 1, nil),
		migrationWorker: &fakeHashSlotMigrationWorker{transitionsByTick: [][]slotmigration.Transition{{{HashSlot: 3, Source: 1, Target: 2, TimedOut: true}}}},
		controllerResources: controllerResources{
			controllerClient: client,
		},
	}
	cluster.agent = &slotAgent{
		cluster: cluster,
		client:  client,
		cache:   newAssignmentCache(),
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	cluster.observeOnce(context.Background())

	if abortCalls != 1 {
		t.Fatalf("abort calls = %d, want 1", abortCalls)
	}
}

func TestSlotAgentHeartbeatOnceSendsNodeHeartbeatOnly(t *testing.T) {
	cluster := newUnitObservationTestCluster(t)
	requireNoErr(t, cluster.ensureManagedSlotLocal(context.Background(), 1, []uint64{1}, false, true))

	var reports []slotcontroller.AgentReport
	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			reportFn: func(_ context.Context, report slotcontroller.AgentReport) error {
				reports = append(reports, report)
				return nil
			},
		},
	}

	if err := agent.HeartbeatOnce(context.Background()); err != nil {
		t.Fatalf("HeartbeatOnce() error = %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("report calls = %d, want 1", len(reports))
	}
	if reports[0].Runtime != nil {
		t.Fatalf("reports[0].Runtime = %#v, want nil", reports[0].Runtime)
	}
}

func TestObserveOnceUsesObservationDeltaBeforeSeparateListReads(t *testing.T) {
	cluster := newUnitObservationTestCluster(t)

	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}, {NodeID: 2}}, nil)
	controllerClient.setLeader(2)
	cluster.controllerClient = controllerClient

	accepted := cluster.wakeState.observeHint(2, observationHint{
		LeaderID:         2,
		LeaderGeneration: 1,
		Revisions: observationRevisions{
			Assignments: 1,
			Nodes:       1,
		},
		AffectedSlots: []uint32{1},
	})
	if !accepted {
		t.Fatal("observeHint() = false, want true")
	}

	deltaCalls := 0
	refreshCalls := 0
	listNodesCalls := 0
	listRuntimeViewsCalls := 0
	listTasksCalls := 0
	cluster.agent = &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			fetchObservationDeltaFn: func(_ context.Context, req observationDeltaRequest) (observationDeltaResponse, error) {
				deltaCalls++
				if got, want := req.RequestedSlots, []uint32{1}; !reflect.DeepEqual(got, want) {
					t.Fatalf("FetchObservationDelta() requested slots = %v, want %v", got, want)
				}
				return observationDeltaResponse{
					LeaderID:         2,
					LeaderGeneration: 1,
					Revisions: observationRevisions{
						Assignments: 1,
						Nodes:       1,
					},
					FullSync: true,
					Assignments: []controllermeta.SlotAssignment{
						testObservationAssignment(1, 1),
					},
					Nodes: []controllermeta.ClusterNode{
						testObservationNode(1, controllermeta.NodeStatusAlive),
					},
				}, nil
			},
			refreshAssignmentsFn: func(context.Context) ([]controllermeta.SlotAssignment, error) {
				refreshCalls++
				return nil, errors.New("RefreshAssignments should not be called when delta wake is pending")
			},
			listNodesFn: func(context.Context) ([]controllermeta.ClusterNode, error) {
				listNodesCalls++
				return nil, errors.New("ListNodes should not be called when delta cache is warm")
			},
			listRuntimeViewsFn: func(context.Context) ([]controllermeta.SlotRuntimeView, error) {
				listRuntimeViewsCalls++
				return nil, errors.New("ListRuntimeViews should not be called when delta cache is warm")
			},
			listTasksFn: func(context.Context) ([]controllermeta.ReconcileTask, error) {
				listTasksCalls++
				return nil, errors.New("ListTasks should not be called when delta cache is warm")
			},
		},
		cache: cluster.assignments,
	}

	cluster.observeOnce(context.Background())

	if got, want := deltaCalls, 1; got != want {
		t.Fatalf("FetchObservationDelta() calls = %d, want %d", got, want)
	}
	if refreshCalls != 0 || listNodesCalls != 0 || listRuntimeViewsCalls != 0 || listTasksCalls != 0 {
		t.Fatalf("unexpected hot reads: refresh=%d nodes=%d runtime=%d tasks=%d", refreshCalls, listNodesCalls, listRuntimeViewsCalls, listTasksCalls)
	}
	assignments := cluster.assignments.Snapshot()
	if got, want := len(assignments), 1; got != want {
		t.Fatalf("cached assignments len = %d, want %d", got, want)
	}
}

func TestWakeReconcileLoopSingleflightsConcurrentHints(t *testing.T) {
	cluster := newUnitObservationTestCluster(t)

	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}, {NodeID: 2}}, nil)
	controllerClient.setLeader(2)
	cluster.controllerClient = controllerClient

	if ok := cluster.wakeState.observeHint(2, observationHint{
		LeaderID:         2,
		LeaderGeneration: 1,
		Revisions:        observationRevisions{Assignments: 1},
		AffectedSlots:    []uint32{1},
	}); !ok {
		t.Fatal("observeHint(first) = false, want true")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	deltaCalls := 0
	cluster.agent = &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			fetchObservationDeltaFn: func(_ context.Context, req observationDeltaRequest) (observationDeltaResponse, error) {
				deltaCalls++
				close(started)
				<-release
				return observationDeltaResponse{
					LeaderID:         2,
					LeaderGeneration: 1,
					Revisions:        observationRevisions{Assignments: 1},
					FullSync:         true,
					Assignments:      []controllermeta.SlotAssignment{testObservationAssignment(1, 1)},
					Nodes:            []controllermeta.ClusterNode{testObservationNode(1, controllermeta.NodeStatusAlive)},
				}, nil
			},
		},
		cache: cluster.assignments,
	}

	done := make(chan struct{})
	go func() {
		cluster.wakeReconcileOnce(context.Background())
		close(done)
	}()

	<-started

	if ok := cluster.handleObservationHint(observationHint{
		LeaderID:         2,
		LeaderGeneration: 1,
		Revisions:        observationRevisions{Assignments: 2},
		AffectedSlots:    []uint32{1},
	}); !ok {
		t.Fatal("handleObservationHint(newer) = false, want true")
	}

	cluster.wakeReconcileOnce(context.Background())

	close(release)
	<-done

	if got, want := deltaCalls, 1; got != want {
		t.Fatalf("FetchObservationDelta() calls = %d, want %d", got, want)
	}
	snapshot := cluster.wakeState.snapshot()
	if !snapshot.Pending {
		t.Fatal("snapshot.Pending = false, want true for the coalesced newer hint")
	}
	if got, want := snapshot.Hint.Revisions.Assignments, uint64(2); got != want {
		t.Fatalf("snapshot.Hint.Revisions.Assignments = %d, want %d", got, want)
	}
}

func TestSlowSyncLoopRecoversDroppedHint(t *testing.T) {
	cluster := newUnitObservationTestCluster(t)

	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}, {NodeID: 2}}, nil)
	controllerClient.setLeader(2)
	cluster.controllerClient = controllerClient

	deltaCalls := 0
	cluster.agent = &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			fetchObservationDeltaFn: func(_ context.Context, req observationDeltaRequest) (observationDeltaResponse, error) {
				deltaCalls++
				if len(req.RequestedSlots) != 0 {
					t.Fatalf("FetchObservationDelta() requested slots = %v, want full slow-sync scope", req.RequestedSlots)
				}
				return observationDeltaResponse{
					LeaderID:         2,
					LeaderGeneration: 1,
					Revisions:        observationRevisions{Assignments: 1, Nodes: 1},
					FullSync:         true,
					Assignments:      []controllermeta.SlotAssignment{testObservationAssignment(1, 1)},
					Nodes:            []controllermeta.ClusterNode{testObservationNode(1, controllermeta.NodeStatusAlive)},
				}, nil
			},
			refreshAssignmentsFn: func(context.Context) ([]controllermeta.SlotAssignment, error) {
				return nil, errors.New("RefreshAssignments should not be used for slow sync")
			},
		},
		cache: cluster.assignments,
	}

	cluster.slowSyncOnce(context.Background())

	if got, want := deltaCalls, 1; got != want {
		t.Fatalf("FetchObservationDelta() calls = %d, want %d", got, want)
	}
	assignments := cluster.assignments.Snapshot()
	if got, want := len(assignments), 1; got != want {
		t.Fatalf("cached assignments len = %d, want %d", got, want)
	}
}

func TestSyncObservationDeltaInvokesOnNodeStatusChangeForNodeDiff(t *testing.T) {
	cluster := newUnitObservationTestCluster(t)

	var (
		gotNode uint64
		gotFrom controllermeta.NodeStatus
		gotTo   controllermeta.NodeStatus
		calls   int
	)
	cluster.obs = ObserverHooks{
		OnNodeStatusChange: func(nodeID uint64, from, to controllermeta.NodeStatus) {
			calls++
			gotNode = nodeID
			gotFrom = from
			gotTo = to
		},
	}

	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			fetchObservationDeltaFn: func(context.Context, observationDeltaRequest) (observationDeltaResponse, error) {
				return observationDeltaResponse{
					LeaderID:         2,
					LeaderGeneration: 1,
					Revisions:        observationRevisions{Nodes: 2},
					Nodes: []controllermeta.ClusterNode{{
						NodeID:         9,
						Addr:           "127.0.0.1:7009",
						Status:         controllermeta.NodeStatusDead,
						CapacityWeight: 1,
					}},
				}, nil
			},
		},
	}
	agent.observationState = observationAppliedState{
		LeaderID:         2,
		LeaderGeneration: 1,
		Nodes: map[uint64]controllermeta.ClusterNode{
			9: {
				NodeID:         9,
				Addr:           "127.0.0.1:7009",
				Status:         controllermeta.NodeStatusAlive,
				CapacityWeight: 1,
			},
		},
		Revisions: observationRevisions{Nodes: 1},
	}

	if err := agent.SyncObservationDelta(context.Background(), observationHint{}); err != nil {
		t.Fatalf("SyncObservationDelta() error = %v", err)
	}

	if calls != 1 {
		t.Fatalf("OnNodeStatusChange() calls = %d, want 1", calls)
	}
	if gotNode != 9 {
		t.Fatalf("OnNodeStatusChange() nodeID = %d, want 9", gotNode)
	}
	if gotFrom != controllermeta.NodeStatusAlive || gotTo != controllermeta.NodeStatusDead {
		t.Fatalf("OnNodeStatusChange() from=%v to=%v, want %v->%v", gotFrom, gotTo, controllermeta.NodeStatusAlive, controllermeta.NodeStatusDead)
	}
	if got, ok := agent.appliedObservationNodes(); !ok || len(got) != 1 || got[0].Status != controllermeta.NodeStatusDead {
		t.Fatalf("appliedObservationNodes() = %+v, ok=%v, want node 9 dead", got, ok)
	}
}

func TestWakeReconcileOnceLeavesMigrationProgressToDedicatedLoop(t *testing.T) {
	cluster := newUnitObservationTestCluster(t)

	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}, {NodeID: 2}}, nil)
	controllerClient.setLeader(2)
	cluster.controllerClient = controllerClient
	cluster.migrationWorker = &fakeHashSlotMigrationWorker{}
	cluster.router = NewRouter(NewHashSlotTable(8, 2), 1, cluster.runtime)

	if ok := cluster.wakeState.observeHint(2, observationHint{
		LeaderID:         2,
		LeaderGeneration: 1,
		Revisions:        observationRevisions{Assignments: 1},
		AffectedSlots:    []uint32{1},
	}); !ok {
		t.Fatal("observeHint() = false, want true")
	}

	worker := cluster.migrationWorker.(*fakeHashSlotMigrationWorker)
	cluster.agent = &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			fetchObservationDeltaFn: func(_ context.Context, _ observationDeltaRequest) (observationDeltaResponse, error) {
				return observationDeltaResponse{
					LeaderID:         2,
					LeaderGeneration: 1,
					Revisions:        observationRevisions{Assignments: 1},
					FullSync:         true,
					Assignments:      []controllermeta.SlotAssignment{testObservationAssignment(1, 1)},
					Nodes:            []controllermeta.ClusterNode{testObservationNode(1, controllermeta.NodeStatusAlive)},
				}, nil
			},
		},
		cache: cluster.assignments,
	}

	cluster.wakeReconcileOnce(context.Background())

	if worker.ticks != 0 {
		t.Fatalf("migration worker ticks = %d, want 0 for wake reconcile path", worker.ticks)
	}
}

func TestSlowSyncOnceLeavesMigrationProgressToDedicatedLoop(t *testing.T) {
	cluster := newUnitObservationTestCluster(t)

	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}, {NodeID: 2}}, nil)
	controllerClient.setLeader(2)
	cluster.controllerClient = controllerClient
	cluster.migrationWorker = &fakeHashSlotMigrationWorker{}
	cluster.router = NewRouter(NewHashSlotTable(8, 2), 1, cluster.runtime)

	worker := cluster.migrationWorker.(*fakeHashSlotMigrationWorker)
	cluster.agent = &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			fetchObservationDeltaFn: func(_ context.Context, _ observationDeltaRequest) (observationDeltaResponse, error) {
				return observationDeltaResponse{
					LeaderID:         2,
					LeaderGeneration: 1,
					Revisions:        observationRevisions{Assignments: 1},
					FullSync:         true,
					Assignments:      []controllermeta.SlotAssignment{testObservationAssignment(1, 1)},
					Nodes:            []controllermeta.ClusterNode{testObservationNode(1, controllermeta.NodeStatusAlive)},
				}, nil
			},
		},
		cache: cluster.assignments,
	}

	cluster.slowSyncOnce(context.Background())

	if worker.ticks != 0 {
		t.Fatalf("migration worker ticks = %d, want 0 for slow sync path", worker.ticks)
	}
}

func TestIdleClusterSkipsMigrationObserveWithoutActiveWork(t *testing.T) {
	worker := &fakeHashSlotMigrationWorker{}
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(NewHashSlotTable(8, 2), 1, nil),
		migrationWorker: worker,
	}

	cluster.migrationObserveOnce(context.Background())

	if worker.ticks != 0 {
		t.Fatalf("migration worker ticks = %d, want 0 when there is no active migration work", worker.ticks)
	}
	if len(worker.started) != 0 {
		t.Fatalf("migration worker started = %#v, want no work started", worker.started)
	}
}

func TestClusterRuntimeObservationLoopSkipsIdleFlush(t *testing.T) {
	now := time.Unix(1710015000, 0)
	sendCalls := 0
	cluster := &Cluster{
		observationResources: observationResources{
			runtimeReporter: newRuntimeObservationReporter(runtimeObservationReporterConfig{
				nodeID: 1,
				now:    func() time.Time { return now },
				snapshot: func() ([]controllermeta.SlotRuntimeView, error) {
					return []controllermeta.SlotRuntimeView{{
						SlotID:       1,
						CurrentPeers: []uint64{1},
						LeaderID:     1,
						HasQuorum:    true,
						LastReportAt: now,
					}}, nil
				},
				send: func(_ context.Context, _ runtimeObservationReport) error {
					sendCalls++
					return nil
				},
				fullSyncInterval: time.Minute,
			}),
		},
	}
	cluster.runtimeReporter.requestFullSync()

	cluster.runtimeObservationOnce(context.Background())
	now = now.Add(5 * time.Second)
	cluster.runtimeObservationOnce(context.Background())

	if sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", sendCalls)
	}
}

func TestClusterRuntimeObservationLoopSendsCloseTombstone(t *testing.T) {
	now := time.Unix(1710016000, 0)
	var reports []runtimeObservationReport
	cluster := &Cluster{
		observationResources: observationResources{
			runtimeReporter: newRuntimeObservationReporter(runtimeObservationReporterConfig{
				nodeID: 1,
				now:    func() time.Time { return now },
				snapshot: func() ([]controllermeta.SlotRuntimeView, error) {
					return []controllermeta.SlotRuntimeView{{
						SlotID:       1,
						CurrentPeers: []uint64{1},
						LeaderID:     1,
						HasQuorum:    true,
						LastReportAt: now,
					}}, nil
				},
				send: func(_ context.Context, report runtimeObservationReport) error {
					reports = append(reports, report)
					return nil
				},
				fullSyncInterval: time.Minute,
			}),
		},
	}
	cluster.runtimeReporter.requestFullSync()

	cluster.runtimeObservationOnce(context.Background())
	cluster.deleteRuntimePeers(2)
	now = now.Add(time.Second)
	cluster.runtimeObservationOnce(context.Background())

	if len(reports) != 2 {
		t.Fatalf("reports len = %d, want 2", len(reports))
	}
	if got, want := reports[1].ClosedSlots, []uint32{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ClosedSlots = %v, want %v", got, want)
	}
}

func TestOrderHashSlotMigrationsForStartInterleavesSources(t *testing.T) {
	ordered := orderHashSlotMigrationsForStart([]HashSlotMigration{
		{HashSlot: 1, Source: 1, Target: 10},
		{HashSlot: 2, Source: 1, Target: 10},
		{HashSlot: 3, Source: 2, Target: 11},
		{HashSlot: 4, Source: 2, Target: 11},
		{HashSlot: 5, Source: 3, Target: 12},
		{HashSlot: 6, Source: 3, Target: 12},
		{HashSlot: 7, Source: 4, Target: 13},
		{HashSlot: 8, Source: 4, Target: 13},
	})

	want := []uint16{1, 3, 5, 7, 2, 4, 6, 8}
	if len(ordered) != len(want) {
		t.Fatalf("ordered migrations len = %d, want %d", len(ordered), len(want))
	}
	for i, hashSlot := range want {
		if ordered[i].HashSlot != hashSlot {
			t.Fatalf("ordered[%d] = hash slot %d, want %d", i, ordered[i].HashSlot, hashSlot)
		}
	}
}

func TestObserveHashSlotMigrationsStartsFirstBatchAcrossDistinctSources(t *testing.T) {
	table := NewHashSlotTable(16, 8)
	table.StartMigration(1, 1, 5)
	table.StartMigration(2, 1, 6)
	table.StartMigration(3, 2, 5)
	table.StartMigration(4, 2, 6)
	table.StartMigration(5, 3, 7)
	table.StartMigration(6, 3, 8)
	table.StartMigration(7, 4, 7)
	table.StartMigration(8, 4, 8)

	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: slotmigration.NewWorker(100, time.Second),
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		switch slotID {
		case 1, 2, 3, 4:
			return 1, nil, true
		default:
			return 0, nil, false
		}
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("observeHashSlotMigrations() error = %v", err)
	}

	worker, ok := cluster.migrationWorker.(*slotmigration.Worker)
	if !ok {
		t.Fatalf("migrationWorker type = %T, want *slotmigration.Worker", cluster.migrationWorker)
	}
	active := worker.ActiveMigrations()
	if len(active) != 4 {
		t.Fatalf("active migrations len = %d, want 4", len(active))
	}

	gotSources := make(map[multiraft.SlotID]int, len(active))
	for _, migration := range active {
		gotSources[migration.Source]++
	}
	wantSources := []multiraft.SlotID{1, 2, 3, 4}
	if len(gotSources) != len(wantSources) {
		t.Fatalf("active migration sources = %#v, want one migration per source", gotSources)
	}
	for _, source := range wantSources {
		if gotSources[source] != 1 {
			t.Fatalf("source %d active migrations = %d, want 1", source, gotSources[source])
		}
	}
}

func TestObserveHashSlotMigrationsRealWorkerStaysInSnapshotUntilMarkedComplete(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)

	advanceCalls := 0
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: slotmigration.NewWorker(100, time.Second),
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				advanceMigrationFn: func(_ context.Context, _ slotcontroller.MigrationRequest) error {
					advanceCalls++
					return nil
				},
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("observeHashSlotMigrations() error = %v", err)
	}
	if advanceCalls != 0 {
		t.Fatalf("advanceCalls = %d, want 0", advanceCalls)
	}

	worker, ok := cluster.migrationWorker.(*slotmigration.Worker)
	if !ok {
		t.Fatalf("migrationWorker type = %T, want *slotmigration.Worker", cluster.migrationWorker)
	}
	active := worker.ActiveMigrations()
	if len(active) != 1 || active[0].Phase != slotmigration.PhaseSnapshot {
		t.Fatalf("active migrations = %#v", active)
	}
}

func TestObserveHashSlotMigrationsCompletesSnapshotViaRegisteredStateMachines(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)

	sourceSM := &testHashSlotSnapshotStateMachine{snapshotData: []byte("snapshot-3")}
	targetSM := &testHashSlotSnapshotStateMachine{}

	var advanced []slotcontroller.MigrationRequest
	cluster := &Cluster{
		cfg:             Config{NodeID: 1},
		router:          NewRouter(table, 1, nil),
		migrationWorker: slotmigration.NewWorker(100, time.Second),
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				advanceMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					advanced = append(advanced, req)
					return nil
				},
			},
		},
		hashSlotRuntimeResources: hashSlotRuntimeResources{
			runtimeStateMachines: map[multiraft.SlotID]hashSlotOwnershipUpdater{
				1: sourceSM,
				2: targetSM,
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		switch slotID {
		case 1, 2:
			return 1, nil, true
		default:
			return 0, nil, false
		}
	})
	defer restoreLeader()

	restoreStatus := cluster.setManagedSlotStatusTestHook(func(_ *Cluster, nodeID multiraft.NodeID, slotID multiraft.SlotID) (managedSlotStatus, error, bool) {
		if nodeID != 1 {
			return managedSlotStatus{}, nil, false
		}
		switch slotID {
		case 1:
			return managedSlotStatus{LeaderID: 1, AppliedIndex: 42}, nil, true
		case 2:
			return managedSlotStatus{LeaderID: 1}, nil, true
		default:
			return managedSlotStatus{}, nil, false
		}
	})
	defer restoreStatus()

	if err := cluster.observeHashSlotMigrations(context.Background()); err != nil {
		t.Fatalf("observeHashSlotMigrations() error = %v", err)
	}
	if len(sourceSM.exportedHashSlots) != 1 || sourceSM.exportedHashSlots[0] != 3 {
		t.Fatalf("source exports = %v, want [3]", sourceSM.exportedHashSlots)
	}
	if len(targetSM.importedSnapshots) != 1 {
		t.Fatalf("target imports = %d, want 1", len(targetSM.importedSnapshots))
	}

	gotSnapshot := targetSM.importedSnapshots[0]
	if len(gotSnapshot.HashSlots) != 1 || gotSnapshot.HashSlots[0] != 3 {
		t.Fatalf("imported snapshot hash slots = %v, want [3]", gotSnapshot.HashSlots)
	}
	if !bytes.Equal(gotSnapshot.Data, []byte("snapshot-3")) {
		t.Fatalf("imported snapshot data = %q, want %q", gotSnapshot.Data, []byte("snapshot-3"))
	}
	if len(advanced) != 1 {
		t.Fatalf("advance calls = %d, want 1", len(advanced))
	}
	if advanced[0].HashSlot != 3 || advanced[0].Source != 1 || advanced[0].Target != 2 || advanced[0].Phase != uint8(slotmigration.PhaseDelta) {
		t.Fatalf("advance request = %#v", advanced[0])
	}

	worker, ok := cluster.migrationWorker.(*slotmigration.Worker)
	if !ok {
		t.Fatalf("migrationWorker type = %T, want *slotmigration.Worker", cluster.migrationWorker)
	}
	active := worker.ActiveMigrations()
	if len(active) != 1 || active[0].Phase != slotmigration.PhaseDelta {
		t.Fatalf("active migrations after snapshot sync = %#v", active)
	}
}

func TestHandleManagedSlotRPCImportSnapshotRequiresLeader(t *testing.T) {
	targetSM := &testHashSlotSnapshotStateMachine{}
	cluster := &Cluster{
		cfg:    Config{NodeID: 1},
		router: NewRouter(NewHashSlotTable(8, 2), 1, nil),
		hashSlotRuntimeResources: hashSlotRuntimeResources{
			runtimeStateMachines: map[multiraft.SlotID]hashSlotOwnershipUpdater{
				2: targetSM,
			},
		},
	}

	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 2 {
			return 0, nil, false
		}
		return 2, nil, true
	})
	defer restoreLeader()

	body, err := encodeManagedSlotRequest(managedSlotRPCRequest{
		Kind:     managedSlotRPCImportSnapshot,
		SlotID:   2,
		HashSlot: 3,
		Snapshot: []byte("snapshot-3"),
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	respBody, err := cluster.handleManagedSlotRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("handleManagedSlotRPC() error = %v", err)
	}
	if _, err := decodeManagedSlotResponse(respBody); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("decodeManagedSlotResponse() err = %v, want %v", err, ErrNotLeader)
	}
	if len(targetSM.importedSnapshots) != 0 {
		t.Fatalf("target imports = %d, want 0", len(targetSM.importedSnapshots))
	}
}

func TestStartControllerClientInitializesDefaultMigrationWorker(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{
			NodeID:             1,
			ControllerMetaPath: "meta",
			ControllerRaftPath: "raft",
			Nodes:              []NodeConfig{{NodeID: 1, Addr: "127.0.0.1:1111"}},
		},
		agentResources: agentResources{
			assignments: newAssignmentCache(),
		},
	}

	cluster.startControllerClient()

	if cluster.migrationWorker == nil {
		t.Fatal("migrationWorker = nil, want default worker")
	}
	if _, ok := cluster.migrationWorker.(*slotmigration.Worker); !ok {
		t.Fatalf("migrationWorker type = %T, want *slotmigration.Worker", cluster.migrationWorker)
	}
}

func TestStartHashSlotMigrationUsesCurrentRouterAssignment(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.Reassign(3, 2)

	var started []slotcontroller.MigrationRequest
	cluster := &Cluster{
		router: NewRouter(table, 1, nil),
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				startMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					started = append(started, req)
					return nil
				},
			},
		},
	}

	if err := cluster.StartHashSlotMigration(context.Background(), 3, 1); err != nil {
		t.Fatalf("StartHashSlotMigration() error = %v", err)
	}
	if len(started) != 1 {
		t.Fatalf("start migration calls = %d, want 1", len(started))
	}
	if started[0].HashSlot != 3 || started[0].Source != 2 || started[0].Target != 1 {
		t.Fatalf("start migration req = %#v", started[0])
	}
}

func TestAbortHashSlotMigrationUsesCurrentMigrationState(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)

	var aborted []slotcontroller.MigrationRequest
	cluster := &Cluster{
		router: NewRouter(table, 1, nil),
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				abortMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					aborted = append(aborted, req)
					return nil
				},
			},
		},
	}

	if err := cluster.AbortHashSlotMigration(context.Background(), 3); err != nil {
		t.Fatalf("AbortHashSlotMigration() error = %v", err)
	}
	if len(aborted) != 1 {
		t.Fatalf("abort migration calls = %d, want 1", len(aborted))
	}
	if aborted[0].HashSlot != 3 || aborted[0].Source != 1 || aborted[0].Target != 2 {
		t.Fatalf("abort migration req = %#v", aborted[0])
	}
}

func TestAbortHashSlotMigrationPrefersPendingAbortRequest(t *testing.T) {
	table := NewHashSlotTable(8, 4)
	table.StartMigration(1, 1, 4)

	var aborted []slotcontroller.MigrationRequest
	cluster := &Cluster{
		router: NewRouter(table, 1, nil),
		pendingHashSlotAborts: map[uint16]pendingHashSlotAbort{
			1: {migration: HashSlotMigration{HashSlot: 1, Source: 1, Target: 2}},
		},
		controllerResources: controllerResources{
			controllerClient: fakeControllerClient{
				abortMigrationFn: func(_ context.Context, req slotcontroller.MigrationRequest) error {
					aborted = append(aborted, req)
					return nil
				},
			},
		},
	}

	if err := cluster.AbortHashSlotMigration(context.Background(), 1); err != nil {
		t.Fatalf("AbortHashSlotMigration() error = %v", err)
	}
	if len(aborted) != 1 {
		t.Fatalf("abort migration calls = %d, want 1", len(aborted))
	}
	if aborted[0].HashSlot != 1 || aborted[0].Source != 1 || aborted[0].Target != 2 {
		t.Fatalf("abort migration req = %#v, want pending request source 1 target 2", aborted[0])
	}
}

type fakeControllerClient struct {
	reportFn                    func(context.Context, slotcontroller.AgentReport) error
	reportErr                   error
	reportRuntimeObservationFn  func(context.Context, runtimeObservationReport) error
	reportRuntimeObservationErr error
	listNodesFn                 func(context.Context) ([]controllermeta.ClusterNode, error)
	nodes                       []controllermeta.ClusterNode
	listNodesErr                error
	refreshAssignmentsFn        func(context.Context) ([]controllermeta.SlotAssignment, error)
	assignments                 []controllermeta.SlotAssignment
	assignmentsErr              error
	listRuntimeViewsFn          func(context.Context) ([]controllermeta.SlotRuntimeView, error)
	runtimeViews                []controllermeta.SlotRuntimeView
	listRuntimeViewsErr         error
	listTasksFn                 func(context.Context) ([]controllermeta.ReconcileTask, error)
	listTasks                   []controllermeta.ReconcileTask
	listTasksErr                error
	fetchObservationDeltaFn     func(context.Context, observationDeltaRequest) (observationDeltaResponse, error)
	fetchObservationDelta       observationDeltaResponse
	fetchObservationDeltaErr    error
	getTaskFn                   func(context.Context, uint32) (controllermeta.ReconcileTask, error)
	tasks                       map[uint32]controllermeta.ReconcileTask
	getTaskErr                  error
	reportTaskResultErr         error
	reportTaskResultFn          func(context.Context, controllermeta.ReconcileTask, error) error
	startMigrationFn            func(context.Context, slotcontroller.MigrationRequest) error
	startMigrationErr           error
	advanceMigrationFn          func(context.Context, slotcontroller.MigrationRequest) error
	advanceMigrationErr         error
	finalizeMigrationFn         func(context.Context, slotcontroller.MigrationRequest) error
	finalizeMigrationErr        error
	abortMigrationFn            func(context.Context, slotcontroller.MigrationRequest) error
	abortMigrationErr           error
	addSlotFn                   func(context.Context, slotcontroller.AddSlotRequest) error
	addSlotErr                  error
	removeSlotFn                func(context.Context, slotcontroller.RemoveSlotRequest) error
	removeSlotErr               error
}

type fakeHashSlotMigrationWorker struct {
	started              []slotmigration.Migration
	aborted              []uint16
	switchCompleted      []uint16
	active               []slotmigration.Migration
	removeTimedOutOnTick bool
	transitionsByTick    [][]slotmigration.Transition
	ticks                int
}

func (f *fakeHashSlotMigrationWorker) StartMigration(hashSlot uint16, source, target multiraft.SlotID) error {
	desc := slotmigration.Migration{HashSlot: hashSlot, Source: source, Target: target}
	f.started = append(f.started, desc)
	for _, existing := range f.active {
		if existing.HashSlot == desc.HashSlot && existing.Source == desc.Source && existing.Target == desc.Target {
			return nil
		}
	}
	f.active = append(f.active, desc)
	return nil
}

func (f *fakeHashSlotMigrationWorker) AbortMigration(hashSlot uint16) error {
	f.aborted = append(f.aborted, hashSlot)
	filtered := f.active[:0]
	for _, migration := range f.active {
		if migration.HashSlot != hashSlot {
			filtered = append(filtered, migration)
		}
	}
	f.active = filtered
	return nil
}

func (f *fakeHashSlotMigrationWorker) MarkSnapshotComplete(hashSlot uint16, _ uint64, _ int64) error {
	for i := range f.active {
		if f.active[i].HashSlot == hashSlot {
			f.active[i].Phase = slotmigration.PhaseDelta
			return nil
		}
	}
	return nil
}

func (f *fakeHashSlotMigrationWorker) MarkSwitchComplete(hashSlot uint16) error {
	f.switchCompleted = append(f.switchCompleted, hashSlot)
	for i := range f.active {
		if f.active[i].HashSlot == hashSlot {
			f.active[i].Phase = slotmigration.PhaseDone
			return nil
		}
	}
	return nil
}

func (f *fakeHashSlotMigrationWorker) ActiveMigrations() []slotmigration.Migration {
	return append([]slotmigration.Migration(nil), f.active...)
}

func (f *fakeHashSlotMigrationWorker) Tick() []slotmigration.Transition {
	if f.ticks >= len(f.transitionsByTick) {
		f.ticks++
		return nil
	}
	transitions := append([]slotmigration.Transition(nil), f.transitionsByTick[f.ticks]...)
	if f.removeTimedOutOnTick {
		for _, transition := range transitions {
			if !transition.TimedOut {
				continue
			}
			filtered := f.active[:0]
			for _, migration := range f.active {
				if migration.HashSlot != transition.HashSlot {
					filtered = append(filtered, migration)
				}
			}
			f.active = filtered
		}
	}
	f.ticks++
	return transitions
}

func (f fakeControllerClient) Report(ctx context.Context, report slotcontroller.AgentReport) error {
	if f.reportFn != nil {
		return f.reportFn(ctx, report)
	}
	return f.reportErr
}

func (f fakeControllerClient) ReportRuntimeObservation(ctx context.Context, report runtimeObservationReport) error {
	if f.reportRuntimeObservationFn != nil {
		return f.reportRuntimeObservationFn(ctx, report)
	}
	return f.reportRuntimeObservationErr
}

func newUnitObservationTestCluster(t *testing.T) *Cluster {
	t.Helper()

	dir := t.TempDir()
	metaDB, err := metadb.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open metadb: %v", err)
	}
	raftDB, err := raftstorage.Open(filepath.Join(dir, "raft"))
	if err != nil {
		_ = metaDB.Close()
		t.Fatalf("open raftstorage: %v", err)
	}

	cluster, err := NewCluster(Config{
		NodeID:       1,
		ListenAddr:   "127.0.0.1:0",
		SlotCount:    1,
		SlotReplicaN: 1,
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:0"},
		},
		NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
			return raftDB.ForSlot(uint64(slotID)), nil
		},
		NewStateMachine: metafsm.NewStateMachineFactory(metaDB),
	})
	if err != nil {
		_ = raftDB.Close()
		_ = metaDB.Close()
		t.Fatalf("NewCluster() error = %v", err)
	}
	if err := cluster.Start(); err != nil {
		_ = raftDB.Close()
		_ = metaDB.Close()
		t.Fatalf("cluster.Start() error = %v", err)
	}

	t.Cleanup(func() {
		cluster.Stop()
		_ = raftDB.Close()
		_ = metaDB.Close()
	})
	return cluster
}

func (f fakeControllerClient) ListNodes(ctx context.Context) ([]controllermeta.ClusterNode, error) {
	if f.listNodesFn != nil {
		return f.listNodesFn(ctx)
	}
	return append([]controllermeta.ClusterNode(nil), f.nodes...), f.listNodesErr
}

func (f fakeControllerClient) RefreshAssignments(ctx context.Context) ([]controllermeta.SlotAssignment, error) {
	if f.refreshAssignmentsFn != nil {
		return f.refreshAssignmentsFn(ctx)
	}
	return append([]controllermeta.SlotAssignment(nil), f.assignments...), f.assignmentsErr
}

func (f fakeControllerClient) ListRuntimeViews(ctx context.Context) ([]controllermeta.SlotRuntimeView, error) {
	if f.listRuntimeViewsFn != nil {
		return f.listRuntimeViewsFn(ctx)
	}
	return append([]controllermeta.SlotRuntimeView(nil), f.runtimeViews...), f.listRuntimeViewsErr
}

func (f fakeControllerClient) ListTasks(ctx context.Context) ([]controllermeta.ReconcileTask, error) {
	if f.listTasksFn != nil {
		return f.listTasksFn(ctx)
	}
	if len(f.listTasks) == 0 && len(f.tasks) > 0 {
		tasks := make([]controllermeta.ReconcileTask, 0, len(f.tasks))
		for _, task := range f.tasks {
			tasks = append(tasks, task)
		}
		return tasks, f.listTasksErr
	}
	return append([]controllermeta.ReconcileTask(nil), f.listTasks...), f.listTasksErr
}

func (f fakeControllerClient) FetchObservationDelta(ctx context.Context, req observationDeltaRequest) (observationDeltaResponse, error) {
	if f.fetchObservationDeltaFn != nil {
		return f.fetchObservationDeltaFn(ctx, req)
	}
	return f.fetchObservationDelta, f.fetchObservationDeltaErr
}

func (f fakeControllerClient) Operator(_ context.Context, _ slotcontroller.OperatorRequest) error {
	return nil
}

func (f fakeControllerClient) GetTask(ctx context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
	if f.getTaskFn != nil {
		return f.getTaskFn(ctx, slotID)
	}
	if f.getTaskErr != nil {
		return controllermeta.ReconcileTask{}, f.getTaskErr
	}
	if task, ok := f.tasks[slotID]; ok {
		return task, nil
	}
	return controllermeta.ReconcileTask{}, controllermeta.ErrNotFound
}

func (f fakeControllerClient) ForceReconcile(_ context.Context, _ uint32) error {
	return nil
}

func (f fakeControllerClient) ReportTaskResult(ctx context.Context, task controllermeta.ReconcileTask, taskErr error) error {
	if f.reportTaskResultFn != nil {
		return f.reportTaskResultFn(ctx, task, taskErr)
	}
	return f.reportTaskResultErr
}

func (f fakeControllerClient) StartMigration(ctx context.Context, req slotcontroller.MigrationRequest) error {
	if f.startMigrationFn != nil {
		return f.startMigrationFn(ctx, req)
	}
	return f.startMigrationErr
}

func (f fakeControllerClient) AdvanceMigration(ctx context.Context, req slotcontroller.MigrationRequest) error {
	if f.advanceMigrationFn != nil {
		return f.advanceMigrationFn(ctx, req)
	}
	return f.advanceMigrationErr
}

func (f fakeControllerClient) FinalizeMigration(ctx context.Context, req slotcontroller.MigrationRequest) error {
	if f.finalizeMigrationFn != nil {
		return f.finalizeMigrationFn(ctx, req)
	}
	return f.finalizeMigrationErr
}

func (f fakeControllerClient) AbortMigration(ctx context.Context, req slotcontroller.MigrationRequest) error {
	if f.abortMigrationFn != nil {
		return f.abortMigrationFn(ctx, req)
	}
	return f.abortMigrationErr
}

func (f fakeControllerClient) AddSlot(ctx context.Context, req slotcontroller.AddSlotRequest) error {
	if f.addSlotFn != nil {
		return f.addSlotFn(ctx, req)
	}
	return f.addSlotErr
}

func (f fakeControllerClient) RemoveSlot(ctx context.Context, req slotcontroller.RemoveSlotRequest) error {
	if f.removeSlotFn != nil {
		return f.removeSlotFn(ctx, req)
	}
	return f.removeSlotErr
}

func TestGroupAgentShouldExecuteTaskUsesLowestAliveAssignedPeerForBootstrap(t *testing.T) {
	agent := &slotAgent{
		cluster: &Cluster{cfg: Config{NodeID: 3}},
	}
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{2, 3, 4},
	}
	task := controllermeta.ReconcileTask{
		SlotID: 1,
		Kind:   controllermeta.TaskKindBootstrap,
		Status: controllermeta.TaskStatusPending,
	}
	nodes := map[uint64]controllermeta.ClusterNode{
		2: {NodeID: 2, Status: controllermeta.NodeStatusDead},
		3: {NodeID: 3, Status: controllermeta.NodeStatusAlive},
		4: {NodeID: 4, Status: controllermeta.NodeStatusAlive},
	}

	if !newReconciler(agent).shouldExecuteTask(assignment, task, nodes) {
		t.Fatal("shouldExecuteTask() = false, want true when local node is lowest alive peer")
	}
}

func TestGroupAgentShouldExecuteRepairTaskOnCurrentGroupLeader(t *testing.T) {
	cluster := &Cluster{cfg: Config{NodeID: 4}}
	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 4, nil, true
	})
	defer restoreLeader()

	agent := &slotAgent{
		cluster: cluster,
	}
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 2, 3},
	}
	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		SourceNode: 4,
		TargetNode: 3,
		Status:     controllermeta.TaskStatusPending,
	}

	if !newReconciler(agent).shouldExecuteTask(assignment, task, nil) {
		t.Fatal("shouldExecuteTask() = false, want true when local node is current slot leader")
	}
}

func TestGroupAgentShouldExecuteRepairTaskOnSourceNodeWhenSourceIsAlive(t *testing.T) {
	cluster := &Cluster{cfg: Config{NodeID: 2}}
	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 3, nil, true
	})
	defer restoreLeader()

	agent := &slotAgent{
		cluster: cluster,
	}
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 3, 4},
	}
	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		SourceNode: 2,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusPending,
	}
	nodes := map[uint64]controllermeta.ClusterNode{
		2: {NodeID: 2, Status: controllermeta.NodeStatusDraining},
	}

	if !newReconciler(agent).shouldExecuteTask(assignment, task, nodes) {
		t.Fatal("shouldExecuteTask() = false, want true when local node is the alive source node")
	}
}

func TestGroupAgentShouldExecuteRepairTaskOnLocalSourceWithoutNodeSnapshot(t *testing.T) {
	cluster := &Cluster{cfg: Config{NodeID: 2}}
	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 3, nil, true
	})
	defer restoreLeader()

	agent := &slotAgent{
		cluster: cluster,
	}
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 3, 4},
	}
	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		SourceNode: 2,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusRetrying,
		NextRunAt:  time.Now(),
	}

	if !newReconciler(agent).shouldExecuteTask(assignment, task, nil) {
		t.Fatal("shouldExecuteTask() = false, want true when local node is the task source")
	}
}

func TestGroupAgentShouldExecuteRepairTaskOnCurrentGroupLeaderWhenSourceUnavailable(t *testing.T) {
	cluster := &Cluster{cfg: Config{NodeID: 3}}
	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 3, nil, true
	})
	defer restoreLeader()

	agent := &slotAgent{
		cluster: cluster,
	}
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1, 3, 4},
	}
	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		SourceNode: 2,
		TargetNode: 4,
		Status:     controllermeta.TaskStatusPending,
	}
	nodes := map[uint64]controllermeta.ClusterNode{
		2: {NodeID: 2, Status: controllermeta.NodeStatusDead},
	}

	if !newReconciler(agent).shouldExecuteTask(assignment, task, nodes) {
		t.Fatal("shouldExecuteTask() = false, want true when source node is unavailable and local node is current slot leader")
	}
}

type standaloneAgentTestCluster struct {
	cluster *Cluster
	raftDB  *raftstorage.DB
	metaDB  *metadb.DB
}

func (c *standaloneAgentTestCluster) Close() {
	if c == nil {
		return
	}
	if c.cluster != nil {
		c.cluster.Stop()
		c.cluster = nil
	}
	if c.raftDB != nil {
		_ = c.raftDB.Close()
		c.raftDB = nil
	}
	if c.metaDB != nil {
		_ = c.metaDB.Close()
		c.metaDB = nil
	}
}

func TestWaitForManagedSlotCatchUpRequiresTargetToReachLeaderCommit(t *testing.T) {
	cluster := &Cluster{}
	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	restore := cluster.setManagedSlotStatusTestHook(func(_ *Cluster, nodeID multiraft.NodeID, slotID multiraft.SlotID) (managedSlotStatus, error, bool) {
		if slotID != 1 {
			return managedSlotStatus{}, nil, false
		}
		switch nodeID {
		case 1:
			return managedSlotStatus{LeaderID: 1, CommitIndex: 9, AppliedIndex: 9}, nil, true
		case 4:
			return managedSlotStatus{LeaderID: 1, CommitIndex: 9, AppliedIndex: 3}, nil, true
		default:
			return managedSlotStatus{}, ErrSlotNotFound, true
		}
	})
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := cluster.waitForManagedSlotCatchUp(ctx, 1, 4)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitForManagedSlotCatchUp() error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestWaitForManagedSlotCatchUpAllowsSlowLearnerCatchUp(t *testing.T) {
	cluster := &Cluster{}
	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	start := time.Now()
	restore := cluster.setManagedSlotStatusTestHook(func(_ *Cluster, nodeID multiraft.NodeID, slotID multiraft.SlotID) (managedSlotStatus, error, bool) {
		if slotID != 1 {
			return managedSlotStatus{}, nil, false
		}
		switch nodeID {
		case 1:
			return managedSlotStatus{LeaderID: 1, CommitIndex: 9, AppliedIndex: 9}, nil, true
		case 4:
			applied := uint64(3)
			if time.Since(start) >= 2300*time.Millisecond {
				applied = 9
			}
			return managedSlotStatus{LeaderID: 1, CommitIndex: 9, AppliedIndex: applied}, nil, true
		default:
			return managedSlotStatus{}, ErrSlotNotFound, true
		}
	})
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	defer cancel()

	if err := cluster.waitForManagedSlotCatchUp(ctx, 1, 4); err != nil {
		t.Fatalf("waitForManagedSlotCatchUp() error = %v, want nil", err)
	}
}

func TestWaitForManagedSlotCatchUpUsesConfiguredPollInterval(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{
			Timeouts: Timeouts{
				ManagedSlotCatchUp: 60 * time.Millisecond,
			},
		},
	}
	restoreLeader := cluster.setManagedSlotLeaderTestHook(func(_ *Cluster, slotID multiraft.SlotID) (multiraft.NodeID, error, bool) {
		if slotID != 1 {
			return 0, nil, false
		}
		return 1, nil, true
	})
	defer restoreLeader()

	start := time.Now()
	restore := cluster.setManagedSlotStatusTestHook(func(_ *Cluster, nodeID multiraft.NodeID, slotID multiraft.SlotID) (managedSlotStatus, error, bool) {
		if slotID != 1 {
			return managedSlotStatus{}, nil, false
		}
		switch nodeID {
		case 1:
			return managedSlotStatus{LeaderID: 1, CommitIndex: 9, AppliedIndex: 9}, nil, true
		case 4:
			applied := uint64(3)
			if time.Since(start) >= 15*time.Millisecond {
				applied = 9
			}
			return managedSlotStatus{LeaderID: 1, CommitIndex: 9, AppliedIndex: applied}, nil, true
		default:
			return managedSlotStatus{}, ErrSlotNotFound, true
		}
	})
	defer restore()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	if err := cluster.waitForManagedSlotCatchUp(ctx, 1, 4); err != nil {
		t.Fatalf("waitForManagedSlotCatchUp() error = %v, want nil", err)
	}
}

func TestManagedSlotExecutionTestHookIsClusterScoped(t *testing.T) {
	clusterA := &Cluster{}
	clusterB := &Cluster{}
	sentinel := errors.New("cluster scoped execution hook")

	restore := clusterA.SetManagedSlotExecutionTestHook(func(slotID uint32, task controllermeta.ReconcileTask) error {
		if slotID != 1 {
			t.Fatalf("hook slotID = %d, want 1", slotID)
		}
		if task.SlotID != 1 {
			t.Fatalf("hook task slotID = %d, want 1", task.SlotID)
		}
		return sentinel
	})
	defer restore()

	state := assignmentTaskState{
		assignment: controllermeta.SlotAssignment{SlotID: 1},
		task:       controllermeta.ReconcileTask{SlotID: 1},
	}

	if err := clusterA.executeReconcileTask(context.Background(), state); !errors.Is(err, sentinel) {
		t.Fatalf("clusterA.executeReconcileTask() error = %v, want %v", err, sentinel)
	}
	if err := clusterB.executeReconcileTask(context.Background(), state); err != nil {
		t.Fatalf("clusterB.executeReconcileTask() error = %v, want nil", err)
	}
}

func TestTimeoutDerivedIntervalsScaleWithOverrides(t *testing.T) {
	cluster := &Cluster{
		cfg: Config{
			Timeouts: Timeouts{
				ControllerObservation:     40 * time.Millisecond,
				ForwardRetryBudget:        60 * time.Millisecond,
				ManagedSlotLeaderWait:     250 * time.Millisecond,
				ManagedSlotCatchUp:        250 * time.Millisecond,
				ManagedSlotLeaderMove:     250 * time.Millisecond,
				ConfigChangeRetryBudget:   60 * time.Millisecond,
				LeaderTransferRetryBudget: 60 * time.Millisecond,
			},
		},
		controllerResources: controllerResources{
			controllerLeaderWaitTimeout: 25 * time.Millisecond,
		},
	}

	if got := cluster.controllerRetryInterval(); got >= 100*time.Millisecond {
		t.Fatalf("controllerRetryInterval() = %v, want < %v", got, 100*time.Millisecond)
	}
	if got := cluster.managedSlotsReadyPollInterval(); got >= 100*time.Millisecond {
		t.Fatalf("managedSlotsReadyPollInterval() = %v, want < %v", got, 100*time.Millisecond)
	}
	if got := cluster.forwardRetryInterval(); got >= 50*time.Millisecond {
		t.Fatalf("forwardRetryInterval() = %v, want < %v", got, 50*time.Millisecond)
	}
	if got := cluster.configChangeRetryInterval(); got >= 50*time.Millisecond {
		t.Fatalf("configChangeRetryInterval() = %v, want < %v", got, 50*time.Millisecond)
	}
	if got := cluster.leaderTransferRetryInterval(); got >= 50*time.Millisecond {
		t.Fatalf("leaderTransferRetryInterval() = %v, want < %v", got, 50*time.Millisecond)
	}
	if got := cluster.managedSlotLeaderPollInterval(); got >= 100*time.Millisecond {
		t.Fatalf("managedSlotLeaderPollInterval() = %v, want < %v", got, 100*time.Millisecond)
	}
	if got := cluster.managedSlotCatchUpPollInterval(); got >= 100*time.Millisecond {
		t.Fatalf("managedSlotCatchUpPollInterval() = %v, want < %v", got, 100*time.Millisecond)
	}
	if got := cluster.managedSlotLeaderMovePollInterval(); got >= 100*time.Millisecond {
		t.Fatalf("managedSlotLeaderMovePollInterval() = %v, want < %v", got, 100*time.Millisecond)
	}
}

func newStartedTestServer(t *testing.T) *transport.Server {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = ln.Close()

	srv := transport.NewServer()
	if err := srv.Start(ln.Addr().String()); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

func newTestClusterWithController(store *controllermeta.Store, timeout time.Duration, client controllerAPI) *Cluster {
	return &Cluster{
		controllerResources: controllerResources{
			controllerMeta:              store,
			controllerLeaderWaitTimeout: timeout,
			controllerClient:            client,
		},
	}
}

func setControllerMetadataSnapshotStateForTest(host *controllerHost, snapshot controllerMetadataSnapshot) {
	if host == nil {
		return
	}
	host.metadataSnapshotState.mu.Lock()
	defer host.metadataSnapshotState.mu.Unlock()
	host.metadataSnapshotState.snapshot = snapshot
	host.metadataSnapshotState.reloadPending = false
	host.metadataSnapshotState.reloadScheduled = false
}
