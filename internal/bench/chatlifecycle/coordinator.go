package chatlifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const (
	coordinatorWorkerCount       = 3
	coordinatorDefaultRoundTime  = 5 * time.Second
	coordinatorGrantCadence      = time.Second
	coordinatorGrantEvidencePoll = 10 * time.Millisecond
	// coordinatorGrantTickTolerance admits platform timer timestamp quantization
	// without accepting a delayed, skipped, or catch-up logical grant tick.
	coordinatorGrantTickTolerance = 10 * time.Millisecond
)

var ErrCoordinatorConfig = errors.New("chat lifecycle coordinator: invalid configuration")

var (
	errCoordinatorRoundDeadline = errors.New("chat lifecycle coordinator: control round deadline")
	errCoordinatorStopRequested = errors.New("chat lifecycle coordinator: operator stop requested")
)

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
	CoordinatorCodeCapacity        CoordinatorCode = "capacity"
	CoordinatorCodeFinalize        CoordinatorCode = "finalize"
	CoordinatorCodeGenerationReuse CoordinatorCode = "generation_reuse"
	CoordinatorCodeStopped         CoordinatorCode = "stopped"
)

// CoordinatorGrantFailureCode distinguishes the bounded reason why a grant
// stage failed without exposing worker RPC errors or other unbounded details.
type CoordinatorGrantFailureCode string

