package presence

import (
	"errors"
	"testing"
	"time"
)

func TestDirectoryRejectsStaleTarget(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 2, AuthorityEpoch: 2}
	dir.BecomeAuthority(target)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute(current target) error = %v", err)
	}

	stale := target
	stale.LeaderTerm = 8
	if _, err := dir.EndpointsByUID(stale, "u1"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("EndpointsByUID(stale target) error = %v, want ErrNotLeader", err)
	}
	if _, err := dir.RegisterRoute(stale, Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 2, SessionID: 11, DeviceID: "d2", DeviceFlag: 1, DeviceLevel: 1}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("RegisterRoute(stale target) error = %v, want ErrNotLeader", err)
	}
}

func TestDirectoryAcceptsDifferentLocalAuthorityEpochWithSameRaftIdentity(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{LocalNodeID: 1})
	installed := RouteTarget{
		HashSlot: 1, SlotID: 2, LeaderNodeID: 1,
		LeaderTerm: 9, ConfigEpoch: 3,
		RouteRevision: 10, AuthorityEpoch: 1,
	}
	remote := installed
	remote.AuthorityEpoch = 4
	dir.BecomeAuthority(installed)

	if err := dir.TouchRoutes(remote, []Route{{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10}}); err != nil {
		t.Fatalf("TouchRoutes() error = %v, want same raft identity accepted", err)
	}
}

func TestDirectoryRejectsOldLeaderTerm(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{LocalNodeID: 1})
	target := RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	stale := target
	stale.LeaderTerm = 8
	if err := dir.TouchRoutes(stale, []Route{{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10}}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("TouchRoutes(old leader term) error = %v, want ErrNotLeader", err)
	}
}

func TestDirectoryRejectsDifferentConfigEpoch(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{LocalNodeID: 1})
	target := RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	stale := target
	stale.ConfigEpoch = 2
	if err := dir.TouchRoutes(stale, []Route{{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10}}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("TouchRoutes(different config epoch) error = %v, want ErrNotLeader", err)
	}
}

func TestDirectoryTouchRoutesIgnoresExplicitUnregisterTombstoneAtSameOwnerSeq(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 2, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1, ConnectedUnix: 100, LastSeenUnix: 100}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if err := dir.UnregisterRoute(target, RouteIdentity{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, SessionID: 10}, 5); err != nil {
		t.Fatalf("UnregisterRoute() error = %v", err)
	}

	touched := route
	touched.LastSeenUnix = 200
	if err := dir.TouchRoutes(target, []Route{touched}); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}
	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("routes = %#v, want none after tombstoned touch", routes)
	}
}

func TestDirectoryTouchRoutesRefreshesExistingRoute(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 6, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp-a", ConnectedUnix: 100, LastSeenUnix: 100}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	touched := route
	touched.LastSeenUnix = 200
	touched.Listener = "tcp-b"

	if err := dir.TouchRoutes(target, []Route{touched}); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}

	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].LastSeenUnix != 200 || routes[0].Listener != "tcp-b" {
		t.Fatalf("routes = %#v, want touched route metadata", routes)
	}
}

func TestDirectoryTouchRoutesDoesNotMoveLastSeenBackward(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 6, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1, ConnectedUnix: 100, LastSeenUnix: 200}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	delayed := route
	delayed.LastSeenUnix = 150
	delayed.Listener = "old-touch"

	if err := dir.TouchRoutes(target, []Route{delayed}); err != nil {
		t.Fatalf("TouchRoutes(delayed) error = %v", err)
	}

	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].LastSeenUnix != 200 {
		t.Fatalf("routes = %#v, want LastSeenUnix preserved at 200", routes)
	}
}

func TestDirectoryTouchRoutesRecreatesMissingRoute(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 6, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1, ConnectedUnix: 100, LastSeenUnix: 200}
	if err := dir.TouchRoutes(target, []Route{route}); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}

	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != route.SessionID || routes[0].LastSeenUnix != 200 {
		t.Fatalf("routes = %#v, want touched missing route recreated", routes)
	}
}

