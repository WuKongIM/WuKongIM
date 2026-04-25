//go:build integration
// +build integration

package cluster

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func TestGroupAgentApplyAssignmentsReturnsTimeoutWhenControllerTaskReadTimesOut(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)

	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	harness.cluster.controllerMeta = store

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	if err := store.UpsertAssignment(context.Background(), assignment); err != nil {
		t.Fatalf("UpsertAssignment() error = %v", err)
	}
	if err := store.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	harness.cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client: fakeControllerClient{
			listNodesErr:        context.DeadlineExceeded,
			listRuntimeViewsErr: context.DeadlineExceeded,
			getTaskErr:          context.DeadlineExceeded,
		},
		cache: harness.cluster.assignments,
	}

	err = harness.cluster.agent.ApplyAssignments(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ApplyAssignments() error = %v, want %v", err, context.DeadlineExceeded)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, statusErr := harness.cluster.runtime.Status(1)
		if errors.Is(statusErr, multiraft.ErrSlotNotFound) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("slot 1 should remain unopened when task read times out")
}

func TestObserveOnceAppliesCachedAssignmentsWhenSyncAssignmentsTimesOut(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}

	reportCalls := 0
	agent := &slotAgent{
		cluster: harness.cluster,
		client: fakeControllerClient{
			assignmentsErr: context.DeadlineExceeded,
			tasks:          map[uint32]controllermeta.ReconcileTask{1: task},
			reportTaskResultFn: func(_ context.Context, task controllermeta.ReconcileTask, taskErr error) error {
				reportCalls++
				if task.SlotID != 1 {
					t.Fatalf("ReportTaskResult() slotID = %d, want 1", task.SlotID)
				}
				if taskErr != nil {
					t.Fatalf("ReportTaskResult() err = %v, want nil", taskErr)
				}
				return nil
			},
		},
		cache: harness.cluster.assignments,
	}
	harness.cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})
	agent.storePendingTaskReport(1, task, nil)
	harness.cluster.agent = agent

	harness.cluster.observeOnce(context.Background())

	if reportCalls != 1 {
		t.Fatalf("ReportTaskResult() calls = %d, want 1", reportCalls)
	}
	if _, ok := agent.pendingTaskReport(1); ok {
		t.Fatal("pending task report was not cleared")
	}
}

func TestGroupAgentBootstrapsBrandNewGroupWhenBootstrapTaskExists(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:    1,
				Kind:      controllermeta.TaskKindBootstrap,
				Step:      controllermeta.TaskStepAddLearner,
				Status:    controllermeta.TaskStatusPending,
				NextRunAt: time.Now(),
			},
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := harness.cluster.runtime.Status(1); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("slot 1 was not bootstrapped")
}

func TestGroupAgentReopensPersistedGroupBeforeTaskFetch(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	requireNoError := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(harness.cluster.ensureManagedSlotLocal(context.Background(), 1, []uint64{1}, false, true))
	requireNoError(harness.cluster.waitForManagedSlotLeader(context.Background(), 1))
	requireNoError(harness.cluster.runtime.CloseSlot(context.Background(), 1))

	taskErr := errors.New("task fetch failed")
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		getTaskErr:  taskErr,
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	err := harness.cluster.agent.ApplyAssignments(context.Background())
	if !errors.Is(err, taskErr) {
		t.Fatalf("ApplyAssignments() error = %v, want %v", err, taskErr)
	}
	if _, err := harness.cluster.runtime.Status(1); err != nil {
		t.Fatalf("runtime.Status() error = %v", err)
	}
}

