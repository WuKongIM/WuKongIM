package chatlifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

func TestObserverClusterHealthFailsAfterContinuousThirtySeconds(t *testing.T) {
	cfg := FormalConfig()
	fixture := newObserverFixture(cfg)
	resultChannel := make(chan ObserverResult, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { resultChannel <- fixture.observer.Run(ctx, cfg) }()
	fixture.waitPoll(t)
	if fixture.clock.period != 5*time.Second {
		t.Fatalf("ticker period = %v, want 5s", fixture.clock.period)
	}

	fixture.targets[0].mutate(func(snapshot *target.DebugCluster) {
		progress := &snapshot.Slots[0].ReplicaProgress[0]
		progress.MatchIndex--
		progress.LagEntries++
	})
	for elapsed := 5 * time.Second; elapsed <= 35*time.Second; elapsed += 5 * time.Second {
		fixture.clock.advance(5 * time.Second)
		fixture.waitPoll(t)
	}
	result := <-resultChannel
	if result.Outcome != ObserverProductFailure || result.Code != ObserverCodeClusterHealth {
		t.Fatalf("result = %+v, want product_failure/cluster_health", result)
	}
}

func TestObserverRoundStartsAllNodesWithOneCadenceContext(t *testing.T) {
	cfg := LocalConfig()
	started := make(chan int, len(cfg.Observation.ServiceNodes))
	completed := make(chan int, len(cfg.Observation.ServiceNodes))
	release := make(chan struct{})
	targets := make([]*barrierObserverTarget, len(cfg.Observation.ServiceNodes))
	for index := range targets {
		targets[index] = &barrierObserverTarget{
			index: index, started: started, completed: completed, release: release,
			cluster: healthyPreflightCluster(uint64(index + 1)),
		}
	}
	type roundObservation struct {
		ctx     context.Context
		timeout time.Duration
	}
	roundStarted := make(chan roundObservation, 1)
	clock := newFakeObserverClock(time.Unix(1_000, 0))
	observer := NewObserver(ObserverOptions{
		BenchToken: "bench-token",
		Clock:      clock,
		RoundContext: func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
			roundCtx, cancel := context.WithCancel(parent)
			roundStarted <- roundObservation{ctx: roundCtx, timeout: timeout}
			return roundCtx, cancel
		},
		TargetFactory: func(index int, _ EndpointDeclaration, _ string) clusterHealthTarget {
			return targets[index]
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	resultChannel := make(chan ObserverResult, 1)
	go func() { resultChannel <- observer.Run(ctx, cfg) }()
	round := <-roundStarted
	if round.timeout != cfg.Observation.Cadence {
		t.Fatalf("round timeout = %v, want cadence %v", round.timeout, cfg.Observation.Cadence)
	}
	for index := 0; index < len(targets); index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("three node polls did not all reach the health barrier concurrently")
		}
	}
	close(release)
	for index := 0; index < len(targets); index++ {
		select {
		case <-completed:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("concurrent observer round did not join every node")
		}
	}
	for index, observed := range targets {
		if stages, contexts := observed.snapshot(); len(stages) != 3 || stages[0] != "health" || stages[1] != "ready" || stages[2] != "cluster" {
			t.Fatalf("target %d stages = %#v, want health/ready/cluster", index, stages)
		} else if contexts[0] != round.ctx || contexts[1] != round.ctx || contexts[2] != round.ctx {
			t.Fatalf("target %d did not share the round context", index)
		}
	}
	cancel()
	if result := <-resultChannel; result.Outcome != ObserverStopped {
		t.Fatalf("result = %+v, want stopped", result)
	}
}

func TestObserverRoundTimeoutIsCappedWithoutChangingCadence(t *testing.T) {
	tests := []struct {
		name    string
		cadence time.Duration
	}{
		{name: "default cadence", cadence: 5 * time.Second},
		{name: "long cadence", cadence: 10 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := LocalConfig()
			cfg.Observation.Cadence = test.cadence
			fixture := newObserverFixture(cfg)
			roundTimeout := make(chan time.Duration, 1)
			fixture.observer.options.RoundContext = func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
				roundTimeout <- timeout
				return context.WithCancel(parent)
			}
			ctx, cancel := context.WithCancel(context.Background())
			resultChannel := make(chan ObserverResult, 1)
			go func() { resultChannel <- fixture.observer.Run(ctx, cfg) }()
			if timeout := <-roundTimeout; timeout != 5*time.Second {
				cancel()
				t.Fatalf("round timeout = %v, want 5s", timeout)
			}
			if fixture.clock.period != test.cadence {
				cancel()
				t.Fatalf("ticker cadence = %v, want %v", fixture.clock.period, test.cadence)
			}
			cancel()
			if result := <-resultChannel; result.Outcome != ObserverStopped {
				t.Fatalf("result = %+v, want stopped", result)
			}
		})
	}
}

