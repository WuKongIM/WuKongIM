package chatlifecycle

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestProductionCapacityEvidenceUsesExactWindowDeltas(t *testing.T) {
	cfg := productionCapacityConfig(t)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "capacity-window", Generation: 7}
	start := time.Unix(1_970_000_000, 0).UTC()
	request := CapacityEvidenceRequest{
		Phase: CapacityPhaseMeasure, RatePerSecond: 2_000,
		Start: start, End: start.Add(cfg.Capacity.Step.Measure),
	}
	observation := &productionCapacityObservation{}
	observation.set(productionCapacityObservationSnapshot(cfg, start, 1, 10, 5, 0))
	lifecycle := &productionCapacityLifecycle{}
	lifecycle.set(productionCapacityLifecycleSnapshot(1, 1))
	var workers [coordinatorWorkerCount]ProductionCapacityWorker
	for workerID := range workers {
		baseline := productionCapacityWorkerSnapshot(cfg, fence, uint64(workerID), 1, 0, 0)
		end := productionCapacityWorkerSnapshot(cfg, fence, uint64(workerID), 2, 10_000, 0)
		workers[workerID] = &productionCapacityWorker{snapshots: []WorkerSnapshot{baseline, end}}
	}
	evidence, err := NewProductionCapacityEvidence(ProductionCapacityEvidenceOptions{
		Config: cfg, Workers: workers, Observation: observation, Lifecycle: lifecycle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := evidence.BeginCapacity(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	observation.set(productionCapacityObservationSnapshot(cfg, request.End, 2, 9, 4, 0))
	lifecycle.set(productionCapacityLifecycleSnapshot(2, 2))

	result, err := evidence.ObserveCapacity(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.CorrectnessFailure || result.HarnessInvalid ||
		!result.ErrorRateAccepted || !result.LatencyAccepted || !result.QueueInflightAccepted ||
		!result.ClusterLagAccepted || !result.ResourceAccepted || !result.ReadinessAccepted ||
		!result.LifecycleAccepted {
		t.Fatalf("capacity observation = %+v, want complete pass", result)
	}
}

func TestProductionCapacityEvidenceRejectsWindowWithoutBaseline(t *testing.T) {
	cfg := productionCapacityConfig(t)
	evidence, err := NewProductionCapacityEvidence(ProductionCapacityEvidenceOptions{
		Config: cfg,
		Workers: [coordinatorWorkerCount]ProductionCapacityWorker{
			&productionCapacityWorker{}, &productionCapacityWorker{}, &productionCapacityWorker{},
		},
		Observation: &productionCapacityObservation{}, Lifecycle: &productionCapacityLifecycle{},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := CapacityEvidenceRequest{Phase: CapacityPhaseMeasure, RatePerSecond: 2_000, Start: time.Now(), End: time.Now().Add(time.Minute)}
	if _, err := evidence.ObserveCapacity(context.Background(), request); err == nil {
		t.Fatal("ObserveCapacity succeeded without BeginCapacity")
	}
}

func TestProductionCapacityTreatsUnsafeDiskAsFatalNotCapacityWarning(t *testing.T) {
	cfg := productionCapacityConfig(t)
	request := CapacityEvidenceRequest{
		Phase: CapacityPhaseMeasure, RatePerSecond: 2_000,
		Start: time.Unix(1_970_100_000, 0).UTC(), End: time.Unix(1_970_100_000, 0).UTC().Add(20 * time.Minute),
	}
	baseline := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, WorkerFence{RunID: cfg.RunID, AssignmentID: "disk", Generation: 2}, 1, time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.Start, 1, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(1, 1),
	}
	current := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, WorkerFence{RunID: cfg.RunID, AssignmentID: "disk", Generation: 2}, 2, 2*time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.End, 2, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(2, 2),
	}
	for index := range baseline.workers {
		baseline.workers[index].Sessions.Target = 1
		baseline.workers[index].Sessions.Online = 1
		baseline.workers[index].Sessions.TrafficReady = 1
		current.workers[index].Sessions.Target = 1
		current.workers[index].Sessions.Online = 1
		current.workers[index].Sessions.TrafficReady = 1
	}
	current.observation.Signals = []VerdictSignal{{Outcome: VerdictInfrastructureFailure, Cause: VerdictCauseDiskExhausted}}
	observation, err := reduceProductionCapacityWindow(cfg, request, baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.InfrastructureFailure || observation.ResourceAccepted {
		t.Fatalf("unsafe disk observation = %+v", observation)
	}
}

func TestProductionCapacityQueueGateUsesDeclaredEightyPercentBoundary(t *testing.T) {
	for _, test := range []struct {
		current int
		want    bool
	}{
		{80, true},
		{81, false},
	} {
		if got := productionQueueBelow(test.current, 100); got != test.want {
			t.Fatalf("queue %d/100 accepted = %t, want %t", test.current, got, test.want)
		}
	}
}

func TestProductionCapacityAttributesSustainedHostSaturationWithoutHarnessInvalid(t *testing.T) {
	cfg := productionCapacityConfig(t)
	start := time.Unix(1_970_200_000, 0).UTC()
	request := CapacityEvidenceRequest{Phase: CapacityPhaseMeasure, RatePerSecond: 2_000, Start: start, End: start.Add(20 * time.Minute)}
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "resource-window", Generation: 3}
	baseline := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 1, time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.Start, 1, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(1, 1),
	}
	current := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 2, 2*time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.End, 2, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(2, 2),
	}
	prepareCapacityWorkersReady(baseline.workers)
	prepareCapacityWorkersReady(current.workers)
	current.observation.ResourceEvidence.Capacity.CPUHighSamples[0] = 4
	current.observation.ResourceEvidence.Capacity.CPUSustainedEvents[0] = 1
	result, err := reduceProductionCapacityWindow(cfg, request, baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ResourceEvidenceComplete || !result.ResourceSaturated || result.ResourceHeadroom || result.ResourceAccepted || result.HarnessInvalid {
		t.Fatalf("sustained saturation result = %+v", result)
	}
}

func TestProductionCapacityCarriesExistingSustainedSaturationIntoNextWindow(t *testing.T) {
	cfg := productionCapacityConfig(t)
	start := time.Unix(1_970_250_000, 0).UTC()
	request := CapacityEvidenceRequest{Phase: CapacityPhaseMeasure, RatePerSecond: 2_000, Start: start, End: start.Add(20 * time.Minute)}
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "active-resource-window", Generation: 3}
	baseline := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 1, time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.Start, 1, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(1, 1),
	}
	current := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 2, 2*time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.End, 2, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(2, 2),
	}
	prepareCapacityWorkersReady(baseline.workers)
	prepareCapacityWorkersReady(current.workers)
	baseline.observation.ResourceEvidence.Capacity.CPUSustainedEvents[0] = 1
	baseline.observation.ResourceEvidence.Capacity.CPUSustainedActive[0] = true
	current.observation.ResourceEvidence.Capacity.CPUSustainedEvents[0] = 1
	current.observation.ResourceEvidence.Capacity.CPUSustainedActive[0] = true
	result, err := reduceProductionCapacityWindow(cfg, request, baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ResourceSaturated || result.ResourceHeadroom || result.ResourceAccepted {
		t.Fatalf("existing sustained saturation result = %+v", result)
	}
}

