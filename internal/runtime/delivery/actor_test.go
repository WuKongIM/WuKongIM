package delivery

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestActorDeliversHigherObservedSequenceImmediatelyAndLaterLowerSequenceAsLate(t *testing.T) {
	runtime, _, pusher := newTestManager()

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(1, 1)))
	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(3, 3)))
	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(2, 2)))

	require.Equal(t, []uint64{1, 3, 2}, pusher.pushedSeqs())
}

func TestActorDispatchesRealtimeScopedZeroSeqMessagesByMessageID(t *testing.T) {
	runtime, _, pusher := newTestManager()

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 0)))
	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(102, 0)))

	require.Len(t, pusher.calls, 2)
	require.Equal(t, uint64(101), pusher.calls[0].envelope.MessageID)
	require.Equal(t, uint64(102), pusher.calls[1].envelope.MessageID)
	require.Equal(t, []uint64{0, 0}, pusher.pushedSeqs())
}

func TestCloneEnvelopeDeepCopiesMessageScopedUIDs(t *testing.T) {
	env := testEnvelope(101, 1)
	env.MessageScopedUIDs = []string{"u1", "u2"}

	copied := cloneEnvelope(env)
	copied.MessageScopedUIDs[0] = "changed"

	require.Equal(t, []string{"u1", "u2"}, env.MessageScopedUIDs)
}

func TestActorEnsureRouteStateAvoidsPerRouteStateAllocation(t *testing.T) {
	act := &actor{}
	routes := make([]RouteKey, 64)
	for i := range routes {
		routes[i] = testRoute("u2", 1, 11, uint64(i+1))
	}

	allocs := testing.AllocsPerRun(1000, func() {
		msg := &InflightMessage{
			Routes: make(map[RouteKey]RouteDeliveryState, len(routes)),
		}
		for _, route := range routes {
			act.ensureRouteState(msg, route)
		}
	})

	require.LessOrEqual(t, allocs, float64(10))
}

func TestRouteStateMapPoolReusesClearedMaps(t *testing.T) {
	if deliveryRaceEnabled {
		t.Skip("race instrumentation adds allocations")
	}
	routes := make([]RouteKey, 64)
	for i := range routes {
		routes[i] = testRoute("u2", 1, 11, uint64(i+1))
	}
	warmed := acquireRouteStateMap()
	for _, route := range routes {
		warmed[route] = RouteDeliveryState{Attempt: 1, Accepted: true}
	}
	releaseRouteStateMap(warmed)

	reused := acquireRouteStateMap()
	require.Empty(t, reused)
	releaseRouteStateMap(reused)

	allocs := testing.AllocsPerRun(1000, func() {
		routesByKey := acquireRouteStateMap()
		for _, route := range routes {
			routesByKey[route] = RouteDeliveryState{Attempt: 1}
		}
		releaseRouteStateMap(routesByKey)
	})
	require.LessOrEqual(t, allocs, float64(1))
}

func TestActorBindsAckIndexOnlyForAcceptedRoutes(t *testing.T) {
	runtime, _, pusher := newTestManager()
	accepted := testRoute("u2", 1, 11, 2)
	retryable := testRoute("u2", 1, 11, 3)
	dropped := testRoute("u2", 1, 11, 4)
	runtime.resolver.(*stubResolver).routesByChannel[testChannelID] = []RouteKey{
		accepted,
		retryable,
		dropped,
	}
	pusher.responses = []pushResponse{{
		result: PushResult{
			Accepted:  []RouteKey{accepted},
			Retryable: []RouteKey{retryable},
			Dropped:   []RouteKey{dropped},
		},
	}}

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))

	binding, ok := runtime.ackIdx.Lookup(accepted.SessionID, 101)
	require.True(t, ok)
	require.Equal(t, accepted, binding.Route)
	_, ok = runtime.ackIdx.Lookup(retryable.SessionID, 101)
	require.False(t, ok)
	_, ok = runtime.ackIdx.Lookup(dropped.SessionID, 101)
	require.False(t, ok)
}

