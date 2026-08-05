package chatlifecycle

import (
	"context"
	"errors"
	"math"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCoordinatorAssignmentPartitionsUsersAndGlobalGrantExactly(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-assignment"
	assignments, err := BuildCoordinatorAssignments(cfg, 7)
	if err != nil {
		t.Fatalf("BuildCoordinatorAssignments() error = %v", err)
	}
	if len(assignments) != 3 {
		t.Fatalf("assignments = %d, want exactly 3", len(assignments))
	}

	seen := make([]bool, cfg.Workload.OnlineUsers)
	var fence WorkerFence
	for index, assignment := range assignments {
		if index == 0 {
			fence = assignment.WorkerFence
		}
		if !sameWorkerFence(assignment.WorkerFence, fence) || assignment.RunID != cfg.RunID || assignment.Generation != 7 {
			t.Fatalf("assignment %d fence = %+v, want shared %+v", index, assignment.WorkerFence, fence)
		}
		if assignment.WorkerID != uint64(index) || assignment.WorkerCount != 3 {
			t.Fatalf("assignment %d worker = %d/%d, want %d/3", index, assignment.WorkerID, assignment.WorkerCount, index)
		}
		partition := assignment.Partition
		if partition.FirstGlobalIndex != uint64(index) || partition.Stride != 3 || partition.RateWeight != 1 {
			t.Fatalf("assignment %d partition = %+v", index, partition)
		}
		for local := uint64(0); local < partition.UserCount; local++ {
			global := partition.FirstGlobalIndex + local*partition.Stride
			if global >= uint64(len(seen)) {
				t.Fatalf("assignment %d global index = %d outside online prefix", index, global)
			}
			if seen[global] {
				t.Fatalf("global index %d assigned more than once", global)
			}
			seen[global] = true
		}
	}
	for global, assigned := range seen {
		if !assigned {
			t.Fatalf("global index %d has an assignment gap", global)
		}
	}

	grantPlan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		t.Fatalf("NewCoordinatorGrantPlan() error = %v", err)
	}
	grant, err := grantPlan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	var fresh, released uint64
	for index := range grant.Fresh {
		fresh += grant.Fresh[index]
		released += grant.Released[index]
	}
	if fresh != uint64(cfg.Workload.SendRatePerSecond) || released != fresh {
		t.Fatalf("global grants fresh/released = %d/%d, want %d", fresh, released, cfg.Workload.SendRatePerSecond)
	}
}

func TestCoordinatorSnapshotAggregationEnforcesFenceSchemaAndMonotonicity(t *testing.T) {
	fence := WorkerFence{RunID: "snapshot-run", AssignmentID: "snapshot-assignment", Generation: 9}
	aggregator, err := NewCoordinatorSnapshotAggregator(fence)
	if err != nil {
		t.Fatalf("NewCoordinatorSnapshotAggregator() error = %v", err)
	}
	first := coordinatorSnapshotFixture(fence, 1, time.Second, 10)
	aggregated, err := aggregator.Aggregate(first)
	if err != nil {
		t.Fatalf("first Aggregate() error = %v", err)
	}
	if aggregated.WorkerCount != 3 || aggregated.Messages.Sent != 33 || aggregated.SendackLatency.Count != 33 || aggregated.SendackLatency.Buckets[1] != 33 {
		t.Fatalf("first aggregate = %+v", aggregated)
	}

	second := coordinatorSnapshotFixture(fence, 2, 2*time.Second, 20)
	if _, err := aggregator.Aggregate(second); err != nil {
		t.Fatalf("second Aggregate() error = %v", err)
	}

	tests := []struct {
		name      string
		mutate    func([]WorkerSnapshot) []WorkerSnapshot
		wantError error
	}{
		{
			name: "missing worker",
			mutate: func(snapshots []WorkerSnapshot) []WorkerSnapshot {
				return snapshots[:2]
			},
			wantError: ErrCoordinatorSnapshotCount,
		},
		{
			name: "duplicate worker",
			mutate: func(snapshots []WorkerSnapshot) []WorkerSnapshot {
				snapshots[2].WorkerID = 1
				return snapshots
			},
			wantError: ErrCoordinatorSnapshotFence,
		},
		{
			name: "cross run",
			mutate: func(snapshots []WorkerSnapshot) []WorkerSnapshot {
				snapshots[2].RunID = "other-run"
				return snapshots
			},
			wantError: ErrCoordinatorSnapshotFence,
		},
		{
			name: "cross assignment",
			mutate: func(snapshots []WorkerSnapshot) []WorkerSnapshot {
				snapshots[2].AssignmentID = "other-assignment"
				return snapshots
			},
			wantError: ErrCoordinatorSnapshotFence,
		},
		{
			name: "cross generation",
			mutate: func(snapshots []WorkerSnapshot) []WorkerSnapshot {
				snapshots[2].Generation++
				return snapshots
			},
			wantError: ErrCoordinatorSnapshotFence,
		},
		{
			name: "histogram schema",
			mutate: func(snapshots []WorkerSnapshot) []WorkerSnapshot {
				snapshots[2].SendackLatency.BucketUpper[1]++
				return snapshots
			},
			wantError: ErrCoordinatorHistogramSchema,
		},
		{
			name: "stale sequence",
			mutate: func(snapshots []WorkerSnapshot) []WorkerSnapshot {
				snapshots[2].SnapshotSequence = 2
				return snapshots
			},
			wantError: ErrCoordinatorSnapshotStale,
		},
		{
			name: "stale worker clock",
			mutate: func(snapshots []WorkerSnapshot) []WorkerSnapshot {
				snapshots[2].Uptime = 2 * time.Second
				return snapshots
			},
			wantError: ErrCoordinatorSnapshotStale,
		},
		{
			name: "counter regression",
			mutate: func(snapshots []WorkerSnapshot) []WorkerSnapshot {
				snapshots[2].Messages.Sent = 1
				return snapshots
			},
			wantError: ErrCoordinatorSnapshotRegression,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := coordinatorSnapshotFixture(fence, 3, 3*time.Second, 30)
			candidate = testCase.mutate(candidate)
			if _, err := aggregator.Aggregate(candidate); !errors.Is(err, testCase.wantError) {
				t.Fatalf("Aggregate() error = %v, want %v", err, testCase.wantError)
			}
		})
	}
}

func TestCoordinatorGrantSnapshotAggregationRejectsCounterOverflow(t *testing.T) {
	fence := WorkerFence{RunID: "snapshot-overflow", AssignmentID: "snapshot-overflow-assignment", Generation: 4}
	aggregator, err := NewCoordinatorSnapshotAggregator(fence)
	if err != nil {
		t.Fatalf("NewCoordinatorSnapshotAggregator() error = %v", err)
	}
	snapshots := coordinatorSnapshotFixture(fence, 1, time.Second, 0)
	snapshots[0].Messages.Sent = math.MaxUint64
	snapshots[1].Messages.Sent = 1
	if _, err := aggregator.Aggregate(snapshots); !errors.Is(err, ErrCoordinatorSnapshotOverflow) {
		t.Fatalf("Aggregate() error = %v, want %v", err, ErrCoordinatorSnapshotOverflow)
	}
}

func TestCoordinatorGrantPlanRetainsOneGlobalTwoTickCredit(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-global-credit"
	assignments, err := BuildCoordinatorAssignments(cfg, 2)
	if err != nil {
		t.Fatalf("BuildCoordinatorAssignments() error = %v", err)
	}
	plan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		t.Fatalf("NewCoordinatorGrantPlan() error = %v", err)
	}
	zeroDemand := [coordinatorWorkerCount]uint64{}
	if _, err := plan.Tick(zeroDemand); err != nil {
		t.Fatalf("first Tick() error = %v", err)
	}
	second, err := plan.Tick(zeroDemand)
	if err != nil {
		t.Fatalf("second Tick() error = %v", err)
	}
	var credit uint64
	for _, count := range second.Credit {
		credit += count
	}
	if credit != uint64(cfg.Workload.MaxGlobalBurst) {
		t.Fatalf("two-tick global credit = %d, want %d", credit, cfg.Workload.MaxGlobalBurst)
	}
	released, err := plan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		t.Fatalf("release Tick() error = %v", err)
	}
	var totalReleased uint64
	for _, count := range released.Released {
		totalReleased += count
	}
	if totalReleased != uint64(cfg.Workload.MaxGlobalBurst) {
		t.Fatalf("released retained credit = %d, want one global burst %d", totalReleased, cfg.Workload.MaxGlobalBurst)
	}
}

func TestCoordinatorDeliversOneGlobalGrantAfterAllWorkersReadyAndRetriesSameSequence(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-grant-delivery"
	log := []string{}
	typedWorkers := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	barrierClock := newGrantBarrierCoordinatorClock(time.Unix(1_700_000_000, 0))
	for workerID := range workers {
		typedWorkers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &log}
		workers[workerID] = typedWorkers[workerID]
	}
	typedWorkers[1].grantResponseLossOnce = true
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 25,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(context.Context, Config) ObserverResult {
			<-barrierClock.reached
			return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth}
		}),
		Clock: barrierClock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	result := coordinator.Run(context.Background(), cfg)
	if result.Outcome != CoordinatorProductFailure || result.Code != CoordinatorCodeObserver {
		t.Fatalf("Run() result = %+v, want product_failure/observer after initial grant", result)
	}
	var common WorkerGrantRequest
	for workerID, worker := range typedWorkers {
		if len(worker.grantRequests) == 0 {
			t.Fatalf("worker %d received no coordinator grant", workerID)
		}
		if workerID == 1 && len(worker.grantRequests) != 2 {
			t.Fatalf("worker 1 grant attempts = %d, want same-sequence transport retry", len(worker.grantRequests))
		}
		if workerID != 1 && len(worker.grantRequests) != 1 {
			t.Fatalf("worker %d grant attempts = %d, want 1", workerID, len(worker.grantRequests))
		}
		if worker.appliedGrantSequences[1] != 1 {
			t.Fatalf("worker %d applied sequence 1 = %d times, want exactly once", workerID, worker.appliedGrantSequences[1])
		}
		if workerID == 0 {
			common = worker.grantRequests[0]
		} else if worker.grantRequests[0] != common {
			t.Fatalf("worker %d grant vector differs: %+v versus %+v", workerID, worker.grantRequests[0], common)
		}
	}
	released, ok := sumWorkerGrantCounts(common.Released)
	if !ok || released != uint64(cfg.Workload.SendRatePerSecond) {
		t.Fatalf("global released sum = %d/%v, want %d", released, ok, cfg.Workload.SendRatePerSecond)
	}
}

func TestCoordinatorDoesNotConsumeGrantBeforeEveryWorkerTrafficReady(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-ready-barrier"
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	statusCalls := make(chan uint64, coordinatorWorkerCount*3)
	grantCalls := make(chan uint64, coordinatorWorkerCount)
	typedWorkers := make([]*staggeredReadyCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		typedWorkers[workerID] = &staggeredReadyCoordinatorWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{
				id: uint64(workerID), log: &[]string{}, grantCalled: grantCalls,
			},
			readyAfter:  workerID + 1,
			statusCalls: statusCalls,
		}
		workers[workerID] = typedWorkers[workerID]
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 27,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(context.Context, Config) ObserverResult {
			for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
				<-grantCalls
			}
			return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	if period := <-clock.created; period != time.Second {
		t.Fatalf("readiness ticker period = %s, want 1s", period)
	}

	for readinessRound := 1; readinessRound <= 3; readinessRound++ {
		clock.advance(time.Second)
		for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
			<-statusCalls
		}
		if readinessRound < 3 {
			for workerID, worker := range typedWorkers {
				if len(worker.grantRequests) != 0 {
					t.Fatalf("round %d worker %d received grant before all workers ready", readinessRound, workerID)
				}
			}
		}
	}
	result := <-resultChannel
	if result.Outcome != CoordinatorProductFailure || result.Code != CoordinatorCodeObserver {
		t.Fatalf("Run() result = %+v, want product observer result after readiness barrier", result)
	}
	if result.Grant.Sequence != 1 {
		t.Fatalf("first post-readiness grant sequence = %d, want 1", result.Grant.Sequence)
	}
}