func TestGroupAgentKeepsSourceGroupOpenWhileRepairTaskPending(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	requireNoError := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(harness.cluster.ensureManagedSlotLocal(context.Background(), 1, []uint64{1}, false, true))
	requireNoError(harness.cluster.waitForManagedSlotLeader(context.Background(), 1))

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{2, 3},
		ConfigEpoch:  2,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:     1,
				Kind:       controllermeta.TaskKindRepair,
				SourceNode: 1,
				TargetNode: 2,
				Status:     controllermeta.TaskStatusPending,
				NextRunAt:  time.Now(),
			},
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if _, err := harness.cluster.runtime.Status(1); err != nil {
		t.Fatalf("runtime.Status() error = %v, want source slot to remain open while repair is pending", err)
	}
	peers, ok := harness.cluster.getRuntimePeers(1)
	if !ok {
		t.Fatal("getRuntimePeers() = not found, want source slot peers to remain available")
	}
	if len(peers) != 1 || peers[0] != 1 {
		t.Fatalf("getRuntimePeers() = %v, want [1] while repair keeps source slot open", peers)
	}
}

func TestGroupAgentReopensSourceGroupUsingCurrentPeersWhileRepairTaskPending(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	requireNoError := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(harness.cluster.ensureManagedSlotLocal(context.Background(), 1, []uint64{1}, false, true))
	requireNoError(harness.cluster.waitForManagedSlotLeader(context.Background(), 1))
	requireNoError(harness.cluster.runtime.CloseSlot(context.Background(), 1))
	harness.cluster.deleteRuntimePeers(1)

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{2, 3},
		ConfigEpoch:  2,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		runtimeViews: []controllermeta.SlotRuntimeView{
			{
				SlotID:       1,
				CurrentPeers: []uint64{1},
				LeaderID:     1,
				HasQuorum:    true,
			},
		},
		tasks: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:     1,
				Kind:       controllermeta.TaskKindRepair,
				SourceNode: 1,
				TargetNode: 2,
				Status:     controllermeta.TaskStatusPending,
				NextRunAt:  time.Now(),
			},
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	peers, ok := harness.cluster.getRuntimePeers(1)
	if !ok {
		t.Fatal("getRuntimePeers() = not found, want source slot peers to be restored from current runtime view")
	}
	if len(peers) != 1 || peers[0] != 1 {
		t.Fatalf("getRuntimePeers() = %v, want [1] when current runtime view still includes source node", peers)
	}
}

func TestGroupAgentDoesNotReopenSourceGroupAfterCurrentPeersDropSourceNode(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	requireNoError := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(harness.cluster.ensureManagedSlotLocal(context.Background(), 1, []uint64{1}, false, true))
	requireNoError(harness.cluster.waitForManagedSlotLeader(context.Background(), 1))
	requireNoError(harness.cluster.runtime.CloseSlot(context.Background(), 1))
	harness.cluster.deleteRuntimePeers(1)

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{2, 3},
		ConfigEpoch:  2,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		runtimeViews: []controllermeta.SlotRuntimeView{
			{
				SlotID:       1,
				CurrentPeers: []uint64{2, 3},
				LeaderID:     3,
				HasQuorum:    true,
			},
		},
		tasks: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:     1,
				Kind:       controllermeta.TaskKindRepair,
				SourceNode: 1,
				TargetNode: 2,
				Status:     controllermeta.TaskStatusPending,
				NextRunAt:  time.Now(),
			},
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if peers, ok := harness.cluster.getRuntimePeers(1); ok {
		t.Fatalf("getRuntimePeers() = %v, want source slot to stay closed after current peers drop source node", peers)
	}
	if _, err := harness.cluster.runtime.Status(1); !errors.Is(err, multiraft.ErrSlotNotFound) {
		t.Fatalf("runtime.Status() error = %v, want %v after source slot is no longer part of current peers", err, multiraft.ErrSlotNotFound)
	}
}

