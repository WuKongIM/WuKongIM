package clusterv2

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
)

func TestWatchRouteAuthoritiesReceivesLeadershipChange(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}}
	watch := node.WatchRouteAuthorities()
	node.publishRouteAuthority(RouteAuthority{
		HashSlot:       7,
		SlotID:         2,
		LeaderNodeID:   1,
		LeaderTerm:     9,
		ConfigEpoch:    3,
		RouteRevision:  11,
		AuthorityEpoch: 1,
	})
	select {
	case ev := <-watch:
		if len(ev.Authorities) != 1 {
			t.Fatalf("authorities len = %d, want 1", len(ev.Authorities))
		}
		got := ev.Authorities[0]
		if got.HashSlot != 7 || got.SlotID != 2 || got.LeaderNodeID != 1 || got.LeaderTerm != 9 || got.ConfigEpoch != 3 || got.RouteRevision != 11 || got.AuthorityEpoch != 1 {
			t.Fatalf("authority = %#v", got)
		}
	default:
		t.Fatal("expected route authority event")
	}
}

func TestRouteAuthorityEpochIncrementsWhenLocalNodeBecomesAuthorityAgain(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, routeAuthorityEpochs: map[uint16]uint64{}}
	first := node.nextAuthorityEpoch(3, 1)
	second := node.nextAuthorityEpoch(3, 1)
	if first != 1 || second != 2 {
		t.Fatalf("epochs = %d,%d want 1,2", first, second)
	}
}

func TestRouteAuthorityEpochIncrementsWhenLeaderBecomesUnknown(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, routeAuthorityEpochs: map[uint16]uint64{3: 7}}
	previous := routeAuthorityKey{slotID: 1, leaderNodeID: 1, leaderTerm: 9, configEpoch: 1, revision: 4}
	current := routeAuthorityKey{slotID: 1, leaderNodeID: 0, leaderTerm: 0, configEpoch: 1, revision: 4}

	epoch := node.authorityEpochForChange(3, previous, true, current)

	if epoch != 8 {
		t.Fatalf("epoch = %d, want 8 for no-leader authority change", epoch)
	}
}

func TestRouteIncludesLeaderTermAndConfigEpoch(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), routeAuthorityEpochs: map[uint16]uint64{}}
	if err := node.router.UpdateControlSnapshot(routeAuthoritySnapshot(1)); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 9}})
	node.started.Store(true)

	route, err := node.RouteHashSlot(0)
	if err != nil {
		t.Fatalf("RouteHashSlot() error = %v", err)
	}
	if route.LeaderTerm != 9 || route.ConfigEpoch != 1 {
		t.Fatalf("route = %#v, want leaderTerm=9 configEpoch=1", route)
	}
}

func TestRouteAuthorityEpochStaysStableForRevisionOnlyUpdate(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), routeAuthorityEpochs: map[uint16]uint64{}}
	previous := routeAuthoritySnapshot(1)
	if err := node.router.UpdateControlSnapshot(previous); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 9}})
	node.routeAuthorityEpochs[0] = 1
	node.routeAuthorityEpochs[1] = 1

	changes := node.routeAuthorityChanges(node.router.Table(), routeAuthorityTable(2))
	if len(changes) != 2 {
		t.Fatalf("changes len = %d, want 2", len(changes))
	}
	for _, got := range changes {
		if got.LeaderNodeID != 1 || got.LeaderTerm != 9 || got.ConfigEpoch != 1 || got.RouteRevision != 2 || got.AuthorityEpoch != 1 {
			t.Fatalf("authority = %#v, want stable epoch 1 for revision-only update", got)
		}
	}
}

func TestRouteAuthorityEpochIncrementsWhenLeaderTermChanges(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), routeAuthorityEpochs: map[uint16]uint64{0: 1, 1: 1}}
	before := routeAuthorityTableWithIdentity(1, 9, 1)
	after := routeAuthorityTableWithIdentity(1, 10, 1)

	changes := node.routeAuthorityChanges(before, after)
	if len(changes) != 2 {
		t.Fatalf("changes len = %d, want 2", len(changes))
	}
	for _, got := range changes {
		if got.LeaderTerm != 10 || got.ConfigEpoch != 1 || got.AuthorityEpoch != 2 {
			t.Fatalf("authority = %#v, want term 10 epoch 2", got)
		}
	}
}

