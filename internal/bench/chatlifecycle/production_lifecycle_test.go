package chatlifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestProductionLifecycleCompletesCohortAndReleasesRawIdentities(t *testing.T) {
	t.Parallel()
	startedAt := time.Unix(1_720_000_000, 0).UTC()
	assignment, err := newInitialLifecycleSlotAssignment()
	if err != nil {
		t.Fatal(err)
	}
	candidates, owners := productionLifecycleCandidates(t, startedAt, assignment)
	fence := WorkerFence{RunID: "run-production-lifecycle", AssignmentID: "assignment-production-lifecycle", Generation: 7}
	leaseEntered := make(chan uint64, 3)
	leaseDeadlines := make(chan time.Time, 3)
	leaseRelease := make(chan struct{})
	approvals := &productionLifecycleApprovals{owners: owners, done: make(chan struct{})}
	var workers [3]ProductionLifecycleWorker
	for workerID := range workers {
		owned := make([]LifecycleCandidate, 0, 400)
		for _, candidate := range candidates {
			if owners[candidate.ChannelID] == uint64(workerID) {
				owned = append(owned, candidate)
			}
		}
		workers[workerID] = &productionLifecycleWorkerFake{
			workerID: uint64(workerID), fence: fence, candidates: owned,
			leaseEntered: leaseEntered, leaseDeadlines: leaseDeadlines, leaseRelease: leaseRelease, approvals: approvals,
		}
	}
	clock := newProductionLifecycleClock(startedAt)
	prober := &productionLifecycleProberFake{phase: productionLifecycleLoaded, calls: make(chan struct{}, 8)}
	runner, err := NewProductionLifecycle(ProductionLifecycleOptions{
		Workers: workers, Prober: prober, Clock: clock, SlotAssignment: assignment,
		Enabled:      true,
		PollEvery:    time.Minute,
		ProbeOptions: LifecycleProbeOptions{BatchSize: lifecycleCohortSize, MaxConcurrency: 1, RequestTimeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx, fence) }()
	clock.waitForTicker(t)
	clock.tick(startedAt.Add(lifecycleNaturalQuiet))
	seenWorkers := make(map[uint64]bool, 3)
	for range 3 {
		select {
		case workerID := <-leaseEntered:
			seenWorkers[workerID] = true
		case <-time.After(time.Second):
			t.Fatal("workers did not enter the lease round concurrently")
		}
	}
	close(leaseRelease)
	if len(seenWorkers) != 3 {
		t.Fatalf("concurrent lease workers = %v, want all three", seenWorkers)
	}
	wantInitialLoadDeadline := startedAt.Add(lifecycleNaturalQuiet + 2*time.Second + productionLifecycleInitialLoadSchedulingReserve)
	for range 3 {
		if got := <-leaseDeadlines; !got.Equal(wantInitialLoadDeadline) {
			t.Fatalf("initial load deadline = %v, want %v", got, wantInitialLoadDeadline)
		}
	}
	prober.waitForCall(t)

	prober.setPhase(productionLifecycleMissing)
	clock.tick(startedAt.Add(11 * time.Minute))
	prober.waitForCall(t)
	select {
	case <-approvals.done:
	case <-time.After(2 * time.Second):
		t.Fatal("cold cohort was not approved for reheat")
	}
	if got := approvals.failure(); got != "" {
		t.Fatal(got)
	}
	if got := approvals.callCount(); got != len(workers) {
		t.Fatalf("reheat approval RPCs = %d, want one batch per worker", got)
	}

	prober.setPhase(productionLifecycleReloaded)
	clock.tick(startedAt.Add(13 * time.Minute))
	prober.waitForCall(t)
	productionLifecycleEventually(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return len(runner.active) == 0 && runner.completed.Completed == lifecycleCohortSize
	})

	snapshot := runner.Snapshot()
	if snapshot.Candidates != lifecycleCohortSize || snapshot.Loaded != lifecycleCohortSize ||
		snapshot.ColdEligible != lifecycleCohortSize || snapshot.Reheated != lifecycleCohortSize || snapshot.Completed != lifecycleCohortSize {
		t.Fatalf("snapshot = %+v, want one completed 12x100 cohort", snapshot)
	}
	if snapshot.ProductFailures != 0 || snapshot.HarnessFailures != 0 {
		t.Fatalf("snapshot failures = %+v", snapshot)
	}
	runner.mu.Lock()
	active := len(runner.active)
	runner.mu.Unlock()
	if active != 0 {
		t.Fatalf("active cohorts = %d, want raw identities released after completion", active)
	}

	cancel()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not join after cancellation")
	}
}