func TestProductionCapacityRecoveryAcceptsSaturationThatClearedByWindowEnd(t *testing.T) {
	cfg := productionCapacityConfig(t)
	start := time.Unix(1_970_265_000, 0).UTC()
	request := CapacityEvidenceRequest{
		Phase: CapacityPhaseRecovery, RatePerSecond: 2_000, Start: start, End: start.Add(30 * time.Minute),
	}
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "recovered-resource-window", Generation: 3}
	baseline := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 1, time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.Start, 1, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(1, 1),
	}
	current := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 2, 2*time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.End, 2, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(2, 2),
	}
	prepareCapacityWorkersReady(baseline.workers)
	prepareCapacityWorkersReady(current.workers)
	current.observation.ResourceEvidence.Capacity.CPUSustainedEvents[0] = 1
	current.observation.ResourceEvidence.Capacity.HostCPUPercentBasisPoints[0] = 2_000
	result, err := reduceProductionCapacityWindow(cfg, request, baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ResourceAccepted || result.ResourceSaturated || !result.ResourcePreviouslySaturated || result.HarnessInvalid {
		t.Fatalf("recovered in-window saturation result = %+v", result)
	}
}

func TestProductionCapacityRejectsWorkerQueueGapBeforeWindowBaseline(t *testing.T) {
	cfg := productionCapacityConfig(t)
	start := time.Unix(1_970_270_000, 0).UTC()
	request := CapacityEvidenceRequest{Phase: CapacityPhaseMeasure, RatePerSecond: 2_000, Start: start, End: start.Add(20 * time.Minute)}
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "queue-gap-window", Generation: 3}
	baseline := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 1, time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.Start, 1, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(1, 1),
	}
	current := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 2, 2*time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.End, 2, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(2, 2),
	}
	prepareCapacityWorkersReady(baseline.workers)
	prepareCapacityWorkersReady(current.workers)
	baseline.observation.ResourceEvidence.Capacity.WorkerQueueMissingSamples = 1
	current.observation.ResourceEvidence.Capacity.WorkerQueueMissingSamples = 1
	result, err := reduceProductionCapacityWindow(cfg, request, baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if result.ResourceEvidenceComplete || result.ResourceAccepted {
		t.Fatalf("pre-baseline worker queue gap was accepted: %+v", result)
	}
}

