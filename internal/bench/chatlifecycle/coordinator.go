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

const coordinatorWorkerCount = 3

var ErrCoordinatorConfig = errors.New("chat lifecycle coordinator: invalid configuration")

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
	UpdateRate(context.Context, WorkerRateRequest) (WorkerStatus, error)
	Checkpoint(context.Context, WorkerCheckpointRequest) (WorkerSnapshot, error)
	Stop(context.Context, WorkerStopRequest) (WorkerSnapshot, error)
}

// CoordinatorObserver is the existing continuous service observer boundary.
type CoordinatorObserver interface {
	Run(context.Context, Config) ObserverResult
}

// CoordinatorClock owns the final observation cutoff and worker-status cadence.
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
	return &Coordinator{
		generation: options.Generation, preflight: options.Preflight, setup: options.Setup,
		workers: workers, observer: options.Observer, clock: options.Clock, cleanupTimeout: options.CleanupTimeout,
	}, nil
}

// Run enforces preflight -> setup -> assign -> start -> final cutoff -> checkpoint/finalize.
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
	attempted := [coordinatorWorkerCount]bool{}
	assigned := [coordinatorWorkerCount]bool{}
	for workerID, assignment := range assignments {
		attempted[workerID] = true
		status, assignErr := c.workers[workerID].Assign(ctx, assignment.WorkerAssignment)
		if assignErr != nil || !validCoordinatorStatus(status, assignment.WorkerAssignment, WorkerPhaseAssigned) {
			c.stopAfterFailure(fence, attempted)
			result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeAssignment
			return result
		}
		assigned[workerID] = true
	}
	for workerID, assignment := range assignments {
		status, startErr := c.workers[workerID].Start(ctx, WorkerStartRequest{WorkerFence: fence})
		if startErr != nil || !validCoordinatorStatus(status, assignment.WorkerAssignment, WorkerPhaseRunning) {
			c.stopAfterFailure(fence, attempted)
			result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeStart
			return result
		}
	}
	grant, err := grantPlan.Tick([coordinatorWorkerCount]uint64{math.MaxUint64, math.MaxUint64, math.MaxUint64})
	if err != nil {
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeGrant
		return result
	}
	result.Grant = grant
	for workerID, assignment := range assignments {
		status, rateErr := c.workers[workerID].UpdateRate(ctx, WorkerRateRequest{
			WorkerFence: fence, RatePerSecond: grantPlan.rate, MaxBurst: grantPlan.burst,
		})
		if rateErr != nil || !validCoordinatorStatus(status, assignment.WorkerAssignment, WorkerPhaseRunning) {
			c.stopAfterFailure(fence, attempted)
			result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeGrant
			return result
		}
	}

	observationDeadline := c.clock.Now().Add(cfg.Thresholds.Timeline.Final)
	observationContext, cancelObservation := context.WithCancel(ctx)
	ticker := c.clock.NewTicker(cfg.Observation.Cadence)
	if ticker == nil {
		cancelObservation()
		c.stopAfterFailure(fence, attempted)
		result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeRuntime
		return result
	}
	defer ticker.Stop()
	observationChannel := make(chan ObserverResult, 1)
	go func() { observationChannel <- c.observer.Run(observationContext, cfg) }()
	var observation ObserverResult
	cutoffOwned := false
	for {
		select {
		case observation = <-observationChannel:
			cancelObservation()
			goto observationComplete
		case <-ticker.C():
			if ctx.Err() != nil {
				cancelObservation()
				<-observationChannel
				c.stopAfterFailure(fence, attempted)
				result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
				return result
			}
			if !c.clock.Now().Before(observationDeadline) {
				cancelObservation()
				observation = <-observationChannel
				if ctx.Err() != nil {
					c.stopAfterFailure(fence, attempted)
					result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
					return result
				}
				cutoffOwned = true
				goto observationComplete
			}
			for workerID, assignment := range assignments {
				status, statusErr := c.workers[workerID].Status(observationContext)
				if statusErr != nil || !validCoordinatorStatus(status, assignment.WorkerAssignment, WorkerPhaseRunning) {
					cancelObservation()
					<-observationChannel
					c.stopAfterFailure(fence, attempted)
					result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeRuntime
					return result
				}
			}
		case <-ctx.Done():
			cancelObservation()
			<-observationChannel
			c.stopAfterFailure(fence, attempted)
			result.Outcome, result.Code = CoordinatorStopped, CoordinatorCodeStopped
			return result
		}
	}

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
	checkpoint := make([]WorkerSnapshot, coordinatorWorkerCount)
	for workerID := range c.workers {
		snapshot, checkpointErr := c.workers[workerID].Checkpoint(ctx, WorkerCheckpointRequest{WorkerFence: fence})
		if checkpointErr != nil {
			c.stopAfterFailure(fence, attempted)
			result.Outcome, result.Code = CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
			return result
		}
		checkpoint[workerID] = snapshot
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

func validCoordinatorStatus(status WorkerStatus, assignment WorkerAssignment, phase WorkerPhase) bool {
	return status.RunID == assignment.RunID && status.AssignmentID == assignment.AssignmentID &&
		status.Generation == assignment.Generation && status.WorkerID == assignment.WorkerID &&
		status.WorkerCount == assignment.WorkerCount && status.Phase == phase && !status.Unexpected
}

func (c *Coordinator) stopAfterFailure(fence WorkerFence, attempted [coordinatorWorkerCount]bool) {
	for workerID, worker := range c.workers {
		if !attempted[workerID] {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), c.cleanupTimeout)
		_, _ = worker.Stop(ctx, WorkerStopRequest{WorkerFence: fence})
		cancel()
	}
}

func (c *Coordinator) stopAssignedSnapshots(fence WorkerFence, assigned [coordinatorWorkerCount]bool) ([]WorkerSnapshot, error) {
	snapshots := make([]WorkerSnapshot, 0, coordinatorWorkerCount)
	var firstErr error
	for workerID, worker := range c.workers {
		if !assigned[workerID] {
			continue
		}
		stopContext, cancel := context.WithTimeout(context.Background(), c.cleanupTimeout)
		snapshot, err := worker.Stop(stopContext, WorkerStopRequest{WorkerFence: fence})
		cancel()
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil {
			snapshots = append(snapshots, snapshot)
		}
	}
	if firstErr != nil || len(snapshots) != coordinatorWorkerCount {
		return nil, ErrCoordinatorSnapshotCount
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
	Fresh    [coordinatorWorkerCount]uint64
	Released [coordinatorWorkerCount]uint64
	Credit   [coordinatorWorkerCount]uint64
}

// CoordinatorGrantPlan owns one global allocator. Workers receive the same
// global rate and independently emit only the deterministic local vector share.
type CoordinatorGrantPlan struct {
	rate      uint64
	burst     uint64
	allocator *RateAllocator
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
				WorkerFence: fence, WorkerID: workerID, WorkerCount: coordinatorWorkerCount, Config: assignmentConfig,
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
