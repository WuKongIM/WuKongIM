package chatlifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"time"
)

const (
	coordinatorWorkerCount      = 3
	coordinatorDefaultRoundTime = 5 * time.Second
	coordinatorGrantCadence     = time.Second
)

var ErrCoordinatorConfig = errors.New("chat lifecycle coordinator: invalid configuration")

var errCoordinatorRoundDeadline = errors.New("chat lifecycle coordinator: control round deadline")

var (
	ErrCoordinatorSnapshotCount      = errors.New("chat lifecycle coordinator: snapshot count must equal three")
	ErrCoordinatorSnapshotFence      = errors.New("chat lifecycle coordinator: snapshot fence mismatch")
	ErrCoordinatorHistogramSchema    = errors.New("chat lifecycle coordinator: incompatible histogram schema")
	ErrCoordinatorSnapshotStale      = errors.New("chat lifecycle coordinator: stale worker snapshot")
	ErrCoordinatorSnapshotRegression = errors.New("chat lifecycle coordinator: worker counter regression")
	ErrCoordinatorSnapshotOverflow   = errors.New("chat lifecycle coordinator: snapshot aggregate overflow")
)

// CoordinatorOutcome is the narrow Phase 4.2 orchestration result. Threshold
// verdict and report semantics remain outside this startup coordinator.
type CoordinatorOutcome string

const (
	CoordinatorCompleted             CoordinatorOutcome = "completed"
	CoordinatorProductFailure        CoordinatorOutcome = "product_failure"
	CoordinatorHarnessInvalid        CoordinatorOutcome = "harness_invalid"
	CoordinatorInfrastructureFailure CoordinatorOutcome = "infrastructure_failure"
	CoordinatorStopped               CoordinatorOutcome = "stopped"
)

// CoordinatorCode is a closed startup/fencing failure vocabulary.
type CoordinatorCode string

const (
	CoordinatorCodeCompleted       CoordinatorCode = "completed"
	CoordinatorCodePreflight       CoordinatorCode = "preflight"
	CoordinatorCodeSetup           CoordinatorCode = "setup"
	CoordinatorCodeAssignment      CoordinatorCode = "assignment"
	CoordinatorCodeStart           CoordinatorCode = "start"
	CoordinatorCodeGrant           CoordinatorCode = "grant"
	CoordinatorCodeRuntime         CoordinatorCode = "runtime"
	CoordinatorCodeObserver        CoordinatorCode = "observer"
	CoordinatorCodeCheckpoint      CoordinatorCode = "checkpoint"
	CoordinatorCodeFinalize        CoordinatorCode = "finalize"
	CoordinatorCodeGenerationReuse CoordinatorCode = "generation_reuse"
	CoordinatorCodeStopped         CoordinatorCode = "stopped"
)

type coordinatorRoundDisposition uint8

const (
	coordinatorRoundSucceeded coordinatorRoundDisposition = iota
	coordinatorRoundStageFailed
	coordinatorRoundParentCanceled
)

// coordinatorRoundEvidence retains the RPC outcome needed to distinguish a
// stage failure from cancellation propagated by the round's parent context.
type coordinatorRoundEvidence struct {
	err   error
	valid bool
}

// coordinatorTerminationReason freezes the first terminal control-round cause
// before observer joining or worker cleanup can admit a later caller cancel.
type coordinatorTerminationReason struct {
	fallback    CoordinatorCode
	disposition coordinatorRoundDisposition
}

// CoordinatorResult contains only bounded orchestration state.
type CoordinatorResult struct {
	Outcome  CoordinatorOutcome
	Code     CoordinatorCode
	Fence    WorkerFence
	Grant    CoordinatorGrant
	Snapshot CoordinatorSnapshot
}

// CoordinatorPreflight is the existing traffic admission boundary.
type CoordinatorPreflight interface {
	Check(context.Context, Config) PreflightResult
}

// CoordinatorGroupSetup prepares the fixed catalog before assignment.
type CoordinatorGroupSetup interface {
	Run(context.Context, Config) error
}

// CoordinatorWorker is the live subset of the dedicated worker client used by startup.
type CoordinatorWorker interface {
	Assign(context.Context, WorkerAssignment) (WorkerStatus, error)
	Start(context.Context, WorkerStartRequest) (WorkerStatus, error)
	Status(context.Context) (WorkerStatus, error)
	Grant(context.Context, WorkerGrantRequest) (WorkerGrantResponse, error)
	Checkpoint(context.Context, WorkerCheckpointRequest) (WorkerSnapshot, error)
	Stop(context.Context, WorkerStopRequest) (WorkerSnapshot, error)
}

// CoordinatorObserver is the existing continuous service observer boundary.
type CoordinatorObserver interface {
	Run(context.Context, Config) ObserverResult
}

// CoordinatorClock owns readiness, grant, status, and final-cutoff time.
type CoordinatorClock interface {
	Now() time.Time
	NewTicker(time.Duration) ObserverTicker
}

// CoordinatorOptions fixes the only generation this coordinator may run.
type CoordinatorOptions struct {
	Generation     uint64
	Preflight      CoordinatorPreflight
	Setup          CoordinatorGroupSetup
	Workers        []CoordinatorWorker
	Observer       CoordinatorObserver
	Clock          CoordinatorClock
	CleanupTimeout time.Duration
	RoundTimeout   time.Duration
}

// Coordinator owns one non-resumable assignment generation.
type Coordinator struct {
	generation     uint64
	preflight      CoordinatorPreflight
	setup          CoordinatorGroupSetup
	workers        []CoordinatorWorker
	observer       CoordinatorObserver
	clock          CoordinatorClock
	cleanupTimeout time.Duration
	roundTimeout   time.Duration

	mu   sync.Mutex
	used bool
}

// NewCoordinator requires exactly three independent workers.
func NewCoordinator(options CoordinatorOptions) (*Coordinator, error) {
	if options.Generation == 0 || options.Generation > maxLogicalGeneration || options.Preflight == nil ||
		options.Setup == nil || options.Observer == nil || len(options.Workers) != coordinatorWorkerCount {
		return nil, ErrCoordinatorConfig
	}
	workers := append([]CoordinatorWorker(nil), options.Workers...)
	for _, worker := range workers {
		if worker == nil {
			return nil, ErrCoordinatorConfig
		}
	}
	if options.Clock == nil {
		options.Clock = realObserverClock{}
	}
	if options.CleanupTimeout == 0 {
		options.CleanupTimeout = workerMaxDrainTimeout
	}
	if options.CleanupTimeout < 0 || options.CleanupTimeout > workerMaxDrainTimeout {
		return nil, ErrCoordinatorConfig
	}
	if options.RoundTimeout == 0 {
		options.RoundTimeout = coordinatorDefaultRoundTime
	}
	if options.RoundTimeout < 0 || options.RoundTimeout > coordinatorDefaultRoundTime {
		return nil, ErrCoordinatorConfig
	}
	return &Coordinator{
		generation: options.Generation, preflight: options.Preflight, setup: options.Setup,
		workers: workers, observer: options.Observer, clock: options.Clock,
		cleanupTimeout: options.CleanupTimeout, roundTimeout: options.RoundTimeout,
	}, nil
}

