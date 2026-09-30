package cluster

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

// Retained publication after a failed initial task cannot certify the recreated
// Slot runtime or suppress retry of the synchronous setup that failed.
func TestInitialSnapshotSetupRetriesAfterTaskFailure(t *testing.T) {
	setupErr := errors.New("initial task setup failed")
	executor := &flakyTaskExecutor{errs: []error{setupErr}}
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), slots: &recordingReconciler{}, tasks: executor}
	snapshot := nodeControlSnapshot()
	snapshot.HashSlots.Count = 256
	snapshot.HashSlots.Ranges[0].To = 255
	snapshot.Tasks = []control.ReconcileTask{bootstrapTaskForNodeSnapshotTest()}
	if err := node.applySnapshot(context.Background(), snapshot); !errors.Is(err, setupErr) {
		t.Fatalf("first setup = %v, want %v", err, setupErr)
	}
	recreated := &recordingReconciler{}
	node.slots = recreated
	if err := node.applySnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if got := executor.Calls(); got != 2 {
		t.Errorf("task setup attempts = %d, want 2", got)
	}
	if got := recreated.Calls(); got != 1 {
		t.Errorf("recreated Slot runtime reconciliations = %d, want 1", got)
	}
}

// Failure modes: an executing task holds snapshot refresh; a queued wake replays
// its captured revision; a stopped task loop leaves an owned executor running.
func TestRuntimeSnapshotRefreshDoesNotWaitForExecutingTask(t *testing.T) {
	initial := nodeControlSnapshot()
	initial.HashSlots.Count = 256
	initial.HashSlots.Ranges[0].To = 255
	initial.Tasks = []control.ReconcileTask{bootstrapTaskForNodeSnapshotTest()}
	source := control.NewStaticController(initial)
	executor := &blockingTaskExecutor{entered: make(chan struct{}, 1), unblock: make(chan struct{})}
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), control: source, tasks: executor, controlSnapshot: initial.Clone()}
	node.started.Store(true)
	node.startTaskReconcileLoop()
	done := make(chan error, 1)
	finished := false
	defer func() {
		close(executor.unblock)
		node.stopTaskReconcileLoop()
		if !finished {
			<-done
		}
	}()
	select {
	case <-executor.entered:
	case <-time.After(time.Second):
		finished = true
		t.Fatal("background task did not start")
	}
	latest := initial.Clone()
	latest.Revision++
	latest.Tasks[0].Status = control.TaskStatusRunning
	for i := range latest.Nodes {
		latest.Nodes[i].Health = control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh, RuntimeReady: true}
	}
	if err := source.Publish(latest); err != nil {
		finished = true
		t.Fatal(err)
	}
	go func() { done <- node.refreshControlSnapshot(context.Background()) }()
	select {
	case err := <-done:
		finished = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("health snapshot refresh waited for an executing task")
	}
	if got := node.Snapshot().StateRevision; got != latest.Revision {
		t.Errorf("published revision = %d, want %d", got, latest.Revision)
	}
	if got := node.channelDataNodes.DataNodes(); !slices.Equal(got, []uint64{1, 2, 3}) {
		t.Errorf("placement candidates = %v, want [1 2 3]", got)
	}
}

// A slow task progress write must not hide a committed health report or leave
// placement candidates and the Node snapshot on a previous route revision.
func TestApplySnapshotPublishesHealthBeforeTaskReconciliation(t *testing.T) {
	executor := &blockingTaskExecutor{entered: make(chan struct{}, 1), unblock: make(chan struct{})}
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), tasks: executor}
	snapshot := nodeControlSnapshot()
	snapshot.HashSlots.Count = 256
	snapshot.HashSlots.Ranges[0].To = 255
	snapshot.Tasks = []control.ReconcileTask{bootstrapTaskForNodeSnapshotTest()}
	for i := range snapshot.Nodes {
		snapshot.Nodes[i].Health = control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh, RuntimeReady: true}
	}
	done := make(chan error, 1)
	go func() { done <- node.applySnapshot(context.Background(), snapshot) }()
	defer func() {
		close(executor.unblock)
		if err := <-done; err != nil {
			t.Errorf("applySnapshot: %v", err)
		}
	}()
	select {
	case <-executor.entered:
	case <-time.After(time.Second):
		t.Fatal("task reconciliation did not start")
	}
	if got := node.Snapshot().StateRevision; got != snapshot.Revision {
		t.Errorf("snapshot revision while task blocked = %d, want %d", got, snapshot.Revision)
	}
	if got := node.channelDataNodes.DataNodes(); !slices.Equal(got, []uint64{1, 2, 3}) {
		t.Errorf("placement candidates while task blocked = %v, want [1 2 3]", got)
	}
	if got := node.router.Table().Revision; got != snapshot.Revision {
		t.Errorf("route revision while task blocked = %d, want %d", got, snapshot.Revision)
	}
}

