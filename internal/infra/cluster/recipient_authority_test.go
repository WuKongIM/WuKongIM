package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestNewRecipientAuthorityResolverRejectsUnsupportedNode(t *testing.T) {
	if resolver := NewRecipientAuthorityResolver(struct{}{}, nil); resolver != nil {
		t.Fatalf("NewRecipientAuthorityResolver() = %#v, want nil for unsupported node", resolver)
	}
}

func TestRecipientAuthorityResolverPrefersAlignedLightweightAuthorityBatch(t *testing.T) {
	secondErr := errors.New("second route unavailable")
	legacy := &batchRecipientRouteNodeForRecipientAuthorityTest{
		routes: map[string]pkgcluster.Route{
			"u1": {HashSlot: 99, SlotID: 99, Leader: 99},
			"u2": {HashSlot: 99, SlotID: 99, Leader: 99},
		},
	}
	node := &authorityRecipientRouteNodeForRecipientAuthorityTest{
		batchRecipientRouteNodeForRecipientAuthorityTest: legacy,
		authorities: map[string]pkgcluster.RouteAuthority{
			"u1": {HashSlot: 1, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000},
			"u2": {HashSlot: 2, SlotID: 22, LeaderNodeID: 20, LeaderTerm: 202, ConfigEpoch: 2002, RouteRevision: 200, AuthorityEpoch: 2000},
		},
		authorityErrs: map[string]error{"u2": secondErr},
	}
	observer := &recordingRecipientAuthorityResolveObserver{}
	resolver := NewRecipientAuthorityResolver(node, observer)

	got, err := resolver.ResolveRecipientAuthorities(context.Background(), []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("ResolveRecipientAuthorities() error = %v", err)
	}

	if node.authorityCalls != 1 {
		t.Fatalf("RouteAuthoritiesPartial calls = %d, want 1", node.authorityCalls)
	}
	if legacy.batchCalls != 0 || legacy.singleCalls != 0 {
		t.Fatalf("legacy route calls = batch:%d single:%d, want zero", legacy.batchCalls, legacy.singleCalls)
	}
	want := []channelappend.RecipientAuthorityResult{
		{Target: channelappend.RecipientAuthorityTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}},
		{Err: secondErr},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
	if len(observer.events) != 1 {
		t.Fatalf("recipient authority observations = %#v, want exactly one batch event", observer.events)
	}
	event := observer.events[0]
	if event.Result != RecipientAuthorityResolveResultPartial || event.Items != 2 || event.Targets != 1 || event.Duration < 0 {
		t.Fatalf("recipient authority observation = %#v, want partial items=2 targets=1", event)
	}
}