func TestGroupAgentDoesNotTrustFallbackRuntimeViewForSourceSlotProtection(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	requireNoError := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(harness.cluster.ensureManagedSlotLocal(context.Background(), 1, []uint64{1}, false, true))
	requireNoError(harness.cluster.waitForManagedSlotLeader(context.Background(), 1))
	requireNoError(harness.cluster.runtime.CloseSlot(context.Background(), 1))
	harness.cluster.deleteRuntimePeers(1)

	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	harness.cluster.controllerMeta = store
	requireNoError(store.UpsertRuntimeView(context.Background(), controllermeta.SlotRuntimeView{
		SlotID:       1,
		CurrentPeers: []uint64{1},
		LeaderID:     1,
		HasQuorum:    true,
		LastReportAt: time.Now(),
	}))

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{2, 3},
		ConfigEpoch:  2,
	}
	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		SourceNode: 1,
		TargetNode: 2,
		Status:     controllermeta.TaskStatusPending,
		NextRunAt:  time.Now(),
	}
	harness.cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client: fakeControllerClient{
			assignments:         []controllermeta.SlotAssignment{assignment},
			listRuntimeViewsErr: context.DeadlineExceeded,
			tasks:               map[uint32]controllermeta.ReconcileTask{1: task},
		},
		cache: harness.cluster.assignments,
	}

	err = harness.cluster.agent.ApplyAssignments(context.Background())
	if err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if peers, ok := harness.cluster.getRuntimePeers(1); ok {
		t.Fatalf("getRuntimePeers() = %v, want source slot to stay closed when runtime views only come from fallback controller meta", peers)
	}
	if _, err := harness.cluster.runtime.Status(1); !errors.Is(err, multiraft.ErrSlotNotFound) {
		t.Fatalf("runtime.Status() error = %v, want %v when runtime views only come from fallback controller meta", err, multiraft.ErrSlotNotFound)
	}
}

func TestGroupAgentDoesNotReopenSourceGroupWhenPersistedVotersDropLocalNode(t *testing.T) {
	storage := &observerTestStorage{
		state: multiraft.BootstrapState{
			HardState: raftpb.HardState{
				Term: 1,
			},
			ConfState: raftpb.ConfState{
				Voters: []uint64{2, 3},
			},
		},
	}
	cluster, err := NewCluster(Config{
		NodeID:       1,
		ListenAddr:   "127.0.0.1:0",
		SlotCount:    1,
		SlotReplicaN: 1,
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:0"},
		},
		NewStorage: func(multiraft.SlotID) (multiraft.Storage, error) {
			return storage, nil
		},
		NewStateMachine: func(multiraft.SlotID) (multiraft.StateMachine, error) {
			return observerTestStateMachine{}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}
	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    observerTestTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  3,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("multiraft.New() error = %v", err)
	}
	cluster.runtime = rt
	cluster.router = NewRouter(
		NewHashSlotTable(cluster.cfg.effectiveHashSlotCount(), int(cluster.cfg.effectiveInitialSlotCount())),
		cluster.cfg.NodeID,
		rt,
	)
	t.Cleanup(func() {
		cluster.Stop()
	})

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{2, 3},
		ConfigEpoch:  2,
	}
	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		SourceNode: 1,
		TargetNode: 2,
		Status:     controllermeta.TaskStatusPending,
		NextRunAt:  time.Now(),
	}
	cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})
	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			assignments: []controllermeta.SlotAssignment{assignment},
			runtimeViews: []controllermeta.SlotRuntimeView{
				{
					SlotID:       1,
					CurrentPeers: []uint64{1},
					LeaderID:     1,
					HasQuorum:    true,
				},
			},
			tasks: map[uint32]controllermeta.ReconcileTask{1: task},
		},
		cache: cluster.assignments,
	}

	if err := agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if _, err := cluster.runtime.Status(1); !errors.Is(err, multiraft.ErrSlotNotFound) {
		t.Fatalf("runtime.Status() error = %v, want %v after persisted voters drop the local node", err, multiraft.ErrSlotNotFound)
	}
}

