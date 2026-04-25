package cluster

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type stubAssignmentReconciler struct {
	tickFn func(context.Context) error
}

func (s stubAssignmentReconciler) Tick(ctx context.Context) error {
	if s.tickFn == nil {
		return nil
	}
	return s.tickFn(ctx)
}

func TestSlotAgentApplyAssignmentsDelegatesToReconciler(t *testing.T) {
	sentinel := errors.New("reconciler tick failed")
	tickCalls := 0
	agent := &slotAgent{
		cluster: &Cluster{cfg: Config{NodeID: 1}},
		client:  fakeControllerClient{},
		cache:   newAssignmentCache(),
		reconciler: stubAssignmentReconciler{
			tickFn: func(context.Context) error {
				tickCalls++
				return sentinel
			},
		},
	}

	err := agent.ApplyAssignments(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("ApplyAssignments() error = %v, want %v", err, sentinel)
	}
	if tickCalls != 1 {
		t.Fatalf("reconciler.Tick() calls = %d, want 1", tickCalls)
	}
}

func TestReconcilerTickReplaysPendingTaskReportWithoutRefetchingTask(t *testing.T) {
	cluster := newObserverTestCluster(t, ObserverHooks{})
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

	cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})
	getTaskCalls := 0
	reportCalls := 0
	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			getTaskFn: func(context.Context, uint32) (controllermeta.ReconcileTask, error) {
				getTaskCalls++
				return task, nil
			},
			reportTaskResultFn: func(_ context.Context, gotTask controllermeta.ReconcileTask, gotErr error) error {
				reportCalls++
				if !sameReconcileTaskIdentity(gotTask, task) {
					t.Fatalf("reported task = %+v, want %+v", gotTask, task)
				}
				if gotErr != nil {
					t.Fatalf("reported taskErr = %v, want nil", gotErr)
				}
				return nil
			},
		},
		cache: cluster.assignments,
	}
	agent.storePendingTaskReport(assignment.SlotID, task, nil)

	if err := newReconciler(agent).Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if getTaskCalls != 0 {
		t.Fatalf("GetTask() calls = %d, want 0 when pending report exists", getTaskCalls)
	}
	if reportCalls != 1 {
		t.Fatalf("ReportTaskResult() calls = %d, want 1", reportCalls)
	}
	if _, ok := agent.pendingTaskReport(assignment.SlotID); ok {
		t.Fatal("pending task report should be cleared after replay")
	}
}

func TestReconcilerTickUsesKnownTaskWhenFreshTaskConfirmationTimesOut(t *testing.T) {
	cluster := newObserverTestCluster(t, ObserverHooks{})
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

	cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})

	execCalls := 0
	restore := cluster.SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == assignment.SlotID && sameReconcileTaskIdentity(got, task) {
			execCalls++
		}
		return nil
	})
	defer restore()

	getTaskCalls := 0
	reportCalls := 0
	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			listTasks: []controllermeta.ReconcileTask{task},
			getTaskFn: func(context.Context, uint32) (controllermeta.ReconcileTask, error) {
				getTaskCalls++
				return controllermeta.ReconcileTask{}, context.DeadlineExceeded
			},
			reportTaskResultFn: func(_ context.Context, gotTask controllermeta.ReconcileTask, gotErr error) error {
				reportCalls++
				if !sameReconcileTaskIdentity(gotTask, task) {
					t.Fatalf("reported task = %+v, want %+v", gotTask, task)
				}
				if gotErr != nil {
					t.Fatalf("reported taskErr = %v, want nil", gotErr)
				}
				return nil
			},
		},
		cache: cluster.assignments,
	}

	if err := newReconciler(agent).Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
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

func TestReconcilerTickScopesToAffectedLocalSlotsWhenDeltaIsScoped(t *testing.T) {
	ensuredSlots := make([]uint32, 0, 2)
	cluster := newObserverTestCluster(t, ObserverHooks{
		OnSlotEnsure: func(slotID uint32, action string, err error) {
			if err != nil {
				t.Fatalf("OnSlotEnsure() err = %v, want nil", err)
			}
			if action == "bootstrap" || action == "open" {
				ensuredSlots = append(ensuredSlots, slotID)
			}
		},
	})
	cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{
		{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1},
		{SlotID: 2, DesiredPeers: []uint64{1}, ConfigEpoch: 1},
	})

	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			},
			runtimeViews: []controllermeta.SlotRuntimeView{
				{SlotID: 1, CurrentPeers: []uint64{1}, LeaderID: 1, HasQuorum: true},
				{SlotID: 2, CurrentPeers: []uint64{1}, LeaderID: 1, HasQuorum: true},
			},
		},
		cache: cluster.assignments,
	}
	agent.setPendingReconcileScope([]uint32{1})

	if err := newReconciler(agent).Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if len(ensuredSlots) != 1 || ensuredSlots[0] != 1 {
		t.Fatalf("ensured slots = %v, want only slot 1 from the scoped delta", ensuredSlots)
	}
	if _, err := cluster.runtime.Status(1); err != nil {
		t.Fatalf("runtime.Status(1) error = %v, want slot 1 opened", err)
	}
	if _, err := cluster.runtime.Status(2); !errors.Is(err, multiraft.ErrSlotNotFound) {
		t.Fatalf("runtime.Status(2) error = %v, want %v when slot 2 is outside the scoped delta", err, multiraft.ErrSlotNotFound)
	}
}

