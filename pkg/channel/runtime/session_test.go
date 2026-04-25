package runtime

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replica"
	"github.com/stretchr/testify/require"
)

func testChannelKey(groupID uint64) core.ChannelKey {
	return core.ChannelKey("group-" + strconv.FormatUint(groupID, 10))
}

func TestSessionManyGroupsToSamePeerReuseOneSession(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(21, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(22, 1, 1, []core.NodeID{1, 2}))

	env.runtime.enqueueReplication(testChannelKey(21), 2)
	env.runtime.enqueueReplication(testChannelKey(22), 2)
	env.runtime.runScheduler()

	if got := env.sessions.createdFor(2); got != 1 {
		t.Fatalf("expected one session for peer 2, got %d", got)
	}
}

func TestSessionAutoRunForegroundDrainWaitsForWorker(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
	})
	key := testChannelKey(20901)
	mustEnsureLocal(t, env.runtime, testMetaLocal(20901, 1, 1, []core.NodeID{1, 2}))

	pause := make(chan struct{})
	popStarted := make(chan struct{}, 1)
	env.runtime.schedulerPopHook = func(popKey core.ChannelKey) {
		if popKey != key {
			return
		}
		select {
		case popStarted <- struct{}{}:
		default:
		}
		<-pause
	}

	env.runtime.enqueueReplication(key, 2)
	<-popStarted

	done := make(chan struct{})
	go func() {
		env.runtime.runScheduler()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("foreground runScheduler returned while auto worker held reserved work")
	case <-time.After(30 * time.Millisecond):
	}

	close(pause)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if env.sessions.session(2).sendCount() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if env.sessions.session(2).sendCount() == 0 {
		t.Fatal("expected auto-run worker to send fetch request")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("foreground runScheduler did not complete after worker release")
	}
}

func TestSessionReplicationRequestPopulatesFetchRequestEnvelope(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(27, 4, 1, []core.NodeID{1, 2}))

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 6
	replica.state.Epoch = 4
	replica.state.OffsetEpoch = 4
	replica.mu.Unlock()

	env.runtime.enqueueReplication(testChannelKey(27), 2)
	env.runtime.runScheduler()

	session := env.sessions.session(2)
	if session.sendCount() != 1 {
		t.Fatalf("expected one fetch request send, got %d", session.sendCount())
	}
	if session.last.Kind != MessageKindFetchRequest {
		t.Fatalf("last kind = %v, want fetch request", session.last.Kind)
	}
	if session.last.FetchRequest == nil {
		t.Fatal("expected fetch request payload")
	}
	if session.last.FetchRequest.ChannelKey != testChannelKey(27) {
		t.Fatalf("FetchRequest.ChannelKey = %q, want %q", session.last.FetchRequest.ChannelKey, testChannelKey(27))
	}
	if session.last.FetchRequest.ReplicaID != 1 {
		t.Fatalf("FetchRequest.ReplicaID = %d, want 1", session.last.FetchRequest.ReplicaID)
	}
	if session.last.FetchRequest.FetchOffset != 6 {
		t.Fatalf("FetchRequest.FetchOffset = %d, want 6", session.last.FetchRequest.FetchOffset)
	}
	if session.last.FetchRequest.OffsetEpoch != 4 {
		t.Fatalf("FetchRequest.OffsetEpoch = %d, want 4", session.last.FetchRequest.OffsetEpoch)
	}
	if session.last.FetchRequest.MaxBytes <= 0 {
		t.Fatalf("FetchRequest.MaxBytes = %d, want > 0", session.last.FetchRequest.MaxBytes)
	}
}

func TestSessionReplicationRequestUsesReplicaOffsetEpochInsteadOfChannelMetaEpoch(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(271, 4, 1, []core.NodeID{1, 2}))

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 6
	replica.state.Epoch = 4
	replica.state.OffsetEpoch = 3
	replica.mu.Unlock()

	env.runtime.enqueueReplication(testChannelKey(271), 2)
	env.runtime.runScheduler()

	session := env.sessions.session(2)
	if session.sendCount() != 1 {
		t.Fatalf("expected one fetch request send, got %d", session.sendCount())
	}
	if session.last.FetchRequest == nil {
		t.Fatal("expected fetch request payload")
	}
	if session.last.FetchRequest.OffsetEpoch != 3 {
		t.Fatalf("FetchRequest.OffsetEpoch = %d, want 3", session.last.FetchRequest.OffsetEpoch)
	}
}

func TestSessionQueuedReplicationRecomputesReplicaProgressBetweenSends(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(272, 4, 1, []core.NodeID{1, 2}))

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 6
	replica.state.Epoch = 4
	replica.state.OffsetEpoch = 4
	replica.mu.Unlock()

	session := env.sessions.session(2)
	session.afterSend = func() {
		replica.mu.Lock()
		defer replica.mu.Unlock()
		replica.state.LEO = 7
	}

	env.runtime.enqueueReplication(testChannelKey(272), 2)
	env.runtime.enqueueReplication(testChannelKey(272), 2)
	env.runtime.runScheduler()

	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected same group to keep one fetch request in flight until response, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected follow-up same-group replication to queue behind in-flight fetch, got %d", got)
	}
	if got := session.sent[0].FetchRequest.FetchOffset; got != 6 {
		t.Fatalf("first FetchOffset = %d, want 6", got)
	}

	env.runtime.releasePeerInflight(2)
	env.runtime.releaseChannelInflight(testChannelKey(272), 2)
	env.runtime.drainPeerQueue(2)

	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected queued same-group fetch request to send after inflight release, got %d sends", got)
	}
	if got := session.sent[1].FetchRequest.FetchOffset; got != 7 {
		t.Fatalf("second FetchOffset after queued drain = %d, want 7", got)
	}
}

func TestSessionQueuedReplicationRecomputesReplicaProgressAfterInflightQueueing(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(273, 4, 1, []core.NodeID{1, 2}))

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 6
	replica.state.Epoch = 4
	replica.state.OffsetEpoch = 4
	replica.mu.Unlock()

	env.runtime.enqueueReplication(testChannelKey(273), 2)
	env.runtime.enqueueReplication(testChannelKey(273), 2)
	env.runtime.runScheduler()

	session := env.sessions.session(2)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected one immediate fetch request send before draining queued inflight work, got %d", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected one queued peer request, got %d", got)
	}
	if got := session.sent[0].FetchRequest.FetchOffset; got != 6 {
		t.Fatalf("first FetchOffset = %d, want 6", got)
	}

	replica.mu.Lock()
	replica.state.LEO = 7
	replica.mu.Unlock()

	env.runtime.releasePeerInflight(2)
	env.runtime.releaseChannelInflight(testChannelKey(273), 2)
	env.runtime.drainPeerQueue(2)

	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected drained queued fetch request to send once inflight is released, got %d sends", got)
	}
	if got := session.sent[1].FetchRequest.FetchOffset; got != 7 {
		t.Fatalf("second FetchOffset after queued drain = %d, want 7", got)
	}
}

func TestSessionQueuedReplicationCoalescesSameGroupRequestsWhileInflight(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(2731, 4, 1, []core.NodeID{1, 2}))

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 6
	replica.state.Epoch = 4
	replica.state.OffsetEpoch = 4
	replica.mu.Unlock()

	env.runtime.enqueueReplication(testChannelKey(2731), 2)
	env.runtime.enqueueReplication(testChannelKey(2731), 2)
	env.runtime.enqueueReplication(testChannelKey(2731), 2)
	env.runtime.runScheduler()

	session := env.sessions.session(2)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected one immediate fetch request send, got %d", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected queued same-group fetches to coalesce to one request, got %d", got)
	}
}

func TestSessionConcurrentPeerInflightStillSerializesSameGroupReplication(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Limits.MaxFetchInflightPeer = 2
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(274, 4, 1, []core.NodeID{1, 2}))

	replica := env.factory.replicas[0]
	replica.mu.Lock()
	replica.state.LEO = 6
	replica.state.Epoch = 4
	replica.state.OffsetEpoch = 4
	replica.mu.Unlock()

	env.runtime.enqueueReplication(testChannelKey(274), 2)
	env.runtime.enqueueReplication(testChannelKey(274), 2)
	env.runtime.runScheduler()

	session := env.sessions.session(2)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected same group replication to keep one in-flight fetch even when peer limit is 2, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 1 {
		t.Fatalf("expected follow-up replication for same group to remain queued behind in-flight fetch, got %d", got)
	}
	if got := session.sent[0].FetchRequest.FetchOffset; got != 6 {
		t.Fatalf("first FetchOffset = %d, want 6", got)
	}

	replica.mu.Lock()
	replica.state.LEO = 7
	replica.mu.Unlock()

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: testChannelKey(274),
		Generation: 1,
		Epoch:      4,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 7,
		},
	})

	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected queued same-group replication to send after first fetch completes, got %d sends", got)
	}
	if got := session.sent[1].FetchRequest.FetchOffset; got != 7 {
		t.Fatalf("second FetchOffset after same-group drain = %d, want 7", got)
	}
}

func TestSessionReplicationSendErrorRetriesOnceWithoutNewWork(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(28, 4, 1, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	session.enqueueSendErrors(context.DeadlineExceeded)

	env.runtime.enqueueReplication(testChannelKey(28), 2)
	env.runtime.runScheduler()

	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected failed replication to be retried once automatically, got %d sends", got)
	}
}

