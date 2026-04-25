package runtime

import (
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestSnapshotTasksRespectMaxSnapshotInflight(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.Limits.MaxSnapshotInflight = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(61, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(62, 1, 1, []core.NodeID{1, 2}))

	env.runtime.queueSnapshot(testChannelKey(61))
	env.runtime.queueSnapshot(testChannelKey(62))
	env.runtime.runScheduler()

	if got := env.runtime.maxSnapshotConcurrent(); got != 1 {
		t.Fatalf("expected single inflight snapshot, got %d", got)
	}
}

func TestSnapshotRecoveryBandwidthLimiterThrottlesSnapshotChunks(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.Limits.MaxRecoveryBytesPerSecond = 128
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(63, 1, 1, []core.NodeID{1, 2}))

	startedAt := env.clock.Now()
	env.runtime.queueSnapshotChunk(testChannelKey(63), 256)
	env.runtime.runScheduler()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if env.clock.Now().Sub(startedAt) >= time.Second {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected throttling delay")
}

func TestSnapshotTaskRequeuesWhenInflightLimitReached(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.Limits.MaxSnapshotInflight = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(64, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(65, 1, 1, []core.NodeID{1, 2}))

	if !env.runtime.snapshots.begin(1) {
		t.Fatalf("expected to reserve one inflight snapshot")
	}
	env.runtime.queueSnapshot(testChannelKey(64))
	env.runtime.queueSnapshot(testChannelKey(65))
	env.runtime.runScheduler()
	if got := env.runtime.queuedSnapshotGroups(); got != 2 {
		t.Fatalf("expected two waiting snapshots, got %d", got)
	}

	env.runtime.completeSnapshot("")
	env.runtime.runScheduler()
	if got := env.runtime.queuedSnapshotGroups(); got != 0 {
		t.Fatalf("expected waiting snapshot to be resumed, got %d", got)
	}
}

func TestSnapshotWaitingQueueIsFIFO(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.Limits.MaxSnapshotInflight = 1
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(66, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(67, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(68, 1, 1, []core.NodeID{1, 2}))

	if !env.runtime.snapshots.begin(1) {
		t.Fatalf("expected to reserve one inflight snapshot")
	}
	env.runtime.queueSnapshotChunk(testChannelKey(66), 101)
	env.runtime.queueSnapshotChunk(testChannelKey(67), 102)
	env.runtime.queueSnapshotChunk(testChannelKey(68), 103)
	env.runtime.runScheduler()

	env.runtime.completeSnapshot("")
	env.runtime.runScheduler()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		env.runtime.runScheduler()
		if len(env.throttle.values) == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if len(env.throttle.values) != 3 || env.throttle.values[0] != 101 || env.throttle.values[1] != 102 || env.throttle.values[2] != 103 {
		t.Fatalf("expected FIFO throttle trace [101 102 103], got %v", env.throttle.values)
	}
}

func TestSnapshotThrottlingDoesNotBlockUnrelatedReplication(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.AutoRunScheduler = true
	})
	mustEnsureLocal(t, env.runtime, testMetaLocal(69, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(70, 1, 1, []core.NodeID{1, 2}))

	blocking := newBlockingSnapshotThrottle()
	env.runtime.snapshotThrottle = blocking

	env.runtime.queueSnapshotChunk(testChannelKey(69), 256)
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("expected snapshot throttle to start")
	}

	env.runtime.enqueueReplication(testChannelKey(70), 2)
	time.Sleep(30 * time.Millisecond)
	if got := env.sessions.session(2).sendCount(); got == 0 {
		t.Fatal("expected replication to make progress while snapshot throttle is blocked")
	}

	close(blocking.release)
}

func TestSnapshotRuntimeClosePreventsPostCloseReschedule(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.Limits.MaxSnapshotInflight = 1
	})
	first := testChannelKey(71)
	second := testChannelKey(72)
	mustEnsureLocal(t, env.runtime, testMetaLocal(71, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(72, 1, 1, []core.NodeID{1, 2}))

	blocking := newBlockingSnapshotThrottle()
	env.runtime.snapshotThrottle = blocking

	env.runtime.queueSnapshotChunk(first, 128)
	env.runtime.queueSnapshotChunk(second, 128)
	env.runtime.runScheduler()

	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("expected first snapshot throttle to start")
	}
	if got := env.runtime.queuedSnapshotGroups(); got != 1 {
		t.Fatalf("expected one queued snapshot waiter before close, got %d", got)
	}

	if err := env.runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	close(blocking.release)
	time.Sleep(30 * time.Millisecond)

	if env.runtime.scheduler.hasReady() {
		t.Fatal("expected runtime close to prevent post-close snapshot reschedule")
	}
}

func TestSnapshotRemoveChannelPurgesWaitingSnapshotEntry(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.Limits.MaxSnapshotInflight = 1
	})
	first := testChannelKey(73)
	second := testChannelKey(74)
	mustEnsureLocal(t, env.runtime, testMetaLocal(73, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(74, 1, 1, []core.NodeID{1, 2}))

	if !env.runtime.snapshots.begin(1) {
		t.Fatal("expected to reserve one inflight snapshot")
	}
	env.runtime.queueSnapshot(first)
	env.runtime.queueSnapshot(second)
	env.runtime.runScheduler()
	if got := env.runtime.queuedSnapshotGroups(); got != 2 {
		t.Fatalf("expected two waiting snapshots before removal, got %d", got)
	}

	if err := env.runtime.RemoveChannel(first); err != nil {
		t.Fatalf("RemoveChannel() error = %v", err)
	}
	if got := env.runtime.queuedSnapshotGroups(); got != 1 {
		t.Fatalf("expected removed channel waiter to be purged, got %d waiting", got)
	}
}