const (
	CoordinatorGrantFailurePlan     CoordinatorGrantFailureCode = "plan"
	CoordinatorGrantFailureDelivery CoordinatorGrantFailureCode = "delivery"
	CoordinatorGrantFailureTick     CoordinatorGrantFailureCode = "tick"
	CoordinatorGrantFailureCoverage CoordinatorGrantFailureCode = "coverage"
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

// CoordinatorWorkerFailure retains one deterministic, bounded worker runtime
// classification without exposing raw worker errors.
type CoordinatorWorkerFailure struct {
	WorkerID    uint64
	RuntimeCode RuntimeFailureCode
}

// CoordinatorResult contains only bounded orchestration state.
type CoordinatorResult struct {
	Outcome CoordinatorOutcome
	Code    CoordinatorCode
	// GrantFailure retains the bounded grant-stage reason when a grant round fails.
	GrantFailure CoordinatorGrantFailureCode
	// ObserverCode retains the bounded terminal observer reason when Code is observer.
	ObserverCode ObserverCode
	// Preflight retains the bounded admission reason without raw errors or credentials.
	Preflight PreflightResult
	Fence     WorkerFence
	Grant     CoordinatorGrant
	Snapshot  CoordinatorSnapshot
	Capacity  CapacitySnapshot
	// WorkerFailure retains the lowest-ID classified worker failure observed by
	// a failed grant round. An empty RuntimeCode means no classification was available.
	WorkerFailure CoordinatorWorkerFailure
	// Continuation is an in-memory, process-local handoff available only after
	// a passing formal Soak boundary. It is never report or restart state.
	Continuation *CoordinatorContinuation
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

// CoordinatorRateWorker is the optional live capacity-control surface. It is
// deliberately narrower than CoordinatorWorker so startup fakes and adapters
// do not acquire a capacity-only method.
type CoordinatorRateWorker interface {
	UpdateRate(context.Context, WorkerRateRequest) (WorkerStatus, error)
}

// CoordinatorObserver is the existing continuous service observer boundary.
type CoordinatorObserver interface {
	Run(context.Context, Config) ObserverResult
}

// CapacityEvidenceRequest identifies one exact measured or recovery window.
type CapacityEvidenceRequest struct {
	Phase         CapacityPhase
	RatePerSecond uint64
	Start         time.Time
	End           time.Time
}

// CoordinatorCapacityEvidence returns one bounded aggregate at a completed
// capacity window. Implementations must honor cancellation and retain no raw identities.
type CoordinatorCapacityEvidence interface {
	ObserveCapacity(context.Context, CapacityEvidenceRequest) (CapacityObservation, error)
}

// CoordinatorCapacityEvidenceBeginner optionally captures the exact start of
// each measured or recovery window. Coordinator runs it asynchronously while
// the one-second grant loop continues.
type CoordinatorCapacityEvidenceBeginner interface {
	BeginCapacity(context.Context, CapacityEvidenceRequest) error
}

// CoordinatorCapacityDatasetProbe reads the current dataset identity directly
// from every declared service node. It is called synchronously during Run after
// preflight and before setup or worker mutation.
type CoordinatorCapacityDatasetProbe interface {
	ProbeCapacityDataset(context.Context, Config) (CapacityLiveDatasetEvidence, error)
}

// CoordinatorCutKind distinguishes bounded periodic evidence, the continuous
// aged qualification checkpoint, and the terminal pre-stop evidence cut.
type CoordinatorCutKind string

const (
	CoordinatorCutPeriodic      CoordinatorCutKind = "periodic"
	CoordinatorCutQualification CoordinatorCutKind = "qualification"
	CoordinatorCutTerminal      CoordinatorCutKind = "terminal"
)

// CoordinatorRunStart fixes the measured-run clock after the initial global
// grant has crossed every worker. It is the sole checkpoint/verdict origin.
type CoordinatorRunStart struct {
	Config    Config
	Fence     WorkerFence
	StartedAt time.Time
}

// CoordinatorEvidenceCut carries exactly three same-generation raw snapshots.
// Hooks receive no assignment, rate, grant, or stop mutation capability.
type CoordinatorEvidenceCut struct {
	Start     CoordinatorRunStart
	Kind      CoordinatorCutKind
	At        time.Time
	Snapshots []WorkerSnapshot
	Capacity  CapacitySnapshot
	// StopRequested distinguishes an operator request from caller-context cancellation.
	StopRequested bool
}

// CoordinatorFinalCut is emitted after the bounded stop round. Decision is the
// terminal outcome frozen by the preceding terminal evidence cut.
type CoordinatorFinalCut struct {
	Start          CoordinatorRunStart
	At             time.Time
	Decision       CoordinatorOutcome
	Prepare        []WorkerSnapshot
	FinalSnapshots []WorkerSnapshot
	Capacity       CapacitySnapshot
	// Continuous marks the hour-72 formal boundary. Workers remain running on
	// the same fence, and the owning formal-chain process must immediately
	// continue into capacity rather than treating this cut as resumable state.
	Continuous bool
}

// CoordinatorRunHooks owns evidence reduction and atomic report persistence;
// Coordinator retains exclusive control of worker lifecycle and traffic.
type CoordinatorRunHooks interface {
	Begin(context.Context, CoordinatorRunStart) error
	Observe(context.Context, CoordinatorEvidenceCut) (CoordinatorOutcome, error)
	Finalize(context.Context, CoordinatorFinalCut) error
}

// CoordinatorCapacityPeriodicHooks opts a run hook into the same bounded
// periodic evidence cuts while the capacity staircase is active. Generic run
// hooks do not receive these extra cuts unless they implement this interface.
type CoordinatorCapacityPeriodicHooks interface {
	ObserveCapacityPeriodic(context.Context, CoordinatorEvidenceCut) (CoordinatorOutcome, error)
}

// CoordinatorClock owns readiness, grant, status, and final-cutoff time.
type CoordinatorClock interface {
	Now() time.Time
	NewTicker(time.Duration) ObserverTicker
}

// CoordinatorContinuation is the in-memory handoff from a passing formal
// Soak boundary to capacity. It carries the exact live assignment fence and
// grant sequence; it is never serialized and cannot be reconstructed from a
// report after process exit.
type CoordinatorContinuation struct {
	Assignments   []CoordinatorAssignment
	GrantSequence uint64
	owner         *coordinatorObservationOwner
}

type coordinatorObservationOwner struct {
	mu          sync.Mutex
	observation <-chan ObserverResult
	cancel      context.CancelFunc
	claimed     bool
}

func newCoordinatorObservationOwner(
	observation <-chan ObserverResult,
	cancel context.CancelFunc,
) *coordinatorObservationOwner {
	return &coordinatorObservationOwner{observation: observation, cancel: cancel}
}

func (o *coordinatorObservationOwner) claim() (<-chan ObserverResult, context.CancelFunc, bool) {
	if o == nil {
		return nil, nil, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.claimed || o.observation == nil || o.cancel == nil {
		return nil, nil, false
	}
	o.claimed = true
	return o.observation, o.cancel, true
}

func (o *coordinatorObservationOwner) cancelUnclaimedAndJoin() {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.claimed || o.observation == nil || o.cancel == nil {
		o.mu.Unlock()
		return
	}
	o.claimed = true
	observation, cancel := o.observation, o.cancel
	o.mu.Unlock()
	cancel()
	<-observation
}

// CancelObservation releases and joins a formal-chain observation session
// only when no capacity coordinator has already claimed its single ownership.
func (c *CoordinatorContinuation) CancelObservation() {
	if c != nil {
		c.owner.cancelUnclaimedAndJoin()
	}
}

// CoordinatorOptions fixes the only generation this coordinator may run.
type CoordinatorOptions struct {
	Generation        uint64
	Preflight         CoordinatorPreflight
	Setup             CoordinatorGroupSetup
	Workers           []CoordinatorWorker
	Observer          CoordinatorObserver
	Clock             CoordinatorClock
	CleanupTimeout    time.Duration
	RoundTimeout      time.Duration
	CapacityAdmission *CapacityAdmission
	CapacityEvidence  CoordinatorCapacityEvidence
	CapacityDataset   CoordinatorCapacityDatasetProbe
	Hooks             CoordinatorRunHooks
	// KeepWorkersRunningOnSuccess is valid only for a passing formal Soak owned
	// by the in-process formal chain. Every failure and operator stop still
	// performs the normal bounded worker stop round.
	KeepWorkersRunningOnSuccess bool
	// Continuation adopts the still-running formal workers for capacity without
	// setup, assignment, start, generation change, or grant-sequence reset.
	Continuation *CoordinatorContinuation
	// StopRequests is closed for one operator stop. It cancels context-aware
	// startup work and, after the evidence barrier, owns one terminal cut and drain.
	StopRequests <-chan struct{}
}

// Coordinator owns one non-resumable assignment generation.
type Coordinator struct {
	generation                  uint64
	preflight                   CoordinatorPreflight
	setup                       CoordinatorGroupSetup
	workers                     []CoordinatorWorker
	observer                    CoordinatorObserver
	clock                       CoordinatorClock
	cleanupTimeout              time.Duration
	roundTimeout                time.Duration
	capacityAdmission           *CapacityAdmission
	capacityEvidence            CoordinatorCapacityEvidence
	capacityDataset             CoordinatorCapacityDatasetProbe
	hooks                       CoordinatorRunHooks
	keepWorkersRunningOnSuccess bool
	continuation                *CoordinatorContinuation
	stopRequests                <-chan struct{}

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
	var capacityAdmission *CapacityAdmission
	if options.CapacityAdmission != nil {
		cloned := *options.CapacityAdmission
		checkpointBody, err := json.Marshal(options.CapacityAdmission.Checkpoint)
		if err != nil || json.Unmarshal(checkpointBody, &cloned.Checkpoint) != nil {
			return nil, ErrCoordinatorConfig
		}
		capacityAdmission = &cloned
	}
	var continuation *CoordinatorContinuation
	if options.Continuation != nil {
		cloned := &CoordinatorContinuation{
			GrantSequence: options.Continuation.GrantSequence,
			owner:         options.Continuation.owner,
		}
		cloned.Assignments = append([]CoordinatorAssignment(nil), options.Continuation.Assignments...)
		continuation = cloned
	}
	return &Coordinator{
		generation: options.Generation, preflight: options.Preflight, setup: options.Setup,
		workers: workers, observer: options.Observer, clock: options.Clock,
		cleanupTimeout: options.CleanupTimeout, roundTimeout: options.RoundTimeout,
		capacityAdmission: capacityAdmission, capacityEvidence: options.CapacityEvidence,
		capacityDataset: options.CapacityDataset, hooks: options.Hooks,
		keepWorkersRunningOnSuccess: options.KeepWorkersRunningOnSuccess,
		continuation:                continuation, stopRequests: options.StopRequests,
	}, nil
}

// Run enforces preflight -> setup -> assign -> start -> observed readiness ->
// grants -> final cutoff -> checkpoint/finalize.
func (c *Coordinator) Run(ctx context.Context, cfg Config) (result CoordinatorResult) {
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
	var observationContext context.Context
	var cancelObservation context.CancelFunc
	var observationChannel <-chan ObserverResult
	observationJoined := false
	observationTransferred := false
	var observation ObserverResult
	defer func() {
		if observationJoined && result.Code == CoordinatorCodeObserver {
			result.ObserverCode = observation.Code
		}
	}()
	if c.continuation != nil {
		var claimed bool
		observationChannel, cancelObservation, claimed = c.continuation.owner.claim()
		if !claimed {
			return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeGenerationReuse}
		}
		observationContext = ctx
	}
	defer func() {
		if observationChannel == nil || observationTransferred {
			return
		}
		cancelObservation()
		if !observationJoined {
			<-observationChannel
		}
	}()
	if c.keepWorkersRunningOnSuccess &&
		(cfg.Profile != ProfileFormal || cfg.Mode != ModeSoak || cfg.Stage != StageFormal) {
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeGenerationReuse}
	}
	if c.continuation != nil && (cfg.Profile != ProfileFormal || cfg.Mode != ModeCapacity || cfg.Stage != StageFormal) {
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeGenerationReuse}
	}
	if cfg.Mode == ModeCapacity && (c.capacityAdmission == nil || c.capacityEvidence == nil || c.capacityDataset == nil) {
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeCapacity}
	}
	if cfg.Mode == ModeCapacity {
		if err := validateCapacityCheckpoint(cfg, *c.capacityAdmission); err != nil {
			return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeCapacity}
		}
	}

	startupContext, cancelStartup := context.WithCancelCause(ctx)
	var startupStopRequested atomic.Bool
	startupWatchDone := make(chan struct{})
	startupWatchExited := make(chan struct{})
	go func() {
		defer close(startupWatchExited)
		select {
		case <-c.stopRequests:
			startupStopRequested.Store(true)
			cancelStartup(errCoordinatorStopRequested)
		case <-startupWatchDone:
		case <-ctx.Done():
		}
	}()
	var stopStartupWatchOnce sync.Once
	stopStartupWatch := func() {
		stopStartupWatchOnce.Do(func() {
			close(startupWatchDone)
			<-startupWatchExited
		})
	}
	defer func() {
		stopStartupWatch()
		cancelStartup(nil)
	}()

	preflight := c.preflight.Check(startupContext, cfg)
	if startupStopRequested.Load() {
		return CoordinatorResult{Outcome: CoordinatorStopped, Code: CoordinatorCodeStopped, Preflight: preflight}
	}
	if !preflight.TrafficAllowed() {
		outcome := CoordinatorHarnessInvalid
		if preflight.Outcome == PreflightInfrastructureFailure {
			outcome = CoordinatorInfrastructureFailure
		}
		return CoordinatorResult{Outcome: outcome, Code: CoordinatorCodePreflight, Preflight: preflight}
	}
	var capacityAdmission capacityAdmissionToken
	if cfg.Mode == ModeCapacity {
		probeStartedAt := c.clock.Now()
		probeContext, cancelProbe := context.WithTimeoutCause(startupContext, c.roundTimeout, errCoordinatorRoundDeadline)
		liveDataset, probeErr := c.capacityDataset.ProbeCapacityDataset(probeContext, cfg)
		probeCause := context.Cause(probeContext)
		probeCompletedAt := c.clock.Now()
		cancelProbe()
		if probeErr != nil {
			if startupStopRequested.Load() {
				return CoordinatorResult{Outcome: CoordinatorStopped, Code: CoordinatorCodeStopped}
			}
			if ctx.Err() != nil && errors.Is(probeErr, ctx.Err()) && errors.Is(probeCause, ctx.Err()) {
				return CoordinatorResult{Outcome: CoordinatorStopped, Code: CoordinatorCodeStopped}
			}
			return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeCapacity}
		}
		var admissionErr error
		capacityAdmission, admissionErr = validateCapacityLiveDataset(
			cfg, *c.capacityAdmission, liveDataset, probeStartedAt, probeCompletedAt,
		)
		if admissionErr != nil {
			return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeCapacity}
		}
		if startupStopRequested.Load() || ctx.Err() != nil {
			return CoordinatorResult{Outcome: CoordinatorStopped, Code: CoordinatorCodeStopped}
		}
	}
	var assignments []CoordinatorAssignment
	var err error
	if c.continuation == nil {
		if err := c.setup.Run(startupContext, cfg); err != nil {
			if startupStopRequested.Load() {
				return CoordinatorResult{Outcome: CoordinatorStopped, Code: CoordinatorCodeStopped}
			}
			return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeSetup}
		}
		if startupStopRequested.Load() {
			return CoordinatorResult{Outcome: CoordinatorStopped, Code: CoordinatorCodeStopped}
		}
		assignments, err = BuildCoordinatorAssignments(cfg, c.generation)
	} else {
		assignments, err = validateCoordinatorContinuation(cfg, c.generation, *c.capacityAdmission, *c.continuation)
	}
	if err != nil {
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeAssignment}
	}
	grantPlan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		return CoordinatorResult{Outcome: CoordinatorHarnessInvalid, Code: CoordinatorCodeAssignment}
	}
	if c.continuation != nil {
		grantPlan.sequence = c.continuation.GrantSequence
	}
	fence := assignments[0].WorkerFence
	result = CoordinatorResult{Fence: fence}
	attempted := [coordinatorWorkerCount]bool{true, true, true}
	assigned := [coordinatorWorkerCount]bool{}
	var startStatuses [coordinatorWorkerCount]WorkerStatus
	var disposition coordinatorRoundDisposition
	if c.continuation == nil {
		if _, disposition = c.assignRound(startupContext, assignments); disposition != coordinatorRoundSucceeded {
			c.stopAfterFailure(fence, attempted)
			if startupStopRequested.Load() {
				result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
				return result
			}
			if disposition == coordinatorRoundParentCanceled {
				result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
				return result
			}
			result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeAssignment
			return result
		}
		if startupStopRequested.Load() || ctx.Err() != nil {
			c.stopAfterFailure(fence, attempted)
			result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
			return result
		}
		assigned = [coordinatorWorkerCount]bool{true, true, true}
		startStatuses, disposition = c.startRound(startupContext, assignments, fence)
	} else {
		assigned = [coordinatorWorkerCount]bool{true, true, true}
		startStatuses, disposition = c.statusRound(startupContext, assignments)
	}
	if disposition != coordinatorRoundSucceeded ||
		(c.continuation != nil && !allCoordinatorTrafficReady(startStatuses)) {
		c.stopAfterFailure(fence, attempted)
		if startupStopRequested.Load() {
			result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
			return result
		}
		if disposition == coordinatorRoundParentCanceled {
			result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
			return result
		}
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeStart
		return result
	}
	if startupStopRequested.Load() || ctx.Err() != nil {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
		return result
	}

	if c.continuation == nil {
		observationContext, cancelObservation = context.WithCancel(ctx)
		started := make(chan ObserverResult, 1)
		observationChannel = started
		go func() {
			started <- c.observer.Run(observationContext, cfg)
			cancelObservation()
		}()
	}
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
	preserveObservation := false
	finishObservationForCutoff := func() {
		if c.keepWorkersRunningOnSuccess {
			preserveObservation = true
			return
		}
		observation = joinObservation()
	}
	completeParentCancellation := func() CoordinatorResult {
		reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeStopped, coordinatorRoundParentCanceled)
		observation = joinObservation()
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	}
	completeStartupStop := func() CoordinatorResult {
		observation = joinObservation()
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
		return result
	}

	ready, earlyObservation, observerDone, readyDisposition := c.waitForTrafficReady(
		startupContext, observationChannel, assignments, startStatuses, cfg.Thresholds.Timeline.Warmup,
	)
	if !ready {
		if startupStopRequested.Load() {
			return completeStartupStop()
		}
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
	stopStartupWatch()
	select {
	case <-c.stopRequests:
		startupStopRequested.Store(true)
	default:
	}
	if startupStopRequested.Load() {
		return completeStartupStop()
	}
	grant, err := grantPlan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		result.GrantFailure = CoordinatorGrantFailurePlan
		reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeGrant, coordinatorRoundStageFailed)
		joinFailureObservation()
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	}
	result.Grant = grant
	if grantDisposition, workerFailure := c.deliverGrant(
		observationContext, assignments, grantPlan.request(fence, grant), c.roundTimeout,
	); grantDisposition != coordinatorRoundSucceeded {
		result.GrantFailure = CoordinatorGrantFailureDelivery
		result.WorkerFailure = workerFailure
		reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeGrant, grantDisposition)
		joinFailureObservation()
		result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
		c.stopAfterFailure(fence, attempted)
		return result
	}

	grantBarrierAt := c.clock.Now()
	runStart := CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: grantBarrierAt}
	if c.hooks != nil {
		hookContext, cancelHook := context.WithTimeoutCause(ctx, c.roundTimeout, errCoordinatorRoundDeadline)
		hookErr := c.hooks.Begin(hookContext, runStart)
		cancelHook()
		if hookErr != nil {
			reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeCheckpoint, coordinatorRoundStageFailed)
			joinFailureObservation()
			result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
			c.stopAfterFailure(fence, attempted)
			return result
		}
	}
	var capacityStaircase *CapacityStaircase
	if cfg.Mode == ModeCapacity {
		capacityStaircase, err = newCapacityStaircase(cfg, capacityAdmission, grantBarrierAt)
		if err != nil {
			reason := lockCoordinatorTerminationReason(ctx, CoordinatorCodeCapacity, coordinatorRoundStageFailed)
			joinFailureObservation()
			result.Outcome, result.Code = coordinatorConcurrentFailure(observation, reason)
			c.stopAfterFailure(fence, attempted)
			return result
		}
		result.Capacity = capacityStaircase.Snapshot()
	}
	observationDeadline := grantBarrierAt.Add(cfg.measuredDuration())
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
	var capacityRateReady uint64
	var capacityRateReadyAt time.Time
	var beginCapacityWindow func(CapacitySnapshot) bool
	deliverScheduledGrant := func(tickAt time.Time) coordinatorRoundDisposition {
		now := c.clock.Now()
		if !validCoordinatorGrantTick(now, tickAt, lastGrantTickAt, haveGrantTick) {
			result.GrantFailure = CoordinatorGrantFailureTick
			return coordinatorRoundStageFailed
		}
		if capacityRateReady > 0 && tickAt.After(capacityRateReadyAt) {
			if err := grantPlan.ScheduleRate(capacityRateReady); err != nil {
				result.GrantFailure = CoordinatorGrantFailurePlan
				_, _ = capacityStaircase.FailRateChange(tickAt)
				result.Capacity = capacityStaircase.Snapshot()
				return coordinatorRoundStageFailed
			}
			capacityRateReady, capacityRateReadyAt = 0, time.Time{}
		}
		grant, grantErr := grantPlan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
		if grantErr != nil {
			result.GrantFailure = CoordinatorGrantFailurePlan
			return coordinatorRoundStageFailed
		}
		if disposition, workerFailure := c.deliverGrant(
			observationContext, assignments, grantPlan.request(fence, grant), coordinatorGrantCadence,
		); disposition != coordinatorRoundSucceeded {
			result.GrantFailure = CoordinatorGrantFailureDelivery
			if workerFailure.RuntimeCode != "" {
				result.WorkerFailure = workerFailure
			}
			if capacityStaircase != nil && grant.RateChanged {
				_, _ = capacityStaircase.FailRateChange(tickAt)
				result.Capacity = capacityStaircase.Snapshot()
			}
			return disposition
		}
		if capacityStaircase != nil && grant.RateChanged {
			if _, commitErr := capacityStaircase.CommitRate(tickAt, grant.RatePerSecond); commitErr != nil {
				result.GrantFailure = CoordinatorGrantFailurePlan
				_, _ = capacityStaircase.FailRateChange(tickAt)
				result.Capacity = capacityStaircase.Snapshot()
				return coordinatorRoundStageFailed
			}
			result.Capacity = capacityStaircase.Snapshot()
			if beginCapacityWindow != nil && !beginCapacityWindow(result.Capacity) {
				result.GrantFailure = CoordinatorGrantFailurePlan
				return coordinatorRoundStageFailed
			}
		}
		lastGrantTickAt, haveGrantTick = tickAt, true
		result.Grant = grant
		return coordinatorRoundSucceeded
	}
	grantCoverageMissing := func(at time.Time) bool {
		return coordinatorGrantCoverageMissing(at, lastGrantTickAt)
	}
	type pendingStatusRoundResult struct {
		statuses    [coordinatorWorkerCount]WorkerStatus
		disposition coordinatorRoundDisposition
	}
	waitForStatusRound := func() (
		[coordinatorWorkerCount]WorkerStatus,
		coordinatorRoundDisposition,
		coordinatorRoundDisposition,
	) {
		statusContext, cancelStatus := context.WithCancel(observationContext)
		defer cancelStatus()
		results := make(chan pendingStatusRoundResult, 1)
		go func() {
			statuses, disposition := c.statusRound(statusContext, assignments)
			results <- pendingStatusRoundResult{statuses: statuses, disposition: disposition}
		}()
		joinAfterGrantFailure := func(disposition coordinatorRoundDisposition) (
			[coordinatorWorkerCount]WorkerStatus,
			coordinatorRoundDisposition,
			coordinatorRoundDisposition,
		) {
			cancelStatus()
			<-results
			return [coordinatorWorkerCount]WorkerStatus{}, coordinatorRoundStageFailed, disposition
		}
		for {
			select {
			case completed := <-results:
				return completed.statuses, completed.disposition, coordinatorRoundSucceeded
			case tickAt := <-grantTicker.C():
				now := c.clock.Now()
				if tickAt.IsZero() || tickAt.After(now) {
					result.GrantFailure = CoordinatorGrantFailureTick
					return joinAfterGrantFailure(coordinatorRoundStageFailed)
				}
				if !tickAt.Before(observationDeadline) {
					continue
				}
				if disposition := deliverScheduledGrant(tickAt); disposition != coordinatorRoundSucceeded {
					return joinAfterGrantFailure(disposition)
				}
			case <-ctx.Done():
				cancelStatus()
				completed := <-results
				return completed.statuses, completed.disposition, coordinatorRoundParentCanceled
			}
		}
	}
	cutoffOwned := false
	stopRequested := false
	failureCode := CoordinatorCode("")
	failureDisposition := coordinatorRoundStageFailed
	var failureReason coordinatorTerminationReason
	var joinCapacityAsync func()
	qualificationCaptured := false
	type coordinatorHookRoundResult struct {
		kind        CoordinatorCutKind
		decision    CoordinatorOutcome
		snapshots   []WorkerSnapshot
		disposition coordinatorRoundDisposition
	}
	var hookResults <-chan coordinatorHookRoundResult
	var hookPrepareSnapshots []WorkerSnapshot
	var hookTerminalDecision CoordinatorOutcome
	observeHookCut := func(
		kind CoordinatorCutKind,
		at time.Time,
		capacity CapacitySnapshot,
		observe func(context.Context, CoordinatorEvidenceCut) (CoordinatorOutcome, error),
	) (CoordinatorOutcome, []WorkerSnapshot, coordinatorRoundDisposition) {
		snapshots, disposition := c.checkpointRound(observationContext, assignments, fence)
		if disposition != coordinatorRoundSucceeded {
			return "", nil, disposition
		}
		hookContext, cancelHook := context.WithTimeoutCause(observationContext, c.roundTimeout, errCoordinatorRoundDeadline)
		decision, hookErr := observe(hookContext, CoordinatorEvidenceCut{
			Start: runStart, Kind: kind, At: at, Snapshots: snapshots, Capacity: capacity,
		})
		cause := context.Cause(hookContext)
		cancelHook()
		if hookErr != nil {
			if ctx.Err() != nil && errors.Is(hookErr, ctx.Err()) && errors.Is(cause, ctx.Err()) {
				return "", nil, coordinatorRoundParentCanceled
			}
			return "", nil, coordinatorRoundStageFailed
		}
		if kind != CoordinatorCutTerminal && decision != "" &&
			decision != CoordinatorProductFailure && decision != CoordinatorHarnessInvalid &&
			decision != CoordinatorInfrastructureFailure && decision != CoordinatorStopped {
			return "", nil, coordinatorRoundStageFailed
		}
		if kind == CoordinatorCutTerminal && decision != CoordinatorCompleted &&
			decision != CoordinatorProductFailure && decision != CoordinatorHarnessInvalid &&
			decision != CoordinatorInfrastructureFailure && decision != CoordinatorStopped {
			return "", nil, coordinatorRoundStageFailed
		}
		return decision, snapshots, coordinatorRoundSucceeded
	}
	startHookCut := func(kind CoordinatorCutKind, at time.Time) bool {
		if c.hooks == nil || hookResults != nil {
			return false
		}
		results := make(chan coordinatorHookRoundResult, 1)
		hookResults = results
		capacity := cloneCapacitySnapshot(result.Capacity)
		go func() {
			decision, snapshots, disposition := observeHookCut(kind, at, capacity, c.hooks.Observe)
			results <- coordinatorHookRoundResult{
				kind: kind, decision: decision, snapshots: snapshots, disposition: disposition,
			}
		}()
		return true
	}
	startCapacityPeriodicHookCut := func(at time.Time) bool {
		hooks, ok := c.hooks.(CoordinatorCapacityPeriodicHooks)
		if !ok || hookResults != nil {
			return false
		}
		results := make(chan coordinatorHookRoundResult, 1)
		hookResults = results
		capacity := cloneCapacitySnapshot(result.Capacity)
		go func() {
			decision, snapshots, disposition := observeHookCut(
				CoordinatorCutPeriodic, at, capacity, hooks.ObserveCapacityPeriodic,
			)
			results <- coordinatorHookRoundResult{
				kind: CoordinatorCutPeriodic, decision: decision, snapshots: snapshots, disposition: disposition,
			}
		}()
		return true
	}
	joinHookAsync := func() coordinatorHookRoundResult {
		if hookResults == nil {
			return coordinatorHookRoundResult{disposition: coordinatorRoundSucceeded}
		}
		observed := <-hookResults
		hookResults = nil
		return observed
	}
	if capacityStaircase != nil {
		type capacityBeginResult struct {
			request     CapacityEvidenceRequest
			disposition coordinatorRoundDisposition
		}
		type capacityEvidenceResult struct {
			request     CapacityEvidenceRequest
			observation CapacityObservation
			disposition coordinatorRoundDisposition
		}
		type capacityRateResult struct {
			rate        uint64
			disposition coordinatorRoundDisposition
		}
		var evidenceResults <-chan capacityEvidenceResult
		var rateResults <-chan capacityRateResult
		var beginResults <-chan capacityBeginResult
		beginCapacityWindow = func(snapshot CapacitySnapshot) bool {
			if snapshot.Phase != CapacityPhaseMeasure && snapshot.Phase != CapacityPhaseRecovery {
				return true
			}
			beginner, ok := c.capacityEvidence.(CoordinatorCapacityEvidenceBeginner)
			if !ok {
				return true
			}
			if beginResults != nil {
				return false
			}
			request := CapacityEvidenceRequest{
				Phase: snapshot.Phase, RatePerSecond: snapshot.CurrentRate,
				Start: snapshot.PhaseStart, End: snapshot.PhaseEnd,
			}
			results := make(chan capacityBeginResult, 1)
			beginResults = results
			go func() {
				err := beginner.BeginCapacity(observationContext, request)
				disposition := coordinatorRoundStageFailed
				if err == nil {
					disposition = coordinatorRoundSucceeded
				} else if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
					disposition = coordinatorRoundParentCanceled
				}
				results <- capacityBeginResult{request: request, disposition: disposition}
			}()
			return true
		}
		joinCapacityAsync = func() {
			if beginResults != nil {
				<-beginResults
				beginResults = nil
			}
			if evidenceResults != nil {
				<-evidenceResults
				evidenceResults = nil
			}
			if rateResults != nil {
				<-rateResults
				rateResults = nil
			}
		}
		completeCapacityFailure := func(snapshot CapacitySnapshot) CoordinatorResult {
			result.Capacity = snapshot
			observation = joinObservation()
			joinCapacityAsync()
			result.Code = CoordinatorCodeCapacity
			switch snapshot.Outcome {
			case CapacityPassed, CapacityPassedWithWarning:
				result.Outcome = CoordinatorCompleted
			case CapacityProductFailure:
				result.Outcome = CoordinatorProductFailure
			case CapacityInfrastructureFailure:
				result.Outcome = CoordinatorInfrastructureFailure
			default:
				result.Outcome = CoordinatorHarnessInvalid
			}
			if observation.Outcome == ObserverProductFailure {
				result.Outcome, result.Code = CoordinatorProductFailure, CoordinatorCodeObserver
			} else if observation.Outcome == ObserverHarnessInvalid && result.Outcome != CoordinatorProductFailure {
				result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeObserver
			}
			c.stopAfterFailure(fence, attempted)
			return result
		}
		startCapacityEvidence := func(snapshot CapacitySnapshot) {
			request := CapacityEvidenceRequest{
				Phase: snapshot.Phase, RatePerSecond: snapshot.CurrentRate,
				Start: snapshot.PhaseStart, End: snapshot.PhaseEnd,
			}
			results := make(chan capacityEvidenceResult, 1)
			evidenceResults = results
			go func() {
				evidenceContext, cancel := context.WithTimeoutCause(
					observationContext, c.roundTimeout, errCoordinatorRoundDeadline,
				)
				defer cancel()
				observed, observeErr := c.capacityEvidence.ObserveCapacity(evidenceContext, request)
				disposition := coordinatorRoundStageFailed
				if observeErr == nil {
					disposition = coordinatorRoundSucceeded
				} else if ctx.Err() != nil && errors.Is(observeErr, ctx.Err()) &&
					errors.Is(context.Cause(evidenceContext), ctx.Err()) {
					disposition = coordinatorRoundParentCanceled
				}
				results <- capacityEvidenceResult{
					request: request, observation: observed, disposition: disposition,
				}
			}()
		}
		startCapacityRateRound := func(rate uint64) {
			results := make(chan capacityRateResult, 1)
			rateResults = results
			go func() {
				results <- capacityRateResult{
					rate: rate, disposition: c.updateCapacityWorkers(observationContext, fence, rate),
				}
			}()
		}

		for {
			now := c.clock.Now()
			snapshot := capacityStaircase.Snapshot()
			result.Capacity = snapshot
			if evidenceResults == nil && beginResults == nil && snapshot.Phase != CapacityPhaseRatePending &&
				!snapshot.Terminal && !now.Before(snapshot.PhaseEnd) {
				select {
				case tickAt := <-grantTicker.C():
					now = c.clock.Now()
					if tickAt.IsZero() || tickAt.After(now) || tickAt.After(snapshot.PhaseEnd) {
						result.GrantFailure = CoordinatorGrantFailureTick
						failureCode = CoordinatorCodeGrant
						goto observationFailure
					}
					if disposition := deliverScheduledGrant(tickAt); disposition != coordinatorRoundSucceeded {
						failureCode, failureDisposition = CoordinatorCodeGrant, disposition
						goto observationFailure
					}
				default:
				}
				if grantCoverageMissing(snapshot.PhaseEnd) {
					result.GrantFailure = CoordinatorGrantFailureCoverage
					failureCode = CoordinatorCodeGrant
					goto observationFailure
				}
				if snapshot.Phase == CapacityPhaseStabilize {
					if _, advanceErr := capacityStaircase.Advance(snapshot.PhaseEnd, CapacityObservation{}); advanceErr != nil {
						result.Capacity = capacityStaircase.Snapshot()
						if c.hooks != nil {
							observation = joinObservation()
							cutoffOwned = true
							goto observationComplete
						}
						return completeCapacityFailure(result.Capacity)
					}
					result.Capacity = capacityStaircase.Snapshot()
					if !beginCapacityWindow(result.Capacity) {
						failureCode = CoordinatorCodeCapacity
						goto observationFailure
					}
					continue
				}
				startCapacityEvidence(snapshot)
				continue
			}
			if grantCoverageMissing(now) {
				result.GrantFailure = CoordinatorGrantFailureCoverage
				failureCode = CoordinatorCodeGrant
				goto observationFailure
			}

			select {
			case observation = <-observationChannel:
				observationJoined = true
				cancelObservation()
				joinCapacityAsync()
				goto observationComplete
			case hook := <-hookResults:
				hookResults = nil
				if hook.disposition != coordinatorRoundSucceeded {
					failureCode, failureDisposition = CoordinatorCodeCheckpoint, hook.disposition
					goto observationFailure
				}
				if hook.decision != "" {
					hookPrepareSnapshots, hookTerminalDecision = hook.snapshots, hook.decision
					observation = joinObservation()
					cutoffOwned = true
					joinCapacityAsync()
					goto observationComplete
				}
			case evidence := <-evidenceResults:
				evidenceResults = nil
				if evidence.disposition == coordinatorRoundParentCanceled {
					result.Capacity = capacityStaircase.Snapshot()
					cancelObservation()
					joinCapacityAsync()
					return completeParentCancellation()
				}
				if evidence.disposition != coordinatorRoundSucceeded {
					evidence.observation.HarnessInvalid = true
				}
				transition, advanceErr := capacityStaircase.Advance(evidence.request.End, evidence.observation)
				result.Capacity = capacityStaircase.Snapshot()
				if advanceErr != nil || result.Capacity.Terminal {
					if result.Capacity.Outcome == CapacityPassed || result.Capacity.Outcome == CapacityPassedWithWarning {
						observation = joinObservation()
						cutoffOwned = true
						goto observationComplete
					}
					if c.hooks != nil {
						observation = joinObservation()
						cutoffOwned = true
						goto observationComplete
					}
					return completeCapacityFailure(result.Capacity)
				}
				if transition.ScheduleRate {
					startCapacityRateRound(transition.RatePerSecond)
				}
			case begin := <-beginResults:
				beginResults = nil
				if begin.disposition == coordinatorRoundParentCanceled {
					result.Capacity = capacityStaircase.Snapshot()
					cancelObservation()
					joinCapacityAsync()
					return completeParentCancellation()
				}
				if begin.disposition != coordinatorRoundSucceeded {
					_, _ = capacityStaircase.Advance(c.clock.Now(), CapacityObservation{HarnessInvalid: true})
					result.Capacity = capacityStaircase.Snapshot()
					observation = joinObservation()
					cutoffOwned = true
					goto observationComplete
				}
			case rateResult := <-rateResults:
				rateResults = nil
				if rateResult.disposition == coordinatorRoundParentCanceled {
					result.Capacity = capacityStaircase.Snapshot()
					cancelObservation()
					joinCapacityAsync()
					return completeParentCancellation()
				}
				if rateResult.disposition != coordinatorRoundSucceeded {
					_, _ = capacityStaircase.FailRateChange(c.clock.Now())
					result.Capacity = capacityStaircase.Snapshot()
					if c.hooks != nil {
						observation = joinObservation()
						cutoffOwned = true
						goto observationComplete
					}
					return completeCapacityFailure(result.Capacity)
				}
				capacityRateReady, capacityRateReadyAt = rateResult.rate, c.clock.Now()
			case <-statusTicker.C():
				now = c.clock.Now()
				select {
				case tickAt := <-grantTicker.C():
					now = c.clock.Now()
					if tickAt.IsZero() || tickAt.After(now) {
						result.GrantFailure = CoordinatorGrantFailureTick
						failureCode = CoordinatorCodeGrant
						goto observationFailure
					}
					if disposition := deliverScheduledGrant(tickAt); disposition != coordinatorRoundSucceeded {
						failureCode, failureDisposition = CoordinatorCodeGrant, disposition
						goto observationFailure
					}
				default:
				}
				if ctx.Err() != nil {
					result.Capacity = capacityStaircase.Snapshot()
					cancelObservation()
					joinCapacityAsync()
					return completeParentCancellation()
				}
				if grantCoverageMissing(now) {
					result.GrantFailure = CoordinatorGrantFailureCoverage
					failureCode = CoordinatorCodeGrant
					goto observationFailure
				}
				statuses, disposition := c.statusRound(observationContext, assignments)
				if disposition != coordinatorRoundSucceeded || !allCoordinatorTrafficReady(statuses) {
					failureCode, failureDisposition = CoordinatorCodeRuntime, disposition
					if disposition == coordinatorRoundSucceeded {
						failureDisposition = coordinatorRoundStageFailed
					}
					goto observationFailure
				}
				if _, ok := c.hooks.(CoordinatorCapacityPeriodicHooks); ok {
					if hookResults == nil && !startCapacityPeriodicHookCut(now) {
						failureCode, failureDisposition = CoordinatorCodeCheckpoint, coordinatorRoundStageFailed
						goto observationFailure
					}
				}
			case tickAt := <-grantTicker.C():
				if ctx.Err() != nil {
					result.Capacity = capacityStaircase.Snapshot()
					cancelObservation()
					joinCapacityAsync()
					return completeParentCancellation()
				}
				if disposition := deliverScheduledGrant(tickAt); disposition != coordinatorRoundSucceeded {
					if capacityStaircase.Snapshot().Phase == CapacityPhaseRatePending {
						_, _ = capacityStaircase.FailRateChange(tickAt)
						result.Capacity = capacityStaircase.Snapshot()
					}
					failureCode, failureDisposition = CoordinatorCodeGrant, disposition
					goto observationFailure
				}
			case <-ctx.Done():
				result.Capacity = capacityStaircase.Snapshot()
				cancelObservation()
				joinCapacityAsync()
				return completeParentCancellation()
			case <-c.stopRequests:
				stopRequested = true
				result.Capacity = capacityStaircase.Snapshot()
				observation = joinObservation()
				cutoffOwned = true
				joinCapacityAsync()
				goto observationComplete
			}
		}
	}
	for {
		now := c.clock.Now()
		if !now.Before(observationDeadline) {
			if hookResults != nil {
				hook := joinHookAsync()
				if hook.disposition != coordinatorRoundSucceeded {
					failureCode, failureDisposition = CoordinatorCodeCheckpoint, hook.disposition
					goto observationFailure
				}
				if hook.kind == CoordinatorCutQualification {
					qualificationCaptured = true
				}
				if hook.decision != "" {
					hookPrepareSnapshots, hookTerminalDecision = hook.snapshots, hook.decision
					finishObservationForCutoff()
					cutoffOwned = true
					goto observationComplete
				}
			}
			select {
			case tickAt := <-grantTicker.C():
				now = c.clock.Now()
				if tickAt.IsZero() || tickAt.After(now) {
					result.GrantFailure = CoordinatorGrantFailureTick
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
				result.GrantFailure = CoordinatorGrantFailureCoverage
				failureCode = CoordinatorCodeGrant
				goto observationFailure
			}
			finishObservationForCutoff()
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
			result.GrantFailure = CoordinatorGrantFailureCoverage
			failureCode = CoordinatorCodeGrant
			goto observationFailure
		}

		select {
		case observation = <-observationChannel:
			observationJoined = true
			goto observationComplete
		case hook := <-hookResults:
			hookResults = nil
			if hook.disposition != coordinatorRoundSucceeded {
				failureCode, failureDisposition = CoordinatorCodeCheckpoint, hook.disposition
				goto observationFailure
			}
			if hook.kind == CoordinatorCutQualification {
				qualificationCaptured = true
			}
			if hook.decision != "" {
				hookPrepareSnapshots, hookTerminalDecision = hook.snapshots, hook.decision
				finishObservationForCutoff()
				cutoffOwned = true
				goto observationComplete
			}
		case statusTickAt := <-statusTicker.C():
			now = c.clock.Now()
			if statusTickAt.IsZero() {
				failureCode, failureDisposition = CoordinatorCodeRuntime, coordinatorRoundStageFailed
				goto observationFailure
			}
			select {
			case tickAt := <-grantTicker.C():
				now = c.clock.Now()
				if tickAt.IsZero() || tickAt.After(now) {
					result.GrantFailure = CoordinatorGrantFailureTick
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
					result.GrantFailure = CoordinatorGrantFailureCoverage
					failureCode = CoordinatorCodeGrant
					goto observationFailure
				}
				finishObservationForCutoff()
				cutoffOwned = true
				goto observationComplete
			}
			if grantCoverageMissing(now) {
				result.GrantFailure = CoordinatorGrantFailureCoverage
				failureCode = CoordinatorCodeGrant
				goto observationFailure
			}
			statuses, disposition, grantDisposition := waitForStatusRound()
			if grantDisposition != coordinatorRoundSucceeded {
				failureCode = CoordinatorCodeGrant
				failureDisposition = grantDisposition
				goto observationFailure
			}
			if disposition != coordinatorRoundSucceeded || !allCoordinatorTrafficReady(statuses) {
				failureCode = CoordinatorCodeRuntime
				failureDisposition = disposition
				if disposition == coordinatorRoundSucceeded {
					failureDisposition = coordinatorRoundStageFailed
				}
				goto observationFailure
			}
			if c.hooks != nil {
				if hookResults != nil {
					failureCode, failureDisposition = CoordinatorCodeCheckpoint, coordinatorRoundStageFailed
					goto observationFailure
				}
				kind := CoordinatorCutPeriodic
				if !qualificationCaptured && !statusTickAt.Before(grantBarrierAt.Add(cfg.Thresholds.Timeline.Checkpoint)) {
					kind = CoordinatorCutQualification
				}
				if !startHookCut(kind, statusTickAt) {
					failureCode, failureDisposition = CoordinatorCodeCheckpoint, coordinatorRoundStageFailed
					goto observationFailure
				}
			}
		case tickAt := <-grantTicker.C():
			if ctx.Err() != nil {
				return completeParentCancellation()
			}
			now = c.clock.Now()
			if !now.Before(observationDeadline) && !tickAt.Before(observationDeadline) {
				if tickAt.IsZero() || tickAt.After(now) {
					result.GrantFailure = CoordinatorGrantFailureTick
					failureCode = CoordinatorCodeGrant
					goto observationFailure
				}
				if grantCoverageMissing(observationDeadline) {
					result.GrantFailure = CoordinatorGrantFailureCoverage
					failureCode = CoordinatorCodeGrant
					goto observationFailure
				}
				finishObservationForCutoff()
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
		case <-c.stopRequests:
			stopRequested = true
			observation = joinObservation()
			cutoffOwned = true
			goto observationComplete
		}
	}

observationFailure:
	failureReason = lockCoordinatorTerminationReason(ctx, failureCode, failureDisposition)
	joinFailureObservation()
	_ = joinHookAsync()
	if joinCapacityAsync != nil {
		joinCapacityAsync()
	}
	result.Outcome, result.Code = coordinatorConcurrentFailure(observation, failureReason)
	c.stopAfterFailure(fence, attempted)
	return result

observationComplete:
	if hookResults != nil {
		hook := joinHookAsync()
		if hook.disposition != coordinatorRoundSucceeded {
			c.stopAfterFailure(fence, attempted)
			result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
			return result
		}
		if hook.kind == CoordinatorCutQualification {
			qualificationCaptured = true
		}
		if hook.decision != "" {
			hookPrepareSnapshots, hookTerminalDecision = hook.snapshots, hook.decision
		}
	}
	if joinCapacityAsync != nil {
		cancelObservation()
		joinCapacityAsync()
	}
	preservedObservationEnded := false
	if preserveObservation {
		select {
		case observation = <-observationChannel:
			observationJoined = true
			preserveObservation = false
			preservedObservationEnded = true
		default:
		}
	}
	if preservedObservationEnded || !preserveObservation && observation.Outcome != ObserverStopped {
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
	checkpoint := hookPrepareSnapshots
	terminalDecision := hookTerminalDecision
	if len(checkpoint) == 0 {
		var disposition coordinatorRoundDisposition
		checkpoint, disposition = c.checkpointRound(ctx, assignments, fence)
		if disposition != coordinatorRoundSucceeded {
			c.stopAfterFailure(fence, attempted)
			if disposition == coordinatorRoundParentCanceled {
				result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
				return result
			}
			result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
			return result
		}
		if terminalSnapshotsNeedTrafficRecovery(checkpoint) {
			statuses, statusDisposition := c.statusRound(ctx, assignments)
			if statusDisposition != coordinatorRoundSucceeded {
				c.stopAfterFailure(fence, attempted)
				result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
				return result
			}
			if !allCoordinatorTrafficReady(statuses) {
				ready, _, _, readyDisposition := c.waitForTrafficReady(
					ctx, nil, assignments, statuses, c.roundTimeout,
				)
				if !ready || readyDisposition != coordinatorRoundSucceeded {
					c.stopAfterFailure(fence, attempted)
					result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
					return result
				}
			}
			checkpoint, disposition = c.checkpointRound(ctx, assignments, fence)
			if disposition != coordinatorRoundSucceeded || terminalSnapshotsNeedTrafficRecovery(checkpoint) {
				c.stopAfterFailure(fence, attempted)
				result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
				return result
			}
		}
		if ctx.Err() != nil {
			c.stopAfterFailure(fence, attempted)
			result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
			return result
		}
		terminalDecision = CoordinatorCompleted
		if stopRequested {
			terminalDecision = CoordinatorStopped
		}
		if c.hooks != nil {
			hookContext, cancelHook := context.WithTimeoutCause(ctx, c.roundTimeout, errCoordinatorRoundDeadline)
			decision, hookErr := c.hooks.Observe(hookContext, CoordinatorEvidenceCut{
				Start: runStart, Kind: CoordinatorCutTerminal, At: c.clock.Now(), Snapshots: checkpoint,
				Capacity: result.Capacity, StopRequested: stopRequested,
			})
			cancelHook()
			if hookErr != nil || (decision != CoordinatorCompleted && decision != CoordinatorProductFailure &&
				decision != CoordinatorHarnessInvalid && decision != CoordinatorInfrastructureFailure && decision != CoordinatorStopped) {
				c.stopAfterFailure(fence, attempted)
				result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
				return result
			}
			if stopRequested && decision != CoordinatorStopped {
				c.stopAfterFailure(fence, attempted)
				result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
				return result
			}
			terminalDecision = decision
		}
	}
	prepareSnapshots := checkpoint
	preparedAggregate, err := aggregator.Aggregate(checkpoint)
	if err != nil {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
		return result
	}
	if c.keepWorkersRunningOnSuccess && terminalDecision == CoordinatorCompleted {
		if c.hooks != nil {
			hookContext, cancelHook := context.WithTimeout(context.Background(), c.cleanupTimeout)
			hookErr := c.hooks.Finalize(hookContext, CoordinatorFinalCut{
				Start: runStart, At: c.clock.Now(), Decision: terminalDecision,
				Prepare: prepareSnapshots, FinalSnapshots: checkpoint, Capacity: result.Capacity, Continuous: true,
			})
			cancelHook()
			if hookErr != nil {
				c.stopAfterFailure(fence, attempted)
				result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeFinalize
				return result
			}
		}
		result.Continuation = &CoordinatorContinuation{
			Assignments:   append([]CoordinatorAssignment(nil), assignments...),
			GrantSequence: result.Grant.Sequence,
			owner:         newCoordinatorObservationOwner(observationChannel, cancelObservation),
		}
		observationTransferred = true
		result.Outcome, result.Code, result.Snapshot = CoordinatorCompleted, CoordinatorCodeCompleted, preparedAggregate
		return result
	}
	final, err := c.stopAssignedSnapshots(fence, assigned)
	if err != nil {
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeFinalize
		return result
	}
	if c.hooks != nil {
		hookContext, cancelHook := context.WithTimeout(context.Background(), c.cleanupTimeout)
		hookErr := c.hooks.Finalize(hookContext, CoordinatorFinalCut{
			Start: runStart, At: c.clock.Now(), Decision: terminalDecision,
			Prepare: prepareSnapshots, FinalSnapshots: final, Capacity: result.Capacity,
		})
		cancelHook()
		if hookErr != nil {
			result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeFinalize
			return result
		}
	}
	aggregated, err := aggregator.Aggregate(final)
	if err != nil {
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeFinalize
		return result
	}
	result.Outcome, result.Snapshot = terminalDecision, aggregated
	switch terminalDecision {
	case CoordinatorCompleted:
		result.Code = CoordinatorCodeCompleted
	case CoordinatorStopped:
		result.Code = CoordinatorCodeStopped
	default:
		result.Code = CoordinatorCodeObserver
	}
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

func terminalSnapshotsNeedTrafficRecovery(snapshots []WorkerSnapshot) bool {
	for _, snapshot := range snapshots {
		sessions := snapshot.Sessions
		if sessions.Target > 0 && (sessions.Online < sessions.Target || sessions.TrafficReady < sessions.Target ||
			sessions.Starting > 0 || sessions.Closing > 0) {
			return true
		}
	}
	return false
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
		return interval >= coordinatorGrantCadence-coordinatorGrantTickTolerance &&
			interval <= coordinatorGrantCadence+coordinatorGrantTickTolerance
	}
	return interval >= coordinatorGrantCadence && interval < 2*coordinatorGrantCadence
}

func coordinatorGrantCoverageMissing(at, lastTickAt time.Time) bool {
	return at.Sub(lastTickAt) > coordinatorGrantCadence+coordinatorGrantTickTolerance
}

// deliverGrant applies one exact grant vector concurrently to all workers.
// maximum distinguishes the pre-clock control round from measured cadence.
func (c *Coordinator) deliverGrant(
	parent context.Context,
	assignments []CoordinatorAssignment,
	request WorkerGrantRequest,
	maxRoundTimeout time.Duration,
) (coordinatorRoundDisposition, CoordinatorWorkerFailure) {
	if maxRoundTimeout <= 0 {
		return coordinatorRoundStageFailed, CoordinatorWorkerFailure{}
	}
	grantRoundTimeout := min(c.roundTimeout, maxRoundTimeout)
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
	var workerFailure CoordinatorWorkerFailure
	for workerID, result := range results {
		if workerFailure.RuntimeCode == "" {
			var apiError *WorkerAPIError
			if errors.As(result.err, &apiError) && validRuntimeFailureCode(apiError.RuntimeCode) {
				workerFailure = CoordinatorWorkerFailure{WorkerID: uint64(workerID), RuntimeCode: apiError.RuntimeCode}
			}
		}
		expectedReleased, _ := request.Released.worker(uint64(workerID))
		evidence[workerID] = coordinatorRoundEvidence{err: result.err, valid: result.err == nil &&
			sameWorkerFence(result.response.WorkerFence, assignments[workerID].WorkerFence) &&
			result.response.WorkerID == uint64(workerID) && result.response.WorkerCount == coordinatorWorkerCount &&
			result.response.Sequence == request.Sequence && result.response.Released == expectedReleased}
	}
	disposition := resolveCoordinatorRoundDisposition(parent, roundContext, evidence)
	if disposition == coordinatorRoundStageFailed && workerFailure.RuntimeCode == "" {
		workerFailure = c.recoverLateGrantFailure(parent, assignments, request)
	}
	return disposition, workerFailure
}

// recoverLateGrantFailure reads only the bounded worker status projection
// after a grant delivery deadline. It never retries the mutation. An admitted
// grant may finish under generation ownership after its HTTP caller has gone;
// this bounded poll retains that cached runtime classification before cleanup
// destroys the worker process.
func (c *Coordinator) recoverLateGrantFailure(
	parent context.Context,
	assignments []CoordinatorAssignment,
	request WorkerGrantRequest,
) CoordinatorWorkerFailure {
	if parent == nil || parent.Err() != nil || len(assignments) != coordinatorWorkerCount || c.roundTimeout <= 0 {
		return CoordinatorWorkerFailure{}
	}
	evidenceContext, cancel := context.WithTimeoutCause(parent, c.roundTimeout, errCoordinatorRoundDeadline)
	defer cancel()
	for {
		type statusResult struct {
			status WorkerStatus
			err    error
		}
		results := [coordinatorWorkerCount]statusResult{}
		var wait sync.WaitGroup
		wait.Add(coordinatorWorkerCount)
		for workerID := 0; workerID < coordinatorWorkerCount; workerID++ {
			go func() {
				defer wait.Done()
				results[workerID].status, results[workerID].err = c.workers[workerID].Status(evidenceContext)
			}()
		}
		wait.Wait()

		pending := false
		for workerID, result := range results {
			if result.err != nil || !validCoordinatorGrantEvidenceStatus(result.status, assignments[workerID].WorkerAssignment) {
				continue
			}
			if result.status.LastGrantSequence == request.Sequence && result.status.LastGrantFailed &&
				validRuntimeFailureCode(result.status.LastGrantRuntimeCode) {
				return CoordinatorWorkerFailure{WorkerID: uint64(workerID), RuntimeCode: result.status.LastGrantRuntimeCode}
			}
			if result.status.ActiveGrantSequence == request.Sequence {
				pending = true
			}
		}
		if !pending {
			return CoordinatorWorkerFailure{}
		}
		timer := time.NewTimer(coordinatorGrantEvidencePoll)
		select {
		case <-evidenceContext.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return CoordinatorWorkerFailure{}
		case <-timer.C:
		}
	}
}

func validCoordinatorGrantEvidenceStatus(status WorkerStatus, assignment WorkerAssignment) bool {
	if status.RunID != assignment.RunID || status.AssignmentID != assignment.AssignmentID ||
		status.Generation != assignment.Generation || status.WorkerID != assignment.WorkerID ||
		status.WorkerCount != assignment.WorkerCount ||
		(status.Phase != WorkerPhaseRunning && status.Phase != WorkerPhaseFinal) {
		return false
	}
	if status.Phase == WorkerPhaseFinal && status.ActiveGrantSequence != 0 {
		return false
	}
	if status.ActiveGrantSequence != 0 &&
		(status.LastGrantSequence == math.MaxUint64 || status.ActiveGrantSequence != status.LastGrantSequence+1) {
		return false
	}
	if status.LastGrantFailed && status.LastGrantSequence == 0 {
		return false
	}
	if status.LastGrantRuntimeCode != "" &&
		(!status.LastGrantFailed || !validRuntimeFailureCode(status.LastGrantRuntimeCode)) {
		return false
	}
	return true
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
	Sequence      uint64
	RatePerSecond uint64
	MaxBurst      uint64
	RateChanged   bool
	Fresh         [coordinatorWorkerCount]uint64
	Released      [coordinatorWorkerCount]uint64
	Credit        [coordinatorWorkerCount]uint64
}

// CoordinatorGrantPlan owns one global allocator and sequences complete grant
// vectors for all workers. Each worker applies only its indexed vector share.
type CoordinatorGrantPlan struct {
	fence      WorkerFence
	rate       uint64
	burst      uint64
	allocator  *RateAllocator
	sequence   uint64
	pending    scheduledRate
	hasPending bool
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
		fence: first.WorkerFence, rate: uint64(rate), burst: uint64(burst), allocator: allocator,
	}, nil
}

// ScheduleRate stages one exact global capacity rate for the next Tick. Any
// retained credit from the old rate is discarded when that Tick applies it.
func (p *CoordinatorGrantPlan) ScheduleRate(rate uint64) error {
	if p == nil || p.allocator == nil || p.hasPending || rate == 0 || rate > math.MaxUint64/2 {
		return ErrCoordinatorConfig
	}
	burst := 2 * rate
	if err := p.allocator.ScheduleRate(rate, burst); err != nil {
		return ErrCoordinatorConfig
	}
	p.pending, p.hasPending = scheduledRate{rate: rate, burst: burst}, true
	return nil
}

// updateCapacityWorkers performs only the concurrent worker control round. It
// never mutates the owner-only grant plan; the grant loop commits a successful
// round on its own next Tick.
func (c *Coordinator) updateCapacityWorkers(
	parent context.Context,
	fence WorkerFence,
	rate uint64,
) coordinatorRoundDisposition {
	if c == nil || parent == nil || !validWorkerFence(fence) ||
		len(c.workers) != coordinatorWorkerCount || c.roundTimeout <= 0 ||
		rate == 0 || rate > math.MaxUint64/2 {
		return coordinatorRoundStageFailed
	}
	workers := [coordinatorWorkerCount]CoordinatorRateWorker{}
	for workerID, worker := range c.workers {
		rateWorker, ok := worker.(CoordinatorRateWorker)
		if !ok || rateWorker == nil {
			return coordinatorRoundStageFailed
		}
		workers[workerID] = rateWorker
	}

	roundContext, cancel := context.WithTimeoutCause(parent, c.roundTimeout, errCoordinatorRoundDeadline)
	defer cancel()
	request := WorkerRateRequest{
		WorkerFence: fence, RatePerSecond: rate, MaxBurst: 2 * rate,
	}
	type rateResult struct {
		status WorkerStatus
		coordinatorRoundEvidence
	}
	results := [coordinatorWorkerCount]rateResult{}
	var wait sync.WaitGroup
	wait.Add(coordinatorWorkerCount)
	for workerID, worker := range workers {
		go func() {
			defer wait.Done()
			result := &results[workerID]
			result.status, result.err = worker.UpdateRate(roundContext, request)
			status := result.status
			result.valid = result.err == nil &&
				sameWorkerFence(WorkerFence{
					RunID: status.RunID, AssignmentID: status.AssignmentID, Generation: status.Generation,
				}, fence) &&
				status.WorkerID == uint64(workerID) && status.WorkerCount == coordinatorWorkerCount &&
				status.Phase == WorkerPhaseRunning && status.TrafficReady && !status.Unexpected
		}()
	}
	wait.Wait()
	var evidence [coordinatorWorkerCount]coordinatorRoundEvidence
	for workerID, result := range results {
		evidence[workerID] = result.coordinatorRoundEvidence
	}
	return resolveCoordinatorRoundDisposition(parent, roundContext, evidence)
}

// Tick releases one vector and verifies the fixed-size global sums before it
// can become coordinator evidence.
func (p *CoordinatorGrantPlan) Tick(demand [coordinatorWorkerCount]uint64) (CoordinatorGrant, error) {
	if p == nil || p.allocator == nil {
		return CoordinatorGrant{}, ErrCoordinatorConfig
	}
	if p.sequence == math.MaxUint64 {
		return CoordinatorGrant{}, ErrCoordinatorConfig
	}
	tick, err := p.allocator.Tick(demand[:])
	if err != nil || len(tick.Fresh) != coordinatorWorkerCount || len(tick.Released) != coordinatorWorkerCount || len(tick.Credit) != coordinatorWorkerCount {
		return CoordinatorGrant{}, ErrCoordinatorConfig
	}
	var grant CoordinatorGrant
	p.sequence++
	grant.Sequence = p.sequence
	if p.hasPending {
		p.rate, p.burst = p.pending.rate, p.pending.burst
		p.pending, p.hasPending = scheduledRate{}, false
		grant.RateChanged = true
	}
	grant.RatePerSecond, grant.MaxBurst = p.rate, p.burst
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
		WorkerFence: fence, Sequence: grant.Sequence, RatePerSecond: grant.RatePerSecond, MaxBurst: grant.MaxBurst,
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

	Sessions                      WorkerSessionSnapshot
	Generated                     WorkerGeneratedSnapshot
	Messages                      WorkerMessageSnapshot
	Sync                          WorkerSyncSnapshot
	HotSendackLatency             WorkerHistogramSnapshot
	ColdFirstCreateSendackLatency WorkerHistogramSnapshot
	LifecycleReheatSendackLatency WorkerHistogramSnapshot
	RecvackLatency                WorkerHistogramSnapshot
	SendPendingToWriteLatency     WorkerHistogramSnapshot
	SendWriteToAckLatency         WorkerHistogramSnapshot
	Correlation                   WorkerCorrelationSnapshot
	Queues                        WorkerQueueSnapshot
	Harness                       WorkerHarnessSnapshot

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
	thresholdSchema := ordered[0].Sync.Thresholds
	for workerID := 1; workerID < coordinatorWorkerCount; workerID++ {
		if ordered[workerID].Sync.Thresholds.P99Limit != thresholdSchema.P99Limit ||
			ordered[workerID].Sync.Thresholds.P999Limit != thresholdSchema.P999Limit {
			return CoordinatorSnapshot{}, ErrCoordinatorHistogramSchema
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
		validCoordinatorHistogram(snapshot.HotSendackLatency) &&
		validCoordinatorHistogram(snapshot.ColdFirstCreateSendackLatency) &&
		validCoordinatorHistogram(snapshot.LifecycleReheatSendackLatency) &&
		validCoordinatorHistogram(snapshot.RecvackLatency) &&
		validCoordinatorHistogram(snapshot.SendPendingToWriteLatency) &&
		validCoordinatorHistogram(snapshot.SendWriteToAckLatency)
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
		coordinatorClassificationRank(current.Harness.Classification) < coordinatorClassificationRank(previous.Harness.Classification) ||
		coordinatorClassificationRank(current.Evidence.Classification) < coordinatorClassificationRank(previous.Evidence.Classification) {
		return true
	}
	currentCounters := []uint64{
		current.Sessions.PlannedNew, current.Sessions.PlannedReturning, current.Sessions.CompletedNew,
		current.Sessions.CompletedReturning, current.Sessions.Expired,
		current.Sessions.CloseReasons.Expired, current.Sessions.CloseReasons.HeartbeatFailed,
		current.Sessions.CloseReasons.RemoteTerminal, current.Sessions.CloseReasons.ReadFailed,
		current.Sessions.CloseReasons.GenerationStop, current.Sessions.CloseReasons.ExplicitLogout,
		current.Sessions.CloseReasons.TransportCloseFailed,
		current.Generated.Primary, current.Generated.Person, current.Generated.Group,
		current.Generated.Canary, current.Generated.PayloadBytes,
		current.Messages.Sent, current.Messages.SendAttempts, current.Messages.SendAcknowledged,
		current.Messages.FirstAttempts, current.Messages.FirstAttemptFailures,
		current.Messages.SendRejected, current.Messages.Received, current.Messages.ReceiveAcknowledged,
		current.Messages.ReceiveAckFailures, current.Messages.RetryAttempts, current.Messages.Terminal,
		current.Messages.TerminalReasons.RetryExhausted.Total,
		current.Messages.TerminalReasons.RetryExhausted.AttemptTimeout,
		current.Messages.TerminalReasons.RetryExhausted.LocalAdmission,
		current.Messages.TerminalReasons.RetryExhausted.TransportError,
		current.Messages.TerminalReasons.RetryExhausted.RetriableSendack,
		current.Messages.TerminalReasons.RetryExhausted.Unclassified,
		current.Messages.TerminalReasons.NonRetriable,
		current.Messages.TerminalReasons.SessionClosed,
		current.Messages.Losses, current.Messages.Duplicates, current.Messages.Corruptions, current.Messages.SequenceRegressions,
		current.Sync.CompletedNew, current.Sync.CompletedReturning, current.Sync.FactoryFailed,
		current.Sync.FactoryCanceled, current.Sync.ConnectStarted, current.Sync.ConnectCompleted,
		current.Sync.ConnectFailed, current.Sync.ConnectCanceled, current.Sync.SyncStarted,
		current.Sync.SyncCompleted, current.Sync.SyncFailed, current.Sync.SyncCanceled, current.Sync.Failures,
		current.Sync.Thresholds.Count, current.Sync.Thresholds.AboveP99,
		current.Sync.Thresholds.AboveP999, current.Sync.Thresholds.Above10Seconds,
		current.Correlation.Sampled, current.Correlation.Delivered, current.Correlation.Expired,
		current.Correlation.DuplicateCompletions, current.Correlation.ConflictingCompletions,
		current.Correlation.UnknownAcknowledgments,
		current.Harness.Failures, current.Harness.CommandSaturation, current.Harness.OfferedUnderdelivery,
		current.Harness.PlannedCancellations,
		current.Queues.TransportRejected,
	}
	previousCounters := []uint64{
		previous.Sessions.PlannedNew, previous.Sessions.PlannedReturning, previous.Sessions.CompletedNew,
		previous.Sessions.CompletedReturning, previous.Sessions.Expired,
		previous.Sessions.CloseReasons.Expired, previous.Sessions.CloseReasons.HeartbeatFailed,
		previous.Sessions.CloseReasons.RemoteTerminal, previous.Sessions.CloseReasons.ReadFailed,
		previous.Sessions.CloseReasons.GenerationStop, previous.Sessions.CloseReasons.ExplicitLogout,
		previous.Sessions.CloseReasons.TransportCloseFailed,
		previous.Generated.Primary, previous.Generated.Person, previous.Generated.Group,
		previous.Generated.Canary, previous.Generated.PayloadBytes,
		previous.Messages.Sent, previous.Messages.SendAttempts, previous.Messages.SendAcknowledged,
		previous.Messages.FirstAttempts, previous.Messages.FirstAttemptFailures,
		previous.Messages.SendRejected, previous.Messages.Received, previous.Messages.ReceiveAcknowledged,
		previous.Messages.ReceiveAckFailures, previous.Messages.RetryAttempts, previous.Messages.Terminal,
		previous.Messages.TerminalReasons.RetryExhausted.Total,
		previous.Messages.TerminalReasons.RetryExhausted.AttemptTimeout,
		previous.Messages.TerminalReasons.RetryExhausted.LocalAdmission,
		previous.Messages.TerminalReasons.RetryExhausted.TransportError,
		previous.Messages.TerminalReasons.RetryExhausted.RetriableSendack,
		previous.Messages.TerminalReasons.RetryExhausted.Unclassified,
		previous.Messages.TerminalReasons.NonRetriable,
		previous.Messages.TerminalReasons.SessionClosed,
		previous.Messages.Losses, previous.Messages.Duplicates, previous.Messages.Corruptions, previous.Messages.SequenceRegressions,
		previous.Sync.CompletedNew, previous.Sync.CompletedReturning, previous.Sync.FactoryFailed,
		previous.Sync.FactoryCanceled, previous.Sync.ConnectStarted, previous.Sync.ConnectCompleted,
		previous.Sync.ConnectFailed, previous.Sync.ConnectCanceled, previous.Sync.SyncStarted,
		previous.Sync.SyncCompleted, previous.Sync.SyncFailed, previous.Sync.SyncCanceled, previous.Sync.Failures,
		previous.Sync.Thresholds.Count, previous.Sync.Thresholds.AboveP99,
		previous.Sync.Thresholds.AboveP999, previous.Sync.Thresholds.Above10Seconds,
		previous.Correlation.Sampled, previous.Correlation.Delivered, previous.Correlation.Expired,
		previous.Correlation.DuplicateCompletions, previous.Correlation.ConflictingCompletions,
		previous.Correlation.UnknownAcknowledgments,
		previous.Harness.Failures, previous.Harness.CommandSaturation, previous.Harness.OfferedUnderdelivery,
		previous.Harness.PlannedCancellations,
		previous.Queues.TransportRejected,
	}
	for index := range currentCounters {
		if currentCounters[index] < previousCounters[index] {
			return true
		}
	}
	for hashSlot := range current.MetaCreate.PersonByHashSlot {
		if current.MetaCreate.PersonByHashSlot[hashSlot] < previous.MetaCreate.PersonByHashSlot[hashSlot] {
			return true
		}
		if current.MetaCreate.GroupByHashSlot[hashSlot] < previous.MetaCreate.GroupByHashSlot[hashSlot] {
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
		coordinatorHistogramRegressed(current.HotSendackLatency, previous.HotSendackLatency) ||
		coordinatorHistogramRegressed(current.ColdFirstCreateSendackLatency, previous.ColdFirstCreateSendackLatency) ||
		coordinatorHistogramRegressed(current.LifecycleReheatSendackLatency, previous.LifecycleReheatSendackLatency) ||
		coordinatorHistogramRegressed(current.RecvackLatency, previous.RecvackLatency) ||
		coordinatorHistogramRegressed(current.SendPendingToWriteLatency, previous.SendPendingToWriteLatency) ||
		coordinatorHistogramRegressed(current.SendWriteToAckLatency, previous.SendWriteToAckLatency)
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
		HotSendackLatency:             newWorkerHistogramSnapshot(),
		ColdFirstCreateSendackLatency: newWorkerHistogramSnapshot(),
		LifecycleReheatSendackLatency: newWorkerHistogramSnapshot(),
		RecvackLatency:                newWorkerHistogramSnapshot(),
		SendPendingToWriteLatency:     newWorkerHistogramSnapshot(),
		SendWriteToAckLatency:         newWorkerHistogramSnapshot(),
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
		if err := addCoordinatorHistogram(&result.HotSendackLatency, snapshot.HotSendackLatency); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorHistogram(&result.ColdFirstCreateSendackLatency, snapshot.ColdFirstCreateSendackLatency); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorHistogram(&result.LifecycleReheatSendackLatency, snapshot.LifecycleReheatSendackLatency); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorHistogram(&result.RecvackLatency, snapshot.RecvackLatency); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorHistogram(&result.SendPendingToWriteLatency, snapshot.SendPendingToWriteLatency); err != nil {
			return CoordinatorSnapshot{}, err
		}
		if err := addCoordinatorHistogram(&result.SendWriteToAckLatency, snapshot.SendWriteToAckLatency); err != nil {
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
	if err := addCoordinatorInt(&total.Closing, value.Closing); err != nil {
		return err
	}
	if err := addCoordinatorInt(&total.TrafficReady, value.TrafficReady); err != nil {
		return err
	}
	values := [12]struct{ destination, source *uint64 }{
		{&total.PlannedNew, &value.PlannedNew}, {&total.PlannedReturning, &value.PlannedReturning},
		{&total.CompletedNew, &value.CompletedNew}, {&total.CompletedReturning, &value.CompletedReturning},
		{&total.Expired, &value.Expired},
		{&total.CloseReasons.Expired, &value.CloseReasons.Expired},
		{&total.CloseReasons.HeartbeatFailed, &value.CloseReasons.HeartbeatFailed},
		{&total.CloseReasons.RemoteTerminal, &value.CloseReasons.RemoteTerminal},
		{&total.CloseReasons.ReadFailed, &value.CloseReasons.ReadFailed},
		{&total.CloseReasons.GenerationStop, &value.CloseReasons.GenerationStop},
		{&total.CloseReasons.ExplicitLogout, &value.CloseReasons.ExplicitLogout},
		{&total.CloseReasons.TransportCloseFailed, &value.CloseReasons.TransportCloseFailed},
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
	values := [23]struct{ destination, source *uint64 }{
		{&total.Sent, &value.Sent}, {&total.SendAttempts, &value.SendAttempts},
		{&total.SendAcknowledged, &value.SendAcknowledged},
		{&total.FirstAttempts, &value.FirstAttempts}, {&total.FirstAttemptFailures, &value.FirstAttemptFailures},
		{&total.SendRejected, &value.SendRejected},
		{&total.Received, &value.Received}, {&total.ReceiveAcknowledged, &value.ReceiveAcknowledged},
		{&total.ReceiveAckFailures, &value.ReceiveAckFailures}, {&total.RetryAttempts, &value.RetryAttempts},
		{&total.Terminal, &value.Terminal},
		{&total.TerminalReasons.RetryExhausted.Total, &value.TerminalReasons.RetryExhausted.Total},
		{&total.TerminalReasons.RetryExhausted.AttemptTimeout, &value.TerminalReasons.RetryExhausted.AttemptTimeout},
		{&total.TerminalReasons.RetryExhausted.LocalAdmission, &value.TerminalReasons.RetryExhausted.LocalAdmission},
		{&total.TerminalReasons.RetryExhausted.TransportError, &value.TerminalReasons.RetryExhausted.TransportError},
		{&total.TerminalReasons.RetryExhausted.RetriableSendack, &value.TerminalReasons.RetryExhausted.RetriableSendack},
		{&total.TerminalReasons.RetryExhausted.Unclassified, &value.TerminalReasons.RetryExhausted.Unclassified},
		{&total.TerminalReasons.NonRetriable, &value.TerminalReasons.NonRetriable},
		{&total.TerminalReasons.SessionClosed, &value.TerminalReasons.SessionClosed},
		{&total.Losses, &value.Losses}, {&total.Duplicates, &value.Duplicates},
		{&total.Corruptions, &value.Corruptions}, {&total.SequenceRegressions, &value.SequenceRegressions},
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
	if err := addCoordinatorHistogram(&total.Latency, value.Latency); err != nil {
		return err
	}
	return addCoordinatorLatencyThresholds(&total.Thresholds, value.Thresholds)
}

func addCoordinatorLatencyThresholds(total *LatencyThresholdCounters, value LatencyThresholdCounters) error {
	if total.P99Limit == 0 && total.P999Limit == 0 {
		total.P99Limit, total.P999Limit = value.P99Limit, value.P999Limit
	}
	if total.P99Limit != value.P99Limit || total.P999Limit != value.P999Limit {
		return ErrCoordinatorHistogramSchema
	}
	fields := [4]struct{ destination, source *uint64 }{
		{&total.Count, &value.Count}, {&total.AboveP99, &value.AboveP99},
		{&total.AboveP999, &value.AboveP999}, {&total.Above10Seconds, &value.Above10Seconds},
	}
	return addCoordinatorUint64Fields(fields[:])
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
	return addCoordinatorUint64(&total.TransportRejected, value.TransportRejected)
}

func addCoordinatorHarness(total *WorkerHarnessSnapshot, value WorkerHarnessSnapshot) error {
	total.Classification = mergeSyncClassification(total.Classification, value.Classification)
	values := [4]struct{ destination, source *uint64 }{
		{&total.Failures, &value.Failures}, {&total.CommandSaturation, &value.CommandSaturation},
		{&total.OfferedUnderdelivery, &value.OfferedUnderdelivery},
		{&total.PlannedCancellations, &value.PlannedCancellations},
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

func validateCoordinatorContinuation(
	capacity Config,
	generation uint64,
	admission CapacityAdmission,
	continuation CoordinatorContinuation,
) ([]CoordinatorAssignment, error) {
	if continuation.GrantSequence == 0 || len(continuation.Assignments) != coordinatorWorkerCount {
		return nil, ErrCoordinatorConfig
	}
	formal := continuation.Assignments[0].Config
	prepared, err := PrepareCapacityConfig(formal, admission.Checkpoint, admission.Reference)
	if err != nil {
		return nil, ErrCoordinatorConfig
	}
	preparedBody, err := json.Marshal(prepared)
	if err != nil {
		return nil, ErrCoordinatorConfig
	}
	capacityBody, err := json.Marshal(capacity)
	if err != nil || !bytes.Equal(preparedBody, capacityBody) {
		return nil, ErrCoordinatorConfig
	}
	expected, err := BuildCoordinatorAssignments(formal, generation)
	if err != nil {
		return nil, ErrCoordinatorConfig
	}
	expectedBody, err := json.Marshal(expected)
	if err != nil {
		return nil, ErrCoordinatorConfig
	}
	actualBody, err := json.Marshal(continuation.Assignments)
	if err != nil || !bytes.Equal(expectedBody, actualBody) {
		return nil, ErrCoordinatorConfig
	}
	return append([]CoordinatorAssignment(nil), continuation.Assignments...), nil
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
