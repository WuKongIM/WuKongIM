package cluster

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
)

func TestControllerHostStartElectsSingleLocalPeer(t *testing.T) {
	cfg := validTestConfig()
	cfg.ControllerReplicaN = 1
	cfg.Nodes = []NodeConfig{{NodeID: cfg.NodeID, Addr: "127.0.0.1:0"}}
	cfg.ControllerMetaPath = filepath.Join(t.TempDir(), "controller-meta")
	cfg.ControllerRaftPath = filepath.Join(t.TempDir(), "controller-raft")

	discovery := NewStaticDiscovery(cfg.Nodes)
	layer := newTransportLayer(cfg, discovery, nil)
	requireNoErr(t, layer.Start(
		"127.0.0.1:0",
		func([]byte) {},
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
	))
	t.Cleanup(layer.Stop)

	cfg.Nodes[0].Addr = layer.server.Listener().Addr().String()
	host, err := newControllerHost(cfg, layer)
	if err != nil {
		t.Fatalf("newControllerHost() error = %v", err)
	}
	if host.observations == nil {
		t.Fatal("newControllerHost() observations = nil")
	}
	requireNoErr(t, host.Start(context.Background()))
	t.Cleanup(host.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for host.LeaderID() != cfg.NodeID && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if host.LeaderID() != cfg.NodeID {
		t.Fatalf("controllerHost.LeaderID() = %d, want %d", host.LeaderID(), cfg.NodeID)
	}
	if !host.IsLeader(cfg.NodeID) {
		t.Fatal("controllerHost.IsLeader() = false, want true")
	}
}

func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestControllerHostHashSlotSnapshotReloadsOnLocalLeaderChange(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)

	table := NewHashSlotTable(8, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), table))

	host.handleLeaderChange(2, host.localNode)

	snapshot := waitForHashSlotTableSnapshot(t, host, time.Second)
	if snapshot.Version() != table.Version() {
		t.Fatalf("hashSlotTableSnapshot().Version() = %d, want %d", snapshot.Version(), table.Version())
	}
}

func TestControllerHostHashSlotSnapshotClearsOnLeaderLoss(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)

	host.storeHashSlotTableSnapshot(NewHashSlotTable(8, 2))
	if _, ok := host.hashSlotTableSnapshot(); !ok {
		t.Fatal("hashSlotTableSnapshot() ok = false before leader loss, want true")
	}

	host.handleLeaderChange(host.localNode, 2)

	if _, ok := host.hashSlotTableSnapshot(); ok {
		t.Fatal("hashSlotTableSnapshot() ok = true after leader loss, want false")
	}
}

func TestControllerHostHandleCommittedCommandReloadsHashSlotSnapshotOnHashSlotMutation(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)

	initial := NewHashSlotTable(8, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), initial))
	host.handleLeaderChange(2, host.localNode)
	waitForHashSlotTableSnapshot(t, host, time.Second)

	updated := initial.Clone()
	updated.StartMigration(3, 1, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), updated))

	host.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindStartMigration,
		Migration: &slotcontroller.MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
		},
	})

	snapshot := waitForHashSlotTableSnapshot(t, host, time.Second)
	if snapshot.Version() != updated.Version() {
		t.Fatalf("hashSlotTableSnapshot().Version() = %d, want %d", snapshot.Version(), updated.Version())
	}
	if migration := snapshot.GetMigration(3); migration == nil {
		t.Fatal("hashSlotTableSnapshot().GetMigration(3) = nil, want active migration")
	}
}

func TestControllerHostLeaderChangeReloadsNodeMirrorOnLocalLeadership(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710000000, 0),
		CapacityWeight:  1,
	}))

	host.handleLeaderChange(2, host.localNode)

	node := waitForMirroredNode(t, host, 1, time.Second)
	if node.Status != controllermeta.NodeStatusAlive {
		t.Fatalf("mirroredNode(1).Status = %v, want alive", node.Status)
	}
}

