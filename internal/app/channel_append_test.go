package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestChannelAppendRecipientResolverPrefersAlignedLightweightAuthorityBatch(t *testing.T) {
	secondErr := errors.New("second route unavailable")
	legacy := &batchRecipientRouteNodeForChannelAppendTest{
		routes: map[string]cluster.Route{
			"u1": {HashSlot: 99, SlotID: 99, Leader: 99},
			"u2": {HashSlot: 99, SlotID: 99, Leader: 99},
		},
	}
	node := &authorityRecipientRouteNodeForChannelAppendTest{
		batchRecipientRouteNodeForChannelAppendTest: legacy,
		authorities: map[string]cluster.RouteAuthority{
			"u1": {HashSlot: 1, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000},
			"u2": {HashSlot: 2, SlotID: 22, LeaderNodeID: 20, LeaderTerm: 202, ConfigEpoch: 2002, RouteRevision: 200, AuthorityEpoch: 2000},
		},
		authorityErrs: map[string]error{"u2": secondErr},
	}
	observer := &recordingRecipientAuthorityResolveObserver{}
	resolver := channelAppendRecipientResolver{node: node, observer: observer}

	got, err := resolver.ResolveRecipientAuthorities(context.Background(), []string{"u1", "u2"})
	if err != nil {
		t.Fatalf("ResolveRecipientAuthorities() error = %v", err)
	}

	if node.authorityCalls != 1 {
		t.Fatalf("RouteAuthorities calls = %d, want 1", node.authorityCalls)
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
	if event.Result != recipientAuthorityResolveResultPartial || event.Items != 2 || event.Targets != 1 || event.Duration < 0 {
		t.Fatalf("recipient authority observation = %#v, want partial items=2 targets=1", event)
	}
}

func TestChannelAppendRecipientResolverUsesBatchRouteNode(t *testing.T) {
	node := &batchRecipientRouteNodeForChannelAppendTest{
		routes: map[string]cluster.Route{
			"u1": {HashSlot: 1, SlotID: 11, Leader: 10, LeaderTerm: 101, ConfigEpoch: 1001, Revision: 100, AuthorityEpoch: 1000},
			"u2": {HashSlot: 2, SlotID: 22, Leader: 20, LeaderTerm: 202, ConfigEpoch: 2002, Revision: 200, AuthorityEpoch: 2000},
		},
	}
	resolver := channelAppendRecipientResolver{node: node}

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

func TestChannelAppendPresenceResolverPreservesExactTargetsAndPartialResults(t *testing.T) {
	first := channelappend.RecipientAuthorityTarget{HashSlot: 1, SlotID: 11, LeaderNodeID: 10, LeaderTerm: 101, ConfigEpoch: 1001, RouteRevision: 100, AuthorityEpoch: 1000}
	second := channelappend.RecipientAuthorityTarget{HashSlot: 2, SlotID: 22, LeaderNodeID: 20, LeaderTerm: 202, ConfigEpoch: 2002, RouteRevision: 200, AuthorityEpoch: 2000}
	firstErr := errors.New("first target unavailable")
	authority := &targetedPresenceAuthorityForChannelAppendTest{results: []presenceusecase.EndpointLookupResult{
		{Err: firstErr},
		{Routes: []presenceusecase.Route{{UID: "u2", OwnerNodeID: 3, OwnerBootID: 4, OwnerSeq: 5, SessionID: 6}}},
	}}
	resolver := channelAppendPresenceResolver{presence: presenceusecase.New(presenceusecase.Options{Authority: authority})}

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

func TestChannelAppendSubscriberMutationObserverRefreshesMetadataCache(t *testing.T) {
	cache := clusterinfra.NewChannelAppendMetadataCache()
	app := &App{channelAppendMetadata: cache}
	observer := channelAppendSubscriberMutationObserver{app: app}

	observer.ObserveSubscriberMutation(context.Background(), channelusecase.SubscriberMutationEvent{
		ChannelKey: channelusecase.ChannelKey{
			ChannelID:   "g1",
			ChannelType: 2,
		},
		Large:                     true,
		SubscriberMutationVersion: 7,
	})

	metadata, ok := cache.Lookup(channelappend.ChannelID{ID: "g1", Type: 2})
	if !ok || !metadata.Large || metadata.SubscriberMutationVersion != 7 {
		t.Fatalf("metadata cache = %#v ok=%v, want large version 7", metadata, ok)
	}
}

func TestChannelAppendOwnerPusherObservesActualPushAttempt(t *testing.T) {
	runtimeRoute := runtimedelivery.Route{UID: "u1", OwnerNodeID: 3, SessionID: 10}
	channelRoute := channelappend.Route{UID: "u1", OwnerNodeID: 3, SessionID: 10}
	next := &recordingRuntimeOwnerPusherForChannelAppendTest{result: runtimedelivery.PushResult{Accepted: []runtimedelivery.Route{runtimeRoute}}}
	observer := &recordingDeliveryObserverForChannelAppendTest{}
	pusher := channelAppendOwnerPusher{next: next, observer: observer}

	result, err := pusher.Push(context.Background(), channelappend.PushCommand{
		OwnerNodeID: 3,
		Envelope:    channelappend.CommittedEnvelope{MessageID: 10},
		Routes:      []channelappend.Route{channelRoute},
	})

	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(result.Accepted) != 1 || result.Accepted[0] != channelRoute {
		t.Fatalf("Push() result = %#v, want accepted route", result)
	}
	if len(observer.pushes) != 1 {
		t.Fatalf("push observations = %d, want 1", len(observer.pushes))
	}
	got := observer.pushes[0]
	if got.OwnerNodeID != 3 || got.Result != runtimedelivery.DeliveryResultOK ||
		got.ErrorClass != runtimedelivery.DeliveryErrorClassNone || got.Routes != 1 || got.Accepted != 1 || got.Duration < 0 {
		t.Fatalf("push observation = %+v, want successful owner 3 route", got)
	}
}

func TestChannelAppendOwnerPusherClassifiesPushOutcomes(t *testing.T) {
	firstRuntimeRoute := runtimedelivery.Route{UID: "u1", OwnerNodeID: 3, SessionID: 10}
	secondRuntimeRoute := runtimedelivery.Route{UID: "u2", OwnerNodeID: 3, SessionID: 20}
	command := channelappend.PushCommand{
		OwnerNodeID: 3,
		Envelope:    channelappend.CommittedEnvelope{MessageID: 10},
		Routes: []channelappend.Route{
			{UID: "u1", OwnerNodeID: 3, SessionID: 10},
			{UID: "u2", OwnerNodeID: 3, SessionID: 20},
		},
	}
	hardErr := errors.New("push failed")
	tests := []struct {
		name          string
		next          *recordingRuntimeOwnerPusherForChannelAppendTest
		wantResult    string
		wantClass     string
		wantAccepted  int
		wantRetryable int
		wantDropped   int
	}{
		{
			name: "mixed accepted and dropped",
			next: &recordingRuntimeOwnerPusherForChannelAppendTest{result: runtimedelivery.PushResult{
				Accepted: []runtimedelivery.Route{firstRuntimeRoute},
				Dropped:  []runtimedelivery.Route{secondRuntimeRoute},
			}},
			wantResult: runtimedelivery.DeliveryResultDropped, wantClass: runtimedelivery.DeliveryErrorClassNone,
			wantAccepted: 1, wantDropped: 1,
		},
		{
			name: "retryable routes",
			next: &recordingRuntimeOwnerPusherForChannelAppendTest{result: runtimedelivery.PushResult{
				Retryable: []runtimedelivery.Route{secondRuntimeRoute},
			}},
			wantResult: runtimedelivery.DeliveryResultRetryable, wantClass: runtimedelivery.DeliveryErrorClassRetryable,
			wantRetryable: 1,
		},
		{
			name:       "retryable error",
			next:       &recordingRuntimeOwnerPusherForChannelAppendTest{err: runtimedelivery.ErrRouteNotReady},
			wantResult: runtimedelivery.DeliveryResultRetryable,
			wantClass:  runtimedelivery.DeliveryErrorClassRouteNotReady,
		},
		{
			name:       "hard error",
			next:       &recordingRuntimeOwnerPusherForChannelAppendTest{err: hardErr},
			wantResult: runtimedelivery.DeliveryResultError,
			wantClass:  runtimedelivery.DeliveryErrorClassError,
		},
		{
			name:        "missing pusher drops routes",
			wantResult:  runtimedelivery.DeliveryResultDropped,
			wantClass:   runtimedelivery.DeliveryErrorClassNone,
			wantDropped: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer := &recordingDeliveryObserverForChannelAppendTest{}
			var next runtimedelivery.Pusher
			if tt.next != nil {
				next = tt.next
			}
			pusher := channelAppendOwnerPusher{next: next, observer: observer}
			_, _ = pusher.Push(context.Background(), command)
			if len(observer.pushes) != 1 {
				t.Fatalf("push observations = %d, want 1", len(observer.pushes))
			}
			got := observer.pushes[0]
			if got.Result != tt.wantResult || got.ErrorClass != tt.wantClass || got.Routes != 2 ||
				got.Accepted != tt.wantAccepted || got.Retryable != tt.wantRetryable || got.Dropped != tt.wantDropped {
				t.Fatalf("push observation = %+v, want result=%s class=%s accepted=%d retryable=%d dropped=%d",
					got, tt.wantResult, tt.wantClass, tt.wantAccepted, tt.wantRetryable, tt.wantDropped)
			}
		})
	}
}

func TestChannelAppendOwnerPusherObservesPanicAndRepanics(t *testing.T) {
	panicValue := &struct{}{}
	observer := &recordingDeliveryObserverForChannelAppendTest{}
	pusher := channelAppendOwnerPusher{
		next:     &recordingRuntimeOwnerPusherForChannelAppendTest{panicValue: panicValue},
		observer: observer,
	}
	command := channelappend.PushCommand{
		OwnerNodeID: 3,
		Routes:      []channelappend.Route{{UID: "u1", OwnerNodeID: 3, SessionID: 10}},
	}

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = pusher.Push(context.Background(), command)
	}()

	if recovered != panicValue {
		t.Fatalf("recovered panic = %#v, want original panic value", recovered)
	}
	if len(observer.pushes) != 1 {
		t.Fatalf("push observations = %d, want panic attempt", len(observer.pushes))
	}
	got := observer.pushes[0]
	if got.Result != runtimedelivery.DeliveryResultError || got.ErrorClass != runtimedelivery.DeliveryErrorClassError || got.Routes != 1 {
		t.Fatalf("panic observation = %+v, want error result for one route", got)
	}
}

type batchRecipientRouteNodeForChannelAppendTest struct {
	routes      map[string]cluster.Route
	singleCalls int
	batchCalls  int
	batchKeys   []string
}

type authorityRecipientRouteNodeForChannelAppendTest struct {
	*batchRecipientRouteNodeForChannelAppendTest
	authorities    map[string]cluster.RouteAuthority
	authorityErrs  map[string]error
	authorityCalls int
}

type targetedPresenceAuthorityForChannelAppendTest struct {
	groups      []presenceusecase.EndpointLookupGroup
	results     []presenceusecase.EndpointLookupResult
	legacyCalls int
}

type recordingRuntimeOwnerPusherForChannelAppendTest struct {
	result     runtimedelivery.PushResult
	err        error
	panicValue any
}

func (p *recordingRuntimeOwnerPusherForChannelAppendTest) Push(context.Context, runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	if p.panicValue != nil {
		panic(p.panicValue)
	}
	return runtimedelivery.PushResult{
		Accepted:  append([]runtimedelivery.Route(nil), p.result.Accepted...),
		Retryable: append([]runtimedelivery.Route(nil), p.result.Retryable...),
		Dropped:   append([]runtimedelivery.Route(nil), p.result.Dropped...),
	}, p.err
}

type recordingDeliveryObserverForChannelAppendTest struct {
	pushes []runtimedelivery.FanoutPushEvent
}

type recordingRecipientAuthorityResolveObserver struct {
	events []recipientAuthorityResolveObservation
}

func (o *recordingRecipientAuthorityResolveObserver) ObserveRecipientAuthorityResolve(event recipientAuthorityResolveObservation) {
	o.events = append(o.events, event)
}

func (o *recordingDeliveryObserverForChannelAppendTest) ObserveFanoutTask(runtimedelivery.FanoutTaskEvent) {
}

func (o *recordingDeliveryObserverForChannelAppendTest) ObserveFanoutResolve(runtimedelivery.FanoutResolveEvent) {
}

func (o *recordingDeliveryObserverForChannelAppendTest) ObserveFanoutPush(event runtimedelivery.FanoutPushEvent) {
	o.pushes = append(o.pushes, event)
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

func (n *batchRecipientRouteNodeForChannelAppendTest) RouteKey(key string) (cluster.Route, error) {
	n.singleCalls++
	return n.routes[key], nil
}

func (n *batchRecipientRouteNodeForChannelAppendTest) RouteKeys(keys []string) ([]cluster.Route, error) {
	n.batchCalls++
	n.batchKeys = append([]string(nil), keys...)
	routes := make([]cluster.Route, len(keys))
	for i, key := range keys {
		routes[i] = n.routes[key]
	}
	return routes, nil
}

func (n *authorityRecipientRouteNodeForChannelAppendTest) RouteAuthorities(keys []string) ([]cluster.RouteAuthority, error) {
	n.authorityCalls++
	authorities := make([]cluster.RouteAuthority, len(keys))
	for i, key := range keys {
		authorities[i] = n.authorities[key]
	}
	return authorities, nil
}

func (n *authorityRecipientRouteNodeForChannelAppendTest) RouteAuthoritiesPartial(keys []string) ([]cluster.RouteAuthorityResult, error) {
	n.authorityCalls++
	results := make([]cluster.RouteAuthorityResult, len(keys))
	for i, key := range keys {
		results[i] = cluster.RouteAuthorityResult{Authority: n.authorities[key], Err: n.authorityErrs[key]}
	}
	return results, nil
}
