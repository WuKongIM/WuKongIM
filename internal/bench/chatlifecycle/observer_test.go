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
	fixture.targets = make([]*fakeObserverTarget, len(cfg.Observation.ServiceNodes))
	for index := range fixture.targets {
		fixture.targets[index] = &fakeObserverTarget{cluster: healthyPreflightCluster(uint64(index + 1))}
	}
	fixture.targets[len(fixture.targets)-1].polls = fixture.polls
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
	polls     chan<- struct{}
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
	if f.polls != nil {
		f.polls <- struct{}{}
	}
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
}

func newFakeObserverClock(now time.Time) *fakeObserverClock {
	return &fakeObserverClock{now: now}
}

func (c *fakeObserverClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeObserverClock) NewTicker(period time.Duration) ObserverTicker {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.period = period
	c.ticker = &fakeObserverTicker{ticks: make(chan time.Time, 1)}
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

type fakeObserverTicker struct{ ticks chan time.Time }

func (t *fakeObserverTicker) C() <-chan time.Time { return t.ticks }
func (t *fakeObserverTicker) Stop()               {}