func TestCoordinatorTrafficReadinessIsBoundedByWarmupDeadline(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-ready-timeout"
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	statusCalls := make(chan uint64, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &staggeredReadyCoordinatorWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
			readyAfter:                 math.MaxInt,
			statusCalls:                statusCalls,
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 28,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-clock.created
	clock.advance(cfg.Thresholds.Timeline.Warmup)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		<-statusCalls
	}
	select {
	case result := <-resultChannel:
		if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeRuntime {
			t.Fatalf("Run() result = %+v, want harness_invalid/runtime readiness timeout", result)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("traffic readiness remained unbounded beyond warmup deadline")
	}
}

func TestCoordinatorObserverProductFailureStopsWorkersBeforeTrafficReady(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-observer-before-ready"
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &staggeredReadyCoordinatorWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &log},
			readyAfter:                 math.MaxInt,
			statusCalls:                make(chan uint64, 1),
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 32,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(context.Context, Config) ObserverResult {
			return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth}
		}),
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	result := coordinator.Run(ctx, cfg)
	if result.Outcome != CoordinatorProductFailure || result.Code != CoordinatorCodeObserver {
		t.Fatalf("Run() pre-ready observer result = %+v, want product_failure/observer", result)
	}
	wantStops := []string{"stop-0", "stop-1", "stop-2"}
	canonical := canonicalCoordinatorLog(&log)
	if len(canonical) < len(wantStops) || !reflect.DeepEqual(canonical[len(canonical)-len(wantStops):], wantStops) {
		t.Fatalf("pre-ready observer cleanup = %v, want all fixed workers stopped", canonical)
	}
}

func TestCoordinatorReadinessTimeoutPreservesObserverProductRace(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-ready-timeout-product-race"
	cfg.Thresholds.Timeline.Warmup = time.Second
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	statusCalls := make(chan uint64, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &staggeredReadyCoordinatorWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
			readyAfter:                 math.MaxInt,
			statusCalls:                statusCalls,
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 33,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-clock.created
	clock.advance(time.Second)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		<-statusCalls
	}
	result := <-resultChannel
	if result.Outcome != CoordinatorProductFailure || result.Code != CoordinatorCodeObserver {
		t.Fatalf("Run() readiness timeout race = %+v, want product_failure/observer", result)
	}
}

func coordinatorSnapshotFixture(fence WorkerFence, sequence uint64, uptime time.Duration, base uint64) []WorkerSnapshot {
	snapshots := make([]WorkerSnapshot, coordinatorWorkerCount)
	for workerID := range snapshots {
		count := base + uint64(workerID)
		histogram := newWorkerHistogramSnapshot()
		histogram.Count = count
		histogram.SumNanos = count * uint64(time.Millisecond)
		histogram.MaxNanos = uint64(time.Millisecond)
		histogram.Buckets[1] = count
		snapshots[workerID] = WorkerSnapshot{
			RunID: fence.RunID, AssignmentID: fence.AssignmentID,
			Phase: WorkerPhaseRunning, Uptime: uptime, SnapshotSequence: sequence,
			Generation: fence.Generation, WorkerID: uint64(workerID), WorkerCount: coordinatorWorkerCount,
			Messages: WorkerMessageSnapshot{Sent: count},
			Sync: WorkerSyncSnapshot{
				ConnectLatency: newWorkerHistogramSnapshot(), Latency: newWorkerHistogramSnapshot(),
			},
			SendackLatency: histogram,
			RecvackLatency: newWorkerHistogramSnapshot(),
		}
	}
	return snapshots
}

func configureFastCoordinatorCutoff(cfg *Config) {
	cfg.Observation.Cadence = 250 * time.Millisecond
	cfg.Thresholds.Timeline = TimelineThresholds{
		Warmup: 100 * time.Millisecond, Checkpoint: 500 * time.Millisecond, Final: 750 * time.Millisecond,
	}
}

func TestCoordinatorStartUsesStrictOrderAndFinalizesExactWorkers(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-start-order"
	configureFastCoordinatorCutoff(&cfg)
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	observerStarted := make(chan struct{})
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		workers[workerID] = &recordingCoordinatorWorker{
			id: uint64(workerID), log: &log, grantBarrier: observerStarted,
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 17,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			appendCoordinatorTestLog(&log, "preflight")
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error {
			appendCoordinatorTestLog(&log, "setup")
			return nil
		}),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			appendCoordinatorTestLog(&log, "observe")
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	clock.advance(cfg.Thresholds.Timeline.Final)
	result := <-resultChannel
	if result.Outcome != CoordinatorCompleted || result.Code != CoordinatorCodeCompleted {
		t.Fatalf("Run() result = %+v", result)
	}
	if result.Fence.RunID != cfg.RunID || result.Fence.Generation != 17 || result.Fence.AssignmentID == "" {
		t.Fatalf("Run() fence = %+v", result.Fence)
	}
	if result.Snapshot.Phase != WorkerPhaseFinal || result.Snapshot.WorkerCount != coordinatorWorkerCount {
		t.Fatalf("Run() final snapshot = %+v", result.Snapshot)
	}
	var granted uint64
	for _, count := range result.Grant.Released {
		granted += count
	}
	if granted != uint64(cfg.Workload.SendRatePerSecond) {
		t.Fatalf("Run() initial global grant = %d, want %d", granted, cfg.Workload.SendRatePerSecond)
	}
	want := []string{
		"preflight", "setup",
		"assign-0", "assign-1", "assign-2",
		"start-0", "start-1", "start-2",
		"grant-0", "grant-1", "grant-2",
		"observe",
		"checkpoint-0", "checkpoint-1", "checkpoint-2",
		"stop-0", "stop-1", "stop-2",
	}
	if !reflect.DeepEqual(canonicalCoordinatorLog(&log), want) {
		t.Fatalf("coordinator order = %v, want %v", log, want)
	}
	callCount := len(log)
	reused := coordinator.Run(context.Background(), cfg)
	if reused.Outcome != CoordinatorHarnessInvalid || reused.Code != CoordinatorCodeGenerationReuse {
		t.Fatalf("reused Run() result = %+v, want harness_invalid/generation_reuse", reused)
	}
	if len(log) != callCount {
		t.Fatalf("reused Run() made %d additional calls", len(log)-callCount)
	}
}

func TestCoordinatorHealthyObservationCompletesOnlyAtFinalCutoff(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-final-cutoff"
	cfg.Observation.Cadence = 500 * time.Millisecond
	cfg.Thresholds.Timeline = TimelineThresholds{
		Warmup: 100 * time.Millisecond, Checkpoint: time.Second, Final: 2500 * time.Millisecond,
	}
	observerFixture := newObserverFixture(cfg)
	coordinatorClock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	statusCalls := make(chan uint64, coordinatorWorkerCount)
	grantCalls := make(chan uint64, coordinatorWorkerCount)
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		workers[workerID] = &recordingCoordinatorWorker{
			id: uint64(workerID), log: &log, statusCalls: statusCalls, grantCalled: grantCalls,
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 18,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:    coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers:  workers,
		Observer: observerFixture.observer,
		Clock:    coordinatorClock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(ctx, cfg) }()

	observerFixture.waitPoll(t)
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-coordinatorClock.created
	}
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		<-grantCalls // initial grant barrier
	}
	for step := 1; step <= 4; step++ {
		coordinatorClock.advance(cfg.Observation.Cadence)
		for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
			<-statusCalls
		}
		if step%2 == 0 {
			for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
				<-grantCalls
			}
		}
	}
	select {
	case result := <-resultChannel:
		t.Fatalf("Run() completed before final cutoff: %+v", result)
	default:
	}
	for _, operation := range coordinatorTestLogSnapshot(&log) {
		if operation == "checkpoint-0" || operation == "checkpoint-1" || operation == "checkpoint-2" ||
			operation == "stop-0" || operation == "stop-1" || operation == "stop-2" {
			t.Fatalf("operation %q ran before final cutoff", operation)
		}
	}

	coordinatorClock.advance(cfg.Observation.Cadence)
	result := <-resultChannel
	if result.Outcome != CoordinatorCompleted || result.Code != CoordinatorCodeCompleted {
		t.Fatalf("Run() at final cutoff = %+v, want completed/completed", result)
	}
	if result.Snapshot.Phase != WorkerPhaseFinal {
		t.Fatalf("final snapshot phase = %s, want final", result.Snapshot.Phase)
	}
}

func TestCoordinatorFinalCutoffPreservesConcurrentProductFailure(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-cutoff-product-race"
	configureFastCoordinatorCutoff(&cfg)
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	observerStarted := make(chan struct{})
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		workers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &log}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 19,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	clock.advance(cfg.Thresholds.Timeline.Final)
	result := <-resultChannel
	if result.Outcome != CoordinatorProductFailure || result.Code != CoordinatorCodeObserver {
		t.Fatalf("Run() cutoff race = %+v, want product_failure/observer", result)
	}
	wantTail := []string{"stop-0", "stop-1", "stop-2"}
	canonical := canonicalCoordinatorLog(&log)
	if len(canonical) < len(wantTail) || !reflect.DeepEqual(canonical[len(canonical)-len(wantTail):], wantTail) {
		t.Fatalf("cleanup tail = %v, want %v", log, wantTail)
	}
	for _, operation := range coordinatorTestLogSnapshot(&log) {
		if len(operation) >= len("checkpoint-") && operation[:len("checkpoint-")] == "checkpoint-" {
			t.Fatalf("product cutoff race ran %q", operation)
		}
	}
}

func TestCoordinatorGrantFailurePreservesConcurrentObserverProductFailure(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-grant-product-race"
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	observerStarted := make(chan struct{})
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		worker := &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}}
		if workerID == 1 {
			worker.grantFailSequence = 2
		}
		workers[workerID] = worker
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 26,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	clock.advance(time.Second)
	result := <-resultChannel
	if result.Outcome != CoordinatorProductFailure || result.Code != CoordinatorCodeObserver {
		t.Fatalf("Run() race result = %+v, want observer product failure to outrank grant harness failure", result)
	}
}

func TestCoordinatorCallerCancellationStopsWithoutCheckpoint(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-caller-cancel"
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	observerStarted := make(chan struct{})
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		workers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &log}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 20,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(ctx, cfg) }()
	<-observerStarted
	cancel()
	result := <-resultChannel
	if result.Outcome != CoordinatorStopped || result.Code != CoordinatorCodeStopped {
		t.Fatalf("Run() caller cancel = %+v, want stopped/stopped", result)
	}
	for _, operation := range coordinatorTestLogSnapshot(&log) {
		if len(operation) >= len("checkpoint-") && operation[:len("checkpoint-")] == "checkpoint-" {
			t.Fatalf("caller cancellation ran %q", operation)
		}
	}
}