func TestActorRetryTickRetriesPendingRoutesUntilAcked(t *testing.T) {
	runtime, clock, pusher := newTestManager()
	pusher.responses = []pushResponse{
		{result: PushResult{Accepted: []RouteKey{testRoute("u2", 1, 11, 2)}}},
		{result: PushResult{Accepted: []RouteKey{testRoute("u2", 1, 11, 2)}}},
	}

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))
	require.Equal(t, 1, pusher.attemptsFor(101))

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	require.NoError(t, runtime.ProcessRetryTicks(context.Background()))
	require.Equal(t, 2, pusher.attemptsFor(101))

	require.NoError(t, runtime.AckRoute(context.Background(), RouteAck{
		UID:        "u2",
		SessionID:  2,
		MessageID:  101,
		MessageSeq: 1,
	}))

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	require.NoError(t, runtime.ProcessRetryTicks(context.Background()))
	require.Equal(t, 2, pusher.attemptsFor(101))
}

func TestActorHonorsAckArrivingBeforePushReturns(t *testing.T) {
	route := testRoute("u2", 1, 11, 2)
	resolver := &stubResolver{routesByChannel: map[string][]RouteKey{testChannelID: {route}}}
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	pusher := &ackDuringPushPusher{}
	runtime := NewManager(Config{
		Resolver:         resolver,
		Push:             pusher,
		Clock:            clock,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 3,
	})
	pusher.runtime = runtime

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))
	require.False(t, runtime.HasAckBinding(route.SessionID, 101))

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	require.NoError(t, runtime.ProcessRetryTicks(context.Background()))
	require.Equal(t, 1, pusher.attemptsFor(101))
}

func TestActorAcksSameSessionMessageOnDifferentNodesIndependently(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	routeA := testRoute("u2", 2, 21, 2)
	routeB := testRoute("u3", 3, 31, 2)
	pusher := &recordingPusher{}
	runtime := NewManager(Config{
		Resolver: &stubResolver{routesByChannel: map[string][]RouteKey{
			testChannelID: {routeA, routeB},
		}},
		Push:             pusher,
		Clock:            clock,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 3,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))
	require.NoError(t, runtime.AckRoute(context.Background(), RouteAck{
		UID:        routeA.UID,
		SessionID:  routeA.SessionID,
		MessageID:  101,
		MessageSeq: 1,
	}))
	require.NoError(t, runtime.AckRoute(context.Background(), RouteAck{
		UID:        routeB.UID,
		SessionID:  routeB.SessionID,
		MessageID:  101,
		MessageSeq: 1,
	}))

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	require.NoError(t, runtime.ProcessRetryTicks(context.Background()))
	require.Equal(t, 1, pusher.attemptsFor(101))
}

func TestActorExpiresAcceptedRouteAfterFinalRetryAttempt(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	route := testRoute("u2", 1, 11, 2)
	pusher := &recordingPusher{responses: []pushResponse{
		{result: PushResult{Accepted: []RouteKey{route}}},
		{result: PushResult{Accepted: []RouteKey{route}}},
	}}
	runtime := NewManager(Config{
		Resolver:         &stubResolver{routesByChannel: map[string][]RouteKey{testChannelID: {route}}},
		Push:             pusher,
		Clock:            clock,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 2,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))
	require.True(t, runtime.HasAckBinding(route.SessionID, 101))

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	require.NoError(t, runtime.ProcessRetryTicks(context.Background()))

	require.False(t, runtime.HasAckBinding(route.SessionID, 101))
	require.Equal(t, 2, pusher.attemptsFor(101))
}

func TestActorReportsRouteExpiryObserver(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	observer := &recordingDeliveryObserver{}
	route := testRoute("u2", 1, 11, 2)
	runtime := NewManager(Config{
		Resolver: &stubResolver{routesByChannel: map[string][]RouteKey{testChannelID: {route}}},
		Push: &recordingPusher{responses: []pushResponse{
			{result: PushResult{Accepted: []RouteKey{route}}},
		}},
		Clock:            clock,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 1,
		Observer:         observer,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))
	require.Len(t, observer.expired, 1)
	require.Equal(t, uint64(101), observer.expired[0].MessageID)
	require.Equal(t, route.SessionID, observer.expired[0].Route.SessionID)
}

