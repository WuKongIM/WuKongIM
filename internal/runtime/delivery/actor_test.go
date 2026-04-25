package delivery

import (
	"context"
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

func (r *pagedStubResolver) BeginResolve(_ context.Context, key ChannelKey, _ CommittedEnvelope) (any, error) {
	return key.ChannelID, nil
}

func (r *pagedStubResolver) ResolvePage(_ context.Context, token any, _ string, _ int) ([]RouteKey, string, bool, error) {
	if r.pageCalls == nil {
		r.pageCalls = make(map[string]int)
	}
	channelID := token.(string)
	call := r.pageCalls[channelID]
	r.pageCalls[channelID] = call + 1
	page := r.pagesByChannel[channelID][call]
	return append([]RouteKey(nil), page.routes...), page.nextCursor, page.done, nil
}

func (r *messagePagedResolver) BeginResolve(_ context.Context, _ ChannelKey, env CommittedEnvelope) (any, error) {
	return env.MessageID, nil
}

func (r *messagePagedResolver) ResolvePage(_ context.Context, token any, _ string, _ int) ([]RouteKey, string, bool, error) {
	if r.pageCalls == nil {
		r.pageCalls = make(map[uint64]int)
	}
	messageID := token.(uint64)
	call := r.pageCalls[messageID]
	r.pageCalls[messageID] = call + 1
	page := r.pagesByMessageID[messageID][call]
	return append([]RouteKey(nil), page.routes...), page.nextCursor, page.done, nil
}

func (r *flakyResolver) BeginResolve(_ context.Context, key ChannelKey, env CommittedEnvelope) (any, error) {
	return flakyResolveToken{channelID: key.ChannelID, messageID: env.MessageID}, nil
}

func (r *flakyResolver) ResolvePage(_ context.Context, token any, _ string, _ int) ([]RouteKey, string, bool, error) {
	resolveToken := token.(flakyResolveToken)
	if err := r.failMessageIDs[resolveToken.messageID]; err != nil {
		delete(r.failMessageIDs, resolveToken.messageID)
		return nil, "", false, err
	}
	return append([]RouteKey(nil), r.routesByChannel[resolveToken.channelID]...), "", true, nil
}