// Run enforces preflight -> setup -> assign -> start -> observed readiness ->
// grants -> final cutoff -> checkpoint/finalize.
func (c *Coordinator) Run(ctx context.Context, cfg Config) CoordinatorResult {
	if c == nil {
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeGenerationReuse}
	}
	c.mu.Lock()
	if c.used {
		c.mu.Unlock()
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeGenerationReuse}
	}
	c.used = true
	c.mu.Unlock()

	preflight := c.preflight.Check(ctx, cfg)
	if !preflight.TrafficAllowed() {
		outcome := CoordinatorHarnessInvalid
		if preflight.Outcome == PreflightInfrastructureFailure {
			outcome = CoordinatorInfrastructureFailure
		}
		return CoordinatorResult{Outcome: outcome, Code: CoordinatorCodePreflight}
	}
	if err := c.setup.Run(ctx, cfg); err != nil {
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeSetup}
	}
	assignments, err := BuildCoordinatorAssignments(cfg, c.generation)
	if err != nil {
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeAssignment}
	}
	grantPlan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeAssignment}
	}
	fence := assignments[0].WorkerFence
	result := CoordinatorResult{Fence: fence}
	attempted := [coordinatorWorkerCount]bool{true, true, true}
	assigned := [coordinatorWorkerCount]bool{}
	if _, disposition := c.assignRound(ctx, assignments); disposition != coordinatorRoundSucceeded {
		c.stopAfterFailure(fence, attempted)
		if disposition == coordinatorRoundParentCanceled {
			result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
			return result
		}
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeAssignment
		return result
	}
	if ctx.Err() != nil {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
		return result
	}
	assigned = [coordinatorWorkerCount]bool{true, true, true}
	startStatuses, disposition := c.startRound(ctx, assignments, fence)
	if disposition != coordinatorRoundSucceeded {
		c.stopAfterFailure(fence, attempted)
		if disposition == coordinatorRoundParentCanceled {
			result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
			return result
		}
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeStart
		return result
	}
	if ctx.Err() != nil {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
		return result
	}

	observationContext, cancelObservation := context.WithCancel(ctx)
	observationChannel := make(chan ObserverResult, 1)
	go func() {
		observationChannel <- c.observer.Run(observationContext, cfg)
		cancelObservation()
	}()
	var observation ObserverResult
	observationJoined := false
	joinObservation := func() ObserverResult {
		if !observationJoined {
			cancelObservation()
			observation = <-observationChannel
			observationJoined = true
		}
		return observation
	}
	joinFailureObservation := func() {
		select {
		case observation = <-observationChannel:
			observationJoined = true
		default:
			observation = joinObservation()
		}
	}
	completeParentCancellation := func() CoordinatorResult {
		reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeStopped, coordinatorRoundParentCanceled)
		observation = joinObservation()
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	}

	ready, earlyObservation, observerDone, readyDisposition := c.waitForTrafficReady(
		observationContext, observationChannel, assignments, startStatuses, cfg.Thresholds.Timeline.Warmup,
	)
	if !ready {
		failureCode := CoordinatorCodeRuntime
		if observerDone {
			observation, observationJoined = earlyObservation, true
			if readyDisposition == coordinatorRoundSucceeded {
				failureCode = CoordinatorCodeObserver
			}
		}
		if readyDisposition == coordinatorRoundSucceeded {
			readyDisposition = coordinatorRoundStageFailed
		}
		reason := lockCoordinatorTerminationReason(ctx, failureCode, readyDisposition)
		observation = joinObservation()
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	}
	select {
	case observation = <-observationChannel:
		observationJoined = true
		observationDisposition := coordinatorRoundStageFailed
		if observation.Outcome == ObserverStopped {
			observationDisposition = coordinatorRoundParentCanceled
		}
		reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeObserver, observationDisposition)
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	default:
	}
	grant, err := grantPlan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeGrant, coordinatorRoundStageFailed)
		joinFailureObservation()
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	}
	result.Grant = grant
	if grantDisposition := c.deliverGrant(
		observationContext, assignments, grantPlan.request(fence, grant),
	); grantDisposition != coordinatorRoundSucceeded {
		reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeGrant, grantDisposition)
		joinFailureObservation()
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	}

	grantBarrierAt := c.clock.Now()
	observationDeadline := grantBarrierAt.Add(cfg.Thresholds.Timeline.Final)
	statusTicker := c.clock.NewTicker(cfg.Observation.Cadence)
	if statusTicker == nil {
		reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeRuntime, coordinatorRoundStageFailed)
		observation = joinObservation()
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	}
	defer statusTicker.Stop()
	grantTickerStartedAt := c.clock.Now()
	grantTicker := c.clock.NewTicker(coordinatorGrantCadence)
	if grantTicker == nil {
		reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeRuntime, coordinatorRoundStageFailed)
		observation = joinObservation()
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	}
	defer grantTicker.Stop()
	lastGrantTickAt := grantTickerStartedAt
	haveGrantTick := false
	deliverScheduledGrant := func(tickAt time.Time) coordinatorRoundDisposition {
		now := c.clock.Now()
		if !validCoordinatorGrantTick(now, tickAt, lastGrantTickAt, haveGrantTick) {
			return coordinatorRoundStageFailed
		}
		grant, grantErr := grantPlan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
		if grantErr != nil {
			return coordinatorRoundStageFailed
		}
		if disposition := c.deliverGrant(
			observationContext, assignments, grantPlan.request(fence, grant),
		); disposition != coordinatorRoundSucceeded {
			return disposition
		}
		lastGrantTickAt, haveGrantTick = tickAt, true
		result.Grant = grant
		return coordinatorRoundSucceeded
	}
	grantCoverageMissing := func(at time.Time) bool {
		return at.Sub(lastGrantTickAt) > coordinatorGrantCadence
	}
	cutoffOwned := false
	failureCode := CoordinatorCode("")
	failureDisposition := coordinatorRoundStageFailed
	var failureReason coordinatorTerminationReason
	for {
		now := c.clock.Now()
		if !now.Before(observationDeadline) {
			select {
			case tickAt := <-grantTicker.C():
				if tickAt.IsZero() || tickAt.After(now) {
					failureCode = CoordinatorCodeGrant
					goto observationFailure
				}
				if tickAt.Before(observationDeadline) {
					if disposition := deliverScheduledGrant(tickAt); disposition != coordinatorRoundSucceeded {
						failureCode = CoordinatorCodeGrant
						failureDisposition = disposition
						goto observationFailure
					}
					continue
				}
			default:
			}
			if grantCoverageMissing(observationDeadline) {
				failureCode = CoordinatorCodeGrant
				goto observationFailure
			}
			observation = joinObservation()
			cutoffOwned = true
			goto observationComplete
		}

		select {
		case tickAt := <-grantTicker.C():
			if disposition := deliverScheduledGrant(tickAt); disposition != coordinatorRoundSucceeded {
				failureCode = CoordinatorCodeGrant
				failureDisposition = disposition
				goto observationFailure
			}
			continue
		default:
		}
		if grantCoverageMissing(now) {
			failureCode = CoordinatorCodeGrant
			goto observationFailure
		}

		select {
		case observation = <-observationChannel:
			observationJoined = true
			goto observationComplete
		case <-statusTicker.C():
			now = c.clock.Now()
			select {
			case tickAt := <-grantTicker.C():
				if tickAt.IsZero() || tickAt.After(now) {
					failureCode = CoordinatorCodeGrant
					goto observationFailure
				}
				if now.Before(observationDeadline) || tickAt.Before(observationDeadline) {
					if disposition := deliverScheduledGrant(tickAt); disposition != coordinatorRoundSucceeded {
						failureCode = CoordinatorCodeGrant
						failureDisposition = disposition
						goto observationFailure
					}
				}
			default:
			}
			if ctx.Err() != nil {
				return completeParentCancellation()
			}
			now = c.clock.Now()
			if !now.Before(observationDeadline) {
				if grantCoverageMissing(observationDeadline) {
					failureCode = CoordinatorCodeGrant
					goto observationFailure
				}
				observation = joinObservation()
				cutoffOwned = true
				goto observationComplete
			}
			if grantCoverageMissing(now) {
				failureCode = CoordinatorCodeGrant
				goto observationFailure
			}
			statuses, disposition := c.statusRound(observationContext, assignments)
			if disposition != coordinatorRoundSucceeded || !allCoordinatorTrafficReady(statuses) {
				failureCode = CoordinatorCodeRuntime
				failureDisposition = disposition
				if disposition == coordinatorRoundSucceeded {
					failureDisposition = coordinatorRoundStageFailed
				}
				goto observationFailure
			}
		case tickAt := <-grantTicker.C():
			if ctx.Err() != nil {
				return completeParentCancellation()
			}
			now = c.clock.Now()
			if !now.Before(observationDeadline) && !tickAt.Before(observationDeadline) {
				if tickAt.IsZero() || tickAt.After(now) || grantCoverageMissing(observationDeadline) {
					failureCode = CoordinatorCodeGrant
					goto observationFailure
				}
				observation = joinObservation()
				cutoffOwned = true
				goto observationComplete
			}
			if disposition := deliverScheduledGrant(tickAt); disposition != coordinatorRoundSucceeded {
				failureCode = CoordinatorCodeGrant
				failureDisposition = disposition
				goto observationFailure
			}
		case <-ctx.Done():
			return completeParentCancellation()
		}
	}