func TestSessionReplicationSendErrorKeepsRetryingUntilSuccessWithoutNewWork(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(29, 4, 1, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	session.enqueueSendErrors(context.DeadlineExceeded, context.DeadlineExceeded)

	env.runtime.enqueueReplication(testChannelKey(29), 2)
	env.runtime.runScheduler()

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := session.sendCount(); got >= 3 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("expected failed replication to keep retrying until success, got %d sends", session.sendCount())
}

func TestSessionReplicationRetryIntervalUsesConfigOverride(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
		cfg.FollowerReplicationRetryInterval = time.Hour
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(30, 4, 1, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	session.enqueueSendErrors(context.DeadlineExceeded, context.DeadlineExceeded)

	env.runtime.enqueueReplication(testChannelKey(30), 2)
	env.runtime.runScheduler()

	time.Sleep(30 * time.Millisecond)
	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected only the immediate retry before custom interval elapses, got %d sends", got)
	}
}

func TestSessionScheduleFollowerReplicationCoalescesPendingDelayedRetry(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.FollowerReplicationRetryInterval = 15 * time.Millisecond
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(301, 4, 2, []core.NodeID{1, 2}))

	env.runtime.runScheduler()

	channelKey := testChannelKey(301)
	env.runtime.scheduleFollowerReplication(channelKey, 2)
	env.runtime.scheduleFollowerReplication(channelKey, 2)
	env.runtime.scheduleFollowerReplication(channelKey, 2)

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if ch, ok := env.runtime.lookupChannel(channelKey); ok {
			if got := queuedReplicationPeers(ch); got > 0 {
				if got != 1 {
					t.Fatalf("expected one delayed retry to be queued, got %d", got)
				}
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("expected delayed retry to be queued")
}

func TestSessionDelayedFollowerRetrySkipsStaleLeaderAfterMetaChange(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
		cfg.FollowerReplicationRetryInterval = 15 * time.Millisecond
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(302, 4, 2, []core.NodeID{1, 2, 3}))

	oldLeader := env.sessions.session(2)
	newLeader := env.sessions.session(3)

	env.runtime.runScheduler()
	if got := oldLeader.sendCount(); got != 1 {
		t.Fatalf("expected initial fetch to old leader, got %d sends", got)
	}

	env.runtime.scheduleFollowerReplication(testChannelKey(302), 2)
	if err := env.runtime.ApplyMeta(testMetaLocal(302, 5, 3, []core.NodeID{1, 2, 3})); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := newLeader.sendCount(); got >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := newLeader.sendCount(); got != 1 {
		t.Fatalf("expected immediate fetch to new leader, got %d sends", got)
	}

	time.Sleep(40 * time.Millisecond)
	if got := oldLeader.sendCount(); got != 1 {
		t.Fatalf("expected stale delayed retry to old leader to be skipped, got %d sends", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 0 {
		t.Fatalf("expected stale delayed retry to avoid queueing old leader fetches, got %d queued", got)
	}
}

func TestSessionApplyMetaSkipsQueuedReplicationForRemovedPeer(t *testing.T) {
	env := newSessionTestEnv(t)
	key := testChannelKey(303)
	mustEnsureLocal(t, env.runtime, testMetaLocal(303, 1, 1, []core.NodeID{1, 2, 3}))

	env.runtime.enqueueReplication(key, 2)
	if err := env.runtime.ApplyMeta(testMetaLocal(303, 2, 1, []core.NodeID{1, 3})); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
	env.runtime.runScheduler()

	if got := env.sessions.session(2).sendCount(); got != 0 {
		t.Fatalf("expected removed peer to receive no replication after meta change, got %d sends", got)
	}

	env.runtime.enqueueReplication(key, 3)
	env.runtime.runScheduler()
	if got := env.sessions.session(3).sendCount(); got != 1 {
		t.Fatalf("expected replication to valid peer after meta change, got %d sends", got)
	}
}

func TestSessionRuntimeCloseClosesCachedPeerSessions(t *testing.T) {
	env := newSessionTestEnv(t)
	key := testChannelKey(304)
	mustEnsureLocal(t, env.runtime, testMetaLocal(304, 1, 1, []core.NodeID{1, 2, 3}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.enqueueReplication(key, 3)
	env.runtime.runScheduler()

	if err := env.runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := env.sessions.session(2).closeCount(); got != 1 {
		t.Fatalf("expected peer 2 session close count = 1, got %d", got)
	}
	if got := env.sessions.session(3).closeCount(); got != 1 {
		t.Fatalf("expected peer 3 session close count = 1, got %d", got)
	}
}

func TestSessionRuntimeClosePreventsPostCloseSessionRecreation(t *testing.T) {
	env := newSessionTestEnv(t)
	key := testChannelKey(305)
	mustEnsureLocal(t, env.runtime, testMetaLocal(305, 1, 1, []core.NodeID{1, 2, 3}))

	const inflightRequestID = 7001
	const queuedRequestID = 7002
	if !env.runtime.peerRequests.tryAcquireChannel(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		RequestID:  inflightRequestID,
		Kind:       MessageKindFetchRequest,
	}) {
		t.Fatal("expected to reserve in-flight channel/peer request state")
	}
	env.runtime.peerRequests.enqueue(Envelope{
		Peer:       2,
		ChannelKey: key,
		Epoch:      1,
		Generation: 1,
		RequestID:  queuedRequestID,
		Kind:       MessageKindFetchRequest,
		FetchRequest: &FetchRequestEnvelope{
			ChannelKey:  key,
			Epoch:       1,
			Generation:  1,
			ReplicaID:   1,
			FetchOffset: 1,
			OffsetEpoch: 1,
			MaxBytes:    128,
		},
	})
	env.runtime.tombstones.add(key, 1, env.runtime.cfg.Now().Add(time.Minute))

	if err := env.runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	createdBefore := env.sessions.createdFor(2)
	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		RequestID:  inflightRequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			ChannelKey: key,
			Epoch:      1,
			Generation: 1,
			LeaderHW:   1,
		},
	})

	time.Sleep(20 * time.Millisecond)
	if got := env.sessions.createdFor(2); got != createdBefore {
		t.Fatalf("expected no post-close session recreation, created count %d -> %d", createdBefore, got)
	}
}

func TestSessionApplyMetaEvictsAndClosesInvalidPeerSession(t *testing.T) {
	env := newSessionTestEnv(t)
	key := testChannelKey(306)
	mustEnsureLocal(t, env.runtime, testMetaLocal(306, 1, 1, []core.NodeID{1, 2, 3}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	session2 := env.sessions.session(2)
	if got := session2.sendCount(); got != 1 {
		t.Fatalf("expected initial replication to create peer session, got %d sends", got)
	}

	if err := env.runtime.ApplyMeta(testMetaLocal(306, 2, 1, []core.NodeID{1, 3})); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	if got := session2.closeCount(); got != 1 {
		t.Fatalf("expected invalid peer session to be closed once, got %d", got)
	}
	env.runtime.sessions.mu.Lock()
	_, exists := env.runtime.sessions.sessions[2]
	env.runtime.sessions.mu.Unlock()
	if exists {
		t.Fatal("expected invalid peer session to be evicted from runtime cache")
	}
}

func TestSessionApplyMetaKeepsPeerSessionWhenStillValidOnOtherChannel(t *testing.T) {
	env := newSessionTestEnv(t)
	first := testChannelKey(307)
	second := testChannelKey(308)
	mustEnsureLocal(t, env.runtime, testMetaLocal(307, 1, 1, []core.NodeID{1, 2, 3}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(308, 1, 1, []core.NodeID{1, 2, 4}))

	env.runtime.enqueueReplication(first, 2)
	env.runtime.enqueueReplication(second, 2)
	env.runtime.runScheduler()
	session2 := env.sessions.session(2)
	if got := session2.sendCount(); got == 0 {
		t.Fatal("expected replication to use peer 2 session")
	}

	if err := env.runtime.ApplyMeta(testMetaLocal(307, 2, 1, []core.NodeID{1, 3})); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	if got := session2.closeCount(); got != 0 {
		t.Fatalf("expected peer session to stay open while still valid elsewhere, got close count %d", got)
	}
	env.runtime.sessions.mu.Lock()
	_, exists := env.runtime.sessions.sessions[2]
	env.runtime.sessions.mu.Unlock()
	if !exists {
		t.Fatal("expected peer session to remain cached while still valid for another channel")
	}
}

func TestSessionApplyMetaDoesNotRecreateEvictedSessionFromStaleInFlightReplication(t *testing.T) {
	env := newSessionTestEnv(t)
	key := testChannelKey(309)
	mustEnsureLocal(t, env.runtime, testMetaLocal(309, 1, 1, []core.NodeID{1, 2, 3}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	session2 := env.sessions.session(2)
	if got := session2.sendCount(); got != 1 {
		t.Fatalf("expected initial replication send, got %d", got)
	}

	if err := env.runtime.ApplyMeta(testMetaLocal(309, 2, 1, []core.NodeID{1, 3})); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
	if got := session2.closeCount(); got != 1 {
		t.Fatalf("expected stale peer session to be evicted and closed, got close count %d", got)
	}

	if err := env.runtime.sendEnvelope(Envelope{
		Peer:       2,
		ChannelKey: key,
		Epoch:      1,
		Generation: 1,
		RequestID:  9001,
		Kind:       MessageKindFetchRequest,
		FetchRequest: &FetchRequestEnvelope{
			ChannelKey:  key,
			Epoch:       1,
			Generation:  1,
			ReplicaID:   1,
			FetchOffset: 1,
			OffsetEpoch: 1,
			MaxBytes:    128,
		},
	}); err != nil {
		t.Fatalf("sendEnvelope() error = %v", err)
	}

	if got := env.sessions.createdFor(2); got != 1 {
		t.Fatalf("expected stale send to not recreate peer session, got created count %d", got)
	}
	if got := session2.sendCount(); got != 1 {
		t.Fatalf("expected stale replication not to send after meta churn, got %d sends", got)
	}
}

func TestSessionApplyMetaDuringSendRaceDoesNotRecreateInvalidPeerSession(t *testing.T) {
	env := newSessionTestEnv(t)
	key := testChannelKey(310)
	mustEnsureLocal(t, env.runtime, testMetaLocal(310, 1, 1, []core.NodeID{1, 2, 3}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	session2 := env.sessions.session(2)
	if got := session2.sendCount(); got != 1 {
		t.Fatalf("expected initial replication send, got %d", got)
	}
	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		Epoch:      1,
		RequestID:  session2.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 1,
		},
	})

	paused := make(chan struct{}, 1)
	release := make(chan struct{})
	env.runtime.beforePeerSessionHook = func(outbound Envelope) {
		if outbound.Kind != MessageKindFetchRequest || outbound.ChannelKey != key || outbound.Peer != 2 {
			return
		}
		select {
		case paused <- struct{}{}:
		default:
		}
		<-release
	}

	env.runtime.enqueueReplication(key, 2)
	done := make(chan struct{})
	go func() {
		env.runtime.runScheduler()
		close(done)
	}()
	<-paused

	if err := env.runtime.ApplyMeta(testMetaLocal(310, 2, 1, []core.NodeID{1, 3})); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
	if got := session2.closeCount(); got != 1 {
		t.Fatalf("expected stale peer session to be evicted and closed, got close count %d", got)
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runScheduler did not finish")
	}

	if got := env.sessions.createdFor(2); got != 1 {
		t.Fatalf("expected raced stale send to not recreate peer session, got created count %d", got)
	}
	if got := session2.sendCount(); got != 1 {
		t.Fatalf("expected raced stale replication not to send after meta churn, got %d sends", got)
	}
}

func TestSessionRemoveChannelDuringSendRaceDropsStaleReplication(t *testing.T) {
	env := newSessionTestEnv(t)
	key := testChannelKey(311)
	mustEnsureLocal(t, env.runtime, testMetaLocal(311, 1, 1, []core.NodeID{1, 2, 3}))

	env.runtime.enqueueReplication(key, 2)
	env.runtime.runScheduler()
	session2 := env.sessions.session(2)
	if got := session2.sendCount(); got != 1 {
		t.Fatalf("expected initial replication send, got %d", got)
	}
	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: key,
		Generation: 1,
		Epoch:      1,
		RequestID:  session2.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 1,
		},
	})

	paused := make(chan struct{}, 1)
	release := make(chan struct{})
	env.runtime.afterOutboundValidationHook = func(outbound Envelope) {
		if outbound.Kind != MessageKindFetchRequest || outbound.ChannelKey != key || outbound.Peer != 2 {
			return
		}
		select {
		case paused <- struct{}{}:
		default:
		}
		<-release
	}

	env.runtime.enqueueReplication(key, 2)
	doneScheduler := make(chan struct{})
	go func() {
		env.runtime.runScheduler()
		close(doneScheduler)
	}()
	<-paused

	doneRemove := make(chan error, 1)
	go func() {
		doneRemove <- env.runtime.RemoveChannel(key)
	}()
	select {
	case err := <-doneRemove:
		t.Fatalf("RemoveChannel() returned before in-flight send released: %v", err)
	default:
	}
	if _, ok := env.runtime.lookupChannel(key); !ok {
		t.Fatal("expected channel to remain visible while in-flight send still holds send serialization")
	}

	close(release)
	select {
	case <-doneScheduler:
	case <-time.After(time.Second):
		t.Fatal("runScheduler did not finish")
	}
	select {
	case err := <-doneRemove:
		if err != nil {
			t.Fatalf("RemoveChannel() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RemoveChannel did not finish")
	}

	if _, ok := env.runtime.lookupChannel(key); ok {
		t.Fatal("expected channel to be removed after RemoveChannel returns")
	}
}

func TestSessionLongPollApplyMetaDoesNotBlockOnInFlightPoll(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Second
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = time.Hour
	})
	key := testChannelKey(3111)
	mustEnsureLocal(t, env.runtime, testMetaLocal(3111, 4, 2, []core.NodeID{1, 2, 3}))

	session2 := env.sessions.session(2)
	paused, release := blockPeerSessionSend(t, session2, func(env Envelope) bool {
		return env.Kind == MessageKindLanePollRequest && env.ChannelKey == key && env.Peer == 2
	})
	defer release()

	doneScheduler := make(chan struct{})
	go func() {
		env.runtime.runScheduler()
		close(doneScheduler)
	}()

	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for long-poll send to block")
	}

	doneApply := make(chan error, 1)
	go func() {
		doneApply <- env.runtime.ApplyMeta(testMetaLocal(3111, 5, 3, []core.NodeID{1, 3}))
	}()

	select {
	case err := <-doneApply:
		if err != nil {
			t.Fatalf("ApplyMeta() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		release()
		select {
		case <-doneScheduler:
		case <-time.After(time.Second):
			t.Fatal("runScheduler did not finish after releasing blocked long poll")
		}
		select {
		case err := <-doneApply:
			if err != nil {
				t.Fatalf("ApplyMeta() error after release = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("ApplyMeta did not finish after releasing blocked long poll")
		}
		t.Fatal("ApplyMeta blocked on in-flight long poll send")
	}

	if got := session2.closeCount(); got != 1 {
		t.Fatalf("expected stale peer session to be evicted and closed, got close count %d", got)
	}

	release()
	select {
	case <-doneScheduler:
	case <-time.After(time.Second):
		t.Fatal("runScheduler did not finish")
	}

	ch, ok := env.runtime.lookupChannel(key)
	if !ok {
		t.Fatal("expected channel to remain after ApplyMeta")
	}
	meta := ch.metaSnapshot()
	if meta.Epoch != 5 || meta.Leader != 3 {
		t.Fatalf("channel meta after ApplyMeta = %+v, want epoch 5 leader 3", meta)
	}
}

func TestSessionLongPollRemoveChannelDoesNotBlockOnInFlightPoll(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Second
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = time.Hour
	})
	key := testChannelKey(3112)
	mustEnsureLocal(t, env.runtime, testMetaLocal(3112, 4, 2, []core.NodeID{1, 2, 3}))

	session2 := env.sessions.session(2)
	paused, release := blockPeerSessionSend(t, session2, func(env Envelope) bool {
		return env.Kind == MessageKindLanePollRequest && env.ChannelKey == key && env.Peer == 2
	})
	defer release()

	doneScheduler := make(chan struct{})
	go func() {
		env.runtime.runScheduler()
		close(doneScheduler)
	}()

	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for long-poll send to block")
	}

	doneRemove := make(chan error, 1)
	go func() {
		doneRemove <- env.runtime.RemoveChannel(key)
	}()

	select {
	case err := <-doneRemove:
		if err != nil {
			t.Fatalf("RemoveChannel() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		release()
		select {
		case <-doneScheduler:
		case <-time.After(time.Second):
			t.Fatal("runScheduler did not finish after releasing blocked long poll")
		}
		select {
		case err := <-doneRemove:
			if err != nil {
				t.Fatalf("RemoveChannel() error after release = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("RemoveChannel did not finish after releasing blocked long poll")
		}
		t.Fatal("RemoveChannel blocked on in-flight long poll send")
	}

	if got := session2.closeCount(); got != 1 {
		t.Fatalf("expected removed peer session to be closed, got close count %d", got)
	}
	if _, ok := env.runtime.lookupChannel(key); ok {
		t.Fatal("expected channel to be removed before blocked long poll completes")
	}

	release()
	select {
	case <-doneScheduler:
	case <-time.After(time.Second):
		t.Fatal("runScheduler did not finish")
	}
}

func TestSessionApplyMetaFailureLeavesCachedChannelMetaUnchanged(t *testing.T) {
	env := newSessionTestEnv(t)
	initial := testMetaLocal(31, 1, 2, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, initial)

	replica := env.factory.replicas[0]
	replica.becomeLeaderErr = context.DeadlineExceeded

	err := env.runtime.ApplyMeta(testMetaLocal(31, 2, 1, []core.NodeID{1, 2}))
	if err == nil {
		t.Fatal("expected ApplyMeta to fail")
	}

	ch, ok := env.runtime.lookupChannel(testChannelKey(31))
	if !ok {
		t.Fatal("expected channel to exist")
	}
	meta := ch.metaSnapshot()
	if meta.Epoch != initial.Epoch || meta.Leader != initial.Leader {
		t.Fatalf("cached meta changed on failure: %+v", meta)
	}
	if state := replica.Status(); state.Role != core.ReplicaRoleFollower || state.Epoch != initial.Epoch || state.Leader != initial.Leader {
		t.Fatalf("replica state changed on failure: %+v", state)
	}
}

func TestSessionInboundEnvelopeDemuxRequiresMatchingGeneration(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(23, 1, 1, []core.NodeID{1, 2}))

	env.transport.deliver(Envelope{ChannelKey: testChannelKey(23), Generation: 99, Epoch: 1, Kind: MessageKindFetchResponse})
	if env.factory.replicas[0].applyFetchCalls != 0 {
		t.Fatalf("unexpected apply fetch on generation mismatch")
	}
}

func TestSessionFetchResponseDropsStaleEpoch(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(24, 3, 1, []core.NodeID{1, 2}))

	env.transport.deliver(Envelope{
		Peer:          2,
		ChannelKey:    testChannelKey(24),
		Generation:    1,
		Epoch:         2,
		Kind:          MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{LeaderHW: 5, Records: []core.Record{{Payload: []byte("stale"), SizeBytes: 5}}},
	})
	if env.factory.replicas[0].applyFetchCalls != 0 {
		t.Fatalf("stale epoch response should be dropped")
	}
}

func TestSessionFetchResponseDecodesPayloadIntoApplyFetch(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(25, 4, 1, []core.NodeID{1, 2}))

	truncateTo := uint64(7)
	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: testChannelKey(25),
		Generation: 1,
		Epoch:      4,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW:   11,
			TruncateTo: &truncateTo,
			Records:    []core.Record{{Payload: []byte("ok"), SizeBytes: 2}},
		},
	})

	if env.factory.replicas[0].applyFetchCalls != 1 {
		t.Fatalf("expected fetch response to be applied")
	}
	got := env.factory.replicas[0].lastApplyFetch
	if got.LeaderHW != 11 {
		t.Fatalf("expected LeaderHW 11, got %d", got.LeaderHW)
	}
	if got.TruncateTo == nil || *got.TruncateTo != 7 {
		t.Fatalf("expected TruncateTo 7, got %+v", got.TruncateTo)
	}
	if len(got.Records) != 1 || string(got.Records[0].Payload) != "ok" {
		t.Fatalf("unexpected records: %+v", got.Records)
	}
}