func TestActorReportsRouteExpiryObserverOutsideActorLock(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	route := testRoute("u2", 1, 11, 2)
	var runtime *Manager
	observer := &lockingDeliveryObserver{
		onExpired: func(RouteExpiredEvent) {
			_ = runtime.ActorLane(testChannelID, frame.ChannelTypePerson)
		},
	}
	runtime = NewManager(Config{
		Resolver: &stubResolver{routesByChannel: map[string][]RouteKey{testChannelID: {route}}},
		Push: &recordingPusher{responses: []pushResponse{
			{result: PushResult{Accepted: []RouteKey{route}}},
		}},
		Clock:            clock,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 1,
		Observer:         observer,
	})

	done := make(chan error, 1)
	go func() {
		done <- runtime.Submit(context.Background(), testEnvelope(101, 1))
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("route expiry observer was called while actor lock was held")
	}
	require.Equal(t, 1, observer.expiredCount())
}

func TestActorDoesNotExpireRouteAckedDuringFinalRetryPush(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	observer := &recordingDeliveryObserver{}
	route := testRoute("u2", 1, 11, 2)
	pusher := newBlockingFinalRetryPusher(route)
	t.Cleanup(pusher.Unblock)
	runtime := NewManager(Config{
		Resolver:         &stubResolver{routesByChannel: map[string][]RouteKey{testChannelID: {route}}},
		Push:             pusher,
		Clock:            clock,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 2,
		Observer:         observer,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))
	require.True(t, runtime.HasAckBinding(route.SessionID, 101))

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- runtime.ProcessRetryTicks(context.Background())
	}()
	pusher.WaitForFinalRetry(t)

	ackDone := make(chan error, 1)
	go func() {
		ackDone <- runtime.AckRoute(context.Background(), RouteAck{
			UID:        route.UID,
			SessionID:  route.SessionID,
			MessageID:  101,
			MessageSeq: 1,
		})
	}()
	require.Eventually(t, func() bool {
		return !runtime.HasAckBinding(route.SessionID, 101)
	}, time.Second, time.Millisecond)

	pusher.Unblock()
	require.NoError(t, <-retryDone)
	require.NoError(t, <-ackDone)
	require.Empty(t, observer.expired)
	require.Equal(t, 2, pusher.attemptsFor(101))
}

func TestActorDoesNotExpireRouteClosedDuringFinalRetryPush(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	observer := &recordingDeliveryObserver{}
	route := testRoute("u2", 1, 11, 2)
	pusher := newBlockingFinalRetryPusher(route)
	t.Cleanup(pusher.Unblock)
	runtime := NewManager(Config{
		Resolver:         &stubResolver{routesByChannel: map[string][]RouteKey{testChannelID: {route}}},
		Push:             pusher,
		Clock:            clock,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 2,
		Observer:         observer,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(101, 1)))
	require.True(t, runtime.HasAckBinding(route.SessionID, 101))

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- runtime.ProcessRetryTicks(context.Background())
	}()
	pusher.WaitForFinalRetry(t)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- runtime.SessionClosed(context.Background(), SessionClosed{
			UID:       route.UID,
			SessionID: route.SessionID,
		})
	}()
	require.Eventually(t, func() bool {
		return !runtime.HasAckBinding(route.SessionID, 101)
	}, time.Second, time.Millisecond)

	pusher.Unblock()
	require.NoError(t, <-retryDone)
	require.NoError(t, <-closeDone)
	require.Empty(t, observer.expired)
	require.Equal(t, 2, pusher.attemptsFor(101))
}

