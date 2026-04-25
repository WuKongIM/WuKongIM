package delivery

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestManagerMaterializesOneVirtualActorPerChannel(t *testing.T) {
	runtime, _, _ := newTestManager()

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(1, 1)))
	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(2, 2)))

	require.Equal(t, 1, runtime.actorCount())
}

func TestManagerEvictsIdleActors(t *testing.T) {
	runtime, clock, _ := newTestManager()
	runtime.resolver.(*stubResolver).routesByChannel[testChannelID] = nil

	require.NoError(t, runtime.Submit(context.Background(), testEnvelope(1, 1)))
	require.Equal(t, 1, runtime.actorCount())

	clock.Advance(2 * time.Minute)
	runtime.SweepIdle()

	require.Zero(t, runtime.actorCount())
}

func TestManagerPromotesHighActivityChannelToDedicatedLane(t *testing.T) {
	runtime, _, _ := newTestManager(Config{
		Limits: Limits{DedicatedLaneActivityThreshold: 8},
	})

	for i := 0; i < 20; i++ {
		require.NoError(t, runtime.Submit(context.Background(), testEnvelopeFor("g-hot", frame.ChannelTypeGroup, uint64(i+1), uint64(i+1), "hot")))
	}

	require.Equal(t, LaneDedicated, runtime.ActorLane("g-hot", frame.ChannelTypeGroup))
}

func TestManagerSubmitKeepsShardLockUntilActorIsLocked(t *testing.T) {
	runtime, clock, _ := newTestManager()
	key := ChannelKey{ChannelID: "g-submit-race", ChannelType: frame.ChannelTypeGroup}
	shard := runtime.shardFor(key)

	shard.mu.Lock()
	act := shard.actorFor(key)
	shard.mu.Unlock()
	act.lastActive = clock.Now().Add(-2 * time.Minute).UnixNano()

	act.mu.Lock()
	submitDone := make(chan error, 1)
	go func() {
		submitDone <- runtime.Submit(context.Background(), testEnvelopeFor(key.ChannelID, key.ChannelType, 701, 1, "race"))
	}()

	require.Eventually(t, func() bool {
		if shard.mu.TryLock() {
			shard.mu.Unlock()
			return false
		}
		return true
	}, time.Second, 10*time.Millisecond)

	sweepDone := make(chan struct{})
	go func() {
		shard.sweepIdle()
		close(sweepDone)
	}()

	act.mu.Unlock()
	require.NoError(t, <-submitDone)
	<-sweepDone
	require.Equal(t, 1, runtime.actorCount())
}

func newTestManager(overrides ...Config) (*Manager, *testClock, *recordingPusher) {
	clock := &testClock{now: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)}
	pusher := &recordingPusher{}
	cfg := Config{
		Resolver: &stubResolver{
			routesByChannel: map[string][]RouteKey{
				testChannelID: {
					testRoute("u2", 1, 11, 2),
				},
			},
		},
		Push:             pusher,
		Clock:            clock,
		ShardCount:       1,
		IdleTimeout:      time.Minute,
		RetryDelays:      []time.Duration{time.Second},
		MaxRetryAttempts: 4,
	}
	if len(overrides) > 0 {
		override := overrides[0]
		if override.Resolver != nil {
			cfg.Resolver = override.Resolver
		}
		if override.Push != nil {
			cfg.Push = override.Push
		}
		if override.Clock != nil {
			cfg.Clock = override.Clock
		}
		if override.ShardCount != 0 {
			cfg.ShardCount = override.ShardCount
		}
		if override.ResolvePageSize != 0 {
			cfg.ResolvePageSize = override.ResolvePageSize
		}
		if override.IdleTimeout != 0 {
			cfg.IdleTimeout = override.IdleTimeout
		}
		if len(override.RetryDelays) > 0 {
			cfg.RetryDelays = override.RetryDelays
		}
		if override.MaxRetryAttempts != 0 {
			cfg.MaxRetryAttempts = override.MaxRetryAttempts
		}
		cfg.Limits = override.Limits
	}
	runtime := NewManager(cfg)
	return runtime, clock, pusher
}