func TestGroupAgentReopensSourceGroupUsingLiveCurrentPeersWhenPersistedVotersAreEmpty(t *testing.T) {
	storage := &observerTestStorage{
		state: multiraft.BootstrapState{
			HardState: raftpb.HardState{
				Term: 1,
			},
		},
	}
	cluster, err := NewCluster(Config{
		NodeID:       1,
		ListenAddr:   "127.0.0.1:0",
		SlotCount:    1,
		SlotReplicaN: 1,
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:0"},
		},
		NewStorage: func(multiraft.SlotID) (multiraft.Storage, error) {
			return storage, nil
		},
		NewStateMachine: func(multiraft.SlotID) (multiraft.StateMachine, error) {
			return observerTestStateMachine{}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}
	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    observerTestTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  3,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("multiraft.New() error = %v", err)
	}
	cluster.runtime = rt
	cluster.router = NewRouter(
		NewHashSlotTable(cluster.cfg.effectiveHashSlotCount(), int(cluster.cfg.effectiveInitialSlotCount())),
		cluster.cfg.NodeID,
		rt,
	)
	t.Cleanup(func() {
		cluster.Stop()
	})

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{2, 3},
		ConfigEpoch:  2,
	}
	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		SourceNode: 1,
		TargetNode: 2,
		Status:     controllermeta.TaskStatusPending,
		NextRunAt:  time.Now(),
	}
	cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})
	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			assignments: []controllermeta.SlotAssignment{assignment},
			runtimeViews: []controllermeta.SlotRuntimeView{
				{
					SlotID:       1,
					CurrentPeers: []uint64{1},
					LeaderID:     1,
					HasQuorum:    true,
				},
			},
			tasks: map[uint32]controllermeta.ReconcileTask{1: task},
		},
		cache: cluster.assignments,
	}

	if err := agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	peers, ok := cluster.getRuntimePeers(1)
	if !ok {
		t.Fatal("getRuntimePeers() = not found, want source slot peers restored from live current peers")
	}
	if len(peers) != 1 || peers[0] != 1 {
		t.Fatalf("getRuntimePeers() = %v, want [1] when persisted voters are empty but live current peers still include the source node", peers)
	}
}

func TestGroupAgentReopensSourceGroupUsingLiveCurrentPeersWhenStorageIsEmpty(t *testing.T) {
	storage := &observerTestStorage{}
	cluster, err := NewCluster(Config{
		NodeID:       1,
		ListenAddr:   "127.0.0.1:0",
		SlotCount:    1,
		SlotReplicaN: 1,
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:0"},
		},
		NewStorage: func(multiraft.SlotID) (multiraft.Storage, error) {
			return storage, nil
		},
		NewStateMachine: func(multiraft.SlotID) (multiraft.StateMachine, error) {
			return observerTestStateMachine{}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}
	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    observerTestTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  3,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("multiraft.New() error = %v", err)
	}
	cluster.runtime = rt
	cluster.router = NewRouter(
		NewHashSlotTable(cluster.cfg.effectiveHashSlotCount(), int(cluster.cfg.effectiveInitialSlotCount())),
		cluster.cfg.NodeID,
		rt,
	)
	t.Cleanup(func() {
		cluster.Stop()
	})

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{2, 3},
		ConfigEpoch:  2,
	}
	task := controllermeta.ReconcileTask{
		SlotID:     1,
		Kind:       controllermeta.TaskKindRepair,
		SourceNode: 1,
		TargetNode: 2,
		Status:     controllermeta.TaskStatusPending,
		NextRunAt:  time.Now(),
	}
	cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})
	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			assignments: []controllermeta.SlotAssignment{assignment},
			runtimeViews: []controllermeta.SlotRuntimeView{
				{
					SlotID:       1,
					CurrentPeers: []uint64{1},
					LeaderID:     1,
					HasQuorum:    true,
				},
			},
			tasks: map[uint32]controllermeta.ReconcileTask{1: task},
		},
		cache: cluster.assignments,
	}

	if err := agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	peers, ok := cluster.getRuntimePeers(1)
	if !ok {
		t.Fatal("getRuntimePeers() = not found, want source slot peers restored from live current peers")
	}
	if len(peers) != 1 || peers[0] != 1 {
		t.Fatalf("getRuntimePeers() = %v, want [1] when storage is empty but live current peers still include the source node", peers)
	}
}