observationFailure:
	failureReason = lockCoordinatorTerminationReason(ctx, failureCode, failureDisposition)
	joinFailureObservation()
	result.Outcome, result.Code = coordinatorConcurrentFailure(observation, failureReason)
	c.stopAfterFailure(fence, attempted)
	return result

observationComplete:
	if observation.Outcome != ObserverStopped {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeObserver
		if observation.Outcome == ObserverProductFailure {
			result.Outcome = CoordinatorProductFailure
		}
		return result
	}
	if ctx.Err() != nil {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
		return result
	}
	if !cutoffOwned {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeObserver
		return result
	}

	aggregator, err := NewCoordinatorSnapshotAggregator(fence)
	if err != nil {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
		return result
	}
	checkpoint, disposition := c.checkpointRound(ctx, assignments, fence)
	if disposition != coordinatorRoundSucceeded {
		c.stopAfterFailure(fence, attempted)
		if disposition == coordinatorRoundParentCanceled {
			result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
			return result
		}
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
		return result
	}
	if ctx.Err() != nil {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
		return result
	}
	if _, err := aggregator.Aggregate(checkpoint); err != nil {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
		return result
	}
	final, err := c.stopAssignedSnapshots(fence, assigned)
	if err != nil {
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeFinalize
		return result
	}
	aggregated, err := aggregator.Aggregate(final)
	if err != nil {
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeFinalize
		return result
	}
	result.Outcome, result.Code, result.Snapshot = CoordinatorCompleted, CoordinatorCodeCompleted, aggregated
	return result
}

func lockCoordinatorTerminationReason(
	ctx context.Context,
	fallback CoordinatorCode,
	disposition coordinatorRoundDisposition,
) coordinatorTerminationReason {
	if disposition == coordinatorRoundParentCanceled && ctx.Err() == nil {
		return coordinatorTerminationReason{
			fallback: CoordinatorCodeObserver, disposition: coordinatorRoundStageFailed,
		}
	}
	return coordinatorTerminationReason{fallback: fallback, disposition: disposition}
}

func coordinatorConcurrentFailure(
	observation ObserverResult,
	reason coordinatorTerminationReason,
) (CoordinatorOutcome, CoordinatorCode) {
	switch observation.Outcome {
	case ObserverProductFailure:
		return CoordinatorProductFailure, CoordinatorCodeObserver
	case ObserverHarnessInvalid:
		return CoordinatorHarnessInvalid, CoordinatorCodeObserver
	}
	if reason.disposition == coordinatorRoundParentCanceled {
		return CoordinatorStopped, CoordinatorCodeStopped
	}
	return CoordinatorHarnessInvalid, reason.fallback
}

func validCoordinatorStatus(status WorkerStatus, assignment WorkerAssignment, phase WorkerPhase) bool {
	return status.RunID == assignment.RunID && status.AssignmentID == assignment.AssignmentID &&
		status.Generation == assignment.Generation && status.WorkerID == assignment.WorkerID &&
		status.WorkerCount == assignment.WorkerCount && status.Phase == phase && !status.Unexpected
}

func allCoordinatorTrafficReady(statuses [coordinatorWorkerCount]WorkerStatus) bool {
	for _, status := range statuses {
		if !status.TrafficReady {
			return false
		}
	}
	return true
}

func (c *Coordinator) assignRound(parent context.Context, assignments []CoordinatorAssignment) ([coordinatorWorkerCount]WorkerStatus, coordinatorRoundDisposition) {
	if len(assignments) != coordinatorWorkerCount {
		return [coordinatorWorkerCount]WorkerStatus{}, coordinatorRoundStageFailed
	}
	roundContext, cancel := context.WithTimeoutCause(parent, c.roundTimeout, errCoordinatorRoundDeadline)
	defer cancel()
	type assignmentResult struct {
		status WorkerStatus
		coordinatorRoundEvidence
	}
	results := [coordinatorWorkerCount]assignmentResult{}
	var wait sync.WaitGroup
	wait.Add(coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		go func() {
			defer wait.Done()
			result := &results[workerID]
			status, err := c.workers[workerID].Assign(
				roundContext, assignments[workerID].WorkerAssignment,
			)
			result.status = status
			result.err = err
			result.valid = err == nil && validCoordinatorStatus(
				status, assignments[workerID].WorkerAssignment, WorkerPhaseAssigned,
			)
		}()
	}
	wait.Wait()
	var statuses [coordinatorWorkerCount]WorkerStatus
	var evidence [coordinatorWorkerCount]coordinatorRoundEvidence
	for workerID, result := range results {
		evidence[workerID] = result.coordinatorRoundEvidence
		statuses[workerID] = result.status
	}
	disposition := resolveCoordinatorRoundDisposition(parent, roundContext, evidence)
	if disposition != coordinatorRoundSucceeded {
		return [coordinatorWorkerCount]WorkerStatus{}, disposition
	}
	return statuses, disposition
}

func (c *Coordinator) startRound(
	parent context.Context,
	assignments []CoordinatorAssignment,
	fence WorkerFence,
) ([coordinatorWorkerCount]WorkerStatus, coordinatorRoundDisposition) {
	if len(assignments) != coordinatorWorkerCount {
		return [coordinatorWorkerCount]WorkerStatus{}, coordinatorRoundStageFailed
	}
	roundContext, cancel := context.WithTimeoutCause(parent, c.roundTimeout, errCoordinatorRoundDeadline)
	defer cancel()
	type startResult struct {
		status WorkerStatus
		coordinatorRoundEvidence
	}
	results := [coordinatorWorkerCount]startResult{}
	var wait sync.WaitGroup
	wait.Add(coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		go func() {
			defer wait.Done()
			result := &results[workerID]
			status, err := c.workers[workerID].Start(
				roundContext, WorkerStartRequest{WorkerFence: fence},
			)
			result.status = status
			result.err = err
			result.valid = err == nil && validCoordinatorStatus(
				status, assignments[workerID].WorkerAssignment, WorkerPhaseRunning,
			)
		}()
	}
	wait.Wait()
	var statuses [coordinatorWorkerCount]WorkerStatus
	var evidence [coordinatorWorkerCount]coordinatorRoundEvidence
	for workerID, result := range results {
		evidence[workerID] = result.coordinatorRoundEvidence
		statuses[workerID] = result.status
	}
	disposition := resolveCoordinatorRoundDisposition(parent, roundContext, evidence)
	if disposition != coordinatorRoundSucceeded {
		return [coordinatorWorkerCount]WorkerStatus{}, disposition
	}
	return statuses, disposition
}

