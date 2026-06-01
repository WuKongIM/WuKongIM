package presence

import (
	"errors"
	"testing"
)

func TestDirectoryRejectsStaleTarget(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 2}
	dir.BecomeAuthority(target)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute(current target) error = %v", err)
	}

	stale := target
	stale.AuthorityEpoch = 1
	if _, err := dir.EndpointsByUID(stale, "u1"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("EndpointsByUID(stale target) error = %v, want ErrNotLeader", err)
	}
	if _, err := dir.RegisterRoute(stale, Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 2, SessionID: 11, DeviceID: "d2", DeviceFlag: 1, DeviceLevel: 1}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("RegisterRoute(stale target) error = %v, want ErrNotLeader", err)
	}
}

func TestDirectoryUnregisterWithNewerOwnerSeqPreventsStaleRehydrate(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 2, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)

	route := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 5, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}
	if _, err := dir.RegisterRoute(target, route); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if err := dir.UnregisterRoute(target, RouteIdentity{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, SessionID: 10}, 7); err != nil {
		t.Fatalf("UnregisterRoute() error = %v", err)
	}

	results, err := dir.RehydrateRoutes(target, []Route{{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 6, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}})
	if err != nil {
		t.Fatalf("RehydrateRoutes() error = %v", err)
	}
	if len(results) != 1 || results[0].Accepted || results[0].Error == "" {
		t.Fatalf("rehydrate results = %#v, want stale rejection", results)
	}
	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("routes = %#v, want none after stale rehydrate", routes)
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

func TestDirectoryLeadershipEpochClearsOldRoutes(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	first := RouteTarget{HashSlot: 3, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(first)
	if _, err := dir.RegisterRoute(first, Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1}); err != nil {
		t.Fatalf("RegisterRoute(first) error = %v", err)
	}

	second := RouteTarget{HashSlot: 3, SlotID: 1, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 2}
	dir.BecomeAuthority(second)
	if routes, err := dir.EndpointsByUID(second, "u1"); err != nil || len(routes) != 0 {
		t.Fatalf("EndpointsByUID(new epoch) routes=%#v error=%v, want empty routes", routes, err)
	}
	if _, err := dir.EndpointsByUID(first, "u1"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("EndpointsByUID(old epoch) error = %v, want ErrNotLeader", err)
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
	if _, err := dir.EndpointsByUID(first, "u1"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("EndpointsByUID(old revision) error = %v, want ErrNotLeader", err)
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

func TestDirectoryRehydrateRoutesUsesConflictPath(t *testing.T) {
	existing := Route{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1}
	incoming := Route{UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, DeviceID: "new", DeviceFlag: 1, DeviceLevel: 1}
	dir := NewDirectory(DirectoryOptions{ShardCount: 4})
	target := RouteTarget{HashSlot: 5, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	dir.BecomeAuthority(target)
	if _, err := dir.RegisterRoute(target, existing); err != nil {
		t.Fatalf("RegisterRoute(existing) error = %v", err)
	}

	results, err := dir.RehydrateRoutes(target, []Route{incoming})
	if err != nil {
		t.Fatalf("RehydrateRoutes() error = %v", err)
	}
	if len(results) != 1 || !results[0].Accepted || results[0].PendingToken == "" || len(results[0].Actions) != 1 {
		t.Fatalf("rehydrate results = %#v, want accepted pending conflict action", results)
	}
	routes, err := dir.EndpointsByUID(target, "u1")
	if err != nil {
		t.Fatalf("EndpointsByUID() error = %v", err)
	}
	if len(routes) != 1 || routes[0].SessionID != existing.SessionID {
		t.Fatalf("routes = %#v, want existing route still active before commit", routes)
	}
}
