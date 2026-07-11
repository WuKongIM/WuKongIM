package presence

import (
	"errors"
	"strconv"
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

func TestDirectoryRouteRevisionUpdatePreservesPendingConflictCandidate(t *testing.T) {
	existing := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1}
	incoming := Route{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, DeviceID: "new", DeviceFlag: 1, DeviceLevel: 1}
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	first := RouteTarget{HashSlot: 3, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(first)
	if _, err := dir.RegisterRoute(first, existing); err != nil {
		t.Fatalf("RegisterRoute(existing) error = %v", err)
	}
	res, err := dir.RegisterRoute(first, incoming)
	if err != nil {
		t.Fatalf("RegisterRoute(incoming) error = %v", err)
	}
	if res.PendingToken == "" {
		t.Fatalf("register result = %#v, want pending token", res)
	}

	second := first
	second.RouteRevision = 2
	dir.BecomeAuthority(second)
	if err := dir.CommitRoute(second, res.PendingToken); err != nil {
		t.Fatalf("CommitRoute(after revision update) error = %v", err)
	}

	routes, err := dir.EndpointsByUID(second, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != incoming.SessionID {
		t.Fatalf("routes = %#v, want pending incoming route committed after revision update", routes)
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
	if _, err := dir.RegisterRoute(target, Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1, LastSeenUnix: 100}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if snap := dir.Snapshot(); snap.ExpiryIndexRoutes != 1 || snap.ExpiryIndexBuckets != 1 {
		t.Fatalf("snapshot before LoseAuthority = %#v, want one indexed route", snap)
	}
	dir.LoseAuthority(target.HashSlot)
	if snap := dir.Snapshot(); snap.Active != 0 || snap.ExpiryIndexRoutes != 0 || snap.ExpiryIndexBuckets != 0 {
		t.Fatalf("snapshot after LoseAuthority = %#v, want authority index dropped", snap)
	}

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

func TestDirectoryTouchMovesRouteOutOfOldExpiryBucket(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)
	route := Route{
		UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10,
		LastSeenUnix: 100,
	}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	touched := route
	touched.OwnerSeq = 2
	touched.LastSeenUnix = 200
	if err := dir.TouchRoutes(target, []Route{touched}); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}

	oldDeadline := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second)
	if oldDeadline.Expired != 0 || oldDeadline.Examined != 0 || oldDeadline.DueBuckets != 0 {
		t.Fatalf("expire at old deadline = %#v, want no old bucket candidates", oldDeadline)
	}
	if oldDeadline.IndexRoutes != 1 || oldDeadline.IndexBuckets != 1 {
		t.Fatalf("expire at old deadline = %#v, want one freshly indexed route", oldDeadline)
	}

	newDeadline := dir.ExpireRoutesDetailed(time.Unix(206, 0), 5*time.Second)
	if newDeadline.Expired != 1 || newDeadline.Examined != 1 || newDeadline.DueBuckets != 1 {
		t.Fatalf("expire at new deadline = %#v, want touched route expired", newDeadline)
	}
}

func TestDirectoryUnregisterUnschedulesExpiryAndPreservesTombstone(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)
	route := Route{
		UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10,
		LastSeenUnix: 100,
	}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if err := dir.UnregisterRoute(target, route.Identity(), route.OwnerSeq); err != nil {
		t.Fatalf("UnregisterRoute() error = %v", err)
	}

	snap := dir.Snapshot()
	if snap.Active != 0 || snap.ExpiryIndexRoutes != 0 || snap.ExpiryIndexBuckets != 0 {
		t.Fatalf("snapshot after unregister = %#v, want empty active and expiry state", snap)
	}
	touched := route
	touched.LastSeenUnix = 200
	if err := dir.TouchRoutes(target, []Route{touched}); err != nil {
		t.Fatalf("TouchRoutes(tombstoned route) error = %v", err)
	}
	if snap := dir.Snapshot(); snap.Active != 0 || snap.ExpiryIndexRoutes != 0 || snap.ExpiryIndexBuckets != 0 {
		t.Fatalf("snapshot after tombstoned touch = %#v, want route still absent", snap)
	}
	result := dir.ExpireRoutesDetailed(time.Unix(1_000, 0), time.Second)
	if result.Expired != 0 || result.Examined != 0 || result.DueBuckets != 0 {
		t.Fatalf("expire result = %#v, want no stale index candidates", result)
	}
}

