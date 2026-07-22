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