func (c *Coordinator) checkpointRound(
	parent context.Context,
	assignments []CoordinatorAssignment,
	fence WorkerFence,
) ([]WorkerSnapshot, coordinatorRoundDisposition) {
	if len(assignments) != coordinatorWorkerCount {
		return nil, coordinatorRoundStageFailed
	}
	roundContext, cancel := context.WithTimeoutCause(parent, c.roundTimeout, errCoordinatorRoundDeadline)
	defer cancel()
	type checkpointResult struct {
		snapshot WorkerSnapshot
		coordinatorRoundEvidence
	}
	results := [coordinatorWorkerCount]checkpointResult{}
	var wait sync.WaitGroup
	wait.Add(coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		go func() {
			defer wait.Done()
			result := &results[workerID]
			snapshot, err := c.workers[workerID].Checkpoint(
				roundContext, WorkerCheckpointRequest{WorkerFence: fence},
			)
			result.snapshot = snapshot
			result.err = err
			result.valid = err == nil && snapshot.WorkerID == uint64(workerID) &&
				sameWorkerFence(WorkerFence{
					RunID: snapshot.RunID, AssignmentID: snapshot.AssignmentID, Generation: snapshot.Generation,
				}, assignments[workerID].WorkerFence)
		}()
	}
	wait.Wait()
	snapshots := make([]WorkerSnapshot, coordinatorWorkerCount)
	var evidence [coordinatorWorkerCount]coordinatorRoundEvidence
	for workerID, result := range results {
		evidence[workerID] = result.coordinatorRoundEvidence
		snapshots[workerID] = result.snapshot
	}
	disposition := resolveCoordinatorRoundDisposition(parent, roundContext, evidence)
	if disposition != coordinatorRoundSucceeded {
		return nil, disposition
	}
	return snapshots, disposition
}

func resolveCoordinatorRoundDisposition(
	parent context.Context,
	roundContext context.Context,
	evidence [coordinatorWorkerCount]coordinatorRoundEvidence,
) coordinatorRoundDisposition {
	if errors.Is(context.Cause(roundContext), errCoordinatorRoundDeadline) {
		return coordinatorRoundStageFailed
	}
	parentErr := parent.Err()
	allValid := true
	for _, result := range evidence {
		if result.valid {
			continue
		}
		allValid = false
		if result.err == nil || parentErr == nil || !errors.Is(result.err, parentErr) {
			return coordinatorRoundStageFailed
		}
	}
	if parentErr != nil || parent.Err() != nil {
		return coordinatorRoundParentCanceled
	}
	if roundContext.Err() != nil || !allValid {
		return coordinatorRoundStageFailed
	}
	return coordinatorRoundSucceeded
}

func (c *Coordinator) waitForTrafficReady(
	ctx context.Context,
	observation <-chan ObserverResult,
	assignments []CoordinatorAssignment,
	statuses [coordinatorWorkerCount]WorkerStatus,
	maximumWait time.Duration,
) (ready bool, result ObserverResult, observerDone bool, disposition coordinatorRoundDisposition) {
	select {
	case result = <-observation:
		return false, result, true, coordinatorRoundParentCanceled
	default:
	}
	if allCoordinatorTrafficReady(statuses) {
		return true, ObserverResult{}, false, coordinatorRoundSucceeded
	}
	ticker := c.clock.NewTicker(time.Second)
	if ticker == nil {
		return false, ObserverResult{}, false, coordinatorRoundStageFailed
	}
	defer ticker.Stop()
	deadline := c.clock.Now().Add(maximumWait)
	for {
		select {
		case result = <-observation:
			return false, result, true, coordinatorRoundParentCanceled
		case <-ctx.Done():
			select {
			case result = <-observation:
				return false, result, true, coordinatorRoundParentCanceled
			default:
				return false, ObserverResult{}, false, coordinatorRoundParentCanceled
			}
		case <-ticker.C():
			now := c.clock.Now()
			if !now.Before(deadline) {
				return false, ObserverResult{}, false, coordinatorRoundStageFailed
			}
			observed, roundDisposition := c.statusRoundWithin(ctx, assignments, deadline.Sub(now))
			if roundDisposition != coordinatorRoundSucceeded {
				select {
				case result = <-observation:
					return false, result, true, roundDisposition
				default:
					return false, ObserverResult{}, false, roundDisposition
				}
			}
			if !c.clock.Now().Before(deadline) {
				return false, ObserverResult{}, false, coordinatorRoundStageFailed
			}
			if allCoordinatorTrafficReady(observed) {
				return true, ObserverResult{}, false, coordinatorRoundSucceeded
			}
		}
	}
}

func validCoordinatorGrantTick(now, tickAt, lastTickAt time.Time, haveLastTick bool) bool {
	if now.IsZero() || tickAt.IsZero() || lastTickAt.IsZero() || tickAt.After(now) {
		return false
	}
	age := now.Sub(tickAt)
	if age < 0 || age >= coordinatorGrantCadence {
		return false
	}
	interval := tickAt.Sub(lastTickAt)
	if haveLastTick {
		return interval == coordinatorGrantCadence
	}
	return interval >= coordinatorGrantCadence && interval < 2*coordinatorGrantCadence
}

func (c *Coordinator) deliverGrant(
	parent context.Context,
	assignments []CoordinatorAssignment,
	request WorkerGrantRequest,
) coordinatorRoundDisposition {
	grantRoundTimeout := min(c.roundTimeout, coordinatorGrantCadence)
	roundContext, cancel := context.WithTimeoutCause(parent, grantRoundTimeout, errCoordinatorRoundDeadline)
	defer cancel()
	type grantResult struct {
		response WorkerGrantResponse
		err      error
	}
	results := [coordinatorWorkerCount]grantResult{}
	var wait sync.WaitGroup
	wait.Add(coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		go func() {
			defer wait.Done()
			for attempt := 0; attempt < 2; attempt++ {
				results[workerID].response, results[workerID].err = c.workers[workerID].Grant(roundContext, request)
				if results[workerID].err == nil || roundContext.Err() != nil {
					return
				}
				var apiError *WorkerAPIError
				if errors.As(results[workerID].err, &apiError) {
					return
				}
			}
		}()
	}
	wait.Wait()
	var evidence [coordinatorWorkerCount]coordinatorRoundEvidence
	for workerID, result := range results {
		expectedReleased, _ := request.Released.worker(uint64(workerID))
		evidence[workerID] = coordinatorRoundEvidence{err: result.err, valid: result.err == nil &&
			sameWorkerFence(result.response.WorkerFence, assignments[workerID].WorkerFence) &&
			result.response.WorkerID == uint64(workerID) && result.response.WorkerCount == coordinatorWorkerCount &&
			result.response.Sequence == request.Sequence && result.response.Released == expectedReleased}
	}
	return resolveCoordinatorRoundDisposition(parent, roundContext, evidence)
}

func (c *Coordinator) statusRound(
	parent context.Context,
	assignments []CoordinatorAssignment,
) ([coordinatorWorkerCount]WorkerStatus, coordinatorRoundDisposition) {
	return c.statusRoundWithin(parent, assignments, c.roundTimeout)
}

