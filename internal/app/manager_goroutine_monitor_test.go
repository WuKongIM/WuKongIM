package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

type goroutineMonitorControl struct {
	nodes managementusecase.NodeList
}

func (c goroutineMonitorControl) ListNodes(context.Context) (managementusecase.NodeList, error) {
	return c.nodes, nil
}

func (goroutineMonitorControl) ListSlots(context.Context, managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error) {
	return nil, nil
}

type goroutineMonitorRemote struct {
	snapshots map[uint64]managementusecase.GoroutineSnapshot
	err       error
}

func (r goroutineMonitorRemote) ManagerGoroutineSnapshot(_ context.Context, nodeID uint64) (managementusecase.GoroutineSnapshot, error) {
	if snapshot, ok := r.snapshots[nodeID]; ok {
		return snapshot, nil
	}
	return managementusecase.GoroutineSnapshot{}, r.err
}

type goroutineMonitorBase struct{}

func (goroutineMonitorBase) RealtimeMonitor(_ context.Context, query accessmanager.RealtimeMonitorQuery) (accessmanager.RealtimeMonitorResponse, error) {
	return accessmanager.RealtimeMonitorResponse{
		Status: accessmanager.RealtimeMonitorStatusPrometheusDisabled,
		Scope:  accessmanager.RealtimeMonitorScope{View: accessmanager.RealtimeMonitorScopeUnified, NodeID: query.NodeID},
	}, nil
}

type goroutineMonitorBoundedRemote struct {
	active atomic.Int32
	max    atomic.Int32
	calls  atomic.Int32
	delay  time.Duration
}

type goroutineMonitorBlockingRemote struct{}

func (goroutineMonitorBlockingRemote) ManagerGoroutineSnapshot(ctx context.Context, _ uint64) (managementusecase.GoroutineSnapshot, error) {
	<-ctx.Done()
	return managementusecase.GoroutineSnapshot{}, ctx.Err()
}

type goroutineMonitorPanicRemote struct {
	calls atomic.Int32
}

func (r *goroutineMonitorPanicRemote) ManagerGoroutineSnapshot(context.Context, uint64) (managementusecase.GoroutineSnapshot, error) {
	r.calls.Add(1)
	panic("remote panic")
}

func (r *goroutineMonitorBoundedRemote) ManagerGoroutineSnapshot(ctx context.Context, _ uint64) (managementusecase.GoroutineSnapshot, error) {
	r.calls.Add(1)
	active := r.active.Add(1)
	defer r.active.Add(-1)
	for {
		peak := r.max.Load()
		if active <= peak || r.max.CompareAndSwap(peak, active) {
			break
		}
	}
	timer := time.NewTimer(r.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return managementusecase.GoroutineSnapshot{GeneratedAt: time.Now()}, nil
	case <-ctx.Done():
		return managementusecase.GoroutineSnapshot{}, ctx.Err()
	}
}

func TestManagerGoroutineMonitorPreservesNodeIdentityAndPartialSupport(t *testing.T) {
	registry := goruntimeregistry.New()
	monitor := &managerGoroutineMonitor{
		localNodeID: 1,
		registry:    registry,
		control: goroutineMonitorControl{nodes: managementusecase.NodeList{Items: []managementusecase.Node{
			{NodeID: 1, Name: "n1", Status: "alive"},
			{NodeID: 2, Name: "n2", Status: "alive"},
		}}},
		remote: goroutineMonitorRemote{err: clusternet.ErrServiceNotFound},
	}

	snapshot, source := monitor.snapshot(context.Background(), 0)
	if snapshot.Status != accessmanager.RealtimeMonitorStatusPartial || len(snapshot.Nodes) != 2 {
		t.Fatalf("snapshot = %+v, want two-node partial result", snapshot)
	}
	if !snapshot.Nodes[0].Supported || snapshot.Nodes[0].Snapshot == nil || snapshot.Nodes[0].NodeID != 1 {
		t.Fatalf("local node = %+v, want supported node 1", snapshot.Nodes[0])
	}
	if snapshot.Nodes[1].Supported || snapshot.Nodes[1].Error == "" || snapshot.Nodes[1].NodeID != 2 {
		t.Fatalf("remote node = %+v, want unsupported node 2", snapshot.Nodes[1])
	}
	if !source.Enabled {
		t.Fatalf("source = %+v, want enabled from local snapshot", source)
	}
}