func TestCoordinatorStartFailuresFenceTrafficAndBestEffortStop(t *testing.T) {
	errInjected := errors.New("injected coordinator failure")
	tests := []struct {
		name                   string
		preflight              PreflightResult
		setupErr               error
		workerChange           func([]*recordingCoordinatorWorker)
		observer               ObserverResult
		observerWaitsForCancel bool
		wantOutcome            CoordinatorOutcome
		wantCode               CoordinatorCode
		wantLog                []string
	}{
		{
			name:        "failed preflight performs no setup or assignment",
			preflight:   PreflightResult{Outcome: PreflightHarnessInvalid, Code: PreflightCodeCluster},
			observer:    ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped},
			wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodePreflight,
			wantLog: []string{"preflight"},
		},
		{
			name:      "failed setup performs no assignment",
			preflight: PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}, setupErr: errInjected,
			observer:    ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped},
			wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeSetup,
			wantLog: []string{"preflight", "setup"},
		},
		{
			name:      "lost assignment response stops prior and attempted workers",
			preflight: PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK},
			workerChange: func(workers []*recordingCoordinatorWorker) {
				workers[1].assignErr = errInjected
				workers[1].installAssignmentBeforeError = true
			},
			observer:    ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped},
			wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeAssignment,
			wantLog: []string{"preflight", "setup", "assign-0", "assign-1", "assign-2", "stop-0", "stop-1", "stop-2"},
		},
		{
			name:      "rejected assignment still stops attempted worker and ignores cleanup error",
			preflight: PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK},
			workerChange: func(workers []*recordingCoordinatorWorker) {
				workers[1].assignErr = errInjected
				workers[1].stopErr = errInjected
			},
			observer:    ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped},
			wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeAssignment,
			wantLog: []string{"preflight", "setup", "assign-0", "assign-1", "assign-2", "stop-0", "stop-1", "stop-2"},
		},
		{
			name:      "start failure stops every assigned worker despite cleanup error",
			preflight: PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK},
			workerChange: func(workers []*recordingCoordinatorWorker) {
				workers[1].startErr = errInjected
				workers[0].stopErr = errInjected
			},
			observer:    ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped},
			wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeStart,
			wantLog: []string{
				"preflight", "setup", "assign-0", "assign-1", "assign-2", "start-0", "start-1", "start-2",
				"stop-0", "stop-1", "stop-2",
			},
		},
		{
			name:      "global grant failure stops every assigned worker",
			preflight: PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK},
			workerChange: func(workers []*recordingCoordinatorWorker) {
				workers[1].rateErr = errInjected
			},
			observer:               ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped},
			observerWaitsForCancel: true,
			wantOutcome:            CoordinatorHarnessInvalid, wantCode: CoordinatorCodeGrant,
			wantLog: []string{
				"preflight", "setup", "assign-0", "assign-1", "assign-2",
				"start-0", "start-1", "start-2", "grant-0", "grant-1", "grant-1", "grant-2",
				"observe",
				"stop-0", "stop-1", "stop-2",
			},
		},
		{
			name:         "product observer failure survives cleanup error",
			preflight:    PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK},
			workerChange: func(workers []*recordingCoordinatorWorker) { workers[0].stopErr = errInjected },
			observer:     ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth},
			wantOutcome:  CoordinatorProductFailure, wantCode: CoordinatorCodeObserver,
			wantLog: []string{
				"preflight", "setup", "assign-0", "assign-1", "assign-2",
				"start-0", "start-1", "start-2", "grant-0", "grant-1", "grant-2",
				"observe", "stop-0", "stop-1", "stop-2",
			},
		},
		{
			name:        "unexpected observer stop cannot complete before final",
			preflight:   PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK},
			observer:    ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped},
			wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeObserver,
			wantLog: []string{
				"preflight", "setup", "assign-0", "assign-1", "assign-2",
				"start-0", "start-1", "start-2", "grant-0", "grant-1", "grant-2",
				"observe", "stop-0", "stop-1", "stop-2",
			},
		},
		{
			name:        "harness observer failure remains harness invalid",
			preflight:   PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK},
			observer:    ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeTopology},
			wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeObserver,
			wantLog: []string{
				"preflight", "setup", "assign-0", "assign-1", "assign-2",
				"start-0", "start-1", "start-2", "grant-0", "grant-1", "grant-2",
				"observe", "stop-0", "stop-1", "stop-2",
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := LocalConfig()
			cfg.RunID = "coordinator-failure-" + strconv.Itoa(len(testCase.name))
			log := []string{}
			typedWorkers := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			for workerID := range workers {
				typedWorkers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &log}
				workers[workerID] = typedWorkers[workerID]
			}
			if testCase.workerChange != nil {
				testCase.workerChange(typedWorkers)
			}
			coordinator, err := NewCoordinator(CoordinatorOptions{
				Generation: 3,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					appendCoordinatorTestLog(&log, "preflight")
					return testCase.preflight
				}),
				Setup: coordinatorSetupFunc(func(context.Context, Config) error {
					appendCoordinatorTestLog(&log, "setup")
					return testCase.setupErr
				}),
				Workers: workers,
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					appendCoordinatorTestLog(&log, "observe")
					if testCase.observerWaitsForCancel {
						<-ctx.Done()
					}
					return testCase.observer
				}),
			})
			if err != nil {
				t.Fatalf("NewCoordinator() error = %v", err)
			}
			result := coordinator.Run(context.Background(), cfg)
			if result.Outcome != testCase.wantOutcome || result.Code != testCase.wantCode {
				t.Fatalf("Run() result = %+v, want %s/%s", result, testCase.wantOutcome, testCase.wantCode)
			}
			if !reflect.DeepEqual(canonicalCoordinatorLog(&log), testCase.wantLog) {
				t.Fatalf("call log = %v, want %v", log, testCase.wantLog)
			}
			for _, worker := range typedWorkers {
				if worker.fenceMismatch {
					t.Fatalf("worker %d received a mismatched control fence", worker.id)
				}
			}
		})
	}
}

func TestCoordinatorAssignmentCleanupUsesIndependentContextAfterCallerCancel(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-assignment-canceled-caller"
	log := []string{}
	typedWorkers := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		typedWorkers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &log}
		workers[workerID] = typedWorkers[workerID]
	}
	typedWorkers[0].assignErr = errors.New("assignment transport error")
	typedWorkers[0].stopErr = errors.New("cleanup error")
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 21,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			appendCoordinatorTestLog(&log, "preflight")
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error {
			appendCoordinatorTestLog(&log, "setup")
			return nil
		}),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(context.Context, Config) ObserverResult {
			t.Fatal("observer ran after assignment failure")
			return ObserverResult{}
		}),
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := coordinator.Run(ctx, cfg)
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeAssignment {
		t.Fatalf("Run() result = %+v, want harness_invalid/assignment", result)
	}
	if want := []string{
		"preflight", "setup", "assign-0", "assign-1", "assign-2", "stop-0", "stop-1", "stop-2",
	}; !reflect.DeepEqual(canonicalCoordinatorLog(&log), want) {
		t.Fatalf("call log = %v, want %v", log, want)
	}
	if typedWorkers[0].stopSawCanceledContext {
		t.Fatal("assignment cleanup inherited the canceled caller context")
	}
}

func TestCoordinatorStartRuntimeFailureStopsAllWorkers(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-runtime-failure"
	log := []string{}
	typedWorkers := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		typedWorkers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &log}
		workers[workerID] = typedWorkers[workerID]
	}
	typedWorkers[1].statusErr = errors.New("injected runtime status failure")
	ticker := newFakeObserverTicker()
	observerStarted := make(chan struct{})
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 5,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			appendCoordinatorTestLog(&log, "preflight")
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error {
			appendCoordinatorTestLog(&log, "setup")
			return nil
		}),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			appendCoordinatorTestLog(&log, "observe")
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock: fixedCoordinatorClock{now: time.Unix(1_700_000_000, 0), ticker: ticker},
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	ticker.ticks <- time.Unix(1_700_000_005, 0)
	result := <-resultChannel
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeRuntime {
		t.Fatalf("Run() result = %+v, want harness_invalid/runtime", result)
	}
	want := []string{
		"preflight", "setup", "assign-0", "assign-1", "assign-2",
		"start-0", "start-1", "start-2", "grant-0", "grant-1", "grant-2",
		"observe", "status-0", "status-1", "status-2",
		"stop-0", "stop-1", "stop-2",
	}
	if !reflect.DeepEqual(canonicalCoordinatorLog(&log), want) {
		t.Fatalf("call log = %v, want %v", log, want)
	}
}

func TestCoordinatorStatusRoundUsesOneBoundedConcurrentDeadline(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-status-round-deadline"
	ticker := newFakeObserverTicker()
	observerStarted := make(chan struct{})
	statusEntered := make(chan coordinatorStatusContext, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &deadlineCoordinatorWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
			entered:                    statusEntered,
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 23,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock:        fixedCoordinatorClock{now: time.Unix(1_700_000_000, 0), ticker: ticker},
		RoundTimeout: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	ticker.ticks <- time.Unix(1_700_000_005, 0)

	contexts := make([]coordinatorStatusContext, 0, coordinatorWorkerCount)
	for len(contexts) < coordinatorWorkerCount {
		select {
		case observed := <-statusEntered:
			contexts = append(contexts, observed)
		case result := <-resultChannel:
			t.Fatalf("Run() returned before all status calls entered: %+v; contexts=%+v", result, contexts)
		case <-time.After(250 * time.Millisecond):
			t.Fatalf("status calls entered = %d, want 3 concurrent calls", len(contexts))
		}
	}
	for index, observed := range contexts {
		if !observed.hasDeadline {
			t.Fatalf("worker %d status context has no deadline", observed.workerID)
		}
		if index > 0 && !observed.deadline.Equal(contexts[0].deadline) {
			t.Fatalf("status deadlines differ: %v versus %v", observed.deadline, contexts[0].deadline)
		}
	}
	select {
	case result := <-resultChannel:
		if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeRuntime {
			t.Fatalf("Run() result = %+v, want harness_invalid/runtime", result)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("bounded status round did not terminate")
	}
}

func TestCoordinatorAssignAndStartRoundsUseOneConcurrentDeadline(t *testing.T) {
	for _, stage := range []CoordinatorCode{CoordinatorCodeAssignment, CoordinatorCodeStart} {
		t.Run(string(stage), func(t *testing.T) {
			const roundTimeout = 80 * time.Millisecond
			cfg := LocalConfig()
			cfg.RunID = "coordinator-stage-round-" + string(stage)
			probe := newCoordinatorStageProbe()
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			for workerID := range workers {
				workers[workerID] = &blockingCoordinatorStageWorker{
					recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
					stage:                      stage, probe: probe,
				}
			}
			coordinator, err := NewCoordinator(CoordinatorOptions{
				Generation: 34,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
				}),
				Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
				Workers: workers,
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					<-ctx.Done()
					return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
				}),
				RoundTimeout: roundTimeout,
			})
			if err != nil {
				t.Fatalf("NewCoordinator() error = %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
			defer cancel()
			started := time.Now()
			result := coordinator.Run(ctx, cfg)
			elapsed := time.Since(started)
			if result.Outcome != CoordinatorHarnessInvalid || result.Code != stage {
				t.Fatalf("Run() %s result = %+v, want harness_invalid/%s", stage, result, stage)
			}
			if elapsed < roundTimeout/2 || elapsed >= 220*time.Millisecond {
				t.Fatalf("Run() %s elapsed = %s, want one %s shared deadline", stage, elapsed, roundTimeout)
			}
			assertCoordinatorStageProbe(t, probe, started, 160*time.Millisecond)
		})
	}
}