func TestRouteAuthorityEpochIncrementsWhenConfigEpochChanges(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), routeAuthorityEpochs: map[uint16]uint64{0: 1, 1: 1}}
	before := routeAuthorityTableWithIdentity(1, 9, 1)
	after := routeAuthorityTableWithIdentity(2, 9, 2)

	changes := node.routeAuthorityChanges(before, after)
	if len(changes) != 2 {
		t.Fatalf("changes len = %d, want 2", len(changes))
	}
	for _, got := range changes {
		if got.LeaderTerm != 9 || got.ConfigEpoch != 2 || got.AuthorityEpoch != 2 {
			t.Fatalf("authority = %#v, want config epoch 2 authority epoch 2", got)
		}
	}
}

func TestApplySnapshotPublishesChangedRouteAuthority(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), routeAuthorityEpochs: map[uint16]uint64{}}
	previous := routeAuthoritySnapshot(1)
	if err := node.router.UpdateControlSnapshot(previous); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 9}})
	node.controlSnapshot = previous
	watch := node.WatchRouteAuthorities()

	next := routeAuthoritySnapshot(2)
	if err := node.applySnapshot(context.Background(), next); err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
	select {
	case ev := <-watch:
		if len(ev.Authorities) != 2 {
			t.Fatalf("authorities len = %d, want 2", len(ev.Authorities))
		}
		for _, got := range ev.Authorities {
			if got.SlotID != 1 || got.LeaderNodeID != 1 || got.LeaderTerm != 9 || got.ConfigEpoch != 1 || got.RouteRevision != 2 || got.AuthorityEpoch == 0 {
				t.Fatalf("authority = %#v", got)
			}
		}
	default:
		t.Fatal("expected route authority event")
	}
}

func TestApplySnapshotPublishesRouteAuthorityBeforeTaskReconcile(t *testing.T) {
	executor := &blockingTaskExecutor{entered: make(chan struct{}, 1), unblock: make(chan struct{})}
	node := &Node{
		cfg:                  Config{NodeID: 1},
		router:               routing.NewRouter(),
		slots:                &recordingReconciler{},
		tasks:                executor,
		routeAuthorityEpochs: map[uint16]uint64{},
	}
	previous := routeAuthoritySnapshot(1)
	if err := node.router.UpdateControlSnapshot(previous); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 9}})
	node.controlSnapshot = previous
	watch := node.WatchRouteAuthorities()

	next := routeAuthoritySnapshot(2)
	next.HashSlots.Revision = 2
	next.Tasks = []control.ReconcileTask{{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		Kind:        control.TaskKindBootstrap,
		Step:        control.TaskStepCreateSlot,
		TargetNode:  1,
		TargetPeers: []uint64{1},
		ConfigEpoch: 1,
		Status:      control.TaskStatusPending,
	}}
	done := make(chan error, 1)
	go func() {
		done <- node.applySnapshot(context.Background(), next)
	}()

	select {
	case <-executor.entered:
	case err := <-done:
		close(executor.unblock)
		t.Fatalf("applySnapshot() returned before task Reconcile: %v", err)
	case <-time.After(2 * time.Second):
		close(executor.unblock)
		t.Fatal("task executor did not enter Reconcile")
	}
	select {
	case ev := <-watch:
		if len(ev.Authorities) != 2 {
			close(executor.unblock)
			t.Fatalf("authorities len = %d, want 2", len(ev.Authorities))
		}
	case <-time.After(200 * time.Millisecond):
		close(executor.unblock)
		t.Fatal("route authority event was not published before task reconciliation blocked")
	}
	close(executor.unblock)
	if err := <-done; err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
}

