package cluster

import (
	"context"
	"errors"
	"testing"

	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestPresenceDirectoryAuthorityPreservesAlignedTargetResults(t *testing.T) {
	directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{LocalNodeID: 1, ShardCount: 4})
	first := presenceusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3, RouteRevision: 10, AuthorityEpoch: 11}
	second := presenceusecase.RouteTarget{HashSlot: 5, SlotID: 6, LeaderNodeID: 1, LeaderTerm: 10, ConfigEpoch: 4, RouteRevision: 12, AuthorityEpoch: 13}
	directory.BecomeAuthority(first)
	directory.BecomeAuthority(second)
	firstRoute := presenceusecase.Route{UID: "first", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20}
	secondRoute := presenceusecase.Route{UID: "second", OwnerNodeID: 3, OwnerBootID: 1, OwnerSeq: 1, SessionID: 30}
	if _, err := directory.RegisterRoute(first, firstRoute); err != nil {
		t.Fatalf("RegisterRoute(first) error = %v", err)
	}
	if _, err := directory.RegisterRoute(second, secondRoute); err != nil {
		t.Fatalf("RegisterRoute(second) error = %v", err)
	}

	stale := first
	stale.LeaderTerm--
	adapter := NewPresenceDirectoryAuthority(directory)
	results := adapter.EndpointsByTargets(context.Background(), []presenceusecase.EndpointLookupGroup{
		{Target: second, UIDs: []string{"second"}},
		{Target: stale, UIDs: []string{"first"}},
		{Target: first, UIDs: []string{"first"}},
	})

	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if results[0].Err != nil || len(results[0].Routes) != 1 || results[0].Routes[0] != secondRoute {
		t.Fatalf("second target result = %#v", results[0])
	}
	if !errors.Is(results[1].Err, authoritypresence.ErrNotLeader) {
		t.Fatalf("stale target error = %v, want ErrNotLeader", results[1].Err)
	}
	if results[2].Err != nil || len(results[2].Routes) != 1 || results[2].Routes[0] != firstRoute {
		t.Fatalf("first target result = %#v", results[2])
	}
}

func TestPresenceDirectoryAuthorityFailsClosedWithoutDirectory(t *testing.T) {
	adapter := NewPresenceDirectoryAuthority(nil)
	target := presenceusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, LeaderTerm: 9, ConfigEpoch: 3}
	results := adapter.EndpointsByTargets(context.Background(), []presenceusecase.EndpointLookupGroup{{Target: target, UIDs: []string{"u1"}}})
	if len(results) != 1 || !errors.Is(results[0].Err, authoritypresence.ErrRouteNotReady) {
		t.Fatalf("nil directory result = %#v, want route not ready", results)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results = adapter.EndpointsByTargets(ctx, []presenceusecase.EndpointLookupGroup{{Target: target, UIDs: []string{"u1"}}})
	if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("canceled result = %#v, want context canceled", results)
	}
}

func TestPresenceDirectoryAuthorityPreservesPendingCommitAbortAndOwnerFenceLifecycle(t *testing.T) {
	t.Parallel()

	directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{LocalNodeID: 1, ShardCount: 4})
	target := presenceusecase.RouteTarget{
		HashSlot: 7, SlotID: 3, LeaderNodeID: 1, LeaderTerm: 9,
		ConfigEpoch: 4, RouteRevision: 12, AuthorityEpoch: 2,
	}
	directory.BecomeAuthority(target)
	authority := NewPresenceDirectoryAuthority(directory)
	first := presenceusecase.Route{
		UID: "u1", OwnerNodeID: 2, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10,
		DeviceID: "old", DeviceFlag: 1, DeviceLevel: 1, LastSeenUnix: 100,
	}
	if result, err := authority.RegisterRoute(context.Background(), target, first); err != nil || result.PendingToken != "" {
		t.Fatalf("RegisterRoute(first) = %#v err=%v", result, err)
	}

	routes, err := authority.EndpointsByUID(context.Background(), target, "u1")
	if err != nil || len(routes) != 1 || routes[0].SessionID != 10 {
		t.Fatalf("EndpointsByUID(first) = %#v err=%v", routes, err)
	}
	routes, err = authority.EndpointsByUIDs(context.Background(), target, []string{"u1", "missing"})
	if err != nil || len(routes) != 1 || routes[0].SessionID != 10 {
		t.Fatalf("EndpointsByUIDs() = %#v err=%v", routes, err)
	}

	touched := first
	touched.LastSeenUnix = 200
	if err := authority.TouchRoutes(context.Background(), target, []presenceusecase.Route{touched}); err != nil {
		t.Fatalf("TouchRoutes() error = %v", err)
	}
	routes, err = authority.EndpointsByUID(context.Background(), target, "u1")
	if err != nil || len(routes) != 1 || routes[0].LastSeenUnix != 200 {
		t.Fatalf("EndpointsByUID(touched) = %#v err=%v", routes, err)
	}

	incoming := presenceusecase.Route{
		UID: "u1", OwnerNodeID: 3, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20,
		DeviceID: "new", DeviceFlag: 1, DeviceLevel: 1, LastSeenUnix: 300,
	}
	pending, err := authority.RegisterRoute(context.Background(), target, incoming)
	if err != nil || pending.PendingToken == "" || len(pending.Actions) != 1 {
		t.Fatalf("RegisterRoute(conflict) = %#v err=%v", pending, err)
	}
	if err := authority.AbortRoute(context.Background(), target, string(pending.PendingToken)); err != nil {
		t.Fatalf("AbortRoute() error = %v", err)
	}
	routes, err = authority.EndpointsByUID(context.Background(), target, "u1")
	if err != nil || len(routes) != 1 || routes[0].SessionID != 10 {
		t.Fatalf("EndpointsByUID(after abort) = %#v err=%v", routes, err)
	}

	pending, err = authority.RegisterRoute(context.Background(), target, incoming)
	if err != nil || pending.PendingToken == "" {
		t.Fatalf("RegisterRoute(second conflict) = %#v err=%v", pending, err)
	}
	if err := authority.CommitRoute(context.Background(), target, string(pending.PendingToken)); err != nil {
		t.Fatalf("CommitRoute() error = %v", err)
	}
	routes, err = authority.EndpointsByUID(context.Background(), target, "u1")
	if err != nil || len(routes) != 1 || routes[0].SessionID != 20 {
		t.Fatalf("EndpointsByUID(after commit) = %#v err=%v", routes, err)
	}

	identity := presenceusecase.RouteIdentity{
		UID: incoming.UID, OwnerNodeID: incoming.OwnerNodeID, OwnerBootID: incoming.OwnerBootID, SessionID: incoming.SessionID,
	}
	if err := authority.UnregisterRoute(context.Background(), target, identity, incoming.OwnerSeq); err != nil {
		t.Fatalf("UnregisterRoute() error = %v", err)
	}
	routes, err = authority.EndpointsByUID(context.Background(), target, "u1")
	if err != nil || len(routes) != 0 {
		t.Fatalf("EndpointsByUID(after unregister) = %#v err=%v", routes, err)
	}
}
