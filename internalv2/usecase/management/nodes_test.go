package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestListNodesBuildsReadOnlyNodeInventory(t *testing.T) {
	generatedAt := time.Date(2026, 6, 16, 9, 30, 0, 0, time.UTC)
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 2,
			snapshot: control.Snapshot{
				ControllerID: 1,
				Nodes: []control.Node{
					{NodeID: 2, Addr: "127.0.0.1:7012", Roles: []control.Role{control.RoleData}, Status: control.NodeSuspect, JoinState: control.NodeJoinStateJoining},
					{NodeID: 1, Addr: "127.0.0.1:7011", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive, Health: freshReadyNodeHealth()},
				},
				Slots: []control.SlotAssignment{
					{SlotID: 2, DesiredPeers: []uint64{2}, PreferredLeader: 2},
					{SlotID: 1, DesiredPeers: []uint64{1, 2}, PreferredLeader: 1},
				},
			},
		},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2}},
				2: {SlotID: 2, LeaderID: 2, CurrentVoters: []uint64{2}},
			},
		},
		Now: func() time.Time { return generatedAt },
	})

	got, err := app.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if !got.GeneratedAt.Equal(generatedAt) {
		t.Fatalf("GeneratedAt = %s, want %s", got.GeneratedAt, generatedAt)
	}
	if got.ControllerLeaderID != 1 {
		t.Fatalf("ControllerLeaderID = %d, want 1", got.ControllerLeaderID)
	}
	if len(got.Items) != 2 {
		t.Fatalf("Items len = %d, want 2: %#v", len(got.Items), got.Items)
	}

	first := got.Items[0]
	if first.NodeID != 1 || first.Name != "node-1" || first.Addr != "127.0.0.1:7011" {
		t.Fatalf("first node identity = %#v, want node-1 at 127.0.0.1:7011", first)
	}
	if first.Status != "alive" || first.Controller.Role != "leader" || !first.Controller.Voter {
		t.Fatalf("first node status/controller = %#v/%#v, want alive controller leader voter", first.Status, first.Controller)
	}
	if first.Membership.Role != "data" || first.Membership.JoinState != "active" || !first.Membership.Schedulable {
		t.Fatalf("first membership = %#v, want active schedulable data", first.Membership)
	}
	if first.Slots.ReplicaCount != 1 || first.Slots.LeaderCount != 1 || first.Slots.FollowerCount != 0 {
		t.Fatalf("first slots = %#v, want one leader replica", first.Slots)
	}
	if first.Runtime.NodeID != 1 || !first.Runtime.Unknown {
		t.Fatalf("first runtime = %#v, want unknown runtime for node 1", first.Runtime)
	}
	if first.Actions.CanScaleIn || first.Actions.CanPromoteControllerVoter ||
		!first.Actions.CanOnboard || !first.Actions.CanMoveSlotsIn || !first.Actions.CanMoveSlotsOut {
		t.Fatalf("first lifecycle actions = %#v, want controller-voter slot migration enabled and scale-in disabled", first.Actions)
	}

	second := got.Items[1]
	if second.NodeID != 2 || !second.IsLocal {
		t.Fatalf("second node = %#v, want local node 2", second)
	}
	if second.Status != "suspect" || second.Membership.Schedulable {
		t.Fatalf("second status/membership = %s/%#v, want suspect not schedulable", second.Status, second.Membership)
	}
	if second.Slots.ReplicaCount != 2 || second.Slots.LeaderCount != 1 || second.Slots.FollowerCount != 1 {
		t.Fatalf("second slots = %#v, want two replicas with one leader", second.Slots)
	}
}

func TestListNodesReportsLifecycleAndCapacity(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{
				ControllerID: 1,
				Nodes: []control.Node{
					{NodeID: 1, Addr: "127.0.0.1:7011", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive, Health: freshReadyNodeHealth(), JoinState: control.NodeJoinStateActive, CapacityWeight: 3},
					{NodeID: 2, Addr: "127.0.0.1:7012", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateJoining, CapacityWeight: 2},
					{NodeID: 3, Addr: "127.0.0.1:7013", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving, CapacityWeight: 1},
					{NodeID: 4, Addr: "127.0.0.1:7014", Roles: []control.Role{control.RoleData}, Status: control.NodeDown, JoinState: control.NodeJoinStateRemoved, CapacityWeight: 1},
					{NodeID: 5, Addr: "127.0.0.1:7015", Roles: []control.Role{control.RoleData}, Status: control.NodeSuspect, JoinState: control.NodeJoinStateActive, CapacityWeight: 0},
				},
			},
		},
	})

	got, err := app.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(got.Items) != 5 {
		t.Fatalf("Items len = %d, want 5", len(got.Items))
	}
	want := map[uint64]struct {
		joinState   string
		schedulable bool
		capacity    int
	}{
		1: {joinState: "active", schedulable: true, capacity: 3},
		2: {joinState: "joining", schedulable: false, capacity: 2},
		3: {joinState: "leaving", schedulable: false, capacity: 1},
		4: {joinState: "removed", schedulable: false, capacity: 1},
		5: {joinState: "active", schedulable: false, capacity: 1},
	}
	for _, item := range got.Items {
		expect := want[item.NodeID]
		if item.Membership.JoinState != expect.joinState || item.Membership.Schedulable != expect.schedulable || item.CapacityWeight != expect.capacity {
			t.Fatalf("node %d membership=%#v capacity=%d, want %#v", item.NodeID, item.Membership, item.CapacityWeight, expect)
		}
	}
}