func TestControllerHostLeaderChangeReloadsNodeMirrorAndRearmsHealthDeadlines(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	now := time.Unix(1710000000, 0)
	var timers []*recordingHealthTimer
	host.healthScheduler.cfg.now = func() time.Time { return now }
	host.healthScheduler.cfg.afterFunc = func(delay time.Duration, fn func()) healthTimer {
		timer := &recordingHealthTimer{delay: delay, fn: fn}
		timers = append(timers, timer)
		return timer
	}
	setControllerHostNodeMirrorLoadFn(host, func(context.Context) ([]controllermeta.ClusterNode, error) {
		return []controllermeta.ClusterNode{{
			NodeID:          7,
			Addr:            "127.0.0.1:7007",
			Status:          controllermeta.NodeStatusAlive,
			LastHeartbeatAt: now.Add(-9 * time.Second),
			CapacityWeight:  1,
		}}, nil
	})

	host.handleLeaderChange(2, host.localNode)

	deadline := time.Now().Add(time.Second)
	for len(timers) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(timers); got != 2 {
		t.Fatalf("warmup health timers = %d, want 2", got)
	}

	var sawImmediateSuspect bool
	var sawDeadAfterOneSecond bool
	for _, timer := range timers {
		if timer.delay <= 0 {
			sawImmediateSuspect = true
		}
		if timer.delay == time.Second {
			sawDeadAfterOneSecond = true
		}
	}
	if !sawImmediateSuspect {
		t.Fatal("warmup health timers missing immediate suspect deadline")
	}
	if !sawDeadAfterOneSecond {
		t.Fatal("warmup health timers missing dead deadline rearm")
	}
}

func TestControllerHostLeaderAcquireClearsHashSlotSnapshotBeforeWarmup(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)

	oldTable := NewHashSlotTable(8, 2)
	host.storeHashSlotTableSnapshot(oldTable)
	if _, ok := host.hashSlotTableSnapshot(); !ok {
		t.Fatal("hashSlotTableSnapshot() ok = false before leader acquire, want true")
	}

	newTable := NewHashSlotTable(8, 2)
	newTable.StartMigration(3, 1, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), newTable))

	started := make(chan struct{})
	release := make(chan struct{})
	setControllerHostHashSlotLoadFn(host, func(ctx context.Context) (*HashSlotTable, error) {
		close(started)
		<-release
		return host.meta.LoadHashSlotTable(ctx)
	})

	host.handleLeaderChange(2, host.localNode)

	if _, ok := host.hashSlotTableSnapshot(); ok {
		t.Fatal("hashSlotTableSnapshot() ok = true immediately after leader acquire, want false")
	}

	<-started
	close(release)

	snapshot := waitForHashSlotTableSnapshot(t, host, time.Second)
	if snapshot.Version() != newTable.Version() {
		t.Fatalf("hashSlotTableSnapshot().Version() = %d, want %d", snapshot.Version(), newTable.Version())
	}
}

func TestControllerHostStaleWarmupDoesNotRepopulateAfterLeaderLoss(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)

	table := NewHashSlotTable(8, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), table))
	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710000000, 0),
		CapacityWeight:  1,
	}))

	hashStarted := make(chan struct{})
	hashRelease := make(chan struct{})
	setControllerHostHashSlotLoadFn(host, func(ctx context.Context) (*HashSlotTable, error) {
		close(hashStarted)
		<-hashRelease
		return host.meta.LoadHashSlotTable(ctx)
	})
	nodeStarted := make(chan struct{})
	nodeRelease := make(chan struct{})
	setControllerHostNodeMirrorLoadFn(host, func(ctx context.Context) ([]controllermeta.ClusterNode, error) {
		close(nodeStarted)
		<-nodeRelease
		return host.meta.ListNodes(ctx)
	})

	host.handleLeaderChange(2, host.localNode)

	<-hashStarted
	<-nodeStarted

	// Lose leadership before warmups can apply.
	host.handleLeaderChange(host.localNode, 2)

	close(hashRelease)
	close(nodeRelease)

	// Give the warmup goroutines a chance to run to completion if they are buggy.
	time.Sleep(100 * time.Millisecond)

	if _, ok := host.hashSlotTableSnapshot(); ok {
		t.Fatal("hashSlotTableSnapshot() ok = true after leader loss, want false")
	}
	if _, ok := host.healthScheduler.mirroredNode(1); ok {
		t.Fatal("mirroredNode(1) ok = true after leader loss, want false")
	}
}

