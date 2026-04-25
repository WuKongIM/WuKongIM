package runtime

import (
	"errors"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestLimitsMaxFetchInflightPeerQueuesExcessReplication(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(51, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(52, 1, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(testChannelKey(51), 2)
	env.runtime.enqueueReplication(testChannelKey(52), 2)
	env.runtime.runScheduler()

	if got := env.sessions.session(2).sendCount(); got != 1 {
		t.Fatalf("expected one immediate send, got %d", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected one queued request, got %d", got)
	}
}

func TestLimitsFetchResponseDrainsQueuedReplicationForPeer(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(71, 3, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(72, 3, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(testChannelKey(71), 2)
	env.runtime.enqueueReplication(testChannelKey(72), 2)
	env.runtime.runScheduler()
	session := env.sessions.session(2)
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected queued request before response, got %d", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: testChannelKey(71),
		Generation: 1,
		Epoch:      3,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 9,
			Records:  []core.Record{{Payload: []byte("r1"), SizeBytes: 2}},
		},
	})

	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected queued send to drain after response, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 0 {
		t.Fatalf("expected peer queue drained, got %d", got)
	}
}

func TestLimitsFetchResponseApplyErrorKeepsPeerInflightAndQueuedReplication(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(81, 3, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(82, 3, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(83, 3, 1, []core.NodeID{1, 2}))

	env.factory.replicas[0].applyFetchErr = core.ErrCorruptState

	env.runtime.enqueueReplication(testChannelKey(81), 2)
	env.runtime.enqueueReplication(testChannelKey(82), 2)
	env.runtime.runScheduler()
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected queued request before failed response, got %d", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: testChannelKey(81),
		Generation: 1,
		Epoch:      3,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 9,
			Records:  []core.Record{{Payload: []byte("r1"), SizeBytes: 2}},
		},
	})

	if got := env.sessions.session(2).sendCount(); got != 1 {
		t.Fatalf("apply failure should not drain queued send, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("apply failure should keep queued request, got %d", got)
	}

	env.runtime.enqueueReplication(testChannelKey(83), 2)
	env.runtime.runScheduler()

	if got := env.sessions.session(2).sendCount(); got != 1 {
		t.Fatalf("apply failure should keep peer inflight, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 2 {
		t.Fatalf("expected later replication to remain queued, got %d", got)
	}
}

func TestLimitsDrainPeerQueueSendErrorRequeuesReplication(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(84, 3, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(85, 3, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(testChannelKey(84), 2)
	env.runtime.enqueueReplication(testChannelKey(85), 2)
	env.runtime.runScheduler()

	session := env.sessions.session(2)
	session.setSendErr(errors.New("boom"))
	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: testChannelKey(84),
		Generation: 1,
		Epoch:      3,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 9,
			Records:  []core.Record{{Payload: []byte("r1"), SizeBytes: 2}},
		},
	})

	session.setSendErr(nil)
	env.runtime.runScheduler()

	if got := session.sendCount(); got != 3 {
		t.Fatalf("expected queued replication to retry after drain send error, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 0 {
		t.Fatalf("expected peer queue drained after retry, got %d", got)
	}
}

func TestLimitsUnknownFetchResponseDoesNotReleasePeerInflight(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(73, 3, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(74, 3, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(testChannelKey(73), 2)
	env.runtime.enqueueReplication(testChannelKey(74), 2)
	env.runtime.runScheduler()

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: testChannelKey(999),
		Generation: 999,
		Epoch:      3,
		Kind:       MessageKindFetchResponse,
	})

	if got := env.sessions.session(2).sendCount(); got != 1 {
		t.Fatalf("unexpected send count after unknown response: %d", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("unknown response should not drain queue, got %d queued", got)
	}
}

func TestLimitsZeroRequestIDFetchResponseDoesNotReleaseInflight(t *testing.T) {
	state := newPeerRequestState()
	key := testChannelKey(777)

	if !state.tryAcquireChannel(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		RequestID:  7,
		Kind:       MessageKindFetchRequest,
	}) {
		t.Fatal("expected initial channel inflight reservation")
	}
	if !state.tryAcquire(2, 1) {
		t.Fatal("expected peer inflight reservation")
	}

	released := state.releaseInflightForEnvelope(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		RequestID:  0,
		Kind:       MessageKindFetchResponse,
	})
	if released {
		t.Fatal("zero request id fetch response must not release inflight reservation")
	}
	if state.tryAcquireChannel(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		RequestID:  8,
		Kind:       MessageKindFetchRequest,
	}) {
		t.Fatal("same channel should stay blocked while the real fetch request is still in flight")
	}
}

func TestLimitsStaleFetchFailureDoesNotReleaseCurrentGenerationInflight(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	key := testChannelKey(901)
	mustEnsureLocal(t, env.runtime, testMetaLocal(901, 3, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(902, 3, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	session := env.sessions.session(2)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected initial send, got %d", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		Epoch:      3,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 1,
		},
	})
	if got := env.runtime.queuedPeerRequests(2); got != 0 {
		t.Fatalf("expected queue drained after matching response, got %d", got)
	}

	if err := env.runtime.RemoveChannel(key); err != nil {
		t.Fatalf("RemoveChannel() error = %v", err)
	}
	mustEnsureLocal(t, env.runtime, testMetaLocal(901, 4, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected send for current generation, got %d", got)
	}
	env.runtime.enqueueReplication(testChannelKey(902), 2)
	env.runtime.runScheduler()
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected queued request before stale failure envelope, got %d", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		Epoch:      3,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchFailure,
	})

	if got := session.sendCount(); got != 2 {
		t.Fatalf("stale fetch failure should not free current inflight, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("stale fetch failure should keep peer queue blocked, got %d", got)
	}
}

func TestLimitsTombstonedFetchResponseDoesNotReleaseCurrentGenerationInflight(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	key := testChannelKey(911)
	mustEnsureLocal(t, env.runtime, testMetaLocal(911, 3, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(912, 3, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	session := env.sessions.session(2)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected initial send, got %d", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		Epoch:      3,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 1,
		},
	})

	if err := env.runtime.RemoveChannel(key); err != nil {
		t.Fatalf("RemoveChannel() error = %v", err)
	}
	mustEnsureLocal(t, env.runtime, testMetaLocal(911, 4, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected send for current generation, got %d", got)
	}
	env.runtime.enqueueReplication(testChannelKey(912), 2)
	env.runtime.runScheduler()
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected queued request before tombstoned envelope, got %d", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		Epoch:      3,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 1,
		},
	})

	if got := session.sendCount(); got != 2 {
		t.Fatalf("tombstoned response should not free current inflight, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("tombstoned response should keep peer queue blocked, got %d", got)
	}
}