func TestCoordinatorCheckpointRoundUsesOneConcurrentDeadline(t *testing.T) {
	const roundTimeout = 80 * time.Millisecond
	cfg := LocalConfig()
	cfg.RunID = "coordinator-checkpoint-round"
	cfg.Observation.Cadence = 500 * time.Millisecond
	cfg.Thresholds.Timeline = TimelineThresholds{
		Warmup: 100 * time.Millisecond, Checkpoint: time.Second, Final: 1500 * time.Millisecond,
	}
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	observerStarted := make(chan struct{})
	probe := newCoordinatorStageProbe()
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &blockingCoordinatorStageWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
			stage:                      CoordinatorCodeCheckpoint, probe: probe,
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 35,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock: clock, RoundTimeout: roundTimeout,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resultChannel := make(chan CoordinatorResult, 1)
	started := time.Now()
	go func() { resultChannel <- coordinator.Run(ctx, cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	clock.advance(cfg.Thresholds.Timeline.Final)
	result := <-resultChannel
	elapsed := time.Since(started)
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeCheckpoint {
		t.Fatalf("Run() checkpoint result = %+v, want harness_invalid/checkpoint", result)
	}
	if elapsed >= 300*time.Millisecond {
		t.Fatalf("Run() checkpoint elapsed = %s, want one %s shared deadline", elapsed, roundTimeout)
	}
	assertCoordinatorStageProbe(t, probe, started, 200*time.Millisecond)
}

func TestCoordinatorOuterCancellationDuringControlRoundIsStopped(t *testing.T) {
	for _, stage := range []CoordinatorCode{
		CoordinatorCodeAssignment,
		CoordinatorCodeStart,
		CoordinatorCodeCheckpoint,
	} {
		t.Run(string(stage), func(t *testing.T) {
			cfg := LocalConfig()
			cfg.RunID = "coordinator-cancel-" + string(stage)
			cfg.Observation.Cadence = 500 * time.Millisecond
			cfg.Thresholds.Timeline = TimelineThresholds{
				Warmup: 100 * time.Millisecond, Checkpoint: time.Second, Final: 1500 * time.Millisecond,
			}
			gate := newCoordinatorCancellationGate()
			log := []string{}
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			for workerID := range workers {
				workers[workerID] = &cancelingCoordinatorStageWorker{
					recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &log},
					stage:                      stage,
					gate:                       gate,
				}
			}
			observerStarted := make(chan struct{})
			options := CoordinatorOptions{
				Generation: 36,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
				}),
				Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
				Workers: workers,
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					close(observerStarted)
					<-ctx.Done()
					return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
				}),
				RoundTimeout: time.Second,
			}
			var clock *manualCoordinatorClock
			if stage == CoordinatorCodeCheckpoint {
				clock = newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
				options.Clock = clock
			}
			coordinator, err := NewCoordinator(options)
			if err != nil {
				t.Fatalf("NewCoordinator() error = %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			resultChannel := make(chan CoordinatorResult, 1)
			go func() { resultChannel <- coordinator.Run(ctx, cfg) }()
			if stage == CoordinatorCodeCheckpoint {
				<-observerStarted
				for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
					<-clock.created
				}
				clock.advance(cfg.Thresholds.Timeline.Final)
			}
			<-gate.allEntered
			cancel()

			select {
			case result := <-resultChannel:
				if result.Outcome != CoordinatorStopped || result.Code != CoordinatorCodeStopped {
					t.Fatalf("Run() cancellation during %s = %+v, want stopped/stopped", stage, result)
				}
				if stage == CoordinatorCodeCheckpoint && !reflect.DeepEqual(result.Snapshot, CoordinatorSnapshot{}) {
					t.Fatalf("canceled final checkpoint was counted complete: %+v", result.Snapshot)
				}
			case <-time.After(time.Second):
				t.Fatalf("Run() did not stop after cancellation during %s", stage)
			}
		})
	}
}

func TestResolveCoordinatorRoundDispositionClassifiesEvidenceCausally(t *testing.T) {
	causalCancellation := [coordinatorWorkerCount]coordinatorRoundEvidence{
		{err: context.Canceled}, {err: context.Canceled}, {err: context.Canceled},
	}
	tests := []struct {
		name        string
		evidence    [coordinatorWorkerCount]coordinatorRoundEvidence
		ownDeadline bool
		want        coordinatorRoundDisposition
	}{
		{
			name: "parent canceled with ordinary error is stage failure",
			evidence: [coordinatorWorkerCount]coordinatorRoundEvidence{
				{err: errors.New("ordinary RPC failure")}, {err: context.Canceled}, {err: context.Canceled},
			},
			want: coordinatorRoundStageFailed,
		},
		{
			name: "parent canceled with nil invalid response is stage failure",
			evidence: [coordinatorWorkerCount]coordinatorRoundEvidence{
				{}, {err: context.Canceled}, {err: context.Canceled},
			},
			want: coordinatorRoundStageFailed,
		},
		{
			name: "parent canceled does not absorb worker deadline error",
			evidence: [coordinatorWorkerCount]coordinatorRoundEvidence{
				{err: context.DeadlineExceeded}, {err: context.Canceled}, {err: context.Canceled},
			},
			want: coordinatorRoundStageFailed,
		},
		{
			name:     "parent canceled with causal cancellation errors is parent cancellation",
			evidence: causalCancellation,
			want:     coordinatorRoundParentCanceled,
		},
		{
			name:        "round deadline remains stage failure after parent cancellation",
			evidence:    causalCancellation,
			ownDeadline: true,
			want:        coordinatorRoundStageFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			var roundContext context.Context
			var cancelRound context.CancelFunc
			if test.ownDeadline {
				roundContext, cancelRound = context.WithTimeoutCause(parent, time.Nanosecond, errCoordinatorRoundDeadline)
				<-roundContext.Done()
			} else {
				roundContext, cancelRound = context.WithCancel(parent)
			}
			defer cancelRound()
			cancelParent()

			if got := resolveCoordinatorRoundDisposition(parent, roundContext, test.evidence); got != test.want {
				t.Fatalf("resolveCoordinatorRoundDisposition() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCoordinatorGrantAndStatusTerminationSurvivesLateCallerCancellation(t *testing.T) {
	tests := []struct {
		name        string
		stage       lateCancellationRoundStage
		evidence    lateCancellationRoundEvidence
		wantOutcome CoordinatorOutcome
		wantCode    CoordinatorCode
	}{
		{name: "grant ordinary error", stage: lateCancellationGrant, evidence: lateCancellationOrdinaryError, wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeGrant},
		{name: "grant nil invalid response", stage: lateCancellationGrant, evidence: lateCancellationInvalidResponse, wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeGrant},
		{name: "grant own deadline", stage: lateCancellationGrant, evidence: lateCancellationOwnDeadline, wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeGrant},
		{name: "grant causal parent cancel", stage: lateCancellationGrant, evidence: lateCancellationParentCancel, wantOutcome: CoordinatorStopped, wantCode: CoordinatorCodeStopped},
		{name: "status ordinary error", stage: lateCancellationStatus, evidence: lateCancellationOrdinaryError, wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeRuntime},
		{name: "status nil invalid response", stage: lateCancellationStatus, evidence: lateCancellationInvalidResponse, wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeRuntime},
		{name: "status own deadline", stage: lateCancellationStatus, evidence: lateCancellationOwnDeadline, wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeRuntime},
		{name: "status causal parent cancel", stage: lateCancellationStatus, evidence: lateCancellationParentCancel, wantOutcome: CoordinatorStopped, wantCode: CoordinatorCodeStopped},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := LocalConfig()
			cfg.RunID = "coordinator-locked-termination-" + strings.ReplaceAll(test.name, " ", "-")
			statusTicker := newFakeObserverTicker()
			observerStarted := make(chan struct{})
			roundEntered := make(chan struct{})
			cleanup := newLateCancellationCleanupGate()
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			for workerID := range workers {
				workers[workerID] = &lateCancellationCoordinatorWorker{
					recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
					stage:                      test.stage, evidence: test.evidence, roundEntered: roundEntered, cleanup: cleanup,
				}
			}
			options := CoordinatorOptions{
				Generation: 39,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
				}),
				Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
				Workers: workers,
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					close(observerStarted)
					<-ctx.Done()
					return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
				}),
				Clock:          fixedCoordinatorClock{now: time.Unix(1_700_000_000, 0), ticker: statusTicker},
				CleanupTimeout: time.Second,
				RoundTimeout:   time.Second,
			}
			if test.evidence == lateCancellationOwnDeadline {
				options.RoundTimeout = 10 * time.Millisecond
			}
			coordinator, err := NewCoordinator(options)
			if err != nil {
				t.Fatalf("NewCoordinator() error = %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resultChannel := make(chan CoordinatorResult, 1)
			go func() { resultChannel <- coordinator.Run(ctx, cfg) }()
			<-observerStarted
			if test.stage == lateCancellationStatus {
				statusTicker.ticks <- time.Unix(1_700_000_005, 0)
			}
			if test.evidence == lateCancellationParentCancel {
				waitForCoordinatorSignal(t, roundEntered, "round entry")
				cancel()
			}
			waitForCoordinatorSignal(t, cleanup.entered, "failure cleanup")
			if test.evidence != lateCancellationParentCancel {
				cancel()
			}
			close(cleanup.release)

			select {
			case result := <-resultChannel:
				if result.Outcome != test.wantOutcome || result.Code != test.wantCode {
					t.Fatalf("Run() = %+v, want %s/%s", result, test.wantOutcome, test.wantCode)
				}
			case <-time.After(time.Second):
				t.Fatal("Run() did not finish after cleanup release")
			}
		})
	}
}

func TestCoordinatorObservedFailureSurvivesLateCallerCancellation(t *testing.T) {
	tests := []struct {
		name                    string
		observation             ObserverResult
		cancelBeforeObservation bool
		wantOutcome             CoordinatorOutcome
	}{
		{
			name:        "product failure",
			observation: ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth},
			wantOutcome: CoordinatorProductFailure,
		},
		{
			name:        "harness invalid",
			observation: ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeTopology},
			wantOutcome: CoordinatorHarnessInvalid,
		},
		{
			name:                    "product failure observed after caller cancellation",
			observation:             ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth},
			cancelBeforeObservation: true,
			wantOutcome:             CoordinatorProductFailure,
		},
		{
			name:                    "harness invalid observed after caller cancellation",
			observation:             ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeTopology},
			cancelBeforeObservation: true,
			wantOutcome:             CoordinatorHarnessInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := LocalConfig()
			cfg.RunID = "coordinator-observer-locked-" + strings.ReplaceAll(test.name, " ", "-")
			cleanup := newLateCancellationCleanupGate()
			clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
			observerStarted := make(chan struct{})
			observerCancelSeen := make(chan struct{})
			observerRelease := make(chan struct{})
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			for workerID := range workers {
				workers[workerID] = &lateCancellationCoordinatorWorker{
					recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
					cleanup:                    cleanup,
				}
			}
			coordinator, err := NewCoordinator(CoordinatorOptions{
				Generation: 40,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
				}),
				Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
				Workers: workers,
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					close(observerStarted)
					if test.cancelBeforeObservation {
						<-ctx.Done()
						close(observerCancelSeen)
						<-observerRelease
					}
					return test.observation
				}),
				Clock:          clock,
				CleanupTimeout: time.Second,
			})
			if err != nil {
				t.Fatalf("NewCoordinator() error = %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resultChannel := make(chan CoordinatorResult, 1)
			go func() { resultChannel <- coordinator.Run(ctx, cfg) }()
			if test.cancelBeforeObservation {
				waitForCoordinatorSignal(t, observerStarted, "observer start")
				for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
					<-clock.created
				}
				cancel()
				waitForCoordinatorSignal(t, observerCancelSeen, "observer cancellation")
				close(observerRelease)
			}
			waitForCoordinatorSignal(t, cleanup.entered, "observer failure cleanup")
			if !test.cancelBeforeObservation {
				cancel()
			}
			close(cleanup.release)

			select {
			case result := <-resultChannel:
				if result.Outcome != test.wantOutcome || result.Code != CoordinatorCodeObserver {
					t.Fatalf("Run() = %+v, want %s/observer", result, test.wantOutcome)
				}
			case <-time.After(time.Second):
				t.Fatal("Run() did not finish after cleanup release")
			}
		})
	}
}