func TestSessionFetchResponseReentrantQueuedPeerDrainDoesNotDeadlock(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(2502, 4, 2, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(2503, 4, 2, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	delivered := false
	session.afterSend = func() {
		if delivered {
			return
		}
		delivered = true
		env.runtime.peerRequests.enqueue(Envelope{
			Peer:       2,
			ChannelKey: testChannelKey(2503),
			Epoch:      4,
			Generation: 1,
			RequestID:  2,
			Kind:       MessageKindFetchRequest,
			FetchRequest: &FetchRequestEnvelope{
				ChannelKey:  testChannelKey(2503),
				Epoch:       4,
				Generation:  1,
				ReplicaID:   1,
				FetchOffset: 0,
				OffsetEpoch: 4,
				MaxBytes:    defaultFetchMaxBytes,
			},
		})
		env.transport.deliver(Envelope{
			Peer:       2,
			ChannelKey: testChannelKey(2502),
			Generation: 1,
			Epoch:      4,
			RequestID:  1,
			Kind:       MessageKindFetchResponse,
			Sync:       true,
			FetchResponse: &FetchResponseEnvelope{
				LeaderHW: 0,
			},
		})
	}

	done := make(chan error, 1)
	go func() {
		done <- env.runtime.sendEnvelope(Envelope{
			Peer:       2,
			ChannelKey: testChannelKey(2502),
			Epoch:      4,
			Generation: 1,
			RequestID:  1,
			Kind:       MessageKindFetchRequest,
			FetchRequest: &FetchRequestEnvelope{
				ChannelKey:  testChannelKey(2502),
				Epoch:       4,
				Generation:  1,
				ReplicaID:   1,
				FetchOffset: 0,
				OffsetEpoch: 4,
				MaxBytes:    defaultFetchMaxBytes,
			},
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sendEnvelope() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("sendEnvelope deadlocked while draining queued peer work from a synchronous fetch response")
	}

	session.mu.Lock()
	sent := append([]Envelope(nil), session.sent...)
	session.mu.Unlock()
	if len(sent) < 2 {
		t.Fatalf("expected initial fetch plus queued drain send, got %d sends", len(sent))
	}
	if sent[1].Kind != MessageKindFetchRequest {
		t.Fatalf("second outbound kind = %v, want fetch request", sent[1].Kind)
	}
	if sent[1].ChannelKey != testChannelKey(2503) {
		t.Fatalf("second outbound channel = %q, want %q", sent[1].ChannelKey, testChannelKey(2503))
	}
}

func TestSessionNonEmptyFetchResponseQueuesImmediateFollowerRefetch(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.FollowerReplicationRetryInterval = time.Hour
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(251, 4, 2, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	env.runtime.runScheduler()
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected initial follower fetch request, got %d sends", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: testChannelKey(251),
		Generation: 1,
		Epoch:      4,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 1,
			Records:  []core.Record{{Payload: []byte("ok"), SizeBytes: 2}},
		},
	})

	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected immediate follower re-fetch after apply, got %d sends", got)
	}
	if session.last.Kind != MessageKindFetchRequest {
		t.Fatalf("last kind = %v, want fetch request", session.last.Kind)
	}
	if session.last.ChannelKey != testChannelKey(251) {
		t.Fatalf("last group = %q, want %q", session.last.ChannelKey, testChannelKey(251))
	}
}

func TestSessionLongPollTimedOutResponseImmediatelyReissuesPoll(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = 2 * time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = time.Hour
	})
	key := testChannelKey(2601)
	mustEnsureLocal(t, env.runtime, testMetaLocal(2601, 4, 2, []core.NodeID{1, 2}))

	env.runtime.runScheduler()

	session := env.sessions.session(2)
	waitForSessionSendCount(t, session, 1)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected initial long poll open, got %d sends", got)
	}
	if session.last.Kind != MessageKindLanePollRequest {
		t.Fatalf("last kind = %v, want lane poll request", session.last.Kind)
	}
	first := session.last.LanePollRequest
	if first == nil {
		t.Fatal("expected lane poll request payload")
	}
	if first.Op != LanePollOpOpen {
		t.Fatalf("initial op = %v, want open", first.Op)
	}

	_, release := blockPeerSessionSend(t, session, func(env Envelope) bool {
		return env.Kind == MessageKindLanePollRequest &&
			env.LanePollRequest != nil &&
			env.LanePollRequest.LaneID == first.LaneID &&
			env.LanePollRequest.Op == LanePollOpPoll
	})
	defer release()

	env.transport.deliver(Envelope{
		Peer: 2,
		Kind: MessageKindLanePollResponse,
		Sync: true,
		LanePollResponse: &LanePollResponseEnvelope{
			LaneID:       first.LaneID,
			Status:       LanePollStatusOK,
			SessionID:    501,
			SessionEpoch: 1,
			TimedOut:     true,
		},
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if laneDispatchHasWork(env.runtime, 2, first.LaneID) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !laneDispatchHasWork(env.runtime, 2, first.LaneID) {
		t.Fatalf("expected timed-out response to schedule lane %d on the dispatcher", first.LaneID)
	}

	release()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if session.sendCount() >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected timed-out empty response to promptly reissue poll, got %d sends", got)
	}
	if session.last.Kind != MessageKindLanePollRequest {
		t.Fatalf("last kind after timeout = %v, want lane poll request", session.last.Kind)
	}
	if session.last.LanePollRequest == nil || session.last.LanePollRequest.Op != LanePollOpPoll {
		t.Fatalf("reissue request = %+v, want poll", session.last.LanePollRequest)
	}
	if session.last.LanePollRequest.LaneID != testFollowerLaneFor(key, 4) {
		t.Fatalf("reissue lane = %d, want %d", session.last.LanePollRequest.LaneID, testFollowerLaneFor(key, 4))
	}
}