func TestDirectorySnapshotCountsAuthorityRoutesByHashSlotAndCounters(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 2})
	targetOne := RouteTarget{HashSlot: 6, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	targetTwo := RouteTarget{HashSlot: 7, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(targetOne)
	dir.BecomeAuthority(targetTwo)

	oldRoute := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, ConnectedUnix: 100, LastSeenUnix: 100}
	freshRoute := Route{UID: "u2", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 11, ConnectedUnix: 100, LastSeenUnix: 200}
	if _, err := dir.RegisterRoute(targetOne, oldRoute); err != nil {
		t.Fatalf("RegisterRoute(old) error = %v", err)
	}
	if err := dir.TouchRoutes(targetOne, []Route{freshRoute}); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}
	if err := dir.TouchRoutes(targetTwo, []Route{{UID: "u3", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 12, ConnectedUnix: 200, LastSeenUnix: 200}}); err != nil {
		t.Fatalf("TouchRoutes(second slot) error = %v", err)
	}
	removed := dir.ExpireRoutes(time.Unix(120, 0), 10*time.Second)
	if removed != 1 {
		t.Fatalf("ExpireRoutes removed = %d, want 1", removed)
	}

	snap := dir.Snapshot()

	if snap.Active != 2 {
		t.Fatalf("active = %d, want 2", snap.Active)
	}
	if snap.ByHashSlot[6] != 1 || snap.ByHashSlot[7] != 1 {
		t.Fatalf("by hash slot = %#v, want one active route per slot", snap.ByHashSlot)
	}
	if snap.TouchRoutesTotal != 2 {
		t.Fatalf("touch routes total = %d, want 2", snap.TouchRoutesTotal)
	}
	if snap.ExpiredRoutesTotal != 1 {
		t.Fatalf("expired routes total = %d, want 1", snap.ExpiredRoutesTotal)
	}
}

func TestDirectoryUnregisterRequiresExactUIDIdentity(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 2, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if err := dir.UnregisterRoute(target, RouteIdentity{UID: "u2", OwnerNodeID: 1, OwnerBootID: 1, SessionID: 10}, 7); err != nil {
		t.Fatalf("UnregisterRoute(wrong uid) error = %v", err)
	}

	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != 10 {
		t.Fatalf("routes = %#v, want original route still active", routes)
	}
}

func TestDirectoryConflictActionFailureLeavesExistingRouteActive(t *testing.T) {
	existing := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1}
	incoming := Route{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, DeviceID: "new", DeviceFlag: 1, DeviceLevel: 1}
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)
	if _, err := dir.RegisterRoute(target, existing); err != nil {
		t.Fatalf("RegisterRoute(existing) error = %v", err)
	}
	res, err := dir.RegisterRoute(target, incoming)
	if err != nil {
		t.Fatalf("RegisterRoute(incoming) error = %v", err)
	}
	if len(res.Actions) != 1 || res.PendingToken == "" {
		t.Fatalf("register result = %#v, want one action and pending token", res)
	}
	if err := dir.AbortRoute(target, res.PendingToken); err != nil {
		t.Fatalf("AbortRoute() error = %v", err)
	}
	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != existing.SessionID {
		t.Fatalf("routes = %#v, want existing active route", routes)
	}
}

func TestDirectoryCommitRouteReplacesConflictingActiveRoute(t *testing.T) {
	existing := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1}
	incoming := Route{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, DeviceID: "new", DeviceFlag: 1, DeviceLevel: 1}
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)
	if _, err := dir.RegisterRoute(target, existing); err != nil {
		t.Fatalf("RegisterRoute(existing) error = %v", err)
	}
	res, err := dir.RegisterRoute(target, incoming)
	if err != nil {
		t.Fatalf("RegisterRoute(incoming) error = %v", err)
	}
	if res.PendingToken == "" {
		t.Fatalf("register result = %#v, want pending token", res)
	}
	if err := dir.CommitRoute(target, res.PendingToken); err != nil {
		t.Fatalf("CommitRoute() error = %v", err)
	}
	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != incoming.SessionID {
		t.Fatalf("routes = %#v, want incoming active route", routes)
	}
}