func TestCoordinatorStageFailureBeforeOuterCancellationKeepsStageFailure(t *testing.T) {
	for _, stage := range []CoordinatorCode{
		CoordinatorCodeAssignment,
		CoordinatorCodeStart,
		CoordinatorCodeCheckpoint,
	} {
		t.Run(string(stage), func(t *testing.T) {
			cfg := LocalConfig()
			cfg.RunID = "coordinator-failure-before-cancel-" + string(stage)
			cfg.Observation.Cadence = 500 * time.Millisecond
			cfg.Thresholds.Timeline = TimelineThresholds{
				Warmup: 100 * time.Millisecond, Checkpoint: time.Second, Final: 1500 * time.Millisecond,
			}
			gate := newCoordinatorFailureCancellationGate()
			log := []string{}
			workers := make([]CoordinatorWorker, coordinatorWorkerCount)
			for workerID := range workers {
				workers[workerID] = &failureThenCancellationCoordinatorWorker{
					recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &log},
					stage:                      stage,
					gate:                       gate,
				}
			}
			observerStarted := make(chan struct{})
			options := CoordinatorOptions{
				Generation: 37,
				Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
					return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
				}),
				Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
				Workers: workers,
				Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
					close(observerStarted)
					<-ctx.Done()
					return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
				}),
				RoundTimeout: time.Second,
			}
			var clock *manualCoordinatorClock
			if stage == CoordinatorCodeCheckpoint {
				clock = newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
				options.Clock = clock
			}
			coordinator, err := NewCoordinator(options)
			if err != nil {
				t.Fatalf("NewCoordinator() error = %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			resultChannel := make(chan CoordinatorResult, 1)
			go func() { resultChannel <- coordinator.Run(ctx, cfg) }()
			if stage == CoordinatorCodeCheckpoint {
				<-observerStarted
				for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
					<-clock.created
				}
				clock.advance(cfg.Thresholds.Timeline.Final)
			}
			<-gate.failureReturned
			<-gate.otherWorkersBlocked
			cancel()

			select {
			case result := <-resultChannel:
				if result.Outcome != CoordinatorHarnessInvalid || result.Code != stage {
					t.Fatalf("Run() failure then cancellation during %s = %+v, want harness_invalid/%s", stage, result, stage)
				}
			case <-time.After(time.Second):
				t.Fatalf("Run() did not preserve failure before cancellation during %s", stage)
			}
		})
	}
}

func TestCoordinatorStageEvidenceSurvivesCancelBeforeResultSampling(t *testing.T) {
	for _, invalidResponse := range []bool{false, true} {
		evidenceName := "ordinary_error"
		if invalidResponse {
			evidenceName = "invalid_response"
		}
		for _, stage := range []CoordinatorCode{
			CoordinatorCodeAssignment,
			CoordinatorCodeStart,
			CoordinatorCodeCheckpoint,
		} {
			t.Run(evidenceName+"_"+string(stage), func(t *testing.T) {
				cfg := LocalConfig()
				cfg.RunID = "coordinator-cancel-before-sample-" + evidenceName + "-" + string(stage)
				cfg.Observation.Cadence = 500 * time.Millisecond
				cfg.Thresholds.Timeline = TimelineThresholds{
					Warmup: 100 * time.Millisecond, Checkpoint: time.Second, Final: 1500 * time.Millisecond,
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cancelOnce := &sync.Once{}
				log := []string{}
				workers := make([]CoordinatorWorker, coordinatorWorkerCount)
				for workerID := range workers {
					workers[workerID] = &cancelBeforeStageEvidenceWorker{
						recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &log},
						stage:                      stage, invalidResponse: invalidResponse, cancel: cancel, cancelOnce: cancelOnce,
					}
				}
				observerStarted := make(chan struct{})
				options := CoordinatorOptions{
					Generation: 38,
					Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
						return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
					}),
					Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
					Workers: workers,
					Observer: coordinatorObserverFunc(func(observerCtx context.Context, _ Config) ObserverResult {
						close(observerStarted)
						<-observerCtx.Done()
						return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
					}),
					RoundTimeout: time.Second,
				}
				var clock *manualCoordinatorClock
				if stage == CoordinatorCodeCheckpoint {
					clock = newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
					options.Clock = clock
				}
				coordinator, err := NewCoordinator(options)
				if err != nil {
					t.Fatalf("NewCoordinator() error = %v", err)
				}
				resultChannel := make(chan CoordinatorResult, 1)
				go func() { resultChannel <- coordinator.Run(ctx, cfg) }()
				if stage == CoordinatorCodeCheckpoint {
					<-observerStarted
					for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
						<-clock.created
					}
					clock.advance(cfg.Thresholds.Timeline.Final)
				}

				select {
				case result := <-resultChannel:
					if result.Outcome != CoordinatorHarnessInvalid || result.Code != stage {
						t.Fatalf("Run() %s racing cancellation during %s = %+v, want harness_invalid/%s", evidenceName, stage, result, stage)
					}
				case <-time.After(time.Second):
					t.Fatalf("Run() did not retain %s during %s", evidenceName, stage)
				}
			})
		}
	}
}

func TestBlockingGrantProbeDetectsSequentialDispatch(t *testing.T) {
	probe := &grantRoundProbe{}
	workers := make([]*blockingGrantCoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &blockingGrantCoordinatorWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
			probe:                      probe,
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	for _, worker := range workers {
		_, _ = worker.Grant(ctx, WorkerGrantRequest{})
	}
	if !probe.returnedBeforeEveryWorkerEntered() {
		t.Fatal("blocking grant probe did not detect deliberately sequential dispatch")
	}
}

func TestCoordinatorGrantRoundUsesOneBoundedConcurrentDeadline(t *testing.T) {
	const roundTimeout = 80 * time.Millisecond
	cfg := LocalConfig()
	cfg.RunID = "coordinator-grant-round-deadline"
	probe := &grantRoundProbe{}
	grantEntered := make(chan coordinatorGrantContext, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &blockingGrantCoordinatorWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
			probe:                      probe,
			entered:                    grantEntered,
		}
	}
	observerEntered := make(chan struct{}, 1)
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 29,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			observerEntered <- struct{}{}
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		RoundTimeout: roundTimeout,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}

	started := time.Now()
	result := coordinator.Run(context.Background(), cfg)
	elapsed := time.Since(started)
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant {
		t.Fatalf("Run() result = %+v, want harness_invalid/grant", result)
	}
	if elapsed < roundTimeout/2 || elapsed >= 200*time.Millisecond {
		t.Fatalf("grant round elapsed = %s, want about one %s shared deadline and less than three sequential deadlines", elapsed, roundTimeout)
	}
	if probe.returnedBeforeEveryWorkerEntered() {
		t.Fatal("a grant call returned before all fixed workers entered")
	}
	select {
	case <-observerEntered:
	default:
		t.Fatal("observer did not start before the initial grant round")
	}

	contexts := make([]coordinatorGrantContext, coordinatorWorkerCount)
	seen := [coordinatorWorkerCount]bool{}
	for attempt := 0; attempt < coordinatorWorkerCount; attempt++ {
		observed := <-grantEntered
		if observed.workerID >= coordinatorWorkerCount || seen[observed.workerID] {
			t.Fatalf("grant worker attempts contain invalid or duplicate worker %d", observed.workerID)
		}
		seen[observed.workerID] = true
		contexts[observed.workerID] = observed
	}
	for workerID, observed := range contexts {
		if !observed.hasDeadline {
			t.Fatalf("worker %d grant context has no deadline", workerID)
		}
		if workerID > 0 && !observed.deadline.Equal(contexts[0].deadline) {
			t.Fatalf("grant deadlines differ: %v versus %v", observed.deadline, contexts[0].deadline)
		}
		if workerID > 0 && observed.request != contexts[0].request {
			t.Fatalf("worker %d grant request differs: %+v versus %+v", workerID, observed.request, contexts[0].request)
		}
	}
}

func TestCoordinatorGrantRoundRejectsLateSuccessWithinOneCadence(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-grant-late-success"
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &lateSuccessGrantCoordinatorWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}},
			lateAfter:                  2 * time.Second,
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 30,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	result := coordinator.Run(ctx, cfg)
	elapsed := time.Since(started)
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant {
		t.Fatalf("Run() late grant result = %+v, want harness_invalid/grant", result)
	}
	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("Run() late grant elapsed = %s, want fail-closed within one-second cadence", elapsed)
	}
}

func TestCoordinatorRejectsStaleGrantTickerWithoutAdvancingGrant(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-stale-grant-tick"
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	observerStarted := make(chan struct{})
	typedWorkers := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		typedWorkers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}}
		workers[workerID] = typedWorkers[workerID]
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 31,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(ctx, cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	grantTicker := clock.ticker(time.Second)
	if grantTicker == nil {
		t.Fatal("coordinator did not create the one-second grant ticker")
	}
	grantTicker.ticks <- clock.Now().Add(-2 * time.Second)
	result := <-resultChannel
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant {
		t.Fatalf("Run() stale tick result = %+v, want harness_invalid/grant", result)
	}
	for workerID, worker := range typedWorkers {
		if len(worker.grantRequests) != 1 {
			t.Fatalf("worker %d grant requests = %d, want only initial grant", workerID, len(worker.grantRequests))
		}
	}
}

func TestValidCoordinatorGrantTickRejectsUnscheduledOrStaleTimestamps(t *testing.T) {
	startedAt := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name         string
		now          time.Time
		tickAt       time.Time
		lastTickAt   time.Time
		haveLastTick bool
		want         bool
	}{
		{name: "zero tick", now: startedAt.Add(time.Second), lastTickAt: startedAt},
		{name: "future tick", now: startedAt, tickAt: startedAt.Add(time.Nanosecond), lastTickAt: startedAt},
		{name: "exactly one cadence stale", now: startedAt.Add(2 * time.Second), tickAt: startedAt.Add(time.Second), lastTickAt: startedAt},
		{name: "first tick too early", now: startedAt.Add(time.Second), tickAt: startedAt.Add(time.Second - time.Nanosecond), lastTickAt: startedAt},
		{name: "first scheduled tick", now: startedAt.Add(time.Second), tickAt: startedAt.Add(time.Second), lastTickAt: startedAt, want: true},
		{name: "next exact scheduled tick", now: startedAt.Add(2 * time.Second), tickAt: startedAt.Add(2 * time.Second), lastTickAt: startedAt.Add(time.Second), haveLastTick: true, want: true},
		{name: "next tick skips cadence", now: startedAt.Add(2500 * time.Millisecond), tickAt: startedAt.Add(2500 * time.Millisecond), lastTickAt: startedAt.Add(time.Second), haveLastTick: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := validCoordinatorGrantTick(testCase.now, testCase.tickAt, testCase.lastTickAt, testCase.haveLastTick); got != testCase.want {
				t.Fatalf("validCoordinatorGrantTick(%s, %s, %s, %v) = %v, want %v", testCase.now, testCase.tickAt, testCase.lastTickAt, testCase.haveLastTick, got, testCase.want)
			}
		})
	}
}

