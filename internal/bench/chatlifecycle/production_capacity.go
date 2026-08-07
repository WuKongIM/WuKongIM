package chatlifecycle

import (
	"context"
	"errors"
	"math"
	"sync"
)

var errProductionCapacity = errors.New("chat lifecycle production capacity evidence failed")

// ProductionCapacityWorker supplies one immutable generation snapshot at a
// capacity-window boundary.
type ProductionCapacityWorker interface {
	Snapshot(context.Context) (WorkerSnapshot, error)
}

type productionCapacityObservationSource interface {
	Snapshot() ProductionObservationSnapshot
}

type productionCapacityLifecycleSource interface {
	Snapshot() LifecycleProofSnapshot
}

// ProductionCapacityEvidenceOptions binds exact worker deltas to the same
// bounded observation and lifecycle sources used by the final report.
type ProductionCapacityEvidenceOptions struct {
	Config      Config
	Workers     [coordinatorWorkerCount]ProductionCapacityWorker
	Observation productionCapacityObservationSource
	Lifecycle   productionCapacityLifecycleSource
}

// ProductionCapacityEvidence captures one baseline at the exact phase
// boundary and reduces a second boundary into the closed staircase gates.
type ProductionCapacityEvidence struct {
	mu sync.Mutex

	cfg         Config
	workers     [coordinatorWorkerCount]ProductionCapacityWorker
	observation productionCapacityObservationSource
	lifecycle   productionCapacityLifecycleSource
	active      *productionCapacityWindow
}

type productionCapacityWindow struct {
	request  CapacityEvidenceRequest
	baseline productionCapacityCut
}

type productionCapacityCut struct {
	workers     []WorkerSnapshot
	observation ProductionObservationSnapshot
	lifecycle   LifecycleProofSnapshot
}

var _ CoordinatorCapacityEvidence = (*ProductionCapacityEvidence)(nil)

// NewProductionCapacityEvidence validates composition without polling.
func NewProductionCapacityEvidence(options ProductionCapacityEvidenceOptions) (*ProductionCapacityEvidence, error) {
	if options.Config.Validate() != nil || options.Config.Mode != ModeCapacity ||
		options.Observation == nil || options.Lifecycle == nil {
		return nil, errProductionCapacity
	}
	for _, worker := range options.Workers {
		if worker == nil {
			return nil, errProductionCapacity
		}
	}
	return &ProductionCapacityEvidence{
		cfg: options.Config, workers: options.Workers,
		observation: options.Observation, lifecycle: options.Lifecycle,
	}, nil
}