func TestActorResolvesSubscribersPageByPageAndOnlyTracksOnlineRoutes(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)}
	resolver := &pagedStubResolver{
		pagesByChannel: map[string][]pagedResolveResult{
			"g1": {
				{
					routes: []RouteKey{
						testRoute("u2", 1, 11, 2),
					},
					nextCursor: "u3",
					done:       false,
				},
				{
					routes: []RouteKey{
						testRoute("u4", 2, 22, 4),
					},
					nextCursor: "u4",
					done:       true,
				},
			},
		},
	}
	pusher := &recordingPusher{}
	runtime := NewManager(Config{
		Resolver:         resolver,
		Push:             pusher,
		Clock:            clock,
		ShardCount:       1,
		IdleTimeout:      time.Minute,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 4,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g1", frame.ChannelTypeGroup, 101, 1, "hello group")))

	require.Equal(t, 2, resolver.pageCalls["g1"])
	require.Equal(t, []uint64{2, 4}, pusher.acceptedSessionIDs(101))
}

func TestActorDropsRealtimeRoutesWhenInflightBudgetExceeded(t *testing.T) {
	pusher := &recordingPusher{}
	runtime := NewManager(Config{
		Resolver: &stubResolver{
			routesByChannel: map[string][]RouteKey{
				"g-budget": {
					testRoute("u2", 1, 11, 2),
					testRoute("u3", 1, 11, 3),
				},
			},
		},
		Push:   pusher,
		Clock:  &testClock{now: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)},
		Limits: Limits{MaxInflightRoutesPerActor: 1},
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-budget", frame.ChannelTypeGroup, 201, 1, "budget")))

	require.Equal(t, []uint64{2}, pusher.acceptedSessionIDs(201))
}

func TestActorDispatchesFirstObservedSequenceWithoutWaitingForSequenceOne(t *testing.T) {
	runtime, _, pusher := newTestManager()
	runtime.resolver.(*stubResolver).routesByChannel["g-first-gap"] = []RouteKey{
		testRoute("u2", 1, 11, 2),
	}

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-first-gap", frame.ChannelTypeGroup, 209, 9, "gap")))

	require.Equal(t, []uint64{9}, pusher.pushedSeqs())
}

func TestActorDispatchesLateLowerSequenceInsteadOfDroppingIt(t *testing.T) {
	runtime, _, pusher := newTestManager()
	runtime.resolver.(*stubResolver).routesByChannel["g-first-gap"] = []RouteKey{
		testRoute("u2", 1, 11, 2),
	}

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-first-gap", frame.ChannelTypeGroup, 209, 9, "gap")))
	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-first-gap", frame.ChannelTypeGroup, 208, 8, "late")))

	require.Equal(t, []uint64{9, 8}, pusher.pushedSeqs())
}

func TestActorDispatchesSparseObservedSequenceWithoutWaitingForMissingGap(t *testing.T) {
	runtime, _, pusher := newTestManager()
	runtime.resolver.(*stubResolver).routesByChannel["g-sparse"] = []RouteKey{
		testRoute("u2", 1, 11, 2),
	}

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-sparse", frame.ChannelTypeGroup, 301, 1, "one")))
	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-sparse", frame.ChannelTypeGroup, 302, 2, "two")))
	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-sparse", frame.ChannelTypeGroup, 304, 4, "four")))

	require.Equal(t, []uint64{1, 2, 4}, pusher.pushedSeqs())
}

func TestActorDispatchesSparseObservedSequenceWhileEarlierMessageIsStillInflight(t *testing.T) {
	runtime, _, pusher := newTestManager()
	runtime.resolver.(*stubResolver).routesByChannel["g-sparse-buffered"] = []RouteKey{
		testRoute("u2", 1, 11, 2),
	}

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-sparse-buffered", frame.ChannelTypeGroup, 401, 1, "one")))
	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-sparse-buffered", frame.ChannelTypeGroup, 404, 4, "four")))
	require.Equal(t, []uint64{1, 4}, pusher.pushedSeqs())
}

func TestActorSuppressesDuplicateLateLowerSequenceAfterCompletion(t *testing.T) {
	runtime, _, pusher := newTestManager()
	runtime.resolver.(*stubResolver).routesByChannel["g-first-gap"] = []RouteKey{
		testRoute("u2", 1, 11, 2),
	}

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-first-gap", frame.ChannelTypeGroup, 209, 9, "gap")))
	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-first-gap", frame.ChannelTypeGroup, 208, 8, "late")))

	require.NoError(t, runtime.AckRoute(context.Background(), RouteAck{
		UID:        "u2",
		SessionID:  2,
		MessageID:  208,
		MessageSeq: 8,
	}))

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-first-gap", frame.ChannelTypeGroup, 208, 8, "late")))

	require.Equal(t, []uint64{9, 8}, pusher.pushedSeqs())
}

func TestActorResolveFailureRetriesBeforeLaterMessages(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)}
	pusher := &recordingPusher{}
	runtime := NewManager(Config{
		Resolver: &flakyResolver{
			failMessageIDs: map[uint64]error{
				101: context.DeadlineExceeded,
			},
			routesByChannel: map[string][]RouteKey{
				"g-flaky": {testRoute("u2", 1, 11, 2)},
			},
		},
		Push:   pusher,
		Clock:  clock,
		Limits: Limits{MaxInflightRoutesPerActor: 8},
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-flaky", frame.ChannelTypeGroup, 101, 1, "first")))

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-flaky", frame.ChannelTypeGroup, 102, 2, "second")))

	require.Empty(t, pusher.pushedSeqs())

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	require.NoError(t, runtime.ProcessRetryTicks(context.Background()))

	require.Equal(t, []uint64{1, 2}, pusher.pushedSeqs())
}

func TestActorContinuesResolvingPagesAfterAckReleasesBudget(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)}
	resolver := &pagedStubResolver{
		pagesByChannel: map[string][]pagedResolveResult{
			"g-budget": {
				{
					routes:     []RouteKey{testRoute("u2", 1, 11, 2)},
					nextCursor: "u2",
					done:       false,
				},
				{
					routes:     []RouteKey{testRoute("u3", 1, 11, 3)},
					nextCursor: "u3",
					done:       true,
				},
			},
		},
	}
	pusher := &recordingPusher{}
	runtime := NewManager(Config{
		Resolver: resolver,
		Push:     pusher,
		Clock:    clock,
		Limits:   Limits{MaxInflightRoutesPerActor: 1},
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-budget", frame.ChannelTypeGroup, 301, 1, "budget")))
	require.Equal(t, []uint64{2}, pusher.acceptedSessionIDs(301))

	require.NoError(t, runtime.AckRoute(context.Background(), RouteAck{
		UID:        "u2",
		SessionID:  2,
		MessageID:  301,
		MessageSeq: 1,
	}))

	require.Equal(t, []uint64{2, 3}, pusher.acceptedSessionIDs(301))
}

func TestActorResumesLaterMessageWhenAckReleasesBudget(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)}
	resolver := &messagePagedResolver{
		pagesByMessageID: map[uint64][]pagedResolveResult{
			401: {{
				routes:     []RouteKey{testRoute("u2", 1, 11, 2)},
				nextCursor: "u2",
				done:       true,
			}},
			402: {{
				routes:     []RouteKey{testRoute("u3", 1, 11, 3)},
				nextCursor: "u3",
				done:       true,
			}},
		},
	}
	pusher := &recordingPusher{}
	runtime := NewManager(Config{
		Resolver: resolver,
		Push:     pusher,
		Clock:    clock,
		Limits:   Limits{MaxInflightRoutesPerActor: 1},
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-budget-later", frame.ChannelTypeGroup, 401, 1, "first")))
	require.Len(t, pusher.calls, 1)
	require.Equal(t, uint64(401), pusher.calls[0].envelope.MessageID)
	require.Equal(t, uint64(2), pusher.calls[0].routes[0].SessionID)

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-budget-later", frame.ChannelTypeGroup, 402, 2, "second")))
	require.Len(t, pusher.calls, 1)

	require.NoError(t, runtime.AckRoute(context.Background(), RouteAck{
		UID:        "u2",
		SessionID:  2,
		MessageID:  401,
		MessageSeq: 1,
	}))

	require.Len(t, pusher.calls, 2)
	require.Equal(t, uint64(402), pusher.calls[1].envelope.MessageID)
	require.Equal(t, uint64(3), pusher.calls[1].routes[0].SessionID)
}

func TestActorContinuesResolvingPagesAfterRetryDropReleasesBudget(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)}
	resolver := &messagePagedResolver{
		pagesByMessageID: map[uint64][]pagedResolveResult{
			501: {
				{
					routes:     []RouteKey{testRoute("u2", 1, 11, 2)},
					nextCursor: "u2",
					done:       false,
				},
				{
					routes:     []RouteKey{testRoute("u3", 1, 11, 3)},
					nextCursor: "u3",
					done:       true,
				},
			},
		},
	}
	pusher := &recordingPusher{
		responses: []pushResponse{
			{result: PushResult{Accepted: []RouteKey{testRoute("u2", 1, 11, 2)}}},
			{result: PushResult{Dropped: []RouteKey{testRoute("u2", 1, 11, 2)}}},
			{result: PushResult{Accepted: []RouteKey{testRoute("u3", 1, 11, 3)}}},
		},
	}
	runtime := NewManager(Config{
		Resolver:         resolver,
		Push:             pusher,
		Clock:            clock,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 4,
		Limits:           Limits{MaxInflightRoutesPerActor: 1},
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-budget-retry", frame.ChannelTypeGroup, 501, 1, "first")))
	require.Len(t, pusher.calls, 1)
	require.Equal(t, uint64(2), pusher.calls[0].routes[0].SessionID)

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	require.NoError(t, runtime.ProcessRetryTicks(context.Background()))

	require.Len(t, pusher.calls, 3)
	require.Equal(t, uint64(2), pusher.calls[1].routes[0].SessionID)
	require.Equal(t, uint64(3), pusher.calls[2].routes[0].SessionID)
}

type pagedStubResolver struct {
	pagesByChannel map[string][]pagedResolveResult
	pageCalls      map[string]int
}

type pagedResolveResult struct {
	routes     []RouteKey
	nextCursor string
	done       bool
}

type messagePagedResolver struct {
	pagesByMessageID map[uint64][]pagedResolveResult
	pageCalls        map[uint64]int
}

type flakyResolver struct {
	failMessageIDs  map[uint64]error
	routesByChannel map[string][]RouteKey
}

type flakyResolveToken struct {
	channelID string
	messageID uint64
}

type recordingDeliveryObserver struct {
	expired []RouteExpiredEvent
}

func (r *recordingDeliveryObserver) OnRouteExpired(event RouteExpiredEvent) {
	r.expired = append(r.expired, event)
}

func (r *recordingDeliveryObserver) OnMaintenanceSnapshot(MaintenanceSnapshot) {}

type lockingDeliveryObserver struct {
	mu        sync.Mutex
	expired   int
	onExpired func(RouteExpiredEvent)
}

func (o *lockingDeliveryObserver) OnRouteExpired(event RouteExpiredEvent) {
	if o.onExpired != nil {
		o.onExpired(event)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.expired++
}

func (o *lockingDeliveryObserver) OnMaintenanceSnapshot(MaintenanceSnapshot) {}

func (o *lockingDeliveryObserver) expiredCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.expired
}

type blockingFinalRetryPusher struct {
	route   RouteKey
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   []PushCommand
}

func newBlockingFinalRetryPusher(route RouteKey) *blockingFinalRetryPusher {
	return &blockingFinalRetryPusher{
		route:   route,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (p *blockingFinalRetryPusher) Push(_ context.Context, cmd PushCommand) (PushResult, error) {
	p.mu.Lock()
	p.calls = append(p.calls, PushCommand{
		Envelope: cmd.Envelope,
		Routes:   append([]RouteKey(nil), cmd.Routes...),
		Attempt:  cmd.Attempt,
	})
	p.mu.Unlock()

	if cmd.Attempt == 2 {
		p.once.Do(func() { close(p.started) })
		<-p.release
	}
	return PushResult{Accepted: []RouteKey{p.route}}, nil
}

func (p *blockingFinalRetryPusher) WaitForFinalRetry(t *testing.T) {
	t.Helper()
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for final retry push")
	}
}

func (p *blockingFinalRetryPusher) Unblock() {
	select {
	case <-p.release:
	default:
		close(p.release)
	}
}

func (p *blockingFinalRetryPusher) attemptsFor(messageID uint64) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, call := range p.calls {
		if call.Envelope.MessageID == messageID {
			count++
		}
	}
	return count
}

func (r *pagedStubResolver) BeginResolve(_ context.Context, key ChannelKey, _ CommittedEnvelope) (any, error) {
	return key.ChannelID, nil
}

func (r *pagedStubResolver) ResolvePage(_ context.Context, token any, _ string, _ int) (ResolvePageResult, error) {
	if r.pageCalls == nil {
		r.pageCalls = make(map[string]int)
	}
	channelID := token.(string)
	call := r.pageCalls[channelID]
	r.pageCalls[channelID] = call + 1
	page := r.pagesByChannel[channelID][call]
	return ResolvePageResult{Routes: append([]RouteKey(nil), page.routes...), NextCursor: page.nextCursor, Done: page.done}, nil
}

func (r *messagePagedResolver) BeginResolve(_ context.Context, _ ChannelKey, env CommittedEnvelope) (any, error) {
	return env.MessageID, nil
}

func (r *messagePagedResolver) ResolvePage(_ context.Context, token any, _ string, _ int) (ResolvePageResult, error) {
	if r.pageCalls == nil {
		r.pageCalls = make(map[uint64]int)
	}
	messageID := token.(uint64)
	call := r.pageCalls[messageID]
	r.pageCalls[messageID] = call + 1
	page := r.pagesByMessageID[messageID][call]
	return ResolvePageResult{Routes: append([]RouteKey(nil), page.routes...), NextCursor: page.nextCursor, Done: page.done}, nil
}

func (r *flakyResolver) BeginResolve(_ context.Context, key ChannelKey, env CommittedEnvelope) (any, error) {
	return flakyResolveToken{channelID: key.ChannelID, messageID: env.MessageID}, nil
}

func (r *flakyResolver) ResolvePage(_ context.Context, token any, _ string, _ int) (ResolvePageResult, error) {
	resolveToken := token.(flakyResolveToken)
	if err := r.failMessageIDs[resolveToken.messageID]; err != nil {
		delete(r.failMessageIDs, resolveToken.messageID)
		return ResolvePageResult{}, err
	}
	return ResolvePageResult{Routes: append([]RouteKey(nil), r.routesByChannel[resolveToken.channelID]...), Done: true}, nil
}

func TestDeliveryActorDoesNotHoldLockDuringResolveIO(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	firstRoute := testRoute("u2", 1, 11, 2)
	secondRoute := testRoute("u3", 1, 11, 3)
	resolver := newBlockingSecondPageResolver(firstRoute, secondRoute)
	pusher := &recordingPusher{}
	runtime := NewManager(Config{
		Resolver: resolver,
		Push:     pusher,
		Clock:    clock,
		Limits:   Limits{MaxInflightRoutesPerActor: 2},
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-resolve-lock", frame.ChannelTypeGroup, 801, 1, "resolve")))
	require.True(t, runtime.HasAckBinding(firstRoute.SessionID, 801))
	require.True(t, runtime.HasAckBinding(secondRoute.SessionID, 801))

	firstAckDone := make(chan error, 1)
	go func() {
		firstAckDone <- runtime.AckRoute(context.Background(), RouteAck{
			UID:        firstRoute.UID,
			SessionID:  firstRoute.SessionID,
			MessageID:  801,
			MessageSeq: 1,
		})
	}()
	resolver.WaitForBlockedPage(t)

	secondAckDone := make(chan error, 1)
	go func() {
		secondAckDone <- runtime.AckRoute(context.Background(), RouteAck{
			UID:        secondRoute.UID,
			SessionID:  secondRoute.SessionID,
			MessageID:  801,
			MessageSeq: 1,
		})
	}()

	select {
	case err := <-secondAckDone:
		require.NoError(t, err)
	case <-time.After(50 * time.Millisecond):
		resolver.Unblock()
		<-firstAckDone
		<-secondAckDone
		t.Fatal("second AckRoute blocked behind resolver I/O")
	}

	resolver.Unblock()
	require.NoError(t, <-firstAckDone)
}

func TestDeliveryActorDoesNotHoldLockDuringPushIO(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)}
	route := testRoute("u2", 1, 11, 2)
	pusher := newBlockingRetryPusher(route)
	t.Cleanup(pusher.Unblock)
	runtime := NewManager(Config{
		Resolver:         &stubResolver{routesByChannel: map[string][]RouteKey{"g-push-lock": {route}}},
		Push:             pusher,
		Clock:            clock,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 3,
	})

	require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-push-lock", frame.ChannelTypeGroup, 901, 1, "push")))
	require.True(t, runtime.HasAckBinding(route.SessionID, 901))

	clock.Advance(cappedBackoffWithJitter([]time.Duration{time.Second}, 2))
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- runtime.ProcessRetryTicks(context.Background())
	}()
	pusher.WaitForRetry(t)

	closedDone := make(chan error, 1)
	go func() {
		closedDone <- runtime.SessionClosed(context.Background(), SessionClosed{
			UID:       route.UID,
			SessionID: route.SessionID,
		})
	}()

	select {
	case err := <-closedDone:
		require.NoError(t, err)
	case <-time.After(50 * time.Millisecond):
		pusher.Unblock()
		<-retryDone
		<-closedDone
		t.Fatal("SessionClosed blocked behind push I/O")
	}

	pusher.Unblock()
	require.NoError(t, <-retryDone)
}

type blockingSecondPageResolver struct {
	firstRoutes []RouteKey
	secondRoute RouteKey
	started     chan struct{}
	release     chan struct{}
	once        sync.Once
	mu          sync.Mutex
	pageCalls   int
}

func newBlockingSecondPageResolver(firstRoute, secondRoute RouteKey) *blockingSecondPageResolver {
	return &blockingSecondPageResolver{
		firstRoutes: []RouteKey{firstRoute, secondRoute},
		secondRoute: secondRoute,
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (r *blockingSecondPageResolver) BeginResolve(_ context.Context, key ChannelKey, _ CommittedEnvelope) (any, error) {
	return key.ChannelID, nil
}

func (r *blockingSecondPageResolver) ResolvePage(_ context.Context, _ any, _ string, _ int) (ResolvePageResult, error) {
	r.mu.Lock()
	call := r.pageCalls
	r.pageCalls++
	r.mu.Unlock()
	if call == 0 {
		return ResolvePageResult{Routes: append([]RouteKey(nil), r.firstRoutes...), NextCursor: "after-first"}, nil
	}
	r.once.Do(func() { close(r.started) })
	<-r.release
	return ResolvePageResult{Routes: []RouteKey{r.secondRoute}, Done: true}, nil
}

func (r *blockingSecondPageResolver) WaitForBlockedPage(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resolver page")
	}
}

func (r *blockingSecondPageResolver) Unblock() {
	select {
	case <-r.release:
	default:
		close(r.release)
	}
}

type blockingRetryPusher struct {
	route   RouteKey
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   []PushCommand
}

func newBlockingRetryPusher(route RouteKey) *blockingRetryPusher {
	return &blockingRetryPusher{
		route:   route,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (p *blockingRetryPusher) Push(_ context.Context, cmd PushCommand) (PushResult, error) {
	p.mu.Lock()
	p.calls = append(p.calls, PushCommand{
		Envelope: cmd.Envelope,
		Routes:   append([]RouteKey(nil), cmd.Routes...),
		Attempt:  cmd.Attempt,
	})
	p.mu.Unlock()
	if cmd.Attempt == 2 {
		p.once.Do(func() { close(p.started) })
		<-p.release
	}
	return PushResult{Accepted: []RouteKey{p.route}}, nil
}

func (p *blockingRetryPusher) WaitForRetry(t *testing.T) {
	t.Helper()
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retry push")
	}
}

func (p *blockingRetryPusher) Unblock() {
	select {
	case <-p.release:
	default:
		close(p.release)
	}
}