func laneDispatchHasWork(rt *runtime, peer core.NodeID, lane uint16) bool {
	rt.laneDispatcher.mu.Lock()
	defer rt.laneDispatcher.mu.Unlock()

	key := laneDispatchWorkKey{peer: peer, lane: lane}
	if _, ok := rt.laneDispatcher.queued[key]; ok {
		return true
	}
	if _, ok := rt.laneDispatcher.processing[key]; ok {
		return true
	}
	if _, ok := rt.laneDispatcher.dirty[key]; ok {
		return true
	}
	return false
}

func laneRetryScheduled(rt *runtime, peer core.NodeID, lane uint16) bool {
	rt.laneRetryMu.Lock()
	defer rt.laneRetryMu.Unlock()

	state, ok := rt.laneRetry[PeerLaneKey{Peer: peer, LaneID: lane}]
	return ok && state.timer != nil
}

func waitForSessionSendCount(t *testing.T, session *trackingPeerSession, want int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := session.sendCount(); got >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("expected at least %d sends, got %d", want, session.sendCount())
}

func TestSessionLongPollTimedOutReissueDoesNotStarveOtherPendingLanes(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 2
		cfg.LongPollMaxWait = 2 * time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = time.Hour
	})

	firstKey := testChannelKeyForLane(t, 0, 2, "lp-starve-first")
	secondKey := testChannelKeyForLane(t, 1, 2, "lp-starve-second")
	mustEnsureLocal(t, env.runtime, core.Meta{
		Key:      firstKey,
		Epoch:    11,
		Leader:   2,
		Replicas: []core.NodeID{1, 2, 3},
		ISR:      []core.NodeID{1, 2, 3},
		MinISR:   2,
	})
	mustEnsureLocal(t, env.runtime, core.Meta{
		Key:      secondKey,
		Epoch:    12,
		Leader:   2,
		Replicas: []core.NodeID{1, 2, 3},
		ISR:      []core.NodeID{1, 2, 3},
		MinISR:   2,
	})

	session := env.sessions.session(2)
	var firstLaneResponses int
	session.afterSend = func() {
		session.mu.Lock()
		last := session.last
		session.mu.Unlock()

		if last.Kind != MessageKindLanePollRequest || last.LanePollRequest == nil {
			return
		}
		if last.LanePollRequest.LaneID != 0 || firstLaneResponses >= 3 {
			return
		}
		firstLaneResponses++
		env.transport.deliver(Envelope{
			Peer: 2,
			Kind: MessageKindLanePollResponse,
			Sync: true,
			LanePollResponse: &LanePollResponseEnvelope{
				LaneID:       0,
				Status:       LanePollStatusOK,
				SessionID:    601,
				SessionEpoch: 1,
				TimedOut:     true,
			},
		})
	}

	env.runtime.runScheduler()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session.mu.Lock()
		sent := append([]Envelope(nil), session.sent...)
		session.mu.Unlock()
		if len(sent) < 2 {
			time.Sleep(time.Millisecond)
			continue
		}
		for _, env := range sent {
			if env.Kind != MessageKindLanePollRequest || env.LanePollRequest == nil {
				continue
			}
			if env.LanePollRequest.LaneID != 0 {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}

	session.mu.Lock()
	sent := append([]Envelope(nil), session.sent...)
	session.mu.Unlock()
	lanes := make([]uint16, 0, len(sent))
	for _, env := range sent {
		if env.Kind != MessageKindLanePollRequest || env.LanePollRequest == nil {
			continue
		}
		lanes = append(lanes, env.LanePollRequest.LaneID)
	}
	t.Fatalf("timed-out lane reissue monopolized sends before another populated lane opened, got lanes %v", lanes)
}

func TestSessionLongPollEnsureChannelStartsInitialOpenForEveryPopulatedLane(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 8
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = time.Hour
		cfg.AutoRunScheduler = true
	})

	const (
		laneCount       = 8
		channelsPerLane = 4
	)
	for lane := 0; lane < laneCount; lane++ {
		for replicaIdx := 0; replicaIdx < channelsPerLane; replicaIdx++ {
			key := testChannelKeyForLane(t, uint16(lane), laneCount, "lp-init-"+strconv.Itoa(lane)+"-"+strconv.Itoa(replicaIdx))
			mustEnsureLocal(t, env.runtime, core.Meta{
				Key:      key,
				Epoch:    uint64(100 + lane*channelsPerLane + replicaIdx),
				Leader:   2,
				Replicas: []core.NodeID{1, 2, 3},
				ISR:      []core.NodeID{1, 2, 3},
				MinISR:   2,
			})
		}
	}

	session := env.sessions.session(2)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session.mu.Lock()
		sent := append([]Envelope(nil), session.sent...)
		session.mu.Unlock()

		laneOpens := make(map[uint16]struct{})
		for _, env := range sent {
			if env.Kind != MessageKindLanePollRequest || env.LanePollRequest == nil {
				continue
			}
			if env.LanePollRequest.Op != LanePollOpOpen {
				continue
			}
			laneOpens[env.LanePollRequest.LaneID] = struct{}{}
		}
		if len(laneOpens) == laneCount {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	session.mu.Lock()
	sent := append([]Envelope(nil), session.sent...)
	session.mu.Unlock()
	laneOpens := make(map[uint16]struct{})
	for _, env := range sent {
		if env.Kind != MessageKindLanePollRequest || env.LanePollRequest == nil {
			continue
		}
		if env.LanePollRequest.Op != LanePollOpOpen {
			continue
		}
		laneOpens[env.LanePollRequest.LaneID] = struct{}{}
	}
	t.Fatalf("initial long-poll opens covered lanes=%v, want all %d lanes", laneOpens, laneCount)
}

