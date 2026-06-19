package clusterv2

import (
	"context"
	"errors"
	"testing"

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
	if reconciler.calls != 1 || reconciler.last.Revision != 1 {
		t.Fatalf("reconciler calls=%d revision=%d, want one call revision 1", reconciler.calls, reconciler.last.Revision)
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
	if reconciler.calls != 1 {
		t.Fatalf("initial reconciler calls = %d, want 1", reconciler.calls)
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
	if reconciler.calls != 1 {
		t.Fatalf("reconciler calls = %d, want node-only change to skip slot reconcile", reconciler.calls)
	}
}

func TestNodeAppliesAliveDataNodesForChannelPlacement(t *testing.T) {
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
		control.Node{NodeID: 4, Addr: "127.0.0.1:1004", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		control.Node{NodeID: 5, Addr: "127.0.0.1:1005", Roles: []control.Role{control.RoleData}, Status: control.NodeDown},
	)
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		got = node.channelDataNodes.DataNodes()
		return equalUint64s(got, []uint64{1, 2, 3, 4})
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
	if reconciler.calls != 1 {
		t.Fatalf("initial reconciler calls = %d, want 1", reconciler.calls)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Slots[0].ConfigEpoch = 2
	next.Slots[0].PreferredLeader = 2
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		return reconciler.calls == 2
	})
	if reconciler.last.Slots[0].ConfigEpoch != 2 || reconciler.last.Slots[0].PreferredLeader != 2 {
		t.Fatalf("last reconciled snapshot = %#v, want slot epoch 2 preferred leader 2", reconciler.last)
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
	if executor.calls != 1 {
		t.Fatalf("initial executor calls = %d, want 1", executor.calls)
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
		return executor.calls == 2
	})
	if len(executor.last.Tasks) != 1 || executor.last.Tasks[0].TaskID != "slot-1-bootstrap-1" {
		t.Fatalf("executor last = %#v, want bootstrap task", executor.last)
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
	calls int
	last  control.Snapshot
}

func (r *recordingReconciler) Reconcile(_ context.Context, snap control.Snapshot) error {
	r.calls++
	r.last = snap.Clone()
	return nil
}

type recordingTaskExecutor struct {
	calls int
	last  control.Snapshot
}

func (e *recordingTaskExecutor) Reconcile(_ context.Context, snap control.Snapshot) error {
	e.calls++
	e.last = snap.Clone()
	return nil
}