func TestControllerHostCommittedCommandSchedulesHashSlotReloadAsync(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)

	initial := NewHashSlotTable(8, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), initial))
	host.storeHashSlotTableSnapshot(initial)

	host.handleLeaderChange(2, host.localNode)

	updated := initial.Clone()
	updated.StartMigration(3, 1, 2)
	requireNoErr(t, host.meta.SaveHashSlotTable(context.Background(), updated))

	started := make(chan struct{})
	release := make(chan struct{})
	setControllerHostHashSlotLoadFn(host, func(ctx context.Context) (*HashSlotTable, error) {
		close(started)
		<-release
		return host.meta.LoadHashSlotTable(ctx)
	})

	done := make(chan struct{})
	go func() {
		host.handleCommittedCommand(slotcontroller.Command{
			Kind: slotcontroller.CommandKindStartMigration,
			Migration: &slotcontroller.MigrationRequest{
				HashSlot: 3,
				Source:   1,
				Target:   2,
			},
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleCommittedCommand blocked; hash slot reload must be async")
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("hash slot reload was not scheduled")
	}

	// Stale snapshot must not be served while reload is pending.
	if _, ok := host.hashSlotTableSnapshot(); ok {
		t.Fatal("hashSlotTableSnapshot() ok = true while reload is pending, want false")
	}

	close(release)

	snapshot := waitForHashSlotTableSnapshot(t, host, time.Second)
	if snapshot.Version() != updated.Version() {
		t.Fatalf("hashSlotTableSnapshot().Version() = %d, want %d", snapshot.Version(), updated.Version())
	}
}

func TestControllerHostLeaderChangeClearsNodeMirrorOnLeaderLoss(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	host.healthScheduler.mirrorNode(controllermeta.ClusterNode{
		NodeID:         1,
		Addr:           "127.0.0.1:7001",
		Status:         controllermeta.NodeStatusAlive,
		CapacityWeight: 1,
	})

	host.handleLeaderChange(host.localNode, 2)

	if _, ok := host.healthScheduler.mirroredNode(1); ok {
		t.Fatal("mirroredNode(1) ok = true after leader loss, want false")
	}
}

func TestControllerHostWarmupRequiresRuntimeFullSync(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, true)
	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710001000, 0),
		CapacityWeight:  1,
	}))
	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          2,
		Addr:            "127.0.0.1:7002",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710001000, 0),
		CapacityWeight:  1,
	}))

	if host.warmupComplete() {
		t.Fatal("warmupComplete() = true before runtime full sync, want false")
	}

	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     1,
		ObservedAt: time.Unix(1710001001, 0),
		FullSync:   true,
	})
	if host.warmupComplete() {
		t.Fatal("warmupComplete() = true after only one node full sync, want false")
	}

	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     2,
		ObservedAt: time.Unix(1710001002, 0),
		FullSync:   true,
	})
	if !host.warmupComplete() {
		t.Fatal("warmupComplete() = false after all alive nodes full sync, want true")
	}
}

func TestControllerHostLeaderChangeResetsRuntimeWarmupCoverage(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, true)
	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710001100, 0),
		CapacityWeight:  1,
	}))

	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     1,
		ObservedAt: time.Unix(1710001101, 0),
		FullSync:   true,
	})
	if !host.warmupComplete() {
		t.Fatal("warmupComplete() = false after local full sync, want true")
	}

	host.handleLeaderChange(host.localNode, 2)
	host.handleLeaderChange(2, host.localNode)
	if host.warmupComplete() {
		t.Fatal("warmupComplete() = true after leader change reset, want false")
	}

	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     1,
		ObservedAt: time.Unix(1710001102, 0),
		FullSync:   true,
	})
	if !host.warmupComplete() {
		t.Fatal("warmupComplete() = false after post-reset full sync, want true")
	}
}

