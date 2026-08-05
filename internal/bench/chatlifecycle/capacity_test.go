package chatlifecycle

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func TestCapacityAdmissionRequiresPassingAgedReportAndSameLiveDataset(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	if staircase, err := NewCapacityStaircase(cfg, admission, start); err != nil || staircase == nil {
		t.Fatalf("valid admission = %#v, %v", staircase, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*CapacityAdmission)
	}{
		{"not live", func(a *CapacityAdmission) { a.Live = false }},
		{"not aged", func(a *CapacityAdmission) { a.Aged = false }},
		{"clean substitute", func(a *CapacityAdmission) { a.Clean = true }},
		{"reference mismatch", func(a *CapacityAdmission) { a.Reference = "other" }},
		{"malformed checkpoint digest", func(a *CapacityAdmission) { a.CheckpointDatasetHash = "dataset" }},
		{"dataset mismatch", func(a *CapacityAdmission) { a.LiveDatasetHash = hashReportValue("other-dataset") }},
		{"not final", func(a *CapacityAdmission) { a.Checkpoint.Kind = CheckpointQualification }},
		{"not pass", func(a *CapacityAdmission) {
			a.Checkpoint.Verdict.Outcome = VerdictProductFailure
			a.Checkpoint.Verdict.Cause = VerdictCauseMessageLoss
		}},
		{"wrong source mode", func(a *CapacityAdmission) { a.Checkpoint.Mode = ModeCapacity }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := admission
			test.mutate(&candidate)
			if staircase, err := NewCapacityStaircase(cfg, candidate, start); !errors.Is(err, ErrCapacityAdmission) || staircase != nil {
				t.Fatalf("admission = %#v, %v", staircase, err)
			}
		})
	}
	if staircase, err := NewCapacityStaircase(cfg, admission, admission.Checkpoint.Window.End); !errors.Is(err, ErrCapacityAdmission) || staircase != nil {
		t.Fatalf("same-instant admission = %#v, %v", staircase, err)
	}
}

func TestZeroCapacitySnapshotDoesNotClaimAnAttempt(t *testing.T) {
	if report := (CapacitySnapshot{}).ReportEvidence(); report.Attempted || !validReportCapacity(report) {
		t.Fatalf("zero snapshot report evidence = %+v", report)
	}
}