func TestDirectoryCommitRouteRejectsNewUnacknowledgedConflicts(t *testing.T) {
	existing := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1}
	firstPending := Route{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, DeviceID: "new-1", DeviceFlag: 1, DeviceLevel: 1}
	secondPending := Route{UID: "u1", OwnerNodeID: 3, OwnerBootID: 1, OwnerSeq: 1, SessionID: 30, DeviceID: "new-2", DeviceFlag: 1, DeviceLevel: 1}
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)
	if _, err := dir.RegisterRoute(target, existing); err != nil {
		t.Fatalf("RegisterRoute(existing) error = %v", err)
	}
	first, err := dir.RegisterRoute(target, firstPending)
	if err != nil {
		t.Fatalf("RegisterRoute(firstPending) error = %v", err)
	}
	second, err := dir.RegisterRoute(target, secondPending)
	if err != nil {
		t.Fatalf("RegisterRoute(secondPending) error = %v", err)
	}
	if err := dir.CommitRoute(target, first.PendingToken); err != nil {
		t.Fatalf("CommitRoute(first) error = %v", err)
	}
	if err := dir.CommitRoute(target, second.PendingToken); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("CommitRoute(second) error = %v, want ErrRouteNotReady", err)
	}

	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != firstPending.SessionID {
		t.Fatalf("routes = %#v, want only first pending active", routes)
	}
}

func TestDirectoryCommitRouteRejectsSupersededPendingRoute(t *testing.T) {
	existing := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1}
	incoming := Route{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, DeviceID: "new", DeviceFlag: 1, DeviceLevel: 1}
	newerIncoming := incoming
	newerIncoming.OwnerSeq = 2
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)
	if _, err := dir.RegisterRoute(target, existing); err != nil {
		t.Fatalf("RegisterRoute(existing) error = %v", err)
	}
	res, err := dir.RegisterRoute(target, incoming)
	if err != nil {
		t.Fatalf("RegisterRoute(incoming) error = %v", err)
	}
	if _, err := dir.RegisterRoute(target, newerIncoming); err != nil {
		t.Fatalf("RegisterRoute(newerIncoming) error = %v", err)
	}
	if err := dir.CommitRoute(target, res.PendingToken); !errors.Is(err, ErrStaleRoute) {
		t.Fatalf("CommitRoute(stale pending) error = %v, want ErrStaleRoute", err)
	}
	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != existing.SessionID {
		t.Fatalf("routes = %#v, want existing active route", routes)
	}
}

func TestDirectoryLeadershipIdentityClearsOldRoutes(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	first := RouteTarget{HashSlot: 3, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(first)
	if _, err := dir.RegisterRoute(first, Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}); err != nil {
		t.Fatalf("RegisterRoute(first) error = %v", err)
	}

	second := RouteTarget{HashSlot: 3, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 10, ConfigEpoch: 3, RouteRevision: 2, AuthorityEpoch: 2}
	dir.BecomeAuthority(second)
	if routes, err := dir.EndpointsByUID(second, "u1"); err != nil || len(routes) != 0 {
		t.Fatalf("EndpointsByUID(new identity) routes=%#v error=%v, want empty routes", routes, err)
	}
	if _, err := dir.EndpointsByUID(first, "u1"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("EndpointsByUID(old identity) error = %v, want ErrNotLeader", err)
	}
}