func TestReconcilerTickLoadsTasksViaListTasksBeforePerSlotConfirmation(t *testing.T) {
	cluster, err := NewCluster(Config{
		NodeID:       1,
		ListenAddr:   "127.0.0.1:0",
		SlotCount:    2,
		SlotReplicaN: 1,
		PoolSize:     1,
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:0"},
		},
		NewStorage: func(multiraft.SlotID) (multiraft.Storage, error) {
			return &observerTestStorage{}, nil
		},
		NewStateMachine: func(multiraft.SlotID) (multiraft.StateMachine, error) {
			return observerTestStateMachine{}, nil
		},
		NewStateMachineWithHashSlots: func(multiraft.SlotID, []uint16) (multiraft.StateMachine, error) {
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
	t.Cleanup(func() {
		_ = rt.Close()
	})
	cluster.runtime = rt
	cluster.router = NewRouter(
		NewHashSlotTable(cluster.cfg.effectiveHashSlotCount(), int(cluster.cfg.effectiveInitialSlotCount())),
		cluster.cfg.NodeID,
		rt,
	)

	cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{
		{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1},
		{SlotID: 2, DesiredPeers: []uint64{1}, ConfigEpoch: 1},
	})

	var listTasksCalls int32
	var getTaskCalls int32
	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			listTasksFn: func(context.Context) ([]controllermeta.ReconcileTask, error) {
				atomic.AddInt32(&listTasksCalls, 1)
				return nil, nil
			},
			getTaskFn: func(context.Context, uint32) (controllermeta.ReconcileTask, error) {
				atomic.AddInt32(&getTaskCalls, 1)
				return controllermeta.ReconcileTask{}, controllermeta.ErrNotFound
			},
		},
		cache: cluster.assignments,
	}

	if err := newReconciler(agent).Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if got := atomic.LoadInt32(&listTasksCalls); got != 1 {
		t.Fatalf("ListTasks() calls = %d, want 1 bulk read", got)
	}
	if got := atomic.LoadInt32(&getTaskCalls); got != 0 {
		t.Fatalf("GetTask() calls = %d, want 0 without runnable tasks", got)
	}
}

func TestReconcilerTickLoadsTasksOnceForMultipleAssignments(t *testing.T) {
	cluster, err := NewCluster(Config{
		NodeID:       1,
		ListenAddr:   "127.0.0.1:0",
		SlotCount:    4,
		SlotReplicaN: 1,
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:0"},
		},
		NewStorage: func(multiraft.SlotID) (multiraft.Storage, error) {
			return &observerTestStorage{}, nil
		},
		NewStateMachine: func(multiraft.SlotID) (multiraft.StateMachine, error) {
			return observerTestStateMachine{}, nil
		},
		NewStateMachineWithHashSlots: func(multiraft.SlotID, []uint16) (multiraft.StateMachine, error) {
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
	t.Cleanup(func() {
		_ = rt.Close()
	})
	cluster.runtime = rt
	cluster.router = NewRouter(
		NewHashSlotTable(cluster.cfg.effectiveHashSlotCount(), int(cluster.cfg.effectiveInitialSlotCount())),
		cluster.cfg.NodeID,
		rt,
	)

	assignments := []controllermeta.SlotAssignment{
		{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1},
		{SlotID: 2, DesiredPeers: []uint64{1}, ConfigEpoch: 1},
		{SlotID: 3, DesiredPeers: []uint64{1}, ConfigEpoch: 1},
		{SlotID: 4, DesiredPeers: []uint64{1}, ConfigEpoch: 1},
	}
	cluster.assignments.SetAssignments(assignments)

	var listTasksCalls int32
	var getTaskCalls int32

	agent := &slotAgent{
		cluster: cluster,
		client: fakeControllerClient{
			listTasksFn: func(context.Context) ([]controllermeta.ReconcileTask, error) {
				atomic.AddInt32(&listTasksCalls, 1)
				return nil, nil
			},
			getTaskFn: func(_ context.Context, _ uint32) (controllermeta.ReconcileTask, error) {
				atomic.AddInt32(&getTaskCalls, 1)
				return controllermeta.ReconcileTask{}, controllermeta.ErrNotFound
			},
		},
		cache: cluster.assignments,
	}

	if err := newReconciler(agent).Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if got := atomic.LoadInt32(&listTasksCalls); got != 1 {
		t.Fatalf("ListTasks() calls = %d, want 1 bulk read", got)
	}
	if got := atomic.LoadInt32(&getTaskCalls); got != 0 {
		t.Fatalf("GetTask() calls = %d, want 0 without runnable tasks", got)
	}
}