func TestCapacityStaircaseCoarseRefineAndRecovery(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := NewCapacityStaircase(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	assertCapacityPhase(t, staircase.Snapshot(), CapacityPhaseStabilize, 2_000, start.Add(10*time.Minute))

	capacityFinishStabilization(t, staircase)
	transition := capacityFinishMeasurement(t, staircase, passingCapacityObservation())
	if !transition.ScheduleRate || transition.RatePerSecond != 2_500 {
		t.Fatalf("first coarse transition = %+v", transition)
	}
	capacityFinishStabilization(t, staircase)
	transition = capacityFinishMeasurement(t, staircase, passingCapacityObservation())
	if !transition.ScheduleRate || transition.RatePerSecond != 3_125 {
		t.Fatalf("second coarse transition = %+v", transition)
	}

	capacityFinishStabilization(t, staircase)
	failed := passingCapacityObservation()
	failed.LatencyAccepted = false
	transition = capacityFinishMeasurement(t, staircase, failed)
	if !transition.ScheduleRate || transition.RatePerSecond != 2_750 {
		t.Fatalf("first refine transition = %+v", transition)
	}
	if snapshot := staircase.Snapshot(); snapshot.LastPassingRate != 2_500 || snapshot.FirstFailingRate != 3_125 || snapshot.RefineSteps != 0 {
		t.Fatalf("coarse boundary = %+v", snapshot)
	}

	capacityFinishStabilization(t, staircase)
	transition = capacityFinishMeasurement(t, staircase, passingCapacityObservation())
	if !transition.ScheduleRate || transition.RatePerSecond != 3_025 {
		t.Fatalf("second refine transition = %+v", transition)
	}
	capacityFinishStabilization(t, staircase)
	failed = passingCapacityObservation()
	failed.QueueInflightAccepted = false
	transition = capacityFinishMeasurement(t, staircase, failed)
	if !transition.ScheduleRate || transition.RatePerSecond != 2_000 || transition.Phase != CapacityPhaseRecovery {
		t.Fatalf("recovery transition = %+v", transition)
	}

	snapshot := staircase.Snapshot()
	if snapshot.LastPassingRate != 2_750 || snapshot.FirstFailingRate != 3_025 || snapshot.StepCount != 5 ||
		snapshot.CoarseSteps != 3 || snapshot.RefineSteps != 2 || len(snapshot.RecentSteps) != 5 {
		t.Fatalf("refined boundary = %+v", snapshot)
	}
	transition, err = staircase.Advance(snapshot.PhaseEnd, passingCapacityObservation())
	if err != nil {
		t.Fatal(err)
	}
	if transition.ScheduleRate || transition.Phase != CapacityPhaseComplete {
		t.Fatalf("completed recovery transition = %+v", transition)
	}
	snapshot = staircase.Snapshot()
	if snapshot.Outcome != CapacityPassed || !snapshot.Terminal || !snapshot.RecoveryPassed {
		t.Fatalf("capacity result = %+v", snapshot)
	}
	report := snapshot.ReportEvidence()
	if !report.Attempted || !report.Completed || report.MaximumPassingRate != 2_750 || report.FirstFailingRate != 3_025 || !report.RecoveryPassed {
		t.Fatalf("report evidence = %+v", report)
	}
}

func TestCapacityCorrectnessFailureTerminatesImmediately(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := NewCapacityStaircase(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	transition, err := staircase.Advance(start.Add(time.Minute), CapacityObservation{CorrectnessFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	if transition.Phase != CapacityPhaseTerminal || transition.Outcome != CapacityProductFailure {
		t.Fatalf("correctness transition = %+v", transition)
	}
	if snapshot := staircase.Snapshot(); snapshot.Cause != CapacityCauseCorrectness || !snapshot.Terminal {
		t.Fatalf("correctness snapshot = %+v", snapshot)
	} else if report := snapshot.ReportEvidence(); !report.Attempted || report.Completed || !validReportCapacity(report) {
		t.Fatalf("correctness report evidence = %+v", report)
	}
}

func TestCapacityRecoveryRequiresEveryBaselineGate(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := NewCapacityStaircase(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	capacityFinishStabilization(t, staircase)
	failed := passingCapacityObservation()
	failed.ErrorRateAccepted = false
	transition := capacityFinishMeasurement(t, staircase, failed)
	if transition.Phase != CapacityPhaseRecovery || transition.RatePerSecond != 2_000 {
		t.Fatalf("start-rate failure transition = %+v", transition)
	}
	recovery := passingCapacityObservation()
	recovery.ResourceAccepted = false
	transition, err = staircase.Advance(staircase.Snapshot().PhaseEnd, recovery)
	if err != nil {
		t.Fatal(err)
	}
	if transition.Outcome != CapacityProductFailure || transition.Phase != CapacityPhaseTerminal {
		t.Fatalf("failed recovery transition = %+v", transition)
	}
	if snapshot := staircase.Snapshot(); snapshot.Cause != CapacityCauseRecovery || snapshot.RecoveryPassed || snapshot.FirstFailingRate != 2_000 {
		t.Fatalf("failed recovery snapshot = %+v", snapshot)
	} else if report := snapshot.ReportEvidence(); !report.Completed || report.MaximumPassingRate != 0 || report.FirstFailingRate != 2_000 || !validReportCapacity(report) {
		t.Fatalf("failed recovery report evidence = %+v", report)
	}
}

func TestCapacityIncompleteMeasurementIsHarnessInvalidWithoutUnboundedHistory(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := NewCapacityStaircase(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	capacityFinishStabilization(t, staircase)
	if _, err := staircase.Advance(staircase.Snapshot().PhaseEnd, CapacityObservation{}); !errors.Is(err, ErrCapacityObservation) {
		t.Fatalf("incomplete measurement error = %v", err)
	}
	if snapshot := staircase.Snapshot(); snapshot.Outcome != CapacityHarnessInvalid || !snapshot.Terminal || len(snapshot.RecentSteps) > maxCapacityRecentSteps {
		t.Fatalf("incomplete snapshot = %+v", snapshot)
	}
}

func TestCapacityRateMathRejectsOverflowAndNeverLoopsForever(t *testing.T) {
	if _, ok := nextCapacityRate(math.MaxUint64/2, 25); ok {
		t.Fatal("overflowing two-tick rate was accepted")
	}
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := NewCapacityStaircase(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	for !staircase.Snapshot().Terminal {
		capacityFinishStabilization(t, staircase)
		_, _ = staircase.Advance(staircase.Snapshot().PhaseEnd, passingCapacityObservation())
	}
	snapshot := staircase.Snapshot()
	if snapshot.Outcome != CapacityHarnessInvalid || snapshot.Cause != CapacityCauseNoBoundary || snapshot.StepCount > maxCapacitySteps || len(snapshot.RecentSteps) > maxCapacityRecentSteps {
		t.Fatalf("unbounded staircase result = %+v", snapshot)
	}
}

func TestCoordinatorGrantPlanSchedulesCapacityRateOnNextTick(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "capacity-grant-plan"
	assignments, err := BuildCoordinatorAssignments(cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := plan.Tick([coordinatorWorkerCount]uint64{})
	if err != nil {
		t.Fatal(err)
	}
	if total, ok := checkedCoordinatorGrantSum(initial.Credit); !ok || total != 2_000 {
		t.Fatalf("initial retained credit = %+v", initial)
	}
	if err := plan.ScheduleRate(2_500); err != nil {
		t.Fatal(err)
	}
	grant, err := plan.Tick([coordinatorWorkerCount]uint64{})
	if err != nil {
		t.Fatal(err)
	}
	if total, ok := checkedCoordinatorGrantSum(grant.Fresh); !ok || total != 2_500 {
		t.Fatalf("capacity grant = %+v", grant)
	}
	if total, ok := checkedCoordinatorGrantSum(grant.Credit); !ok || total != 2_500 {
		t.Fatalf("old-rate credit survived capacity tick = %+v", grant)
	}
	request := plan.request(assignments[0].WorkerFence, grant)
	if request.RatePerSecond != 2_500 || request.MaxBurst != 5_000 {
		t.Fatalf("capacity request = %+v", request)
	}
	if err := plan.ScheduleRate(math.MaxUint64); !errors.Is(err, ErrCoordinatorConfig) {
		t.Fatalf("overflow schedule error = %v", err)
	}
}

func TestCoordinatorSchedulesAllWorkerRatesBeforeGrantPlan(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "capacity-rate-round"
	assignments, err := BuildCoordinatorAssignments(cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		t.Fatal(err)
	}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	rateWorkers := make([]*capacityRateWorker, coordinatorWorkerCount)
	for workerID := range workers {
		rateWorkers[workerID] = &capacityRateWorker{workerID: uint64(workerID), fence: assignments[0].WorkerFence}
		workers[workerID] = rateWorkers[workerID]
	}
	coordinator := &Coordinator{workers: workers, roundTimeout: time.Second}
	if err := coordinator.ScheduleCapacityRate(context.Background(), plan, 2_500); err != nil {
		t.Fatal(err)
	}
	for workerID, worker := range rateWorkers {
		requests := worker.requestsSnapshot()
		if len(requests) != 1 || requests[0].RatePerSecond != 2_500 || requests[0].MaxBurst != 5_000 || requests[0].WorkerFence != assignments[workerID].WorkerFence {
			t.Fatalf("worker %d requests = %+v", workerID, requests)
		}
	}
	grant, err := plan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		t.Fatal(err)
	}
	if total, _ := checkedCoordinatorGrantSum(grant.Fresh); total != 2_500 {
		t.Fatalf("scheduled total = %d", total)
	}

	failedPlan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		t.Fatal(err)
	}
	rateWorkers[1].err = errors.New("rate failed")
	if err := coordinator.ScheduleCapacityRate(context.Background(), failedPlan, 3_000); !errors.Is(err, ErrCoordinatorRateUpdate) {
		t.Fatalf("partial rate error = %v", err)
	}
	grant, err = failedPlan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		t.Fatal(err)
	}
	if total, _ := checkedCoordinatorGrantSum(grant.Fresh); total != 2_000 {
		t.Fatalf("failed round mutated plan total = %d", total)
	}
}

func TestCoordinatorSchedulesCapacityRatesConcurrently(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "capacity-rate-concurrent"
	assignments, err := BuildCoordinatorAssignments(cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan uint64, coordinatorWorkerCount)
	release := make(chan struct{})
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &capacityRateWorker{
			workerID: uint64(workerID), fence: assignments[workerID].WorkerFence,
			entered: entered, release: release,
		}
	}
	coordinator := &Coordinator{workers: workers, roundTimeout: time.Second}
	done := make(chan error, 1)
	go func() {
		done <- coordinator.ScheduleCapacityRate(context.Background(), plan, 2_500)
	}()
	seen := make(map[uint64]bool, coordinatorWorkerCount)
	for len(seen) < coordinatorWorkerCount {
		select {
		case workerID := <-entered:
			seen[workerID] = true
		case <-time.After(250 * time.Millisecond):
			close(release)
			t.Fatal("rate requests did not enter all three workers concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorCapacityRateRoundUsesOneDeadlineAndPreservesPlan(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "capacity-rate-deadline"
	assignments, err := BuildCoordinatorAssignments(cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan uint64, coordinatorWorkerCount)
	release := make(chan struct{})
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &capacityRateWorker{
			workerID: uint64(workerID), fence: assignments[workerID].WorkerFence,
			entered: entered, release: release,
		}
	}
	const roundTimeout = 25 * time.Millisecond
	coordinator := &Coordinator{workers: workers, roundTimeout: roundTimeout}
	started := time.Now()
	err = coordinator.ScheduleCapacityRate(context.Background(), plan, 2_500)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrCoordinatorRateUpdate) {
		t.Fatalf("deadline error = %v", err)
	}
	if elapsed < roundTimeout/2 || elapsed >= 150*time.Millisecond {
		t.Fatalf("rate round elapsed = %s, want one shared %s deadline", elapsed, roundTimeout)
	}
	if len(entered) != coordinatorWorkerCount {
		t.Fatalf("entered workers = %d, want %d", len(entered), coordinatorWorkerCount)
	}
	grant, err := plan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		t.Fatal(err)
	}
	if total, _ := checkedCoordinatorGrantSum(grant.Fresh); total != 2_000 {
		t.Fatalf("deadline mutated plan total = %d", total)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := coordinator.ScheduleCapacityRate(canceled, plan, 2_500); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation error = %v", err)
	}
}

func capacityTestAdmission(t *testing.T) (Config, CapacityAdmission, time.Time) {
	t.Helper()
	soak := FormalConfig()
	soak.RunID = "capacity-aged-soak"
	soakStart := time.Unix(1_900_000_000, 0)
	fence := WorkerFence{RunID: soak.RunID, AssignmentID: "aged-assignment", Generation: 1}
	recorder, err := NewCheckpointRecorder(soak, fence, soakStart)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureCheckpoint(t, recorder, soakStart.Add(24*time.Hour), coordinatorSnapshotFixture(fence, 1, 24*time.Hour, 1), checkpointEvidenceFixture(false)); err != nil {
		t.Fatal(err)
	}
	finalSnapshots := coordinatorSnapshotFixture(fence, 2, 72*time.Hour, 2)
	for index := range finalSnapshots {
		finalSnapshots[index].Phase = WorkerPhaseFinal
	}
	finalEvidence := checkpointEvidenceFixture(false)
	finalEvidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictPass, Cause: VerdictCauseCompleted}
	checkpoint, err := captureCheckpoint(t, recorder, soakStart.Add(72*time.Hour), finalSnapshots, finalEvidence)
	if err != nil {
		t.Fatal(err)
	}

	cfg := FormalConfig()
	cfg.RunID = "capacity-run"
	cfg.Mode = ModeCapacity
	cfg.Capacity.AgedCheckpoint = AgedCheckpoint{Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour}
	dataset := hashReportValue("service-dataset-generation-1")
	admission := CapacityAdmission{
		Reference: cfg.Capacity.AgedCheckpoint.Reference, Checkpoint: checkpoint,
		CheckpointDatasetHash: dataset, LiveDatasetHash: dataset, Live: true, Aged: true,
	}
	return cfg, admission, checkpoint.Window.End.Add(time.Minute)
}

func capacityFinishStabilization(t *testing.T, staircase *CapacityStaircase) {
	t.Helper()
	snapshot := staircase.Snapshot()
	transition, err := staircase.Advance(snapshot.PhaseEnd, CapacityObservation{})
	if err != nil {
		t.Fatal(err)
	}
	if transition.Phase != CapacityPhaseMeasure || transition.ScheduleRate {
		t.Fatalf("stabilization transition = %+v", transition)
	}
}

func capacityFinishMeasurement(t *testing.T, staircase *CapacityStaircase, observation CapacityObservation) CapacityTransition {
	t.Helper()
	transition, err := staircase.Advance(staircase.Snapshot().PhaseEnd, observation)
	if err != nil {
		t.Fatal(err)
	}
	return transition
}

func passingCapacityObservation() CapacityObservation {
	return CapacityObservation{
		Complete: true, ErrorRateAccepted: true, LatencyAccepted: true,
		QueueInflightAccepted: true, ClusterLagAccepted: true, ResourceAccepted: true,
	}
}

func assertCapacityPhase(t *testing.T, snapshot CapacitySnapshot, phase CapacityPhase, rate uint64, end time.Time) {
	t.Helper()
	if snapshot.Phase != phase || snapshot.CurrentRate != rate || !snapshot.PhaseEnd.Equal(end) || snapshot.Terminal {
		t.Fatalf("capacity phase = %+v", snapshot)
	}
}

func checkedCoordinatorGrantSum(values [coordinatorWorkerCount]uint64) (uint64, bool) {
	var total uint64
	for _, value := range values {
		if math.MaxUint64-total < value {
			return 0, false
		}
		total += value
	}
	return total, true
}

type capacityRateWorker struct {
	workerID uint64
	fence    WorkerFence
	err      error
	entered  chan<- uint64
	release  <-chan struct{}
	mu       sync.Mutex
	requests []WorkerRateRequest
}

func (w *capacityRateWorker) Assign(context.Context, WorkerAssignment) (WorkerStatus, error) {
	return WorkerStatus{}, errors.New("unexpected assign")
}
func (w *capacityRateWorker) Start(context.Context, WorkerStartRequest) (WorkerStatus, error) {
	return WorkerStatus{}, errors.New("unexpected start")
}
func (w *capacityRateWorker) Status(context.Context) (WorkerStatus, error) {
	return WorkerStatus{}, errors.New("unexpected status")
}
func (w *capacityRateWorker) Grant(context.Context, WorkerGrantRequest) (WorkerGrantResponse, error) {
	return WorkerGrantResponse{}, errors.New("unexpected grant")
}
func (w *capacityRateWorker) Checkpoint(context.Context, WorkerCheckpointRequest) (WorkerSnapshot, error) {
	return WorkerSnapshot{}, errors.New("unexpected checkpoint")
}
func (w *capacityRateWorker) Stop(context.Context, WorkerStopRequest) (WorkerSnapshot, error) {
	return WorkerSnapshot{}, errors.New("unexpected stop")
}
func (w *capacityRateWorker) UpdateRate(ctx context.Context, request WorkerRateRequest) (WorkerStatus, error) {
	if w.entered != nil {
		select {
		case w.entered <- w.workerID:
		case <-ctx.Done():
			return WorkerStatus{}, ctx.Err()
		}
		select {
		case <-w.release:
		case <-ctx.Done():
			return WorkerStatus{}, ctx.Err()
		}
	}
	w.mu.Lock()
	w.requests = append(w.requests, request)
	err := w.err
	w.mu.Unlock()
	if err != nil {
		return WorkerStatus{}, err
	}
	return WorkerStatus{
		RunID: w.fence.RunID, AssignmentID: w.fence.AssignmentID, Generation: w.fence.Generation,
		WorkerID: w.workerID, WorkerCount: coordinatorWorkerCount, Phase: WorkerPhaseRunning, TrafficReady: true,
	}, nil
}
func (w *capacityRateWorker) requestsSnapshot() []WorkerRateRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]WorkerRateRequest(nil), w.requests...)
}