func TestControllerHostMetadataSnapshotReloadsOnLocalLeaderChange(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	if _, ok := host.metadataSnapshot(); ok {
		t.Fatal("metadataSnapshot() ok = true before local leadership, want false")
	}

	host.handleLeaderChange(2, host.localNode)

	// Reload is async; readers fall back until warm-up completes.
	waitForControllerMetadataSnapshotClean(t, host, time.Second)

	snapshot, ok := host.metadataSnapshot()
	if !ok {
		t.Fatal("metadataSnapshot() ok = false after local leadership, want true")
	}
	if !snapshot.Ready {
		t.Fatal("metadataSnapshot().Ready = false after reload, want true")
	}
	if snapshot.Dirty {
		t.Fatal("metadataSnapshot().Dirty = true after reload, want false")
	}
	if snapshot.LeaderID != host.localNode {
		t.Fatalf("metadataSnapshot().LeaderID = %d, want %d", snapshot.LeaderID, host.localNode)
	}
	if snapshot.Generation != 1 {
		t.Fatalf("metadataSnapshot().Generation = %d, want 1", snapshot.Generation)
	}
	if len(snapshot.Nodes) != 1 || snapshot.NodesByID[1].NodeID != 1 {
		t.Fatalf("metadataSnapshot() nodes not loaded, got %+v", snapshot.Nodes)
	}
	if len(snapshot.Assignments) != 1 || snapshot.AssignmentsBySlot[1].SlotID != 1 {
		t.Fatalf("metadataSnapshot() assignments not loaded, got %+v", snapshot.Assignments)
	}
	if len(snapshot.Tasks) != 1 || snapshot.TasksBySlot[1].SlotID != 1 {
		t.Fatalf("metadataSnapshot() tasks not loaded, got %+v", snapshot.Tasks)
	}

	// Losing and re-acquiring local leadership must invalidate the previous leader generation.
	host.handleLeaderChange(host.localNode, 2)
	host.handleLeaderChange(2, host.localNode)

	snapshot2 := peekControllerMetadataSnapshot(host)
	if snapshot2.Generation != 2 {
		t.Fatalf("metadataSnapshotPeek().Generation = %d after leader reacquire, want 2", snapshot2.Generation)
	}
}

func TestControllerHostMetadataSnapshotReloadUpdatesObservationSyncState(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)

	if host.syncState == nil {
		t.Fatal("controllerHost.syncState = nil, want initialized")
	}
	if got, want := host.syncState.currentRevisions(), (observationRevisions{
		Assignments: 1,
		Tasks:       1,
		Nodes:       1,
	}); got != want {
		t.Fatalf("syncState.currentRevisions() = %+v, want %+v", got, want)
	}

	resp := host.syncState.buildDelta(observationDeltaRequest{ForceFullSync: true})
	if !resp.FullSync {
		t.Fatal("buildDelta(...).FullSync = false, want true")
	}
	if got, want := len(resp.Assignments), 1; got != want {
		t.Fatalf("len(resp.Assignments) = %d, want %d", got, want)
	}
	if got, want := len(resp.Tasks), 1; got != want {
		t.Fatalf("len(resp.Tasks) = %d, want %d", got, want)
	}
	if got, want := len(resp.Nodes), 1; got != want {
		t.Fatalf("len(resp.Nodes) = %d, want %d", got, want)
	}
}