func TestSessionLongPollFollowerStartupSchedulesDispatcher(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = time.Hour
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(2603, 4, 2, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	paused, release := blockPeerSessionSend(t, session, func(env Envelope) bool {
		return env.Kind == MessageKindLanePollRequest &&
			env.LanePollRequest != nil &&
			env.LanePollRequest.Op == LanePollOpOpen
	})
	defer release()

	done := make(chan struct{})
	go func() {
		env.runtime.runScheduler()
		close(done)
	}()

	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("expected follower startup to begin a lane poll send")
	}

	session.mu.Lock()
	last := session.last
	session.mu.Unlock()
	if last.LanePollRequest == nil {
		t.Fatal("expected lane poll request payload")
	}
	if !laneDispatchHasWork(env.runtime, 2, last.LanePollRequest.LaneID) {
		t.Fatalf("expected follower startup to schedule lane %d on the dispatcher before send completion", last.LanePollRequest.LaneID)
	}

	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runScheduler did not complete after releasing the blocked follower startup send")
	}

	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected one follower startup send, got %d", got)
	}
	if session.last.Kind != MessageKindLanePollRequest || session.last.LanePollRequest == nil || session.last.LanePollRequest.Op != LanePollOpOpen {
		t.Fatalf("last envelope after startup = %+v, want lane poll open", session.last)
	}
}

func TestSessionLongPollDispatcherDoesNotSerializeBlockedLanes(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Second
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = time.Hour
	})

	firstKey := testChannelKeyForLane(t, 0, 4, "lp-dispatch-blocked-first")
	secondKey := testChannelKeyForLane(t, 1, 4, "lp-dispatch-blocked-second")
	mustEnsureLocal(t, env.runtime, core.Meta{
		Key:      firstKey,
		Epoch:    41,
		Leader:   2,
		Replicas: []core.NodeID{1, 2, 3},
		ISR:      []core.NodeID{1, 2, 3},
		MinISR:   2,
	})
	mustEnsureLocal(t, env.runtime, core.Meta{
		Key:      secondKey,
		Epoch:    42,
		Leader:   2,
		Replicas: []core.NodeID{1, 2, 3},
		ISR:      []core.NodeID{1, 2, 3},
		MinISR:   2,
	})

	session := env.sessions.session(2)
	paused := make(chan struct{}, 1)
	releaseCh := make(chan struct{})
	var blockFirst sync.Once
	session.afterSend = func() {
		session.mu.Lock()
		last := session.last
		session.mu.Unlock()
		if last.Kind != MessageKindLanePollRequest || last.LanePollRequest == nil {
			return
		}
		blockFirst.Do(func() {
			select {
			case paused <- struct{}{}:
			default:
			}
			<-releaseCh
		})
	}

	done := make(chan struct{})
	go func() {
		env.runtime.runScheduler()
		close(done)
	}()

	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("expected first lane send to block")
	}

	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := session.sendCount(); got >= 2 {
			close(releaseCh)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("runScheduler did not finish after releasing blocked lane send")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}

	close(releaseCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runScheduler did not finish after releasing blocked lane send")
	}
	t.Fatalf("expected another lane send while the first lane send was blocked, got %d sends", session.sendCount())
}

func TestSessionLongPollMembershipChangeSchedulesAffectedLane(t *testing.T) {
	t.Run("remove", func(t *testing.T) {
		env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
			cfg.LongPollLaneCount = 4
			cfg.LongPollMaxWait = time.Millisecond
			cfg.LongPollMaxBytes = 64 * 1024
			cfg.LongPollMaxChannels = 64
			cfg.FollowerReplicationRetryInterval = time.Hour
		})

		const peer core.NodeID = 2
		keep := testChannelKeyForLane(t, 0, 4, "lp-membership-keep")
		remove := testChannelKeyForLane(t, 0, 4, "lp-membership-remove")

		manager := env.runtime.ensureLaneManager(peer)
		manager.UpsertChannel(keep, 11)
		manager.UpsertChannel(remove, 12)
		laneID := manager.LaneFor(keep)
		env.runtime.laneDispatcher.reset()

		prev := core.Meta{
			Key:      remove,
			Epoch:    12,
			Leader:   peer,
			Replicas: []core.NodeID{1, 2, 3},
			ISR:      []core.NodeID{1, 2, 3},
			MinISR:   2,
		}
		env.runtime.syncFollowerLaneMembership(&prev, core.Meta{})

		if !laneDispatchHasWork(env.runtime, peer, laneID) {
			t.Fatalf("expected removal to schedule lane %d for peer %d", laneID, peer)
		}
	})

	t.Run("upsert", func(t *testing.T) {
		env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
			cfg.LongPollLaneCount = 4
			cfg.LongPollMaxWait = time.Millisecond
			cfg.LongPollMaxBytes = 64 * 1024
			cfg.LongPollMaxChannels = 64
			cfg.FollowerReplicationRetryInterval = time.Hour
		})

		const peer core.NodeID = 2
		key := testChannelKeyForLane(t, 0, 4, "lp-membership-upsert")
		manager := env.runtime.ensureLaneManager(peer)
		manager.UpsertChannel(key, 12)
		env.runtime.laneDispatcher.reset()

		prev := core.Meta{
			Key:      key,
			Epoch:    12,
			Leader:   peer,
			Replicas: []core.NodeID{1, 2, 3},
			ISR:      []core.NodeID{1, 2, 3},
			MinISR:   2,
		}
		next := core.Meta{
			Key:      key,
			Epoch:    13,
			Leader:   peer,
			Replicas: []core.NodeID{1, 2, 3},
			ISR:      []core.NodeID{1, 2, 3},
			MinISR:   2,
		}

		env.runtime.syncFollowerLaneMembership(&prev, next)

		laneID := manager.LaneFor(key)
		if !laneDispatchHasWork(env.runtime, peer, laneID) {
			t.Fatalf("expected upsert to schedule lane %d for peer %d", laneID, peer)
		}
	})
}

func TestSessionLongPollLeaseOnlyMetaUpdateKeepsExistingLaneManager(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = time.Hour
	})

	meta := testMetaLocal(26021, 4, 2, []core.NodeID{1, 2, 3})
	meta.LeaseUntil = time.Unix(1_700_000_000, 0).UTC()
	mustEnsureLocal(t, env.runtime, meta)

	manager, ok := env.runtime.laneManager(2)
	require.True(t, ok)
	laneID := manager.LaneFor(meta.Key)

	req, ok := manager.NextRequest(laneID)
	require.True(t, ok)
	require.Equal(t, LanePollOpOpen, req.Op)
	require.True(t, manager.ApplyResponse(LanePollResponseEnvelope{
		LaneID:       laneID,
		Status:       LanePollStatusOK,
		SessionID:    901,
		SessionEpoch: 7,
	}))

	renewed := meta
	renewed.LeaseUntil = meta.LeaseUntil.Add(30 * time.Second)
	require.NoError(t, env.runtime.ApplyMeta(renewed))

	nextManager, ok := env.runtime.laneManager(2)
	require.True(t, ok)
	require.Same(t, manager, nextManager)

	req, ok = nextManager.NextRequest(laneID)
	require.True(t, ok)
	require.Equal(t, LanePollOpPoll, req.Op)
	require.Equal(t, uint64(901), req.SessionID)
	require.Equal(t, uint64(7), req.SessionEpoch)
}

func TestSessionLongPollNeedResetForcesFullOpen(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = 2 * time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = time.Hour
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(2602, 4, 2, []core.NodeID{1, 2}))

	env.runtime.runScheduler()

	session := env.sessions.session(2)
	waitForSessionSendCount(t, session, 1)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected initial long poll open, got %d sends", got)
	}
	first := session.last.LanePollRequest
	if first == nil {
		t.Fatal("expected lane poll request payload")
	}

	_, release := blockPeerSessionSend(t, session, func(env Envelope) bool {
		return env.Kind == MessageKindLanePollRequest &&
			env.LanePollRequest != nil &&
			env.LanePollRequest.LaneID == first.LaneID &&
			env.LanePollRequest.Op == LanePollOpOpen
	})
	defer release()

	env.transport.deliver(Envelope{
		Peer: 2,
		Kind: MessageKindLanePollResponse,
		Sync: true,
		LanePollResponse: &LanePollResponseEnvelope{
			LaneID:        first.LaneID,
			Status:        LanePollStatusNeedReset,
			ResetRequired: true,
			ResetReason:   LanePollResetReasonLaneLayoutMismatch,
			SessionID:     900,
			SessionEpoch:  7,
		},
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if laneDispatchHasWork(env.runtime, 2, first.LaneID) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !laneDispatchHasWork(env.runtime, 2, first.LaneID) {
		t.Fatalf("expected need_reset response to schedule lane %d on the dispatcher", first.LaneID)
	}

	release()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if session.sendCount() >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := session.sendCount(); got != 2 {
		t.Fatalf("expected need_reset response to promptly send a fresh open, got %d sends", got)
	}
	if session.last.Kind != MessageKindLanePollRequest {
		t.Fatalf("last kind after reset = %v, want lane poll request", session.last.Kind)
	}
	if session.last.LanePollRequest == nil || session.last.LanePollRequest.Op != LanePollOpOpen {
		t.Fatalf("reopen request = %+v, want open", session.last.LanePollRequest)
	}
	if len(session.last.LanePollRequest.FullMembership) == 0 {
		t.Fatal("reset reopen should carry full membership snapshot")
	}
}

func TestSessionLongPollBackpressuredReissueSchedulesLaneRetry(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = 2 * time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = 15 * time.Millisecond
	})
	key := testChannelKey(2604)
	mustEnsureLocal(t, env.runtime, testMetaLocal(2604, 4, 2, []core.NodeID{1, 2}))

	env.runtime.runScheduler()

	session := env.sessions.session(2)
	waitForSessionSendCount(t, session, 1)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected initial long poll open, got %d sends", got)
	}
	first := session.last.LanePollRequest
	if first == nil {
		t.Fatal("expected lane poll request payload")
	}

	session.setBackpressure(BackpressureState{Level: BackpressureHard})
	env.transport.deliver(Envelope{
		Peer: 2,
		Kind: MessageKindLanePollResponse,
		Sync: true,
		LanePollResponse: &LanePollResponseEnvelope{
			LaneID:       first.LaneID,
			Status:       LanePollStatusOK,
			SessionID:    777,
			SessionEpoch: 1,
			TimedOut:     true,
		},
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if laneRetryScheduled(env.runtime, 2, first.LaneID) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected backpressured response reissue to stay queued, got %d sends", got)
	}
	ch, ok := env.runtime.lookupChannel(key)
	if !ok {
		t.Fatal("expected channel to still exist")
	}
	if laneRetryScheduled(env.runtime, 2, first.LaneID) {
		if got := queuedReplicationPeers(ch); got != 0 {
			t.Fatalf("expected lane retry to avoid channel retry queueing, got %d queued peers", got)
		}
		return
	}
	t.Fatal("expected backpressured response reissue to schedule a lane retry")
}