func TestGroupAgentRetriesTaskResultReportOnControllerLeaderChange(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execErr := errors.New("injected execution failure")
	restore := harness.cluster.SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			return execErr
		}
		return nil
	})
	defer restore()

	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		reportTaskResultFn: func(_ context.Context, task controllermeta.ReconcileTask, taskErr error) error {
			reportCalls++
			if task.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", task.SlotID)
			}
			if !errors.Is(taskErr, execErr) {
				t.Fatalf("ReportTaskResult() err = %v, want %v", taskErr, execErr)
			}
			if reportCalls == 1 {
				return ErrNotLeader
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if reportCalls != 2 {
		t.Fatalf("ReportTaskResult() calls = %d, want 2", reportCalls)
	}
}

func TestGroupAgentRetriesTaskResultReportAfterTransientControllerTimeout(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execErr := errors.New("injected execution failure")
	restore := harness.cluster.SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			return execErr
		}
		return nil
	})
	defer restore()

	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		reportTaskResultFn: func(_ context.Context, task controllermeta.ReconcileTask, taskErr error) error {
			reportCalls++
			if task.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", task.SlotID)
			}
			if !errors.Is(taskErr, execErr) {
				t.Fatalf("ReportTaskResult() err = %v, want %v", taskErr, execErr)
			}
			if reportCalls == 1 {
				return context.DeadlineExceeded
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if reportCalls != 2 {
		t.Fatalf("ReportTaskResult() calls = %d, want 2", reportCalls)
	}
}

func TestGroupAgentApplyAssignmentsRetriesTransientGetTaskTimeoutWithoutLocalControllerMeta(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}

	listTasksCalls := 0
	getTaskCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		listTasksFn: func(context.Context) ([]controllermeta.ReconcileTask, error) {
			listTasksCalls++
			if listTasksCalls == 1 {
				return nil, context.DeadlineExceeded
			}
			return []controllermeta.ReconcileTask{task}, nil
		},
		getTaskFn: func(_ context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
			getTaskCalls++
			if slotID != 1 {
				t.Fatalf("GetTask() slotID = %d, want 1", slotID)
			}
			return task, nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if listTasksCalls < 2 {
		t.Fatalf("ListTasks() calls = %d, want >= 2 retry attempts", listTasksCalls)
	}
	if getTaskCalls == 0 {
		t.Fatal("GetTask() was not called for fresh confirmation")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := harness.cluster.runtime.Status(1); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("slot 1 was not bootstrapped")
}

func TestGroupAgentDoesNotReexecuteTaskWhileResultReportIsPending(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execCalls := 0
	restore := harness.cluster.SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			execCalls++
		}
		return nil
	})
	defer restore()

	allowReport := false
	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		reportTaskResultFn: func(_ context.Context, task controllermeta.ReconcileTask, _ error) error {
			reportCalls++
			if task.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", task.SlotID)
			}
			if !allowReport {
				return ErrNotLeader
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	firstCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := harness.cluster.agent.ApplyAssignments(firstCtx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("ApplyAssignments() first error = %v, want %v", err, ErrNotLeader)
	}
	if execCalls != 1 {
		t.Fatalf("exec calls after first ApplyAssignments() = %d, want 1", execCalls)
	}

	allowReport = true
	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() second error = %v", err)
	}
	if execCalls != 1 {
		t.Fatalf("exec calls after second ApplyAssignments() = %d, want 1", execCalls)
	}
	if reportCalls < 2 {
		t.Fatalf("ReportTaskResult() calls = %d, want >= 2", reportCalls)
	}
}