func TestCoordinatorFinalCutoffCannotBypassQueuedStaleGrant(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-cutoff-stale-grant"
	cfg.Observation.Cadence = 500 * time.Millisecond
	cfg.Thresholds.Timeline = TimelineThresholds{
		Warmup: 100 * time.Millisecond, Checkpoint: time.Second, Final: 2500 * time.Millisecond,
	}
	startedAt := time.Unix(1_700_000_000, 0)
	clock := newManualCoordinatorClock(startedAt)
	observerStarted := make(chan struct{})
	typedWorkers := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		typedWorkers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}}
		workers[workerID] = typedWorkers[workerID]
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 36,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	clock.setNowAndQueue(
		startedAt.Add(cfg.Thresholds.Timeline.Final),
		map[time.Duration]time.Time{
			cfg.Observation.Cadence: startedAt.Add(cfg.Thresholds.Timeline.Final),
			coordinatorGrantCadence: startedAt.Add(coordinatorGrantCadence),
		},
	)
	result := <-resultChannel
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant {
		t.Fatalf("Run() queued stale cutoff result = %+v, want harness_invalid/grant", result)
	}
	for workerID, worker := range typedWorkers {
		if len(worker.grantRequests) != 1 {
			t.Fatalf("worker %d grant requests = %d, want only initial sequence", workerID, len(worker.grantRequests))
		}
	}
}

func TestCoordinatorScheduledGrantsKeepExactFixedThreeWorkerVectors(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-scheduled-grant-vectors"
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	observerStarted := make(chan struct{})
	grantCalls := make(chan uint64, coordinatorWorkerCount)
	typedWorkers := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		typedWorkers[workerID] = &recordingCoordinatorWorker{
			id: uint64(workerID), log: &[]string{}, grantCalled: grantCalls, grantFailSequence: 4,
		}
		workers[workerID] = typedWorkers[workerID]
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 37,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			close(observerStarted)
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	for sequence := uint64(1); sequence <= 4; sequence++ {
		if sequence > 1 {
			clock.advance(coordinatorGrantCadence)
		}
		for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
			got := <-grantCalls
			if got != sequence {
				t.Fatalf("grant notification sequence = %d, want %d", got, sequence)
			}
		}
	}
	result := <-resultChannel
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeGrant {
		t.Fatalf("Run() terminal grant result = %+v, want harness_invalid/grant", result)
	}
	for sequenceIndex := 0; sequenceIndex < 4; sequenceIndex++ {
		common := typedWorkers[0].grantRequests[sequenceIndex]
		if common.Sequence != uint64(sequenceIndex+1) {
			t.Fatalf("grant request sequence = %d, want %d", common.Sequence, sequenceIndex+1)
		}
		released, ok := sumWorkerGrantCounts(common.Released)
		if !ok || released != uint64(cfg.Workload.SendRatePerSecond) {
			t.Fatalf("sequence %d released total = %d/%v, want %d", common.Sequence, released, ok, cfg.Workload.SendRatePerSecond)
		}
		for workerID, worker := range typedWorkers {
			if len(worker.grantRequests) != 4 {
				t.Fatalf("worker %d grant requests = %d, want 4", workerID, len(worker.grantRequests))
			}
			if worker.grantRequests[sequenceIndex] != common {
				t.Fatalf("sequence %d worker %d vector differs: %+v versus %+v", common.Sequence, workerID, worker.grantRequests[sequenceIndex], common)
			}
		}
	}
}