// BeginCapacity captures a non-overlapping measurement or recovery baseline.
// Coordinator starts this call asynchronously so one slow boundary read never
// suppresses the one-second global grant loop.
func (e *ProductionCapacityEvidence) BeginCapacity(ctx context.Context, request CapacityEvidenceRequest) error {
	if e == nil || ctx == nil || !validProductionCapacityRequest(request) {
		return errProductionCapacity
	}
	cut, err := e.capture(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active != nil {
		return errProductionCapacity
	}
	e.active = &productionCapacityWindow{request: request, baseline: cut}
	return nil
}

// ObserveCapacity closes the matching window and returns exact counter deltas
// plus current queue, cluster, resource, readiness, and lifecycle gates.
func (e *ProductionCapacityEvidence) ObserveCapacity(
	ctx context.Context,
	request CapacityEvidenceRequest,
) (CapacityObservation, error) {
	if e == nil || ctx == nil || !validProductionCapacityRequest(request) {
		return CapacityObservation{}, errProductionCapacity
	}
	e.mu.Lock()
	if e.active == nil || e.active.request != request {
		e.mu.Unlock()
		return CapacityObservation{}, errProductionCapacity
	}
	baseline := e.active.baseline
	e.active = nil
	e.mu.Unlock()

	current, err := e.capture(ctx)
	if err != nil {
		return CapacityObservation{}, err
	}
	return reduceProductionCapacityWindow(e.cfg, request, baseline, current)
}

func (e *ProductionCapacityEvidence) capture(ctx context.Context) (productionCapacityCut, error) {
	if err := ctx.Err(); err != nil {
		return productionCapacityCut{}, err
	}
	type result struct {
		index    int
		snapshot WorkerSnapshot
		err      error
	}
	results := make(chan result, coordinatorWorkerCount)
	for index, worker := range e.workers {
		go func(index int, worker ProductionCapacityWorker) {
			snapshot, err := worker.Snapshot(ctx)
			results <- result{index: index, snapshot: snapshot, err: err}
		}(index, worker)
	}
	workers := make([]WorkerSnapshot, coordinatorWorkerCount)
	failed := false
	for range coordinatorWorkerCount {
		result := <-results
		if result.err != nil {
			failed = true
			continue
		}
		workers[result.index] = result.snapshot
	}
	if err := ctx.Err(); err != nil {
		return productionCapacityCut{}, err
	}
	if failed {
		return productionCapacityCut{}, errProductionCapacity
	}
	return productionCapacityCut{
		workers: workers, observation: e.observation.Snapshot(), lifecycle: e.lifecycle.Snapshot(),
	}, nil
}

func validProductionCapacityRequest(request CapacityEvidenceRequest) bool {
	return (request.Phase == CapacityPhaseMeasure || request.Phase == CapacityPhaseRecovery) &&
		request.RatePerSecond > 0 && !request.Start.IsZero() && request.End.After(request.Start)
}

func reduceProductionCapacityWindow(
	cfg Config,
	request CapacityEvidenceRequest,
	baseline productionCapacityCut,
	current productionCapacityCut,
) (CapacityObservation, error) {
	if cfg.Validate() != nil || !validProductionCapacityRequest(request) ||
		!validProductionCapacityObservation(cfg, request, baseline.observation, false) ||
		!validProductionCapacityObservation(cfg, request, current.observation, true) ||
		current.observation.Sequence <= baseline.observation.Sequence ||
		current.observation.ActivationRejections < baseline.observation.ActivationRejections ||
		current.lifecycle.Completed < baseline.lifecycle.Completed ||
		current.lifecycle.ProductFailures < baseline.lifecycle.ProductFailures ||
		current.lifecycle.HarnessFailures < baseline.lifecycle.HarnessFailures {
		return CapacityObservation{}, errProductionCapacity
	}
	baselineCorrectness, baselineLatency, _, err := projectWorkerVerdictEvidence(cfg, baseline.workers, baseline.lifecycle)
	if err != nil {
		return CapacityObservation{}, errProductionCapacity
	}
	currentCorrectness, currentLatency, signals, err := projectWorkerVerdictEvidence(cfg, current.workers, current.lifecycle)
	if err != nil || correctnessCountersRegressed(currentCorrectness, baselineCorrectness) ||
		latencyCountersRegressed(currentLatency, baselineLatency) {
		return CapacityObservation{}, errProductionCapacity
	}
	deltaCorrectness := subtractProductionCorrectness(currentCorrectness, baselineCorrectness)
	deltaCorrectness.ActivationRejections = current.observation.ActivationRejections - baseline.observation.ActivationRejections
	deltaLatency := subtractLatencyCounters(currentLatency, baselineLatency)

	observation := CapacityObservation{Complete: true}
	observation.CorrectnessFailure = deltaCorrectness.TerminalSends > 0 || deltaCorrectness.ActivationRejections > 0 ||
		deltaCorrectness.Losses > 0 || deltaCorrectness.Duplicates > 0 || deltaCorrectness.Corruptions > 0 ||
		deltaCorrectness.SequenceRegressions > 0
	observation.HarnessInvalid = deltaCorrectness.QueueSaturations > 0 || deltaCorrectness.ObserverGaps > 0
	for _, signal := range append(signals, current.observation.Signals...) {
		switch signal.Outcome {
		case VerdictHarnessInvalid:
			observation.HarnessInvalid = true
		case VerdictProductFailure:
			observation.CorrectnessFailure = true
		case VerdictInfrastructureFailure:
			observation.InfrastructureFailure = true
		}
	}
	observation.ErrorRateAccepted = !rationalViolates(
		deltaCorrectness.FirstAttemptFailures,
		deltaCorrectness.FirstAttempts,
		cfg.Thresholds.Correctness.OverallFirstAttemptFailure,
	)
	observation.LatencyAccepted = productionCapacityLatencyAccepted(deltaLatency)
	observation.QueueInflightAccepted = productionCapacityQueuesAccepted(request, baseline, current)
	observation.ClusterUnavailable = productionCapacityClusterUnavailable(baseline.observation, current.observation)
	observation.ClusterLagAccepted = productionCapacityClusterAccepted(baseline.observation, current.observation)
	resourceComplete, resourceSaturated, resourceHeadroom, resourceOK := productionCapacityResourceWindow(
		baseline.observation.ResourceEvidence.Capacity,
		current.observation.ResourceEvidence.Capacity,
	)
	if !resourceOK {
		return CapacityObservation{}, errProductionCapacity
	}
	loadUnderdelivered, deliveryOK := productionCapacityLoadUnderdelivered(baseline.workers, current.workers)
	if !deliveryOK {
		return CapacityObservation{}, errProductionCapacity
	}
	observation.ResourceEvidenceComplete = resourceComplete
	resourceCurrentlyHigh := productionCapacityResourceCurrentlyHigh(cfg, current.observation.ResourceEvidence.Capacity)
	recoveredSaturation := request.Phase == CapacityPhaseRecovery && resourceSaturated && !resourceCurrentlyHigh
	observation.ResourceSaturated = resourceSaturated && !recoveredSaturation
	observation.ResourcePreviouslySaturated = recoveredSaturation || productionCapacityHadPriorSaturation(
		baseline.observation.ResourceEvidence.Capacity,
	)
	observation.ResourceHeadroom = resourceHeadroom
	observation.LoadUnderdelivered = loadUnderdelivered
	observation.ResourceAccepted = productionCapacityResourcesAccepted(cfg, request, baseline.observation, current.observation) &&
		resourceComplete && !observation.ResourceSaturated && !loadUnderdelivered &&
		(request.Phase != CapacityPhaseRecovery || !resourceCurrentlyHigh)
	observation.ReadinessAccepted = productionCapacityWorkersReady(current.workers)
	if !observation.ReadinessAccepted {
		observation.HarnessInvalid = true
	}
	observation.LifecycleAccepted = current.lifecycle.ProductFailures == baseline.lifecycle.ProductFailures &&
		current.lifecycle.HarnessFailures == baseline.lifecycle.HarnessFailures &&
		current.lifecycle.Completed > baseline.lifecycle.Completed && deltaLatency.Cold.Count > 0
	return observation, nil
}

func productionCapacityResourceCurrentlyHigh(cfg Config, resources ReportCapacityResourceEvidence) bool {
	cpuLimit := uint32(cfg.Thresholds.Resource.HostCPUPercent * 100)
	memoryLimit := uint32(cfg.Thresholds.Resource.HostMemoryPercent * 100)
	queueLimit := uint32(cfg.Thresholds.Resource.BoundedQueuePercent * 100)
	for host := 0; host < productionHostCount; host++ {
		if resources.HostCPUPercentBasisPoints[host] > cpuLimit ||
			resources.HostMemoryPercentBasisPoints[host] > memoryLimit {
			return true
		}
	}
	for node := 0; node < coordinatorWorkerCount; node++ {
		if resources.ServiceQueuePercentBasisPoints[node] > queueLimit {
			return true
		}
		for queue := 0; queue < workerBoundedQueueCount; queue++ {
			if resources.WorkerQueuePercentBasisPoints[node][queue] > queueLimit {
				return true
			}
		}
	}
	return false
}

func subtractProductionCorrectness(current, previous CorrectnessCounters) CorrectnessCounters {
	return CorrectnessCounters{
		FirstAttempts:        current.FirstAttempts - previous.FirstAttempts,
		FirstAttemptFailures: current.FirstAttemptFailures - previous.FirstAttemptFailures,
		TerminalSends:        current.TerminalSends - previous.TerminalSends,
		ActivationRejections: current.ActivationRejections - previous.ActivationRejections,
		Losses:               current.Losses - previous.Losses,
		Duplicates:           current.Duplicates - previous.Duplicates,
		Corruptions:          current.Corruptions - previous.Corruptions,
		SequenceRegressions:  current.SequenceRegressions - previous.SequenceRegressions,
		QueueSaturations:     current.QueueSaturations - previous.QueueSaturations,
		ObserverGaps:         current.ObserverGaps - previous.ObserverGaps,
	}
}

func productionCapacityLatencyAccepted(counters LatencyCounters) bool {
	for _, operation := range []LatencyThresholdCounters{counters.Hot, counters.Cold, counters.Sync} {
		if operation.Count == 0 || operation.Above10Seconds > 0 ||
			ratioAboveUnitFraction(operation.AboveP99, operation.Count, 100) ||
			ratioAboveUnitFraction(operation.AboveP999, operation.Count, 1_000) {
			return false
		}
	}
	return true
}

func validProductionCapacityObservation(
	cfg Config,
	request CapacityEvidenceRequest,
	snapshot ProductionObservationSnapshot,
	end bool,
) bool {
	if snapshot.Sequence == 0 || snapshot.At.IsZero() || len(snapshot.Resources) != coordinatorWorkerCount ||
		snapshot.ClusterEvidence.LogicalSlotGroups != uint64(cfg.Workload.Topology.LogicalSlotGroups) {
		return false
	}
	boundary := request.Start
	if end {
		boundary = request.End
	}
	return !snapshot.At.After(boundary.Add(cfg.Observation.Cadence)) &&
		!snapshot.At.Before(boundary.Add(-cfg.Observation.Cadence))
}

func productionCapacityQueuesAccepted(
	request CapacityEvidenceRequest,
	baseline productionCapacityCut,
	current productionCapacityCut,
) bool {
	for index, snapshot := range current.workers {
		queues := snapshot.Queues
		if !productionQueueBelow(queues.WorkCurrent, queues.WorkCapacity) ||
			!productionQueueBelow(queues.RetryCurrent, queues.RetryCapacity) ||
			!productionQueueBelow(queues.InflightCurrent, queues.InflightCapacity) ||
			!productionQueueBelow(queues.TransportCurrent, queues.TransportCapacity) {
			return false
		}
		if request.Phase == CapacityPhaseRecovery {
			previous := baseline.workers[index].Queues
			if queues.WorkCurrent > previous.WorkCurrent || queues.RetryCurrent > previous.RetryCurrent ||
				queues.InflightCurrent > previous.InflightCurrent || queues.TransportCurrent > previous.TransportCurrent {
				return false
			}
		}
	}
	for index, resource := range current.observation.Resources {
		if resource.QueueDepth < 0 || resource.Inflight < 0 {
			return false
		}
		if request.Phase == CapacityPhaseRecovery {
			previous := baseline.observation.Resources[index]
			if resource.QueueDepth > previous.QueueDepth || resource.Inflight > previous.Inflight {
				return false
			}
		}
	}
	return true
}

func productionQueueBelow(current, capacity int) bool {
	if current < 0 || capacity <= 0 {
		return false
	}
	// Equality at 80 percent is permitted; the declared warning boundary is
	// strictly above 80 percent for the retained 15-minute window.
	maximum := (capacity/5)*4 + (capacity%5)*4/5
	return current <= maximum
}

func productionCapacityClusterAccepted(baseline, current ProductionObservationSnapshot) bool {
	if productionCapacityClusterUnavailable(baseline, current) {
		return false
	}
	left, right := baseline.ClusterEvidence, current.ClusterEvidence
	return right.HealthySamples > left.HealthySamples && right.HotReplicaLagBreaches == left.HotReplicaLagBreaches
}

func productionCapacityClusterUnavailable(baseline, current ProductionObservationSnapshot) bool {
	left, right := baseline.ClusterEvidence, current.ClusterEvidence
	return right.UnhealthySamples != left.UnhealthySamples || right.LogicalSlotGroups != formalLogicalSlotGroups ||
		right.LeaderGroups != formalLogicalSlotGroups || right.FullReplicaGroups != formalLogicalSlotGroups
}

func productionCapacityResourcesAccepted(
	cfg Config,
	request CapacityEvidenceRequest,
	baseline, current ProductionObservationSnapshot,
) bool {
	for _, signal := range current.Signals {
		if signal.Outcome == VerdictInfrastructureFailure || signal.Cause == VerdictCauseDiskExhausted {
			return false
		}
	}
	for index, node := range current.ResourceEvidence.Nodes {
		if node.DataFilesystemBytes < uint64(cfg.Thresholds.MinimumDataFilesystemBytes) ||
			node.DataFilesystemAvailableBytes*100 < node.DataFilesystemBytes*uint64(cfg.Thresholds.DiskSafeStopFreePercent) {
			return false
		}
		if request.Phase == CapacityPhaseRecovery {
			previous := baseline.ResourceEvidence.Nodes[index]
			if node.QueueCurrent > previous.QueueCurrent || node.InflightCurrent > previous.InflightCurrent {
				return false
			}
		}
	}
	return true
}

func productionCapacityResourceWindow(
	baseline, current ReportCapacityResourceEvidence,
) (complete, saturated, headroom, valid bool) {
	if baseline.SustainedWindow <= 0 || current.SustainedWindow != baseline.SustainedWindow ||
		current.Samples < baseline.Samples || current.MissingSamples < baseline.MissingSamples {
		return false, false, false, false
	}
	complete = baseline.Complete && current.Complete && baseline.ProcessesComplete && current.ProcessesComplete &&
		current.Samples > baseline.Samples && baseline.MissingSamples == 0 && current.MissingSamples == 0 &&
		baseline.WorkerQueuesComplete && current.WorkerQueuesComplete && baseline.WorkerQueueMissingSamples == 0 &&
		current.WorkerQueueSamples > baseline.WorkerQueueSamples &&
		current.WorkerQueueMissingSamples == 0
	headroom = complete
	for index := 0; index < productionHostCount; index++ {
		if current.CPUHighSamples[index] < baseline.CPUHighSamples[index] ||
			current.MemoryHighSamples[index] < baseline.MemoryHighSamples[index] ||
			current.CPUSustainedEvents[index] < baseline.CPUSustainedEvents[index] ||
			current.MemorySustainedEvents[index] < baseline.MemorySustainedEvents[index] {
			return false, false, false, false
		}
		if current.CPUHighSamples[index] != baseline.CPUHighSamples[index] ||
			current.MemoryHighSamples[index] != baseline.MemoryHighSamples[index] ||
			current.CPUSustainedActive[index] || current.MemorySustainedActive[index] {
			headroom = false
		}
		if current.CPUSustainedEvents[index] != baseline.CPUSustainedEvents[index] ||
			current.MemorySustainedEvents[index] != baseline.MemorySustainedEvents[index] ||
			current.CPUSustainedActive[index] || current.MemorySustainedActive[index] {
			saturated = true
		}
	}
	for index := 0; index < coordinatorWorkerCount; index++ {
		if current.QueueHighSamples[index] < baseline.QueueHighSamples[index] ||
			current.QueueSustainedEvents[index] < baseline.QueueSustainedEvents[index] {
			return false, false, false, false
		}
		if current.QueueHighSamples[index] != baseline.QueueHighSamples[index] ||
			current.QueueSustainedActive[index] {
			headroom = false
		}
		if current.QueueSustainedEvents[index] != baseline.QueueSustainedEvents[index] ||
			current.QueueSustainedActive[index] {
			saturated = true
		}
	}
	for worker := 0; worker < coordinatorWorkerCount; worker++ {
		for queue := 0; queue < workerBoundedQueueCount; queue++ {
			if current.WorkerQueueHighSamples[worker][queue] < baseline.WorkerQueueHighSamples[worker][queue] ||
				current.WorkerQueueSustainedEvents[worker][queue] < baseline.WorkerQueueSustainedEvents[worker][queue] {
				return false, false, false, false
			}
			if current.WorkerQueueHighSamples[worker][queue] != baseline.WorkerQueueHighSamples[worker][queue] ||
				current.WorkerQueueSustainedActive[worker][queue] {
				headroom = false
			}
			if current.WorkerQueueSustainedEvents[worker][queue] != baseline.WorkerQueueSustainedEvents[worker][queue] ||
				current.WorkerQueueSustainedActive[worker][queue] {
				saturated = true
			}
		}
	}
	for host := 0; host < productionHostCount; host++ {
		for process := 0; process < productionProcessCount; process++ {
			if baseline.ProcessUp[host][process] != current.ProcessUp[host][process] ||
				current.ProcessCPUJiffies[host][process] < baseline.ProcessCPUJiffies[host][process] {
				return false, false, false, false
			}
		}
	}
	return complete, saturated, headroom, true
}

func productionCapacityHadPriorSaturation(resources ReportCapacityResourceEvidence) bool {
	for index := 0; index < productionHostCount; index++ {
		if resources.CPUSustainedEvents[index] > 0 || resources.MemorySustainedEvents[index] > 0 {
			return true
		}
	}
	for index := 0; index < coordinatorWorkerCount; index++ {
		if resources.QueueSustainedEvents[index] > 0 {
			return true
		}
		for queue := 0; queue < workerBoundedQueueCount; queue++ {
			if resources.WorkerQueueSustainedEvents[index][queue] > 0 {
				return true
			}
		}
	}
	return false
}

func productionCapacityLoadUnderdelivered(baseline, current []WorkerSnapshot) (bool, bool) {
	if len(baseline) != coordinatorWorkerCount || len(current) != coordinatorWorkerCount {
		return false, false
	}
	var before, after uint64
	for index := 0; index < coordinatorWorkerCount; index++ {
		if math.MaxUint64-before < baseline[index].Harness.OfferedUnderdelivery ||
			math.MaxUint64-after < current[index].Harness.OfferedUnderdelivery {
			return false, false
		}
		before += baseline[index].Harness.OfferedUnderdelivery
		after += current[index].Harness.OfferedUnderdelivery
	}
	if after < before {
		return false, false
	}
	return after > before, true
}

func productionCapacityWorkersReady(snapshots []WorkerSnapshot) bool {
	if len(snapshots) != coordinatorWorkerCount {
		return false
	}
	for _, snapshot := range snapshots {
		if snapshot.Phase != WorkerPhaseRunning || snapshot.Sessions.Target <= 0 ||
			snapshot.Sessions.Online < snapshot.Sessions.Target ||
			snapshot.Sessions.TrafficReady < snapshot.Sessions.Target {
			return false
		}
	}
	return true
}
