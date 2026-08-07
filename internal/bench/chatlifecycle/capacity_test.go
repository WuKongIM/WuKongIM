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
	live := capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, start)
	token, err := validateCapacityLiveDataset(cfg, admission, live, start, start)
	if err != nil {
		t.Fatal(err)
	}
	if staircase, err := newCapacityStaircase(cfg, token, start); err != nil || staircase == nil {
		t.Fatalf("valid admission = %#v, %v", staircase, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*CapacityLiveDatasetEvidence)
	}{
		{"not live", func(e *CapacityLiveDatasetEvidence) { e.Nodes[0].State = CapacityDatasetUnavailable }},
		{"clean substitute", func(e *CapacityLiveDatasetEvidence) { e.Nodes[0].State = CapacityDatasetClean }},
		{"duplicate node", func(e *CapacityLiveDatasetEvidence) { e.Nodes[2].NodeID = e.Nodes[1].NodeID }},
		{"dataset mismatch", func(e *CapacityLiveDatasetEvidence) { e.Nodes[1].DatasetDigest = hashReportValue("other-dataset") }},
		{"stale probe result", func(e *CapacityLiveDatasetEvidence) { e.Nodes[2].ObservedAt = start.Add(-time.Nanosecond) }},
		{"future probe result", func(e *CapacityLiveDatasetEvidence) { e.Nodes[2].ObservedAt = start.Add(time.Nanosecond) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := live
			test.mutate(&candidate)
			if _, err := validateCapacityLiveDataset(cfg, admission, candidate, start, start); !errors.Is(err, ErrCapacityAdmission) {
				t.Fatalf("admission error = %v", err)
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*CapacityAdmission)
	}{
		{"reference mismatch", func(a *CapacityAdmission) { a.Reference = "other" }},
		{"malformed checkpoint digest", func(a *CapacityAdmission) { a.Checkpoint.DatasetDigest = "dataset" }},
		{"not final", func(a *CapacityAdmission) { a.Checkpoint.Kind = CheckpointQualification }},
		{"not pass", func(a *CapacityAdmission) {
			a.Checkpoint.Verdict.Outcome = VerdictProductFailure
			a.Checkpoint.Verdict.Cause = VerdictCauseMessageLoss
		}},
		{"wrong source mode", func(a *CapacityAdmission) { a.Checkpoint.Mode = ModeCapacity }},
		{"rehearsal source stage", func(a *CapacityAdmission) { a.Checkpoint.Stage = StageRehearsal }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := admission
			test.mutate(&candidate)
			if err := validateCapacityCheckpoint(cfg, candidate); !errors.Is(err, ErrCapacityAdmission) {
				t.Fatalf("admission error = %v", err)
			}
		})
	}
	if _, err := validateCapacityLiveDataset(
		cfg, admission, live, admission.Checkpoint.Window.End, admission.Checkpoint.Window.End,
	); !errors.Is(err, ErrCapacityAdmission) {
		t.Fatalf("same-instant admission error = %v", err)
	}
}

func TestZeroCapacitySnapshotDoesNotClaimAnAttempt(t *testing.T) {
	if report := (CapacitySnapshot{}).ReportEvidence(); report.Attempted || !validReportCapacity(report) {
		t.Fatalf("zero snapshot report evidence = %+v", report)
	}
}