func TestRecipientAuthorityResolverUsesBatchRouteNode(t *testing.T) {
	node := &batchRecipientRouteNodeForRecipientAuthorityTest{
		routes: map[string]pkgcluster.Route{
			"u1": {HashSlot: 1, SlotID: 11, Leader: 10, LeaderTerm: 101, ConfigEpoch: 1001, Revision: 100, AuthorityEpoch: 1000},
			"u2": {HashSlot: 2, SlotID: 22, Leader: 20, LeaderTerm: 202, ConfigEpoch: 2002, Revision: 200, AuthorityEpoch: 2000},
		},
	}
	resolver := NewRecipientAuthorityResolver(node, nil)

	got, err := resolver.ResolveRecipientAuthorities(context.Background(), []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("ResolveRecipientAuthorities() error = %v", err)
	}

	if node.singleCalls != 0 {
		t.Fatalf("RouteKey calls = %d, want 0 when RouteKeys is available", node.singleCalls)
	}
	if node.batchCalls != 1 {
		t.Fatalf("RouteKeys calls = %d, want 1", node.batchCalls)
	}
	if !reflect.DeepEqual(node.batchKeys, []string{"u1", "u2"}) {
		t.Fatalf("RouteKeys keys = %#v, want u1,u2", node.batchKeys)
	}
	want := []channelappend.RecipientAuthorityResult{
		{Target: channelappend.RecipientAuthorityTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}},
		{Target: channelappend.RecipientAuthorityTarget{HashSlot: 2, SlotID: 22, LeaderNodeID: 20, LeaderTerm: 202, ConfigEpoch: 2002, RouteRevision: 200, AuthorityEpoch: 2000}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestRecipientAuthorityResolverRetainsBatchCapabilityFallbacks(t *testing.T) {
	t.Run("full authority batch", func(t *testing.T) {
		node := fullAuthorityRecipientNode{
			authorities: []pkgcluster.RouteAuthority{
				{HashSlot: 1, SlotID: 11, LeaderNodeID: 10},
				{HashSlot: 2, SlotID: 22, LeaderNodeID: 20},
			},
		}
		resolved, err := NewRecipientAuthorityResolver(node, nil).ResolveRecipientAuthorities(context.Background(), []string{"u1", "u2"})
		if err != nil || len(resolved) != 2 || resolved[0].Target.LeaderNodeID != 10 || resolved[1].Target.LeaderNodeID != 20 {
			t.Fatalf("ResolveRecipientAuthorities() = %#v, %v", resolved, err)
		}
	})

	t.Run("partial route batch", func(t *testing.T) {
		node := partialRouteRecipientNode{results: []pkgcluster.RouteKeyResult{
			{Route: pkgcluster.Route{HashSlot: 1, SlotID: 11, Leader: 10}},
			{Err: pkgcluster.ErrNoSlotLeader},
		}}
		resolved, err := NewRecipientAuthorityResolver(node, nil).ResolveRecipientAuthorities(context.Background(), []string{"u1", "u2"})
		if err != nil || len(resolved) != 2 || resolved[0].Target.LeaderNodeID != 10 || !errors.Is(resolved[1].Err, channelappend.ErrRouteNotReady) {
			t.Fatalf("ResolveRecipientAuthorities() = %#v, %v", resolved, err)
		}
	})
}

func TestRecipientAuthorityResolverFallsBackToAlignedSingleRoutes(t *testing.T) {
	node := &singleRecipientRouteNode{
		routes: map[string]pkgcluster.Route{
			"u1": {HashSlot: 1, SlotID: 11, Leader: 10},
		},
		errs: map[string]error{"u2": pkgcluster.ErrNoSlotLeader},
	}
	observer := &recordingRecipientAuthorityResolveObserver{}
	resolved, err := NewRecipientAuthorityResolver(node, observer).ResolveRecipientAuthorities(context.Background(), []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("ResolveRecipientAuthorities() error = %v", err)
	}
	if !reflect.DeepEqual(node.keys, []string{"u1", "u2"}) {
		t.Fatalf("RouteKey order = %#v, want input order", node.keys)
	}
	if len(resolved) != 2 || resolved[0].Target.LeaderNodeID != 10 || !errors.Is(resolved[1].Err, channelappend.ErrRouteNotReady) {
		t.Fatalf("ResolveRecipientAuthorities() = %#v, want aligned success and route error", resolved)
	}
	if len(observer.events) != 1 || observer.events[0].Result != RecipientAuthorityResolveResultPartial || observer.events[0].Items != 2 || observer.events[0].Targets != 1 {
		t.Fatalf("single-route fallback observation = %#v, want partial items=2 targets=1", observer.events)
	}
}

func TestRecipientAuthorityResolverMapsRouteErrorAndObservesCanceledLookup(t *testing.T) {
	node := &batchRecipientRouteNodeForRecipientAuthorityTest{
		routes:     map[string]pkgcluster.Route{"u1": {}},
		singleErrs: map[string]error{"u1": pkgcluster.ErrNotLeader},
	}
	resolver := NewRecipientAuthorityResolver(node, nil)
	_, err := resolver.ResolveRecipientAuthority(context.Background(), "u1")
	if !errors.Is(err, channelappend.ErrNotLeader) || !errors.Is(err, pkgcluster.ErrNotLeader) {
		t.Fatalf("ResolveRecipientAuthority() error = %v, want mapped and source not-leader", err)
	}

	observer := &recordingRecipientAuthorityResolveObserver{}
	resolver = NewRecipientAuthorityResolver(node, observer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.ResolveRecipientAuthorities(ctx, []string{"u1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveRecipientAuthorities() error = %v, want canceled", err)
	}
	if len(observer.events) != 1 || observer.events[0].Result != RecipientAuthorityResolveResultCanceled || observer.events[0].Items != 1 || observer.events[0].Targets != 0 {
		t.Fatalf("canceled observation = %#v, want one canceled item", observer.events)
	}
}

func TestRecipientAuthorityResolverFailsClosedOnMisalignedBatch(t *testing.T) {
	resolver := NewRecipientAuthorityResolver(misalignedRecipientAuthorityNode{}, nil)
	_, err := resolver.ResolveRecipientAuthorities(context.Background(), []string{"u1", "u2"})
	if !errors.Is(err, channelappend.ErrRouteNotReady) {
		t.Fatalf("ResolveRecipientAuthorities() error = %v, want route not ready", err)
	}
}

type batchRecipientRouteNodeForRecipientAuthorityTest struct {
	routes      map[string]pkgcluster.Route
	singleErrs  map[string]error
	singleCalls int
	batchCalls  int
	batchKeys   []string
}

type authorityRecipientRouteNodeForRecipientAuthorityTest struct {
	*batchRecipientRouteNodeForRecipientAuthorityTest
	authorities    map[string]pkgcluster.RouteAuthority
	authorityErrs  map[string]error
	authorityCalls int
}

type recordingRecipientAuthorityResolveObserver struct {
	events []RecipientAuthorityResolveObservation
}

type misalignedRecipientAuthorityNode struct{}

type fullAuthorityRecipientNode struct {
	authorities []pkgcluster.RouteAuthority
}

type partialRouteRecipientNode struct {
	results []pkgcluster.RouteKeyResult
}

type singleRecipientRouteNode struct {
	routes map[string]pkgcluster.Route
	errs   map[string]error
	keys   []string
}

func (o *recordingRecipientAuthorityResolveObserver) ObserveRecipientAuthorityResolve(event RecipientAuthorityResolveObservation) {
	o.events = append(o.events, event)
}

func (misalignedRecipientAuthorityNode) RouteKey(string) (pkgcluster.Route, error) {
	return pkgcluster.Route{}, nil
}

func (misalignedRecipientAuthorityNode) RouteAuthoritiesPartial([]string) ([]pkgcluster.RouteAuthorityResult, error) {
	return []pkgcluster.RouteAuthorityResult{{}}, nil
}

func (fullAuthorityRecipientNode) RouteKey(string) (pkgcluster.Route, error) {
	return pkgcluster.Route{}, errors.New("unexpected single route lookup")
}

func (n fullAuthorityRecipientNode) RouteAuthorities([]string) ([]pkgcluster.RouteAuthority, error) {
	return n.authorities, nil
}

func (partialRouteRecipientNode) RouteKey(string) (pkgcluster.Route, error) {
	return pkgcluster.Route{}, errors.New("unexpected single route lookup")
}

func (n partialRouteRecipientNode) RouteKeysPartial([]string) ([]pkgcluster.RouteKeyResult, error) {
	return n.results, nil
}

func (n *singleRecipientRouteNode) RouteKey(key string) (pkgcluster.Route, error) {
	n.keys = append(n.keys, key)
	return n.routes[key], n.errs[key]
}

func (n *batchRecipientRouteNodeForRecipientAuthorityTest) RouteKey(key string) (pkgcluster.Route, error) {
	n.singleCalls++
	return n.routes[key], n.singleErrs[key]
}

func (n *batchRecipientRouteNodeForRecipientAuthorityTest) RouteKeys(keys []string) ([]pkgcluster.Route, error) {
	n.batchCalls++
	n.batchKeys = append([]string(nil), keys...)
	routes := make([]pkgcluster.Route, len(keys))
	for i, key := range keys {
		routes[i] = n.routes[key]
	}
	return routes, nil
}

func (n *authorityRecipientRouteNodeForRecipientAuthorityTest) RouteAuthorities(keys []string) ([]pkgcluster.RouteAuthority, error) {
	n.authorityCalls++
	authorities := make([]pkgcluster.RouteAuthority, len(keys))
	for i, key := range keys {
		authorities[i] = n.authorities[key]
	}
	return authorities, nil
}

func (n *authorityRecipientRouteNodeForRecipientAuthorityTest) RouteAuthoritiesPartial(keys []string) ([]pkgcluster.RouteAuthorityResult, error) {
	n.authorityCalls++
	results := make([]pkgcluster.RouteAuthorityResult, len(keys))
	for i, key := range keys {
		results[i] = pkgcluster.RouteAuthorityResult{Authority: n.authorities[key], Err: n.authorityErrs[key]}
	}
	return results, nil
}