func TestSessionLongPollLaneRetryIsScopedPerLane(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 2
		cfg.LongPollMaxWait = 2 * time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = 20 * time.Millisecond
	})

	firstKey := testChannelKeyForLane(t, 0, 2, "lp-lane-retry-first")
	secondKey := testChannelKeyForLane(t, 1, 2, "lp-lane-retry-second")
	mustEnsureLocal(t, env.runtime, core.Meta{
		Key:      firstKey,
		Epoch:    31,
		Leader:   2,
		Replicas: []core.NodeID{1, 2, 3},
		ISR:      []core.NodeID{1, 2, 3},
		MinISR:   2,
	})
	mustEnsureLocal(t, env.runtime, core.Meta{
		Key:      secondKey,
		Epoch:    32,
		Leader:   2,
		Replicas: []core.NodeID{1, 2, 3},
		ISR:      []core.NodeID{1, 2, 3},
		MinISR:   2,
	})

	session := env.sessions.session(2)
	session.enqueueSendErrors(context.DeadlineExceeded)

	env.runtime.runScheduler()
	waitForSessionSendCount(t, session, 2)

	session.mu.Lock()
	sent := append([]Envelope(nil), session.sent...)
	session.mu.Unlock()
	if len(sent) < 2 || sent[0].LanePollRequest == nil || sent[1].LanePollRequest == nil {
		t.Fatalf("expected two lane poll sends, got %+v", sent)
	}
	failedLane := sent[0].LanePollRequest.LaneID
	otherLane := sent[1].LanePollRequest.LaneID
	if failedLane == otherLane {
		t.Fatalf("expected different lanes, got %d and %d", failedLane, otherLane)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if laneRetryScheduled(env.runtime, 2, failedLane) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !laneRetryScheduled(env.runtime, 2, failedLane) {
		t.Fatalf("expected failed lane %d to have a retry scheduled", failedLane)
	}
	if laneRetryScheduled(env.runtime, 2, otherLane) {
		t.Fatalf("expected successful lane %d to avoid retry scheduling", otherLane)
	}
}

func TestSessionLongPollRPCTimeoutUsesRecoveryBackoff(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = 2 * time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
		cfg.FollowerReplicationRetryInterval = 20 * time.Millisecond
		cfg.AutoRunScheduler = true
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(2603, 4, 2, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	session.enqueueSendErrors(context.DeadlineExceeded)

	env.runtime.runScheduler()

	waitForSessionSendCount(t, session, 1)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected only the initial long poll attempt before backoff, got %d sends", got)
	}

	time.Sleep(10 * time.Millisecond)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("long poll timeout should not immediately retry, got %d sends", got)
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := session.sendCount(); got >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("expected retry after recovery backoff, got %d sends", session.sendCount())
}

func TestRuntimeStartsLeaderReconcileProbeWhenCommitNotReady(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
	})
	env.factory.initialCommitReady = false
	mustEnsureLocal(t, env.runtime, testMetaLocal(256, 4, 1, []core.NodeID{1, 2}))

	replica := env.factory.replicas[0]
	state := replica.Status()
	if state.Role != core.ReplicaRoleLeader {
		t.Fatalf("state.Role = %v, want leader", state.Role)
	}
	if state.CommitReady {
		t.Fatal("expected provisional leader state to remain not commit-ready before reconcile")
	}

	env.runtime.runScheduler()

	requireProbe := func() bool {
		return env.sessions.createdFor(2) > 0
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if requireProbe() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected runtime to start a leader reconcile probe while commit-ready is false")
}

func TestRuntimeLeaderSchedulesWakeUpProbeForColdFollower(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.LongPollLaneCount = 4
		cfg.LongPollMaxWait = time.Millisecond
		cfg.LongPollMaxBytes = 64 * 1024
		cfg.LongPollMaxChannels = 64
	})
	meta := testMetaLocal(257, 4, 1, []core.NodeID{1, 2})
	mustEnsureLocal(t, env.runtime, meta)

	handle, ok := env.runtime.Channel(meta.Key)
	if !ok {
		t.Fatalf("Channel(%q) not found", meta.Key)
	}

	_, err := handle.Append(context.Background(), []core.Record{
		{Payload: []byte("wake"), SizeBytes: len("wake")},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	env.runtime.runScheduler()

	session := env.sessions.session(2)
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected one wake-up probe send, got %d", got)
	}
	if session.last.Kind != MessageKindReconcileProbeRequest {
		t.Fatalf("last kind = %v, want reconcile probe request", session.last.Kind)
	}
	if session.last.ReconcileProbeRequest == nil {
		t.Fatal("expected reconcile probe payload")
	}

	_, err = handle.Append(context.Background(), []core.Record{
		{Payload: []byte("wake-again"), SizeBytes: len("wake-again")},
	})
	if err != nil {
		t.Fatalf("second Append() error = %v", err)
	}
	env.runtime.runScheduler()

	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected wake-up probe dedupe to keep one outstanding send, got %d", got)
	}
	if got := env.runtime.queuedPeerRequests(2); got != 0 {
		t.Fatalf("expected wake-up probe dedupe to avoid queued duplicates, got %d", got)
	}
}

func TestSessionFetchFailureEnvelopeReleasesInflightAndRetriesReplication(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
		cfg.FollowerReplicationRetryInterval = time.Hour
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(255, 4, 2, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	env.runtime.runScheduler()
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected initial follower fetch request, got %d sends", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: testChannelKey(255),
		Generation: 1,
		Epoch:      4,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchFailure,
	})

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := session.sendCount(); got >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("expected fetch failure to trigger immediate retry, got %d sends", session.sendCount())
}

func TestSessionEmptyFetchResponseImmediatelyReopensFollowerFetch(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
		cfg.FollowerReplicationRetryInterval = 20 * time.Millisecond
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(252, 4, 2, []core.NodeID{1, 2}))

	session := env.sessions.session(2)
	env.runtime.runScheduler()
	if got := session.sendCount(); got != 1 {
		t.Fatalf("expected initial follower fetch request, got %d sends", got)
	}

	env.transport.deliver(Envelope{
		Peer:       2,
		ChannelKey: testChannelKey(252),
		Generation: 1,
		Epoch:      4,
		RequestID:  session.sent[0].RequestID,
		Kind:       MessageKindFetchResponse,
		FetchResponse: &FetchResponseEnvelope{
			LeaderHW: 1,
		},
	})

	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := session.sendCount(); got >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("expected empty fetch response to reopen follower fetch immediately, got %d sends", session.sendCount())
}

func TestSessionServeFetchReturnsReplicaFetchResult(t *testing.T) {
	env := newSessionTestEnv(t)
	mustEnsureLocal(t, env.runtime, testMetaLocal(26, 5, 1, []core.NodeID{1, 2}))

	truncateTo := uint64(9)
	env.factory.replicas[0].fetchResult = core.ReplicaFetchResult{
		Epoch:      5,
		HW:         11,
		TruncateTo: &truncateTo,
		Records: []core.Record{
			{Payload: []byte("a"), SizeBytes: 1},
			{Payload: []byte("bc"), SizeBytes: 2},
		},
	}

	resp, err := env.runtime.ServeFetch(context.Background(), FetchRequestEnvelope{
		ChannelKey:  testChannelKey(26),
		Epoch:       5,
		Generation:  1,
		ReplicaID:   2,
		FetchOffset: 7,
		OffsetEpoch: 4,
		MaxBytes:    1024,
	})
	if err != nil {
		t.Fatalf("ServeFetch() error = %v", err)
	}
	if env.factory.replicas[0].fetchCalls != 1 {
		t.Fatalf("expected replica.Fetch to be called once")
	}
	gotReq := env.factory.replicas[0].lastFetch
	if gotReq.ChannelKey != testChannelKey(26) || gotReq.FetchOffset != 7 || gotReq.OffsetEpoch != 4 || gotReq.MaxBytes != 1024 {
		t.Fatalf("unexpected fetch request: %+v", gotReq)
	}
	if resp.ChannelKey != testChannelKey(26) || resp.Epoch != 5 || resp.Generation != 1 {
		t.Fatalf("unexpected response envelope metadata: %+v", resp)
	}
	if resp.TruncateTo == nil || *resp.TruncateTo != truncateTo {
		t.Fatalf("unexpected TruncateTo: %+v", resp.TruncateTo)
	}
	if resp.LeaderHW != 11 {
		t.Fatalf("expected LeaderHW 11, got %d", resp.LeaderHW)
	}
	if len(resp.Records) != 2 || string(resp.Records[1].Payload) != "bc" {
		t.Fatalf("unexpected response records: %+v", resp.Records)
	}
}