func TestObserverUpdatesFailureWindowsAtRoundCompletion(t *testing.T) {
	cfg := LocalConfig()
	started := make(chan int, len(cfg.Observation.ServiceNodes))
	release := make(chan struct{})
	targets := make([]*barrierObserverTarget, len(cfg.Observation.ServiceNodes))
	for index := range targets {
		targets[index] = &barrierObserverTarget{
			index: index, started: started, release: release,
			cluster: healthyPreflightCluster(uint64(index + 1)),
		}
	}
	startedAt := time.Unix(2_000, 0)
	clock := newCompletionObserverClock(startedAt)
	observer := NewObserver(ObserverOptions{
		BenchToken: "bench-token",
		Clock:      clock,
		RoundContext: func(parent context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		},
		TargetFactory: func(index int, _ EndpointDeclaration, _ string) clusterHealthTarget {
			return targets[index]
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	resultChannel := make(chan ObserverResult, 1)
	go func() { resultChannel <- observer.Run(ctx, cfg) }()
	for index := 0; index < len(targets); index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("observer round did not reach all node barriers")
		}
	}
	select {
	case observedAt := <-clock.calls:
		close(release)
		cancel()
		<-resultChannel
		t.Fatalf("Clock.Now() called at %v before node I/O completed", observedAt)
	default:
	}
	completedAt := startedAt.Add(17 * time.Second)
	clock.setNow(completedAt)
	close(release)
	select {
	case observedAt := <-clock.calls:
		if !observedAt.Equal(completedAt) {
			t.Fatalf("window observation time = %v, want round completion %v", observedAt, completedAt)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Clock.Now() was not called after the node round joined")
	}
	cancel()
	if result := <-resultChannel; result.Outcome != ObserverStopped {
		t.Fatalf("result = %+v, want stopped", result)
	}
}

func TestObserverRoundDeadlineAndParentCancellationStopEveryNode(t *testing.T) {
	cfg := LocalConfig()
	started := make(chan context.Context, len(cfg.Observation.ServiceNodes))
	completed := make(chan struct{}, len(cfg.Observation.ServiceNodes))
	targets := make([]*cancelObserverTarget, len(cfg.Observation.ServiceNodes))
	for index := range targets {
		targets[index] = &cancelObserverTarget{started: started, completed: completed}
	}
	clock := newFakeObserverClock(time.Unix(3_000, 0))
	observer := NewObserver(ObserverOptions{
		BenchToken: "bench-token",
		Clock:      clock,
		TargetFactory: func(index int, _ EndpointDeclaration, _ string) clusterHealthTarget {
			return targets[index]
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	resultChannel := make(chan ObserverResult, 1)
	go func() { resultChannel <- observer.Run(ctx, cfg) }()

	contexts := make([]context.Context, 0, len(targets))
	for index := 0; index < len(targets); index++ {
		select {
		case roundCtx := <-started:
			contexts = append(contexts, roundCtx)
		case <-time.After(time.Second):
			cancel()
			t.Fatal("observer did not start every cancelable node poll")
		}
	}
	deadline, ok := contexts[0].Deadline()
	if !ok {
		cancel()
		t.Fatal("production round context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > cfg.Observation.Cadence {
		cancel()
		t.Fatalf("round deadline remaining = %v, want within cadence %v", remaining, cfg.Observation.Cadence)
	}
	for index, roundCtx := range contexts[1:] {
		otherDeadline, otherOK := roundCtx.Deadline()
		if roundCtx != contexts[0] || !otherOK || !otherDeadline.Equal(deadline) {
			cancel()
			t.Fatalf("node %d does not share the one round deadline", index+1)
		}
	}

	cancel()
	for index := 0; index < len(targets); index++ {
		select {
		case <-completed:
		case <-time.After(time.Second):
			t.Fatal("parent cancellation did not release every node poll")
		}
	}
	if result := <-resultChannel; result.Outcome != ObserverStopped {
		t.Fatalf("result = %+v, want stopped", result)
	}
	select {
	case <-clock.ticker.stopped:
	default:
		t.Fatal("observer ticker was not stopped")
	}
}

func TestObserverServiceHealthWindowResetsOnOneHealthyPoll(t *testing.T) {
	cfg := FormalConfig()
	fixture := newObserverFixture(cfg)
	resultChannel := make(chan ObserverResult, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { resultChannel <- fixture.observer.Run(ctx, cfg) }()
	fixture.waitPoll(t)

	fixture.targets[1].setHealthError(errors.New("unavailable"))
	for elapsed := 5 * time.Second; elapsed <= 25*time.Second; elapsed += 5 * time.Second {
		fixture.clock.advance(5 * time.Second)
		fixture.waitPoll(t)
	}
	fixture.targets[1].setHealthError(nil)
	fixture.clock.advance(5 * time.Second)
	fixture.waitPoll(t)
	fixture.targets[1].setHealthError(errors.New("unavailable again"))
	for elapsed := 5 * time.Second; elapsed <= 25*time.Second; elapsed += 5 * time.Second {
		fixture.clock.advance(5 * time.Second)
		fixture.waitPoll(t)
	}
	select {
	case result := <-resultChannel:
		t.Fatalf("observer terminated before reset window elapsed: %+v", result)
	default:
	}
	cancel()
	result := <-resultChannel
	if result.Outcome != ObserverStopped {
		t.Fatalf("result = %+v, want stopped", result)
	}
}

func TestObserverLeaderImbalanceFailsAfterContinuousTenMinutes(t *testing.T) {
	cfg := FormalConfig()
	fixture := newObserverFixture(cfg)
	for _, observed := range fixture.targets {
		observed.mutate(func(snapshot *target.DebugCluster) {
			snapshot.Slots[2].LeaderID = 1
			snapshot.Slots[2].ReplicaProgress = nil
			if snapshot.NodeID == 1 {
				snapshot.Slots[2].ReplicaProgress = []target.ReplicaProgress{
					{NodeID: 1, MatchIndex: 100, State: "StateReplicate"},
					{NodeID: 2, MatchIndex: 100, State: "StateReplicate"},
					{NodeID: 3, MatchIndex: 100, State: "StateReplicate"},
				}
			}
		})
	}
	resultChannel := make(chan ObserverResult, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { resultChannel <- fixture.observer.Run(ctx, cfg) }()
	fixture.waitPoll(t)
	for elapsed := 5 * time.Second; elapsed <= 10*time.Minute; elapsed += 5 * time.Second {
		fixture.clock.advance(5 * time.Second)
		fixture.waitPoll(t)
	}
	result := <-resultChannel
	if result.Outcome != ObserverProductFailure || result.Code != ObserverCodeLeaderImbalance {
		t.Fatalf("result = %+v, want product_failure/leader_imbalance", result)
	}
}

type observerFixture struct {
	observer *Observer
	clock    *fakeObserverClock
	targets  []*fakeObserverTarget
	polls    chan struct{}
}

func newObserverFixture(cfg Config) *observerFixture {
	fixture := &observerFixture{
		clock: newFakeObserverClock(time.Unix(1_000, 0)),
		polls: make(chan struct{}, 1),
	}
	fixture.clock.polls = fixture.polls
	fixture.targets = make([]*fakeObserverTarget, len(cfg.Observation.ServiceNodes))
	for index := range fixture.targets {
		fixture.targets[index] = &fakeObserverTarget{cluster: healthyPreflightCluster(uint64(index + 1))}
	}
	fixture.observer = NewObserver(ObserverOptions{
		BenchToken: "bench-token",
		Clock:      fixture.clock,
		TargetFactory: func(index int, _ EndpointDeclaration, _ string) clusterHealthTarget {
			return fixture.targets[index]
		},
	})
	return fixture
}

func (f *observerFixture) waitPoll(t *testing.T) {
	t.Helper()
	<-f.polls
}

type fakeObserverTarget struct {
	mu        sync.Mutex
	healthErr error
	readyErr  error
	cluster   target.DebugCluster
}

type barrierObserverTarget struct {
	mu        sync.Mutex
	index     int
	started   chan<- int
	completed chan<- int
	release   <-chan struct{}
	cluster   target.DebugCluster
	stages    []string
	contexts  []context.Context
}

type cancelObserverTarget struct {
	started   chan<- context.Context
	completed chan<- struct{}
}

func (f *cancelObserverTarget) Healthz(ctx context.Context) error {
	f.started <- ctx
	<-ctx.Done()
	return ctx.Err()
}

func (f *cancelObserverTarget) Readyz(ctx context.Context) error { return ctx.Err() }

func (f *cancelObserverTarget) DebugCluster(ctx context.Context) (target.DebugCluster, error) {
	f.completed <- struct{}{}
	return target.DebugCluster{}, ctx.Err()
}

func (f *barrierObserverTarget) record(stage string, ctx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stages = append(f.stages, stage)
	f.contexts = append(f.contexts, ctx)
}

func (f *barrierObserverTarget) Healthz(ctx context.Context) error {
	f.record("health", ctx)
	f.started <- f.index
	select {
	case <-f.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *barrierObserverTarget) Readyz(ctx context.Context) error {
	f.record("ready", ctx)
	return ctx.Err()
}

func (f *barrierObserverTarget) DebugCluster(ctx context.Context) (target.DebugCluster, error) {
	f.record("cluster", ctx)
	if err := ctx.Err(); err != nil {
		return target.DebugCluster{}, err
	}
	if f.completed != nil {
		f.completed <- f.index
	}
	return cloneDebugCluster(f.cluster), nil
}

func (f *barrierObserverTarget) snapshot() ([]string, []context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stages...), append([]context.Context(nil), f.contexts...)
}

func (f *fakeObserverTarget) Healthz(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthErr
}

func (f *fakeObserverTarget) Readyz(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readyErr
}

func (f *fakeObserverTarget) DebugCluster(context.Context) (target.DebugCluster, error) {
	f.mu.Lock()
	snapshot := cloneDebugCluster(f.cluster)
	f.mu.Unlock()
	return snapshot, nil
}

func (f *fakeObserverTarget) setHealthError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healthErr = err
}

func (f *fakeObserverTarget) mutate(change func(*target.DebugCluster)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(&f.cluster)
}

func cloneDebugCluster(snapshot target.DebugCluster) target.DebugCluster {
	clone := snapshot
	clone.Slots = append([]target.ClusterSlot(nil), snapshot.Slots...)
	for index := range clone.Slots {
		clone.Slots[index].Replicas = append([]uint64(nil), snapshot.Slots[index].Replicas...)
		clone.Slots[index].Voters = append([]uint64(nil), snapshot.Slots[index].Voters...)
		clone.Slots[index].ReplicaProgress = append([]target.ReplicaProgress(nil), snapshot.Slots[index].ReplicaProgress...)
	}
	return clone
}

type fakeObserverClock struct {
	mu     sync.Mutex
	now    time.Time
	ticker *fakeObserverTicker
	period time.Duration
	polls  chan<- struct{}
}

type completionObserverClock struct {
	mu     sync.Mutex
	now    time.Time
	calls  chan time.Time
	ticker *fakeObserverTicker
}

func newCompletionObserverClock(now time.Time) *completionObserverClock {
	return &completionObserverClock{now: now, calls: make(chan time.Time, 4)}
}

func (c *completionObserverClock) Now() time.Time {
	c.mu.Lock()
	now := c.now
	c.mu.Unlock()
	c.calls <- now
	return now
}

func (c *completionObserverClock) NewTicker(time.Duration) ObserverTicker {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ticker = newFakeObserverTicker()
	return c.ticker
}

func (c *completionObserverClock) setNow(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func newFakeObserverClock(now time.Time) *fakeObserverClock {
	return &fakeObserverClock{now: now}
}

func (c *fakeObserverClock) Now() time.Time {
	c.mu.Lock()
	now := c.now
	polls := c.polls
	c.mu.Unlock()
	if polls != nil {
		polls <- struct{}{}
	}
	return now
}

func (c *fakeObserverClock) NewTicker(period time.Duration) ObserverTicker {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.period = period
	c.ticker = newFakeObserverTicker()
	return c.ticker
}

func (c *fakeObserverClock) advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	now := c.now
	ticker := c.ticker
	c.mu.Unlock()
	ticker.ticks <- now
}

type fakeObserverTicker struct {
	ticks    chan time.Time
	stopped  chan struct{}
	stopOnce sync.Once
}

func newFakeObserverTicker() *fakeObserverTicker {
	return &fakeObserverTicker{ticks: make(chan time.Time, 1), stopped: make(chan struct{})}
}

func (t *fakeObserverTicker) C() <-chan time.Time { return t.ticks }
func (t *fakeObserverTicker) Stop() {
	t.stopOnce.Do(func() { close(t.stopped) })
}