func (c *Coordinator) statusRoundWithin(
	parent context.Context,
	assignments []CoordinatorAssignment,
	maximum time.Duration,
) ([coordinatorWorkerCount]WorkerStatus, coordinatorRoundDisposition) {
	if maximum <= 0 {
		return [coordinatorWorkerCount]WorkerStatus{}, coordinatorRoundStageFailed
	}
	roundContext, cancel := context.WithTimeoutCause(parent, min(c.roundTimeout, maximum), errCoordinatorRoundDeadline)
	defer cancel()
	type statusResult struct {
		status WorkerStatus
		coordinatorRoundEvidence
	}
	results := [coordinatorWorkerCount]statusResult{}
	var statuses [coordinatorWorkerCount]WorkerStatus
	var wait sync.WaitGroup
	wait.Add(coordinatorWorkerCount)
	for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
		go func() {
			defer wait.Done()
			result := &results[workerID]
			result.status, result.err = c.workers[workerID].Status(roundContext)
			result.valid = result.err == nil && validCoordinatorStatus(
				result.status, assignments[workerID].WorkerAssignment, WorkerPhaseRunning,
			)
		}()
	}
	wait.Wait()
	var evidence [coordinatorWorkerCount]coordinatorRoundEvidence
	for workerID, result := range results {
		evidence[workerID] = result.coordinatorRoundEvidence
		statuses[workerID] = result.status
	}
	disposition := resolveCoordinatorRoundDisposition(parent, roundContext, evidence)
	if disposition != coordinatorRoundSucceeded {
		return [coordinatorWorkerCount]WorkerStatus{}, disposition
	}
	return statuses, disposition
}

func (c *Coordinator) stopAfterFailure(fence WorkerFence, attempted [coordinatorWorkerCount]bool) {
	stopContext, cancel := context.WithTimeout(context.Background(), c.cleanupTimeout)
	defer cancel()
	var wait sync.WaitGroup
	for workerID, worker := range c.workers {
		if !attempted[workerID] {
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _ = worker.Stop(stopContext, WorkerStopRequest{WorkerFence: fence})
		}()
	}
	wait.Wait()
}

func (c *Coordinator) stopAssignedSnapshots(fence WorkerFence, assigned [coordinatorWorkerCount]bool) ([]WorkerSnapshot, error) {
	stopContext, cancel := context.WithTimeout(context.Background(), c.cleanupTimeout)
	defer cancel()
	type stopResult struct {
		snapshot WorkerSnapshot
		err      error
	}
	results := make([]stopResult, coordinatorWorkerCount)
	var wait sync.WaitGroup
	for workerID, worker := range c.workers {
		if !assigned[workerID] {
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[workerID].snapshot, results[workerID].err = worker.Stop(stopContext, WorkerStopRequest{WorkerFence: fence})
		}()
	}
	wait.Wait()
	snapshots := make([]WorkerSnapshot, coordinatorWorkerCount)
	for workerID, result := range results {
		if !assigned[workerID] || result.err != nil {
			return nil, ErrCoordinatorSnapshotCount
		}
		snapshots[workerID] = result.snapshot
	}
	return snapshots, nil
}

// CoordinatorPartition describes one interleaved global user-index lane and
// its positive share of the single global rate allocator.
type CoordinatorPartition struct {
	FirstGlobalIndex uint64
	Stride           uint64
	UserCount        uint64
	RateWeight       int64
}

// CoordinatorGrant is one fixed three-worker projection from the single
// coordinator-owned Phase 2 global token bucket.
type CoordinatorGrant struct {
	Sequence uint64
	Fresh    [coordinatorWorkerCount]uint64
	Released [coordinatorWorkerCount]uint64
	Credit   [coordinatorWorkerCount]uint64
}

// CoordinatorGrantPlan owns one global allocator and sequences complete grant
// vectors for all workers. Each worker applies only its indexed vector share.
type CoordinatorGrantPlan struct {
	rate      uint64
	burst     uint64
	allocator *RateAllocator
	sequence  uint64
}

// NewCoordinatorGrantPlan validates that exactly three assignments describe
// one fence, one global rate, and one positive weight vector.
func NewCoordinatorGrantPlan(assignments []CoordinatorAssignment) (*CoordinatorGrantPlan, error) {
	if len(assignments) != coordinatorWorkerCount {
		return nil, ErrCoordinatorConfig
	}
	first := assignments[0]
	rate := first.Config.Workload.SendRatePerSecond
	burst := first.Config.Workload.MaxGlobalBurst
	if rate <= 0 || burst <= 0 {
		return nil, ErrCoordinatorConfig
	}
	weights := make([]int64, coordinatorWorkerCount)
	for workerID, assignment := range assignments {
		if assignment.WorkerID != uint64(workerID) || assignment.WorkerCount != coordinatorWorkerCount ||
			!sameWorkerFence(assignment.WorkerFence, first.WorkerFence) ||
			assignment.Config.Workload.SendRatePerSecond != rate ||
			assignment.Config.Workload.MaxGlobalBurst != burst || assignment.Partition.RateWeight <= 0 {
			return nil, ErrCoordinatorConfig
		}
		weights[workerID] = assignment.Partition.RateWeight
	}
	allocator, err := NewRateAllocator(uint64(rate), uint64(burst), weights)
	if err != nil {
		return nil, ErrCoordinatorConfig
	}
	return &CoordinatorGrantPlan{
		rate: uint64(rate), burst: uint64(burst), allocator: allocator,
	}, nil
}

// Tick releases one vector and verifies the fixed-size global sums before it
// can become coordinator evidence.
func (p *CoordinatorGrantPlan) Tick(demand [coordinatorWorkerCount]uint64) (CoordinatorGrant, error) {
	if p == nil || p.allocator == nil {
		return CoordinatorGrant{}, ErrCoordinatorConfig
	}
	tick, err := p.allocator.Tick(demand[:])
	if err != nil || len(tick.Fresh) != coordinatorWorkerCount || len(tick.Released) != coordinatorWorkerCount || len(tick.Credit) != coordinatorWorkerCount {
		return CoordinatorGrant{}, ErrCoordinatorConfig
	}
	var grant CoordinatorGrant
	if p.sequence == math.MaxUint64 {
		return CoordinatorGrant{}, ErrCoordinatorConfig
	}
	p.sequence++
	grant.Sequence = p.sequence
	copy(grant.Fresh[:], tick.Fresh)
	copy(grant.Released[:], tick.Released)
	copy(grant.Credit[:], tick.Credit)
	var fresh, credit uint64
	for workerID := range grant.Fresh {
		if math.MaxUint64-fresh < grant.Fresh[workerID] || math.MaxUint64-credit < grant.Credit[workerID] {
			return CoordinatorGrant{}, ErrCoordinatorConfig
		}
		fresh += grant.Fresh[workerID]
		credit += grant.Credit[workerID]
	}
	if fresh != p.rate || credit > p.burst {
		return CoordinatorGrant{}, ErrCoordinatorConfig
	}
	return grant, nil
}

func (p *CoordinatorGrantPlan) request(fence WorkerFence, grant CoordinatorGrant) WorkerGrantRequest {
	return WorkerGrantRequest{
		WorkerFence: fence, Sequence: grant.Sequence, RatePerSecond: p.rate, MaxBurst: p.burst,
		Fresh: WorkerGrantCounts{
			Worker0: grant.Fresh[0], Worker1: grant.Fresh[1], Worker2: grant.Fresh[2],
		},
		Released: WorkerGrantCounts{
			Worker0: grant.Released[0], Worker1: grant.Released[1], Worker2: grant.Released[2],
		},
		Credit: WorkerGrantCounts{
			Worker0: grant.Credit[0], Worker1: grant.Credit[1], Worker2: grant.Credit[2],
		},
	}
}

// CoordinatorAssignment combines the live worker protocol assignment with
// the coordinator-owned proof of its deterministic user and rate partition.
type CoordinatorAssignment struct {
	WorkerAssignment
	Partition CoordinatorPartition
}