func TestProductionCapacityRetainsRecoveredFormalSaturationAsWarning(t *testing.T) {
	cfg := productionCapacityConfig(t)
	start := time.Unix(1_970_275_000, 0).UTC()
	request := CapacityEvidenceRequest{Phase: CapacityPhaseMeasure, RatePerSecond: 2_000, Start: start, End: start.Add(20 * time.Minute)}
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "prior-resource-window", Generation: 3}
	baseline := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 1, time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.Start, 1, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(1, 1),
	}
	current := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 2, 2*time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.End, 2, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(2, 2),
	}
	prepareCapacityWorkersReady(baseline.workers)
	prepareCapacityWorkersReady(current.workers)
	baseline.observation.ResourceEvidence.Capacity.CPUSustainedEvents[0] = 1
	current.observation.ResourceEvidence.Capacity.CPUSustainedEvents[0] = 1
	result, err := reduceProductionCapacityWindow(cfg, request, baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ResourcePreviouslySaturated || result.ResourceSaturated || !result.ResourceHeadroom || !result.ResourceAccepted {
		t.Fatalf("recovered prior saturation result = %+v", result)
	}
}

func TestProductionCapacityTreatsOfferedUnderdeliveryAsInfrastructureEvidence(t *testing.T) {
	cfg := productionCapacityConfig(t)
	start := time.Unix(1_970_300_000, 0).UTC()
	request := CapacityEvidenceRequest{Phase: CapacityPhaseMeasure, RatePerSecond: 2_000, Start: start, End: start.Add(20 * time.Minute)}
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "load-window", Generation: 4}
	baseline := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 1, time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.Start, 1, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(1, 1),
	}
	current := productionCapacityCut{
		workers:     productionControllerWorkerSnapshots(cfg, fence, 2, 2*time.Minute, WorkerPhaseRunning),
		observation: productionCapacityObservationSnapshot(cfg, request.End, 2, 1, 1, 0),
		lifecycle:   productionCapacityLifecycleSnapshot(2, 2),
	}
	prepareCapacityWorkersReady(baseline.workers)
	prepareCapacityWorkersReady(current.workers)
	current.workers[0].Harness.OfferedUnderdelivery = 1
	result, err := reduceProductionCapacityWindow(cfg, request, baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if !result.LoadUnderdelivered || result.ResourceAccepted || result.HarnessInvalid || !result.ResourceEvidenceComplete {
		t.Fatalf("load underdelivery result = %+v", result)
	}
}

func prepareCapacityWorkersReady(workers []WorkerSnapshot) {
	for index := range workers {
		workers[index].Sessions.Target = 1
		workers[index].Sessions.Online = 1
		workers[index].Sessions.TrafficReady = 1
	}
}

func TestProductionCapacitySeparatesClusterUnavailabilityFromRateBoundary(t *testing.T) {
	baseline := ProductionObservationSnapshot{ClusterEvidence: ReportClusterEvidence{
		HealthySamples: 10, LogicalSlotGroups: 12, LeaderGroups: 12, FullReplicaGroups: 12,
	}}
	current := baseline
	current.ClusterEvidence.HealthySamples++
	if productionCapacityClusterUnavailable(baseline, current) || !productionCapacityClusterAccepted(baseline, current) {
		t.Fatalf("healthy cluster was rejected: %+v", current.ClusterEvidence)
	}
	current.ClusterEvidence.UnhealthySamples++
	if !productionCapacityClusterUnavailable(baseline, current) || productionCapacityClusterAccepted(baseline, current) {
		t.Fatalf("unavailable cluster was treated as a capacity boundary: %+v", current.ClusterEvidence)
	}
}

func productionCapacityConfig(t *testing.T) Config {
	t.Helper()
	cfg := FormalConfig()
	cfg.RunID = "production-capacity"
	cfg.Mode = ModeCapacity
	cfg.Capacity.AgedCheckpoint = AgedCheckpoint{
		Reference: "aged.json", Completed: true, Passed: true, Duration: 72 * time.Hour,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

type productionCapacityWorker struct {
	mu        sync.Mutex
	snapshots []WorkerSnapshot
}

func (w *productionCapacityWorker) Snapshot(context.Context) (WorkerSnapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.snapshots) == 0 {
		return WorkerSnapshot{}, errProductionCapacity
	}
	snapshot := w.snapshots[0]
	w.snapshots = w.snapshots[1:]
	return snapshot, nil
}

type productionCapacityObservation struct {
	mu       sync.Mutex
	snapshot ProductionObservationSnapshot
}

func (o *productionCapacityObservation) set(snapshot ProductionObservationSnapshot) {
	o.mu.Lock()
	o.snapshot = snapshot
	o.mu.Unlock()
}

func (o *productionCapacityObservation) Snapshot() ProductionObservationSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return cloneProductionObservationSnapshot(o.snapshot)
}

