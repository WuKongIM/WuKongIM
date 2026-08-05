package chatlifecycle

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestProductionEvidenceControllerWritesOperatorStopFinalAfterJoinedLifecycle(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "production-controller-stop"
	start := time.Unix(1_960_000_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller", Generation: 4}
	observation := newProductionControllerObservation(cfg, start)
	lifecycle := &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})}
	meta := &productionControllerMeta{}
	accounting := NewMetaCreateAccounting()
	dataset := &productionControllerDataset{digest: hashReportValue("production-controller-dataset")}
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: t.TempDir(), Observation: observation,
		Lifecycle: lifecycle, Meta: meta, MetaAccounting: accounting,
		Dataset: dataset, SlotAssignment: mustInitialLifecycleSlotAssignment(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if err := controller.Begin(context.Background(), CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}); err != nil {
		t.Fatal(err)
	}
	prepare := productionControllerWorkerSnapshots(cfg, fence, 1, time.Minute, WorkerPhaseRunning)
	decision, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{
		Start: CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start},
		Kind:  CoordinatorCutTerminal, At: start.Add(time.Minute), Snapshots: prepare, StopRequested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision != CoordinatorStopped {
		t.Fatalf("terminal decision = %q, want %q", decision, CoordinatorStopped)
	}
	if meta.calls != 0 {
		t.Fatalf("live terminal cut performed racy meta reconciliation: calls=%d", meta.calls)
	}
	final := productionControllerWorkerSnapshots(cfg, fence, 2, time.Minute+time.Second, WorkerPhaseFinal)
	if err := controller.Finalize(context.Background(), CoordinatorFinalCut{
		Start: CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}, At: start.Add(time.Minute + time.Second),
		Decision: CoordinatorStopped, Prepare: prepare, FinalSnapshots: final,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lifecycle.done:
	default:
		t.Fatal("Finalize returned before lifecycle Run joined")
	}
	report, err := ReadReport(filepath.Join(controller.OutputDir(), "final.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict.Outcome != VerdictOperatorStop || report.Verdict.Cause != VerdictCauseOperatorRequested ||
		!report.Verdict.Terminal || !report.Final || report.DatasetDigest != dataset.digest {
		t.Fatalf("final report = %+v", report)
	}
	if meta.calls != 1 || dataset.calls != 3 {
		t.Fatalf("meta/dataset calls = %d/%d, want 1/3", meta.calls, dataset.calls)
	}
}

func TestProductionEvidenceControllerPersistsFrozenLifecycleProductFailure(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "production-controller-lifecycle-product"
	start := time.Unix(1_960_025_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller-product", Generation: 7}
	lifecycle := &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{
		ProductFailures: 1, ReheatLatency: newWorkerHistogramSnapshot(),
	}, done: make(chan struct{})}
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: t.TempDir(), Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: lifecycle, Meta: &productionControllerMeta{}, MetaAccounting: NewMetaCreateAccounting(),
		Dataset:        &productionControllerDataset{digest: hashReportValue("production-controller-product-dataset")},
		SlotAssignment: mustInitialLifecycleSlotAssignment(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	startCut := CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}
	if err := controller.Begin(context.Background(), startCut); err != nil {
		t.Fatal(err)
	}
	prepare := productionControllerWorkerSnapshots(cfg, fence, 1, time.Minute, WorkerPhaseRunning)
	decision, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{
		Start: startCut, Kind: CoordinatorCutTerminal, At: start.Add(time.Minute), Snapshots: prepare,
	})
	if err != nil || decision != CoordinatorProductFailure {
		t.Fatalf("lifecycle product terminal = %q/%v", decision, err)
	}
	final := productionControllerWorkerSnapshots(cfg, fence, 2, time.Minute+time.Second, WorkerPhaseFinal)
	if err := controller.Finalize(context.Background(), CoordinatorFinalCut{
		Start: startCut, At: start.Add(time.Minute + time.Second), Decision: decision,
		Prepare: prepare, FinalSnapshots: final,
	}); err != nil {
		t.Fatalf("Finalize() rejected frozen product evidence: %v", err)
	}
	report, err := ReadReport(filepath.Join(controller.OutputDir(), "final.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict.Outcome != VerdictProductFailure || report.Verdict.Cause != VerdictCauseLifecycleProduct {
		t.Fatalf("final product verdict = %+v", report.Verdict)
	}
}

func TestProductionEvidenceControllerSkipsPeriodicCutUntilFirstObservation(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "production-controller-await-observation"
	start := time.Unix(1_960_050_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller-await", Generation: 8}
	observation := &productionControllerObservation{}
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: t.TempDir(), Observation: observation,
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
		Meta:      &productionControllerMeta{}, MetaAccounting: NewMetaCreateAccounting(),
		Dataset:        &productionControllerDataset{digest: hashReportValue("production-controller-await-dataset")},
		SlotAssignment: mustInitialLifecycleSlotAssignment(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	startCut := CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}
	if err := controller.Begin(context.Background(), startCut); err != nil {
		t.Fatal(err)
	}
	decision, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{
		Start: startCut, Kind: CoordinatorCutPeriodic, At: start.Add(cfg.Observation.Cadence),
		Snapshots: productionControllerWorkerSnapshots(cfg, fence, 1, cfg.Observation.Cadence, WorkerPhaseRunning),
	})
	if err != nil || decision != "" {
		t.Fatalf("periodic cut before first observation = %q/%v, want skipped", decision, err)
	}
}

func TestProductionEvidenceControllerWritesNonTerminalQualificationAndKeepsRunning(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "production-controller-qualification"
	start := time.Unix(1_960_100_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller-q", Generation: 5}
	output := t.TempDir()
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: output, Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
		Meta:      &productionControllerMeta{}, MetaAccounting: NewMetaCreateAccounting(),
		Dataset:        &productionControllerDataset{digest: hashReportValue("production-controller-qualification-dataset")},
		SlotAssignment: mustInitialLifecycleSlotAssignment(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	startCut := CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}
	if err := controller.Begin(context.Background(), startCut); err != nil {
		t.Fatal(err)
	}
	decision, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{
		Start: startCut, Kind: CoordinatorCutQualification,
		At:        start.Add(cfg.Thresholds.Timeline.Checkpoint),
		Snapshots: productionControllerWorkerSnapshots(cfg, fence, 1, cfg.Thresholds.Timeline.Checkpoint, WorkerPhaseRunning),
	})
	if err != nil || decision != "" {
		t.Fatalf("qualification decision/error = %q/%v", decision, err)
	}
	report, err := ReadReport(filepath.Join(output, "qualification.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Kind != CheckpointQualification || report.Final || !report.Continue || report.Verdict.Terminal {
		t.Fatalf("qualification report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(output, "final.json")); !os.IsNotExist(err) {
		t.Fatalf("qualification unexpectedly wrote final report: %v", err)
	}
}

type productionControllerObservation struct {
	begin    time.Time
	snapshot ProductionObservationSnapshot
}

func newProductionControllerObservation(cfg Config, at time.Time) *productionControllerObservation {
	resources := make([]NodeResourceSample, coordinatorWorkerCount)
	evidence := ReportResourceEvidence{}
	for index := range resources {
		resources[index] = NodeResourceSample{NodeID: uint64(index + 1), ForcedGC: true, HeapBytes: 100, Goroutines: 10}
		evidence.Nodes[index] = ReportResourceNodeEvidence{
			DataFilesystemBytes:          uint64(cfg.Thresholds.MinimumDataFilesystemBytes),
			DataFilesystemAvailableBytes: uint64(cfg.Thresholds.MinimumDataFilesystemBytes) * 9 / 10,
			ForcedGCSamples:              1, HeapStartBytes: 100, HeapEndBytes: 100,
			GoroutineStart: 10, GoroutineEnd: 10,
		}
	}
	return &productionControllerObservation{snapshot: ProductionObservationSnapshot{
		Sequence: 1, At: at, Resources: resources, ResourceEvidence: evidence,
		ClusterEvidence: ReportClusterEvidence{HealthySamples: 1, LogicalSlotGroups: 12, LeaderGroups: 12, FullReplicaGroups: 12},
	}}
}

func (s *productionControllerObservation) Begin(start time.Time) error { s.begin = start; return nil }
func (s *productionControllerObservation) Snapshot() ProductionObservationSnapshot {
	return cloneProductionObservationSnapshot(s.snapshot)
}

type productionControllerLifecycle struct {
	mu       sync.Mutex
	snapshot LifecycleProofSnapshot
	done     chan struct{}
}

func (l *productionControllerLifecycle) Run(ctx context.Context, _ WorkerFence) error {
	<-ctx.Done()
	close(l.done)
	return ctx.Err()
}
func (l *productionControllerLifecycle) Snapshot() LifecycleProofSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshot
}

type productionControllerMeta struct{ calls int }

func (m *productionControllerMeta) Checkpoint(context.Context, []WorkerSnapshot, LifecycleSlotAssignment, bool) error {
	m.calls++
	return nil
}

type productionControllerDataset struct {
	digest string
	calls  int
}

func (d *productionControllerDataset) ProbeDatasetDigest(context.Context, Config) (string, error) {
	d.calls++
	return d.digest, nil
}

func productionControllerWorkerSnapshots(cfg Config, fence WorkerFence, sequence uint64, uptime time.Duration, phase WorkerPhase) []WorkerSnapshot {
	snapshots := coordinatorSnapshotFixture(fence, sequence, uptime, 1)
	for index := range snapshots {
		snapshots[index].Phase = phase
		snapshots[index].Messages.FirstAttempts = uint64(index + 1)
		snapshots[index].Sync.Thresholds = LatencyThresholdCounters{
			P99Limit: cfg.Thresholds.Latency.Sync.P99, P999Limit: cfg.Thresholds.Latency.Sync.P999,
		}
	}
	return snapshots
}
