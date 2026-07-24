package delivery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestChannelAppendPresenceResolverPreservesExactTargetsAndPartialResults(t *testing.T) {
	first := channelappend.RecipientAuthorityTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}
	second := channelappend.RecipientAuthorityTarget{HashSlot: 2, SlotID: 22, LeaderNodeID: 20, LeaderTerm: 202, ConfigEpoch: 2002, RouteRevision: 200, AuthorityEpoch: 2000}
	firstErr := errors.New("first target unavailable")
	authority := &targetedPresenceAuthorityForChannelAppendTest{results: []presenceusecase.EndpointLookupResult{
		{Err: firstErr},
		{Routes: []presenceusecase.Route{{UID: "u2", OwnerNodeID: 3, OwnerBootID: 4, OwnerSeq: 5, SessionID: 6}}},
	}}
	resolver := NewChannelAppendPresenceResolver(presenceusecase.New(presenceusecase.Options{Authority: authority}))

	got := resolver.EndpointsByTargets(context.Background(), []channelappend.RecipientTargetBatch{
		{Target: first, Recipients: []channelappend.Recipient{{UID: "u1"}}},
		{Target: second, Recipients: []channelappend.Recipient{{UID: "u2"}}},
	})

	if len(got) != 2 || !errors.Is(got[0].Err, firstErr) || got[1].Err != nil {
		t.Fatalf("target results = %#v, want first error and second success", got)
	}
	if len(got[1].Routes) != 1 || got[1].Routes[0].UID != "u2" || got[1].Routes[0].OwnerNodeID != 3 {
		t.Fatalf("second target routes = %#v, want converted u2 route", got[1].Routes)
	}
	wantGroups := []presenceusecase.EndpointLookupGroup{
		{Target: presenceRouteTargetFromRecipientTargetForChannelAppendTest(first), UIDs: []string{"u1"}},
		{Target: presenceRouteTargetFromRecipientTargetForChannelAppendTest(second), UIDs: []string{"u2"}},
	}
	if !reflect.DeepEqual(authority.groups, wantGroups) {
		t.Fatalf("presence groups = %#v, want exact targets %#v", authority.groups, wantGroups)
	}
	if authority.legacyCalls != 0 {
		t.Fatalf("legacy endpoint calls = %d, want 0", authority.legacyCalls)
	}
}

type targetedPresenceAuthorityForChannelAppendTest struct {
	groups      []presenceusecase.EndpointLookupGroup
	results     []presenceusecase.EndpointLookupResult
	legacyCalls int
}

func (a *targetedPresenceAuthorityForChannelAppendTest) RegisterRoute(context.Context, presenceusecase.Route) (presenceusecase.RegisterResult, error) {
	return presenceusecase.RegisterResult{}, nil
}

func (a *targetedPresenceAuthorityForChannelAppendTest) CommitRoute(context.Context, presenceusecase.PendingRouteToken) error {
	return nil
}

func (a *targetedPresenceAuthorityForChannelAppendTest) AbortRoute(context.Context, presenceusecase.PendingRouteToken) error {
	return nil
}

func (a *targetedPresenceAuthorityForChannelAppendTest) EnqueueUnregister(context.Context, presenceusecase.RouteIdentity, uint64) {
}

func (a *targetedPresenceAuthorityForChannelAppendTest) EndpointsByUID(context.Context, string) ([]presenceusecase.Route, error) {
	a.legacyCalls++
	return nil, nil
}

func (a *targetedPresenceAuthorityForChannelAppendTest) EndpointsByTargets(_ context.Context, groups []presenceusecase.EndpointLookupGroup) []presenceusecase.EndpointLookupResult {
	a.groups = make([]presenceusecase.EndpointLookupGroup, len(groups))
	for i, group := range groups {
		a.groups[i] = presenceusecase.EndpointLookupGroup{Target: group.Target, UIDs: append([]string(nil), group.UIDs...)}
	}
	return append([]presenceusecase.EndpointLookupResult(nil), a.results...)
}

func presenceRouteTargetFromRecipientTargetForChannelAppendTest(target channelappend.RecipientAuthorityTarget) presenceusecase.RouteTarget {
	return presenceusecase.RouteTarget{
		HashSlot:       target.HashSlot,
		SlotID:         target.SlotID,
		LeaderNodeID:   target.LeaderNodeID,
		LeaderTerm:     target.LeaderTerm,
		ConfigEpoch:    target.ConfigEpoch,
		RouteRevision:  target.RouteRevision,
		AuthorityEpoch: target.AuthorityEpoch,
	}
}