func TestLimitsRemoveChannelClearsPeerInflightAndQueuedState(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	firstKey := testChannelKey(921)
	secondKey := testChannelKey(922)
	mustEnsureLocal(t, env.runtime, testMetaLocal(921, 3, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(922, 3, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(firstKey, 2)
	env.runtime.enqueueReplication(secondKey, 2)
	env.runtime.runScheduler()

	session := env.sessions.session(2)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected first request to be in-flight, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected second request queued, got %d", got)
	}

	if err := env.runtime.RemoveChannel(firstKey); err != nil {
		t.Fatalf("RemoveChannel() error = %v", err)
	}

	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected queued second request to drain after first channel removal, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 0 {
		t.Fatalf("expected peer queue to be clear after removal cleanup, got %d", got)
	}
}

func TestLimitsApplyMetaClearsStalePeerInflightAndQueueState(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	key := testChannelKey(923)
	mustEnsureLocal(t, env.runtime, testMetaLocal(923, 3, 1, []core.NodeID{1, 2, 3}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	session2 := env.sessions.session(2)
	if got := session2.sendCount(); got != 1 {
		t.Fatalf("expected first send to peer 2, got %d", got)
	}

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected queued stale request before meta change, got %d", got)
	}

	if err := env.runtime.ApplyMeta(testMetaLocal(923, 4, 1, []core.NodeID{1, 3})); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	if got := env.runtime.queuedPeerRequests(2); got != 0 {
		t.Fatalf("expected peer 2 queue cleared after meta change, got %d", got)
	}
	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		Epoch:      3,
		RequestID:  session2.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 1,
		},
	})
	if got := session2.sendCount(); got != 1 {
		t.Fatalf("stale peer should not receive follow-up sends, got %d", got)
	}

	env.runtime.enqueueReplication(key, 3)
	env.runtime.runScheduler()
	if got := env.sessions.session(3).sendCount(); got != 1 {
		t.Fatalf("expected replication to new valid peer, got %d sends", got)
	}
}

func TestLimitsHardBackpressureQueuesWithoutSending(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 2
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(75, 3, 1, []core.NodeID{1, 2}))

	env.sessions.session(2).setBackpressure(BackpressureState{Level: BackpressureHard})
	env.runtime.enqueueReplication(testChannelKey(75), 2)
	env.runtime.runScheduler()

	if got := env.sessions.session(2).sendCount(); got != 0 {
		t.Fatalf("hard backpressure should block immediate send, got %d", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("hard backpressure should queue request, got %d", got)
	}
}