func TestSessionServeFetchActivatesMissingChannelOnce(t *testing.T) {
	activator := &recordingActivator{}
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Activator = activator
	})
	meta := testMetaLocal(2604, 5, 1, []core.NodeID{1, 2})

	activator.fn = func(context.Context, core.ChannelKey, ActivationSource) (core.Meta, error) {
		if err := env.runtime.EnsureChannel(meta); err != nil {
			t.Fatalf("EnsureChannel(%q) error = %v", meta.Key, err)
		}
		replica := env.factory.replicas[len(env.factory.replicas)-1]
		replica.mu.Lock()
		replica.state.LEO = 1
		replica.fetchResult = core.ReplicaFetchResult{
			Epoch: 5,
			HW:    1,
			Records: []core.Record{
				{Payload: []byte("cold"), SizeBytes: len("cold")},
			},
		}
		replica.mu.Unlock()
		return meta, nil
	}

	resp, err := env.runtime.ServeFetch(context.Background(), FetchRequestEnvelope{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		Generation:  1,
		ReplicaID:   2,
		FetchOffset: 0,
		OffsetEpoch: meta.Epoch,
		MaxBytes:    1024,
	})
	if err != nil {
		t.Fatalf("ServeFetch() error = %v", err)
	}
	if activator.callCount() != 1 {
		t.Fatalf("expected activator to be called once, got %d", activator.callCount())
	}
	if activator.calls[0].source != ActivationSourceFetch {
		t.Fatalf("activation source = %q, want %q", activator.calls[0].source, ActivationSourceFetch)
	}
	if activator.calls[0].key != meta.Key {
		t.Fatalf("activation key = %q, want %q", activator.calls[0].key, meta.Key)
	}
	if len(env.factory.replicas) != 1 {
		t.Fatalf("expected one activated replica, got %d", len(env.factory.replicas))
	}
	if env.factory.replicas[0].fetchCalls != 1 {
		t.Fatalf("expected activated replica.Fetch to be called once, got %d", env.factory.replicas[0].fetchCalls)
	}
	if len(resp.Records) != 1 || string(resp.Records[0].Payload) != "cold" {
		t.Fatalf("ServeFetch returned %+v, want activated record", resp)
	}
}

func TestSessionServeReconcileProbeActivatesMissingChannelOnce(t *testing.T) {
	activator := &recordingActivator{}
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Activator = activator
	})
	meta := testMetaLocal(2605, 6, 1, []core.NodeID{1, 2})

	activator.fn = func(context.Context, core.ChannelKey, ActivationSource) (core.Meta, error) {
		if err := env.runtime.EnsureChannel(meta); err != nil {
			t.Fatalf("EnsureChannel(%q) error = %v", meta.Key, err)
		}
		replica := env.factory.replicas[len(env.factory.replicas)-1]
		replica.mu.Lock()
		replica.state.LEO = 7
		replica.state.OffsetEpoch = 4
		replica.state.CheckpointHW = 5
		replica.mu.Unlock()
		return meta, nil
	}

	resp, err := env.runtime.ServeReconcileProbe(context.Background(), ReconcileProbeRequestEnvelope{
		ChannelKey: meta.Key,
		Epoch:      meta.Epoch,
		Generation: 1,
		ReplicaID:  2,
	})
	if err != nil {
		t.Fatalf("ServeReconcileProbe() error = %v", err)
	}
	if activator.callCount() != 1 {
		t.Fatalf("expected activator to be called once, got %d", activator.callCount())
	}
	if activator.calls[0].source != ActivationSourceProbe {
		t.Fatalf("activation source = %q, want %q", activator.calls[0].source, ActivationSourceProbe)
	}
	if activator.calls[0].key != meta.Key {
		t.Fatalf("activation key = %q, want %q", activator.calls[0].key, meta.Key)
	}
	if resp.ChannelKey != meta.Key || resp.Epoch != meta.Epoch || resp.Generation != 1 {
		t.Fatalf("unexpected probe response metadata: %+v", resp)
	}
	if resp.OffsetEpoch != 4 || resp.LogEndOffset != 7 || resp.CheckpointHW != 5 {
		t.Fatalf("unexpected probe response state: %+v", resp)
	}
}

func TestSessionServeReconcileProbeAllowsExternalProbeWithoutGeneration(t *testing.T) {
	activator := &recordingActivator{}
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.Activator = activator
	})
	meta := testMetaLocal(2606, 6, 1, []core.NodeID{1, 2})

	activator.fn = func(context.Context, core.ChannelKey, ActivationSource) (core.Meta, error) {
		if err := env.runtime.EnsureChannel(meta); err != nil {
			t.Fatalf("EnsureChannel(%q) error = %v", meta.Key, err)
		}
		replica := env.factory.replicas[len(env.factory.replicas)-1]
		replica.mu.Lock()
		replica.state.LEO = 9
		replica.state.OffsetEpoch = 6
		replica.state.CheckpointHW = 8
		replica.mu.Unlock()
		return meta, nil
	}

	resp, err := env.runtime.ServeReconcileProbe(context.Background(), ReconcileProbeRequestEnvelope{
		ChannelKey: meta.Key,
		Epoch:      meta.Epoch,
		Generation: 0,
		ReplicaID:  2,
	})
	require.NoError(t, err)
	require.Equal(t, meta.Key, resp.ChannelKey)
	require.Equal(t, meta.Epoch, resp.Epoch)
	require.Equal(t, uint64(9), resp.LogEndOffset)
	require.Equal(t, uint64(8), resp.CheckpointHW)
}

func TestSessionServeFetchWaitsForReplicaStateChangeBeforeReturningRecords(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.FollowerReplicationRetryInterval = 50 * time.Millisecond
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(2601, 5, 1, []core.NodeID{1, 2}))

	rep := env.factory.replicas[0]
	respCh := make(chan FetchResponseEnvelope, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := env.runtime.ServeFetch(context.Background(), FetchRequestEnvelope{
			ChannelKey:  testChannelKey(2601),
			Epoch:       5,
			Generation:  1,
			ReplicaID:   2,
			FetchOffset: 0,
			OffsetEpoch: 5,
			MaxBytes:    1024,
		})
		respCh <- resp
		errCh <- err
	}()

	select {
	case <-respCh:
		t.Fatal("ServeFetch returned before leader replica changed")
	case <-time.After(10 * time.Millisecond):
	}

	rep.mu.Lock()
	rep.fetchResult = core.ReplicaFetchResult{
		Epoch:   5,
		HW:      1,
		Records: []core.Record{{Payload: []byte("a"), SizeBytes: 1}},
	}
	rep.state.LEO = 1
	rep.mu.Unlock()
	rep.notifyStateChange()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeFetch() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ServeFetch did not return after leader replica changed")
	}

	resp := <-respCh
	if len(resp.Records) != 1 || string(resp.Records[0].Payload) != "a" {
		t.Fatalf("ServeFetch returned %+v, want appended record", resp)
	}
	if rep.fetchCalls < 2 {
		t.Fatalf("expected ServeFetch to refetch after replica change, got %d fetch calls", rep.fetchCalls)
	}
}

func TestSessionServeFetchReturnsEmptyAfterLongPollTimeout(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.FollowerReplicationRetryInterval = 20 * time.Millisecond
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(2602, 5, 1, []core.NodeID{1, 2}))

	startedAt := time.Now()
	resp, err := env.runtime.ServeFetch(context.Background(), FetchRequestEnvelope{
		ChannelKey:  testChannelKey(2602),
		Epoch:       5,
		Generation:  1,
		ReplicaID:   2,
		FetchOffset: 0,
		OffsetEpoch: 5,
		MaxBytes:    1024,
	})
	if err != nil {
		t.Fatalf("ServeFetch() error = %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed < 20*time.Millisecond {
		t.Fatalf("ServeFetch returned too early after %s, want at least 20ms", elapsed)
	}
	if len(resp.Records) != 0 || resp.TruncateTo != nil {
		t.Fatalf("ServeFetch returned %+v, want empty long-poll timeout response", resp)
	}
}