func TestControllerHostApplyRuntimeReportUpdatesObservationSyncState(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	host.handleLeaderChange(2, host.localNode)

	host.applyRuntimeReport(runtimeObservationReport{
		NodeID:     1,
		ObservedAt: time.Unix(1710001200, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{
			testObservationRuntimeView(1, 1, []uint64{1, 2, 3}, 1, time.Unix(1710001200, 0)),
		},
	})

	if host.syncState == nil {
		t.Fatal("controllerHost.syncState = nil, want initialized")
	}
	if got, want := host.syncState.currentRevisions().Runtime, uint64(1); got != want {
		t.Fatalf("syncState.currentRevisions().Runtime = %d, want %d", got, want)
	}

	resp := host.syncState.buildDelta(observationDeltaRequest{ForceFullSync: true})
	if got, want := len(resp.RuntimeViews), 1; got != want {
		t.Fatalf("len(resp.RuntimeViews) = %d, want %d", got, want)
	}
	if got, want := resp.RuntimeViews[0].LeaderID, uint64(1); got != want {
		t.Fatalf("resp.RuntimeViews[0].LeaderID = %d, want %d", got, want)
	}
}

func TestControllerHostMetadataSnapshotLeaderChangeCallbackIsNonBlocking(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	started := make(chan struct{})
	release := make(chan struct{})
	setControllerMetadataSnapshotLoadFn(host, func(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error) {
		close(started)
		<-release
		return loadControllerMetadataSnapshot(ctx, store)
	})
	t.Cleanup(func() { setControllerMetadataSnapshotLoadFn(host, nil) })
	t.Cleanup(func() { close(release) })

	done := make(chan struct{})
	go func() {
		host.handleLeaderChange(2, host.localNode)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleLeaderChange blocked; leader-change callback must be non-blocking")
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata snapshot reload was not started after local leader change")
	}
}

func TestControllerHostMetadataSnapshotClearsOnLeaderLoss(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)
	if _, ok := host.metadataSnapshot(); !ok {
		t.Fatal("metadataSnapshot() ok = false before leader loss, want true")
	}

	host.handleLeaderChange(host.localNode, 2)

	if _, ok := host.metadataSnapshot(); ok {
		t.Fatal("metadataSnapshot() ok = true after leader loss, want false")
	}
	peek := peekControllerMetadataSnapshot(host)
	if peek.Ready {
		t.Fatal("metadataSnapshotPeek().Ready = true after leader loss, want false")
	}
	if peek.Dirty {
		t.Fatal("metadataSnapshotPeek().Dirty = true after leader loss, want false")
	}
}

func TestControllerHostHandleCommittedCommandMarksMetadataSnapshotDirty(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)
	if _, ok := host.metadataSnapshot(); !ok {
		t.Fatal("metadataSnapshot() ok = false before commit, want true")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	setControllerMetadataSnapshotLoadFn(host, func(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error) {
		close(started)
		<-release
		return loadControllerMetadataSnapshot(ctx, store)
	})
	t.Cleanup(func() { setControllerMetadataSnapshotLoadFn(host, nil) })

	done := make(chan struct{})
	go func() {
		host.handleCommittedCommand(slotcontroller.Command{
			Kind: slotcontroller.CommandKindOperatorRequest,
			Op: &slotcontroller.OperatorRequest{
				Kind:   slotcontroller.OperatorResumeNode,
				NodeID: 1,
			},
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleCommittedCommand blocked; metadata snapshot reload must be async")
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata snapshot reload was not scheduled after dirty mark")
	}

	peek := peekControllerMetadataSnapshot(host)
	if !peek.Ready || !peek.Dirty {
		t.Fatalf("metadataSnapshot state = (Ready=%v Dirty=%v), want (true true)", peek.Ready, peek.Dirty)
	}
	if _, ok := host.metadataSnapshot(); ok {
		t.Fatal("metadataSnapshot() ok = true while dirty, want false")
	}

	close(release)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)
}

func TestControllerHostMetadataSnapshotAsyncReloadUsesTimeoutContext(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)
	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)

	started := make(chan struct{})
	done := make(chan struct{})
	var gotDeadline atomic.Uint32
	setControllerMetadataSnapshotLoadFn(host, func(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error) {
		_, ok := ctx.Deadline()
		if ok {
			gotDeadline.Store(1)
		}
		close(started)
		<-done
		return controllerMetadataSnapshot{}, context.Canceled
	})
	t.Cleanup(func() { setControllerMetadataSnapshotLoadFn(host, nil) })
	t.Cleanup(func() { close(done) })

	host.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindOperatorRequest,
		Op: &slotcontroller.OperatorRequest{
			Kind:   slotcontroller.OperatorResumeNode,
			NodeID: 1,
		},
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata snapshot reload did not start")
	}
	if gotDeadline.Load() == 0 {
		t.Fatal("metadata snapshot reload used an unbounded context without deadline")
	}
}