func TestCoordinatorCleanupUsesOneConcurrentTotalDeadline(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-concurrent-cleanup"
	stopEntered := make(chan uint64, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		base := &recordingCoordinatorWorker{id: uint64(workerID), log: &[]string{}}
		if workerID == coordinatorWorkerCount-1 {
			base.startErr = errors.New("injected start failure")
		}
		workers[workerID] = &blockingStopCoordinatorWorker{
			recordingCoordinatorWorker: base,
			entered:                    stopEntered,
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 24,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:          coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers:        workers,
		Observer:       coordinatorObserverFunc(func(context.Context, Config) ObserverResult { return ObserverResult{} }),
		CleanupTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	started := time.Now()
	result := coordinator.Run(context.Background(), cfg)
	elapsed := time.Since(started)
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeStart {
		t.Fatalf("Run() result = %+v, want harness_invalid/start", result)
	}
	if got := len(stopEntered); got != coordinatorWorkerCount {
		t.Fatalf("stop attempts = %d, want all 3", got)
	}
	if elapsed >= 120*time.Millisecond {
		t.Fatalf("cleanup elapsed = %s, want one 50ms total deadline rather than three sequential deadlines", elapsed)
	}
}

type coordinatorStatusContext struct {
	workerID    uint64
	deadline    time.Time
	hasDeadline bool
}

type coordinatorGrantContext struct {
	workerID    uint64
	request     WorkerGrantRequest
	deadline    time.Time
	hasDeadline bool
}

type coordinatorStageContext struct {
	workerID    uint64
	deadline    time.Time
	hasDeadline bool
}

type coordinatorStageProbe struct {
	mu                       sync.Mutex
	entered                  int
	returnedBeforeAllEntered bool
	contexts                 chan coordinatorStageContext
}

func newCoordinatorStageProbe() *coordinatorStageProbe {
	return &coordinatorStageProbe{contexts: make(chan coordinatorStageContext, coordinatorWorkerCount)}
}

func (p *coordinatorStageProbe) block(ctx context.Context, workerID uint64) {
	deadline, ok := ctx.Deadline()
	p.mu.Lock()
	p.entered++
	p.mu.Unlock()
	p.contexts <- coordinatorStageContext{workerID: workerID, deadline: deadline, hasDeadline: ok}
	<-ctx.Done()
	p.mu.Lock()
	if p.entered != coordinatorWorkerCount {
		p.returnedBeforeAllEntered = true
	}
	p.mu.Unlock()
}

func assertCoordinatorStageProbe(t *testing.T, probe *coordinatorStageProbe, started time.Time, maximumDeadline time.Duration) {
	t.Helper()
	contexts := make([]coordinatorStageContext, coordinatorWorkerCount)
	seen := [coordinatorWorkerCount]bool{}
	for attempt := 0; attempt < coordinatorWorkerCount; attempt++ {
		observed := <-probe.contexts
		if observed.workerID >= coordinatorWorkerCount || seen[observed.workerID] {
			t.Fatalf("stage worker attempts contain invalid or duplicate worker %d", observed.workerID)
		}
		seen[observed.workerID] = true
		contexts[observed.workerID] = observed
	}
	probe.mu.Lock()
	returnedBeforeAll := probe.returnedBeforeAllEntered
	probe.mu.Unlock()
	if returnedBeforeAll {
		t.Fatal("a stage call returned before all fixed workers entered")
	}
	for workerID, observed := range contexts {
		if !observed.hasDeadline {
			t.Fatalf("worker %d stage context has no deadline", workerID)
		}
		if observed.deadline.Sub(started) > maximumDeadline {
			t.Fatalf("worker %d stage deadline = %s after start, want <= %s", workerID, observed.deadline.Sub(started), maximumDeadline)
		}
		if workerID > 0 && !observed.deadline.Equal(contexts[0].deadline) {
			t.Fatalf("stage deadlines differ: %v versus %v", observed.deadline, contexts[0].deadline)
		}
	}
}

type blockingCoordinatorStageWorker struct {
	*recordingCoordinatorWorker
	stage CoordinatorCode
	probe *coordinatorStageProbe
}

type coordinatorCancellationGate struct {
	mu         sync.Mutex
	entered    int
	allEntered chan struct{}
}

type coordinatorFailureCancellationGate struct {
	mu                  sync.Mutex
	blocked             int
	failureReturned     chan struct{}
	otherWorkersBlocked chan struct{}
}

func newCoordinatorFailureCancellationGate() *coordinatorFailureCancellationGate {
	return &coordinatorFailureCancellationGate{
		failureReturned:     make(chan struct{}),
		otherWorkersBlocked: make(chan struct{}),
	}
}

func (g *coordinatorFailureCancellationGate) blockUntilCancellation(ctx context.Context) {
	g.mu.Lock()
	g.blocked++
	if g.blocked == coordinatorWorkerCount-1 {
		close(g.otherWorkersBlocked)
	}
	g.mu.Unlock()
	<-ctx.Done()
}

type failureThenCancellationCoordinatorWorker struct {
	*recordingCoordinatorWorker
	stage CoordinatorCode
	gate  *coordinatorFailureCancellationGate
}

type lateCancellationRoundStage uint8

const (
	lateCancellationNoRound lateCancellationRoundStage = iota
	lateCancellationGrant
	lateCancellationStatus
)

type lateCancellationRoundEvidence uint8

const (
	lateCancellationOrdinaryError lateCancellationRoundEvidence = iota
	lateCancellationInvalidResponse
	lateCancellationOwnDeadline
	lateCancellationParentCancel
)

type lateCancellationCleanupGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newLateCancellationCleanupGate() *lateCancellationCleanupGate {
	return &lateCancellationCleanupGate{entered: make(chan struct{}), release: make(chan struct{})}
}

type lateCancellationCoordinatorWorker struct {
	*recordingCoordinatorWorker
	stage            lateCancellationRoundStage
	evidence         lateCancellationRoundEvidence
	roundEntered     chan struct{}
	roundEnteredOnce sync.Once
	cleanup          *lateCancellationCleanupGate
}

func (w *lateCancellationCoordinatorWorker) signalRoundEntered() {
	if w.roundEntered == nil {
		return
	}
	w.roundEnteredOnce.Do(func() { close(w.roundEntered) })
}

func (w *lateCancellationCoordinatorWorker) Grant(ctx context.Context, request WorkerGrantRequest) (WorkerGrantResponse, error) {
	if w.stage != lateCancellationGrant || w.id != 0 {
		return w.recordingCoordinatorWorker.Grant(ctx, request)
	}
	w.signalRoundEntered()
	switch w.evidence {
	case lateCancellationOrdinaryError:
		return WorkerGrantResponse{}, errors.New("injected ordinary grant failure")
	case lateCancellationInvalidResponse:
		return WorkerGrantResponse{}, nil
	case lateCancellationOwnDeadline, lateCancellationParentCancel:
		<-ctx.Done()
		return WorkerGrantResponse{}, ctx.Err()
	default:
		return w.recordingCoordinatorWorker.Grant(ctx, request)
	}
}

func (w *lateCancellationCoordinatorWorker) Status(ctx context.Context) (WorkerStatus, error) {
	if w.stage != lateCancellationStatus || w.id != 0 {
		return w.recordingCoordinatorWorker.Status(ctx)
	}
	w.signalRoundEntered()
	switch w.evidence {
	case lateCancellationOrdinaryError:
		return WorkerStatus{}, errors.New("injected ordinary status failure")
	case lateCancellationInvalidResponse:
		return WorkerStatus{}, nil
	case lateCancellationOwnDeadline, lateCancellationParentCancel:
		<-ctx.Done()
		return WorkerStatus{}, ctx.Err()
	default:
		return w.recordingCoordinatorWorker.Status(ctx)
	}
}

func (w *lateCancellationCoordinatorWorker) Stop(ctx context.Context, request WorkerStopRequest) (WorkerSnapshot, error) {
	if w.cleanup != nil {
		w.cleanup.once.Do(func() { close(w.cleanup.entered) })
		<-w.cleanup.release
	}
	return w.recordingCoordinatorWorker.Stop(ctx, request)
}

func waitForCoordinatorSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

type cancelBeforeStageEvidenceWorker struct {
	*recordingCoordinatorWorker
	stage           CoordinatorCode
	invalidResponse bool
	cancel          context.CancelFunc
	cancelOnce      *sync.Once
}

func (w *cancelBeforeStageEvidenceWorker) resultAfterCancellation(ctx context.Context) error {
	if w.id != 0 {
		<-ctx.Done()
		return nil
	}
	w.cancelOnce.Do(w.cancel)
	if w.invalidResponse {
		return nil
	}
	return errors.New("injected ordinary stage error")
}

func (w *cancelBeforeStageEvidenceWorker) Assign(ctx context.Context, assignment WorkerAssignment) (WorkerStatus, error) {
	if w.stage != CoordinatorCodeAssignment {
		return w.recordingCoordinatorWorker.Assign(ctx, assignment)
	}
	appendCoordinatorTestLog(w.log, "assign-"+strconv.FormatUint(w.id, 10))
	w.attempted, w.assignment = assignment, assignment
	err := w.resultAfterCancellation(ctx)
	if w.id == 0 {
		return WorkerStatus{}, err
	}
	return w.status(WorkerPhaseAssigned), err
}

func (w *cancelBeforeStageEvidenceWorker) Start(ctx context.Context, request WorkerStartRequest) (WorkerStatus, error) {
	if w.stage != CoordinatorCodeStart {
		return w.recordingCoordinatorWorker.Start(ctx, request)
	}
	appendCoordinatorTestLog(w.log, "start-"+strconv.FormatUint(w.id, 10))
	err := w.resultAfterCancellation(ctx)
	if w.id == 0 {
		return WorkerStatus{}, err
	}
	return w.status(WorkerPhaseRunning), err
}

func (w *cancelBeforeStageEvidenceWorker) Checkpoint(ctx context.Context, request WorkerCheckpointRequest) (WorkerSnapshot, error) {
	if w.stage != CoordinatorCodeCheckpoint {
		return w.recordingCoordinatorWorker.Checkpoint(ctx, request)
	}
	appendCoordinatorTestLog(w.log, "checkpoint-"+strconv.FormatUint(w.id, 10))
	err := w.resultAfterCancellation(ctx)
	if w.id == 0 {
		return WorkerSnapshot{}, err
	}
	w.sequence++
	return w.snapshot(WorkerPhaseRunning), err
}

func (w *failureThenCancellationCoordinatorWorker) stageResult(ctx context.Context) error {
	if w.id == 0 {
		defer close(w.gate.failureReturned)
		return errors.New("injected stage failure before cancellation")
	}
	w.gate.blockUntilCancellation(ctx)
	return nil
}

func (w *failureThenCancellationCoordinatorWorker) Assign(ctx context.Context, assignment WorkerAssignment) (WorkerStatus, error) {
	if w.stage != CoordinatorCodeAssignment {
		return w.recordingCoordinatorWorker.Assign(ctx, assignment)
	}
	appendCoordinatorTestLog(w.log, "assign-"+strconv.FormatUint(w.id, 10))
	w.attempted, w.assignment = assignment, assignment
	if err := w.stageResult(ctx); err != nil {
		return WorkerStatus{}, err
	}
	return w.status(WorkerPhaseAssigned), nil
}

func (w *failureThenCancellationCoordinatorWorker) Start(ctx context.Context, request WorkerStartRequest) (WorkerStatus, error) {
	if w.stage != CoordinatorCodeStart {
		return w.recordingCoordinatorWorker.Start(ctx, request)
	}
	appendCoordinatorTestLog(w.log, "start-"+strconv.FormatUint(w.id, 10))
	if err := w.stageResult(ctx); err != nil {
		return WorkerStatus{}, err
	}
	return w.status(WorkerPhaseRunning), nil
}

func (w *failureThenCancellationCoordinatorWorker) Checkpoint(ctx context.Context, request WorkerCheckpointRequest) (WorkerSnapshot, error) {
	if w.stage != CoordinatorCodeCheckpoint {
		return w.recordingCoordinatorWorker.Checkpoint(ctx, request)
	}
	appendCoordinatorTestLog(w.log, "checkpoint-"+strconv.FormatUint(w.id, 10))
	if err := w.stageResult(ctx); err != nil {
		return WorkerSnapshot{}, err
	}
	w.sequence++
	return w.snapshot(WorkerPhaseRunning), nil
}

func newCoordinatorCancellationGate() *coordinatorCancellationGate {
	return &coordinatorCancellationGate{allEntered: make(chan struct{})}
}

func (g *coordinatorCancellationGate) enter() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entered++
	if g.entered == coordinatorWorkerCount {
		close(g.allEntered)
	}
}

type cancelingCoordinatorStageWorker struct {
	*recordingCoordinatorWorker
	stage CoordinatorCode
	gate  *coordinatorCancellationGate
}

func (w *cancelingCoordinatorStageWorker) waitForCancellation(ctx context.Context) {
	w.gate.enter()
	<-ctx.Done()
}

func (w *cancelingCoordinatorStageWorker) Assign(ctx context.Context, assignment WorkerAssignment) (WorkerStatus, error) {
	if w.stage != CoordinatorCodeAssignment {
		return w.recordingCoordinatorWorker.Assign(ctx, assignment)
	}
	appendCoordinatorTestLog(w.log, "assign-"+strconv.FormatUint(w.id, 10))
	w.attempted, w.assignment = assignment, assignment
	w.waitForCancellation(ctx)
	return w.status(WorkerPhaseAssigned), nil
}

func (w *cancelingCoordinatorStageWorker) Start(ctx context.Context, request WorkerStartRequest) (WorkerStatus, error) {
	if w.stage != CoordinatorCodeStart {
		return w.recordingCoordinatorWorker.Start(ctx, request)
	}
	appendCoordinatorTestLog(w.log, "start-"+strconv.FormatUint(w.id, 10))
	w.waitForCancellation(ctx)
	return w.status(WorkerPhaseRunning), nil
}

func (w *cancelingCoordinatorStageWorker) Checkpoint(ctx context.Context, request WorkerCheckpointRequest) (WorkerSnapshot, error) {
	if w.stage != CoordinatorCodeCheckpoint {
		return w.recordingCoordinatorWorker.Checkpoint(ctx, request)
	}
	appendCoordinatorTestLog(w.log, "checkpoint-"+strconv.FormatUint(w.id, 10))
	w.waitForCancellation(ctx)
	w.sequence++
	return w.snapshot(WorkerPhaseRunning), nil
}

func (w *blockingCoordinatorStageWorker) Assign(ctx context.Context, assignment WorkerAssignment) (WorkerStatus, error) {
	w.attempted, w.assignment = assignment, assignment
	if w.stage == CoordinatorCodeAssignment {
		w.probe.block(ctx, w.id)
		return w.status(WorkerPhaseAssigned), nil
	}
	return w.recordingCoordinatorWorker.Assign(ctx, assignment)
}

func (w *blockingCoordinatorStageWorker) Start(ctx context.Context, request WorkerStartRequest) (WorkerStatus, error) {
	if w.stage == CoordinatorCodeStart {
		w.probe.block(ctx, w.id)
		return w.status(WorkerPhaseRunning), nil
	}
	return w.recordingCoordinatorWorker.Start(ctx, request)
}

func (w *blockingCoordinatorStageWorker) Checkpoint(ctx context.Context, _ WorkerCheckpointRequest) (WorkerSnapshot, error) {
	if w.stage == CoordinatorCodeCheckpoint {
		w.probe.block(ctx, w.id)
		w.sequence++
		return w.snapshot(WorkerPhaseRunning), nil
	}
	return w.recordingCoordinatorWorker.Checkpoint(ctx, WorkerCheckpointRequest{WorkerFence: w.assignment.WorkerFence})
}

type grantRoundProbe struct {
	mu                       sync.Mutex
	entered                  int
	returnedBeforeAllEntered bool
}

func (p *grantRoundProbe) recordEntry() {
	p.mu.Lock()
	p.entered++
	p.mu.Unlock()
}

func (p *grantRoundProbe) recordReturn() {
	p.mu.Lock()
	if p.entered != coordinatorWorkerCount {
		p.returnedBeforeAllEntered = true
	}
	p.mu.Unlock()
}

func (p *grantRoundProbe) returnedBeforeEveryWorkerEntered() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.returnedBeforeAllEntered
}

type blockingGrantCoordinatorWorker struct {
	*recordingCoordinatorWorker
	probe   *grantRoundProbe
	entered chan<- coordinatorGrantContext
}

type lateSuccessGrantCoordinatorWorker struct {
	*recordingCoordinatorWorker
	lateAfter time.Duration
}

func (w *lateSuccessGrantCoordinatorWorker) Grant(ctx context.Context, request WorkerGrantRequest) (WorkerGrantResponse, error) {
	timer := time.NewTimer(w.lateAfter)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	released, _ := request.Released.worker(w.id)
	return WorkerGrantResponse{
		WorkerFence: request.WorkerFence, WorkerID: w.id, WorkerCount: coordinatorWorkerCount,
		Sequence: request.Sequence, Released: released,
	}, nil
}

func (w *blockingGrantCoordinatorWorker) Grant(ctx context.Context, request WorkerGrantRequest) (WorkerGrantResponse, error) {
	deadline, ok := ctx.Deadline()
	w.probe.recordEntry()
	if w.entered != nil {
		w.entered <- coordinatorGrantContext{
			workerID: w.id, request: request, deadline: deadline, hasDeadline: ok,
		}
	}
	<-ctx.Done()
	w.probe.recordReturn()
	return WorkerGrantResponse{}, ctx.Err()
}

type deadlineCoordinatorWorker struct {
	*recordingCoordinatorWorker
	entered chan<- coordinatorStatusContext
}

type staggeredReadyCoordinatorWorker struct {
	*recordingCoordinatorWorker
	readyAfter  int
	statusCount int
	statusCalls chan<- uint64
}

func (w *staggeredReadyCoordinatorWorker) Start(ctx context.Context, request WorkerStartRequest) (WorkerStatus, error) {
	status, err := w.recordingCoordinatorWorker.Start(ctx, request)
	status.TrafficReady = false
	return status, err
}

func (w *staggeredReadyCoordinatorWorker) Status(context.Context) (WorkerStatus, error) {
	w.statusCount++
	w.statusCalls <- w.id
	status := w.status(WorkerPhaseRunning)
	status.TrafficReady = w.statusCount >= w.readyAfter
	return status, nil
}

func (w *deadlineCoordinatorWorker) Status(ctx context.Context) (WorkerStatus, error) {
	deadline, ok := ctx.Deadline()
	w.entered <- coordinatorStatusContext{workerID: w.id, deadline: deadline, hasDeadline: ok}
	if !ok {
		return WorkerStatus{}, errors.New("status context is unbounded")
	}
	<-ctx.Done()
	return WorkerStatus{}, ctx.Err()
}

type blockingStopCoordinatorWorker struct {
	*recordingCoordinatorWorker
	entered chan<- uint64
}

func (w *blockingStopCoordinatorWorker) Stop(ctx context.Context, _ WorkerStopRequest) (WorkerSnapshot, error) {
	w.entered <- w.id
	<-ctx.Done()
	return WorkerSnapshot{}, ctx.Err()
}

type fixedCoordinatorClock struct {
	now    time.Time
	ticker ObserverTicker
}

type grantBarrierCoordinatorClock struct {
	now     time.Time
	reached chan struct{}
	once    sync.Once
}

func newGrantBarrierCoordinatorClock(now time.Time) *grantBarrierCoordinatorClock {
	return &grantBarrierCoordinatorClock{now: now, reached: make(chan struct{})}
}