func TestListNodesIncludesHealthFreshnessFields(t *testing.T) {
	generatedAt := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	reportedAt := generatedAt.Add(-4 * time.Second)
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 4,
			snapshot: control.Snapshot{
				ControllerID: 1,
				Nodes: []control.Node{
					{
						NodeID:    4,
						Addr:      "127.0.0.1:7004",
						Roles:     []control.Role{control.RoleData},
						JoinState: control.NodeJoinStateActive,
						Status:    control.NodeAlive,
						Health: control.NodeHealth{
							Status:                  control.NodeAlive,
							Freshness:               control.NodeHealthFresh,
							RuntimeReady:            true,
							ObservedControlRevision: 12,
							ObservedSlotRevision:    21,
							ReportedAt:              reportedAt,
							ReportAge:               4 * time.Second,
							ReportTTL:               30 * time.Second,
							ErrorCode:               "ok",
						},
					},
					{
						NodeID:    5,
						Addr:      "127.0.0.1:7005",
						Roles:     []control.Role{control.RoleData},
						JoinState: control.NodeJoinStateActive,
						Status:    control.NodeAlive,
						Health: control.NodeHealth{
							Status:       control.NodeAlive,
							Freshness:    control.NodeHealthStale,
							RuntimeReady: true,
							ReportAge:    31 * time.Second,
							ReportTTL:    30 * time.Second,
						},
					},
				},
			},
		},
		Now: func() time.Time { return generatedAt },
	})

	resp, err := app.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("Items len = %d, want 2: %#v", len(resp.Items), resp.Items)
	}
	fresh := resp.Items[0]
	if !fresh.Health.Fresh || fresh.Health.Freshness != "fresh" || !fresh.Health.RuntimeReady ||
		fresh.Health.ReportAgeMS != 4000 || fresh.Health.ReportTTLMS != 30000 ||
		fresh.Health.ObservedControlRevision != 12 || fresh.Health.ObservedSlotRevision != 21 ||
		fresh.Health.ErrorCode != "ok" || !fresh.Health.LastHeartbeatAt.Equal(reportedAt) {
		t.Fatalf("fresh node health = %#v, want fresh health evidence fields", fresh.Health)
	}
	if !fresh.Membership.Schedulable {
		t.Fatalf("fresh membership = %#v, want schedulable from shared health predicate", fresh.Membership)
	}
	stale := resp.Items[1]
	if stale.Health.Fresh || stale.Health.Freshness != "stale" || stale.Health.ReportAgeMS != 31000 || stale.Membership.Schedulable {
		t.Fatalf("stale node health/membership = %#v/%#v, want stale non-schedulable health", stale.Health, stale.Membership)
	}
}

func TestListNodesReportsLifecycleActionHints(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{
				ControllerID: 1,
				Nodes: []control.Node{
					{NodeID: 1, Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
					{NodeID: 2, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
					{NodeID: 3, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
					{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateJoining},
					{NodeID: 5, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateRemoved},
				},
			},
		},
	})

	got, err := app.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	actions := map[uint64]NodeActions{}
	for _, item := range got.Items {
		actions[item.NodeID] = item.Actions
	}
	if actions[1].CanScaleIn || actions[1].CanPromoteControllerVoter ||
		!actions[1].CanOnboard || !actions[1].CanMoveSlotsIn || !actions[1].CanMoveSlotsOut {
		t.Fatalf("controller voter actions = %#v, want slot move actions enabled and scale-in disabled", actions[1])
	}
	if !actions[2].CanScaleIn || !actions[2].CanOnboard || !actions[2].CanMoveSlotsIn ||
		!actions[2].CanMoveSlotsOut || !actions[2].CanPromoteControllerVoter {
		t.Fatalf("active data actions = %#v, want scale-in, slot move, and controller promotion enabled", actions[2])
	}
	if !actions[3].CanScaleIn || actions[3].CanOnboard || actions[3].CanMoveSlotsIn ||
		actions[3].CanMoveSlotsOut || actions[3].CanPromoteControllerVoter {
		t.Fatalf("leaving data actions = %#v, want scale-in enabled and slot move/promotion disabled", actions[3])
	}
	if actions[4].CanScaleIn || actions[4].CanOnboard || actions[4].CanMoveSlotsIn ||
		actions[4].CanMoveSlotsOut || actions[4].CanPromoteControllerVoter ||
		actions[5].CanScaleIn || actions[5].CanOnboard || actions[5].CanMoveSlotsIn ||
		actions[5].CanMoveSlotsOut || actions[5].CanPromoteControllerVoter {
		t.Fatalf("inactive data actions = %#v/%#v, want lifecycle actions disabled", actions[4], actions[5])
	}
}