func TestControllerHostMetadataSnapshotAsyncReloadCanceledOnStop(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)
	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)

	started := make(chan struct{})
	release := make(chan struct{})
	var sawCancel atomic.Uint32
	setControllerMetadataSnapshotLoadFn(host, func(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error) {
		close(started)
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				sawCancel.Store(1)
			}
			return controllerMetadataSnapshot{}, ctx.Err()
		case <-release:
			return controllerMetadataSnapshot{}, context.Canceled
		}
	})
	t.Cleanup(func() { setControllerMetadataSnapshotLoadFn(host, nil) })
	t.Cleanup(func() { close(release) })

	host.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindOperatorRequest,
		Op: &slotcontroller.OperatorRequest{
			Kind:   slotcontroller.OperatorResumeNode,
			NodeID: 1,
		},
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata snapshot reload did not start")
	}

	host.Stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && sawCancel.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if sawCancel.Load() == 0 {
		t.Fatal("metadata snapshot reload context was not canceled on host.Stop()")
	}
}

func TestControllerHostMetadataSnapshotHeartbeatMarksDirtyWithoutReload(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)

	started := make(chan struct{})
	setControllerMetadataSnapshotLoadFn(host, func(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error) {
		close(started)
		return loadControllerMetadataSnapshot(ctx, store)
	})
	t.Cleanup(func() { setControllerMetadataSnapshotLoadFn(host, nil) })

	host.handleCommittedCommand(slotcontroller.Command{Kind: slotcontroller.CommandKindNodeHeartbeat})

	select {
	case <-started:
		t.Fatal("metadata snapshot reload was scheduled for node heartbeat; want dirty-only to avoid churn")
	case <-time.After(200 * time.Millisecond):
	}

	peek := peekControllerMetadataSnapshot(host)
	if !peek.Ready || !peek.Dirty {
		t.Fatalf("metadataSnapshot state after heartbeat = (Ready=%v Dirty=%v), want (true true)", peek.Ready, peek.Dirty)
	}
	if _, ok := host.metadataSnapshot(); ok {
		t.Fatal("metadataSnapshot() ok = true after heartbeat dirty, want false")
	}
}

func TestControllerHostMetadataSnapshotEvaluateTimeoutsMarksDirtyWithoutReload(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)

	started := make(chan struct{})
	setControllerMetadataSnapshotLoadFn(host, func(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error) {
		close(started)
		return loadControllerMetadataSnapshot(ctx, store)
	})
	t.Cleanup(func() { setControllerMetadataSnapshotLoadFn(host, nil) })

	host.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindEvaluateTimeouts,
		Advance: &slotcontroller.TaskAdvance{
			SlotID:  1,
			Attempt: 1,
			Now:     time.Unix(1710000002, 0),
		},
	})

	select {
	case <-started:
		t.Fatal("metadata snapshot reload was scheduled for evaluate timeouts; want dirty-only to avoid churn")
	case <-time.After(200 * time.Millisecond):
	}

	peek := peekControllerMetadataSnapshot(host)
	if !peek.Ready || !peek.Dirty {
		t.Fatalf("metadataSnapshot state after evaluate timeouts = (Ready=%v Dirty=%v), want (true true)", peek.Ready, peek.Dirty)
	}
	if _, ok := host.metadataSnapshot(); ok {
		t.Fatal("metadataSnapshot() ok = true after evaluate timeouts dirty, want false")
	}
}

