package chatlifecycle

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strconv"
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

func TestCoordinatorStartUsesStrictOrderAndFinalizesExactWorkers(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "coordinator-start-order"
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	observerStarted := make(chan struct{})
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		workers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &log}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 17,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			log = append(log, "preflight")
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error {
			log = append(log, "setup")
			return nil
		}),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			log = append(log, "observe")
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
	clock.advance(30 * time.Minute)
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
		"rate-0", "rate-1", "rate-2",
		"observe",
		"checkpoint-0", "checkpoint-1", "checkpoint-2",
		"stop-0", "stop-1", "stop-2",
	}
	if !reflect.DeepEqual(log, want) {
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
	assertCoordinatorHealthyObservationCompletesOnlyAtFinalCutoff(t, cfg, 30*time.Minute)
}

func TestCoordinatorFormalHealthyObservationCompletesOnlyAtFinalCutoff(t *testing.T) {
	cfg := FormalConfig()
	assertCoordinatorHealthyObservationCompletesOnlyAtFinalCutoff(t, cfg, 72*time.Hour)
}

func assertCoordinatorHealthyObservationCompletesOnlyAtFinalCutoff(t *testing.T, cfg Config, final time.Duration) {
	t.Helper()
	if cfg.Thresholds.Timeline.Final != final {
		t.Fatalf("configured final cutoff = %s, want %s", cfg.Thresholds.Timeline.Final, final)
	}
	cfg.RunID = "coordinator-final-cutoff-" + string(cfg.Profile)
	observerFixture := newObserverFixture(cfg)
	coordinatorClock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	statusCalls := make(chan uint64, coordinatorWorkerCount)
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		workers[workerID] = &recordingCoordinatorWorker{
			id: uint64(workerID), log: &log, statusCalls: statusCalls,
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
	for poll := 0; poll < 2; poll++ {
		observerFixture.clock.advance(cfg.Observation.Cadence)
		observerFixture.waitPoll(t)
	}
	coordinatorClock.advance(final - cfg.Observation.Cadence)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		<-statusCalls
	}
	select {
	case result := <-resultChannel:
		t.Fatalf("Run() completed before final cutoff: %+v", result)
	default:
	}
	for _, operation := range log {
		if operation == "checkpoint-0" || operation == "checkpoint-1" || operation == "checkpoint-2" ||
			operation == "stop-0" || operation == "stop-1" || operation == "stop-2" {
			t.Fatalf("operation %q ran before final cutoff", operation)
		}
	}

	coordinatorClock.advance(cfg.Observation.Cadence)
	var result CoordinatorResult
	statusAfterFinal := 0
	for statusAfterFinal < coordinatorWorkerCount {
		select {
		case result = <-resultChannel:
			statusAfterFinal = coordinatorWorkerCount
		case <-statusCalls:
			statusAfterFinal++
		}
	}
	if result == (CoordinatorResult{}) {
		cancel()
		result = <-resultChannel
	}
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
	clock.advance(30 * time.Minute)
	result := <-resultChannel
	if result.Outcome != CoordinatorProductFailure || result.Code != CoordinatorCodeObserver {
		t.Fatalf("Run() cutoff race = %+v, want product_failure/observer", result)
	}
	wantTail := []string{"stop-0", "stop-1", "stop-2"}
	if len(log) < len(wantTail) || !reflect.DeepEqual(log[len(log)-len(wantTail):], wantTail) {
		t.Fatalf("cleanup tail = %v, want %v", log, wantTail)
	}
	for _, operation := range log {
		if len(operation) >= len("checkpoint-") && operation[:len("checkpoint-")] == "checkpoint-" {
			t.Fatalf("product cutoff race ran %q", operation)
		}
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
	for _, operation := range log {
		if len(operation) >= len("checkpoint-") && operation[:len("checkpoint-")] == "checkpoint-" {
			t.Fatalf("caller cancellation ran %q", operation)
		}
	}
}

func TestCoordinatorStartFailuresFenceTrafficAndBestEffortStop(t *testing.T) {
	errInjected := errors.New("injected coordinator failure")
	tests := []struct {
		name         string
		preflight    PreflightResult
		setupErr     error
		workerChange func([]*recordingCoordinatorWorker)
		observer     ObserverResult
		wantOutcome  CoordinatorOutcome
		wantCode     CoordinatorCode
		wantLog      []string
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
			wantLog: []string{"preflight", "setup", "assign-0", "assign-1", "stop-0", "stop-1"},
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
			wantLog: []string{"preflight", "setup", "assign-0", "assign-1", "stop-0", "stop-1"},
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
				"preflight", "setup", "assign-0", "assign-1", "assign-2", "start-0", "start-1",
				"stop-0", "stop-1", "stop-2",
			},
		},
		{
			name:      "global grant failure stops every assigned worker",
			preflight: PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK},
			workerChange: func(workers []*recordingCoordinatorWorker) {
				workers[1].rateErr = errInjected
			},
			observer:    ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped},
			wantOutcome: CoordinatorHarnessInvalid, wantCode: CoordinatorCodeGrant,
			wantLog: []string{
				"preflight", "setup", "assign-0", "assign-1", "assign-2",
				"start-0", "start-1", "start-2", "rate-0", "rate-1",
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
				"start-0", "start-1", "start-2", "rate-0", "rate-1", "rate-2",
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
				"start-0", "start-1", "start-2", "rate-0", "rate-1", "rate-2",
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
				"start-0", "start-1", "start-2", "rate-0", "rate-1", "rate-2",
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
					log = append(log, "preflight")
					return testCase.preflight
				}),
				Setup: coordinatorSetupFunc(func(context.Context, Config) error {
					log = append(log, "setup")
					return testCase.setupErr
				}),
				Workers: workers,
				Observer: coordinatorObserverFunc(func(context.Context, Config) ObserverResult {
					log = append(log, "observe")
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
			if !reflect.DeepEqual(log, testCase.wantLog) {
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
			log = append(log, "preflight")
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error {
			log = append(log, "setup")
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
	if want := []string{"preflight", "setup", "assign-0", "stop-0"}; !reflect.DeepEqual(log, want) {
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
			log = append(log, "preflight")
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup: coordinatorSetupFunc(func(context.Context, Config) error {
			log = append(log, "setup")
			return nil
		}),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			log = append(log, "observe")
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
		"start-0", "start-1", "start-2", "rate-0", "rate-1", "rate-2",
		"observe", "status-0", "status-1",
		"stop-0", "stop-1", "stop-2",
	}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("call log = %v, want %v", log, want)
	}
}

type fixedCoordinatorClock struct {
	now    time.Time
	ticker ObserverTicker
}

func (c fixedCoordinatorClock) Now() time.Time                         { return c.now }
func (c fixedCoordinatorClock) NewTicker(time.Duration) ObserverTicker { return c.ticker }

type manualCoordinatorClock struct {
	mu     sync.Mutex
	now    time.Time
	ticker *fakeObserverTicker
	period time.Duration
}

func newManualCoordinatorClock(now time.Time) *manualCoordinatorClock {
	return &manualCoordinatorClock{now: now}
}

func (c *manualCoordinatorClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualCoordinatorClock) NewTicker(period time.Duration) ObserverTicker {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.period = period
	c.ticker = newFakeObserverTicker()
	return c.ticker
}

func (c *manualCoordinatorClock) advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	now := c.now
	ticker := c.ticker
	c.mu.Unlock()
	ticker.ticks <- now
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
}

func (w *recordingCoordinatorWorker) Assign(_ context.Context, assignment WorkerAssignment) (WorkerStatus, error) {
	*w.log = append(*w.log, "assign-"+strconv.FormatUint(w.id, 10))
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
	*w.log = append(*w.log, "start-"+strconv.FormatUint(w.id, 10))
	if !sameWorkerFence(request.WorkerFence, w.assignment.WorkerFence) {
		w.fenceMismatch = true
	}
	if w.startErr != nil {
		return WorkerStatus{}, w.startErr
	}
	return w.status(WorkerPhaseRunning), nil
}

func (w *recordingCoordinatorWorker) Status(context.Context) (WorkerStatus, error) {
	*w.log = append(*w.log, "status-"+strconv.FormatUint(w.id, 10))
	if w.statusCalls != nil {
		w.statusCalls <- w.id
	}
	if w.statusErr != nil {
		return WorkerStatus{}, w.statusErr
	}
	return w.status(WorkerPhaseRunning), nil
}

func (w *recordingCoordinatorWorker) UpdateRate(_ context.Context, request WorkerRateRequest) (WorkerStatus, error) {
	*w.log = append(*w.log, "rate-"+strconv.FormatUint(w.id, 10))
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
	*w.log = append(*w.log, "checkpoint-"+strconv.FormatUint(w.id, 10))
	if !sameWorkerFence(request.WorkerFence, w.assignment.WorkerFence) {
		w.fenceMismatch = true
	}
	w.sequence++
	return w.snapshot(WorkerPhaseRunning), nil
}

func (w *recordingCoordinatorWorker) Stop(ctx context.Context, request WorkerStopRequest) (WorkerSnapshot, error) {
	*w.log = append(*w.log, "stop-"+strconv.FormatUint(w.id, 10))
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
		Generation: w.assignment.Generation, WorkerID: w.id, WorkerCount: coordinatorWorkerCount,
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
