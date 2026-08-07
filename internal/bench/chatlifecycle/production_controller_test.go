package chatlifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	accounting := NewMetaCreateAccounting()
	meta := &productionControllerMeta{accounting: accounting}
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

func TestProductionEvidenceControllerContinuesFormalBoundaryWithoutRestartingEvidenceSources(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "production-controller-continuous-formal"
	start := time.Unix(1_960_006_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller-continuous", Generation: 1}
	formalOutput := t.TempDir()
	capacityOutput := t.TempDir()
	observation := newProductionControllerObservation(cfg, start)
	lifecycle := &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})}
	accounting := NewMetaCreateAccounting()
	dataset := &productionControllerDataset{digest: hashReportValue("production-controller-continuous-dataset")}
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: formalOutput, Observation: observation,
		Lifecycle: lifecycle, Meta: &productionControllerMeta{accounting: accounting}, MetaAccounting: accounting,
		Dataset: dataset, SlotAssignment: mustInitialLifecycleSlotAssignment(t), Continuous: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	startCut := CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}
	if err := controller.Begin(context.Background(), startCut); err != nil {
		t.Fatal(err)
	}
	// This unit focuses on the boundary/finalization transaction. Qualification
	// sequencing and the 72-hour reducer are covered independently.
	controller.mu.Lock()
	controller.recorder.qualificationCaptured = true
	controller.frozen = VerdictSnapshot{Outcome: VerdictPass, Cause: VerdictCauseCompleted, Terminal: true}
	controller.lastObservation = observation.Snapshot()
	controller.lastObservationID = controller.lastObservation.Sequence
	controller.mu.Unlock()
	running := productionControllerWorkerSnapshots(cfg, fence, 1, 72*time.Hour, WorkerPhaseRunning)
	if err := controller.Finalize(context.Background(), CoordinatorFinalCut{
		Start: startCut, At: start.Add(72 * time.Hour), Decision: CoordinatorCompleted,
		Prepare: running, FinalSnapshots: running, Continuous: true,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lifecycle.done:
		t.Fatal("continuous formal boundary stopped the lifecycle proof loop")
	default:
	}
	formalPath := filepath.Join(formalOutput, "final.json")
	report, err := ReadReport(formalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Continuous || report.Fence.Generation != 1 || report.Verdict.Outcome != VerdictPass {
		t.Fatalf("continuous formal report = %+v", report)
	}
	for _, worker := range report.Workers {
		if worker.Phase != WorkerPhaseRunning || worker.Generation != 1 {
			t.Fatalf("continuous formal worker = %+v", worker)
		}
	}
	capacity, err := PrepareCapacityConfig(cfg, report, formalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.ContinueCapacity(capacity, capacityOutput); err != nil {
		t.Fatal(err)
	}
	capacityStart := CoordinatorRunStart{Config: capacity, Fence: fence, StartedAt: start.Add(72*time.Hour + time.Second)}
	if err := controller.Begin(context.Background(), capacityStart); err != nil {
		t.Fatal(err)
	}
	if observation.begin != start {
		t.Fatalf("capacity reset observation start to %v, want %v", observation.begin, start)
	}
	select {
	case <-lifecycle.done:
		t.Fatal("capacity Begin restarted or stopped the lifecycle proof loop")
	default:
	}
}

func TestProductionEvidenceControllerFreezesRehearsalPassWithoutFormalLongWindows(t *testing.T) {
	cfg := RehearsalConfig()
	cfg.RunID = "production-controller-rehearsal"
	start := time.Unix(1_960_012_500, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller-rehearsal", Generation: 5}
	accounting := NewMetaCreateAccounting()
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: t.TempDir(), Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
		Meta:      &productionControllerMeta{accounting: accounting}, MetaAccounting: accounting,
		Dataset:        &productionControllerDataset{digest: hashReportValue("production-controller-rehearsal-dataset")},
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
	startBody, err := os.ReadFile(filepath.Join(controller.OutputDir(), "run-start.json"))
	if err != nil || !strings.Contains(string(startBody), RunStartReceiptSchemaV1) ||
		strings.Contains(string(startBody), cfg.RunID) || strings.Contains(string(startBody), fence.AssignmentID) {
		t.Fatalf("bounded run-start receipt = %q/%v", startBody, err)
	}
	prepare := productionControllerWorkerSnapshots(cfg, fence, 1, 2*time.Hour, WorkerPhaseRunning)
	decision, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{
		Start: startCut, Kind: CoordinatorCutTerminal, At: start.Add(2 * time.Hour), Snapshots: prepare,
	})
	if err != nil || decision != CoordinatorCompleted {
		t.Fatalf("rehearsal terminal decision = %q/%v", decision, err)
	}
	final := productionControllerWorkerSnapshots(cfg, fence, 2, 2*time.Hour+time.Second, WorkerPhaseFinal)
	if err := controller.Finalize(context.Background(), CoordinatorFinalCut{
		Start: startCut, At: start.Add(2*time.Hour + time.Second), Decision: decision,
		Prepare: prepare, FinalSnapshots: final,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := ReadReport(filepath.Join(controller.OutputDir(), "final.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict.Outcome != VerdictRehearsalPass || report.Verdict.Cause != VerdictCauseRehearsalCompleted ||
		report.Stage != StageRehearsal || report.Window.FinalAt != start.Add(2*time.Hour) {
		t.Fatalf("rehearsal final report = %+v", report)
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
	accounting := NewMetaCreateAccounting()
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: t.TempDir(), Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: lifecycle, Meta: &productionControllerMeta{accounting: accounting, metricError: true}, MetaAccounting: accounting,
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
	if report.Verdict.Outcome != VerdictProductFailure || report.Verdict.Cause != VerdictCauseLifecycleProduct || report.MetaCreate.Errors != 1 {
		t.Fatalf("final product verdict = %+v", report.Verdict)
	}
}

func TestProductionEvidenceControllerSkipsPeriodicCutUntilFirstObservation(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "production-controller-await-observation"
	start := time.Unix(1_960_050_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller-await", Generation: 8}
	observation := &productionControllerObservation{}
	accounting := NewMetaCreateAccounting()
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: t.TempDir(), Observation: observation,
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
		Meta:      &productionControllerMeta{accounting: accounting}, MetaAccounting: accounting,
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

func TestFormalSoakLatencyAttributionJoinsResourceAndDeliveryEvidence(t *testing.T) {
	cfg := FormalConfig()
	complete := ReportCapacityResourceEvidence{
		Complete: true, ProcessesComplete: true, WorkerQueuesComplete: true,
	}
	for _, test := range []struct {
		name      string
		resources ReportCapacityResourceEvidence
		delivered uint64
		want      CapacityAttribution
	}{
		{name: "clear four-host headroom", resources: complete, want: CapacityAttributionProduct},
		{name: "load underdelivery", resources: complete, delivered: 1, want: CapacityAttributionInfrastructure},
		{name: "incomplete resource round", resources: ReportCapacityResourceEvidence{}, want: CapacityAttributionInsufficient},
		{name: "threshold high but not sustained", resources: func() ReportCapacityResourceEvidence {
			value := complete
			value.HostCPUPercentBasisPoints[0] = uint32((cfg.Thresholds.Resource.HostCPUPercent + 1) * 100)
			return value
		}(), want: CapacityAttributionInsufficient},
		{name: "sustained server saturation", resources: func() ReportCapacityResourceEvidence {
			value := complete
			value.CPUSustainedActive[0] = true
			return value
		}(), want: CapacityAttributionInfrastructure},
	} {
		t.Run(test.name, func(t *testing.T) {
			workers := make([]WorkerSnapshot, coordinatorWorkerCount)
			workers[0].Harness.OfferedUnderdelivery = test.delivered
			observation := ProductionObservationSnapshot{}
			observation.ResourceEvidence.Capacity = test.resources
			got, total, err := formalSoakLatencyAttribution(cfg, observation, workers, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want || total != test.delivered {
				t.Fatalf("attribution/underdelivery = %q/%d, want %q/%d", got, total, test.want, test.delivered)
			}
		})
	}
}

func TestProductionEvidenceControllerWritesNonTerminalQualificationAndKeepsRunning(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "production-controller-qualification"
	start := time.Unix(1_960_100_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller-q", Generation: 5}
	output := t.TempDir()
	accounting := NewMetaCreateAccounting()
	meta := &productionControllerMeta{accounting: accounting}
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: output, Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
		Meta:      meta, MetaAccounting: accounting,
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
	if meta.calls != 1 || report.MetaCreate.Checkpoints != 1 {
		t.Fatalf("qualification meta checkpoints = %d/%d, want 1/1", meta.calls, report.MetaCreate.Checkpoints)
	}
	if _, err := os.Stat(filepath.Join(output, "final.json")); !os.IsNotExist(err) {
		t.Fatalf("qualification unexpectedly wrote final report: %v", err)
	}
}

func TestProductionEvidenceControllerClassifiesQualificationMetaDeficitAsProductFailure(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "production-controller-qualification-meta-product"
	start := time.Unix(1_960_125_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller-q-meta", Generation: 6}
	output := t.TempDir()
	accounting := NewMetaCreateAccounting()
	meta := &productionControllerMeta{accounting: accounting, deficit: true, harnessOnCall: 2}
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: output, Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
		Meta:      meta, MetaAccounting: accounting,
		Dataset:        &productionControllerDataset{digest: hashReportValue("production-controller-q-meta-dataset")},
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
	checkpointAt := start.Add(cfg.Thresholds.Timeline.Checkpoint)
	prepare := productionControllerWorkerSnapshots(cfg, fence, 1, cfg.Thresholds.Timeline.Checkpoint, WorkerPhaseRunning)
	decision, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{
		Start: startCut, Kind: CoordinatorCutQualification, At: checkpointAt, Snapshots: prepare,
	})
	if err != nil || decision != CoordinatorProductFailure {
		t.Fatalf("qualification meta decision/error = %q/%v", decision, err)
	}
	final := productionControllerWorkerSnapshots(cfg, fence, 2, cfg.Thresholds.Timeline.Checkpoint+time.Second, WorkerPhaseFinal)
	if err := controller.Finalize(context.Background(), CoordinatorFinalCut{
		Start: startCut, At: checkpointAt.Add(time.Second), Decision: decision, Prepare: prepare, FinalSnapshots: final,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := ReadReport(filepath.Join(output, "final.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict.Outcome != VerdictProductFailure || report.Verdict.Cause != VerdictCauseMetaCreateProduct ||
		report.MetaCreate.ExpectedUnique != 1 || report.MetaCreate.Created != 0 || report.MetaCreate.Checkpoints != 1 {
		t.Fatalf("qualification meta product report = %+v", report)
	}
}

func TestProductionEvidenceControllerClassifiesFinalMetaErrorAsProductFailure(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "production-controller-final-meta-product"
	start := time.Unix(1_960_150_000, 0).UTC()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "production-controller-final-meta", Generation: 7}
	output := t.TempDir()
	accounting := NewMetaCreateAccounting()
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: output, Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
		Meta:      &productionControllerMeta{accounting: accounting, metricError: true}, MetaAccounting: accounting,
		Dataset:        &productionControllerDataset{digest: hashReportValue("production-controller-final-meta-dataset")},
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
		Start: startCut, Kind: CoordinatorCutTerminal, At: start.Add(time.Minute), Snapshots: prepare, StopRequested: true,
	})
	if err != nil || decision != CoordinatorStopped {
		t.Fatalf("terminal prepare decision/error = %q/%v", decision, err)
	}
	final := productionControllerWorkerSnapshots(cfg, fence, 2, time.Minute+time.Second, WorkerPhaseFinal)
	if err := controller.Finalize(context.Background(), CoordinatorFinalCut{
		Start: startCut, At: start.Add(time.Minute + time.Second), Decision: decision, Prepare: prepare, FinalSnapshots: final,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := ReadReport(filepath.Join(output, "final.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict.Outcome != VerdictProductFailure || report.Verdict.Cause != VerdictCauseMetaCreateProduct ||
		report.MetaCreate.Errors != 1 || report.MetaCreate.Checkpoints != 1 {
		t.Fatalf("final meta product report = %+v", report)
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

type productionControllerMeta struct {
	calls         int
	accounting    *MetaCreateAccounting
	deficit       bool
	metricError   bool
	harnessOnCall int
}

func (m *productionControllerMeta) Checkpoint(
	_ context.Context,
	_ []WorkerSnapshot,
	assignment LifecycleSlotAssignment,
	reheat bool,
) error {
	m.calls++
	if m.harnessOnCall > 0 && m.calls >= m.harnessOnCall {
		return ErrLifecycleHarnessInvalid
	}
	var counts MetaCreateHashSlotCounts
	var emptyCounts MetaCreateHashSlotCounts
	if m.deficit {
		counts[0] = 1
	}
	var errorsBySlot [formalLogicalSlotGroups]uint64
	if m.metricError {
		errorsBySlot[0] = 1
	}
	return m.accounting.Checkpoint(
		counts,
		emptyCounts,
		assignment,
		lifecycleMetaMetricsBySlot(
			[formalLogicalSlotGroups]uint64{},
			[formalLogicalSlotGroups]uint64{},
			errorsBySlot,
		),
		reheat,
	)
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