func TestManagerRealtimeGoroutineCategoryWorksWithoutPrometheus(t *testing.T) {
	registry := goruntimeregistry.New()
	provider := &managerRealtimeMonitorProvider{
		base: goroutineMonitorBase{},
		goroutines: &managerGoroutineMonitor{
			localNodeID: 1,
			registry:    registry,
			control: goroutineMonitorControl{nodes: managementusecase.NodeList{Items: []managementusecase.Node{
				{NodeID: 1, Name: "n1", Status: "alive"},
			}}},
		},
	}

	response, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Category: accessmanager.RealtimeMonitorCategoryGoroutines,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if response.Status != accessmanager.RealtimeMonitorStatusReady || response.Goroutines == nil || len(response.Goroutines.Nodes) != 1 {
		t.Fatalf("response = %+v, want ready direct snapshot", response)
	}
	if len(response.Snapshot) != 5 || response.Sources.Goroutines == nil || !response.Sources.Goroutines.Enabled {
		t.Fatalf("summary/source = %+v / %+v, want direct five-value summary", response.Snapshot, response.Sources.Goroutines)
	}
	if tone := summaryEntryTone(response.Snapshot, "goroutineUnmanagedTotal"); tone != accessmanager.RealtimeMonitorToneNormal {
		t.Fatalf("unmanaged summary tone = %q, want normal trend", tone)
	}
}

func TestManagerGoroutineMonitorBoundsFanoutAndCachesSuccessfulReads(t *testing.T) {
	items := make([]managementusecase.Node, 20)
	for i := range items {
		items[i] = managementusecase.Node{NodeID: uint64(i + 1), Name: "node", Status: "alive"}
	}
	remote := &goroutineMonitorBoundedRemote{delay: 10 * time.Millisecond}
	monitor := &managerGoroutineMonitor{
		localNodeID: 1,
		registry:    goruntimeregistry.New(),
		control:     goroutineMonitorControl{nodes: managementusecase.NodeList{Items: items}},
		remote:      remote,
	}

	first, _ := monitor.snapshot(context.Background(), 0)
	if first.Status != accessmanager.RealtimeMonitorStatusReady {
		t.Fatalf("first snapshot status = %s, want ready", first.Status)
	}
	if got := remote.max.Load(); got > managerGoroutineFanoutConcurrency {
		t.Fatalf("max remote fanout = %d, want <= %d", got, managerGoroutineFanoutConcurrency)
	}
	if got := remote.calls.Load(); got != 19 {
		t.Fatalf("remote calls = %d, want 19", got)
	}

	second, _ := monitor.snapshot(context.Background(), 0)
	if second.Status != accessmanager.RealtimeMonitorStatusReady {
		t.Fatalf("second snapshot status = %s, want ready", second.Status)
	}
	if got := remote.calls.Load(); got != 19 {
		t.Fatalf("cached remote calls = %d, want 19", got)
	}
}