func TestApplySnapshotSkipsDuplicatePostReconcileAuthority(t *testing.T) {
	reconciler := &blockingSlotReconciler{entered: make(chan struct{}, 1), unblock: make(chan struct{})}
	node := &Node{
		cfg:                  Config{NodeID: 1},
		router:               routing.NewRouter(),
		slots:                reconciler,
		routeAuthorityEpochs: map[uint16]uint64{},
	}
	previous := routeAuthoritySnapshot(1)
	if err := node.router.UpdateControlSnapshot(previous); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 9}})
	node.controlSnapshot = previous
	watch := node.WatchRouteAuthorities()

	next := routeAuthoritySnapshot(2)
	next.Slots[0].ConfigEpoch = 2
	done := make(chan error, 1)
	go func() {
		done <- node.applySnapshot(context.Background(), next)
	}()

	select {
	case <-reconciler.entered:
	case err := <-done:
		close(reconciler.unblock)
		t.Fatalf("applySnapshot() returned before slot Reconcile: %v", err)
	case <-time.After(2 * time.Second):
		close(reconciler.unblock)
		t.Fatal("slot reconciler did not enter Reconcile")
	}

	beforePoll := node.router.Table()
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 10}})
	node.publishRouteAuthority(node.routeAuthorityChanges(beforePoll, node.router.Table())...)
	pollEvent := receiveRouteAuthorityEvent(t, watch)
	assertRouteAuthorityEventIdentity(t, pollEvent, routeAuthorityWant{leaderTerm: 10, configEpoch: 2, authorityEpoch: 1})

	close(reconciler.unblock)
	if err := <-done; err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
	assertNoRouteAuthorityEvent(t, watch)
	node.mu.RLock()
	epoch := node.routeAuthorityEpochs[0]
	node.mu.RUnlock()
	if epoch != pollEvent.Authorities[0].AuthorityEpoch {
		t.Fatalf("authority epoch = %d, want duplicate identity to keep epoch %d", epoch, pollEvent.Authorities[0].AuthorityEpoch)
	}
}

func TestNodeStopClosesRouteAuthorityWatchers(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}}
	watch := node.WatchRouteAuthorities()
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case _, ok := <-watch:
		if ok {
			t.Fatal("route authority watcher remains open after Stop")
		}
	default:
		t.Fatal("route authority watcher not closed after Stop")
	}
}

func routeAuthoritySnapshot(revision uint64) control.Snapshot {
	return control.Snapshot{
		Revision:     revision,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1},
		},
		HashSlots: control.HashSlotTable{Revision: revision, Count: 2, Ranges: []control.HashSlotRange{{From: 0, To: 1, SlotID: 1}}},
	}
}

func routeAuthorityTable(revision uint64) *routing.Table {
	return routeAuthorityTableWithIdentity(revision, 9, 1)
}

func routeAuthorityTableWithIdentity(revision uint64, leaderTerm uint64, configEpoch uint64) *routing.Table {
	snapshot := routeAuthoritySnapshot(revision)
	snapshot.Slots[0].ConfigEpoch = configEpoch
	router := routing.NewRouter()
	if err := router.UpdateControlSnapshot(snapshot); err != nil {
		panic(err)
	}
	router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: leaderTerm}})
	return router.Table()
}

type blockingTaskExecutor struct {
	entered chan struct{}
	unblock chan struct{}
}

func (e *blockingTaskExecutor) Reconcile(ctx context.Context, snap control.Snapshot) error {
	select {
	case e.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.unblock:
		return nil
	}
}

type blockingSlotReconciler struct {
	entered chan struct{}
	unblock chan struct{}
}

func (r *blockingSlotReconciler) Reconcile(ctx context.Context, snap control.Snapshot) error {
	select {
	case r.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.unblock:
		return nil
	}
}

func receiveRouteAuthorityEvent(t *testing.T, watch <-chan RouteAuthorityEvent) RouteAuthorityEvent {
	t.Helper()
	select {
	case ev := <-watch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("expected route authority event")
		return RouteAuthorityEvent{}
	}
}

func assertNoRouteAuthorityEvent(t *testing.T, watch <-chan RouteAuthorityEvent) {
	t.Helper()
	select {
	case ev := <-watch:
		t.Fatalf("unexpected duplicate route authority event: %#v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

type routeAuthorityWant struct {
	leaderTerm     uint64
	configEpoch    uint64
	authorityEpoch uint64
}

func assertRouteAuthorityEventIdentity(t *testing.T, ev RouteAuthorityEvent, want routeAuthorityWant) {
	t.Helper()
	if len(ev.Authorities) != 2 {
		t.Fatalf("authorities len = %d, want 2", len(ev.Authorities))
	}
	for _, got := range ev.Authorities {
		if got.LeaderTerm != want.leaderTerm || got.ConfigEpoch != want.configEpoch || got.AuthorityEpoch != want.authorityEpoch {
			t.Fatalf("authority = %#v, want term=%d configEpoch=%d authorityEpoch=%d", got, want.leaderTerm, want.configEpoch, want.authorityEpoch)
		}
	}
}