func TestCapacityStaircaseCoarseRefineAndRecovery(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := newCapacityStaircaseForTest(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	assertCapacityPhase(t, staircase.Snapshot(), CapacityPhaseStabilize, 2_000, start.Add(10*time.Minute))

	capacityFinishStabilization(t, staircase)
	transition := capacityFinishMeasurement(t, staircase, passingCapacityObservation())
	if !transition.ScheduleRate || transition.RatePerSecond != 2_500 || transition.Phase != CapacityPhaseRatePending {
		t.Fatalf("first coarse transition = %+v", transition)
	}
	capacityCommitRate(t, staircase, transition, 5*time.Second)
	capacityFinishStabilization(t, staircase)
	transition = capacityFinishMeasurement(t, staircase, passingCapacityObservation())
	if !transition.ScheduleRate || transition.RatePerSecond != 3_125 {
		t.Fatalf("second coarse transition = %+v", transition)
	}
	capacityCommitRate(t, staircase, transition, 3*time.Second)

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
	capacityCommitRate(t, staircase, transition, 2*time.Second)

	capacityFinishStabilization(t, staircase)
	transition = capacityFinishMeasurement(t, staircase, passingCapacityObservation())
	if !transition.ScheduleRate || transition.RatePerSecond != 3_025 {
		t.Fatalf("second refine transition = %+v", transition)
	}
	capacityCommitRate(t, staircase, transition, time.Second)
	capacityFinishStabilization(t, staircase)
	failed = passingCapacityObservation()
	failed.QueueInflightAccepted = false
	transition = capacityFinishMeasurement(t, staircase, failed)
	if !transition.ScheduleRate || transition.RatePerSecond != 2_000 || transition.Phase != CapacityPhaseRatePending {
		t.Fatalf("recovery transition = %+v", transition)
	}
	capacityCommitRate(t, staircase, transition, 4*time.Second)

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

func TestCapacityRateWindowStartsOnlyAfterOwnerCommit(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := newCapacityStaircaseForTest(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	capacityFinishStabilization(t, staircase)
	transition := capacityFinishMeasurement(t, staircase, passingCapacityObservation())
	pending := staircase.Snapshot()
	if pending.Phase != CapacityPhaseRatePending || !pending.PhaseEnd.IsZero() {
		t.Fatalf("pending snapshot = %+v", pending)
	}
	commitAt := pending.PhaseStart.Add(7 * time.Second)
	transition, err = staircase.CommitRate(commitAt, transition.RatePerSecond)
	if err != nil {
		t.Fatal(err)
	}
	if transition.Phase != CapacityPhaseStabilize {
		t.Fatalf("committed transition = %+v", transition)
	}
	if snapshot := staircase.Snapshot(); !snapshot.PhaseStart.Equal(commitAt) || !snapshot.PhaseEnd.Equal(commitAt.Add(10*time.Minute)) {
		t.Fatalf("committed window = %+v", snapshot)
	}
}

func TestCapacityRateFailureFreezesHarnessWithoutStartingWindow(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := newCapacityStaircaseForTest(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	capacityFinishStabilization(t, staircase)
	transition := capacityFinishMeasurement(t, staircase, passingCapacityObservation())
	if !transition.ScheduleRate {
		t.Fatalf("pending transition = %+v", transition)
	}
	if _, err := staircase.FailRateChange(staircase.Snapshot().PhaseStart.Add(time.Second)); !errors.Is(err, ErrCapacityObservation) {
		t.Fatalf("rate failure error = %v", err)
	}
	if snapshot := staircase.Snapshot(); !snapshot.Terminal || snapshot.Outcome != CapacityHarnessInvalid || !snapshot.PhaseEnd.IsZero() {
		t.Fatalf("rate failure snapshot = %+v", snapshot)
	}
}

func TestCapacityCorrectnessFailureTerminatesImmediately(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := newCapacityStaircaseForTest(cfg, admission, start)
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
	staircase, err := newCapacityStaircaseForTest(cfg, admission, start)
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

func TestCapacityRecoveryRequiresReadinessAndLifecycleActivity(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*CapacityObservation)
	}{
		{"readiness", func(o *CapacityObservation) { o.ReadinessAccepted = false }},
		{"lifecycle", func(o *CapacityObservation) { o.LifecycleAccepted = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, admission, start := capacityTestAdmission(t)
			staircase, err := newCapacityStaircaseForTest(cfg, admission, start)
			if err != nil {
				t.Fatal(err)
			}
			capacityFinishStabilization(t, staircase)
			failed := passingCapacityObservation()
			failed.ErrorRateAccepted = false
			_ = capacityFinishMeasurement(t, staircase, failed)
			recovery := passingCapacityObservation()
			test.mutate(&recovery)
			transition, err := staircase.Advance(staircase.Snapshot().PhaseEnd, recovery)
			if err != nil || transition.Outcome != CapacityProductFailure {
				t.Fatalf("recovery transition = %+v, %v", transition, err)
			}
		})
	}
}

func TestCapacityIncompleteMeasurementIsHarnessInvalidWithoutUnboundedHistory(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := newCapacityStaircaseForTest(cfg, admission, start)
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

func TestCapacityMissedPhaseBoundaryIsHarnessInvalid(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := newCapacityStaircaseForTest(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staircase.Advance(start.Add(10*time.Minute+time.Nanosecond), CapacityObservation{}); !errors.Is(err, ErrCapacityObservation) {
		t.Fatalf("missed boundary error = %v", err)
	}
	if snapshot := staircase.Snapshot(); !snapshot.Terminal || snapshot.Outcome != CapacityHarnessInvalid {
		t.Fatalf("missed boundary snapshot = %+v", snapshot)
	}
}

func TestCapacityRateMathRejectsOverflowAndNeverLoopsForever(t *testing.T) {
	if _, ok := nextCapacityRate(math.MaxUint64/2, 25); ok {
		t.Fatal("overflowing two-tick rate was accepted")
	}
	cfg, admission, start := capacityTestAdmission(t)
	staircase, err := newCapacityStaircaseForTest(cfg, admission, start)
	if err != nil {
		t.Fatal(err)
	}
	for !staircase.Snapshot().Terminal {
		capacityFinishStabilization(t, staircase)
		transition, _ := staircase.Advance(staircase.Snapshot().PhaseEnd, passingCapacityObservation())
		if transition.ScheduleRate {
			capacityCommitRate(t, staircase, transition, time.Second)
		}
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
	if request := plan.request(assignments[0].WorkerFence, initial); request.RatePerSecond != 2_000 || request.MaxBurst != 4_000 {
		t.Fatalf("pre-commit request = %+v", request)
	}
	grant, err := plan.Tick([coordinatorWorkerCount]uint64{})
	if err != nil {
		t.Fatal(err)
	}
	if total, ok := checkedCoordinatorGrantSum(grant.Fresh); !ok || total != 2_500 || !grant.RateChanged || grant.RatePerSecond != 2_500 {
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
	if disposition := coordinator.updateCapacityWorkers(context.Background(), plan.fence, 2_500); disposition != coordinatorRoundSucceeded {
		t.Fatalf("rate disposition = %v", disposition)
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
	if total, _ := checkedCoordinatorGrantSum(grant.Fresh); total != 2_000 {
		t.Fatalf("worker round mutated owner plan total = %d", total)
	}

	rateWorkers[1].err = errors.New("rate failed")
	if disposition := coordinator.updateCapacityWorkers(context.Background(), plan.fence, 3_000); disposition != coordinatorRoundStageFailed {
		t.Fatalf("partial rate disposition = %v", disposition)
	}
	grant, err = plan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
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
		if disposition := coordinator.updateCapacityWorkers(context.Background(), plan.fence, 2_500); disposition != coordinatorRoundSucceeded {
			done <- errors.New("rate round failed")
			return
		}
		done <- nil
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
	for tick := 0; tick < 3; tick++ {
		grant, err := plan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
		if err != nil {
			t.Fatal(err)
		}
		if total, _ := checkedCoordinatorGrantSum(grant.Fresh); total != 2_000 || grant.RateChanged {
			t.Fatalf("grant changed while worker rate round was pending: %+v", grant)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := plan.ScheduleRate(2_500); err != nil {
		t.Fatal(err)
	}
	if grant, err := plan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64}); err != nil || !grant.RateChanged || grant.RatePerSecond != 2_500 {
		t.Fatalf("owner commit grant = %+v, %v", grant, err)
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
	disposition := coordinator.updateCapacityWorkers(context.Background(), plan.fence, 2_500)
	elapsed := time.Since(started)
	if disposition != coordinatorRoundStageFailed {
		t.Fatalf("deadline disposition = %v", disposition)
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
	if disposition := coordinator.updateCapacityWorkers(canceled, plan.fence, 2_500); disposition != coordinatorRoundParentCanceled {
		t.Fatalf("parent cancellation disposition = %v", disposition)
	}
}

func TestCoordinatorRunExecutesCapacityModeThroughRecovery(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	clock := newManualCoordinatorClock(start)
	grantCalls := make(chan uint64, coordinatorWorkerCount)
	observerStarted := make(chan struct{})
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &recordingCoordinatorWorker{
			id: uint64(workerID), log: &log, grantCalled: grantCalls,
		}
	}
	evidence := &scriptedCapacityEvidence{requests: make(chan CapacityEvidenceRequest, 2)}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 1,
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
		Clock: clock, CapacityAdmission: &admission, CapacityEvidence: evidence,
		CapacityDataset: coordinatorCapacityDatasetProbeFunc(func(context.Context, Config) (CapacityLiveDatasetEvidence, error) {
			return capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, clock.Now()), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		<-grantCalls
	}
	for second := 0; second < int((time.Hour)/time.Second); second++ {
		clock.advance(time.Second)
		for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
			select {
			case <-grantCalls:
			case result := <-resultChannel:
				t.Fatalf("capacity Run ended at second %d: %+v", second+1, result)
			case <-time.After(time.Second):
				t.Fatalf("grant %d stalled at second %d", workerID, second+1)
			}
		}
		if second+1 == int((10*time.Minute)/time.Second) || second+1 == int((30*time.Minute)/time.Second) {
			// The manual clock can otherwise advance a second before the owner
			// consumes the just-delivered phase-boundary tick.
			time.Sleep(10 * time.Millisecond)
		}
	}
	result := <-resultChannel
	if result.Outcome != CoordinatorCompleted || result.Code != CoordinatorCodeCompleted ||
		result.Capacity.Outcome != CapacityPassed || !result.Capacity.RecoveryPassed {
		t.Fatalf("capacity Run result = %+v", result)
	}
	first, second := <-evidence.requests, <-evidence.requests
	if first.Phase != CapacityPhaseMeasure || first.RatePerSecond != 2_000 || first.End.Sub(first.Start) != 20*time.Minute ||
		second.Phase != CapacityPhaseRecovery || second.RatePerSecond != 2_000 || second.End.Sub(second.Start) != 30*time.Minute {
		t.Fatalf("capacity evidence windows = %+v, %+v", first, second)
	}
}

func TestCoordinatorCapacityProductFailureFinalizesHookEvidence(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	clock := newManualCoordinatorClock(start)
	grantCalls := make(chan uint64, coordinatorWorkerCount)
	observerStarted := make(chan struct{})
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &recordingCoordinatorWorker{
			id: uint64(workerID), log: &[]string{}, grantCalled: grantCalls,
		}
	}
	productFailure := passingCapacityObservation()
	productFailure.CorrectnessFailure = true
	evidence := &scriptedCapacityEvidence{
		requests: make(chan CapacityEvidenceRequest, 1), observations: []CapacityObservation{productFailure},
	}
	hooks := newRecordingCoordinatorRunHooks()
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 3,
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
		Clock: clock, Hooks: hooks, CapacityAdmission: &admission, CapacityEvidence: evidence,
		CapacityDataset: coordinatorCapacityDatasetProbeFunc(func(context.Context, Config) (CapacityLiveDatasetEvidence, error) {
			return capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, clock.Now()), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	<-hooks.started
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		<-grantCalls
	}
	for second := 1; second <= int((30*time.Minute)/time.Second); second++ {
		clock.advance(time.Second)
		for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
			<-grantCalls
		}
		if second == int((10*time.Minute)/time.Second) || second == int((30*time.Minute)/time.Second) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	result := <-resultChannel
	if result.Outcome != CoordinatorProductFailure || result.Code != CoordinatorCodeObserver ||
		result.Capacity.Outcome != CapacityProductFailure {
		t.Fatalf("capacity product result = %+v", result)
	}
	var final CoordinatorFinalCut
	select {
	case final = <-hooks.finalized:
	case <-time.After(time.Second):
		t.Fatal("capacity product failure skipped hook finalization")
	}
	if final.Decision != CoordinatorProductFailure || final.Capacity.Outcome != CapacityProductFailure ||
		len(final.FinalSnapshots) != coordinatorWorkerCount {
		t.Fatalf("capacity product final cut = %+v", final)
	}
}

func TestCoordinatorCapacityBeginFailureFinalizesHarnessInvalidHookEvidence(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	clock := newManualCoordinatorClock(start)
	grantCalls := make(chan uint64, coordinatorWorkerCount)
	observerStarted := make(chan struct{})
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &recordingCoordinatorWorker{
			id: uint64(workerID), log: &[]string{}, grantCalled: grantCalls,
		}
	}
	evidence := &scriptedCapacityEvidence{
		requests: make(chan CapacityEvidenceRequest, 1), beginErr: errors.New("baseline unavailable"),
	}
	hooks := newRecordingCoordinatorRunHooks()
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 4,
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
		Clock: clock, Hooks: hooks, CapacityAdmission: &admission, CapacityEvidence: evidence,
		CapacityDataset: coordinatorCapacityDatasetProbeFunc(func(context.Context, Config) (CapacityLiveDatasetEvidence, error) {
			return capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, clock.Now()), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	<-hooks.started
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		<-grantCalls
	}
	for second := 1; second <= int((10*time.Minute)/time.Second); second++ {
		clock.advance(time.Second)
		for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
			<-grantCalls
		}
	}
	result := <-resultChannel
	if result.Outcome != CoordinatorHarnessInvalid || !result.Capacity.Terminal ||
		result.Capacity.Outcome != CapacityHarnessInvalid || result.Capacity.Cause != CapacityCauseObservation {
		t.Fatalf("capacity begin failure result = %+v", result)
	}
	var final CoordinatorFinalCut
	select {
	case final = <-hooks.finalized:
	case <-time.After(time.Second):
		t.Fatal("capacity begin failure skipped hook finalization")
	}
	if final.Decision != CoordinatorHarnessInvalid || final.Capacity.Outcome != CapacityHarnessInvalid ||
		len(final.FinalSnapshots) != coordinatorWorkerCount {
		t.Fatalf("capacity begin failure final cut = %+v", final)
	}
}

func TestCoordinatorCapacityEvidenceParentCancellationIsStoppedAndJoined(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	clock := newManualCoordinatorClock(start)
	grantCalls := make(chan uint64, coordinatorWorkerCount)
	observerStarted := make(chan struct{})
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &log, grantCalled: grantCalls}
	}
	evidence := &cancelJoiningCapacityEvidence{started: make(chan struct{}), returned: make(chan struct{})}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 1,
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
		Clock: clock, CapacityAdmission: &admission, CapacityEvidence: evidence,
		CapacityDataset: coordinatorCapacityDatasetProbeFunc(func(context.Context, Config) (CapacityLiveDatasetEvidence, error) {
			return capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, clock.Now()), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runContext, cancelRun := context.WithCancel(context.Background())
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(runContext, cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		<-grantCalls
	}
	for second := 1; second <= int((30*time.Minute)/time.Second); second++ {
		clock.advance(time.Second)
		for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
			<-grantCalls
		}
		if second == int((10*time.Minute)/time.Second) || second == int((30*time.Minute)/time.Second) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	<-evidence.started
	cancelRun()
	result := <-resultChannel
	if result.Outcome != CoordinatorStopped || result.Code != CoordinatorCodeStopped {
		t.Fatalf("parent cancellation result = %+v", result)
	}
	select {
	case <-evidence.returned:
	default:
		t.Fatal("coordinator returned before joining capacity evidence")
	}
}

func TestCoordinatorRejectsCapacityAdmissionBeforeTargetMutation(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	log := []string{}
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		workers[workerID] = &recordingCoordinatorWorker{id: uint64(workerID), log: &log}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 1,
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
			appendCoordinatorTestLog(&log, "observe")
			return ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeTopology}
		}),
		Clock: fixedCoordinatorClock{now: start}, CapacityAdmission: &admission,
		CapacityEvidence: &scriptedCapacityEvidence{requests: make(chan CapacityEvidenceRequest, 1)},
		CapacityDataset: coordinatorCapacityDatasetProbeFunc(func(context.Context, Config) (CapacityLiveDatasetEvidence, error) {
			appendCoordinatorTestLog(&log, "dataset_probe")
			live := capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, start)
			live.Nodes[2].State = CapacityDatasetClean
			return live, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := coordinator.Run(context.Background(), cfg)
	if result.Outcome != CoordinatorHarnessInvalid || result.Code != CoordinatorCodeCapacity {
		t.Fatalf("invalid admission result = %+v", result)
	}
	if got := coordinatorTestLogSnapshot(&log); len(got) != 2 || got[0] != "preflight" || got[1] != "dataset_probe" {
		t.Fatalf("invalid admission crossed the read-only gate: %v", got)
	}
}

func TestCoordinatorCapacityRateChangesCommitInsideActiveGrantLoop(t *testing.T) {
	cfg, admission, start := capacityTestAdmission(t)
	clock := newManualCoordinatorClock(start)
	grantCalls := make(chan uint64, coordinatorWorkerCount)
	observerStarted := make(chan struct{})
	log := []string{}
	typedWorkers := make([]*recordingCoordinatorWorker, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for workerID := range workers {
		typedWorkers[workerID] = &recordingCoordinatorWorker{
			id: uint64(workerID), log: &log, grantCalled: grantCalls,
		}
		workers[workerID] = typedWorkers[workerID]
	}
	pass := passingCapacityObservation()
	coarseFail := passingCapacityObservation()
	coarseFail.LatencyAccepted = false
	refineFail := passingCapacityObservation()
	refineFail.QueueInflightAccepted = false
	evidence := &scriptedCapacityEvidence{
		requests:     make(chan CapacityEvidenceRequest, 4),
		observations: []CapacityObservation{pass, coarseFail, refineFail, pass},
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 2,
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
		Clock: clock, CapacityAdmission: &admission, CapacityEvidence: evidence,
		CapacityDataset: coordinatorCapacityDatasetProbeFunc(func(context.Context, Config) (CapacityLiveDatasetEvidence, error) {
			return capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, clock.Now()), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	resultChannel := make(chan CoordinatorResult, 1)
	go func() { resultChannel <- coordinator.Run(context.Background(), cfg) }()
	<-observerStarted
	for tickerIndex := 0; tickerIndex < 2; tickerIndex++ {
		<-clock.created
	}
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		<-grantCalls
	}
	ownerBoundaries := map[int]bool{
		600: true, 1800: true, 1801: true, 2401: true, 3601: true,
		3602: true, 4202: true, 5402: true, 5403: true,
	}
	for second := 1; second <= 7_203; second++ {
		clock.advance(time.Second)
		for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
			select {
			case <-grantCalls:
			case result := <-resultChannel:
				t.Fatalf("capacity rate Run ended at second %d: %+v", second, result)
			case <-time.After(time.Second):
				t.Fatalf("capacity rate grant %d stalled at second %d", workerID, second)
			}
		}
		if ownerBoundaries[second] {
			time.Sleep(10 * time.Millisecond)
		}
	}
	result := <-resultChannel
	if result.Outcome != CoordinatorCompleted || result.Capacity.Outcome != CapacityPassed ||
		result.Capacity.LastPassingRate != 2_000 || result.Capacity.FirstFailingRate != 2_200 || !result.Capacity.RecoveryPassed {
		t.Fatalf("capacity rate result = %+v", result)
	}
	if begins := evidence.beginSnapshot(); len(begins) != 4 {
		t.Fatalf("capacity baseline windows = %+v, want four measured/recovery windows", begins)
	} else {
		for index, begin := range begins {
			observed := evidence.observedRequest(index)
			if begin != observed {
				t.Fatalf("capacity window %d begin = %+v, observed = %+v", index, begin, observed)
			}
		}
	}
	for workerID, worker := range typedWorkers {
		seen := map[uint64]bool{}
		for _, request := range worker.grantRequests {
			seen[request.RatePerSecond] = true
		}
		if !seen[2_000] || !seen[2_500] || !seen[2_200] {
			t.Fatalf("worker %d grant rates = %+v", workerID, seen)
		}
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
	admission := CapacityAdmission{
		Reference: cfg.Capacity.AgedCheckpoint.Reference, Checkpoint: checkpoint,
	}
	return cfg, admission, checkpoint.Window.End.Add(time.Minute)
}

func capacityLiveDatasetFixture(digest string, observedAt time.Time) CapacityLiveDatasetEvidence {
	var evidence CapacityLiveDatasetEvidence
	for index := range evidence.Nodes {
		evidence.Nodes[index] = CapacityLiveDatasetNodeEvidence{
			NodeID: uint64(index + 1), DatasetDigest: digest,
			ObservedAt: observedAt, State: CapacityDatasetLiveAged,
		}
	}
	return evidence
}

func newCapacityStaircaseForTest(cfg Config, admission CapacityAdmission, start time.Time) (*CapacityStaircase, error) {
	live := capacityLiveDatasetFixture(admission.Checkpoint.DatasetDigest, start)
	token, err := validateCapacityLiveDataset(cfg, admission, live, start, start)
	if err != nil {
		return nil, err
	}
	return newCapacityStaircase(cfg, token, start)
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
		ReadinessAccepted: true, LifecycleAccepted: true,
	}
}

func capacityCommitRate(t *testing.T, staircase *CapacityStaircase, transition CapacityTransition, delay time.Duration) {
	t.Helper()
	pending := staircase.Snapshot()
	committed, err := staircase.CommitRate(pending.PhaseStart.Add(delay), transition.RatePerSecond)
	if err != nil {
		t.Fatal(err)
	}
	if committed.ScheduleRate || committed.RatePerSecond != transition.RatePerSecond {
		t.Fatalf("committed rate transition = %+v", committed)
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

type scriptedCapacityEvidence struct {
	mu           sync.Mutex
	calls        int
	requests     chan CapacityEvidenceRequest
	observations []CapacityObservation
	beginErr     error
	begins       []CapacityEvidenceRequest
	observed     []CapacityEvidenceRequest
}

type cancelJoiningCapacityEvidence struct {
	started  chan struct{}
	returned chan struct{}
}

func (e *cancelJoiningCapacityEvidence) ObserveCapacity(ctx context.Context, _ CapacityEvidenceRequest) (CapacityObservation, error) {
	close(e.started)
	<-ctx.Done()
	close(e.returned)
	return CapacityObservation{}, ctx.Err()
}

type coordinatorCapacityDatasetProbeFunc func(context.Context, Config) (CapacityLiveDatasetEvidence, error)

func (f coordinatorCapacityDatasetProbeFunc) ProbeCapacityDataset(ctx context.Context, cfg Config) (CapacityLiveDatasetEvidence, error) {
	return f(ctx, cfg)
}

func (e *scriptedCapacityEvidence) ObserveCapacity(_ context.Context, request CapacityEvidenceRequest) (CapacityObservation, error) {
	e.requests <- request
	e.mu.Lock()
	defer e.mu.Unlock()
	e.observed = append(e.observed, request)
	e.calls++
	if e.calls <= len(e.observations) {
		return e.observations[e.calls-1], nil
	}
	observation := passingCapacityObservation()
	if e.calls == 1 {
		observation.ErrorRateAccepted = false
	}
	return observation, nil
}

func (e *scriptedCapacityEvidence) BeginCapacity(_ context.Context, request CapacityEvidenceRequest) error {
	e.mu.Lock()
	e.begins = append(e.begins, request)
	e.mu.Unlock()
	return e.beginErr
}

func (e *scriptedCapacityEvidence) beginSnapshot() []CapacityEvidenceRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]CapacityEvidenceRequest(nil), e.begins...)
}

func (e *scriptedCapacityEvidence) observedRequest(index int) CapacityEvidenceRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.observed[index]
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
