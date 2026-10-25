package chatlifecycle

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestProductionEvidenceControllerRejectsElapsedOnlyRehearsalWindow(t *testing.T) {
	for _, test := range []struct {
		name     string
		duration time.Duration
		loss     bool
		want     CoordinatorOutcome
	}{
		{"two hour rehearsal", 2 * time.Hour, false, CoordinatorHarnessInvalid},
		{"short direct diagnostic", 16 * time.Minute, false, CoordinatorHarnessInvalid},
		{"real correctness failure retains priority", 2 * time.Hour, true, CoordinatorProductFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := RehearsalConfig()
			cfg.RunID = "rehearsal-without-latency-window"
			if test.duration != 2*time.Hour {
				cfg.RunDuration = test.duration
			}
			start := time.Unix(1_960_022_500, 0).UTC()
			fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "rehearsal-no-window", Generation: 8}
			accounting := NewMetaCreateAccounting()
			controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
				Config: cfg, OutputDir: t.TempDir(), Observation: newProductionControllerObservation(cfg, start),
				Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
				Meta:      &productionControllerMeta{accounting: accounting}, MetaAccounting: accounting,
				Dataset: &productionControllerDataset{digest: hashReportValue("rehearsal-no-window-dataset")}, SlotAssignment: mustInitialLifecycleSlotAssignment(t),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer controller.Close()
			started := CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}
			if err := controller.Begin(context.Background(), started); err != nil {
				t.Fatal(err)
			}
			snapshots := productionControllerWorkerSnapshots(cfg, fence, 1, test.duration, WorkerPhaseRunning)
			if test.loss {
				snapshots[0].Messages.Losses = 1
			}
			decision, err := controller.Observe(context.Background(), CoordinatorEvidenceCut{Start: started, Kind: CoordinatorCutTerminal, At: start.Add(test.duration), Snapshots: snapshots})
			if err != nil || decision != test.want {
				t.Fatalf("elapsed-only rehearsal decision = %s/%v, want %s", decision, err, test.want)
			}
		})
	}
}

func productionControllerQualifiedSnapshots(cfg Config, fence WorkerFence, sequence uint64, uptime time.Duration, phase WorkerPhase, base uint64) []WorkerSnapshot {
	snapshots := productionControllerWorkerSnapshots(cfg, fence, sequence, uptime, phase)
	for index := range snapshots {
		count := base + uint64(index)
		histogram := newWorkerHistogramSnapshot()
		histogram.Count = count
		histogram.SumNanos = count * uint64(time.Millisecond)
		histogram.MaxNanos = uint64(time.Millisecond)
		histogram.Buckets[1] = count
		snapshots[index].Messages.FirstAttempts = count
		snapshots[index].Messages.Sent = count
		snapshots[index].HotSendackLatency = histogram
		snapshots[index].ColdFirstCreateSendackLatency = histogram
		snapshots[index].Sync.Latency = histogram
		snapshots[index].Sync.Thresholds.Count = count
		snapshots[index].SendPendingToWriteLatency = histogram
		snapshots[index].SendWriteToAckLatency = histogram
	}
	return snapshots
}

func seedProductionControllerLatencyWindow(t *testing.T, controller *ProductionEvidenceController, start, end time.Time) {
	t.Helper()
	counters := LatencyCountersForThresholds(controller.cfg.Thresholds.Latency)
	baseline := end.Add(-10 * time.Minute)
	for index := uint64(0); index <= 120; index++ {
		counters.Hot.Count = index * 10
		counters.Cold.Count = index * 10
		counters.Sync.Count = index * 10
		if err := controller.evaluator.Observe(VerdictObservation{At: baseline.Add(time.Duration(index) * 5 * time.Second), Latency: &counters}); err != nil {
			t.Fatal(err)
		}
	}
	if got := controller.evaluator.LatencyWindowEvidence(); got.Qualified != ([3]bool{true, true, true}) {
		t.Fatalf("qualified reducer fixture = %+v", got)
	}
}

func TestProductionEvidenceControllerDowngradesStaleRehearsalDuringFinalization(t *testing.T) {
	cfg := RehearsalConfig()
	cfg.RunID = "rehearsal-stale-final-cut"
	start := time.Unix(1_960_030_000, 0).UTC()
	end := start.Add(2 * time.Hour)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "rehearsal-stale-final", Generation: 9}
	accounting := NewMetaCreateAccounting()
	controller, err := NewProductionEvidenceController(ProductionEvidenceControllerOptions{
		Config: cfg, OutputDir: t.TempDir(), Observation: newProductionControllerObservation(cfg, start),
		Lifecycle: &productionControllerLifecycle{snapshot: LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}, done: make(chan struct{})},
		Meta:      &productionControllerMeta{accounting: accounting}, MetaAccounting: accounting,
		Dataset: &productionControllerDataset{digest: hashReportValue("rehearsal-stale-final-dataset")}, SlotAssignment: mustInitialLifecycleSlotAssignment(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	started := CoordinatorRunStart{Config: cfg, Fence: fence, StartedAt: start}
	if err := controller.Begin(context.Background(), started); err != nil {
		t.Fatal(err)
	}
	seedProductionControllerLatencyWindow(t, controller, start, end)
	controller.frozen = VerdictSnapshot{Outcome: VerdictRehearsalPass, Cause: VerdictCauseRehearsalCompleted, Terminal: true}
	controller.lastObservation = newProductionControllerObservation(cfg, start).Snapshot()
	snapshots := productionControllerQualifiedSnapshots(cfg, fence, 1, 2*time.Hour+6*time.Second, WorkerPhaseFinal, 1201)
	if err := controller.Finalize(context.Background(), CoordinatorFinalCut{Start: started, At: end.Add(6 * time.Second), FinalSnapshots: snapshots, Decision: CoordinatorCompleted}); err != nil {
		t.Fatalf("stale finalization lost the final report: %v", err)
	}
	report, err := ReadReport(filepath.Join(controller.OutputDir(), "final.json"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict.Outcome != VerdictHarnessInvalid || report.Verdict.Cause != VerdictCauseInvalidObservation || report.Latency.QualificationWindow.Status != ReportLatencyInsufficient || report.Latency.QualificationWindow.Qualified != ([3]bool{}) {
		t.Fatalf("stale terminal source became successful/fresh: %+v / %+v", report.Verdict, report.Latency.QualificationWindow)
	}
}