// CoordinatorSnapshot is one checked aggregate over exactly three bounded
// worker snapshots. It retains fixed evidence counts, never identities or raw history.
type CoordinatorSnapshot struct {
	RunID          string
	AssignmentID   string
	Generation     uint64
	WorkerCount    uint64
	Phase          WorkerPhase
	MinimumUptime  time.Duration
	WorkerSequence [coordinatorWorkerCount]uint64

	Sessions       WorkerSessionSnapshot
	Generated      WorkerGeneratedSnapshot
	Messages       WorkerMessageSnapshot
	Sync           WorkerSyncSnapshot
	SendackLatency WorkerHistogramSnapshot
	RecvackLatency WorkerHistogramSnapshot
	Correlation    WorkerCorrelationSnapshot
	Queues         WorkerQueueSnapshot
	Harness        WorkerHarnessSnapshot

	EvidenceClassification SyncClassification
	EvidenceCounts         [FailureClassHarness + 1]uint64
}

// CoordinatorSnapshotAggregator owns the three fixed worker baselines used to
// reject stale observations and monotonic counter regressions.
type CoordinatorSnapshotAggregator struct {
	mu       sync.Mutex
	fence    WorkerFence
	seen     [coordinatorWorkerCount]bool
	previous [coordinatorWorkerCount]WorkerSnapshot
	evidence [coordinatorWorkerCount][FailureClassHarness + 1]uint64
}

// NewCoordinatorSnapshotAggregator binds all future snapshots to one exact fence.
func NewCoordinatorSnapshotAggregator(fence WorkerFence) (*CoordinatorSnapshotAggregator, error) {
	if !validWorkerFence(fence) {
		return nil, ErrCoordinatorConfig
	}
	return &CoordinatorSnapshotAggregator{fence: fence}, nil
}

