package routing

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestRouterRoutesHashSlotZero(t *testing.T) {
	r := NewRouter()
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 2}})
	route, err := r.RouteHashSlot(0)
	if err != nil {
		t.Fatalf("RouteHashSlot() error = %v", err)
	}
	if route.HashSlot != 0 || route.SlotID != 1 || route.Leader != 2 {
		t.Fatalf("route = %#v, want hashSlot=0 slot=1 leader=2", route)
	}
	if route.PreferredLeader != 1 {
		t.Fatalf("PreferredLeader = %d, want snapshot preferred leader 1", route.PreferredLeader)
	}
}

func TestRouterRouteIncludesLeaderTermAndConfigEpoch(t *testing.T) {
	r := NewRouter()
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 9}})

	route, err := r.RouteHashSlot(0)
	if err != nil {
		t.Fatalf("RouteHashSlot() error = %v", err)
	}
	if route.LeaderTerm != 9 || route.ConfigEpoch != 1 {
		t.Fatalf("route = %#v, want leaderTerm=9 configEpoch=1", route)
	}
}

func TestRouterOldSnapshotIsImmutable(t *testing.T) {
	r := NewRouter()
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 9}})
	before := r.Table()
	next := testSnapshot()
	next.Revision++
	next.Slots[0].ConfigEpoch = 2
	next.Slots[0].DesiredPeers[1] = 9
	if err := r.UpdateControlSnapshot(next); err != nil {
		t.Fatalf("UpdateControlSnapshot(next) error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 10}})
	if before == r.Table() {
		t.Fatal("UpdateControlSnapshot reused the old table pointer")
	}
	if got := before.SlotPeers[1]; len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("old peers = %#v, want [1 2 3]", got)
	}
	if got := before.SlotLeaderTerms[1]; got != 9 {
		t.Fatalf("old leader term = %d, want 9", got)
	}
	if got := before.SlotConfigEpochs[1]; got != 1 {
		t.Fatalf("old config epoch = %d, want 1", got)
	}
}

func TestRouterRouteKeyUsesCRC32HashSlot(t *testing.T) {
	r := NewRouter()
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 1}, {SlotID: 2, Leader: 2}})
	key := keyForHashSlot(t, 3, 4)
	route, err := r.RouteKey(key)
	if err != nil {
		t.Fatalf("RouteKey() error = %v", err)
	}
	if route.HashSlot != 3 || route.SlotID != 2 || route.Leader != 2 {
		t.Fatalf("route = %#v, want hashSlot=3 slot=2 leader=2", route)
	}
}

func TestRouterRouteKeysPreservesInputOrder(t *testing.T) {
	r := NewRouter()
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 1}, {SlotID: 2, Leader: 2}})
	first := keyForHashSlot(t, 3, 4)
	second := keyForHashSlot(t, 0, 4)

	routes, err := r.RouteKeys([]string{first, second})
	if err != nil {
		t.Fatalf("RouteKeys() error = %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	if routes[0].HashSlot != 3 || routes[0].SlotID != 2 || routes[0].Leader != 2 {
		t.Fatalf("first route = %#v, want hashSlot=3 slot=2 leader=2", routes[0])
	}
	if routes[1].HashSlot != 0 || routes[1].SlotID != 1 || routes[1].Leader != 1 {
		t.Fatalf("second route = %#v, want hashSlot=0 slot=1 leader=1", routes[1])
	}
}

func TestRouterIgnoresZeroLeaderObservation(t *testing.T) {
	r := NewRouter()
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 9}, {SlotID: 2, Leader: 2, LeaderTerm: 8}})
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 0, LeaderTerm: 10}})

	route, err := r.RouteHashSlot(0)
	if err != nil {
		t.Fatalf("RouteHashSlot() error = %v", err)
	}
	if route.Leader != 1 {
		t.Fatalf("Leader = %d, want previous leader 1 to remain installed", route.Leader)
	}
	if route.LeaderTerm != 9 {
		t.Fatalf("LeaderTerm = %d, want previous term 9 to remain installed", route.LeaderTerm)
	}
}

func TestRouterRouteKeysErrorIncludesKeyAndHashSlot(t *testing.T) {
	r := NewRouter()
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 1}})
	first := keyForHashSlot(t, 0, 4)
	second := keyForHashSlot(t, 3, 4)

	_, err := r.RouteKeys([]string{first, second})
	if !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteKeys() error = %v, want ErrNoSlotLeader", err)
	}
	msg := err.Error()
	for _, want := range []string{"index=1", `key="` + second + `"`, "hashSlot=3"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("RouteKeys() error = %q, want %q", msg, want)
		}
	}
}

func TestRouterReturnsTypedErrors(t *testing.T) {
	r := NewRouter()
	if _, err := r.RouteHashSlot(0); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("RouteHashSlot() error = %v, want ErrRouteNotReady", err)
	}
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	if _, err := r.RouteHashSlot(0); !errors.Is(err, ErrNoSlotLeader) {
		t.Fatalf("RouteHashSlot() error = %v, want ErrNoSlotLeader", err)
	}
}

func TestRouterRouteSlotValidatesHashSlotOwnership(t *testing.T) {
	r := NewRouter()
	if err := r.UpdateControlSnapshot(testSnapshot()); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	r.UpdateSlotLeaders([]SlotStatus{{SlotID: 1, Leader: 1}, {SlotID: 2, Leader: 2}})
	if _, err := r.RouteSlot(1, 3); !errors.Is(err, ErrRouteMismatch) {
		t.Fatalf("RouteSlot() error = %v, want ErrRouteMismatch", err)
	}
}

func keyForHashSlot(t *testing.T, want uint16, count uint16) string {
	t.Helper()
	for i := 0; i < 10000; i++ {
		key := "key-" + strconv.Itoa(i)
		if HashSlotForKey(key, count) == want {
			return key
		}
	}
	t.Fatalf("no key found for hash slot %d", want)
	return ""
}

func testSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision:     10,
		ControllerID: 1,
		Nodes: []control.Node{
			{NodeID: 1, Addr: "127.0.0.1:1001", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 2, Addr: "127.0.0.1:1002", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 3, Addr: "127.0.0.1:1003", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
			{NodeID: 9, Addr: "127.0.0.1:1009", Roles: []control.Role{control.RoleData}, Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: 1},
			{SlotID: 2, DesiredPeers: []uint64{2, 3, 9}, ConfigEpoch: 1, PreferredLeader: 2},
		},
		HashSlots: control.HashSlotTable{Revision: 10, Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 1, SlotID: 1}, {From: 2, To: 3, SlotID: 2}}},
	}
}