func TestListNodesCountsActualSlotRaftLeaders(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{
				ControllerID: 1,
				Nodes: []control.Node{
					{NodeID: 1, Addr: "127.0.0.1:7011", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
					{NodeID: 2, Addr: "127.0.0.1:7012", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
					{NodeID: 3, Addr: "127.0.0.1:7013", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
				},
				Slots: []control.SlotAssignment{
					{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 1},
					{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 2},
					{SlotID: 3, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 3},
				},
			},
		},
		SlotRuntimeStatus: &fakeSlotRuntimeStatusReader{
			statuses: map[uint32]SlotRuntimeStatus{
				1: {SlotID: 1, LeaderID: 2, CurrentVoters: []uint64{1, 2, 3}},
				2: {SlotID: 2, LeaderID: 3, CurrentVoters: []uint64{1, 2, 3}},
				3: {SlotID: 3, LeaderID: 3, CurrentVoters: []uint64{1, 2, 3}},
			},
		},
	})

	got, err := app.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(got.Items) != 3 {
		t.Fatalf("Items len = %d, want 3: %#v", len(got.Items), got.Items)
	}
	wantLeaders := map[uint64]int{1: 0, 2: 1, 3: 2}
	for _, item := range got.Items {
		if item.Slots.LeaderCount != wantLeaders[item.NodeID] {
			t.Fatalf("node %d leader count = %d, want %d from actual raft leaders", item.NodeID, item.Slots.LeaderCount, wantLeaders[item.NodeID])
		}
		if item.Slots.FollowerCount != item.Slots.ReplicaCount-item.Slots.LeaderCount {
			t.Fatalf("node %d slots = %#v, want followers derived from actual raft leaders", item.NodeID, item.Slots)
		}
	}
}

func TestListNodesAttachesRuntimeSummary(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{
			nodeID: 1,
			snapshot: control.Snapshot{
				ControllerID: 1,
				Nodes: []control.Node{
					{NodeID: 1, Addr: "127.0.0.1:7011", Roles: []control.Role{control.RoleController, control.RoleData}, Status: control.NodeAlive},
					{NodeID: 2, Addr: "127.0.0.1:7012", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
				},
			},
		},
		RuntimeSummary: fakeNodeRuntimeSummaryReader{
			summaries: map[uint64]NodeRuntimeSummary{
				1: {
					NodeID:               1,
					ActiveOnline:         4,
					ClosingOnline:        1,
					TotalOnline:          5,
					GatewaySessions:      6,
					SessionsByListener:   map[string]int{"tcp": 6},
					AcceptingNewSessions: false,
					Draining:             true,
				},
			},
			errs: map[uint64]error{2: errors.New("node runtime unavailable")},
		},
	})

	got, err := app.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("Items len = %d, want 2: %#v", len(got.Items), got.Items)
	}
	first := got.Items[0].Runtime
	if first.Unknown || first.ActiveOnline != 4 || first.ClosingOnline != 1 || first.TotalOnline != 5 ||
		first.GatewaySessions != 6 || first.SessionsByListener["tcp"] != 6 ||
		first.AcceptingNewSessions || !first.Draining {
		t.Fatalf("first runtime = %#v, want concrete runtime summary", first)
	}
	second := got.Items[1].Runtime
	if second.NodeID != 2 || !second.Unknown || len(second.SessionsByListener) != 0 {
		t.Fatalf("second runtime = %#v, want unknown runtime fallback for node 2", second)
	}
}

func TestListNodesReturnsClusterSnapshotError(t *testing.T) {
	wantErr := errors.New("control unavailable")
	app := New(Options{Cluster: fakeNodeSnapshotReader{err: wantErr}})

	_, err := app.ListNodes(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("ListNodes() error = %v, want %v", err, wantErr)
	}
}

type fakeNodeSnapshotReader struct {
	nodeID   uint64
	snapshot control.Snapshot
	err      error
}

func (f fakeNodeSnapshotReader) NodeID() uint64 { return f.nodeID }

func (f fakeNodeSnapshotReader) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return f.snapshot.Clone(), f.err
}

type fakeNodeRuntimeSummaryReader struct {
	summaries map[uint64]NodeRuntimeSummary
	errs      map[uint64]error
}

func (f fakeNodeRuntimeSummaryReader) NodeRuntimeSummary(_ context.Context, nodeID uint64) (NodeRuntimeSummary, error) {
	if err := f.errs[nodeID]; err != nil {
		return NodeRuntimeSummary{}, err
	}
	summary := f.summaries[nodeID]
	if summary.NodeID == 0 {
		summary.NodeID = nodeID
	}
	return summary, nil
}

func freshReadyNodeHealth() control.NodeHealth {
	return control.NodeHealth{
		Status:       control.NodeAlive,
		Freshness:    control.NodeHealthFresh,
		RuntimeReady: true,
	}
}