func TestDirectoryExpiredRouteRejectsLowerOwnerSeqTouch(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)
	route := Route{
		UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10,
		LastSeenUnix: 100,
	}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if result := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second); result.Expired != 1 {
		t.Fatalf("initial expire result = %#v, want one expired route", result)
	}

	stale := route
	stale.OwnerSeq--
	stale.LastSeenUnix = 200
	if err := dir.TouchRoutes(target, []Route{stale}); err != nil {
		t.Fatalf("TouchRoutes(lower owner sequence) error = %v", err)
	}
	if snap := dir.Snapshot(); snap.Active != 0 || snap.ExpiryIndexRoutes != 0 || snap.ExpiryIndexBuckets != 0 {
		t.Fatalf("snapshot after lower owner sequence touch = %#v, want route still absent", snap)
	}
	routes, err := dir.EndpointsByUID(target, route.UID)
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("routes = %#v, want lower owner sequence fenced after TTL expiry", routes)
	}
}

func TestDirectoryExpiredRouteCanBeRecreatedByEqualOwnerSeqTouch(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)
	route := Route{
		UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10,
		LastSeenUnix: 100,
	}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if result := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second); result.Expired != 1 {
		t.Fatalf("initial expire result = %#v, want one expired route", result)
	}

	touched := route
	touched.LastSeenUnix = 200
	if err := dir.TouchRoutes(target, []Route{touched}); err != nil {
		t.Fatalf("TouchRoutes(expired route) error = %v", err)
	}
	snap := dir.Snapshot()
	if snap.Active != 1 || snap.ExpiryIndexRoutes != 1 || snap.ExpiryIndexBuckets != 1 {
		t.Fatalf("snapshot after fresh touch = %#v, want recreated indexed route", snap)
	}
	routes, err := dir.EndpointsByUID(target, route.UID)
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].LastSeenUnix != touched.LastSeenUnix || routes[0].OwnerSeq != route.OwnerSeq {
		t.Fatalf("routes = %#v, want equal owner sequence touch to recreate route", routes)
	}
}

func TestDirectoryExpiredRouteCanBeRecreatedByHigherOwnerSeqTouch(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 8, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)
	route := Route{
		UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10,
		LastSeenUnix: 100,
	}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if result := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second); result.Expired != 1 {
		t.Fatalf("initial expire result = %#v, want one expired route", result)
	}

	fresh := route
	fresh.OwnerSeq++
	fresh.LastSeenUnix = 200
	if err := dir.TouchRoutes(target, []Route{fresh}); err != nil {
		t.Fatalf("TouchRoutes(higher owner sequence) error = %v", err)
	}
	snap := dir.Snapshot()
	if snap.Active != 1 || snap.ExpiryIndexRoutes != 1 || snap.ExpiryIndexBuckets != 1 {
		t.Fatalf("snapshot after higher owner sequence touch = %#v, want recreated indexed route", snap)
	}
	routes, err := dir.EndpointsByUID(target, route.UID)
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].LastSeenUnix != fresh.LastSeenUnix || routes[0].OwnerSeq != fresh.OwnerSeq {
		t.Fatalf("routes = %#v, want higher owner sequence touch to recreate route", routes)
	}
}

func TestDirectoryRevisionOnlyAuthorityUpdatePreservesExpiryIndex(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	first := RouteTarget{
		HashSlot: 9, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3,
		ConfigEpoch: 4, RouteRevision: 10, AuthorityEpoch: 1,
	}
	dir.BecomeAuthority(first)
	if _, err := dir.RegisterRoute(first, Route{
		UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, LastSeenUnix: 100,
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}

	second := first
	second.RouteRevision++
	second.AuthorityEpoch++
	dir.BecomeAuthority(second)
	if snap := dir.Snapshot(); snap.Active != 1 || snap.ExpiryIndexRoutes != 1 || snap.ExpiryIndexBuckets != 1 {
		t.Fatalf("snapshot after revision update = %#v, want preserved index", snap)
	}
	result := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second)
	if result.Expired != 1 || result.Examined != 1 || result.DueBuckets != 1 {
		t.Fatalf("expire after revision update = %#v, want preserved route expired", result)
	}
}