type productionCapacityLifecycle struct {
	mu       sync.Mutex
	snapshot LifecycleProofSnapshot
}

func (l *productionCapacityLifecycle) set(snapshot LifecycleProofSnapshot) {
	l.mu.Lock()
	l.snapshot = snapshot
	l.mu.Unlock()
}

func (l *productionCapacityLifecycle) Snapshot() LifecycleProofSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshot
}

func productionCapacityWorkerSnapshot(
	cfg Config,
	fence WorkerFence,
	workerID, sequence, attempts, failures uint64,
) WorkerSnapshot {
	snapshot := productionControllerWorkerSnapshots(cfg, fence, sequence, time.Minute*time.Duration(sequence), WorkerPhaseRunning)[workerID]
	snapshot.Messages.FirstAttempts = attempts
	snapshot.Messages.FirstAttemptFailures = failures
	snapshot.SendackLatency = newWorkerHistogramSnapshot()
	snapshot.SendackLatency.Count = attempts
	if attempts > 0 {
		snapshot.SendackLatency.SumNanos = attempts * uint64(time.Millisecond)
		snapshot.SendackLatency.MaxNanos = uint64(time.Millisecond)
		snapshot.SendackLatency.Buckets[1] = attempts
	}
	snapshot.Sync.Thresholds.Count = attempts
	snapshot.Sessions.Target = 10
	snapshot.Sessions.Online = snapshot.Sessions.Target
	snapshot.Sessions.TrafficReady = snapshot.Sessions.Target
	snapshot.Queues = WorkerQueueSnapshot{
		WorkCapacity: 100, RetryCapacity: 100, InflightCapacity: 100, TransportCapacity: 100,
		WorkCurrent: 1, RetryCurrent: 1, InflightCurrent: 1, TransportCurrent: 1,
	}
	return snapshot
}

func productionCapacityObservationSnapshot(
	cfg Config,
	at time.Time,
	sequence uint64,
	queue, inflight float64,
	activation uint64,
) ProductionObservationSnapshot {
	snapshot := newProductionControllerObservation(cfg, at).snapshot
	snapshot.Sequence = sequence
	snapshot.ActivationRejections = activation
	snapshot.ClusterEvidence.HealthySamples = sequence
	resources := &snapshot.ResourceEvidence.Capacity
	resources.Samples = sequence
	resources.WorkerQueueSamples = sequence
	resources.SustainedWindow = cfg.Thresholds.Resource.SustainedSaturationWindow
	resources.Complete = true
	resources.ProcessesComplete = true
	resources.WorkerQueuesComplete = true
	for index := 0; index < productionHostCount; index++ {
		minimum := uint64(cfg.Thresholds.MinimumDataFilesystemBytes)
		if index == coordinatorWorkerCount {
			minimum = uint64(cfg.Thresholds.Resource.MinimumLoadFilesystemBytes)
		}
		resources.DataFilesystemBytes[index] = minimum
		resources.DataFilesystemAvailableBytes[index] = minimum * 9 / 10
		resources.SystemFilesystemBytes[index] = 40_000_000_000
		resources.SystemFilesystemAvailableBytes[index] = 20_000_000_000
	}
	for index := range snapshot.Resources {
		snapshot.Resources[index].QueueDepth = queue
		snapshot.Resources[index].Inflight = inflight
		snapshot.ResourceEvidence.Nodes[index].QueueCurrent = uint64(queue)
		snapshot.ResourceEvidence.Nodes[index].InflightCurrent = uint64(inflight)
	}
	return snapshot
}

func productionCapacityLifecycleSnapshot(completed, latencyCount uint64) LifecycleProofSnapshot {
	snapshot := LifecycleProofSnapshot{Completed: completed, ReheatLatency: newWorkerHistogramSnapshot()}
	for index := range snapshot.ReheatLatency.BucketUpper {
		if snapshot.ReheatLatency.BucketUpper[index] >= uint64(time.Millisecond) {
			snapshot.ReheatLatency.Buckets[index] = latencyCount
			break
		}
	}
	snapshot.ReheatLatency.Count = latencyCount
	snapshot.ReheatLatency.SumNanos = latencyCount * uint64(time.Millisecond)
	snapshot.ReheatLatency.MaxNanos = uint64(time.Millisecond)
	return snapshot
}