func TestSnapshotCompletionSkipsRemovedWaitersAndAdvancesLiveSnapshot(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.Limits.MaxSnapshotInflight = 1
	})
	first := testChannelKey(75)
	second := testChannelKey(76)
	mustEnsureLocal(t, env.runtime, testMetaLocal(75, 1, 1, []core.NodeID{1, 2}))
	mustEnsureLocal(t, env.runtime, testMetaLocal(76, 1, 1, []core.NodeID{1, 2}))

	if !env.runtime.snapshots.begin(1) {
		t.Fatal("expected to reserve one inflight snapshot")
	}
	env.runtime.queueSnapshot(first)
	env.runtime.queueSnapshot(second)
	env.runtime.runScheduler()
	if got := env.runtime.queuedSnapshotGroups(); got != 2 {
		t.Fatalf("expected two waiting snapshots before completion, got %d", got)
	}

	if err := env.runtime.RemoveChannel(first); err != nil {
		t.Fatalf("RemoveChannel() error = %v", err)
	}

	env.runtime.completeSnapshot("")
	env.runtime.runScheduler()
	if got := env.runtime.queuedSnapshotGroups(); got != 0 {
		t.Fatalf("expected completion to advance to live waiter, got %d waiting", got)
	}
}

func TestSnapshotApplyMetaPurgesStaleWaitingSnapshotWork(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.Limits.MaxSnapshotInflight = 1
	})
	key := testChannelKey(77)
	mustEnsureLocal(t, env.runtime, testMetaLocal(77, 1, 1, []core.NodeID{1, 2}))

	if !env.runtime.snapshots.begin(1) {
		t.Fatal("expected to reserve one inflight snapshot")
	}
	env.runtime.queueSnapshotChunk(key, 256)
	env.runtime.runScheduler()
	if got := env.runtime.queuedSnapshotGroups(); got != 1 {
		t.Fatalf("expected queued snapshot waiter before meta churn, got %d", got)
	}

	if err := env.runtime.ApplyMeta(testMetaLocal(77, 2, 1, []core.NodeID{1, 2, 3})); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}
	if got := env.runtime.queuedSnapshotGroups(); got != 0 {
		t.Fatalf("expected ApplyMeta to purge stale snapshot waiter, got %d", got)
	}
}

func TestSnapshotApplyMetaClearsStaleSnapshotBytes(t *testing.T) {
	env := newSnapshotTestEnv(t, func(cfg *Config) {
		cfg.Limits.MaxSnapshotInflight = 1
	})
	key := testChannelKey(78)
	mustEnsureLocal(t, env.runtime, testMetaLocal(78, 1, 1, []core.NodeID{1, 2}))

	if !env.runtime.snapshots.begin(1) {
		t.Fatal("expected to reserve one inflight snapshot")
	}
	env.runtime.queueSnapshotChunk(key, 512)
	env.runtime.runScheduler()

	if err := env.runtime.ApplyMeta(testMetaLocal(78, 2, 1, []core.NodeID{1, 2, 3})); err != nil {
		t.Fatalf("ApplyMeta() error = %v", err)
	}

	ch, ok := env.runtime.lookupChannel(key)
	if !ok {
		t.Fatal("expected channel to exist")
	}
	ch.mu.Lock()
	gotBytes := ch.snapshotBytes
	gotPending := ch.pending&taskSnapshot != 0
	ch.mu.Unlock()
	if gotBytes != 0 {
		t.Fatalf("expected stale snapshot bytes to be cleared, got %d", gotBytes)
	}
	if gotPending {
		t.Fatal("expected stale snapshot task marker to be cleared")
	}
}

type snapshotTestEnv struct {
	runtime  *runtime
	clock    *snapshotManualClock
	throttle *fakeSnapshotThrottle
	sessions *sessionPeerSessionManager
}

func newSnapshotTestEnv(t *testing.T, mutate func(*Config)) *snapshotTestEnv {
	t.Helper()

	clock := newSnapshotManualClock(time.Unix(1700000000, 0))
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
		Now: clock.Now,
	}
	if mutate != nil {
		mutate(&cfg)
	}

	rt, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	impl := rt.(*runtime)
	throttle := &fakeSnapshotThrottle{advance: clock.Advance, rate: cfg.Limits.MaxRecoveryBytesPerSecond}
	impl.snapshotThrottle = throttle

	return &snapshotTestEnv{
		runtime:  impl,
		clock:    clock,
		throttle: throttle,
		sessions: sessions,
	}
}

type snapshotManualClock struct {
	now time.Time
}

func newSnapshotManualClock(now time.Time) *snapshotManualClock {
	return &snapshotManualClock{now: now}
}

func (c *snapshotManualClock) Now() time.Time {
	return c.now
}

func (c *snapshotManualClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

type fakeSnapshotThrottle struct {
	advance func(time.Duration)
	values  []int64
	rate    int64
}

func (t *fakeSnapshotThrottle) Wait(bytes int64) {
	t.values = append(t.values, bytes)
	if t.advance != nil && bytes > 0 {
		rate := t.rate
		if rate <= 0 {
			return
		}
		t.advance(time.Duration(bytes) * time.Second / time.Duration(rate))
	}
}

type blockingSnapshotThrottle struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingSnapshotThrottle() *blockingSnapshotThrottle {
	return &blockingSnapshotThrottle{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (t *blockingSnapshotThrottle) Wait(bytes int64) {
	if bytes <= 0 {
		return
	}
	select {
	case t.started <- struct{}{}:
	default:
	}
	<-t.release
}