func TestDirectoryAuthorityIdentityReplacementDropsExpiryIndex(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	first := RouteTarget{
		HashSlot: 9, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 3,
		ConfigEpoch: 4, RouteRevision: 10, AuthorityEpoch: 1,
	}
	dir.BecomeAuthority(first)
	if _, err := dir.RegisterRoute(first, Route{
		UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, LastSeenUnix: 100,
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}

	second := first
	second.LeaderTerm++
	second.RouteRevision++
	second.AuthorityEpoch++
	dir.BecomeAuthority(second)
	if snap := dir.Snapshot(); snap.Active != 0 || snap.ExpiryIndexRoutes != 0 || snap.ExpiryIndexBuckets != 0 {
		t.Fatalf("snapshot after authority replacement = %#v, want empty slot state", snap)
	}
	result := dir.ExpireRoutesDetailed(time.Unix(1_000, 0), time.Second)
	if result.Expired != 0 || result.Examined != 0 || result.DueBuckets != 0 {
		t.Fatalf("expire after authority replacement = %#v, want no old candidates", result)
	}
}

func TestDirectoryConflictReplacementUnschedulesOldRoute(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 9, SlotID: 2, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)
	existing := Route{
		UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10,
		DeviceID: "old", DeviceFlag: 1, DeviceLevel: deviceLevelMaster, LastSeenUnix: 100,
	}
	incoming := Route{
		UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20,
		DeviceID: "new", DeviceFlag: 1, DeviceLevel: deviceLevelMaster, LastSeenUnix: 200,
	}
	if _, err := dir.RegisterRoute(target, existing); err != nil {
		t.Fatalf("RegisterRoute(existing) error = %v", err)
	}
	registered, err := dir.RegisterRoute(target, incoming)
	if err != nil {
		t.Fatalf("RegisterRoute(incoming) error = %v", err)
	}
	if registered.PendingToken == "" {
		t.Fatalf("RegisterRoute(incoming) result = %#v, want pending conflict", registered)
	}
	if err := dir.CommitRoute(target, registered.PendingToken); err != nil {
		t.Fatalf("CommitRoute() error = %v", err)
	}

	atOldDeadline := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second)
	if atOldDeadline.Expired != 0 || atOldDeadline.Examined != 0 || atOldDeadline.DueBuckets != 0 {
		t.Fatalf("expire at old deadline = %#v, want old conflict unscheduled", atOldDeadline)
	}
	if atOldDeadline.IndexRoutes != 1 || atOldDeadline.IndexBuckets != 1 {
		t.Fatalf("expire at old deadline = %#v, want only incoming route indexed", atOldDeadline)
	}
	afterIncomingDeadline := dir.ExpireRoutesDetailed(time.Unix(206, 0), 5*time.Second)
	if afterIncomingDeadline.Expired != 1 || afterIncomingDeadline.Examined != 1 || afterIncomingDeadline.DueBuckets != 1 {
		t.Fatalf("expire after incoming deadline = %#v, want replacement expired", afterIncomingDeadline)
	}
}

func TestDirectoryExpireRoutesExaminesOnlyDueCandidates(t *testing.T) {
	const (
		freshRoutes = 100_000
		dueRoutes   = 10
	)
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 10, SlotID: 2, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)
	routes := make([]Route, 0, freshRoutes+dueRoutes)
	for i := 0; i < freshRoutes; i++ {
		routes = append(routes, Route{
			UID:          "fresh-" + strconv.Itoa(i),
			OwnerNodeID:  1,
			OwnerBootID:  1,
			OwnerSeq:     1,
			SessionID:    uint64(i + 1),
			LastSeenUnix: 1_000,
		})
	}
	for i := 0; i < dueRoutes; i++ {
		routes = append(routes, Route{
			UID:          "due-" + strconv.Itoa(i),
			OwnerNodeID:  1,
			OwnerBootID:  1,
			OwnerSeq:     1,
			SessionID:    uint64(freshRoutes + i + 1),
			LastSeenUnix: 100,
		})
	}
	if err := dir.TouchRoutes(target, routes); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}

	result := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second)
	if result.Examined != dueRoutes || result.Expired != dueRoutes || result.DueBuckets == 0 {
		t.Fatalf("expire result = %#v, want examined=10 expired=10", result)
	}
	if result.IndexRoutes != freshRoutes {
		t.Fatalf("expire result = %#v, want 100000 indexed fresh routes", result)
	}
	if snap := dir.Snapshot(); snap.Active != freshRoutes || snap.ExpiryIndexRoutes != freshRoutes {
		t.Fatalf("post-expiry snapshot = %#v, want 100000 active indexed fresh routes", snap)
	}
}
