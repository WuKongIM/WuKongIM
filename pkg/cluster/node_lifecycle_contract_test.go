package cluster

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/internal/lifecycle"
)

func TestNodeStartStartsResourcesInOrder(t *testing.T) {
	var calls []string
	node := newInMemoryLifecycleNode(t,
		namedTestResource("transport", &recordingResource{name: "transport", calls: &calls}),
		namedTestResource("control-adapter", &recordingResource{name: "control-adapter", calls: &calls}),
	)
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	want := []string{"start:transport", "start:control-adapter"}
	if !equalStrings(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestNodeStopStopsResourcesInReverseOrder(t *testing.T) {
	var calls []string
	node := newInMemoryLifecycleNode(t,
		namedTestResource("transport", &recordingResource{name: "transport", calls: &calls}),
		namedTestResource("control-adapter", &recordingResource{name: "control-adapter", calls: &calls}),
	)
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	calls = nil
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	want := []string{"stop:control-adapter", "stop:transport"}
	if !equalStrings(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestNodeStopKeepsReadinessInvalidWhenResourceShutdownFails(t *testing.T) {
	var calls []string
	transportErr := errors.New("transport stop")
	adapterErr := errors.New("control adapter stop")
	node := newInMemoryLifecycleNode(t,
		namedTestResource("transport", &recordingResource{name: "transport", calls: &calls, stopErr: transportErr}),
		namedTestResource("control-adapter", &recordingResource{name: "control-adapter", calls: &calls, stopErr: adapterErr}),
	)
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	calls = nil
	err := node.Stop(context.Background())
	if !errors.Is(err, transportErr) || !errors.Is(err, adapterErr) {
		t.Fatalf("Stop() error = %v, want both shutdown failures", err)
	}
	if want := []string{"stop:control-adapter", "stop:transport"}; !equalStrings(calls, want) {
		t.Fatalf("resource calls = %#v, want reverse shutdown %#v", calls, want)
	}
	snapshot := node.Snapshot()
	if snapshot.RoutesReady || snapshot.SlotsReady || snapshot.ChannelsReady {
		t.Fatalf("Snapshot() after failed Stop = %+v, want readiness invalidated", snapshot)
	}
}

func TestNodeStartStopsStartedResourcesOnFailure(t *testing.T) {
	var calls []string
	boom := errors.New("boom")
	node := newInMemoryLifecycleNode(t,
		namedTestResource("transport", &recordingResource{name: "transport", calls: &calls}),
		namedTestResource("control-adapter", &recordingResource{name: "control-adapter", calls: &calls, startErr: boom}),
	)
	if err := node.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start() error = %v, want boom", err)
	}
	want := []string{"start:transport", "start:control-adapter", "stop:transport"}
	if !equalStrings(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestStoppedNodeRejectsForegroundWithErrStopping(t *testing.T) {
	node := newInMemoryLifecycleNode(t)
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, err := node.RouteKey("u1"); !errors.Is(err, ErrStopping) {
		t.Fatalf("RouteKey() error = %v, want ErrStopping", err)
	}
}

func TestNodeStopInvalidatesPublishedReadiness(t *testing.T) {
	node := newInMemoryLifecycleNode(t)
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	before := node.Snapshot()
	if !before.RoutesReady || !before.SlotsReady || !before.ChannelsReady {
		_ = node.Stop(context.Background())
		t.Fatalf("Snapshot() before Stop = %+v, want published readiness", before)
	}

	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	after := node.Snapshot()
	if after.RoutesReady || after.SlotsReady || after.ChannelsReady {
		t.Fatalf("Snapshot() after Stop = %+v, want all runtime readiness invalidated", after)
	}
}

func TestNodeStopFencesInFlightControlSnapshotReadiness(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	reconciler := &blockingSecondSlotReconciler{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	node, err := New(
		validNodeConfig(t),
		withController(controller),
		withSlotReconciler(reconciler),
		WithProposer(&recordingProposer{}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node.channels = noopChannelService{}
	t.Cleanup(func() {
		reconciler.Release()
		_ = node.Stop(context.Background())
	})
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if snapshot := node.Snapshot(); !snapshot.RoutesReady || !snapshot.SlotsReady || !snapshot.ChannelsReady {
		t.Fatalf("Snapshot() after Start = %+v, want published readiness", snapshot)
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.HashSlots.Revision = 2
	next.Slots[0].ConfigEpoch = 2
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case <-reconciler.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("watched snapshot did not enter Slot reconciliation")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- node.Stop(context.Background()) }()
	readinessInvalidated := make(chan struct{})
	go func() {
		for {
			snapshot := node.Snapshot()
			if !snapshot.RoutesReady && !snapshot.SlotsReady && !snapshot.ChannelsReady {
				close(readinessInvalidated)
				return
			}
			runtime.Gosched()
		}
	}()
	select {
	case <-readinessInvalidated:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not invalidate readiness while the watched snapshot was in flight")
	}

	reconciler.Release()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not wait for the watched snapshot to finish")
	}
	after := node.Snapshot()
	if after.StateRevision != 2 {
		t.Fatalf("Snapshot() after Stop revision = %d, want completed watched revision 2", after.StateRevision)
	}
	if after.RoutesReady || after.SlotsReady || after.ChannelsReady {
		t.Fatalf("Snapshot() after Stop = %+v, want in-flight apply fenced from republishing readiness", after)
	}
}

func TestNodeControlWatchKeepsRuntimeReadyBeforeStop(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	observer := &revisionSnapshotObserver{revision: 2, observed: make(chan control.Snapshot, 1)}
	cfg := validNodeConfig(t)
	cfg.Control.SnapshotObserver = observer
	node, err := New(
		cfg,
		withController(controller),
		withSlotReconciler(&recordingReconciler{}),
		WithProposer(&recordingProposer{}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node.channels = noopChannelService{}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	next := nodeControlSnapshot()
	next.Revision = 2
	next.HashSlots.Revision = 2
	next.Slots[0].ConfigEpoch = 2
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case observed := <-observer.observed:
		if observed.Revision != 2 {
			t.Fatalf("observed revision = %d, want 2", observed.Revision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watched snapshot revision 2 was not applied")
	}

	after := node.Snapshot()
	if after.StateRevision != 2 || !after.RoutesReady || !after.SlotsReady || !after.ChannelsReady {
		t.Fatalf("Snapshot() after watched update = %+v, want revision 2 fully ready", after)
	}
}

func TestNodeStartWaitsForFirstValidControlSnapshot(t *testing.T) {
	next := nodeControlSnapshot()
	next.Revision = 2
	next.HashSlots.Revision = 2
	events := make(chan control.SnapshotEvent, 1)
	events <- control.SnapshotEvent{Snapshot: next}
	controller := &bootstrapSnapshotController{
		StaticController: control.NewStaticController(nodeControlSnapshot()),
		events:           events,
	}
	node := newInMemoryLifecycleNodeWithController(t, controller)
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	snapshot := node.Snapshot()
	if snapshot.StateRevision != 2 || !snapshot.RoutesReady || !snapshot.SlotsReady || !snapshot.ChannelsReady {
		t.Fatalf("Snapshot() = %+v, want first valid watched revision 2 ready", snapshot)
	}
}

func TestNodeStartRejectsInvalidControlSnapshotBeforePublishingReadiness(t *testing.T) {
	var calls []string
	controller := &bootstrapSnapshotController{
		StaticController: control.NewStaticController(nodeControlSnapshot()),
		local:            control.Snapshot{Revision: 9},
		events:           make(chan control.SnapshotEvent),
	}
	node := newInMemoryLifecycleNodeWithController(
		t, controller,
		namedTestResource("transport", &recordingResource{name: "transport", calls: &calls}),
	)
	err := node.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "hash slot count") {
		t.Fatalf("Start() error = %v, want invalid non-empty control snapshot", err)
	}
	if want := []string{"start:transport", "stop:transport"}; !equalStrings(calls, want) {
		t.Fatalf("resource calls = %#v, want rollback %#v", calls, want)
	}
	if snapshot := node.Snapshot(); snapshot.RoutesReady || snapshot.SlotsReady || snapshot.ChannelsReady {
		t.Fatalf("Snapshot() after rejected control snapshot = %+v, want unpublished readiness", snapshot)
	}
	if _, routeErr := node.RouteKey("user"); !errors.Is(routeErr, ErrNotStarted) {
		t.Fatalf("RouteKey() error = %v, want ErrNotStarted after failed Start", routeErr)
	}
}

func newInMemoryLifecycleNode(t *testing.T, resources ...lifecycle.NamedResource) *Node {
	t.Helper()
	return newInMemoryLifecycleNodeWithController(
		t, control.NewStaticController(nodeControlSnapshot()), resources...,
	)
}

func newInMemoryLifecycleNodeWithController(t *testing.T, controller control.Controller, resources ...lifecycle.NamedResource) *Node {
	t.Helper()
	node, err := New(
		validNodeConfig(t),
		withController(controller),
		WithProposer(&recordingProposer{}),
		withResources(resources...),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node.channels = noopChannelService{}
	return node
}

func namedTestResource(name string, resource lifecycle.Resource) lifecycle.NamedResource {
	return lifecycle.NamedResource{Name: name, Resource: resource}
}

type recordingResource struct {
	name     string
	calls    *[]string
	startErr error
	stopErr  error
}

func (r *recordingResource) Start(context.Context) error {
	*r.calls = append(*r.calls, "start:"+r.name)
	return r.startErr
}

func (r *recordingResource) Stop(context.Context) error {
	*r.calls = append(*r.calls, "stop:"+r.name)
	return r.stopErr
}

type bootstrapSnapshotController struct {
	*control.StaticController
	local  control.Snapshot
	events chan control.SnapshotEvent
}

func (c *bootstrapSnapshotController) LocalSnapshot(ctx context.Context) (control.Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return control.Snapshot{}, err
	}
	return c.local.Clone(), nil
}

func (c *bootstrapSnapshotController) Watch() <-chan control.SnapshotEvent {
	return c.events
}

type blockingSecondSlotReconciler struct {
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	calls       int
}

func (r *blockingSecondSlotReconciler) Reconcile(context.Context, control.Snapshot) error {
	r.calls++
	if r.calls != 2 {
		return nil
	}
	close(r.entered)
	<-r.release
	return nil
}

func (r *blockingSecondSlotReconciler) Release() {
	r.releaseOnce.Do(func() { close(r.release) })
}

type revisionSnapshotObserver struct {
	revision uint64
	observed chan control.Snapshot
}

func (o *revisionSnapshotObserver) ObserveControlSnapshot(snapshot control.Snapshot) {
	if snapshot.Revision != o.revision {
		return
	}
	select {
	case o.observed <- snapshot.Clone():
	default:
	}
}