func TestDirectoryRouteRevisionUpdatePreservesAuthorityState(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	first := RouteTarget{HashSlot: 3, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(first)
	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}
	if _, err := dir.RegisterRoute(first, route); err != nil {
		t.Fatalf("RegisterRoute(first) error = %v", err)
	}

	second := first
	second.RouteRevision = 2
	dir.BecomeAuthority(second)
	routes, err := dir.EndpointsByUID(second, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID(new revision) error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != route.SessionID {
		t.Fatalf("routes = %#v, want existing route preserved", routes)
	}
	if routes, err := dir.EndpointsByUID(first, "u1"); err != nil || len(routes) != 1 {
		t.Fatalf("EndpointsByUID(old revision) routes=%#v error=%v, want preserved route", routes, err)
	}
	lateRoute := Route{UID: "u2", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 11, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}
	if _, err := dir.RegisterRoute(first, lateRoute); err != nil {
		t.Fatalf("RegisterRoute(old revision same identity) error = %v", err)
	}
}

func TestDirectoryAcceptsSameIdentityTargetBeforeRevisionUpdateEvent(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	current := RouteTarget{HashSlot: 3, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	next := current
	next.RouteRevision = 2
	dir.BecomeAuthority(current)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}
	if _, err := dir.RegisterRoute(next, route); err != nil {
		t.Fatalf("RegisterRoute(new revision same identity before BecomeAuthority) error = %v", err)
	}
}

func TestDirectoryLoseAuthorityRejectsOldTarget(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 4, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)
	if _, err := dir.RegisterRoute(target, Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	dir.LoseAuthority(target.HashSlot)

	if _, err := dir.EndpointsByUID(target, "u1"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("EndpointsByUID(after LoseAuthority) error = %v, want ErrNotLeader", err)
	}
}

func TestDirectoryTouchRoutesIgnoresConflictingMissingRoute(t *testing.T) {
	existing := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1}
	incoming := Route{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, DeviceID: "new", DeviceFlag: 1, DeviceLevel: 1}
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 5, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)
	if _, err := dir.RegisterRoute(target, existing); err != nil {
		t.Fatalf("RegisterRoute(existing) error = %v", err)
	}

	if err := dir.TouchRoutes(target, []Route{incoming}); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}
	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != existing.SessionID {
		t.Fatalf("routes = %#v, want existing route still active after conflicting touch", routes)
	}
}

func TestDirectoryExpireRoutesRemovesUntouchedRoutes(t *testing.T) {
	now := time.Unix(1_000, 0)
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 7, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	stale := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1, ConnectedUnix: now.Add(-10 * time.Second).Unix()}
	fresh := Route{UID: "u2", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, DeviceID: "new", DeviceFlag: 1, DeviceLevel: 1, ConnectedUnix: now.Add(-10 * time.Second).Unix(), LastSeenUnix: now.Add(-2 * time.Second).Unix()}
	if _, err := dir.RegisterRoute(target, stale); err != nil {
		t.Fatalf("RegisterRoute(stale) error = %v", err)
	}
	if _, err := dir.RegisterRoute(target, fresh); err != nil {
		t.Fatalf("RegisterRoute(fresh) error = %v", err)
	}

	if removed := dir.ExpireRoutes(now, 5*time.Second); removed != 1 {
		t.Fatalf("ExpireRoutes() removed = %d, want 1", removed)
	}
	staleRoutes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID(stale) error = %v", err)
	}
	if len(staleRoutes) != 0 {
		t.Fatalf("stale routes = %#v, want none", staleRoutes)
	}
	freshRoutes, err := dir.EndpointsByUID(target, "u2")
	if err != nil {
		t.Fatalf("EndpointsByUID(fresh) error = %v", err)
	}
	if len(freshRoutes) != 1 || freshRoutes[0].SessionID != fresh.SessionID {
		t.Fatalf("fresh routes = %#v, want fresh route preserved", freshRoutes)
	}
}

func TestDirectoryExpireRoutesKeepsUntimestampedRoutes(t *testing.T) {
	now := time.Unix(1_000, 0)
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 7, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}

	if removed := dir.ExpireRoutes(now, 5*time.Second); removed != 0 {
		t.Fatalf("ExpireRoutes() removed = %d, want 0", removed)
	}
	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %#v, want untimestamped route preserved", routes)
	}
}