func TestControllerHostMetadataSnapshotReloadFailureLeavesDirty(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)
	if _, ok := host.metadataSnapshot(); !ok {
		t.Fatal("metadataSnapshot() ok = false before reload failure, want true")
	}

	started := make(chan struct{})
	setControllerMetadataSnapshotLoadFn(host, func(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error) {
		close(started)
		return controllerMetadataSnapshot{}, context.Canceled
	})
	t.Cleanup(func() { setControllerMetadataSnapshotLoadFn(host, nil) })

	host.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindOperatorRequest,
		Op: &slotcontroller.OperatorRequest{
			Kind:   slotcontroller.OperatorResumeNode,
			NodeID: 1,
		},
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata snapshot reload was not scheduled")
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, ok := host.metadataSnapshot(); ok {
			t.Fatal("metadataSnapshot() ok = true after reload failure, want false")
		}
		time.Sleep(10 * time.Millisecond)
	}
	peek := peekControllerMetadataSnapshot(host)
	if !peek.Dirty {
		t.Fatal("metadata snapshot should remain dirty after reload failure")
	}
}

func TestControllerHostMetadataSnapshotReloadRestoresReadyState(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)

	updated := controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710000001, 0),
		CapacityWeight:  2,
	}
	requireNoErr(t, host.meta.UpsertNode(context.Background(), updated))

	// Mark dirty and rely on the coalesced async reload to restore readiness.
	host.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindOperatorRequest,
		Op: &slotcontroller.OperatorRequest{
			Kind:   slotcontroller.OperatorResumeNode,
			NodeID: 1,
		},
	})
	waitForControllerMetadataSnapshotClean(t, host, time.Second)

	snapshot, ok := host.metadataSnapshot()
	if !ok {
		t.Fatal("metadataSnapshot() ok = false after async reload, want true")
	}
	if got := snapshot.NodesByID[1].CapacityWeight; got != 2 {
		t.Fatalf("metadataSnapshot().NodesByID[1].CapacityWeight = %d, want 2", got)
	}
}

func TestControllerHostMetadataSnapshotCloneIsDeepReadOnly(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)

	snapshot, ok := host.metadataSnapshot()
	if !ok {
		t.Fatal("metadataSnapshot() ok = false, want true")
	}
	if len(snapshot.Assignments) != 1 {
		t.Fatalf("metadataSnapshot().Assignments len = %d, want 1", len(snapshot.Assignments))
	}
	if got := snapshot.Assignments[0].DesiredPeers[0]; got != 1 {
		t.Fatalf("metadataSnapshot().Assignments[0].DesiredPeers[0] = %d, want 1", got)
	}

	// Mutate the returned snapshot and ensure it cannot corrupt the cached snapshot state.
	snapshot.Assignments[0].DesiredPeers[0] = 999

	snapshot2, ok := host.metadataSnapshot()
	if !ok {
		t.Fatal("metadataSnapshot() ok = false after mutation, want true")
	}
	if got := snapshot2.AssignmentsBySlot[1].DesiredPeers[0]; got != 1 {
		t.Fatalf("metadataSnapshot() DesiredPeers aliasing detected: got %d, want 1", got)
	}
}

func TestControllerHostMetadataSnapshotMarkDirtyDuringReloadIsPreserved(t *testing.T) {
	_, host, _ := newTestLocalControllerCluster(t, false)
	seedControllerMetaForSnapshot(t, host)

	host.handleLeaderChange(2, host.localNode)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)
	if _, ok := host.metadataSnapshot(); !ok {
		t.Fatal("metadataSnapshot() ok = false before reload, want true")
	}

	var calls uint32
	started1 := make(chan struct{})
	release1 := make(chan struct{})
	started2 := make(chan struct{})
	release2 := make(chan struct{})
	setControllerMetadataSnapshotLoadFn(host, func(ctx context.Context, store *controllermeta.Store) (controllerMetadataSnapshot, error) {
		n := atomic.AddUint32(&calls, 1)
		switch n {
		case 1:
			close(started1)
			<-release1
		case 2:
			close(started2)
			<-release2
		}
		return loadControllerMetadataSnapshot(ctx, store)
	})
	t.Cleanup(func() { setControllerMetadataSnapshotLoadFn(host, nil) })

	reloadDone := make(chan error, 1)
	go func() {
		reloadDone <- host.reloadMetadataSnapshot(context.Background())
	}()

	select {
	case <-started1:
	case <-time.After(time.Second):
		t.Fatal("manual metadata snapshot reload did not start")
	}
	host.handleCommittedCommand(slotcontroller.Command{
		Kind: slotcontroller.CommandKindOperatorRequest,
		Op: &slotcontroller.OperatorRequest{
			Kind:   slotcontroller.OperatorResumeNode,
			NodeID: 1,
		},
	})
	close(release1)

	requireNoErr(t, <-reloadDone)

	select {
	case <-started2:
	case <-time.After(time.Second):
		t.Fatal("second metadata snapshot reload was not scheduled after dirty during reload")
	}
	peek := peekControllerMetadataSnapshot(host)
	if !peek.Ready || !peek.Dirty {
		t.Fatalf("metadataSnapshot state after reload = (Ready=%v Dirty=%v), want (true true)", peek.Ready, peek.Dirty)
	}
	close(release2)
	waitForControllerMetadataSnapshotClean(t, host, time.Second)
}