func TestProductionLifecycleLeasesOneFixedCohortAtFiveMinutes(t *testing.T) {
	t.Parallel()
	startedAt := time.Unix(1_720_050_000, 0).UTC()
	assignment, err := newInitialLifecycleSlotAssignment()
	if err != nil {
		t.Fatal(err)
	}
	candidates, owners := productionLifecycleCandidateSet(
		t, "fixed", assignment,
		startedAt.Add(6*time.Minute), startedAt.Add(18*time.Minute), startedAt.Add(20*time.Minute),
	)
	fence := WorkerFence{RunID: "run-fixed-lifecycle", AssignmentID: "assignment-fixed-lifecycle", Generation: 8}
	var workers [3]ProductionLifecycleWorker
	var rotating [3]*productionLifecycleRotatingWorker
	for workerID := range workers {
		owned := make([]LifecycleCandidate, 0, lifecycleCohortSize/3)
		for _, candidate := range candidates {
			if owners[candidate.ChannelID] == uint64(workerID) {
				owned = append(owned, candidate)
			}
		}
		rotating[workerID] = &productionLifecycleRotatingWorker{
			workerID: uint64(workerID), fence: fence, cohorts: [][]LifecycleCandidate{owned},
		}
		workers[workerID] = rotating[workerID]
	}
	clock := newProductionLifecycleClock(startedAt)
	prober := &productionLifecycleProberFake{phase: productionLifecycleLoaded, calls: make(chan struct{}, 4)}
	runner, err := NewProductionLifecycle(ProductionLifecycleOptions{
		Workers: workers, Prober: prober, Clock: clock, SlotAssignment: assignment,
		Enabled: true, PollEvery: time.Minute,
		ProbeOptions: LifecycleProbeOptions{BatchSize: lifecycleCohortSize, MaxConcurrency: 1, RequestTimeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx, fence) }()
	clock.waitForTicker(t)

	clock.tick(startedAt.Add(lifecycleNaturalQuiet))
	prober.waitForCall(t)
	for workerID, worker := range rotating {
		worker.mu.Lock()
		leases := worker.lease
		worker.mu.Unlock()
		if leases != 1 {
			t.Fatalf("worker %d leases after fixed-cohort boundary = %d, want 1", workerID, leases)
		}
	}

	clock.tick(startedAt.Add(lifecycleNaturalQuiet + productionLifecycleFirstLeaseGrace))
	prober.waitForCall(t)
	for workerID, worker := range rotating {
		worker.mu.Lock()
		leases := worker.lease
		worker.mu.Unlock()
		if leases != 1 {
			t.Fatalf("worker %d leases after later cadence = %d, want fixed cohort retained", workerID, leases)
		}
	}
	select {
	case runErr := <-runDone:
		t.Fatalf("Run terminated while polling the fixed cohort: %v", runErr)
	default:
	}
}

func TestProductionLifecycleCancellationJoinsInFlightPoll(t *testing.T) {
	t.Parallel()
	startedAt := time.Unix(1_720_100_000, 0).UTC()
	assignment, err := newInitialLifecycleSlotAssignment()
	if err != nil {
		t.Fatal(err)
	}
	candidates, owners := productionLifecycleCandidates(t, startedAt, assignment)
	fence := WorkerFence{RunID: "run-cancel-lifecycle", AssignmentID: "assignment-cancel-lifecycle", Generation: 3}
	leaseEntered := make(chan uint64, 3)
	leaseRelease := make(chan struct{})
	close(leaseRelease)
	approvals := &productionLifecycleApprovals{owners: owners, done: make(chan struct{})}
	var workers [3]ProductionLifecycleWorker
	for workerID := range workers {
		owned := make([]LifecycleCandidate, 0, 400)
		for _, candidate := range candidates {
			if owners[candidate.ChannelID] == uint64(workerID) {
				owned = append(owned, candidate)
			}
		}
		workers[workerID] = &productionLifecycleWorkerFake{
			workerID: uint64(workerID), fence: fence, candidates: owned,
			leaseEntered: leaseEntered, leaseRelease: leaseRelease, approvals: approvals,
		}
	}
	clock := newProductionLifecycleClock(startedAt)
	prober := &productionLifecycleBlockingProber{entered: make(chan struct{}), returned: make(chan struct{})}
	runner, err := NewProductionLifecycle(ProductionLifecycleOptions{
		Workers: workers, Prober: prober, Clock: clock, SlotAssignment: assignment,
		Enabled:      true,
		PollEvery:    time.Minute,
		ProbeOptions: LifecycleProbeOptions{BatchSize: lifecycleCohortSize, MaxConcurrency: 1, RequestTimeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx, fence) }()
	clock.waitForTicker(t)
	clock.tick(startedAt.Add(lifecycleNaturalQuiet))
	select {
	case <-prober.entered:
	case <-time.After(time.Second):
		t.Fatal("poll did not enter the blocking prober")
	}
	cancel()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context canceled", err)
		}
		select {
		case <-prober.returned:
		default:
			t.Fatal("Run returned before the in-flight poll joined")
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if got := runner.Snapshot().HarnessFailures; got != 0 {
		t.Fatalf("cancellation was classified as %d harness failures", got)
	}
}

func TestProductionLifecycleDisabledDoesNotRequireFormalProofDependencies(t *testing.T) {
	t.Parallel()
	runner, err := NewProductionLifecycle(ProductionLifecycleOptions{Enabled: false})
	if err != nil {
		t.Fatalf("NewProductionLifecycle: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fence := WorkerFence{RunID: "run-local-throughput", AssignmentID: "assignment-local-throughput", Generation: 5}
	if err := runner.Run(ctx, fence); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context canceled", err)
	}
	if snapshot := runner.Snapshot(); snapshot != (LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}) {
		t.Fatalf("disabled lifecycle snapshot = %+v, want zero proof evidence", snapshot)
	}
}

func TestProductionLifecycleReturnsProductClassificationFromProof(t *testing.T) {
	t.Parallel()
	startedAt := time.Unix(1_720_300_000, 0).UTC()
	assignment, err := newInitialLifecycleSlotAssignment()
	if err != nil {
		t.Fatal(err)
	}
	candidates, owners := productionLifecycleCandidates(t, startedAt, assignment)
	fence := WorkerFence{RunID: "run-product-lifecycle", AssignmentID: "assignment-product-lifecycle", Generation: 2}
	leaseEntered := make(chan uint64, 3)
	leaseRelease := make(chan struct{})
	close(leaseRelease)
	approvals := &productionLifecycleApprovals{owners: owners, done: make(chan struct{})}
	var workers [3]ProductionLifecycleWorker
	for workerID := range workers {
		owned := make([]LifecycleCandidate, 0, 400)
		for _, candidate := range candidates {
			if owners[candidate.ChannelID] == uint64(workerID) {
				owned = append(owned, candidate)
			}
		}
		workers[workerID] = &productionLifecycleWorkerFake{
			workerID: uint64(workerID), fence: fence, candidates: owned,
			leaseEntered: leaseEntered, leaseRelease: leaseRelease, approvals: approvals,
		}
	}
	clock := newProductionLifecycleClock(startedAt)
	prober := &productionLifecycleProberFake{phase: productionLifecycleMissing, calls: make(chan struct{}, 2)}
	runner, err := NewProductionLifecycle(ProductionLifecycleOptions{
		Workers: workers, Prober: prober, Clock: clock, SlotAssignment: assignment,
		Enabled:      true,
		PollEvery:    time.Minute,
		ProbeOptions: LifecycleProbeOptions{BatchSize: lifecycleCohortSize, MaxConcurrency: 1, RequestTimeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx, fence) }()
	clock.waitForTicker(t)
	clock.tick(startedAt.Add(lifecycleNaturalQuiet))
	select {
	case err := <-runDone:
		if !errors.Is(err, ErrLifecycleProductFailure) || errors.Is(err, ErrLifecycleHarnessInvalid) {
			t.Fatalf("Run error = %v, want strict product classification", err)
		}
	case <-time.After(time.Second):
		t.Fatal("product transition failure did not terminate the run")
	}
	snapshot := runner.Snapshot()
	if snapshot.ProductFailures != 1 || snapshot.ProductFailureReasons.InitialLoad != 1 || snapshot.HarnessFailures != 0 {
		t.Fatalf("product failure snapshot = %+v", snapshot)
	}
}

type productionLifecyclePhase uint8

const (
	productionLifecycleLoaded productionLifecyclePhase = iota
	productionLifecycleMissing
	productionLifecycleReloaded
)

type productionLifecycleProberFake struct {
	mu    sync.Mutex
	phase productionLifecyclePhase
	calls chan struct{}
}

type productionLifecycleBlockingProber struct {
	entered  chan struct{}
	returned chan struct{}
	once     sync.Once
}

func (p *productionLifecycleBlockingProber) ProbeChannelRuntimeAll(ctx context.Context, _ model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	p.once.Do(func() { close(p.entered) })
	<-ctx.Done()
	close(p.returned)
	return nil, ctx.Err()
}

func (p *productionLifecycleProberFake) setPhase(phase productionLifecyclePhase) {
	p.mu.Lock()
	p.phase = phase
	p.mu.Unlock()
}

func (p *productionLifecycleProberFake) waitForCall(t *testing.T) {
	t.Helper()
	select {
	case <-p.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("lifecycle probe was not called")
	}
}

func (p *productionLifecycleProberFake) ProbeChannelRuntimeAll(_ context.Context, request model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	p.mu.Lock()
	phase := p.phase
	p.mu.Unlock()
	results := make([]model.ChannelRuntimeProbeResult, 3)
	for node := range results {
		results[node].NodeID = uint64(node + 1)
		results[node].Checked = len(request.Channels)
		results[node].Channels = make([]model.ChannelRuntimeProbeChannel, len(request.Channels))
		for index, identity := range request.Channels {
			row := model.ChannelRuntimeProbeChannel{ChannelID: identity.ChannelID, ChannelType: identity.ChannelType}
			switch phase {
			case productionLifecycleMissing:
				row.Role, row.Status = "missing", "missing"
			case productionLifecycleLoaded, productionLifecycleReloaded:
				row.Role, row.Status = "follower", "active"
				if node == 0 {
					row.Role = "leader"
				}
				row.LEO, row.HW, row.CheckpointHW = 10, 10, 10
				if phase == productionLifecycleReloaded {
					row.LEO, row.HW, row.CheckpointHW = 11, 11, 11
				}
			}
			results[node].Channels[index] = row
		}
	}
	p.calls <- struct{}{}
	return results, nil
}

type productionLifecycleApprovals struct {
	mu       sync.Mutex
	owners   map[string]uint64
	approved int
	calls    int
	failed   string
	done     chan struct{}
	once     sync.Once
}

func (a *productionLifecycleApprovals) record(workerID uint64, request WorkerLifecycleReheatRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	for _, item := range request.Items {
		owner, exists := a.owners[item.ChannelID]
		if !exists {
			a.failed = "approval contained an unknown channel identity"
		} else if owner != workerID {
			a.failed = fmt.Sprintf("channel was approved by worker %d, want original owner %d", workerID, owner)
		}
	}
	a.approved += len(request.Items)
	if a.approved == lifecycleCohortSize {
		a.once.Do(func() { close(a.done) })
	}
}

func (a *productionLifecycleApprovals) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *productionLifecycleApprovals) failure() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.failed
}

type productionLifecycleWorkerFake struct {
	workerID       uint64
	fence          WorkerFence
	candidates     []LifecycleCandidate
	leaseEntered   chan<- uint64
	leaseDeadlines chan<- time.Time
	leaseRelease   <-chan struct{}
	approvals      *productionLifecycleApprovals
}

type productionLifecycleRotatingWorker struct {
	mu       sync.Mutex
	workerID uint64
	fence    WorkerFence
	cohorts  [][]LifecycleCandidate
	lease    int
}

func (w *productionLifecycleRotatingWorker) LeaseLifecycleCandidates(_ context.Context, request WorkerLifecycleCandidateLeaseRequest) (WorkerLifecycleCandidateLeaseResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if request.Requested != lifecycleCohortSize || request.InitialLoadDeadline.IsZero() ||
		!sameWorkerFence(request.WorkerFence, w.fence) || w.lease >= len(w.cohorts) {
		return WorkerLifecycleCandidateLeaseResponse{}, ErrLifecycleHarnessInvalid
	}
	candidates := append([]LifecycleCandidate(nil), w.cohorts[w.lease]...)
	w.lease++
	return WorkerLifecycleCandidateLeaseResponse{
		WorkerFence: w.fence, WorkerID: w.workerID, WorkerCount: 3, Candidates: candidates,
	}, nil
}

func (w *productionLifecycleRotatingWorker) ApproveLifecycleReheat(_ context.Context, request WorkerLifecycleReheatRequest) (WorkerLifecycleReheatResponse, error) {
	return WorkerLifecycleReheatResponse{
		WorkerFence: w.fence, WorkerID: w.workerID, WorkerCount: 3,
		Approved: uint16(len(request.Items)),
	}, nil
}

func (w *productionLifecycleWorkerFake) LeaseLifecycleCandidates(ctx context.Context, request WorkerLifecycleCandidateLeaseRequest) (WorkerLifecycleCandidateLeaseResponse, error) {
	if request.Requested != lifecycleCohortSize || request.InitialLoadDeadline.IsZero() || !sameWorkerFence(request.WorkerFence, w.fence) {
		return WorkerLifecycleCandidateLeaseResponse{}, ErrLifecycleHarnessInvalid
	}
	if w.leaseDeadlines != nil {
		w.leaseDeadlines <- request.InitialLoadDeadline
	}
	w.leaseEntered <- w.workerID
	select {
	case <-w.leaseRelease:
	case <-ctx.Done():
		return WorkerLifecycleCandidateLeaseResponse{}, ctx.Err()
	}
	return WorkerLifecycleCandidateLeaseResponse{
		WorkerFence: w.fence, WorkerID: w.workerID, WorkerCount: 3,
		Candidates: append([]LifecycleCandidate(nil), w.candidates...),
	}, nil
}

func (w *productionLifecycleWorkerFake) ApproveLifecycleReheat(_ context.Context, request WorkerLifecycleReheatRequest) (WorkerLifecycleReheatResponse, error) {
	w.approvals.record(w.workerID, request)
	return WorkerLifecycleReheatResponse{WorkerFence: w.fence, WorkerID: w.workerID, WorkerCount: 3, Approved: uint16(len(request.Items))}, nil
}

type productionLifecycleClock struct {
	mu      sync.Mutex
	now     time.Time
	ticker  *productionLifecycleTicker
	created chan struct{}
}

func newProductionLifecycleClock(now time.Time) *productionLifecycleClock {
	return &productionLifecycleClock{now: now, created: make(chan struct{})}
}

func (c *productionLifecycleClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *productionLifecycleClock) NewTicker(time.Duration) ObserverTicker {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ticker = &productionLifecycleTicker{ticks: make(chan time.Time, 16)}
	close(c.created)
	return c.ticker
}

func (c *productionLifecycleClock) waitForTicker(t *testing.T) {
	t.Helper()
	select {
	case <-c.created:
	case <-time.After(time.Second):
		t.Fatal("Run did not create its ticker")
	}
}

func (c *productionLifecycleClock) tick(now time.Time) {
	c.mu.Lock()
	c.now = now
	ticker := c.ticker
	c.mu.Unlock()
	ticker.ticks <- now
}

type productionLifecycleTicker struct {
	ticks chan time.Time
}

func (t *productionLifecycleTicker) C() <-chan time.Time { return t.ticks }
func (t *productionLifecycleTicker) Stop()               {}

func productionLifecycleCandidates(t *testing.T, startedAt time.Time, assignment LifecycleSlotAssignment) ([]LifecycleCandidate, map[string]uint64) {
	t.Helper()
	return productionLifecycleCandidateSet(
		t, "production", assignment,
		startedAt.Add(11*time.Minute), startedAt.Add(12*time.Minute), startedAt.Add(13*time.Minute),
	)
}

func productionLifecycleCandidateSet(
	t *testing.T,
	prefix string,
	assignment LifecycleSlotAssignment,
	quietNotBefore, quietDeadline, reheatAt time.Time,
) ([]LifecycleCandidate, map[string]uint64) {
	t.Helper()
	bySlot := make([]int, formalLogicalSlotGroups)
	candidates := make([]LifecycleCandidate, 0, lifecycleCohortSize)
	owners := make(map[string]uint64, lifecycleCohortSize)
	for index := 0; len(candidates) < lifecycleCohortSize; index++ {
		identity := channelid.EncodePersonChannel(fmt.Sprintf("%s-a-%d", prefix, index), fmt.Sprintf("%s-b-%d", prefix, index))
		hashSlot := lifecycleHashSlotForKey(identity, formalHashSlots)
		slotID, ok := assignment.Lookup(hashSlot)
		if !ok || bySlot[slotID-1] == lifecyclePerSlot {
			continue
		}
		candidate := LifecycleCandidate{
			ChannelID: identity, ChannelType: 1, HashSlot: hashSlot, SlotID: slotID,
			TimerToken: uint64(index + 1), ActivityVersion: 1, InitialSequence: 10,
			QuietNotBefore: quietNotBefore, QuietDeadline: quietDeadline,
			ReheatAt: reheatAt, ObservedLoaded: true,
		}
		owners[identity] = uint64(len(candidates) % 3)
		candidates = append(candidates, candidate)
		bySlot[slotID-1]++
	}
	return candidates, owners
}

func productionLifecycleEventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}
