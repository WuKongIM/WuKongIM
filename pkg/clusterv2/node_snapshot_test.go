package clusterv2

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestNodeStartAppliesControlSnapshot(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	reconciler := &recordingReconciler{}
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(reconciler))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if _, err := node.RouteHashSlot(0); !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteHashSlot() error = %v, want ErrNoSlotLeader before status observation", err)
	}
	if calls, last := reconciler.Calls(), reconciler.Last(); calls != 1 || last.Revision != 1 {
		t.Fatalf("reconciler calls=%d revision=%d, want one call revision 1", calls, last.Revision)
	}
	if snap := node.Snapshot(); !snap.RoutesReady || !snap.SlotsReady || snap.StateRevision != 1 {
		t.Fatalf("Snapshot() = %#v, want ready revision 1", snap)
	}
}

func TestNodeControlWatchUpdatesRouteRevision(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(&recordingReconciler{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	next := nodeControlSnapshot()
	next.Revision = 2
	next.HashSlots.Revision = 2
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		return node.Snapshot().StateRevision == 2
	})
}

func TestNodeControlSnapshotObserverSeesInitialAndWatchedSnapshots(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	observer := &recordingControlSnapshotObserver{}
	cfg := validNodeConfig(t)
	cfg.Control.SnapshotObserver = observer
	node, err := New(cfg, withController(controller), withSlotReconciler(&recordingReconciler{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if calls, last := observer.Calls(), observer.Last(); calls != 1 || last.Revision != 1 {
		t.Fatalf("observer calls=%d last revision=%d, want initial revision 1", calls, last.Revision)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.HashSlots.Revision = 2
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		calls, last := observer.Calls(), observer.Last()
		return calls == 2 && last.Revision == 2
	})
}

func TestNodeControlWatchNodeOnlyChangeSkipsSlotReconcile(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	reconciler := &recordingReconciler{}
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(reconciler))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if calls := reconciler.Calls(); calls != 1 {
		t.Fatalf("initial reconciler calls = %d, want 1", calls)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Nodes[1].Status = control.NodeDown
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		return node.Snapshot().StateRevision == 2
	})
	if calls := reconciler.Calls(); calls != 1 {
		t.Fatalf("reconciler calls = %d, want node-only change to skip slot reconcile", calls)
	}
}

func TestNodeAppliesActiveDataNodesForChannelPlacement(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(&recordingReconciler{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	got := node.channelDataNodes.DataNodes()
	want := []uint64{1, 2, 3}
	if !equalUint64s(got, want) {
		t.Fatalf("DataNodes() = %v, want %v", got, want)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Nodes = append(next.Nodes,
		control.Node{NodeID: 4, Addr: "127.0.0.1:1004", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
		control.Node{NodeID: 5, Addr: "127.0.0.1:1005", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateJoining},
		control.Node{NodeID: 6, Addr: "127.0.0.1:1006", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
		control.Node{NodeID: 7, Addr: "127.0.0.1:1007", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateRemoved},
		control.Node{NodeID: 8, Addr: "127.0.0.1:1008", Roles: []control.Role{control.RoleData}, Status: control.NodeSuspect, JoinState: control.NodeJoinStateActive},
	)
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		got = node.channelDataNodes.DataNodes()
		return equalUint64s(got, []uint64{1, 2, 3, 4, 8})
	})
}

func TestNodeControlWatchSlotChangeReconcilesSlots(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	reconciler := &recordingReconciler{}
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(reconciler))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if calls := reconciler.Calls(); calls != 1 {
		t.Fatalf("initial reconciler calls = %d, want 1", calls)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Slots[0].ConfigEpoch = 2
	next.Slots[0].PreferredLeader = 2
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		return reconciler.Calls() == 2
	})
	lastReconciled := reconciler.Last()
	if lastReconciled.Slots[0].ConfigEpoch != 2 || lastReconciled.Slots[0].PreferredLeader != 2 {
		t.Fatalf("last reconciled snapshot = %#v, want slot epoch 2 preferred leader 2", lastReconciled)
	}
}

func TestNodeControlWatchTaskChangeRunsTaskExecutor(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	executor := &recordingTaskExecutor{}
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(&recordingReconciler{}), withTaskExecutor(executor))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if calls := executor.Calls(); calls != 1 {
		t.Fatalf("initial executor calls = %d, want 1", calls)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Tasks = []control.ReconcileTask{{
		TaskID:           "slot-1-bootstrap-1",
		SlotID:           1,
		Kind:             control.TaskKindBootstrap,
		Step:             control.TaskStepCreateSlot,
		TargetNode:       1,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: control.TaskCompletionPolicyAllTargetPeers,
		ParticipantProgress: []control.TaskParticipantProgress{
			{NodeID: 1, Status: control.TaskParticipantStatusPending},
			{NodeID: 2, Status: control.TaskParticipantStatusPending},
			{NodeID: 3, Status: control.TaskParticipantStatusPending},
		},
		ConfigEpoch: 1,
		Status:      control.TaskStatusPending,
	}}
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		return executor.Calls() == 2
	})
	lastExecuted := executor.Last()
	if len(lastExecuted.Tasks) != 1 || lastExecuted.Tasks[0].TaskID != "slot-1-bootstrap-1" {
		t.Fatalf("executor last = %#v, want bootstrap task", lastExecuted)
	}
}