func seedControllerMetaForSnapshot(t *testing.T, host *controllerHost) {
	t.Helper()
	requireNoErr(t, host.meta.UpsertNode(context.Background(), controllermeta.ClusterNode{
		NodeID:          1,
		Addr:            "127.0.0.1:7001",
		Status:          controllermeta.NodeStatusAlive,
		LastHeartbeatAt: time.Unix(1710000000, 0),
		CapacityWeight:  1,
	}))
	requireNoErr(t, host.meta.UpsertAssignment(context.Background(), controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}))
	requireNoErr(t, host.meta.UpsertTask(context.Background(), controllermeta.ReconcileTask{
		SlotID:  1,
		Kind:    controllermeta.TaskKindBootstrap,
		Step:    controllermeta.TaskStepAddLearner,
		Status:  controllermeta.TaskStatusPending,
		Attempt: 1,
	}))
}

func peekControllerMetadataSnapshot(host *controllerHost) controllerMetadataSnapshot {
	if host == nil {
		return controllerMetadataSnapshot{}
	}
	host.metadataSnapshotState.mu.RLock()
	defer host.metadataSnapshotState.mu.RUnlock()
	return host.metadataSnapshotState.snapshot.clone()
}

func setControllerMetadataSnapshotLoadFn(host *controllerHost, fn func(context.Context, *controllermeta.Store) (controllerMetadataSnapshot, error)) {
	if host == nil {
		return
	}
	host.metadataSnapshotState.mu.Lock()
	defer host.metadataSnapshotState.mu.Unlock()
	host.metadataSnapshotState.loadFn = fn
}

func waitForControllerMetadataSnapshotClean(t *testing.T, host *controllerHost, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, ok := host.metadataSnapshot(); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("controller metadata snapshot did not become clean before timeout")
}

func waitForHashSlotTableSnapshot(t *testing.T, host *controllerHost, timeout time.Duration) *HashSlotTable {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if table, ok := host.hashSlotTableSnapshot(); ok {
			return table
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("hash slot snapshot did not become ready before timeout")
	return nil
}

func waitForMirroredNode(t *testing.T, host *controllerHost, nodeID uint64, timeout time.Duration) controllermeta.ClusterNode {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if node, ok := host.healthScheduler.mirroredNode(nodeID); ok {
			return node
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("mirroredNode(%d) did not become ready before timeout", nodeID)
	return controllermeta.ClusterNode{}
}

func setControllerHostHashSlotLoadFn(host *controllerHost, fn func(context.Context) (*HashSlotTable, error)) {
	if host == nil {
		return
	}
	host.warmupHooksMu.Lock()
	defer host.warmupHooksMu.Unlock()
	host.loadHashSlotTableFn = fn
}

func setControllerHostNodeMirrorLoadFn(host *controllerHost, fn func(context.Context) ([]controllermeta.ClusterNode, error)) {
	if host == nil {
		return
	}
	host.warmupHooksMu.Lock()
	defer host.warmupHooksMu.Unlock()
	host.loadNodeMirrorFn = fn
}