type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time {
	return c.now
}

func (c *testClock) Advance(delta time.Duration) {
	c.now = c.now.Add(delta)
}

type stubResolver struct {
	routesByChannel map[string][]RouteKey
}

func (r *stubResolver) BeginResolve(_ context.Context, key ChannelKey, _ CommittedEnvelope) (any, error) {
	return key.ChannelID, nil
}

func (r *stubResolver) ResolvePage(_ context.Context, token any, cursor string, limit int) ([]RouteKey, string, bool, error) {
	key := ChannelKey{ChannelID: token.(string)}
	routes := r.routesByChannel[key.ChannelID]
	start := 0
	if cursor != "" {
		for i, route := range routes {
			if route.UID == cursor {
				start = i + 1
				break
			}
		}
	}
	if start >= len(routes) {
		return nil, cursor, true, nil
	}
	if limit <= 0 {
		limit = len(routes) - start
	}
	end := start + limit
	if end > len(routes) {
		end = len(routes)
	}
	page := append([]RouteKey(nil), routes[start:end]...)
	nextCursor := cursor
	if len(page) > 0 {
		nextCursor = page[len(page)-1].UID
	}
	return page, nextCursor, end >= len(routes), nil
}

func (r *stubResolver) ResolveRoutes(_ context.Context, key ChannelKey, _ CommittedEnvelope) ([]RouteKey, error) {
	routes := r.routesByChannel[key.ChannelID]
	return append([]RouteKey(nil), routes...), nil
}

type pushResponse struct {
	result PushResult
	err    error
}

type pushCall struct {
	envelope CommittedEnvelope
	routes   []RouteKey
}

type recordingPusher struct {
	calls     []pushCall
	responses []pushResponse
}

func (p *recordingPusher) Push(_ context.Context, cmd PushCommand) (PushResult, error) {
	copiedEnv := cmd.Envelope
	copiedEnv.Payload = append([]byte(nil), cmd.Envelope.Payload...)
	copiedRoutes := append([]RouteKey(nil), cmd.Routes...)
	p.calls = append(p.calls, pushCall{
		envelope: copiedEnv,
		routes:   copiedRoutes,
	})
	if len(p.responses) == 0 {
		return PushResult{Accepted: copiedRoutes}, nil
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp.result, resp.err
}

func (p *recordingPusher) pushedSeqs() []uint64 {
	out := make([]uint64, 0, len(p.calls))
	for _, call := range p.calls {
		out = append(out, call.envelope.MessageSeq)
	}
	return out
}

func (p *recordingPusher) attemptsFor(messageID uint64) int {
	count := 0
	for _, call := range p.calls {
		if call.envelope.MessageID == messageID {
			count++
		}
	}
	return count
}

func (p *recordingPusher) acceptedSessionIDs(messageID uint64) []uint64 {
	out := make([]uint64, 0, len(p.calls))
	for _, call := range p.calls {
		if call.envelope.MessageID != messageID {
			continue
		}
		for _, route := range call.routes {
			out = append(out, route.SessionID)
		}
	}
	return out
}

const testChannelID = "u1@u2"

func testEnvelope(messageID, messageSeq uint64) CommittedEnvelope {
	return testEnvelopeFor(testChannelID, frame.ChannelTypePerson, messageID, messageSeq, "hi")
}

func testEnvelopeFor(channelID string, channelType uint8, messageID, messageSeq uint64, payload string) CommittedEnvelope {
	return CommittedEnvelope{
		Message: channel.Message{
			ChannelID:   channelID,
			ChannelType: channelType,
			MessageID:   messageID,
			MessageSeq:  messageSeq,
			FromUID:     "u1",
			ClientMsgNo: "m1",
			Payload:     []byte(payload),
			ClientSeq:   9,
		},
	}
}

func testRoute(uid string, nodeID, bootID, sessionID uint64) RouteKey {
	return RouteKey{
		UID:       uid,
		NodeID:    nodeID,
		BootID:    bootID,
		SessionID: sessionID,
	}
}