func TestNodeStopWaitsForControlWatchApplySnapshot(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	executor := newBlockingTaskExecutor(2)
	node, err := New(validNodeConfig(t), withController(controller), withSlotReconciler(&recordingReconciler{}), withTaskExecutor(executor))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Tasks = []control.ReconcileTask{{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		Kind:        control.TaskKindBootstrap,
		Step:        control.TaskStepCreateSlot,
		TargetNode:  1,
		TargetPeers: []uint64{1, 2, 3},
		ConfigEpoch: 1,
		Status:      control.TaskStatusPending,
	}}
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		return executor.blockingCallEntered()
	})

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- node.Stop(context.Background())
	}()
	select {
	case err := <-stopDone:
		t.Fatalf("Stop() returned while watch-loop applySnapshot was still blocked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	executor.unblock()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return after task executor unblocked")
	}
}

func TestControlSnapshotChangesDetectTasks(t *testing.T) {
	previous := nodeControlSnapshot()
	next := previous.Clone()
	next.Revision = 2
	next.Tasks = []control.ReconcileTask{{
		TaskID:      "bootstrap-1",
		SlotID:      1,
		Kind:        control.TaskKindBootstrap,
		TargetNode:  1,
		TargetPeers: []uint64{1, 2, 3},
		ConfigEpoch: 1,
	}}

	changes := snapshotChanges(previous, next)
	if !changes.tasks || changes.nodes || changes.slots || changes.hashSlots {
		t.Fatalf("snapshotChanges() = %#v, want only tasks changed", changes)
	}
}

func TestControlSnapshotChangesDetectHashSlots(t *testing.T) {
	previous := nodeControlSnapshot()
	next := previous.Clone()
	next.Revision = 2
	next.HashSlots.Revision = 2

	changes := snapshotChanges(previous, next)
	if !changes.hashSlots || changes.nodes || changes.slots || changes.tasks {
		t.Fatalf("snapshotChanges() = %#v, want only hash slots changed", changes)
	}
}

func equalUint64s(a []uint64, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type recordingReconciler struct {
	mu    sync.Mutex
	calls int
	last  control.Snapshot
}

func (r *recordingReconciler) Reconcile(_ context.Context, snap control.Snapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.last = snap.Clone()
	return nil
}

func (r *recordingReconciler) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recordingReconciler) Last() control.Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last.Clone()
}

type recordingControlSnapshotObserver struct {
	mu    sync.Mutex
	calls int
	last  control.Snapshot
}

func (o *recordingControlSnapshotObserver) ObserveControlSnapshot(snap control.Snapshot) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	o.last = snap.Clone()
}

func (o *recordingControlSnapshotObserver) Calls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

func (o *recordingControlSnapshotObserver) Last() control.Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.last.Clone()
}

type recordingTaskExecutor struct {
	mu    sync.Mutex
	calls int
	last  control.Snapshot
}

func (e *recordingTaskExecutor) Reconcile(_ context.Context, snap control.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.last = snap.Clone()
	return nil
}

func (e *recordingTaskExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *recordingTaskExecutor) Last() control.Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.last.Clone()
}

type blockingWatchTaskExecutor struct {
	mu          sync.Mutex
	calls       int
	blockOnCall int
	blocked     bool
	unblockCh   chan struct{}
}

func newBlockingTaskExecutor(blockOnCall int) *blockingWatchTaskExecutor {
	return &blockingWatchTaskExecutor{blockOnCall: blockOnCall, unblockCh: make(chan struct{})}
}

func (e *blockingWatchTaskExecutor) Reconcile(_ context.Context, snap control.Snapshot) error {
	e.mu.Lock()
	e.calls++
	shouldBlock := e.calls == e.blockOnCall
	if shouldBlock {
		e.blocked = true
	}
	e.mu.Unlock()
	if !shouldBlock {
		return nil
	}
	<-e.unblockCh
	return nil
}

func (e *blockingWatchTaskExecutor) blockingCallEntered() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.blocked
}

func (e *blockingWatchTaskExecutor) unblock() {
	close(e.unblockCh)
}