// A probe can read an older Controller snapshot while the watch is applying a
// newer one. Acquiring the apply lock later must not roll back that publication.
func TestApplySnapshotRejectsOlderSnapshotAfterConcurrentApply(t *testing.T) {
	executor := &blockingTaskExecutor{entered: make(chan struct{}, 1), unblock: make(chan struct{})}
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), tasks: executor}
	older := nodeControlSnapshot()
	older.HashSlots.Count = 256
	older.HashSlots.Ranges[0].To = 255
	newer := older.Clone()
	newer.Revision++
	newer.Maintenance = true
	newer.Tasks = []control.ReconcileTask{bootstrapTaskForNodeSnapshotTest()}
	newer.Slots[0].ConfigEpoch++
	applied := make(chan error, 1)
	go func() { applied <- node.applySnapshot(context.Background(), newer) }()
	select {
	case <-executor.entered:
	case err := <-applied:
		close(executor.unblock)
		t.Fatalf("apply returned before reconciliation: %v", err)
	case <-time.After(time.Second):
		close(executor.unblock)
		t.Fatal("newer snapshot did not reach task reconciliation")
	}
	staleDone := make(chan error, 1)
	go func() { staleDone <- node.applySnapshot(context.Background(), older) }()
	close(executor.unblock)
	for _, done := range []chan error{applied, staleDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("snapshot application did not complete")
		}
	}
	if got := node.Snapshot().StateRevision; got != newer.Revision {
		t.Errorf("applied revision = %d, want %d", got, newer.Revision)
	}
	if !node.maintenance.Load() {
		t.Error("older snapshot cleared maintenance")
	}
	if got := node.router.Table(); got.Revision != newer.Revision || got.SlotConfigEpochs[newer.Slots[0].SlotID] != newer.Slots[0].ConfigEpoch {
		t.Errorf("older snapshot replaced route authority: revision=%d epoch=%d", got.Revision, got.SlotConfigEpochs[newer.Slots[0].SlotID])
	}
	if len(node.controlSnapshot.Tasks) != 1 {
		t.Error("older snapshot replaced current task state")
	}
}

// Health reports may change placement eligibility without a logical revision
// increment; equal-revision snapshots must continue to refresh node state.
func TestApplySnapshotPreservesEqualRevisionRefresh(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter()}
	snapshot := nodeControlSnapshot()
	snapshot.HashSlots.Count = 256
	snapshot.HashSlots.Ranges[0].To = 255
	if err := node.applySnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	refreshed := snapshot.Clone()
	refreshed.ControllerID = 2
	refreshed.Nodes[0].Health = control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthStale}
	if err := node.applySnapshot(context.Background(), refreshed); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(node.channelDataNodes.DataNodes(), uint64(1)) {
		t.Error("equal revision did not remove stale-health placement candidate")
	}
	if got := node.Snapshot().ControllerLead; got != 2 {
		t.Fatalf("controller = %d, want refreshed controller 2", got)
	}
}

func TestControlWatchReadsCurrentSnapshotInsteadOfQueuedTaskProgress(t *testing.T) {
	queued := nodeControlSnapshot()
	queued.HashSlots.Count = 256
	queued.HashSlots.Ranges[0].To = 255
	queued.Tasks = []control.ReconcileTask{bootstrapTaskForNodeSnapshotTest()}
	current := queued.Clone()
	current.Revision++
	current.Tasks = nil
	source := &advancingLocalSnapshotController{snapshots: []control.Snapshot{current}, watch: make(chan control.SnapshotEvent, 1)}
	executor := &snapshotNotificationExecutor{snapshots: make(chan control.Snapshot, 1)}
	node := &Node{cfg: Config{NodeID: 1}, control: source, router: routing.NewRouter(), tasks: executor}
	source.watch <- control.SnapshotEvent{Snapshot: queued}
	node.startWatchLoop()
	defer node.stopWatchLoop()
	select {
	case got := <-executor.snapshots:
		if got.Revision != current.Revision || len(got.Tasks) != 0 {
			t.Fatalf("watch reconciled queued tasks at revision %d, want current revision %d without tasks", got.Revision, current.Revision)
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not reconcile its notification")
	}
}

type snapshotNotificationExecutor struct{ snapshots chan control.Snapshot }

func (e *snapshotNotificationExecutor) Reconcile(_ context.Context, snapshot control.Snapshot) error {
	e.snapshots <- snapshot.Clone()
	return nil
}