func TestLimitsHardBackpressureClearingEventuallyDrainsQueuedWork(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 2
		cfg.FollowerReplicationRetryInterval = 5 * time.Millisecond
	})
	key := testChannelKey(76)
	mustEnsureLocal(t, env.runtime, testMetaLocal(76, 3, 1, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	session.setBackpressure(BackpressureState{Level: BackpressureHard})
	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()

	if got := session.sendCount(); got != 0 {
		t.Fatalf("hard backpressure should block immediate send, got %d", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("hard backpressure should queue request, got %d", got)
	}

	session.setBackpressure(BackpressureState{Level: BackpressureNone})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if session.sendCount() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected queued request to drain after hard backpressure clears, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 0 {
		t.Fatalf("expected queued requests to be drained after backpressure clears, got %d", got)
	}
}

func TestLimitsQueueDrainSuccessAllowsLaterRetryReplicationScheduling(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
		cfg.FollowerReplicationRetryInterval = time.Hour
	})
	key := testChannelKey(77)
	mustEnsureLocal(t, env.runtime, testMetaLocal(77, 3, 1, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	session.setBackpressure(BackpressureState{Level: BackpressureHard})

	env.runtime.retryReplication(key, 2, true)
	env.runtime.runScheduler()

	if got := session.sendCount(); got != 0 {
		t.Fatalf("hard backpressure should keep retry request queued, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected queued request under hard backpressure, got %d", got)
	}

	session.setBackpressure(BackpressureState{Level: BackpressureNone})
	env.runtime.drainPeerQueue(2)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected one successful drain send after backpressure clears, got %d", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		Epoch:      3,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 1,
		},
	})

	env.runtime.retryReplication(key, 2, true)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if session.sendCount() >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected retryReplication to schedule again after drained success, got %d sends", session.sendCount())
}

func TestLimitsPeerRequestStateRetainsQueueBucketAfterDrain(t *testing.T) {
	state := newPeerRequestState()
	peer := core.NodeID(2)

	state.enqueue(Envelope{Peer: peer, ChannelKey: testChannelKey(1)})
	state.enqueue(Envelope{Peer: peer, ChannelKey: testChannelKey(2)})

	if _, ok := state.popQueued(peer); !ok {
		t.Fatal("expected first queued envelope")
	}
	if _, ok := state.popQueued(peer); !ok {
		t.Fatal("expected second queued envelope")
	}

	if _, ok := state.queued[peer]; !ok {
		t.Fatal("expected drained peer queue bucket to be retained for reuse")
	}
	if got := state.queuedCount(peer); got != 0 {
		t.Fatalf("queuedCount() = %d, want 0", got)
	}

	state.enqueue(Envelope{Peer: peer, ChannelKey: testChannelKey(3)})
	if got := state.queuedCount(peer); got != 1 {
		t.Fatalf("queuedCount() after reuse = %d, want 1", got)
	}
}

func TestLimitsPeerQueueRetainsEnvelopeChannelKeyOrder(t *testing.T) {
	state := newPeerRequestState()
	peer := core.NodeID(2)

	state.enqueue(Envelope{Peer: peer, ChannelKey: testChannelKey(1)})
	state.enqueue(Envelope{Peer: peer, ChannelKey: testChannelKey(2)})
	state.enqueue(Envelope{Peer: peer, ChannelKey: testChannelKey(3)})

	for _, want := range []uint64{1, 2} {
		env, ok := state.popQueued(peer)
		if !ok {
			t.Fatalf("popQueued() missing envelope %d", want)
		}
		if env.ChannelKey != testChannelKey(want) {
			t.Fatalf("popQueued() group = %q, want %q", env.ChannelKey, testChannelKey(want))
		}
	}

	state.enqueue(Envelope{Peer: peer, ChannelKey: testChannelKey(4)})

	for _, want := range []uint64{3, 4} {
		env, ok := state.popQueued(peer)
		if !ok {
			t.Fatalf("popQueued() missing envelope %d", want)
		}
		if env.ChannelKey != testChannelKey(want) {
			t.Fatalf("popQueued() group = %q, want %q", env.ChannelKey, testChannelKey(want))
		}
	}
}

func newSessionTestEnvWithConfig(t *testing.T, mutate func(*Config)) *sessionTestEnv {
	t.Helper()

	generations := newSessionGenerationStore()
	factory := newSessionReplicaFactory()
	transport := &sessionTransport{}
	sessions := newSessionPeerSessionManager()

	cfg := Config{
		LocalNode:       1,
		ReplicaFactory:  factory,
		GenerationStore: generations,
		Transport:       transport,
		PeerSessions:    sessions,
		Tombstones: TombstonePolicy{
			TombstoneTTL: 30 * time.Second,
		},
		Now: func() time.Time { return time.Unix(1700000000, 0) },
	}
	if mutate != nil {
		mutate(&cfg)
	}

	rt, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return &sessionTestEnv{
		runtime:     rt.(*runtime),
		generations: generations,
		factory:     factory,
		transport:   transport,
		sessions:    sessions,
	}
}