func TestManagerGoroutineMonitorHonorsGlobalDeadline(t *testing.T) {
	monitor := &managerGoroutineMonitor{
		localNodeID: 1,
		registry:    goruntimeregistry.New(),
		control: goroutineMonitorControl{nodes: managementusecase.NodeList{Items: []managementusecase.Node{
			{NodeID: 2, Name: "n2", Status: "alive"},
		}}},
		remote: goroutineMonitorBlockingRemote{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	snapshot, _ := monitor.snapshot(ctx, 2)
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("snapshot elapsed = %v, want bounded by caller/global deadline", elapsed)
	}
	if snapshot.Status != accessmanager.RealtimeMonitorStatusPartial || len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Error != "timeout" {
		t.Fatalf("snapshot = %+v, want one timed-out partial node", snapshot)
	}
}

func TestManagerGoroutineMonitorRejectsStaleRemoteSnapshot(t *testing.T) {
	monitor := &managerGoroutineMonitor{
		localNodeID: 1,
		registry:    goruntimeregistry.New(),
		control: goroutineMonitorControl{nodes: managementusecase.NodeList{Items: []managementusecase.Node{
			{NodeID: 2, Name: "n2", Status: "alive"},
		}}},
		remote: goroutineMonitorRemote{snapshots: map[uint64]managementusecase.GoroutineSnapshot{
			2: {GeneratedAt: time.Now().Add(-managerGoroutineStaleAfter - time.Second)},
		}},
	}
	snapshot, _ := monitor.snapshot(context.Background(), 2)
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Error != "stale" || snapshot.Nodes[0].Snapshot != nil {
		t.Fatalf("snapshot = %+v, want stale remote evidence without data", snapshot)
	}
}

func TestManagerGoroutineMonitorPanicDoesNotLeaveStuckInflightRead(t *testing.T) {
	remote := &goroutineMonitorPanicRemote{}
	monitor := &managerGoroutineMonitor{
		localNodeID: 1,
		registry:    goruntimeregistry.New(),
		control: goroutineMonitorControl{nodes: managementusecase.NodeList{Items: []managementusecase.Node{
			{NodeID: 2, Name: "n2", Status: "alive"},
		}}},
		remote: remote,
	}
	for attempt := 0; attempt < 2; attempt++ {
		snapshot, _ := monitor.snapshot(context.Background(), 2)
		if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Error != "unavailable" {
			t.Fatalf("attempt %d snapshot = %+v, want unavailable", attempt, snapshot)
		}
	}
	if calls := remote.calls.Load(); calls != 2 {
		t.Fatalf("remote calls = %d, want 2 independent reads after panic cleanup", calls)
	}
}

func TestManagerGoroutineMonitorEvictsRemovedNodeCache(t *testing.T) {
	monitor := &managerGoroutineMonitor{
		localNodeID: 1,
		registry:    goruntimeregistry.New(),
		control: goroutineMonitorControl{nodes: managementusecase.NodeList{Items: []managementusecase.Node{
			{NodeID: 1, Name: "n1", Status: "alive"},
			{NodeID: 2, Name: "n2", Status: "alive"},
		}}},
		remote: goroutineMonitorRemote{snapshots: map[uint64]managementusecase.GoroutineSnapshot{
			2: {GeneratedAt: time.Now()},
		}},
		cache: map[uint64]managerGoroutineCacheEntry{
			99: {snapshot: managementusecase.GoroutineSnapshot{BootID: "removed"}, readAt: time.Now()},
		},
	}

	monitor.snapshot(context.Background(), 0)
	if _, ok := monitor.cache[99]; ok {
		t.Fatal("removed node 99 remained in manager goroutine cache")
	}
	if len(monitor.cache) > managerGoroutineMaxNodes {
		t.Fatalf("cache size = %d, want <= %d", len(monitor.cache), managerGoroutineMaxNodes)
	}
}

func TestManagerGoroutineReadErrorUsesTypedCapabilityError(t *testing.T) {
	if got := managerGoroutineReadError(clusternet.ErrServiceNotFound); got != "unsupported" {
		t.Fatalf("service-not-found error = %q, want unsupported", got)
	}
	if got := managerGoroutineReadError(clusternet.ErrNodeNotFound); got != "unavailable" {
		t.Fatalf("node-not-found error = %q, want unavailable", got)
	}
}

func TestProjectGoroutineSnapshotPreservesPoolAndBootFields(t *testing.T) {
	projected := projectGoroutineSnapshot(goruntimeregistry.Snapshot{
		BootID:         "boot-2",
		ProcessTotal:   20,
		ManagedTotal:   12,
		UnmanagedTotal: 8,
		Modules: []goruntimeregistry.ModuleSnapshot{{
			Module:        goruntimeregistry.ModuleGateway,
			QueueCapacity: 64,
			Tasks: []goruntimeregistry.TaskSnapshot{{
				Task:          goruntimeregistry.TaskGatewayAsyncAuth,
				Kind:          goruntimeregistry.TaskKindPool,
				QueueDepth:    4,
				QueueCapacity: 64,
				Health:        goruntimeregistry.HealthWarning,
				HealthReason:  "pressure",
			}},
		}},
	})

	if projected.BootID != "boot-2" || projected.ProcessTotal != 20 || projected.UnmanagedTotal != 8 {
		t.Fatalf("projected process fields = %+v", projected)
	}
	if len(projected.Modules) != 1 || projected.Modules[0].QueueCapacity != 64 || len(projected.Modules[0].Tasks) != 1 {
		t.Fatalf("projected modules = %+v", projected.Modules)
	}
	task := projected.Modules[0].Tasks[0]
	if task.Task != string(goruntimeregistry.TaskGatewayAsyncAuth) || task.QueueDepth != 4 || task.QueueCapacity != 64 ||
		task.Health != goruntimeregistry.HealthWarning || task.HealthReason != "pressure" {
		t.Fatalf("projected task = %+v", task)
	}
}

func summaryEntryTone(entries []accessmanager.RealtimeMonitorSnapshotEntry, key string) string {
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Tone
		}
	}
	return ""
}