func TestGroupAgentRetriesPendingTaskReportWithoutRefreshingTask(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execErr := errors.New("injected execution failure")
	restore := harness.cluster.SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			return execErr
		}
		return nil
	})
	defer restore()

	getTaskCalls := 0
	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		getTaskFn: func(_ context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
			getTaskCalls++
			if slotID != 1 {
				t.Fatalf("GetTask() slotID = %d, want 1", slotID)
			}
			if getTaskCalls == 1 {
				return task, nil
			}
			return controllermeta.ReconcileTask{}, context.DeadlineExceeded
		},
		reportTaskResultFn: func(_ context.Context, got controllermeta.ReconcileTask, taskErr error) error {
			reportCalls++
			if got.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", got.SlotID)
			}
			if got.Attempt != task.Attempt {
				t.Fatalf("ReportTaskResult() attempt = %d, want %d", got.Attempt, task.Attempt)
			}
			if !errors.Is(taskErr, execErr) {
				t.Fatalf("ReportTaskResult() err = %v, want %v", taskErr, execErr)
			}
			if reportCalls == 1 {
				return context.DeadlineExceeded
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	firstCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := harness.cluster.agent.ApplyAssignments(firstCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ApplyAssignments() first error = %v, want %v", err, context.DeadlineExceeded)
	}
	if reportCalls != 1 {
		t.Fatalf("ReportTaskResult() calls after first ApplyAssignments() = %d, want 1", reportCalls)
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() second error = %v", err)
	}
	if reportCalls != 2 {
		t.Fatalf("ReportTaskResult() calls after second ApplyAssignments() = %d, want 2", reportCalls)
	}
	if getTaskCalls != 1 {
		t.Fatalf("GetTask() calls = %d, want 1 because pending replay skips task refresh", getTaskCalls)
	}
}

func TestGroupAgentReusesKnownTaskWhenFreshConfirmationTimesOut(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execErr := errors.New("injected execution failure")
	execCalls := 0
	restore := harness.cluster.SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			execCalls++
			return execErr
		}
		return nil
	})
	defer restore()

	getTaskCalls := 0
	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		getTaskFn: func(_ context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
			getTaskCalls++
			if slotID != 1 {
				t.Fatalf("GetTask() slotID = %d, want 1", slotID)
			}
			return controllermeta.ReconcileTask{}, context.DeadlineExceeded
		},
		reportTaskResultFn: func(_ context.Context, got controllermeta.ReconcileTask, taskErr error) error {
			reportCalls++
			if got.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", got.SlotID)
			}
			if got.Attempt != task.Attempt {
				t.Fatalf("ReportTaskResult() attempt = %d, want %d", got.Attempt, task.Attempt)
			}
			if !errors.Is(taskErr, execErr) {
				t.Fatalf("ReportTaskResult() err = %v, want %v", taskErr, execErr)
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if getTaskCalls == 0 {
		t.Fatal("GetTask() was not called for fresh confirmation")
	}
	if execCalls != 1 {
		t.Fatalf("execution calls = %d, want 1 when fresh confirmation times out transiently", execCalls)
	}
	if reportCalls != 1 {
		t.Fatalf("ReportTaskResult() calls = %d, want 1 after executing the known task", reportCalls)
	}
}

func TestGroupAgentSkipsBootstrapUntilBootstrapTaskExists(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}

	_, err := harness.cluster.runtime.Status(1)
	if !errors.Is(err, multiraft.ErrSlotNotFound) {
		t.Fatalf("runtime.Status() error = %v, want %v", err, multiraft.ErrSlotNotFound)
	}
}

func newStandaloneAgentTestCluster(t *testing.T) *standaloneAgentTestCluster {
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
		(&standaloneAgentTestCluster{
			cluster: cluster,
			raftDB:  raftDB,
			metaDB:  metaDB,
		}).Close()
	})

	return &standaloneAgentTestCluster{
		cluster: cluster,
		raftDB:  raftDB,
		metaDB:  metaDB,
	}
}
