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