// Aggregate validates a complete three-worker generation before atomically
// advancing any baseline.
func (a *CoordinatorSnapshotAggregator) Aggregate(snapshots []WorkerSnapshot) (CoordinatorSnapshot, error) {
	if a == nil || len(snapshots) != coordinatorWorkerCount {
		return CoordinatorSnapshot{}, ErrCoordinatorSnapshotCount
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	var ordered [coordinatorWorkerCount]WorkerSnapshot
	var evidence [coordinatorWorkerCount][FailureClassHarness + 1]uint64
	var present [coordinatorWorkerCount]bool
	phase := WorkerPhase("")
	for _, snapshot := range snapshots {
		if snapshot.RunID != a.fence.RunID || snapshot.AssignmentID != a.fence.AssignmentID ||
			snapshot.Generation != a.fence.Generation || snapshot.WorkerCount != coordinatorWorkerCount ||
			snapshot.WorkerID >= coordinatorWorkerCount || present[snapshot.WorkerID] ||
			(snapshot.Phase != WorkerPhaseRunning && snapshot.Phase != WorkerPhaseFinal) || !validWorkerSnapshot(snapshot) {
			return CoordinatorSnapshot{}, ErrCoordinatorSnapshotFence
		}
		if phase == "" {
			phase = snapshot.Phase
		} else if phase != snapshot.Phase {
			return CoordinatorSnapshot{}, ErrCoordinatorSnapshotFence
		}
		if snapshot.SnapshotSequence == 0 || snapshot.Uptime <= 0 {
			return CoordinatorSnapshot{}, ErrCoordinatorSnapshotStale
		}
		if !validCoordinatorSnapshotHistograms(snapshot) {
			return CoordinatorSnapshot{}, ErrCoordinatorHistogramSchema
		}
		workerID := int(snapshot.WorkerID)
		currentEvidence, ok := coordinatorEvidenceCounts(snapshot.Evidence)
		if !ok {
			return CoordinatorSnapshot{}, ErrCoordinatorSnapshotFence
		}
		if a.seen[workerID] {
			previous := a.previous[workerID]
			if snapshot.SnapshotSequence <= previous.SnapshotSequence || snapshot.Uptime <= previous.Uptime {
				return CoordinatorSnapshot{}, ErrCoordinatorSnapshotStale
			}
			if coordinatorSnapshotRegressed(snapshot, currentEvidence, previous, a.evidence[workerID]) {
				return CoordinatorSnapshot{}, ErrCoordinatorSnapshotRegression
			}
		}
		present[workerID] = true
		ordered[workerID] = snapshot
		evidence[workerID] = currentEvidence
	}
	for _, found := range present {
		if !found {
			return CoordinatorSnapshot{}, ErrCoordinatorSnapshotFence
		}
	}

	aggregated, err := aggregateCoordinatorSnapshots(a.fence, phase, ordered, evidence)
	if err != nil {
		return CoordinatorSnapshot{}, err
	}
	for workerID := range ordered {
		baseline := ordered[workerID]
		baseline.Evidence.Classes = nil
		a.previous[workerID] = baseline
		a.evidence[workerID] = evidence[workerID]
		a.seen[workerID] = true
	}
	return aggregated, nil
}

func validCoordinatorSnapshotHistograms(snapshot WorkerSnapshot) bool {
	return validCoordinatorHistogram(snapshot.Sync.ConnectLatency) &&
		validCoordinatorHistogram(snapshot.Sync.Latency) &&
		validCoordinatorHistogram(snapshot.SendackLatency) &&
		validCoordinatorHistogram(snapshot.RecvackLatency)
}

func validCoordinatorHistogram(histogram WorkerHistogramSnapshot) bool {
	if histogram.BucketUpper != workerLatencyBucketUpperNanos {
		return false
	}
	var total uint64
	for _, count := range histogram.Buckets {
		if math.MaxUint64-total < count {
			return false
		}
		total += count
	}
	return total == histogram.Count
}

func coordinatorEvidenceCounts(snapshot EvidenceSnapshot) ([FailureClassHarness + 1]uint64, bool) {
	var counts [FailureClassHarness + 1]uint64
	for _, class := range snapshot.Classes {
		if class.Class < FailureClassSend || class.Class > FailureClassHarness || counts[class.Class] != 0 {
			return counts, false
		}
		counts[class.Class] = class.Count
	}
	return counts, true
}

func coordinatorSnapshotRegressed(
	current WorkerSnapshot,
	currentEvidence [FailureClassHarness + 1]uint64,
	previous WorkerSnapshot,
	previousEvidence [FailureClassHarness + 1]uint64,
) bool {
	if current.Sessions.Target != previous.Sessions.Target ||
		current.Queues.WorkCapacity != previous.Queues.WorkCapacity ||
		current.Queues.RetryCapacity != previous.Queues.RetryCapacity ||
		current.Queues.InflightCapacity != previous.Queues.InflightCapacity ||
		current.Queues.TransportCapacity != previous.Queues.TransportCapacity ||
		coordinatorClassificationRank(current.Harness.Classification) < coordinatorClassificationRank(previous.Harness.Classification) ||
		coordinatorClassificationRank(current.Evidence.Classification) < coordinatorClassificationRank(previous.Evidence.Classification) {
		return true
	}
	currentCounters := []uint64{
		current.Sessions.PlannedNew, current.Sessions.PlannedReturning, current.Sessions.CompletedNew,
		current.Sessions.CompletedReturning, current.Sessions.Expired,
		current.Generated.Primary, current.Generated.Person, current.Generated.Group,
		current.Generated.Canary, current.Generated.PayloadBytes,
		current.Messages.Sent, current.Messages.SendAttempts, current.Messages.SendAcknowledged,
		current.Messages.SendRejected, current.Messages.Received, current.Messages.ReceiveAcknowledged,
		current.Messages.ReceiveAckFailures, current.Messages.RetryAttempts, current.Messages.Terminal,
		current.Sync.CompletedNew, current.Sync.CompletedReturning, current.Sync.FactoryFailed,
		current.Sync.FactoryCanceled, current.Sync.ConnectStarted, current.Sync.ConnectCompleted,
		current.Sync.ConnectFailed, current.Sync.ConnectCanceled, current.Sync.SyncStarted,
		current.Sync.SyncCompleted, current.Sync.SyncFailed, current.Sync.SyncCanceled, current.Sync.Failures,
		current.Correlation.Sampled, current.Correlation.Delivered, current.Correlation.Expired,
		current.Correlation.DuplicateCompletions, current.Correlation.ConflictingCompletions,
		current.Correlation.UnknownAcknowledgments,
		current.Harness.Failures, current.Harness.CommandSaturation, current.Harness.OfferedUnderdelivery,
	}
	previousCounters := []uint64{
		previous.Sessions.PlannedNew, previous.Sessions.PlannedReturning, previous.Sessions.CompletedNew,
		previous.Sessions.CompletedReturning, previous.Sessions.Expired,
		previous.Generated.Primary, previous.Generated.Person, previous.Generated.Group,
		previous.Generated.Canary, previous.Generated.PayloadBytes,
		previous.Messages.Sent, previous.Messages.SendAttempts, previous.Messages.SendAcknowledged,
		previous.Messages.SendRejected, previous.Messages.Received, previous.Messages.ReceiveAcknowledged,
		previous.Messages.ReceiveAckFailures, previous.Messages.RetryAttempts, previous.Messages.Terminal,
		previous.Sync.CompletedNew, previous.Sync.CompletedReturning, previous.Sync.FactoryFailed,
		previous.Sync.FactoryCanceled, previous.Sync.ConnectStarted, previous.Sync.ConnectCompleted,
		previous.Sync.ConnectFailed, previous.Sync.ConnectCanceled, previous.Sync.SyncStarted,
		previous.Sync.SyncCompleted, previous.Sync.SyncFailed, previous.Sync.SyncCanceled, previous.Sync.Failures,
		previous.Correlation.Sampled, previous.Correlation.Delivered, previous.Correlation.Expired,
		previous.Correlation.DuplicateCompletions, previous.Correlation.ConflictingCompletions,
		previous.Correlation.UnknownAcknowledgments,
		previous.Harness.Failures, previous.Harness.CommandSaturation, previous.Harness.OfferedUnderdelivery,
	}
	for index := range currentCounters {
		if currentCounters[index] < previousCounters[index] {
			return true
		}
	}
	if current.Queues.WorkPeak < previous.Queues.WorkPeak ||
		current.Queues.RetryPeak < previous.Queues.RetryPeak ||
		current.Queues.InflightPeak < previous.Queues.InflightPeak ||
		(previous.Harness.DrainTimedOut && !current.Harness.DrainTimedOut) ||
		(previous.Harness.UnexpectedExit && !current.Harness.UnexpectedExit) {
		return true
	}
	for index := FailureClassSend; index <= FailureClassHarness; index++ {
		if currentEvidence[index] < previousEvidence[index] {
			return true
		}
	}
	return coordinatorHistogramRegressed(current.Sync.ConnectLatency, previous.Sync.ConnectLatency) ||
		coordinatorHistogramRegressed(current.Sync.Latency, previous.Sync.Latency) ||
		coordinatorHistogramRegressed(current.SendackLatency, previous.SendackLatency) ||
		coordinatorHistogramRegressed(current.RecvackLatency, previous.RecvackLatency)
}

func coordinatorHistogramRegressed(current, previous WorkerHistogramSnapshot) bool {
	if current.Count < previous.Count || current.SumNanos < previous.SumNanos || current.MaxNanos < previous.MaxNanos {
		return true
	}
	for index := range current.Buckets {
		if current.Buckets[index] < previous.Buckets[index] {
			return true
		}
	}
	return false
}

func coordinatorClassificationRank(classification SyncClassification) int {
	switch classification {
	case "":
		return 0
	case SyncClassificationHarnessInvalid:
		return 1
	case SyncClassificationProductFailure:
		return 2
	default:
		return 1
	}
}

func aggregateCoordinatorSnapshots(
	fence WorkerFence,
	phase WorkerPhase,
	snapshots [coordinatorWorkerCount]WorkerSnapshot,
	evidence [coordinatorWorkerCount][FailureClassHarness + 1]uint64,
) (CoordinatorSnapshot, error) {
	result := CoordinatorSnapshot{
		RunID: fence.RunID, AssignmentID: fence.AssignmentID, Generation: fence.Generation,
		WorkerCount: coordinatorWorkerCount, Phase: phase,
		Sync: WorkerSyncSnapshot{
			ConnectLatency: newWorkerHistogramSnapshot(), Latency: newWorkerHistogramSnapshot(),
		},
		SendackLatency: newWorkerHistogramSnapshot(),
		RecvackLatency: newWorkerHistogramSnapshot(),
	}
	for workerID, snapshot := range snapshots {
		result.WorkerSequence[workerID] = snapshot.SnapshotSequence
		if result.MinimumUptime == 0 || snapshot.Uptime < result.MinimumUptime {
			result.MinimumUptime = snapshot.Uptime
		}
		if err := addCoordinatorSessions(&result.Sessions, snapshot.Sessions); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorGenerated(&result.Generated, snapshot.Generated); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorMessages(&result.Messages, snapshot.Messages); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorSync(&result.Sync, snapshot.Sync); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorHistogram(&result.SendackLatency, snapshot.SendackLatency); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorHistogram(&result.RecvackLatency, snapshot.RecvackLatency); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorCorrelation(&result.Correlation, snapshot.Correlation); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorQueues(&result.Queues, snapshot.Queues); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorHarness(&result.Harness, snapshot.Harness); err != nil {
			return CoordinatorSnapshot{}, err
		}
		result.EvidenceClassification = mergeSyncClassification(result.EvidenceClassification, snapshot.Evidence.Classification)
		for class := FailureClassSend; class <= FailureClassHarness; class++ {
			if err := addCoordinatorUint64(&result.EvidenceCounts[class], evidence[workerID][class]); err != nil {
				return CoordinatorSnapshot{}, err
			}
		}
	}
	return result, nil
}

func addCoordinatorSessions(total *WorkerSessionSnapshot, value WorkerSessionSnapshot) error {
	if err := addCoordinatorInt(&total.Target, value.Target); err != nil {
		return err
	}
	if err := addCoordinatorInt(&total.Online, value.Online); err != nil {
		return err
	}
	if err := addCoordinatorInt(&total.Starting, value.Starting); err != nil {
		return err
	}
	if err := addCoordinatorInt(&total.TrafficReady, value.TrafficReady); err != nil {
		return err
	}
	values := [5]struct{ destination, source *uint64 }{
		{&total.PlannedNew, &value.PlannedNew}, {&total.PlannedReturning, &value.PlannedReturning},
		{&total.CompletedNew, &value.CompletedNew}, {&total.CompletedReturning, &value.CompletedReturning},
		{&total.Expired, &value.Expired},
	}
	return addCoordinatorUint64Fields(values[:])
}

func addCoordinatorGenerated(total *WorkerGeneratedSnapshot, value WorkerGeneratedSnapshot) error {
	values := [5]struct{ destination, source *uint64 }{
		{&total.Primary, &value.Primary}, {&total.Person, &value.Person}, {&total.Group, &value.Group},
		{&total.Canary, &value.Canary}, {&total.PayloadBytes, &value.PayloadBytes},
	}
	return addCoordinatorUint64Fields(values[:])
}

func addCoordinatorMessages(total *WorkerMessageSnapshot, value WorkerMessageSnapshot) error {
	values := [9]struct{ destination, source *uint64 }{
		{&total.Sent, &value.Sent}, {&total.SendAttempts, &value.SendAttempts},
		{&total.SendAcknowledged, &value.SendAcknowledged}, {&total.SendRejected, &value.SendRejected},
		{&total.Received, &value.Received}, {&total.ReceiveAcknowledged, &value.ReceiveAcknowledged},
		{&total.ReceiveAckFailures, &value.ReceiveAckFailures}, {&total.RetryAttempts, &value.RetryAttempts},
		{&total.Terminal, &value.Terminal},
	}
	return addCoordinatorUint64Fields(values[:])
}

func addCoordinatorSync(total *WorkerSyncSnapshot, value WorkerSyncSnapshot) error {
	values := [13]struct{ destination, source *uint64 }{
		{&total.CompletedNew, &value.CompletedNew}, {&total.CompletedReturning, &value.CompletedReturning},
		{&total.FactoryFailed, &value.FactoryFailed}, {&total.FactoryCanceled, &value.FactoryCanceled},
		{&total.ConnectStarted, &value.ConnectStarted}, {&total.ConnectCompleted, &value.ConnectCompleted},
		{&total.ConnectFailed, &value.ConnectFailed}, {&total.ConnectCanceled, &value.ConnectCanceled},
		{&total.SyncStarted, &value.SyncStarted}, {&total.SyncCompleted, &value.SyncCompleted},
		{&total.SyncFailed, &value.SyncFailed}, {&total.SyncCanceled, &value.SyncCanceled},
		{&total.Failures, &value.Failures},
	}
	if err := addCoordinatorUint64Fields(values[:]); err != nil {
		return err
	}
	if err := addCoordinatorHistogram(&total.ConnectLatency, value.ConnectLatency); err != nil {
		return err
	}
	return addCoordinatorHistogram(&total.Latency, value.Latency)
}

func addCoordinatorHistogram(total *WorkerHistogramSnapshot, value WorkerHistogramSnapshot) error {
	if total.BucketUpper != value.BucketUpper {
		return ErrCoordinatorHistogramSchema
	}
	if err := addCoordinatorUint64(&total.Count, value.Count); err != nil {
		return err
	}
	if err := addCoordinatorUint64(&total.SumNanos, value.SumNanos); err != nil {
		return err
	}
	if value.MaxNanos > total.MaxNanos {
		total.MaxNanos = value.MaxNanos
	}
	for index := range total.Buckets {
		if err := addCoordinatorUint64(&total.Buckets[index], value.Buckets[index]); err != nil {
			return err
		}
	}
	return nil
}

func addCoordinatorCorrelation(total *WorkerCorrelationSnapshot, value WorkerCorrelationSnapshot) error {
	if err := addCoordinatorInt(&total.PendingUnfinished, value.PendingUnfinished); err != nil {
		return err
	}
	if err := addCoordinatorInt(&total.Outstanding, value.Outstanding); err != nil {
		return err
	}
	values := [6]struct{ destination, source *uint64 }{
		{&total.Sampled, &value.Sampled}, {&total.Delivered, &value.Delivered}, {&total.Expired, &value.Expired},
		{&total.DuplicateCompletions, &value.DuplicateCompletions},
		{&total.ConflictingCompletions, &value.ConflictingCompletions},
		{&total.UnknownAcknowledgments, &value.UnknownAcknowledgments},
	}
	return addCoordinatorUint64Fields(values[:])
}

func addCoordinatorQueues(total *WorkerQueueSnapshot, value WorkerQueueSnapshot) error {
	fields := [11]struct{ destination, source *int }{
		{&total.WorkCurrent, &value.WorkCurrent}, {&total.WorkPeak, &value.WorkPeak},
		{&total.WorkCapacity, &value.WorkCapacity}, {&total.RetryCurrent, &value.RetryCurrent},
		{&total.RetryPeak, &value.RetryPeak}, {&total.RetryCapacity, &value.RetryCapacity},
		{&total.InflightCurrent, &value.InflightCurrent}, {&total.InflightPeak, &value.InflightPeak},
		{&total.InflightCapacity, &value.InflightCapacity}, {&total.TransportCurrent, &value.TransportCurrent},
		{&total.TransportCapacity, &value.TransportCapacity},
	}
	for _, field := range fields {
		if err := addCoordinatorInt(field.destination, *field.source); err != nil {
			return err
		}
	}
	return nil
}

func addCoordinatorHarness(total *WorkerHarnessSnapshot, value WorkerHarnessSnapshot) error {
	total.Classification = mergeSyncClassification(total.Classification, value.Classification)
	values := [3]struct{ destination, source *uint64 }{
		{&total.Failures, &value.Failures}, {&total.CommandSaturation, &value.CommandSaturation},
		{&total.OfferedUnderdelivery, &value.OfferedUnderdelivery},
	}
	if err := addCoordinatorUint64Fields(values[:]); err != nil {
		return err
	}
	total.DrainTimedOut = total.DrainTimedOut || value.DrainTimedOut
	total.UnexpectedExit = total.UnexpectedExit || value.UnexpectedExit
	return nil
}

func addCoordinatorUint64Fields(fields []struct{ destination, source *uint64 }) error {
	for _, field := range fields {
		if err := addCoordinatorUint64(field.destination, *field.source); err != nil {
			return err
		}
	}
	return nil
}

func addCoordinatorUint64(destination *uint64, value uint64) error {
	if math.MaxUint64-*destination < value {
		return ErrCoordinatorSnapshotOverflow
	}
	*destination += value
	return nil
}

func addCoordinatorInt(destination *int, value int) error {
	if value < 0 || *destination < 0 || value > math.MaxInt-*destination {
		return ErrCoordinatorSnapshotOverflow
	}
	*destination += value
	return nil
}

// BuildCoordinatorAssignments creates exactly three immutable worker fences.
func BuildCoordinatorAssignments(cfg Config, generation uint64) ([]CoordinatorAssignment, error) {
	if cfg.Validate() != nil || cfg.Workload.Workers != coordinatorWorkerCount ||
		generation == 0 || generation > maxLogicalGeneration {
		return nil, ErrCoordinatorConfig
	}
	assignmentID, err := coordinatorAssignmentID(cfg, generation)
	if err != nil {
		return nil, ErrCoordinatorConfig
	}
	assignments := make([]CoordinatorAssignment, coordinatorWorkerCount)
	for workerID := uint64(0); workerID < coordinatorWorkerCount; workerID++ {
		userCount, err := workerOnlineTarget(cfg.Workload.OnlineUsers, workerID, coordinatorWorkerCount)
		if err != nil {
			return nil, ErrCoordinatorConfig
		}
		assignmentConfig, err := cloneCoordinatorConfig(cfg)
		if err != nil {
			return nil, ErrCoordinatorConfig
		}
		fence := WorkerFence{RunID: cfg.RunID, AssignmentID: assignmentID, Generation: generation}
		assignments[workerID] = CoordinatorAssignment{
			WorkerAssignment: WorkerAssignment{
				WorkerFence: fence, WorkerID: workerID, WorkerCount: coordinatorWorkerCount,
				CoordinatorGrants: true, Config: assignmentConfig,
			},
			Partition: CoordinatorPartition{
				FirstGlobalIndex: workerID, Stride: coordinatorWorkerCount,
				UserCount: uint64(userCount), RateWeight: 1,
			},
		}
	}
	return assignments, nil
}

func coordinatorAssignmentID(cfg Config, generation uint64) (string, error) {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte("wukongim/chat-lifecycle/coordinator-assignment/v1"))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(encoded)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write(encoded)
	binary.BigEndian.PutUint64(length[:], generation)
	_, _ = digest.Write(length[:])
	sum := digest.Sum(nil)
	return "cla-" + hex.EncodeToString(sum[:16]), nil
}

func cloneCoordinatorConfig(cfg Config) (Config, error) {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return Config{}, err
	}
	var clone Config
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return Config{}, err
	}
	return clone, nil
}
