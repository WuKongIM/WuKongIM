package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	clustertasks "github.com/WuKongIM/WuKongIM/pkg/cluster/tasks"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
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

func TestSeedJoinJoiningMirrorDoesNotInstallPreferredSlotLeaders(t *testing.T) {
	node := seedJoinMirrorRouteNodeForTest(4, &fakeSlotStatusCaller{statuses: []routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 9}}})
	node.started.Store(true)
	snapshot := nodeControlSnapshot()
	snapshot.Nodes = append(snapshot.Nodes, control.Node{
		NodeID:    4,
		Addr:      "127.0.0.1:1004",
		Roles:     []control.Role{control.RoleData},
		JoinState: control.NodeJoinStateJoining,
	})

	if err := node.applySnapshot(context.Background(), snapshot); err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
	if _, err := node.RouteHashSlot(0); !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteHashSlot() error = %v, want ErrNoSlotLeader while joining", err)
	}
}

func TestSeedJoinActiveMirrorInstallsRemoteObservedSlotLeaders(t *testing.T) {
	node := seedJoinMirrorRouteNodeForTest(4, &fakeSlotStatusCaller{statuses: []routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 9}}})
	node.started.Store(true)
	snapshot := nodeControlSnapshot()
	snapshot.Nodes = append(snapshot.Nodes, control.Node{
		NodeID:    4,
		Addr:      "127.0.0.1:1004",
		Roles:     []control.Role{control.RoleData},
		JoinState: control.NodeJoinStateActive,
	})

	if err := node.applySnapshot(context.Background(), snapshot); err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
	route, err := node.RouteHashSlot(0)
	if err != nil {
		t.Fatalf("RouteHashSlot() error = %v", err)
	}
	if route.Leader != 2 || route.LeaderTerm != 9 || route.PreferredLeader != 1 || route.SlotID != 1 {
		t.Fatalf("route = %#v, want remote observed leader 2 term 9 on slot 1", route)
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

func seedJoinMirrorRouteNodeForTest(nodeID uint64, caller clusternet.Caller) *Node {
	return &Node{
		cfg: Config{
			NodeID: nodeID,
			Join: JoinConfig{
				Seeds:         []string{"127.0.0.1:1001"},
				AdvertiseAddr: "127.0.0.1:1004",
				Token:         "join-secret",
			},
		},
		router:               routing.NewRouter(),
		slotStatusCaller:     caller,
		routeAuthorityEpochs: map[uint16]uint64{},
	}
}

type fakeSlotStatusCaller struct {
	statuses []routing.SlotStatus
}

func (f *fakeSlotStatusCaller) Call(_ context.Context, _ uint64, serviceID uint8, _ []byte) ([]byte, error) {
	if serviceID != clusternet.RPCSlotStatus {
		return nil, fmt.Errorf("unexpected service id %d", serviceID)
	}
	return encodeSlotStatusResponse(f.statuses)
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
	if table := node.router.Table(); table == nil || table.Revision != 2 {
		t.Fatalf("route table = %#v, want node-only control update to advance revision to 2", table)
	}
	if calls := reconciler.Calls(); calls != 1 {
		t.Fatalf("reconciler calls = %d, want node-only change to skip slot reconcile", calls)
	}
}

func TestNodeAppliesActiveDataNodesForChannelPlacement(t *testing.T) {
	snapshot := nodeControlSnapshot()
	markFreshAliveReady(snapshot.Nodes)
	controller := control.NewStaticController(snapshot)
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

	next := snapshot.Clone()
	next.Revision = 2
	next.Nodes = append(next.Nodes,
		healthNode(4, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(5, control.NodeJoinStateJoining, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(6, control.NodeJoinStateLeaving, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(7, control.NodeJoinStateRemoved, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(8, control.NodeJoinStateActive, control.NodeSuspect, control.NodeHealthFresh, true),
	)
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool {
		got = node.channelDataNodes.DataNodes()
		return equalUint64s(got, []uint64{1, 2, 3, 4})
	})
}

func TestActiveDataNodeIDsExcludeLeavingAndRemovedNodes(t *testing.T) {
	got := activeDataNodeIDs([]control.Node{
		healthNode(1, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(2, control.NodeJoinStateJoining, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(3, control.NodeJoinStateLeaving, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(4, control.NodeJoinStateRemoved, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(5, control.NodeJoinStateActive, control.NodeSuspect, control.NodeHealthFresh, true),
		{NodeID: 6, Roles: []control.Role{control.RoleController}, Status: control.NodeAlive, Health: control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh, RuntimeReady: true}, JoinState: control.NodeJoinStateActive},
	})
	want := []uint64{1}
	if !equalUint64s(got, want) {
		t.Fatalf("activeDataNodeIDs() = %v, want %v", got, want)
	}
}

func TestActiveDataNodeIDsRequireFreshAliveHealth(t *testing.T) {
	got := activeDataNodeIDs([]control.Node{
		healthNode(6, control.NodeJoinStateActive, control.NodeSuspect, control.NodeHealthFresh, true),
		healthNode(3, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthStale, true),
		healthNode(8, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthFresh, false),
		healthNode(1, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(5, control.NodeJoinStateLeaving, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(7, control.NodeJoinStateActive, control.NodeAlive, control.NodeHealthMissing, true),
		healthNode(4, control.NodeJoinStateJoining, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(2, control.NodeJoinStateRemoved, control.NodeAlive, control.NodeHealthFresh, true),
		healthNode(9, control.NodeJoinStateActive, control.NodeDown, control.NodeHealthFresh, true),
	})
	want := []uint64{1}
	if !equalUint64s(got, want) {
		t.Fatalf("activeDataNodeIDs() = %v, want %v", got, want)
	}
}

func healthNode(nodeID uint64, joinState control.NodeJoinState, status control.NodeStatus, freshness control.NodeHealthFreshness, ready bool) control.Node {
	return control.Node{
		NodeID:    nodeID,
		Addr:      fmt.Sprintf("127.0.0.1:%d", 1000+nodeID),
		Roles:     []control.Role{control.RoleData},
		Status:    status,
		Health:    control.NodeHealth{Status: status, Freshness: freshness, RuntimeReady: ready},
		JoinState: joinState,
	}
}

func markFreshAliveReady(nodes []control.Node) {
	for i := range nodes {
		nodes[i].Health = control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh, RuntimeReady: true}
	}
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

func TestNodeStartRetriesTaskReconcileAfterRetryableControlWrite(t *testing.T) {
	snapshot := nodeControlSnapshot()
	snapshot.Tasks = []control.ReconcileTask{{
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
	controlSource := control.NewStaticController(snapshot)
	executor := &flakyTaskExecutor{errs: []error{controller.ErrNotLeader}}
	cfg := validNodeConfig(t)
	cfg.Slots.TickInterval = 10 * time.Millisecond
	node, err := New(cfg, withController(controlSource), withSlotReconciler(&recordingReconciler{}), withTaskExecutor(executor))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v, want retryable task write to stay background", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	waitUntil(t, func() bool {
		return executor.Calls() >= 2
	})
}

func TestTaskReconcileLoopUsesFreshLocalSnapshotWhenWatchMissesTaskProgress(t *testing.T) {
	initial := nodeControlSnapshot()
	initial.Tasks = []control.ReconcileTask{bootstrapTaskForNodeSnapshotTest()}
	fresh := initial.Clone()
	fresh.Revision = 2
	for i := range fresh.Tasks[0].ParticipantProgress {
		fresh.Tasks[0].ParticipantProgress[i].Status = control.TaskParticipantStatusDone
	}
	controller := &advancingLocalSnapshotController{
		snapshots: []control.Snapshot{initial, fresh},
		watch:     make(chan control.SnapshotEvent),
	}
	executor := &recordingTaskExecutor{}
	cfg := validNodeConfig(t)
	cfg.Slots.TickInterval = 10 * time.Millisecond
	node, err := New(cfg, withController(controller), withSlotReconciler(&recordingReconciler{}), withTaskExecutor(executor))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	waitUntil(t, func() bool {
		last := executor.Last()
		return last.Revision == 2 && len(last.Tasks) == 1 && participantStatuses(last.Tasks[0], control.TaskParticipantStatusDone)
	})
}

func TestTaskReconcileLoopBacksOffFreshSnapshotWhenNoCachedTasks(t *testing.T) {
	controller := &advancingLocalSnapshotController{
		snapshots: []control.Snapshot{nodeControlSnapshot()},
		watch:     make(chan control.SnapshotEvent),
	}
	cfg := validNodeConfig(t)
	cfg.Slots.TickInterval = 5 * time.Millisecond
	node, err := New(cfg, withController(controller), withSlotReconciler(&recordingReconciler{}), withTaskExecutor(&recordingTaskExecutor{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	initialCalls := controller.LocalSnapshotCalls()
	time.Sleep(120 * time.Millisecond)
	if got := controller.LocalSnapshotCalls(); got != initialCalls {
		t.Fatalf("LocalSnapshot calls = %d after idle wait, want %d with no cached tasks", got, initialCalls)
	}
}

func TestPreferredLeaderLoopRunsOnlyIdleSeamWithoutControllerTasks(t *testing.T) {
	controller := &advancingLocalSnapshotController{
		snapshots: []control.Snapshot{nodeControlSnapshot()},
		watch:     make(chan control.SnapshotEvent),
	}
	tasks := &recordingTaskExecutor{}
	preferred := &recordingTaskExecutor{}
	cfg := validNodeConfig(t)
	cfg.Slots.TickInterval = 5 * time.Millisecond
	node, err := New(cfg,
		withController(controller),
		withSlotReconciler(&recordingReconciler{}),
		withTaskExecutor(tasks),
		withPreferredLeaderReconciler(preferred),
		withPreferredLeaderReconcileInterval(10*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	initialTaskCalls := tasks.Calls()
	waitUntil(t, func() bool {
		return preferred.Calls() > 0
	})
	if got := tasks.Calls(); got != initialTaskCalls {
		t.Fatalf("task executor calls = %d, want %d while Controller tasks are idle", got, initialTaskCalls)
	}
}

func TestBlockingPreferredLeaderLoopDoesNotBlockStartWatchOrStop(t *testing.T) {
	controller := control.NewStaticController(nodeControlSnapshot())
	preferred := newCancellableBlockingPreferredLeaderExecutor()
	node, err := New(validNodeConfig(t),
		withController(controller),
		withSlotReconciler(&recordingReconciler{}),
		withTaskExecutor(&recordingTaskExecutor{}),
		withPreferredLeaderReconciler(preferred),
		withPreferredLeaderReconcileInterval(5*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	started := make(chan error, 1)
	go func() { started <- node.Start(context.Background()) }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start() waited for preferred-leader background reconcile")
	}
	select {
	case <-preferred.entered:
	case <-time.After(time.Second):
		t.Fatal("preferred-leader background reconcile did not start")
	}

	next := nodeControlSnapshot()
	next.Revision = 2
	next.Slots[0].ConfigEpoch++
	if err := controller.Publish(next); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	waitUntil(t, func() bool { return node.Snapshot().StateRevision == 2 })

	stopped := make(chan error, 1)
	go func() { stopped <- node.Stop(context.Background()) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not cancel blocked preferred-leader reconcile")
	}
}

func TestPreferredLeaderIntentGuardRequiresExactLatestAppliedSnapshot(t *testing.T) {
	snapshot := nodeControlSnapshot()
	node := &Node{}
	node.publishPreferredLeaderIntent(snapshot)
	t.Cleanup(node.invalidatePreferredLeaderIntent)
	intent := clustertasks.PreferredLeaderIntent{
		Revision:        snapshot.Revision,
		SlotID:          snapshot.Slots[0].SlotID,
		ConfigEpoch:     snapshot.Slots[0].ConfigEpoch,
		PreferredLeader: snapshot.Slots[0].PreferredLeader,
		DesiredPeers:    append([]uint64(nil), snapshot.Slots[0].DesiredPeers...),
	}
	if _, ok := node.preferredLeaderIntentGuard(intent); !ok {
		t.Fatal("exact latest applied intent was rejected")
	}

	tests := []struct {
		name   string
		mutate func(*clustertasks.PreferredLeaderIntent, *control.Snapshot)
	}{
		{name: "revision", mutate: func(intent *clustertasks.PreferredLeaderIntent, _ *control.Snapshot) { intent.Revision++ }},
		{name: "slot", mutate: func(intent *clustertasks.PreferredLeaderIntent, _ *control.Snapshot) { intent.SlotID++ }},
		{name: "config epoch", mutate: func(intent *clustertasks.PreferredLeaderIntent, _ *control.Snapshot) { intent.ConfigEpoch++ }},
		{name: "preferred", mutate: func(intent *clustertasks.PreferredLeaderIntent, _ *control.Snapshot) { intent.PreferredLeader++ }},
		{name: "desired peers", mutate: func(intent *clustertasks.PreferredLeaderIntent, _ *control.Snapshot) {
			intent.DesiredPeers = []uint64{1, 3, 2}
		}},
		{name: "active task", mutate: func(_ *clustertasks.PreferredLeaderIntent, snapshot *control.Snapshot) {
			snapshot.Tasks = []control.ReconcileTask{{TaskID: "active"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := intent
			candidate.DesiredPeers = append([]uint64(nil), intent.DesiredPeers...)
			latest := snapshot.Clone()
			tt.mutate(&candidate, &latest)
			node.publishPreferredLeaderIntent(latest)
			if _, ok := node.preferredLeaderIntentGuard(candidate); ok {
				t.Fatal("mismatched or task-blocked intent was accepted")
			}
		})
	}
}

func TestPreferredLeaderIntentGuardRejectsExecutionDuringNewSnapshotApply(t *testing.T) {
	previous := nodeControlSnapshot()
	previous.Slots[0].PreferredLeader = 2
	applyGate := newBlockingSlotReconciler()
	node := &Node{slots: applyGate, controlSnapshot: previous.Clone()}
	node.publishPreferredLeaderIntent(previous)
	t.Cleanup(node.invalidatePreferredLeaderIntent)
	runtime := newDelayedGuardPreferredLeaderRuntime()
	reconciler := clustertasks.NewPreferredLeaderReconciler(clustertasks.PreferredLeaderReconcilerConfig{
		LocalNode:      1,
		Runtime:        runtime,
		AttemptTimeout: time.Second,
		IntentGuard:    node.preferredLeaderIntentGuard,
	})

	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- reconciler.Reconcile(context.Background(), previous) }()
	select {
	case <-runtime.entered:
	case <-time.After(time.Second):
		t.Fatal("strict preferred-leader request did not reach the execution gap")
	}

	next := previous.Clone()
	next.Revision++
	next.Slots[0].PreferredLeader = 3
	next.Tasks = []control.ReconcileTask{{TaskID: "new-task"}}
	applyDone := make(chan error, 1)
	go func() { applyDone <- node.applySnapshot(context.Background(), next) }()
	select {
	case <-applyGate.entered:
	case <-time.After(time.Second):
		t.Fatal("new snapshot apply did not enter the blocking Slot reconcile")
	}
	if _, ok := node.preferredLeaderIntentGuard(clustertasks.PreferredLeaderIntent{
		Revision: previous.Revision, SlotID: 1, ConfigEpoch: 1,
		PreferredLeader: 2, DesiredPeers: []uint64{1, 2, 3},
	}); ok {
		t.Fatal("old intent remained current while the newer snapshot was applying")
	}

	close(runtime.allowExecution)
	if err := <-reconcileDone; err != nil {
		t.Fatalf("preferred Reconcile() error = %v", err)
	}
	if runtime.TransferStarted() {
		t.Fatal("old preferred-leader transfer executed after newer snapshot apply began")
	}
	close(applyGate.release)
	if err := <-applyDone; err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
}

func TestPreferredLeaderIntentGuardExecutionLinearizesBeforeNewSnapshotApply(t *testing.T) {
	previous := nodeControlSnapshot()
	node := &Node{controlSnapshot: previous.Clone()}
	node.publishPreferredLeaderIntent(previous)
	t.Cleanup(node.invalidatePreferredLeaderIntent)
	intent := clustertasks.PreferredLeaderIntent{
		Revision:        previous.Revision,
		SlotID:          previous.Slots[0].SlotID,
		ConfigEpoch:     previous.Slots[0].ConfigEpoch,
		PreferredLeader: previous.Slots[0].PreferredLeader,
		DesiredPeers:    append([]uint64(nil), previous.Slots[0].DesiredPeers...),
	}
	guard, ok := node.preferredLeaderIntentGuard(intent)
	if !ok {
		t.Fatal("current preferred-leader intent guard was rejected")
	}

	actionEntered := make(chan struct{})
	releaseAction := make(chan struct{})
	executionDone := make(chan bool, 1)
	go func() {
		executionDone <- guard.ExecuteIfCurrent(func() {
			close(actionEntered)
			<-releaseAction
		})
	}()
	select {
	case <-actionEntered:
	case <-time.After(time.Second):
		t.Fatal("guarded transfer action did not begin")
	}

	next := previous.Clone()
	next.Revision++
	next.Slots[0].PreferredLeader = 3
	applyDone := make(chan error, 1)
	go func() { applyDone <- node.applySnapshot(context.Background(), next) }()
	waitUntil(t, func() bool {
		node.preferredLeaderIntentMu.Lock()
		defer node.preferredLeaderIntentMu.Unlock()
		return node.preferredLeaderIntentGeneration == nil
	})
	select {
	case err := <-applyDone:
		t.Fatalf("applySnapshot() returned before the earlier guarded action linearized: %v", err)
	default:
	}

	close(releaseAction)
	select {
	case current := <-executionDone:
		if !current {
			t.Fatal("execution that acquired the generation guard first was rejected")
		}
	case <-time.After(time.Second):
		t.Fatal("guarded execution did not finish")
	}
	select {
	case err := <-applyDone:
		if err != nil {
			t.Fatalf("applySnapshot() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("applySnapshot did not continue after guarded execution completed")
	}
	if guard.Context().Err() == nil {
		t.Fatal("old generation context remained current after newer snapshot apply")
	}
}

func TestStopInvalidatesPreferredLeaderIntentBeforeWaitingForOtherLoops(t *testing.T) {
	previous := nodeControlSnapshot()
	node := &Node{}
	node.publishPreferredLeaderIntent(previous)
	intent := clustertasks.PreferredLeaderIntent{
		Revision:        previous.Revision,
		SlotID:          previous.Slots[0].SlotID,
		ConfigEpoch:     previous.Slots[0].ConfigEpoch,
		PreferredLeader: previous.Slots[0].PreferredLeader,
		DesiredPeers:    append([]uint64(nil), previous.Slots[0].DesiredPeers...),
	}
	guard, ok := node.preferredLeaderIntentGuard(intent)
	if !ok {
		t.Fatal("current preferred-leader intent guard was rejected")
	}

	healthCtx, healthCancel := context.WithCancel(context.Background())
	healthLoopStopping := make(chan struct{})
	releaseHealthLoop := make(chan struct{})
	node.healthReportCancel = healthCancel
	node.healthReportWG.Add(1)
	go func() {
		defer node.healthReportWG.Done()
		<-healthCtx.Done()
		close(healthLoopStopping)
		<-releaseHealthLoop
	}()

	stopDone := make(chan error, 1)
	go func() { stopDone <- node.Stop(context.Background()) }()
	select {
	case <-healthLoopStopping:
	case <-time.After(time.Second):
		t.Fatal("Stop did not reach the blocking health-loop shutdown")
	}
	if guard.Context().Err() == nil {
		t.Fatal("Stop waited for another loop before invalidating preferred-leader intent")
	}
	close(releaseHealthLoop)
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not finish after the blocking loop exited")
	}
}

func TestTaskReconcileLoopRecordsLocalSnapshotError(t *testing.T) {
	initial := nodeControlSnapshot()
	initial.Tasks = []control.ReconcileTask{bootstrapTaskForNodeSnapshotTest()}
	controller := &advancingLocalSnapshotController{
		snapshots:        []control.Snapshot{initial},
		watch:            make(chan control.SnapshotEvent),
		snapshotErrAfter: 1,
		snapshotErr:      errors.New("control read failed"),
	}
	cfg := validNodeConfig(t)
	cfg.Slots.TickInterval = 5 * time.Millisecond
	node, err := New(cfg, withController(controller), withSlotReconciler(&recordingReconciler{}), withTaskExecutor(&recordingTaskExecutor{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	waitUntil(t, func() bool {
		return strings.Contains(node.Snapshot().LastTaskReconcileError, "snapshot: control read failed")
	})
}

func TestTaskReconcileLoopRecordsNonRetryableReconcileError(t *testing.T) {
	initial := nodeControlSnapshot()
	initial.Tasks = []control.ReconcileTask{bootstrapTaskForNodeSnapshotTest()}
	controller := &advancingLocalSnapshotController{
		snapshots: []control.Snapshot{initial},
		watch:     make(chan control.SnapshotEvent),
	}
	executor := &flakyTaskExecutor{errs: []error{nil, errors.New("executor boom")}}
	cfg := validNodeConfig(t)
	cfg.Slots.TickInterval = 5 * time.Millisecond
	node, err := New(cfg, withController(controller), withSlotReconciler(&recordingReconciler{}), withTaskExecutor(executor))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	waitUntil(t, func() bool {
		return strings.Contains(node.Snapshot().LastTaskReconcileError, "reconcile: executor boom")
	})
}

func TestRetryableTaskReconcileErrorMatchesRemoteControllerNotLeader(t *testing.T) {
	err := transport.RemoteError{Code: "remote_error", Message: controller.ErrNotLeader.Error()}
	if !retryableTaskReconcileError(err) {
		t.Fatalf("retryableTaskReconcileError(%v) = false, want true", err)
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

func bootstrapTaskForNodeSnapshotTest() control.ReconcileTask {
	return control.ReconcileTask{
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
	}
}

func participantStatuses(task control.ReconcileTask, status control.TaskParticipantStatus) bool {
	if len(task.ParticipantProgress) == 0 {
		return false
	}
	for _, progress := range task.ParticipantProgress {
		if progress.Status != status {
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

type blockingPreferredApplySlotReconciler struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingSlotReconciler() *blockingPreferredApplySlotReconciler {
	return &blockingPreferredApplySlotReconciler{entered: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingPreferredApplySlotReconciler) Reconcile(context.Context, control.Snapshot) error {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	return nil
}

type delayedGuardPreferredLeaderRuntime struct {
	entered         chan struct{}
	allowExecution  chan struct{}
	once            sync.Once
	mu              sync.Mutex
	transferStarted bool
}

func newDelayedGuardPreferredLeaderRuntime() *delayedGuardPreferredLeaderRuntime {
	return &delayedGuardPreferredLeaderRuntime{
		entered:        make(chan struct{}),
		allowExecution: make(chan struct{}),
	}
}

func (r *delayedGuardPreferredLeaderRuntime) Status(multiraft.SlotID) (multiraft.Status, error) {
	return multiraft.Status{
		SlotID: 1, NodeID: 1, LeaderID: 1, Term: 7,
		CurrentVoters: []multiraft.NodeID{1, 2, 3},
	}, nil
}

func (r *delayedGuardPreferredLeaderRuntime) TryTransferLeadershipToPreferred(
	_ context.Context,
	_ multiraft.SlotID,
	_ multiraft.NodeID,
	_ uint64,
	_ []multiraft.NodeID,
	_ multiraft.NodeID,
	guard multiraft.PreferredLeaderTransferGuard,
) (multiraft.PreferredLeaderTransferDecision, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.allowExecution
	if guard == nil || !guard.ExecuteIfCurrent(func() {
		r.mu.Lock()
		r.transferStarted = true
		r.mu.Unlock()
	}) {
		return multiraft.PreferredLeaderTransferStaleIntent, nil
	}
	return multiraft.PreferredLeaderTransferStarted, nil
}

func (r *delayedGuardPreferredLeaderRuntime) TransferStarted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.transferStarted
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

type cancellableBlockingPreferredLeaderExecutor struct {
	entered chan struct{}
	once    sync.Once
}

func newCancellableBlockingPreferredLeaderExecutor() *cancellableBlockingPreferredLeaderExecutor {
	return &cancellableBlockingPreferredLeaderExecutor{entered: make(chan struct{})}
}

func (e *cancellableBlockingPreferredLeaderExecutor) Reconcile(ctx context.Context, _ control.Snapshot) error {
	e.once.Do(func() { close(e.entered) })
	<-ctx.Done()
	return ctx.Err()
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

type flakyTaskExecutor struct {
	mu    sync.Mutex
	calls int
	errs  []error
	last  control.Snapshot
}

func (e *flakyTaskExecutor) Reconcile(_ context.Context, snap control.Snapshot) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.last = snap.Clone()
	if len(e.errs) == 0 {
		return nil
	}
	err := e.errs[0]
	e.errs = e.errs[1:]
	return err
}

func (e *flakyTaskExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type advancingLocalSnapshotController struct {
	mu               sync.Mutex
	snapshots        []control.Snapshot
	watch            chan control.SnapshotEvent
	snapshotErrAfter int
	snapshotErr      error
	calls            int
}

func (c *advancingLocalSnapshotController) Start(context.Context) error { return nil }

func (c *advancingLocalSnapshotController) Stop(context.Context) error { return nil }

func (c *advancingLocalSnapshotController) LocalSnapshot(context.Context) (control.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.snapshotErr != nil && c.snapshotErrAfter > 0 && c.calls > c.snapshotErrAfter {
		return control.Snapshot{}, c.snapshotErr
	}
	if len(c.snapshots) == 0 {
		return control.Snapshot{}, nil
	}
	snapshot := c.snapshots[0]
	if len(c.snapshots) > 1 {
		c.snapshots = c.snapshots[1:]
	}
	return snapshot.Clone(), nil
}

func (c *advancingLocalSnapshotController) LocalSnapshotCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *advancingLocalSnapshotController) LeaderID() uint64 { return 1 }

func (c *advancingLocalSnapshotController) ReportNode(context.Context, control.NodeReport) error {
	return nil
}

func (c *advancingLocalSnapshotController) ReportSlots(context.Context, control.SlotRuntimeReport) error {
	return nil
}

func (c *advancingLocalSnapshotController) CompleteTask(context.Context, control.TaskResult) error {
	return nil
}

func (c *advancingLocalSnapshotController) FailTask(context.Context, control.TaskResult) error {
	return nil
}

func (c *advancingLocalSnapshotController) ReportTaskProgress(context.Context, control.TaskProgress) error {
	return nil
}

func (c *advancingLocalSnapshotController) AdvanceSlotReplicaMovePhase(context.Context, control.SlotReplicaMovePhaseAdvance) error {
	return nil
}

func (c *advancingLocalSnapshotController) CommitSlotReplicaMove(context.Context, control.SlotReplicaMoveCommit) error {
	return nil
}

func (c *advancingLocalSnapshotController) RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	return control.SlotLeaderTransferResult{}, nil
}

func (c *advancingLocalSnapshotController) RequestSlotReplicaMove(context.Context, control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	return control.SlotReplicaMoveResult{}, nil
}

func (c *advancingLocalSnapshotController) Watch() <-chan control.SnapshotEvent {
	return c.watch
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