func (c *grantBarrierCoordinatorClock) Now() time.Time {
	c.once.Do(func() { close(c.reached) })
	return c.now
}

func (c *grantBarrierCoordinatorClock) NewTicker(time.Duration) ObserverTicker {
	return newFakeObserverTicker()
}

func (c fixedCoordinatorClock) Now() time.Time { return c.now }
func (c fixedCoordinatorClock) NewTicker(period time.Duration) ObserverTicker {
	if period == time.Second {
		return newFakeObserverTicker()
	}
	return c.ticker
}

type manualCoordinatorClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*manualCoordinatorTicker
	created chan time.Duration
}

type manualCoordinatorTicker struct {
	period time.Duration
	next   time.Time
	ticker *fakeObserverTicker
}

func newManualCoordinatorClock(now time.Time) *manualCoordinatorClock {
	return &manualCoordinatorClock{
		now: now, created: make(chan time.Duration, 8),
	}
}

func (c *manualCoordinatorClock) ticker(period time.Duration) *fakeObserverTicker {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index := len(c.tickers) - 1; index >= 0; index-- {
		state := c.tickers[index]
		if state.period != period {
			continue
		}
		select {
		case <-state.ticker.stopped:
			continue
		default:
			return state.ticker
		}
	}
	return nil
}

func TestManualCoordinatorClockAdvanceDoesNotBlockOnBackloggedTicker(t *testing.T) {
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	clock.NewTicker(time.Second)
	clock.advance(time.Second)

	done := make(chan struct{})
	go func() {
		clock.advance(time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("manual clock blocked instead of dropping a backlogged ticker event")
	}
}

func (c *manualCoordinatorClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualCoordinatorClock) NewTicker(period time.Duration) ObserverTicker {
	c.mu.Lock()
	defer c.mu.Unlock()
	ticker := newFakeObserverTicker()
	c.tickers = append(c.tickers, &manualCoordinatorTicker{
		period: period, next: c.now.Add(period), ticker: ticker,
	})
	c.created <- period
	return ticker
}

func (c *manualCoordinatorClock) advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	now := c.now
	type dueTick struct {
		ticker *fakeObserverTicker
		at     time.Time
	}
	due := make([]dueTick, 0, len(c.tickers))
	for _, state := range c.tickers {
		select {
		case <-state.ticker.stopped:
			continue
		default:
		}
		if now.Before(state.next) {
			continue
		}
		due = append(due, dueTick{ticker: state.ticker, at: state.next})
		periods := now.Sub(state.next)/state.period + 1
		state.next = state.next.Add(periods * state.period)
	}
	c.mu.Unlock()
	for _, tick := range due {
		select {
		case tick.ticker.ticks <- tick.at:
		default:
		}
	}
}

func (c *manualCoordinatorClock) setNowAndQueue(now time.Time, ticks map[time.Duration]time.Time) {
	c.mu.Lock()
	c.now = now
	for period, tickAt := range ticks {
		for index := len(c.tickers) - 1; index >= 0; index-- {
			state := c.tickers[index]
			if state.period != period {
				continue
			}
			select {
			case <-state.ticker.stopped:
				continue
			case state.ticker.ticks <- tickAt:
			}
			break
		}
	}
	c.mu.Unlock()
}

type coordinatorPreflightFunc func(context.Context, Config) PreflightResult

func (f coordinatorPreflightFunc) Check(ctx context.Context, cfg Config) PreflightResult {
	return f(ctx, cfg)
}

type coordinatorSetupFunc func(context.Context, Config) error

func (f coordinatorSetupFunc) Run(ctx context.Context, cfg Config) error { return f(ctx, cfg) }

type coordinatorObserverFunc func(context.Context, Config) ObserverResult

func (f coordinatorObserverFunc) Run(ctx context.Context, cfg Config) ObserverResult {
	return f(ctx, cfg)
}

type recordingCoordinatorWorker struct {
	id                           uint64
	log                          *[]string
	attempted                    WorkerAssignment
	assignment                   WorkerAssignment
	sequence                     uint64
	assignErr                    error
	installAssignmentBeforeError bool
	startErr                     error
	statusErr                    error
	rateErr                      error
	stopErr                      error
	fenceMismatch                bool
	statusCalls                  chan<- uint64
	stopSawCanceledContext       bool
	grantResponseLossOnce        bool
	grantFailSequence            uint64
	grantCalled                  chan<- uint64
	grantBarrier                 <-chan struct{}
	grantRequests                []WorkerGrantRequest
	appliedGrantSequences        map[uint64]int
}

func (w *recordingCoordinatorWorker) Assign(_ context.Context, assignment WorkerAssignment) (WorkerStatus, error) {
	appendCoordinatorTestLog(w.log, "assign-"+strconv.FormatUint(w.id, 10))
	w.attempted = assignment
	if w.assignErr != nil {
		if w.installAssignmentBeforeError {
			w.assignment = assignment
		}
		return WorkerStatus{}, w.assignErr
	}
	w.assignment = assignment
	return w.status(WorkerPhaseAssigned), nil
}

func (w *recordingCoordinatorWorker) Start(_ context.Context, request WorkerStartRequest) (WorkerStatus, error) {
	appendCoordinatorTestLog(w.log, "start-"+strconv.FormatUint(w.id, 10))
	if !sameWorkerFence(request.WorkerFence, w.assignment.WorkerFence) {
		w.fenceMismatch = true
	}
	if w.startErr != nil {
		return WorkerStatus{}, w.startErr
	}
	return w.status(WorkerPhaseRunning), nil
}

func (w *recordingCoordinatorWorker) Status(context.Context) (WorkerStatus, error) {
	appendCoordinatorTestLog(w.log, "status-"+strconv.FormatUint(w.id, 10))
	if w.statusCalls != nil {
		w.statusCalls <- w.id
	}
	if w.statusErr != nil {
		return WorkerStatus{}, w.statusErr
	}
	return w.status(WorkerPhaseRunning), nil
}

func (w *recordingCoordinatorWorker) Grant(_ context.Context, request WorkerGrantRequest) (WorkerGrantResponse, error) {
	if w.grantBarrier != nil {
		<-w.grantBarrier
	}
	appendCoordinatorTestLog(w.log, "grant-"+strconv.FormatUint(w.id, 10))
	w.grantRequests = append(w.grantRequests, request)
	if w.grantCalled != nil {
		w.grantCalled <- request.Sequence
	}
	if request.Sequence == w.grantFailSequence {
		return WorkerGrantResponse{}, &WorkerAPIError{Code: WorkerErrorRuntimeFailure, Status: http.StatusUnprocessableEntity}
	}
	if w.rateErr != nil {
		return WorkerGrantResponse{}, w.rateErr
	}
	if w.appliedGrantSequences == nil {
		w.appliedGrantSequences = make(map[uint64]int)
	}
	if w.appliedGrantSequences[request.Sequence] == 0 {
		w.appliedGrantSequences[request.Sequence] = 1
	}
	if w.grantResponseLossOnce {
		w.grantResponseLossOnce = false
		return WorkerGrantResponse{}, errors.New("injected grant response loss")
	}
	released, _ := request.Released.worker(w.id)
	return WorkerGrantResponse{
		WorkerFence: request.WorkerFence, WorkerID: w.id, WorkerCount: coordinatorWorkerCount,
		Sequence: request.Sequence, Released: released,
	}, nil
}

var coordinatorTestLogMu sync.Mutex

func appendCoordinatorTestLog(log *[]string, operation string) {
	coordinatorTestLogMu.Lock()
	*log = append(*log, operation)
	coordinatorTestLogMu.Unlock()
}

func coordinatorTestLogSnapshot(log *[]string) []string {
	coordinatorTestLogMu.Lock()
	defer coordinatorTestLogMu.Unlock()
	return append([]string(nil), (*log)...)
}

func canonicalCoordinatorLog(log *[]string) []string {
	canonical := coordinatorTestLogSnapshot(log)
	for start := 0; start < len(canonical); {
		prefix := coordinatorConcurrentLogPrefix(canonical[start])
		if prefix == "" {
			start++
			continue
		}
		end := start + 1
		for end < len(canonical) && coordinatorConcurrentLogPrefix(canonical[end]) == prefix {
			end++
		}
		sort.Strings(canonical[start:end])
		start = end
	}
	return canonical
}

func coordinatorConcurrentLogPrefix(operation string) string {
	if operation == "observe" || strings.HasPrefix(operation, "grant-") {
		return "grant-observer"
	}
	for _, prefix := range []string{"assign-", "start-", "status-", "checkpoint-", "stop-"} {
		if strings.HasPrefix(operation, prefix) {
			return prefix
		}
	}
	return ""
}

func (w *recordingCoordinatorWorker) UpdateRate(_ context.Context, request WorkerRateRequest) (WorkerStatus, error) {
	appendCoordinatorTestLog(w.log, "rate-"+strconv.FormatUint(w.id, 10))
	if !sameWorkerFence(request.WorkerFence, w.assignment.WorkerFence) ||
		request.RatePerSecond != uint64(w.assignment.Config.Workload.SendRatePerSecond) ||
		request.MaxBurst != uint64(w.assignment.Config.Workload.MaxGlobalBurst) {
		w.fenceMismatch = true
	}
	if w.rateErr != nil {
		return WorkerStatus{}, w.rateErr
	}
	return w.status(WorkerPhaseRunning), nil
}

func (w *recordingCoordinatorWorker) Checkpoint(_ context.Context, request WorkerCheckpointRequest) (WorkerSnapshot, error) {
	appendCoordinatorTestLog(w.log, "checkpoint-"+strconv.FormatUint(w.id, 10))
	if !sameWorkerFence(request.WorkerFence, w.assignment.WorkerFence) {
		w.fenceMismatch = true
	}
	w.sequence++
	return w.snapshot(WorkerPhaseRunning), nil
}

func (w *recordingCoordinatorWorker) Stop(ctx context.Context, request WorkerStopRequest) (WorkerSnapshot, error) {
	appendCoordinatorTestLog(w.log, "stop-"+strconv.FormatUint(w.id, 10))
	if ctx.Err() != nil {
		w.stopSawCanceledContext = true
	}
	if !sameWorkerFence(request.WorkerFence, w.attempted.WorkerFence) {
		w.fenceMismatch = true
	}
	if w.stopErr != nil {
		return WorkerSnapshot{}, w.stopErr
	}
	w.sequence++
	return w.snapshot(WorkerPhaseFinal), nil
}

func (w *recordingCoordinatorWorker) status(phase WorkerPhase) WorkerStatus {
	return WorkerStatus{
		RunID: w.assignment.RunID, AssignmentID: w.assignment.AssignmentID, Phase: phase,
		Generation: w.assignment.Generation, WorkerID: w.id, WorkerCount: coordinatorWorkerCount, TrafficReady: true,
	}
}

func (w *recordingCoordinatorWorker) snapshot(phase WorkerPhase) WorkerSnapshot {
	return WorkerSnapshot{
		RunID: w.assignment.RunID, AssignmentID: w.assignment.AssignmentID, Phase: phase,
		Uptime: time.Duration(w.sequence) * time.Second, SnapshotSequence: w.sequence,
		Generation: w.assignment.Generation, WorkerID: w.id, WorkerCount: coordinatorWorkerCount,
		Sync: WorkerSyncSnapshot{
			ConnectLatency: newWorkerHistogramSnapshot(), Latency: newWorkerHistogramSnapshot(),
		},
		SendackLatency: newWorkerHistogramSnapshot(), RecvackLatency: newWorkerHistogramSnapshot(),
	}
}
