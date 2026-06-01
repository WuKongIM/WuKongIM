package clusterv2

import (
	"context"
	"testing"

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
		RouteRevision:  11,
		AuthorityEpoch: 1,
	})
	select {
	case ev := <-watch:
		if len(ev.Authorities) != 1 {
			t.Fatalf("authorities len = %d, want 1", len(ev.Authorities))
		}
		got := ev.Authorities[0]
		if got.HashSlot != 7 || got.SlotID != 2 || got.LeaderNodeID != 1 || got.RouteRevision != 11 || got.AuthorityEpoch != 1 {
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

func TestRouteAuthorityEpochStaysStableForRevisionOnlyUpdate(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), routeAuthorityEpochs: map[uint16]uint64{}}
	previous := routeAuthoritySnapshot(1)
	if err := node.router.UpdateControlSnapshot(previous); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})
	node.routeAuthorityEpochs[0] = 1
	node.routeAuthorityEpochs[1] = 1

	changes := node.routeAuthorityChanges(node.router.Table(), routeAuthorityTable(2))
	if len(changes) != 2 {
		t.Fatalf("changes len = %d, want 2", len(changes))
	}
	for _, got := range changes {
		if got.LeaderNodeID != 1 || got.RouteRevision != 2 || got.AuthorityEpoch != 1 {
			t.Fatalf("authority = %#v, want stable epoch 1 for revision-only update", got)
		}
	}
}

func TestApplySnapshotPublishesChangedRouteAuthority(t *testing.T) {
	node := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), routeAuthorityEpochs: map[uint16]uint64{}}
	previous := routeAuthoritySnapshot(1)
	if err := node.router.UpdateControlSnapshot(previous); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})
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
			if got.SlotID != 1 || got.LeaderNodeID != 1 || got.RouteRevision != 2 || got.AuthorityEpoch == 0 {
				t.Fatalf("authority = %#v", got)
			}
		}
	default:
		t.Fatal("expected route authority event")
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
	snapshot := routeAuthoritySnapshot(revision)
	router := routing.NewRouter()
	if err := router.UpdateControlSnapshot(snapshot); err != nil {
		panic(err)
	}
	router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1}})
	return router.Table()
}