func TestSessionServeFetchSkipsLongPollWhenDisabledByContext(t *testing.T) {
	env := newSessionTestEnvWithConfig(t, func(cfg *Config) {
		cfg.FollowerReplicationRetryInterval = 50 * time.Millisecond
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(2603, 5, 1, []core.NodeID{1, 2}))

	startedAt := time.Now()
	resp, err := env.runtime.ServeFetch(WithoutFetchLongPoll(context.Background()), FetchRequestEnvelope{
		ChannelKey:  testChannelKey(2603),
		Epoch:       5,
		Generation:  1,
		ReplicaID:   2,
		FetchOffset: 0,
		OffsetEpoch: 5,
		MaxBytes:    1024,
	})
	if err != nil {
		t.Fatalf("ServeFetch() error = %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed >= 20*time.Millisecond {
		t.Fatalf("ServeFetch took %s with long-poll disabled, want immediate response", elapsed)
	}
	if len(resp.Records) != 0 || resp.TruncateTo != nil {
		t.Fatalf("ServeFetch returned %+v, want immediate empty response when long-poll is disabled", resp)
	}
}

type sessionTestEnv struct {
	runtime     *runtime
	generations *sessionGenerationStore
	factory     *sessionReplicaFactory
	transport   *sessionTransport
	sessions    *sessionPeerSessionManager
}

type activationCall struct {
	key    core.ChannelKey
	source ActivationSource
}

type recordingActivator struct {
	mu    sync.Mutex
	calls []activationCall
	fn    func(context.Context, core.ChannelKey, ActivationSource) (core.Meta, error)
}

func (a *recordingActivator) ActivateByKey(ctx context.Context, key core.ChannelKey, source ActivationSource) (core.Meta, error) {
	a.mu.Lock()
	a.calls = append(a.calls, activationCall{key: key, source: source})
	fn := a.fn
	a.mu.Unlock()
	if fn == nil {
		return core.Meta{}, nil
	}
	return fn(ctx, key, source)
}

func (a *recordingActivator) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func newSessionTestEnv(t *testing.T) *sessionTestEnv {
	t.Helper()

	generations := newSessionGenerationStore()
	factory := newSessionReplicaFactory()
	transport := &sessionTransport{}
	sessions := newSessionPeerSessionManager()

	rt, err := New(Config{
		LocalNode:       1,
		ReplicaFactory:  factory,
		GenerationStore: generations,
		Transport:       transport,
		PeerSessions:    sessions,
		Tombstones: TombstonePolicy{
			TombstoneTTL: 30 * time.Second,
		},
		Now: func() time.Time { return time.Unix(1700000000, 0) },
	})
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

func testMetaLocal(groupID, epoch uint64, leader core.NodeID, replicas []core.NodeID) core.Meta {
	return core.Meta{
		Key:      testChannelKey(groupID),
		Epoch:    epoch,
		Leader:   leader,
		Replicas: append([]core.NodeID(nil), replicas...),
		ISR:      append([]core.NodeID(nil), replicas...),
		MinISR:   1,
	}
}

func queuedReplicationPeers(g *channel) int {
	count := 0
	for {
		if _, ok := g.popReplicationPeer(); !ok {
			return count
		}
		count++
	}
}

func mustEnsureLocal(t *testing.T, rt *runtime, meta core.Meta) {
	t.Helper()
	if err := rt.EnsureChannel(meta); err != nil {
		t.Fatalf("EnsureChannel(%q) error = %v", meta.Key, err)
	}
}

type sessionGenerationStore struct {
	mu     sync.Mutex
	values map[core.ChannelKey]uint64
}

func newSessionGenerationStore() *sessionGenerationStore {
	return &sessionGenerationStore{values: make(map[core.ChannelKey]uint64)}
}

func (s *sessionGenerationStore) Load(channelKey core.ChannelKey) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[channelKey], nil
}

func (s *sessionGenerationStore) Store(channelKey core.ChannelKey, generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[channelKey] = generation
	return nil
}

type sessionReplicaFactory struct {
	mu                 sync.Mutex
	replicas           []*sessionReplica
	initialCommitReady bool
}

func newSessionReplicaFactory() *sessionReplicaFactory {
	return &sessionReplicaFactory{initialCommitReady: true}
}

func (f *sessionReplicaFactory) New(cfg ChannelConfig) (replica.Replica, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	replica := &sessionReplica{
		state: core.ReplicaState{
			ChannelKey:  cfg.ChannelKey,
			Role:        core.ReplicaRoleLeader,
			Epoch:       cfg.Meta.Epoch,
			OffsetEpoch: cfg.Meta.Epoch,
			Leader:      cfg.Meta.Leader,
			CommitReady: f.initialCommitReady,
		},
		onStateChange: cfg.OnReplicaStateChange,
	}
	f.replicas = append(f.replicas, replica)
	return replica, nil
}

type sessionReplica struct {
	mu                    sync.Mutex
	state                 core.ReplicaState
	onStateChange         func()
	onLeaderHWAdvance     func()
	applyFetchCalls       int
	lastApplyFetch        core.ReplicaApplyFetchRequest
	applyFetchErr         error
	applyProgressAckCalls int
	lastProgressAck       core.ReplicaProgressAckRequest
	applyProgressAckErr   error
	fetchCalls            int
	lastFetch             core.ReplicaFetchRequest
	fetchResult           core.ReplicaFetchResult
	fetchErr              error
	applyMetaErr          error
	becomeLeaderErr       error
	becomeFollowErr       error
}

func (r *sessionReplica) ApplyMeta(meta core.Meta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.applyMetaErr != nil {
		return r.applyMetaErr
	}
	r.state.ChannelKey = meta.Key
	r.state.Epoch = meta.Epoch
	r.state.Leader = meta.Leader
	return nil
}

func (r *sessionReplica) BecomeLeader(meta core.Meta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.becomeLeaderErr != nil {
		return r.becomeLeaderErr
	}
	r.state.ChannelKey = meta.Key
	r.state.Epoch = meta.Epoch
	r.state.Leader = meta.Leader
	r.state.Role = core.ReplicaRoleLeader
	return nil
}

func (r *sessionReplica) BecomeFollower(meta core.Meta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.becomeFollowErr != nil {
		return r.becomeFollowErr
	}
	r.state.ChannelKey = meta.Key
	r.state.Epoch = meta.Epoch
	r.state.Leader = meta.Leader
	r.state.Role = core.ReplicaRoleFollower
	return nil
}
func (r *sessionReplica) Tombstone() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Role = core.ReplicaRoleTombstoned
	return nil
}
func (r *sessionReplica) Close() error {
	return nil
}
func (r *sessionReplica) InstallSnapshot(ctx context.Context, snap core.Snapshot) error {
	return nil
}
func (r *sessionReplica) Append(ctx context.Context, batch []core.Record) (core.CommitResult, error) {
	return core.CommitResult{}, nil
}
func (r *sessionReplica) Fetch(ctx context.Context, req core.ReplicaFetchRequest) (core.ReplicaFetchResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fetchCalls++
	r.lastFetch = req
	return r.fetchResult, r.fetchErr
}

func (r *sessionReplica) notifyStateChange() {
	if r.onStateChange != nil {
		r.onStateChange()
	}
}

func (r *sessionReplica) SetLeaderHWAdvanceNotifier(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onLeaderHWAdvance = fn
}

func (r *sessionReplica) ApplyFetch(ctx context.Context, req core.ReplicaApplyFetchRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applyFetchCalls++
	r.lastApplyFetch = req
	return r.applyFetchErr
}
func (r *sessionReplica) ApplyProgressAck(ctx context.Context, req core.ReplicaProgressAckRequest) error {
	var notify func()
	r.mu.Lock()
	r.applyProgressAckCalls++
	r.lastProgressAck = req
	if r.applyProgressAckErr != nil {
		err := r.applyProgressAckErr
		r.mu.Unlock()
		return err
	}
	if req.MatchOffset > r.state.HW {
		r.state.HW = req.MatchOffset
		notify = r.onLeaderHWAdvance
	}
	r.mu.Unlock()
	if notify != nil {
		notify()
	}
	return nil
}
func (r *sessionReplica) ApplyReconcileProof(ctx context.Context, proof core.ReplicaReconcileProof) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.OffsetEpoch = proof.OffsetEpoch
	return nil
}
func (r *sessionReplica) Status() core.ReplicaState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

type sessionTransport struct {
	mu      sync.Mutex
	handler func(Envelope)
}

func (t *sessionTransport) Send(peer core.NodeID, env Envelope) error {
	return nil
}

func (t *sessionTransport) RegisterHandler(fn func(Envelope)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handler = fn
}

func (t *sessionTransport) deliver(env Envelope) {
	t.mu.Lock()
	handler := t.handler
	t.mu.Unlock()
	if handler != nil {
		handler(env)
	}
}

type sessionPeerSessionManager struct {
	mu      sync.Mutex
	created map[core.NodeID]int
	cache   map[core.NodeID]*trackingPeerSession
}

func newSessionPeerSessionManager() *sessionPeerSessionManager {
	return &sessionPeerSessionManager{
		created: make(map[core.NodeID]int),
		cache:   make(map[core.NodeID]*trackingPeerSession),
	}
}

func (m *sessionPeerSessionManager) Session(peer core.NodeID) PeerSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.created[peer]++
	if session, ok := m.cache[peer]; ok {
		return session
	}
	session := &trackingPeerSession{}
	m.cache[peer] = session
	return session
}

func (m *sessionPeerSessionManager) createdFor(peer core.NodeID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.created[peer]
}

func (m *sessionPeerSessionManager) session(peer core.NodeID) *trackingPeerSession {
	m.mu.Lock()
	session, ok := m.cache[peer]
	m.mu.Unlock()
	if ok {
		return session
	}
	return m.Session(peer).(*trackingPeerSession)
}

type trackingPeerSession struct {
	mu           sync.Mutex
	sends        int
	flushes      int
	closes       int
	last         Envelope
	sent         []Envelope
	batched      []Envelope
	sendErr      error
	sendErrs     []error
	flushErr     error
	backpressure BackpressureState
	afterSend    func()
	afterFlush   func()
	tryBatch     bool
}

func blockPeerSessionSend(t *testing.T, session *trackingPeerSession, match func(Envelope) bool) (<-chan struct{}, func()) {
	t.Helper()

	paused := make(chan struct{}, 1)
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	session.afterSend = func() {
		session.mu.Lock()
		last := session.last
		session.mu.Unlock()
		if match != nil && !match(last) {
			return
		}
		select {
		case paused <- struct{}{}:
		default:
		}
		<-releaseCh
	}
	return paused, func() {
		releaseOnce.Do(func() {
			close(releaseCh)
		})
	}
}

func (s *trackingPeerSession) Send(env Envelope) error {
	s.mu.Lock()
	s.sends++
	s.last = env
	s.sent = append(s.sent, env)
	afterSend := s.afterSend
	if len(s.sendErrs) > 0 {
		err := s.sendErrs[0]
		s.sendErrs = append([]error(nil), s.sendErrs[1:]...)
		s.mu.Unlock()
		if afterSend != nil {
			afterSend()
		}
		return err
	}
	err := s.sendErr
	s.mu.Unlock()
	if afterSend != nil {
		afterSend()
	}
	return err
}

func (s *trackingPeerSession) TryBatch(env Envelope) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tryBatch {
		return false
	}
	s.batched = append(s.batched, env)
	return true
}

func (s *trackingPeerSession) Flush() error {
	s.mu.Lock()
	s.flushes++
	afterFlush := s.afterFlush
	err := s.flushErr
	s.mu.Unlock()
	if afterFlush != nil {
		afterFlush()
	}
	return err
}

func (s *trackingPeerSession) Backpressure() BackpressureState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backpressure
}

func (s *trackingPeerSession) Close() error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	return nil
}

func (s *trackingPeerSession) sendCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sends
}

func (s *trackingPeerSession) batchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.batched)
}

func (s *trackingPeerSession) flushCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushes
}

func (s *trackingPeerSession) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}

func (s *trackingPeerSession) setBackpressure(state BackpressureState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backpressure = state
}

func (s *trackingPeerSession) setTryBatch(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tryBatch = v
}

func (s *trackingPeerSession) setSendErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendErr = err
}

func (s *trackingPeerSession) enqueueSendErrors(errs ...error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendErrs = append(s.sendErrs, errs...)
}

func (s *trackingPeerSession) setFlushErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushErr = err
}
