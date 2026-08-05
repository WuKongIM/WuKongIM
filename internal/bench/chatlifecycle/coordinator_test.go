package chatlifecycle

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strconv"
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
		Observer: coordinatorObserverFunc(func(context.Context, Config) ObserverResult {
			log = append(log, "observe")
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	result := coordinator.Run(context.Background(), cfg)
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
			name:         "assignment failure stops only already assigned workers",
			preflight:    PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK},
			workerChange: func(workers []*recordingCoordinatorWorker) { workers[1].assignErr = errInjected },
			observer:     ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped},
			wantOutcome:  CoordinatorHarnessInvalid, wantCode: CoordinatorCodeAssignment,
			wantLog: []string{"preflight", "setup", "assign-0", "assign-1", "stop-0"},
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
	id            uint64
	log           *[]string
	assignment    WorkerAssignment
	sequence      uint64
	assignErr     error
	startErr      error
	statusErr     error
	rateErr       error
	stopErr       error
	fenceMismatch bool
}

func (w *recordingCoordinatorWorker) Assign(_ context.Context, assignment WorkerAssignment) (WorkerStatus, error) {
	*w.log = append(*w.log, "assign-"+strconv.FormatUint(w.id, 10))
	if w.assignErr != nil {
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

func (w *recordingCoordinatorWorker) Stop(_ context.Context, request WorkerStopRequest) (WorkerSnapshot, error) {
	*w.log = append(*w.log, "stop-"+strconv.FormatUint(w.id, 10))
	if !sameWorkerFence(request.WorkerFence, w.assignment.WorkerFence) {
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
